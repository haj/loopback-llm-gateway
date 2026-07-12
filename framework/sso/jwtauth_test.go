package sso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	jwtAuthTestIssuer = "https://idp.example.com"
	jwtAuthTestKid    = "jwt-auth-kid-1"
)

// newJWTAuthTestSetup generates a signing key, serves its JWKS, and returns a
// validator for cfg (issuer/JWKS pre-filled unless already set).
func newJWTAuthTestSetup(t *testing.T, cfg JWTAuthConfig) (*JWTAuthValidator, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := jwksTestServer(t, jwtAuthTestKid, key)
	cfg.Enabled = true
	if cfg.Issuer == "" {
		cfg.Issuer = jwtAuthTestIssuer
	}
	if cfg.JWKSURL == "" {
		cfg.JWKSURL = srv.URL
	}
	v, err := NewJWTAuthValidator(cfg, srv.Client())
	if err != nil {
		t.Fatalf("NewJWTAuthValidator: %v", err)
	}
	return v, key
}

// jwtAuthClaims returns a baseline valid claim set, merged with extra.
func jwtAuthClaims(extra jwt.MapClaims) jwt.MapClaims {
	claims := jwt.MapClaims{
		"iss": jwtAuthTestIssuer,
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	return claims
}

func TestJWTAuth_ValidTokenVerifies(t *testing.T) {
	v, key := newJWTAuthTestSetup(t, JWTAuthConfig{})
	token := signToken(t, key, jwtAuthTestKid, jwtAuthClaims(jwt.MapClaims{"tenant": "acme"}))

	claims, err := v.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
	if got, _ := claims["tenant"].(string); got != "acme" {
		t.Fatalf("expected tenant claim to round-trip, got %q", got)
	}
}

func TestJWTAuth_RejectsBadTokens(t *testing.T) {
	v, key := newJWTAuthTestSetup(t, JWTAuthConfig{Audience: "gateway"})
	ctx := context.Background()

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		token string
	}{
		{"wrong issuer", signToken(t, key, jwtAuthTestKid, jwtAuthClaims(jwt.MapClaims{"iss": "https://evil.example.com", "aud": "gateway"}))},
		{"missing audience", signToken(t, key, jwtAuthTestKid, jwtAuthClaims(nil))},
		{"wrong audience", signToken(t, key, jwtAuthTestKid, jwtAuthClaims(jwt.MapClaims{"aud": "someone-else"}))},
		{"expired", signToken(t, key, jwtAuthTestKid, jwtAuthClaims(jwt.MapClaims{"aud": "gateway", "exp": time.Now().Add(-time.Hour).Unix()}))},
		{"no expiry", func() string {
			c := jwtAuthClaims(jwt.MapClaims{"aud": "gateway"})
			delete(c, "exp")
			return signToken(t, key, jwtAuthTestKid, c)
		}()},
		{"bad signature", signToken(t, otherKey, jwtAuthTestKid, jwtAuthClaims(jwt.MapClaims{"aud": "gateway"}))},
		{"unknown kid", signToken(t, key, "unknown-kid", jwtAuthClaims(jwt.MapClaims{"aud": "gateway"}))},
		{"garbage", "not.a.jwt"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := v.Validate(ctx, tc.token); err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
		})
	}

	// The audience-carrying valid token still passes (sanity check that the
	// cases above fail for the right reason).
	good := signToken(t, key, jwtAuthTestKid, jwtAuthClaims(jwt.MapClaims{"aud": "gateway"}))
	if _, err := v.Validate(ctx, good); err != nil {
		t.Fatalf("expected valid audience token to pass, got %v", err)
	}
}

func TestJWTAuth_MapToVirtualKeyID(t *testing.T) {
	mappings := []JWTClaimVKMapping{
		{Claim: "tenant", Value: "acme", VirtualKeyID: "vk-acme"},
		{Claim: "resource_access.gateway.roles", Value: "power-user", VirtualKeyID: "vk-power"},
		{Claim: "groups", Value: "*", VirtualKeyID: "vk-any-group"},
		{Claim: "tenant", Value: "acme", VirtualKeyID: "vk-shadowed"}, // never reached: first match wins
	}
	v, _ := newJWTAuthTestSetup(t, JWTAuthConfig{
		ClaimMappings:       mappings,
		DefaultVirtualKeyID: "vk-default",
	})

	cases := []struct {
		name   string
		claims jwt.MapClaims
		want   string
	}{
		{"exact string match", jwt.MapClaims{"tenant": "acme"}, "vk-acme"},
		{"first match wins over later rules", jwt.MapClaims{"tenant": "acme", "groups": []any{"g1"}}, "vk-acme"},
		{"dot-path array match", jwt.MapClaims{"resource_access": map[string]any{"gateway": map[string]any{"roles": []any{"viewer", "power-user"}}}}, "vk-power"},
		{"wildcard presence match", jwt.MapClaims{"groups": []any{"team-x"}}, "vk-any-group"},
		{"no rule matches falls to default", jwt.MapClaims{"tenant": "globex"}, "vk-default"},
		{"missing claims fall to default", jwt.MapClaims{}, "vk-default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := v.MapToVirtualKeyID(tc.claims); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestJWTAuth_NoDefaultReturnsEmpty(t *testing.T) {
	v, _ := newJWTAuthTestSetup(t, JWTAuthConfig{
		ClaimMappings: []JWTClaimVKMapping{{Claim: "tenant", Value: "acme", VirtualKeyID: "vk-acme"}},
	})
	if got := v.MapToVirtualKeyID(jwt.MapClaims{"tenant": "globex"}); got != "" {
		t.Fatalf("expected empty VK ID with no default, got %q", got)
	}
}

func TestJWTAuth_DisabledConfigYieldsNilValidator(t *testing.T) {
	v, err := NewJWTAuthValidator(JWTAuthConfig{Enabled: false, Issuer: "x", JWKSURL: "y"}, nil)
	if err != nil {
		t.Fatalf("disabled config must not error, got %v", err)
	}
	if v != nil {
		t.Fatal("disabled config must yield a nil validator")
	}
}

func TestJWTAuth_EnabledConfigValidation(t *testing.T) {
	if _, err := NewJWTAuthValidator(JWTAuthConfig{Enabled: true, JWKSURL: "https://x/jwks"}, nil); err == nil {
		t.Fatal("missing issuer must error")
	}
	if _, err := NewJWTAuthValidator(JWTAuthConfig{Enabled: true, Issuer: "https://x"}, nil); err == nil {
		t.Fatal("missing jwks_url must error")
	}
}
