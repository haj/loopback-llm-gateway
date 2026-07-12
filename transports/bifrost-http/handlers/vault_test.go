package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/framework/vault"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// vaultTestBackend is a minimal in-memory vault.Backend for handler tests.
type vaultTestBackend struct {
	secrets map[string]map[string]string
	pingErr error
}

func (b *vaultTestBackend) Name() string { return "test-backend" }

func (b *vaultTestBackend) GetSecret(_ context.Context, path string) (map[string]string, error) {
	data, ok := b.secrets[path]
	if !ok {
		return nil, fmt.Errorf("%w: %s", vault.ErrNotFound, path)
	}
	return data, nil
}

func (b *vaultTestBackend) PutSecret(_ context.Context, path string, data map[string]string) error {
	b.secrets[path] = data
	return nil
}

func (b *vaultTestBackend) DeleteSecret(_ context.Context, path string) error {
	delete(b.secrets, path)
	return nil
}

func (b *vaultTestBackend) Ping(_ context.Context) error { return b.pingErr }

func newVaultTestHandler(t *testing.T, backend *vaultTestBackend) *VaultHandler {
	t.Helper()
	cfg := &vault.Config{
		Enabled:    true,
		Type:       vault.TypeHashiCorp,
		AccessMode: vault.AccessModeReadOnly,
		CacheTTL:   "1m",
	}
	registry, err := vault.NewRegistryWithBackend(cfg, backend, nil)
	require.NoError(t, err)
	config := &lib.Config{Vault: &lib.VaultRuntime{Registry: registry, Config: cfg}}
	h, err := NewVaultHandler(config)
	require.NoError(t, err)
	return h
}

func decodeVaultBody(t *testing.T, ctx *fasthttp.RequestCtx) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &body))
	return body
}

func TestVaultHandlerRequiresConfig(t *testing.T) {
	_, err := NewVaultHandler(nil)
	require.Error(t, err)
}

func TestVaultStatusDisabledShape(t *testing.T) {
	h, err := NewVaultHandler(&lib.Config{})
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	h.getStatus(ctx)
	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	body := decodeVaultBody(t, ctx)
	require.Equal(t, map[string]any{"enabled": false}, body)
}

func TestVaultMutationsRejectedWhenDisabled(t *testing.T) {
	h, err := NewVaultHandler(&lib.Config{})
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	h.refresh(ctx)
	require.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode())

	ctx = &fasthttp.RequestCtx{}
	h.testConnection(ctx)
	require.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode())
}

func TestVaultStatusEnabled(t *testing.T) {
	h := newVaultTestHandler(t, &vaultTestBackend{secrets: map[string]map[string]string{}})

	ctx := &fasthttp.RequestCtx{}
	h.getStatus(ctx)
	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	body := decodeVaultBody(t, ctx)
	require.Equal(t, true, body["enabled"])
	require.Equal(t, vault.TypeHashiCorp, body["type"])
	require.Equal(t, vault.AccessModeReadOnly, body["access_mode"])
	require.Equal(t, vault.DefaultPrefix, body["prefix"])
	require.Equal(t, true, body["healthy"])
	cache, ok := body["cache"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(0), cache["entries"])
	require.Equal(t, float64(60), cache["ttl_seconds"])
	require.Equal(t, []any{}, body["last_refresh_errors"])
	require.NotContains(t, body, "last_refresh", "no refresh has run yet")
}

func TestVaultStatusUnhealthyBackend(t *testing.T) {
	h := newVaultTestHandler(t, &vaultTestBackend{
		secrets: map[string]map[string]string{},
		pingErr: errors.New("connection refused"),
	})

	ctx := &fasthttp.RequestCtx{}
	h.getStatus(ctx)
	body := decodeVaultBody(t, ctx)
	require.Equal(t, true, body["enabled"])
	require.Equal(t, false, body["healthy"])
	require.Contains(t, body["health_error"], "connection refused")
}

func TestVaultTestConnection(t *testing.T) {
	backend := &vaultTestBackend{secrets: map[string]map[string]string{}}
	h := newVaultTestHandler(t, backend)

	ctx := &fasthttp.RequestCtx{}
	h.testConnection(ctx)
	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	require.Equal(t, true, decodeVaultBody(t, ctx)["healthy"])

	backend.pingErr = errors.New("permission denied")
	ctx = &fasthttp.RequestCtx{}
	h.testConnection(ctx)
	require.Equal(t, fasthttp.StatusBadGateway, ctx.Response.StatusCode())
}

func TestVaultRefreshEnabledEmptyProviders(t *testing.T) {
	h := newVaultTestHandler(t, &vaultTestBackend{secrets: map[string]map[string]string{}})

	ctx := &fasthttp.RequestCtx{}
	h.refresh(ctx)
	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	body := decodeVaultBody(t, ctx)
	require.Equal(t, []any{}, body["providers_updated"])
	require.Equal(t, []any{}, body["errors"])
	require.Equal(t, float64(0), body["keys_rechecked"])
}
