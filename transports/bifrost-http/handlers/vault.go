// This file contains the HTTP handler backing the Loopback Gateway vault /
// secret-manager sync UI.
//
// Vault backend configuration is file/env-only (config_store.vault_store in
// config.json), never DB-backed — config-store rows may themselves hold vault
// references — so this handler exposes STATUS + operations, not credential
// CRUD: a status snapshot, a manual refresh (re-resolve rotated provider
// secrets and hot-swap them into the live engine, no restart), and a
// connectivity test. It is always registered and reports {enabled:false} when
// vault is unconfigured, keeping the default-off invariant: with no
// vault_store config nothing here touches the request path or the hooks.
package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// vaultPingTimeout bounds the connectivity probes issued by the status and
// test-connection endpoints so a down vault cannot stall the dashboard.
const vaultPingTimeout = 3 * time.Second

// VaultHandler serves vault status, manual refresh, and connection testing.
type VaultHandler struct {
	config *lib.Config
}

// NewVaultHandler creates a vault handler.
func NewVaultHandler(config *lib.Config) (*VaultHandler, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}
	return &VaultHandler{config: config}, nil
}

// RegisterRoutes wires the vault endpoints.
func (h *VaultHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/vault/status", lib.ChainMiddlewares(h.getStatus, middlewares...))
	r.POST("/api/vault/refresh", lib.ChainMiddlewares(h.refresh, middlewares...))
	r.POST("/api/vault/test-connection", lib.ChainMiddlewares(h.testConnection, middlewares...))
}

// vaultRuntime returns the live vault runtime, or nil when vault is disabled.
func (h *VaultHandler) vaultRuntime() *lib.VaultRuntime {
	if h.config == nil || h.config.Vault == nil || h.config.Vault.Registry == nil {
		return nil
	}
	return h.config.Vault
}

// getStatus returns the vault status snapshot. Shape when disabled: {"enabled": false}.
func (h *VaultHandler) getStatus(ctx *fasthttp.RequestCtx) {
	v := h.vaultRuntime()
	if v == nil {
		SendJSON(ctx, map[string]any{"enabled": false})
		return
	}
	status := v.Registry.Status()
	syncInterval := ""
	if d := v.Config.EffectiveSyncInterval(); d > 0 {
		syncInterval = d.String()
	}
	resp := map[string]any{
		"enabled":       true,
		"type":          v.Config.Type,
		"prefix":        status.Prefix,
		"access_mode":   v.Config.EffectiveAccessMode(),
		"sync_interval": syncInterval,
		"cache": map[string]any{
			"entries":     status.CacheEntries,
			"ttl_seconds": int(status.CacheTTL.Seconds()),
		},
	}
	if status.LastError != "" {
		resp["last_error"] = status.LastError
		if status.LastErrorAt != nil {
			resp["last_error_at"] = status.LastErrorAt.UTC().Format(time.RFC3339)
		}
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), vaultPingTimeout)
	defer cancel()
	if err := v.Registry.Ping(pingCtx); err != nil {
		resp["healthy"] = false
		resp["health_error"] = err.Error()
	} else {
		resp["healthy"] = true
	}
	lastRefresh, refreshErrors := v.LastRefresh()
	if !lastRefresh.IsZero() {
		resp["last_refresh"] = lastRefresh.UTC().Format(time.RFC3339)
	}
	if refreshErrors == nil {
		refreshErrors = []string{}
	}
	resp["last_refresh_errors"] = refreshErrors
	SendJSON(ctx, resp)
}

// refresh invalidates the registry cache and re-resolves vault-backed provider
// secrets, hot-swapping changed provider configs into the live engine.
func (h *VaultHandler) refresh(ctx *fasthttp.RequestCtx) {
	if h.vaultRuntime() == nil {
		SendError(ctx, 400, "Vault is not enabled")
		return
	}
	result := h.config.RefreshVaultSecrets(ctx)
	outcome := configstoreTables.AuditOutcomeSuccess
	if len(result.Errors) > 0 {
		outcome = configstoreTables.AuditOutcomeFailure
	}
	recordAudit(ctx, h.config.ConfigStore, AuditActionVaultRefresh, outcome, "vault")
	SendJSON(ctx, map[string]any{
		"message":           "Vault refresh completed",
		"providers_updated": result.ProvidersUpdated,
		"keys_rechecked":    result.KeysRechecked,
		"secrets_updated":   result.SecretsUpdated,
		"errors":            result.Errors,
	})
}

// testConnection pings the configured backend and reports 200 or 502.
func (h *VaultHandler) testConnection(ctx *fasthttp.RequestCtx) {
	v := h.vaultRuntime()
	if v == nil {
		SendError(ctx, 400, "Vault is not enabled")
		return
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), vaultPingTimeout)
	defer cancel()
	if err := v.Registry.Ping(pingCtx); err != nil {
		recordAudit(ctx, h.config.ConfigStore, AuditActionVaultTestConnection, configstoreTables.AuditOutcomeFailure, "vault")
		SendError(ctx, 502, fmt.Sprintf("Vault connection failed: %v", err))
		return
	}
	recordAudit(ctx, h.config.ConfigStore, AuditActionVaultTestConnection, configstoreTables.AuditOutcomeSuccess, "vault")
	SendJSON(ctx, map[string]any{
		"healthy": true,
		"message": "Vault connection successful",
	})
}
