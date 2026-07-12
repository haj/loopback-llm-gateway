package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newJWTAuthHandler builds a handler over a real SQLite configstore with the
// live middleware attached, plus a seeded virtual key for FK validation.
func newJWTAuthHandler(t *testing.T) (*JWTAuthHandler, *JWTVKAuthMiddleware, configstore.ConfigStore) {
	t.Helper()
	store := newAuditTestStore(t)
	require.NoError(t, store.CreateVirtualKey(context.Background(), &configstoreTables.TableVirtualKey{
		ID:        "vk-acme",
		Name:      "acme-key",
		Value:     "sk-bf-acme-value",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}))
	middleware := NewJWTVKAuthMiddleware(store)
	h, err := NewJWTAuthHandler(store, middleware)
	require.NoError(t, err)
	return h, middleware, store
}

func TestJWTAuthHandler_CreateValidation(t *testing.T) {
	h, _, _ := newJWTAuthHandler(t)
	cases := []struct {
		name string
		body string
	}{
		{"malformed json", `{nope`},
		{"missing issuer", `{"jwks_url": "https://idp/jwks"}`},
		{"missing jwks", `{"issuer": "https://idp"}`},
		{"non-http jwks", `{"issuer": "https://idp", "jwks_url": "ftp://idp/jwks"}`},
		{"mapping without claim", `{"issuer": "https://idp", "jwks_url": "https://idp/jwks", "claim_mappings": [{"value": "x", "virtual_key_id": "vk-acme"}]}`},
		{"mapping without value", `{"issuer": "https://idp", "jwks_url": "https://idp/jwks", "claim_mappings": [{"claim": "tenant", "virtual_key_id": "vk-acme"}]}`},
		{"mapping without vk", `{"issuer": "https://idp", "jwks_url": "https://idp/jwks", "claim_mappings": [{"claim": "tenant", "value": "x"}]}`},
		{"unknown vk in mapping", `{"issuer": "https://idp", "jwks_url": "https://idp/jwks", "claim_mappings": [{"claim": "tenant", "value": "x", "virtual_key_id": "vk-missing"}]}`},
		{"unknown default vk", `{"issuer": "https://idp", "jwks_url": "https://idp/jwks", "default_virtual_key_id": "vk-missing"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := auditRequestCtx("POST", "/api/governance/jwt-auth", []byte(tc.body))
			h.createConfig(ctx)
			assert.Equal(t, 400, ctx.Response.StatusCode())
		})
	}
}

func TestJWTAuthHandler_CRUDAndLiveApply(t *testing.T) {
	h, middleware, store := newJWTAuthHandler(t)

	// Create an enabled issuer config.
	body := `{
		"name": "corp-idp",
		"issuer": "https://idp.example.com",
		"jwks_url": "https://idp.example.com/jwks",
		"audience": "gateway",
		"claim_mappings": [{"claim": "tenant", "value": "acme", "virtual_key_id": "vk-acme"}],
		"default_virtual_key_id": "vk-acme"
	}`
	ctx := auditRequestCtx("POST", "/api/governance/jwt-auth", []byte(body))
	h.createConfig(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode(), "body: %s", ctx.Response.Body())

	// VK values never appear in responses (only IDs).
	assert.NotContains(t, string(ctx.Response.Body()), "sk-bf-acme-value")

	var created struct {
		Config struct {
			ID string `json:"id"`
		} `json:"jwt_auth_config"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &created))
	id := created.Config.ID

	// The mutation applied to the live middleware: snapshot is non-nil.
	require.NotNil(t, middleware.snapshot.Load(), "create must push the config into the live snapshot")
	assert.NotNil(t, (*middleware.snapshot.Load())["https://idp.example.com"])

	// Duplicate issuer is a 409.
	ctx = auditRequestCtx("POST", "/api/governance/jwt-auth", []byte(body))
	h.createConfig(ctx)
	assert.Equal(t, 409, ctx.Response.StatusCode())

	// Disable via update: snapshot returns to nil (default-off).
	updCtx := auditRequestCtx("PUT", "/api/governance/jwt-auth/"+id, []byte(`{"enabled": false}`))
	updCtx.SetUserValue("id", id)
	h.updateConfig(updCtx)
	require.Equal(t, 200, updCtx.Response.StatusCode(), "body: %s", updCtx.Response.Body())
	assert.Nil(t, middleware.snapshot.Load(), "disabling the only issuer must clear the snapshot")

	// List and get.
	listCtx := auditRequestCtx("GET", "/api/governance/jwt-auth", nil)
	h.listConfigs(listCtx)
	require.Equal(t, 200, listCtx.Response.StatusCode())
	assert.NotContains(t, string(listCtx.Response.Body()), "sk-bf-acme-value")

	// Audit rows recorded for both mutations.
	logs, _, err := store.GetAuditLogs(context.Background(), configstore.AuditLogsQueryParams{Target: id})
	require.NoError(t, err)
	actions := map[string]int{}
	for _, row := range logs {
		actions[row.Action]++
	}
	assert.Equal(t, 1, actions[AuditActionJWTAuthCreate])
	assert.Equal(t, 1, actions[AuditActionJWTAuthUpdate])

	// Delete, then 404.
	delCtx := auditRequestCtx("DELETE", "/api/governance/jwt-auth/"+id, nil)
	delCtx.SetUserValue("id", id)
	h.deleteConfig(delCtx)
	require.Equal(t, 200, delCtx.Response.StatusCode())
	delCtx = auditRequestCtx("DELETE", "/api/governance/jwt-auth/"+id, nil)
	delCtx.SetUserValue("id", id)
	h.deleteConfig(delCtx)
	assert.Equal(t, 404, delCtx.Response.StatusCode())
}
