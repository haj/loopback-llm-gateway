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

const entraTestTenant = "11111111-2222-3333-4444-555555555555"

func entraSCIMConfig(t *testing.T, extra string) *SCIMConfig {
	t.Helper()
	raw := fmt.Sprintf(`{"enabled":true,"provider":"entra","config":{
		"tenantId": %q, "clientId": "entra-client", "clientSecret": "cs"%s
	}}`, entraTestTenant, extra)
	c := &SCIMConfig{}
	if err := json.Unmarshal([]byte(raw), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEntraIssuerAndJWKSDerivation(t *testing.T) {
	cases := []struct {
		extra      string
		wantIssuer string
		wantJWKS   string
		wantAud    string
	}{
		{
			"",
			"https://login.microsoftonline.com/" + entraTestTenant + "/v2.0",
			"https://login.microsoftonline.com/" + entraTestTenant + "/discovery/v2.0/keys",
			"entra-client", // v2.0 default: the client ID
		},
		{
			`, "cloud": "gcc-high"`,
			"https://login.microsoftonline.us/" + entraTestTenant + "/v2.0",
			"https://login.microsoftonline.us/" + entraTestTenant + "/discovery/v2.0/keys",
			"entra-client",
		},
		{
			`, "appIdUri": "api://entra-client"`,
			"https://login.microsoftonline.com/" + entraTestTenant + "/v2.0",
			"https://login.microsoftonline.com/" + entraTestTenant + "/discovery/v2.0/keys",
			"api://entra-client", // appIdUri beats the clientId default
		},
	}
	for _, tc := range cases {
		ec, err := entraSCIMConfig(t, tc.extra).Entra()
		if err != nil {
			t.Fatal(err)
		}
		if got := ec.IssuerURL(); got != tc.wantIssuer {
			t.Fatalf("issuer: want %s, got %s", tc.wantIssuer, got)
		}
		if got := ec.JWKSURL(); got != tc.wantJWKS {
			t.Fatalf("jwks: want %s, got %s", tc.wantJWKS, got)
		}
		if got := ec.ExpectedAudience(); got != tc.wantAud {
			t.Fatalf("audience: want %s, got %s", tc.wantAud, got)
		}
	}
}

func TestEntraValidator_AcceptAndReject(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "entra-kid"
	jwks := jwksTestServer(t, kid, key)

	v, err := NewValidator(entraSCIMConfig(t, ""), jwks.Client())
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	v.jwks = newJWKSCache(jwks.URL, jwks.Client(), time.Now)

	issuer := "https://login.microsoftonline.com/" + entraTestTenant + "/v2.0"
	base := jwt.MapClaims{
		"iss": issuer, "oid": "user-object-id", "aud": "entra-client",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"name": "Barbara Jensen",
	}
	sign := func(claims jwt.MapClaims) string { return signToken(t, key, kid, claims) }

	identity, err := v.Validate(context.Background(), sign(base))
	if err != nil {
		t.Fatalf("valid entra token rejected: %v", err)
	}
	if identity.Provider != ProviderEntra || identity.Subject != "user-object-id" {
		t.Fatalf("oid claim must drive the subject, got %+v", identity)
	}

	bad := jwt.MapClaims{}
	for k, v := range base {
		bad[k] = v
	}
	bad["iss"] = "https://login.microsoftonline.com/other-tenant/v2.0"
	if _, err := v.Validate(context.Background(), sign(bad)); err == nil {
		t.Fatal("wrong tenant issuer must be rejected")
	}
	bad["iss"] = issuer
	bad["exp"] = time.Now().Add(-time.Hour).Unix()
	if _, err := v.Validate(context.Background(), sign(bad)); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

// mockGraph serves the token endpoint and paginated users/groups with
// @odata.nextLink.
func mockGraph(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_secret") != "cs" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token": "graph-token"}`)
	})
	mux.HandleFunc("/v1.0/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer graph-token" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("$skiptoken") == "" {
			fmt.Fprintf(w, `{"value": [{"id":"aad-1","userPrincipalName":"one@corp.example","mail":"one@corp.example","displayName":"One","accountEnabled":true}],
				"@odata.nextLink": "%s/v1.0/users?$skiptoken=p2"}`, srv.URL)
			return
		}
		fmt.Fprint(w, `{"value": [{"id":"aad-2","userPrincipalName":"two@corp.example","displayName":"Two","accountEnabled":false}]}`)
	})
	mux.HandleFunc("/v1.0/groups", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"value": [{"id":"g-1","displayName":"Platform"}]}`)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestEntraDirectory_PaginatesAndProjects(t *testing.T) {
	graph := mockGraph(t)
	ec, err := entraSCIMConfig(t, "").Entra()
	if err != nil {
		t.Fatal(err)
	}
	dirIface, err := ec.Directory(graph.Client())
	if err != nil {
		t.Fatal(err)
	}
	// Point token + Graph at the mock.
	dir := dirIface.(*entraDirectory)
	dir.tokenURL = graph.URL + "/token"
	dir.graphBase = graph.URL

	users, err := dir.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected both @odata.nextLink pages (2 users), got %d", len(users))
	}
	if users[0].ExternalID != "aad-1" || !users[0].Active || users[0].Email != "one@corp.example" {
		t.Fatalf("unexpected first user: %+v", users[0])
	}
	if users[1].Active {
		t.Fatalf("accountEnabled=false must project inactive: %+v", users[1])
	}
	if users[1].Email != "two@corp.example" {
		t.Fatalf("missing mail must fall back to userPrincipalName: %+v", users[1])
	}

	groups, err := dir.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 1 || groups[0].DisplayName != "Platform" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
}

func TestEntraDirectory_RequiresClientSecret(t *testing.T) {
	raw := fmt.Sprintf(`{"enabled":true,"provider":"entra","config":{"tenantId": %q, "clientId": "cid"}}`, entraTestTenant)
	c := &SCIMConfig{}
	if err := json.Unmarshal([]byte(raw), c); err != nil {
		t.Fatal(err)
	}
	ec, err := c.Entra()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ec.Directory(nil); err == nil {
		t.Fatal("directory without clientSecret must error")
	}
}
