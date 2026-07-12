package handlers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

const (
	jwtMWTestIssuer = "https://idp.example.com"
	jwtMWTestKid    = "mw-kid-1"
)

// jwtMWJWKSServer serves a JWKS for the given RSA key (local copy of the
// framework/sso test helper — the jwk types there are unexported).
func jwtMWJWKSServer(t *testing.T, kid string, key *rsa.PrivateKey) *httptest.Server {
	t.Helper()
	eBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(eBytes, uint64(key.PublicKey.E))
	i := 0
	for i < len(eBytes)-1 && eBytes[i] == 0 {
		i++
	}
	body := map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(eBytes[i:]),
	}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// signJWTMWToken mints an RS256 token with the test kid.
func signJWTMWToken(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = jwtMWTestKid
	s, err := tok.SignedString(key)
	require.NoError(t, err)
	return s
}

func jwtMWClaims(extra jwt.MapClaims) jwt.MapClaims {
	claims := jwt.MapClaims{
		"iss": jwtMWTestIssuer,
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	return claims
}

// fakeVKResolver returns scripted virtual keys and counts lookups.
type fakeVKResolver struct {
	mu    sync.Mutex
	vks   map[string]*configstoreTables.TableVirtualKey
	calls int
}

func (f *fakeVKResolver) GetVirtualKey(_ context.Context, id string) (*configstoreTables.TableVirtualKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	vk, ok := f.vks[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return vk, nil
}

func (f *fakeVKResolver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// jwtMWSetup builds a middleware configured for the test issuer with one
// mapping rule tenant=acme → vk-acme (value "sk-bf-acme-value").
func jwtMWSetup(t *testing.T, rejectInvalid bool) (*JWTVKAuthMiddleware, *rsa.PrivateKey, *fakeVKResolver) {
	t.Helper()
	SetLogger(&mockLogger{})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks := jwtMWJWKSServer(t, jwtMWTestKid, key)

	resolver := &fakeVKResolver{vks: map[string]*configstoreTables.TableVirtualKey{
		"vk-acme": {ID: "vk-acme", Value: "sk-bf-acme-value"},
	}}
	m := NewJWTVKAuthMiddleware(resolver)
	m.SetConfigs([]configstoreTables.TableJWTAuthConfig{{
		ID: "jwt-1", Enabled: true, Issuer: jwtMWTestIssuer, JWKSURL: jwks.URL,
		RejectInvalid: rejectInvalid,
		ClaimMappings: []configstoreTables.JWTAuthClaimMapping{
			{Claim: "tenant", Value: "acme", VirtualKeyID: "vk-acme"},
		},
	}})
	return m, key, resolver
}

// runJWTMW drives one request through the middleware, returning the ctx and
// whether next was reached.
func runJWTMW(t *testing.T, m *JWTVKAuthMiddleware, authorization string, presetVK string) (*fasthttp.RequestCtx, bool) {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	var req fasthttp.Request
	req.Header.SetMethod("POST")
	req.SetRequestURI("/v1/chat/completions")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if presetVK != "" {
		req.Header.Set("x-bf-vk", presetVK)
	}
	ctx.Init(&req, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, nil)

	nextCalled := false
	handler := m.Middleware()(func(c *fasthttp.RequestCtx) { nextCalled = true })
	handler(ctx)
	return ctx, nextCalled
}

func vkHeader(ctx *fasthttp.RequestCtx) string {
	return string(ctx.Request.Header.Peek("x-bf-vk"))
}

func TestJWTMW_UnconfiguredIsPassthrough(t *testing.T) {
	SetLogger(&mockLogger{})
	m := NewJWTVKAuthMiddleware(&fakeVKResolver{})
	// No SetConfigs call: snapshot nil.
	ctx, nextCalled := runJWTMW(t, m, "Bearer whatever.looks.jwtish", "")
	assert.True(t, nextCalled)
	assert.Empty(t, vkHeader(ctx), "unconfigured middleware must not touch headers")

	// Zero enabled rows also stores a nil snapshot.
	m.SetConfigs([]configstoreTables.TableJWTAuthConfig{{ID: "x", Enabled: false, Issuer: "i", JWKSURL: "j"}})
	ctx, nextCalled = runJWTMW(t, m, "Bearer a.b.c", "")
	assert.True(t, nextCalled)
	assert.Empty(t, vkHeader(ctx))
}

func TestJWTMW_ValidJWTInjectsVK(t *testing.T) {
	m, key, _ := jwtMWSetup(t, false)
	token := signJWTMWToken(t, key, jwtMWClaims(jwt.MapClaims{"tenant": "acme"}))

	ctx, nextCalled := runJWTMW(t, m, "Bearer "+token, "")
	assert.True(t, nextCalled)
	assert.Equal(t, "sk-bf-acme-value", vkHeader(ctx), "the VK plaintext value must be injected, not the ID")
}

func TestJWTMW_SkipsNonJWTTraffic(t *testing.T) {
	m, _, resolver := jwtMWSetup(t, false)

	cases := []struct {
		name string
		auth string
	}{
		{"no authorization", ""},
		{"sk-bf virtual key bearer", "Bearer sk-bf-existing-key"},
		{"opaque session token", "Bearer some-opaque-session-token"},
		{"two segments only", "Bearer aaaa.bbbb"},
		{"basic auth", "Basic dXNlcjpwYXNz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, nextCalled := runJWTMW(t, m, tc.auth, "")
			assert.True(t, nextCalled)
			assert.Empty(t, vkHeader(ctx))
		})
	}
	assert.Zero(t, resolver.callCount(), "non-JWT traffic must never hit the VK resolver")
}

func TestJWTMW_CallerSuppliedVKWins(t *testing.T) {
	m, key, resolver := jwtMWSetup(t, false)
	token := signJWTMWToken(t, key, jwtMWClaims(jwt.MapClaims{"tenant": "acme"}))

	ctx, nextCalled := runJWTMW(t, m, "Bearer "+token, "sk-bf-caller-chosen")
	assert.True(t, nextCalled)
	assert.Equal(t, "sk-bf-caller-chosen", vkHeader(ctx), "a pre-existing x-bf-vk must never be overwritten")
	assert.Zero(t, resolver.callCount())
}

func TestJWTMW_UnknownIssuerFallsThrough(t *testing.T) {
	m, key, _ := jwtMWSetup(t, true) // even with reject_invalid on the configured issuer
	token := signJWTMWToken(t, key, jwtMWClaims(jwt.MapClaims{"iss": "https://other-idp.example.com"}))

	ctx, nextCalled := runJWTMW(t, m, "Bearer "+token, "")
	assert.True(t, nextCalled, "tokens from unconfigured issuers are not ours to reject")
	assert.Empty(t, vkHeader(ctx))
}

func TestJWTMW_InvalidJWTFallsThroughByDefault(t *testing.T) {
	m, _, _ := jwtMWSetup(t, false)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	badToken := signJWTMWToken(t, otherKey, jwtMWClaims(jwt.MapClaims{"tenant": "acme"}))

	ctx, nextCalled := runJWTMW(t, m, "Bearer "+badToken, "")
	assert.True(t, nextCalled, "default failure mode is fall-through")
	assert.Empty(t, vkHeader(ctx))
	assert.NotEqual(t, 401, ctx.Response.StatusCode())
}

func TestJWTMW_InvalidJWTRejectedWhenConfigured(t *testing.T) {
	m, _, _ := jwtMWSetup(t, true)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	badToken := signJWTMWToken(t, otherKey, jwtMWClaims(jwt.MapClaims{"tenant": "acme"}))

	ctx, nextCalled := runJWTMW(t, m, "Bearer "+badToken, "")
	assert.False(t, nextCalled, "reject_invalid must short-circuit")
	assert.Equal(t, 401, ctx.Response.StatusCode())
}

func TestJWTMW_NoMappingMatchFallsThrough(t *testing.T) {
	m, key, _ := jwtMWSetup(t, false)
	token := signJWTMWToken(t, key, jwtMWClaims(jwt.MapClaims{"tenant": "globex"}))

	ctx, nextCalled := runJWTMW(t, m, "Bearer "+token, "")
	assert.True(t, nextCalled)
	assert.Empty(t, vkHeader(ctx), "no rule match and no default VK means no attribution")
}

func TestJWTMW_UnresolvableVKFallsThrough(t *testing.T) {
	m, key, resolver := jwtMWSetup(t, true) // even with reject_invalid: the token IS valid
	delete(resolver.vks, "vk-acme")
	token := signJWTMWToken(t, key, jwtMWClaims(jwt.MapClaims{"tenant": "acme"}))

	ctx, nextCalled := runJWTMW(t, m, "Bearer "+token, "")
	assert.True(t, nextCalled, "a valid token with a broken VK mapping is a config problem, not an auth failure")
	assert.Empty(t, vkHeader(ctx))
}

func TestJWTMW_VKValueCacheTTL(t *testing.T) {
	m, key, resolver := jwtMWSetup(t, false)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	m.now = func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	token := signJWTMWToken(t, key, jwtMWClaims(jwt.MapClaims{"tenant": "acme"}))

	// First request resolves; second hits the cache.
	runJWTMW(t, m, "Bearer "+token, "")
	runJWTMW(t, m, "Bearer "+token, "")
	assert.Equal(t, 1, resolver.callCount(), "second lookup within the TTL must hit the cache")

	// Past the TTL the value is re-resolved.
	nowMu.Lock()
	now = now.Add(jwtVKValueTTL + time.Second)
	nowMu.Unlock()
	runJWTMW(t, m, "Bearer "+token, "")
	assert.Equal(t, 2, resolver.callCount())

	// SetConfigs clears the cache immediately (mapping changes must not serve
	// stale values for up to a TTL).
	m.SetConfigs([]configstoreTables.TableJWTAuthConfig{})
	ctx, nextCalled := runJWTMW(t, m, "Bearer "+token, "")
	assert.True(t, nextCalled)
	assert.Empty(t, vkHeader(ctx), "clearing configs must disable injection")
}

func TestJWTMW_InactiveVKNotInjected(t *testing.T) {
	m, key, resolver := jwtMWSetup(t, false)
	inactive := false
	resolver.vks["vk-acme"].IsActive = &inactive
	token := signJWTMWToken(t, key, jwtMWClaims(jwt.MapClaims{"tenant": "acme"}))

	ctx, nextCalled := runJWTMW(t, m, "Bearer "+token, "")
	assert.True(t, nextCalled)
	assert.Empty(t, vkHeader(ctx), "inactive virtual keys must not be injected")
}
