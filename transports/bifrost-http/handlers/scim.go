// This file contains HTTP handlers for the Loopback Gateway SSO/SCIM first
// slice. It exposes:
//   - a PUBLIC login-page config endpoint reporting whether SSO is enabled and,
//     if so, the provider/issuer so the login UI can render an SSO option;
//   - ADMIN endpoints to list provisioned SCIM users/groups and to trigger a
//     manual sync run against the configured IdP (Keycloak, Okta, or Entra).
//
// It follows the guardrails.go / circuitbreaker.go handler patterns
// (RegisterRoutes + SendJSON/SendError + recordAudit). Everything here is
// default-OFF: when SSO is not configured, the admin endpoints report "disabled"
// and the public config endpoint reports enabled=false. Password auth is never
// involved in this handler.
//
// DEFERRED (not built in this slice): the interactive browser authorization-code
// login flow that mints a dashboard session cookie from an SSO login, and the
// inbound SCIM 2.0 provisioning REST surface (PATCH/filter). The supported SSO
// auth path is presenting a valid IdP-issued Bearer JWT (see middlewares.go).
package handlers

import (
	"context"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/sso"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// SCIMHandler manages HTTP requests for SSO/SCIM provisioning.
type SCIMHandler struct {
	configStore configstore.ConfigStore
	// scimResolver returns the live SSO/SCIM config, or nil when SSO is not
	// configured. Resolved lazily so a config reload is honored without
	// capturing a stale reference.
	scimResolver func() *sso.SCIMConfig
}

// NewSCIMHandler creates a SCIM handler. scimResolver may be nil (e.g. tests);
// when nil, SSO is always reported disabled.
func NewSCIMHandler(configStore configstore.ConfigStore, scimResolver func() *sso.SCIMConfig) (*SCIMHandler, error) {
	return &SCIMHandler{configStore: configStore, scimResolver: scimResolver}, nil
}

// RegisterRoutes wires the SCIM endpoints. /api/scim/oauth/config is already
// whitelisted (public) by the auth middleware so the login page can read it.
func (h *SCIMHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	// Public: consumed by the login page to decide whether to show SSO.
	r.GET("/api/scim/oauth/config", lib.ChainMiddlewares(h.getLoginConfig, middlewares...))
	// Admin: provisioning inspection + manual sync.
	r.GET("/api/scim/users", lib.ChainMiddlewares(h.listUsers, middlewares...))
	r.GET("/api/scim/groups", lib.ChainMiddlewares(h.listGroups, middlewares...))
	r.GET("/api/scim/status", lib.ChainMiddlewares(h.getStatus, middlewares...))
	r.POST("/api/scim/sync", lib.ChainMiddlewares(h.triggerSync, middlewares...))
}

func (h *SCIMHandler) scim() *sso.SCIMConfig {
	if h.scimResolver == nil {
		return nil
	}
	return h.scimResolver()
}

// getLoginConfig handles GET /api/scim/oauth/config. PUBLIC. It returns the
// minimum the login page needs to render an SSO sign-in option. When SSO is off
// it returns enabled=false and the login page shows only password auth.
func (h *SCIMHandler) getLoginConfig(ctx *fasthttp.RequestCtx) {
	scim := h.scim()
	if scim == nil || !scim.Enabled {
		SendJSON(ctx, map[string]any{"enabled": false})
		return
	}
	resp := map[string]any{
		"enabled":  true,
		"provider": scim.Provider,
	}
	// Provider-aware (keycloak/okta/entra) via the OIDCProvider abstraction.
	// The gateway accepts IdP-issued Bearer JWTs; the interactive code-flow
	// dashboard login remains deferred.
	if p, err := sso.NewProvider(scim); err == nil && p != nil {
		resp["issuer_url"] = p.IssuerURL()
		resp["auth_mode"] = "bearer_jwt"
		if kc, err := scim.Keycloak(); err == nil {
			resp["authorization_endpoint"] = kc.IssuerURL() + "/protocol/openid-connect/auth"
			resp["client_id"] = kc.ClientID
		}
	}
	SendJSON(ctx, resp)
}

// getStatus handles GET /api/scim/status. ADMIN. Reports the effective SSO
// configuration state without leaking secrets.
func (h *SCIMHandler) getStatus(ctx *fasthttp.RequestCtx) {
	scim := h.scim()
	if scim == nil {
		SendJSON(ctx, map[string]any{"enabled": false, "configured": false})
		return
	}
	status := map[string]any{
		"enabled":    scim.Enabled,
		"configured": true,
		"provider":   scim.Provider,
		// Reports whether the inbound /scim/v2 endpoint is live. The token
		// itself is never serialized.
		"provisioning_enabled": scim.Provisioning.InboundEnabled(),
	}
	if scim.Enabled {
		if err := scim.Validate(); err != nil {
			status["valid"] = false
			status["error"] = err.Error()
		} else {
			status["valid"] = true
		}
	}
	SendJSON(ctx, status)
}

// listUsers handles GET /api/scim/users. ADMIN.
func (h *SCIMHandler) listUsers(ctx *fasthttp.RequestCtx) {
	if h.configStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "config store unavailable")
		return
	}
	params := configstore.SCIMUsersQueryParams{
		Search:   string(ctx.QueryArgs().Peek("search")),
		Provider: string(ctx.QueryArgs().Peek("provider")),
		Limit:    queryInt(ctx, "limit"),
		Offset:   queryInt(ctx, "offset"),
	}
	if v := strings.TrimSpace(string(ctx.QueryArgs().Peek("active"))); v != "" {
		b := v == "true" || v == "1"
		params.Active = &b
	}
	users, total, err := h.configStore.GetSCIMUsers(ctx, params)
	if err != nil {
		logger.Error("failed to list scim users: %v", err)
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to list SCIM users")
		return
	}
	SendJSON(ctx, map[string]any{"users": users, "total": total, "count": len(users)})
}

