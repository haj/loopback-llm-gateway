// This file contains the RBAC authorization middleware for the Loopback Gateway
// HTTP server. It gates MUTATING (POST/PUT/PATCH/DELETE) requests on the
// governance, config and provider management routes against the roles &
// permissions configured via the RBAC UI.
//
// SAFETY — this middleware must never brick an existing deployment:
//   - It is fail-OPEN by default. It only enforces when RBAC is explicitly
//     enabled (LOOPBACK_RBAC_ENABLED / BIFROST_RBAC_ENABLED truthy) AND at least
//     one role assignment exists.
//   - Read-only requests (GET/HEAD/OPTIONS) are never gated.
//   - The local-admin (password) auth path sets IsLocalAdminContextKey and is
//     always allowed through, so an operator can never lock themselves out.
//   - Any store error while resolving permissions fails OPEN (logged, allowed)
//     rather than denying — the gateway and its /api endpoints stay reachable.
package handlers

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/valyala/fasthttp"
)

// RBACMiddleware enforces resource/operation permissions on mutating management
// routes once RBAC is enabled and at least one assignment exists.
type RBACMiddleware struct {
	store   configstore.ConfigStore
	enabled atomic.Bool
}

// NewRBACMiddleware creates an RBAC middleware. The initial enabled state is
// the OR of the LOOPBACK_RBAC_ENABLED (or legacy BIFROST_RBAC_ENABLED)
// environment variable and the persisted rbac_enforcement_enabled
// governance-config flag written by the secure-setup enforce action. A missing
// key or ANY store error reads as disabled (fail-open) so a broken store can
// never brick boot or the request path. The flag can be toggled at runtime via
// SetEnabled.
func NewRBACMiddleware(store configstore.ConfigStore) *RBACMiddleware {
	m := &RBACMiddleware{store: store}
	m.enabled.Store(rbacEnabledFromEnv() || rbacEnabledFromStore(store))
	return m
}

// rbacEnabledFromStore reads the persisted enforcement flag. Fail-open: a
// missing row or store error means disabled.
func rbacEnabledFromStore(store configstore.ConfigStore) bool {
	if store == nil {
		return false
	}
	cfg, err := store.GetConfig(context.Background(), configstoreTables.ConfigRBACEnforcementKey)
	if err != nil {
		if !errors.Is(err, configstore.ErrNotFound) && logger != nil {
			logger.Warn("rbac: failed to read persisted enforcement flag, staying fail-open: %v", err)
		}
		return false
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(cfg.Value))
	return err == nil && enabled
}

// rbacEnabledFromEnv reports whether RBAC enforcement is enabled per environment.
func rbacEnabledFromEnv() bool {
	for _, key := range []string{"LOOPBACK_RBAC_ENABLED", "BIFROST_RBAC_ENABLED"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				return b
			}
		}
	}
	return false
}

// SetEnabled toggles RBAC enforcement at runtime.
func (m *RBACMiddleware) SetEnabled(enabled bool) { m.enabled.Store(enabled) }

// IsEnabled reports whether RBAC enforcement is currently enabled.
func (m *RBACMiddleware) IsEnabled() bool { return m.enabled.Load() }

