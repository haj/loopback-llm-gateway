package sso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func oktaSCIMConfig(t *testing.T, issuer, apiToken string) *SCIMConfig {
	t.Helper()
	raw := fmt.Sprintf(`{"enabled":true,"provider":"okta","config":{
		"issuerUrl": %q, "clientId": "okta-client", "clientSecret": "cs", "apiToken": %q, "audience": "api://gateway"
	}}`, issuer, apiToken)
	c := &SCIMConfig{}
	if err := json.Unmarshal([]byte(raw), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestOktaJWKSURLDerivation(t *testing.T) {
	cases := []struct {
		issuer   string
		explicit string
		wantJWKS string
	}{
		// Custom authorization server auto-detected from /oauth2/ path.
		{"https://dev.okta.com/oauth2/default", "", "https://dev.okta.com/oauth2/default/v1/keys"},
		// Org authorization server (bare domain).
		{"https://dev.okta.com", "", "https://dev.okta.com/oauth2/v1/keys"},
		// Explicit override beats the heuristic.
		{"https://dev.okta.com/oauth2/default", "org", "https://dev.okta.com/oauth2/default/oauth2/v1/keys"},
	}
	for _, tc := range cases {
		scim := oktaSCIMConfig(t, tc.issuer, "tok")
		oc, err := scim.Okta()
		if err != nil {
			t.Fatal(err)
		}
		oc.AuthServerType = tc.explicit
		if got := oc.JWKSURL(); got != tc.wantJWKS {
			t.Fatalf("issuer %s (type %q): want %s, got %s", tc.issuer, tc.explicit, tc.wantJWKS, got)
		}
	}
}

func TestOktaValidator_AcceptAndReject(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "okta-kid"
	jwks := jwksTestServer(t, kid, key)

	const issuer = "https://dev.okta.com/oauth2/default"
	scim := oktaSCIMConfig(t, issuer, "tok")
	v, err := NewValidator(scim, jwks.Client())
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	// Point the JWKS cache at the mock (the derived URL is unreachable here).
	v.jwks = newJWKSCache(jwks.URL, jwks.Client(), time.Now)

	sign := func(claims jwt.MapClaims) string { return signToken(t, key, kid, claims) }
	base := jwt.MapClaims{
		"iss": issuer, "sub": "00u123", "aud": "api://gateway",
		"exp": time.Now().Add(time.Hour).Unix(),
		"preferred_username": "bjensen@example.com",
		"groups":             []string{"eng"},
	}

	identity, err := v.Validate(context.Background(), sign(base))
	if err != nil {
		t.Fatalf("valid okta token rejected: %v", err)
	}
	if identity.Provider != ProviderOkta || identity.Subject != "00u123" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if len(identity.Groups) != 1 || identity.Groups[0] != "eng" {
		t.Fatalf("groups claim not projected: %+v", identity.Groups)
	}

	bad := jwt.MapClaims{}
	for k, v := range base {
		bad[k] = v
	}
	bad["iss"] = "https://evil.okta.com"
	if _, err := v.Validate(context.Background(), sign(bad)); err == nil {
		t.Fatal("wrong issuer must be rejected")
	}
	bad["iss"] = issuer
	bad["aud"] = "someone-else"
	if _, err := v.Validate(context.Background(), sign(bad)); err == nil {
		t.Fatal("wrong audience must be rejected")
	}
}

// mockOktaAdmin serves paginated /api/v1/users and /api/v1/groups with Link
// rel="next" headers, asserting the SSWS token.
func mockOktaAdmin(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "SSWS test-api-token" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			// Page 1 with a next link.
			w.Header().Set("Link", fmt.Sprintf(`<%s/api/v1/users?after=p2&limit=200>; rel="next"`, srv.URL))
			fmt.Fprint(w, `[{"id":"u1","status":"ACTIVE","profile":{"login":"one@example.com","email":"one@example.com","firstName":"One","lastName":"User"}}]`)
			return
		}
		fmt.Fprint(w, `[{"id":"u2","status":"DEPROVISIONED","profile":{"login":"two@example.com","email":"two@example.com"}}]`)
	})
	mux.HandleFunc("/api/v1/groups", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"g1","profile":{"name":"Engineering"}}]`)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestOktaDirectory_PaginatesAndProjects(t *testing.T) {
	admin := mockOktaAdmin(t)
	scim := oktaSCIMConfig(t, admin.URL+"/oauth2/default", "test-api-token")
	oc, err := scim.Okta()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := oc.Directory(admin.Client())
	if err != nil {
		t.Fatal(err)
	}

	users, err := dir.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected both pages (2 users), got %d", len(users))
	}
	if users[0].ExternalID != "u1" || !users[0].Active || users[0].DisplayName != "One User" {
		t.Fatalf("unexpected first user: %+v", users[0])
	}
	if users[1].ExternalID != "u2" || users[1].Active {
		t.Fatalf("DEPROVISIONED user must be inactive: %+v", users[1])
	}

	groups, err := dir.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 1 || groups[0].DisplayName != "Engineering" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
}

func TestOktaDirectory_RequiresAPIToken(t *testing.T) {
	scim := oktaSCIMConfig(t, "https://dev.okta.com", "")
	oc, err := scim.Okta()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oc.Directory(nil); err == nil {
		t.Fatal("directory without apiToken must error")
	}
}