// listGroups handles GET /api/scim/groups. ADMIN.
func (h *SCIMHandler) listGroups(ctx *fasthttp.RequestCtx) {
	if h.configStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "config store unavailable")
		return
	}
	params := configstore.SCIMGroupsQueryParams{
		Search:   string(ctx.QueryArgs().Peek("search")),
		Provider: string(ctx.QueryArgs().Peek("provider")),
		Limit:    queryInt(ctx, "limit"),
		Offset:   queryInt(ctx, "offset"),
	}
	groups, total, err := h.configStore.GetSCIMGroups(ctx, params)
	if err != nil {
		logger.Error("failed to list scim groups: %v", err)
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to list SCIM groups")
		return
	}
	SendJSON(ctx, map[string]any{"groups": groups, "total": total, "count": len(groups)})
}

// triggerSync handles POST /api/scim/sync. ADMIN. Runs a one-shot reconcile
// against the configured IdP and returns a summary.
func (h *SCIMHandler) triggerSync(ctx *fasthttp.RequestCtx) {
	scim := h.scim()
	if scim == nil || !scim.Enabled {
		SendError(ctx, fasthttp.StatusBadRequest, "SSO/SCIM is not enabled")
		return
	}
	engine, err := sso.NewSyncEngine(scim, h.configStore, nil)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Failed to initialize sync engine: "+err.Error())
		return
	}
	if engine == nil {
		SendError(ctx, fasthttp.StatusBadRequest, "SSO/SCIM is not enabled")
		return
	}
	// Use a background context so a slow IdP doesn't tie the result to the
	// request connection lifetime.
	res, err := engine.Sync(context.Background())
	if err != nil {
		logger.Error("scim sync failed: %v", err)
		recordAudit(ctx, h.configStore, "scim.sync", "failure", scim.Provider)
		SendError(ctx, fasthttp.StatusBadGateway, "SCIM sync failed: "+err.Error())
		return
	}
	recordAudit(ctx, h.configStore, "scim.sync", "success", scim.Provider)
	SendJSON(ctx, map[string]any{"result": res})
}

// queryInt parses a non-negative integer query arg, returning 0 when absent or
// invalid.
func queryInt(ctx *fasthttp.RequestCtx, key string) int {
	v := strings.TrimSpace(string(ctx.QueryArgs().Peek(key)))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
