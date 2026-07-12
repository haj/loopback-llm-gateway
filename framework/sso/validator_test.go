package sso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// jwksTestServer serves a JWKS containing the public half of key, under the
// given kid, and returns the server.
func jwksTestServer(t *testing.T, kid string, key *rsa.PrivateKey) *httptest.Server {
	t.Helper()
	eBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(eBytes, uint64(key.PublicKey.E))
	// trim leading zero bytes
	i := 0
	for i < len(eBytes)-1 && eBytes[i] == 0 {
		i++
	}
	set := jwkSet{Keys: []jwk{{
		Kty: "RSA",
		Kid: kid,
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(eBytes[i:]),
	}}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newTestValidator builds a validator whose IssuerURL() equals
// "{serverURL}/realms/{realm}". All tests use issuer
// "https://kc.example.com/realms/loopback".
func newTestValidator(t *testing.T, serverURL, realm, jwksURL, audience string) *Validator {
	t.Helper()
	return &Validator{
		oidc: &KeycloakConfig{
			ServerURL:    serverURL,
			Realm:        realm,
			ClientID:     audience,
			Audience:     audience,
			UserIDField:  "sub",
			TeamIDsField: "groups",
			RolesField:   "realm_access.roles",
		},
		jwks: newJWKSCache(jwksURL, &http.Client{Timeout: 5 * time.Second}, time.Now),
		now:  time.Now,
	}
}

func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func TestValidatorAcceptsValidToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "kid-1"
	const issuer = "https://kc.example.com/realms/loopback"
	srv := jwksTestServer(t, kid, key)
	v := newTestValidator(t, "https://kc.example.com", "loopback", srv.URL, "loopback")

	raw := signToken(t, key, kid, jwt.MapClaims{
		"iss":                issuer,
		"aud":                "loopback",
		"sub":                "user-123",
		"email":              "alice@example.com",
		"preferred_username": "alice",
		"name":               "Alice Example",
		"exp":                time.Now().Add(time.Hour).Unix(),
		"groups":             []any{"engineering", "admins"},
		"realm_access":       map[string]any{"roles": []any{"gateway-admin"}},
	})

	id, err := v.Validate(context.Background(), raw)
	if err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
	if id.Subject != "user-123" || id.Email != "alice@example.com" || id.UserName != "alice" {
		t.Fatalf("bad identity: %+v", id)
	}
	if len(id.Groups) != 2 || id.Groups[0] != "engineering" {
		t.Fatalf("groups not extracted: %+v", id.Groups)
	}
	if len(id.Roles) != 1 || id.Roles[0] != "gateway-admin" {
		t.Fatalf("roles not extracted (dot path): %+v", id.Roles)
	}
}

func TestValidatorRejectsExpired(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	const kid, issuer = "kid-1", "https://kc.example.com/realms/loopback"
	srv := jwksTestServer(t, kid, key)
	v := newTestValidator(t, "https://kc.example.com", "loopback", srv.URL, "loopback")
	raw := signToken(t, key, kid, jwt.MapClaims{
		"iss": issuer, "aud": "loopback", "sub": "u", "exp": time.Now().Add(-time.Hour).Unix(),
	})
	if _, err := v.Validate(context.Background(), raw); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestValidatorRejectsWrongIssuer(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	const kid, issuer = "kid-1", "https://kc.example.com/realms/loopback"
	srv := jwksTestServer(t, kid, key)
	v := newTestValidator(t, "https://kc.example.com", "loopback", srv.URL, "loopback")
	raw := signToken(t, key, kid, jwt.MapClaims{
		"iss": "https://evil.example.com/realms/loopback", "aud": "loopback", "sub": "u",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := v.Validate(context.Background(), raw); err == nil {
		t.Fatal("expected wrong-issuer token to be rejected")
	}
}

func TestValidatorRejectsBadSignature(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	const kid, issuer = "kid-1", "https://kc.example.com/realms/loopback"
	srv := jwksTestServer(t, kid, key) // publishes key's public part
	v := newTestValidator(t, "https://kc.example.com", "loopback", srv.URL, "loopback")
	// Sign with `other` (whose public key is NOT in the JWKS) under the same kid.
	raw := signToken(t, other, kid, jwt.MapClaims{
		"iss": issuer, "aud": "loopback", "sub": "u", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := v.Validate(context.Background(), raw); err == nil {
		t.Fatal("expected bad-signature token to be rejected")
	}
}

func TestValidatorRejectsWrongAudience(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	const kid, issuer = "kid-1", "https://kc.example.com/realms/loopback"
	srv := jwksTestServer(t, kid, key)
	v := newTestValidator(t, "https://kc.example.com", "loopback", srv.URL, "loopback")
	raw := signToken(t, key, kid, jwt.MapClaims{
		"iss": issuer, "aud": "some-other-client", "sub": "u", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := v.Validate(context.Background(), raw); err == nil {
		t.Fatal("expected wrong-audience token to be rejected")
	}
}
