package sso

import (
	"encoding/json"
	"testing"
)

func keycloakSCIM(t *testing.T, raw string) *SCIMConfig {
	t.Helper()
	c := &SCIMConfig{}
	if err := json.Unmarshal([]byte(raw), c); err != nil {
		t.Fatalf("unmarshal scim config: %v", err)
	}
	return c
}

func TestSCIMConfigDisabledAlwaysValidates(t *testing.T) {
	c := keycloakSCIM(t, `{"enabled":false,"provider":"okta","config":{}}`)
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled config should validate, got %v", err)
	}
}

func TestSCIMConfigOktaEntraNowValidate(t *testing.T) {
	// Okta and Entra are implemented: an empty config block is
	// a missing-fields error (not ErrProviderNotImplemented), and a complete
	// block validates.
	for _, p := range []string{"okta", "entra"} {
		c := keycloakSCIM(t, `{"enabled":true,"provider":"`+p+`","config":{}}`)
		err := c.Validate()
		if err == nil {
			t.Fatalf("provider %s: empty config must fail validation", p)
		}
		if err == ErrProviderNotImplemented {
			t.Fatalf("provider %s: must be implemented now, got ErrProviderNotImplemented", p)
		}
	}

	okta := keycloakSCIM(t, `{"enabled":true,"provider":"okta","config":{
		"issuerUrl": "https://dev.okta.com/oauth2/default",
		"clientId": "cid", "clientSecret": "cs", "apiToken": "tok"
	}}`)
	if err := okta.Validate(); err != nil {
		t.Fatalf("complete okta config must validate, got %v", err)
	}

	entra := keycloakSCIM(t, `{"enabled":true,"provider":"entra","config":{
		"tenantId": "11111111-2222-3333-4444-555555555555", "clientId": "cid"
	}}`)
	if err := entra.Validate(); err != nil {
		t.Fatalf("complete entra config must validate, got %v", err)
	}

	// tenantId "common" cannot pin an issuer and is rejected.
	common := keycloakSCIM(t, `{"enabled":true,"provider":"entra","config":{"tenantId": "common", "clientId": "cid"}}`)
	if err := common.Validate(); err == nil {
		t.Fatal("entra tenantId \"common\" must be rejected")
	}
}

func TestSCIMConfigKeycloakMissingFields(t *testing.T) {
	c := keycloakSCIM(t, `{"enabled":true,"provider":"keycloak","config":{}}`)
	if err := c.Validate(); err == nil {
		t.Fatal("expected validation error for empty keycloak config")
	}
}

func TestKeycloakConfigDefaultsAndURLs(t *testing.T) {
	t.Setenv("TEST_KC_SECRET", "s3cr3t")
	c := keycloakSCIM(t, `{
		"enabled": true,
		"provider": "keycloak",
		"config": {
			"serverUrl": "https://kc.example.com/",
			"realm": "loopback-prod",
			"clientId": "loopback",
			"clientSecret": "env.TEST_KC_SECRET"
		}
	}`)
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
	kc, err := c.Keycloak()
	if err != nil {
		t.Fatalf("Keycloak(): %v", err)
	}
	if kc.ClientSecret != "s3cr3t" {
		t.Fatalf("env secret not resolved, got %q", kc.ClientSecret)
	}
	if kc.ServerURL != "https://kc.example.com" {
		t.Fatalf("trailing slash not trimmed: %q", kc.ServerURL)
	}
	if kc.UserIDField != "sub" || kc.TeamIDsField != "groups" || kc.RolesField != "realm_access.roles" {
		t.Fatalf("defaults not applied: %+v", kc)
	}
	if got, want := kc.IssuerURL(), "https://kc.example.com/realms/loopback-prod"; got != want {
		t.Fatalf("issuer = %q want %q", got, want)
	}
	if got, want := kc.JWKSURL(), "https://kc.example.com/realms/loopback-prod/protocol/openid-connect/certs"; got != want {
		t.Fatalf("jwks = %q want %q", got, want)
	}
	if got, want := kc.AdminUsersURL(), "https://kc.example.com/admin/realms/loopback-prod/users"; got != want {
		t.Fatalf("admin users = %q want %q", got, want)
	}
	if got, want := kc.ExpectedAudience(), "loopback"; got != want {
		t.Fatalf("expected audience = %q want %q", got, want)
	}
}
