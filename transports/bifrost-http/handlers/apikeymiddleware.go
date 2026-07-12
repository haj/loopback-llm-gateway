// This file contains the admin API key authentication middleware for the
// Loopback Gateway HTTP server. It authenticates "lbk_"-prefixed bearer tokens
// (scope-based admin-plane API keys — see apikeys.go) against the management
// API. Admin API keys are DISTINCT from governance virtual keys (x-bf-vk):
// this middleware runs only on dashboard/API routes and is never part of the
// inference middleware chain.
//
// SAFETY — this middleware must be a byte-for-byte no-op unless an admin API
// key is actually presented:
//   - No "lbk_" bearer credential  → next(ctx) with ZERO mutations. Sessions,
//     basic auth, IdP JWTs, cookies and anonymous requests are untouched.
//   - Invalid / unknown / revoked / expired "lbk_" token → falls through
//     untouched so the downstream auth middleware rejects it exactly as it
//     rejects any bad credential today (whitelisted routes keep working).
//   - Valid key + sufficient scope → sets IsAPIKeyAuthContextKey (which
//     AuthMiddleware already honors as a skip) plus the key ID for audit
//     attribution, then continues.
//   - Valid key + insufficient scope → 403. Under key auth authorization is
//     deny-by-default: paths that map to no RBAC resource are only granted by
//     a wildcard-resource scope.
//
// Scopes reuse the RBAC permission vocabulary verbatim: paths map to resources
// via rbacResourceForPath (rbac_middleware.go) and methods map to operations
// like operationForMethod, extended so read-only methods require the "Read"
// operation (RBAC never gates reads for session users, but a key must be
// explicitly granted them).
package handlers

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/valyala/fasthttp"
)

// apiKeyLastUsedThrottle is the minimum interval between persisted
// last_used_at stamps for a single key, so a busy automation client cannot
// hammer the config store with one write per request.
const apiKeyLastUsedThrottle = 60 * time.Second

// APIKeyMiddleware authenticates admin API keys ("lbk_" bearer tokens) and
// enforces their scopes on management routes.
type APIKeyMiddleware struct {
	store configstore.ConfigStore

	// lastUsed throttles last_used_at writes: key ID → unix seconds of the most
	// recently persisted use.
	lastUsed sync.Map
}

// NewAPIKeyMiddleware creates an admin API key middleware. A nil store keeps
// the middleware fully passive (every request falls through untouched).
func NewAPIKeyMiddleware(store configstore.ConfigStore) *APIKeyMiddleware {
	return &APIKeyMiddleware{store: store}
}

// adminAPIKeyFromRequest extracts an admin API key candidate from the
// Authorization header. It returns ok=false for anything that is not a
// "Bearer lbk_..." credential, mirroring how trySSOAuth cheaply distinguishes
// JWTs — prefix sniffing keeps every other credential type on its existing
// auth path.
func adminAPIKeyFromRequest(ctx *fasthttp.RequestCtx) (string, bool) {
	authorization := string(ctx.Request.Header.Peek("Authorization"))
	if authorization == "" {
		return "", false
	}
	scheme, token, ok := strings.Cut(authorization, " ")
	if !ok || scheme != "Bearer" {
		return "", false
	}
	if !strings.HasPrefix(token, configstoreTables.AdminAPIKeyPrefix) {
		return "", false
	}
	return token, true
}

// Middleware returns the admin API key authentication middleware.
func (m *APIKeyMiddleware) Middleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			// No admin API key presented: pass through byte-for-byte.
			token, ok := adminAPIKeyFromRequest(ctx)
			if !ok || m.store == nil {
				next(ctx)
				return
			}
			// Look up by SHA-256 of the plaintext (keys are hashed at rest).
			// Unknown keys FALL THROUGH untouched — the downstream auth
			// middleware 401s them exactly as it would any bad credential, and
			// whitelisted routes (e.g. /health) stay reachable.
			key, err := m.store.GetAPIKeyByValueHash(ctx, encrypt.HashSHA256(token))
			if err != nil {
				next(ctx)
				return
			}
			// Revoked/expired keys are treated like unknown ones: fall through.
			if key.Status != configstoreTables.AdminAPIKeyStatusActive || key.IsExpired(time.Now()) {
				next(ctx)
				return
			}
			// The key is valid — enforce its scopes. Deny-by-default: a path
			// that maps to no RBAC resource (rbacResourceForPath == "") is only
			// covered by a wildcard-resource scope, and likewise an unmapped
			// method only by a wildcard operation.
			resource := rbacResourceForPath(string(ctx.Path()))
			operation := apiKeyOperationForMethod(string(ctx.Method()))
			allowed := false
			for i := range key.Scopes {
				if key.Scopes[i].Matches(resource, operation) {
					allowed = true
					break
				}
			}
			if !allowed {
				SendError(ctx, fasthttp.StatusForbidden, "Forbidden: this API key does not have the required scope for this request")
				return
			}
			// Authenticated: mark the request so AuthMiddleware skips its
			// session/password checks (middlewares.go honors this key), and
			// attach identity for audit attribution. RBACMiddleware also skips
			// key-authenticated requests — scopes were just enforced with the
			// identical vocabulary.
			ctx.SetUserValue(schemas.IsAPIKeyAuthContextKey, true)
			ctx.SetUserValue(schemas.APIKeyIDContextKey, key.ID)
			ctx.SetUserValue(schemas.BifrostContextKeyUserName, "api-key:"+key.Name)
			m.touchLastUsed(key.ID)
			next(ctx)
		}
	}
}

// apiKeyOperationForMethod maps an HTTP method to the RBAC operation an admin
// API key must be granted. Unlike operationForMethod (which returns "" for
// read-only methods because RBAC never gates session-user reads), key-based
// auth gates reads too: GET/HEAD require "Read". Unrecognized methods return
// "" and therefore only match a wildcard-operation scope.
func apiKeyOperationForMethod(method string) string {
	switch method {
	case fasthttp.MethodGet, fasthttp.MethodHead:
		return configstoreTables.RbacOperationRead
	case fasthttp.MethodPost:
		return configstoreTables.RbacOperationCreate
	case fasthttp.MethodPut, fasthttp.MethodPatch:
		return configstoreTables.RbacOperationUpdate
	case fasthttp.MethodDelete:
		return configstoreTables.RbacOperationDelete
	default:
		return ""
	}
}

// touchLastUsed stamps the key's last_used_at, throttled to once per
// apiKeyLastUsedThrottle and performed asynchronously. Best-effort: a write
// failure is logged and never affects the authenticated request.
func (m *APIKeyMiddleware) touchLastUsed(id string) {
	now := time.Now()
	if v, ok := m.lastUsed.Load(id); ok {
		if last, ok := v.(int64); ok && now.Unix()-last < int64(apiKeyLastUsedThrottle/time.Second) {
			return
		}
	}
	m.lastUsed.Store(id, now.Unix())
	go func() {
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.store.TouchAPIKeyLastUsed(writeCtx, id, now); err != nil && logger != nil {
			logger.Warn("api-key: failed to stamp last_used_at for key %s: %v", id, err)
		}
	}()
}