// Middleware returns the RBAC enforcement middleware.
func (m *RBACMiddleware) Middleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			// Admin API-key requests were already authorized by APIKeyMiddleware
			// (apikeymiddleware.go), which enforced the key's scopes against the
			// SAME (resource, operation) vocabulary this middleware uses — and,
			// unlike RBAC, gates reads too. Re-gating here would require binding
			// keys to managed users; skipping is safe ONLY because the scope
			// check ran first (the two land together — never ship this skip
			// without the middleware).
			if isAPIKeyAuth, ok := ctx.UserValue(schemas.IsAPIKeyAuthContextKey).(bool); ok && isAPIKeyAuth {
				next(ctx)
				return
			}
			// Only gate mutating requests; reads are always allowed.
			operation := operationForMethod(string(ctx.Method()))
			if operation == "" {
				next(ctx)
				return
			}
			// Fail-open when RBAC is disabled.
			if m.store == nil || !m.enabled.Load() {
				next(ctx)
				return
			}
			// Local admin (password auth) always bypasses RBAC so an operator is
			// never locked out.
			if isLocal, ok := ctx.UserValue(schemas.IsLocalAdminContextKey).(bool); ok && isLocal {
				next(ctx)
				return
			}
			// Map the path to a protected resource; ungated paths pass through.
			resource := rbacResourceForPath(string(ctx.Path()))
			if resource == "" {
				next(ctx)
				return
			}
			// Fail-open until at least one role assignment exists, so enabling RBAC
			// on an unconfigured deployment never bricks access.
			count, err := m.store.CountRoleAssignments(ctx)
			if err != nil {
				logger.Warn("rbac: failed to count role assignments, allowing request: %v", err)
				next(ctx)
				return
			}
			if count == 0 {
				next(ctx)
				return
			}
			// RBAC is enabled and configured. Resolve the acting user's identity.
			// In OSS the only authenticated dashboard principal is the local admin
			// (handled above); a non-admin principal is identified by the user ID
			// set by the (enterprise) auth middleware.
			userID, _ := ctx.UserValue(schemas.BifrostContextKeyUserID).(string)
			userID = strings.TrimSpace(userID)
			if userID == "" {
				// Authenticated but unidentifiable principal on a gated mutating
				// route while RBAC is active and configured: fail closed.
				SendError(ctx, fasthttp.StatusForbidden, "Forbidden: RBAC is enabled and this request could not be attributed to a user with the required permission")
				return
			}
			if m.userAllowed(ctx, userID, resource, operation) {
				next(ctx)
				return
			}
			SendError(ctx, fasthttp.StatusForbidden, "Forbidden: you do not have permission to "+operation+" "+resource)
		}
	}
}

// userAllowed reports whether the user has any role granting (resource,
// operation). A store error fails OPEN (logged, allowed) to keep the API
// reachable.
func (m *RBACMiddleware) userAllowed(ctx *fasthttp.RequestCtx, userID, resource, operation string) bool {
	assignments, err := m.store.GetRoleAssignmentsByUser(ctx, userID)
	if err != nil {
		logger.Warn("rbac: failed to resolve permissions for user %s, allowing request: %v", userID, err)
		return true
	}
	for i := range assignments {
		role := assignments[i].Role
		if role == nil {
			continue
		}
		for j := range role.Permissions {
			if role.Permissions[j].Matches(resource, operation) {
				return true
			}
		}
	}
	return false
}

// operationForMethod maps an HTTP method to an RBAC operation. Read-only methods
// return "" (not gated).
func operationForMethod(method string) string {
	switch method {
	case fasthttp.MethodPost:
		return "Create"
	case fasthttp.MethodPut, fasthttp.MethodPatch:
		return "Update"
	case fasthttp.MethodDelete:
		return "Delete"
	default:
		return ""
	}
}

// rbacResourceForPath maps an API path to a protected RBAC resource name
// (matching the dashboard's RbacResource enum). Returns "" for routes that are
// not gated by RBAC.
func rbacResourceForPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/prompt-repo/deployments"),
		strings.HasPrefix(path, "/api/prompt-repo/prompts/") && strings.Contains(path, "/deployments"):
		return "PromptDeploymentStrategy"
	case strings.HasPrefix(path, "/api/governance/guardrails"):
		return "GuardrailsConfig"
	case strings.HasPrefix(path, "/api/governance/pii"):
		return "PIIRedactor"
	case strings.HasPrefix(path, "/api/governance/roles"),
		strings.HasPrefix(path, "/api/governance/role-assignments"),
		strings.HasPrefix(path, "/api/governance/rbac"):
		return "RBAC"
	// Must precede the generic "/api/governance" case so admin API key CRUD is
	// gated as its own resource (matching the dashboard's RbacResource.APIKeys).
	case strings.HasPrefix(path, "/api/governance/api-keys"):
		return "APIKeys"
	case strings.HasPrefix(path, "/api/governance/jwt-auth"):
		return "JWTAuth"
	case strings.HasPrefix(path, "/api/governance/users"),
		strings.HasPrefix(path, "/api/governance/business-units"):
		return "Users"
	case strings.HasPrefix(path, "/api/governance/virtual-keys"):
		return "VirtualKeys"
	case strings.HasPrefix(path, "/api/governance/teams"):
		return "Teams"
	case strings.HasPrefix(path, "/api/governance/customers"):
		return "Customers"
	case strings.HasPrefix(path, "/api/governance"):
		return "Governance"
	case strings.HasPrefix(path, "/api/alerting"):
		return "AlertChannels"
	case strings.HasPrefix(path, "/api/providers"):
		return "ModelProvider"
	case strings.HasPrefix(path, "/api/config"):
		return "Settings"
	default:
		return ""
	}
}
