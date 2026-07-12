// Hermetic tests for the admin API key middleware (apikeymiddleware.go) and its
// composition with AuthMiddleware and RBACMiddleware, chained exactly as
// server.go chains them (APIKeyMiddleware → AuthMiddleware.APIMiddleware, with
// RBACMiddleware on the mutating handler chain). These pin the safety
// invariants:
//
//	(a) no key            → byte-for-byte pass-through, zero ctx mutations
//	(b) invalid/revoked/expired key → falls through, downstream auth 401s as today
//	(c) valid + in-scope  → next runs, AuthMiddleware skips via IsAPIKeyAuthContextKey
//	(d) valid + out-of-scope → 403
//	(e) wildcard scope grants unmapped paths; non-wildcard is deny-by-default
//	(f) RBAC skips key-auth requests but still enforces for session users
//	(g) Basic/password auth is unchanged with the new middleware in the chain
//
// All state lives in a SQLite config store under t.TempDir(); no network.
package handlers

import (
	"context"
	"encoding/base64"
	"net"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// seedAdminAPIKey inserts an admin API key directly through the store and
// returns its plaintext secret.
func seedAdminAPIKey(t *testing.T, store configstore.ConfigStore, id, name, status string, expiresAt *time.Time, scopes []configstoreTables.TableAdminAPIKeyScope) string {
	t.Helper()
	plaintext, err := generateAdminAPIKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	key := &configstoreTables.TableAdminAPIKey{
		ID:        id,
		Name:      name,
		KeyPrefix: plaintext[:adminAPIKeyDisplayPrefixLen],
		Value:     plaintext,
		Status:    status,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.CreateAPIKey(context.Background(), key); err != nil {
		t.Fatalf("failed to seed key: %v", err)
	}
	if err := store.ReplaceAPIKeyScopes(context.Background(), id, stampAPIKeyScopes(id, scopes)); err != nil {
		t.Fatalf("failed to seed scopes: %v", err)
	}
	return plaintext
}

// newAuthedTestChain builds the server.go middleware order — APIKeyMiddleware
// immediately before AuthMiddleware.APIMiddleware — around a terminal handler,
// with password auth ENABLED (admin / password123).
func newAuthedTestChain(t *testing.T, store configstore.ConfigStore, terminal fasthttp.RequestHandler) fasthttp.RequestHandler {
	t.Helper()
	SetLogger(&mockLogger{})
	hashed, err := encrypt.Hash("password123")
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	am := &AuthMiddleware{store: store}
	am.UpdateAuthConfig(&configstore.AuthConfig{
		AdminUserName: schemas.NewSecretVar("admin"),
		AdminPassword: schemas.NewSecretVar(hashed),
		IsEnabled:     true,
	})
	akm := NewAPIKeyMiddleware(store)
	return lib.ChainMiddlewares(terminal, akm.Middleware(), am.APIMiddleware())
}

func newRequestCtx(method, uri string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	var req fasthttp.Request
	// Init wires the ctx to a (fake) server so it is usable as a
	// context.Context by store calls (bare RequestCtx panics in Done()).
	ctx.Init(&req, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, nil)
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	return ctx
}

// (a) A request with NO admin API key must pass through the middleware with
// zero mutations — the byte-for-byte invariant for existing traffic.
func TestAPIKeyMiddleware_NoKeyIsByteForBytePassthrough(t *testing.T) {
	store := newAPIKeysTestStore(t)
	akm := NewAPIKeyMiddleware(store)

	credentials := map[string]string{
		"no header":        "",
		"basic auth":       "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:password123")),
		"bearer non-lbk":   "Bearer some-session-token",
		"bearer jwt-like":  "Bearer aaa.bbb.ccc",
		"malformed header": "Bearer",
	}
	for name, auth := range credentials {
		t.Run(name, func(t *testing.T) {
			ctx := newRequestCtx("POST", "/api/providers")
			if auth != "" {
				ctx.Request.Header.Set("Authorization", auth)
			}
			nextCalled := false
			akm.Middleware()(func(ctx *fasthttp.RequestCtx) { nextCalled = true })(ctx)

			if !nextCalled {
				t.Fatal("next must be called when no admin API key is presented")
			}
			if v := ctx.UserValue(schemas.IsAPIKeyAuthContextKey); v != nil {
				t.Fatalf("IsAPIKeyAuthContextKey must not be set, got %v", v)
			}
			if v := ctx.UserValue(schemas.APIKeyIDContextKey); v != nil {
				t.Fatalf("APIKeyIDContextKey must not be set, got %v", v)
			}
			if v := ctx.UserValue(schemas.BifrostContextKeyUserName); v != nil {
				t.Fatalf("user name must not be set, got %v", v)
			}
			if ctx.Response.StatusCode() != fasthttp.StatusOK || len(ctx.Response.Body()) != 0 {
				t.Fatalf("response must be untouched, got %d %q", ctx.Response.StatusCode(), ctx.Response.Body())
			}
		})
	}
}

// (b) Invalid, revoked and expired lbk_ tokens fall through the key middleware
// untouched, so the downstream auth middleware rejects them exactly as it
// rejects any bad credential today — and whitelisted routes stay reachable.
func TestAPIKeyMiddleware_BadKeysFallThroughToAuth(t *testing.T) {
	store := newAPIKeysTestStore(t)
	past := time.Now().Add(-time.Hour)
	revoked := seedAdminAPIKey(t, store, "k-revoked", "revoked", configstoreTables.AdminAPIKeyStatusRevoked, nil,
		[]configstoreTables.TableAdminAPIKeyScope{{Resource: "*", Operation: "*"}})
	expired := seedAdminAPIKey(t, store, "k-expired", "expired", configstoreTables.AdminAPIKeyStatusActive, &past,
		[]configstoreTables.TableAdminAPIKeyScope{{Resource: "*", Operation: "*"}})

	tokens := map[string]string{
		"unknown": configstoreTables.AdminAPIKeyPrefix + "deadbeefdeadbeef",
		"revoked": revoked,
		"expired": expired,
	}
	for name, token := range tokens {
		t.Run(name, func(t *testing.T) {
			nextCalled := false
			chain := newAuthedTestChain(t, store, func(ctx *fasthttp.RequestCtx) { nextCalled = true })
			ctx := newRequestCtx("GET", "/api/providers")
			ctx.Request.Header.Set("Authorization", "Bearer "+token)
			chain(ctx)

			if nextCalled {
				t.Fatal("handler must not run for a bad admin API key")
			}
			if ctx.Response.StatusCode() != fasthttp.StatusUnauthorized {
				t.Fatalf("expected 401 from downstream auth, got %d", ctx.Response.StatusCode())
			}
		})

		t.Run(name+" on whitelisted route", func(t *testing.T) {
			nextCalled := false
			chain := newAuthedTestChain(t, store, func(ctx *fasthttp.RequestCtx) { nextCalled = true })
			ctx := newRequestCtx("GET", "/api/session/is-auth-enabled")
			ctx.Request.Header.Set("Authorization", "Bearer "+token)
			chain(ctx)

			if !nextCalled {
				t.Fatalf("whitelisted route must stay reachable with a bad key (got %d)", ctx.Response.StatusCode())
			}
		})
	}
}

// (c) A valid key with a matching (resource, operation) scope authenticates:
// next runs, the context is marked, and AuthMiddleware skips its session /
// password checks via the existing IsAPIKeyAuthContextKey hook.
func TestAPIKeyMiddleware_ValidKeyInScopeAuthenticates(t *testing.T) {
	store := newAPIKeysTestStore(t)
	token := seedAdminAPIKey(t, store, "k-scoped", "provider-bot", configstoreTables.AdminAPIKeyStatusActive, nil,
		[]configstoreTables.TableAdminAPIKeyScope{
			{Resource: "ModelProvider", Operation: "Create"},
			{Resource: "ModelProvider", Operation: "Read"},
		})

	for _, tc := range []struct{ method, uri string }{
		{"POST", "/api/providers"},
		{"GET", "/api/providers/openai"},
	} {
		t.Run(tc.method+" "+tc.uri, func(t *testing.T) {
			nextCalled := false
			var seenAPIKeyAuth, seenKeyID any
			chain := newAuthedTestChain(t, store, func(ctx *fasthttp.RequestCtx) {
				nextCalled = true
				seenAPIKeyAuth = ctx.UserValue(schemas.IsAPIKeyAuthContextKey)
				seenKeyID = ctx.UserValue(schemas.APIKeyIDContextKey)
			})
			ctx := newRequestCtx(tc.method, tc.uri)
			ctx.Request.Header.Set("Authorization", "Bearer "+token)
			chain(ctx)

			if !nextCalled {
				t.Fatalf("expected handler to run, got %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
			}
			if isAuth, ok := seenAPIKeyAuth.(bool); !ok || !isAuth {
				t.Fatalf("expected IsAPIKeyAuthContextKey=true, got %v", seenAPIKeyAuth)
			}
			if id, ok := seenKeyID.(string); !ok || id != "k-scoped" {
				t.Fatalf("expected APIKeyIDContextKey=k-scoped, got %v", seenKeyID)
			}
		})
	}
}

// (d) A valid key with insufficient scope is rejected with 403 (never 401 — the
// key authenticated, it is just not authorized for this request).
func TestAPIKeyMiddleware_ValidKeyOutOfScopeIs403(t *testing.T) {
	store := newAPIKeysTestStore(t)
	token := seedAdminAPIKey(t, store, "k-narrow", "guardrails-reader", configstoreTables.AdminAPIKeyStatusActive, nil,
		[]configstoreTables.TableAdminAPIKeyScope{{Resource: "GuardrailsConfig", Operation: "Read"}})

	for _, tc := range []struct{ method, uri string }{
		{"POST", "/api/providers"},                  // wrong resource
		{"POST", "/api/governance/guardrails"},      // right resource, wrong operation
		{"DELETE", "/api/governance/guardrails/g1"}, // right resource, wrong operation
	} {
		t.Run(tc.method+" "+tc.uri, func(t *testing.T) {
			nextCalled := false
			chain := newAuthedTestChain(t, store, func(ctx *fasthttp.RequestCtx) { nextCalled = true })
			ctx := newRequestCtx(tc.method, tc.uri)
			ctx.Request.Header.Set("Authorization", "Bearer "+token)
			chain(ctx)

			if nextCalled {
				t.Fatal("handler must not run for an out-of-scope key")
			}
			if ctx.Response.StatusCode() != fasthttp.StatusForbidden {
				t.Fatalf("expected 403, got %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
			}
		})
	}

	// The matching scope does work (sanity check that 403s above were about
	// scope, not the key).
	nextCalled := false
	chain := newAuthedTestChain(t, store, func(ctx *fasthttp.RequestCtx) { nextCalled = true })
	ctx := newRequestCtx("GET", "/api/governance/guardrails")
	ctx.Request.Header.Set("Authorization", "Bearer "+token)
	chain(ctx)
	if !nextCalled {
		t.Fatalf("expected in-scope read to pass, got %d", ctx.Response.StatusCode())
	}
}

// (e) Under key auth, authorization is deny-by-default: paths that map to no
// RBAC resource are only granted by a wildcard-resource scope.
func TestAPIKeyMiddleware_UnmappedPathsAreDenyByDefault(t *testing.T) {
	store := newAPIKeysTestStore(t)
	wildcard := seedAdminAPIKey(t, store, "k-wild", "root-bot", configstoreTables.AdminAPIKeyStatusActive, nil,
		[]configstoreTables.TableAdminAPIKeyScope{{Resource: "*", Operation: "*"}})
	narrow := seedAdminAPIKey(t, store, "k-not-wild", "provider-bot", configstoreTables.AdminAPIKeyStatusActive, nil,
		[]configstoreTables.TableAdminAPIKeyScope{{Resource: "ModelProvider", Operation: "*"}})

	// /api/logs maps to no RBAC resource (rbacResourceForPath returns "").
	if got := rbacResourceForPath("/api/logs"); got != "" {
		t.Fatalf("test premise broken: /api/logs now maps to %q", got)
	}

	t.Run("wildcard grants unmapped path", func(t *testing.T) {
		nextCalled := false
		chain := newAuthedTestChain(t, store, func(ctx *fasthttp.RequestCtx) { nextCalled = true })
		ctx := newRequestCtx("GET", "/api/logs")
		ctx.Request.Header.Set("Authorization", "Bearer "+wildcard)
		chain(ctx)
		if !nextCalled {
			t.Fatalf("wildcard key must reach unmapped paths, got %d", ctx.Response.StatusCode())
		}
	})

	t.Run("non-wildcard denied on unmapped path", func(t *testing.T) {
		nextCalled := false
		chain := newAuthedTestChain(t, store, func(ctx *fasthttp.RequestCtx) { nextCalled = true })
		ctx := newRequestCtx("GET", "/api/logs")
		ctx.Request.Header.Set("Authorization", "Bearer "+narrow)
		chain(ctx)
		if nextCalled {
			t.Fatal("non-wildcard key must be denied on unmapped paths")
		}
		if ctx.Response.StatusCode() != fasthttp.StatusForbidden {
			t.Fatalf("expected 403, got %d", ctx.Response.StatusCode())
		}
	})
}

// (f) MANDATORY companion of the RBAC skip (rbac_middleware.go): with RBAC
// enabled AND assignments configured, key-authenticated requests skip RBAC
// (scopes were already enforced upstream with the identical vocabulary) while
// session users remain fully enforced — the regression half proves the skip
// did not weaken RBAC for anyone else.
func TestRBACMiddleware_SkipsKeyAuthButStillEnforcesSessionUsers(t *testing.T) {
	SetLogger(&mockLogger{})
	store := newAPIKeysTestStore(t)
	bg := context.Background()

	// Configure RBAC: one user holding a role that only grants Teams:Read —
	// i.e. NOT allowed to Create ModelProvider.
	user := &configstoreTables.TableUser{ID: "u-1", Name: "Limited", Email: "limited@example.com", Status: configstoreTables.UserStatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.CreateUser(bg, user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	role := &configstoreTables.TableRole{ID: "r-1", Name: "limited-role", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.CreateRole(bg, role); err != nil {
		t.Fatalf("failed to create role: %v", err)
	}
	if err := store.ReplaceRolePermissions(bg, role.ID, []configstoreTables.TablePermission{
		{ID: "p-1", RoleID: role.ID, Resource: "Teams", Operation: "Read", CreatedAt: time.Now()},
	}); err != nil {
		t.Fatalf("failed to set permissions: %v", err)
	}
	if err := store.CreateRoleAssignment(bg, &configstoreTables.TableRoleAssignment{ID: "a-1", RoleID: role.ID, UserID: user.ID, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("failed to create assignment: %v", err)
	}

	rbac := NewRBACMiddleware(store)
	rbac.SetEnabled(true) // enabled + ≥1 assignment ⇒ enforcing

	run := func(prepare func(ctx *fasthttp.RequestCtx)) (*fasthttp.RequestCtx, bool) {
		nextCalled := false
		ctx := newRequestCtx("POST", "/api/providers")
		prepare(ctx)
		rbac.Middleware()(func(ctx *fasthttp.RequestCtx) { nextCalled = true })(ctx)
		return ctx, nextCalled
	}

	// Key-authenticated request: RBAC must skip (scopes already enforced).
	ctx, nextCalled := run(func(ctx *fasthttp.RequestCtx) {
		ctx.SetUserValue(schemas.IsAPIKeyAuthContextKey, true)
		ctx.SetUserValue(schemas.APIKeyIDContextKey, "k-1")
	})
	if !nextCalled {
		t.Fatalf("RBAC must skip key-authenticated requests, got %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}

	// Regression: a session user WITHOUT the permission is still denied.
	ctx, nextCalled = run(func(ctx *fasthttp.RequestCtx) {
		ctx.SetUserValue(schemas.BifrostContextKeyUserID, user.ID)
	})
	if nextCalled {
		t.Fatal("RBAC must still enforce for session users after adding the key-auth skip")
	}
	if ctx.Response.StatusCode() != fasthttp.StatusForbidden {
		t.Fatalf("expected 403 for unpermitted session user, got %d", ctx.Response.StatusCode())
	}

	// Regression: the local admin bypass is intact.
	_, nextCalled = run(func(ctx *fasthttp.RequestCtx) {
		ctx.SetUserValue(schemas.IsLocalAdminContextKey, true)
	})
	if !nextCalled {
		t.Fatal("local admin must still bypass RBAC")
	}

	// A false IsAPIKeyAuthContextKey value must NOT trigger the skip.
	ctx, nextCalled = run(func(ctx *fasthttp.RequestCtx) {
		ctx.SetUserValue(schemas.IsAPIKeyAuthContextKey, false)
		ctx.SetUserValue(schemas.BifrostContextKeyUserID, user.ID)
	})
	if nextCalled {
		t.Fatal("IsAPIKeyAuthContextKey=false must not bypass RBAC")
	}
	if ctx.Response.StatusCode() != fasthttp.StatusForbidden {
		t.Fatalf("expected 403, got %d", ctx.Response.StatusCode())
	}
}

// (g) Password (Basic and legacy Bearer-base64) auth is unchanged with the API
// key middleware in the chain.
func TestAPIKeyMiddleware_PasswordAuthUnchangedInChain(t *testing.T) {
	store := newAPIKeysTestStore(t)
	creds := base64.StdEncoding.EncodeToString([]byte("admin:password123"))

	for name, header := range map[string]string{
		"basic":                "Basic " + creds,
		"legacy bearer-base64": "Bearer " + creds,
	} {
		t.Run(name, func(t *testing.T) {
			nextCalled := false
			chain := newAuthedTestChain(t, store, func(ctx *fasthttp.RequestCtx) { nextCalled = true })
			ctx := newRequestCtx("GET", "/api/providers")
			ctx.Request.Header.Set("Authorization", header)
			chain(ctx)
			if !nextCalled {
				t.Fatalf("password auth must keep working, got %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
			}
		})
	}

	t.Run("wrong password still 401", func(t *testing.T) {
		nextCalled := false
		chain := newAuthedTestChain(t, store, func(ctx *fasthttp.RequestCtx) { nextCalled = true })
		ctx := newRequestCtx("GET", "/api/providers")
		ctx.Request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:wrong")))
		chain(ctx)
		if nextCalled || ctx.Response.StatusCode() != fasthttp.StatusUnauthorized {
			t.Fatalf("expected 401 for wrong password, got called=%v status=%d", nextCalled, ctx.Response.StatusCode())
		}
	})
}

// rbacResourceForPath must map admin API key CRUD to its own resource before
// the generic governance prefix, so session-user mutations of keys are
// RBAC-gated as "APIKeys".
func TestRBACResourceForPath_APIKeys(t *testing.T) {
	for path, want := range map[string]string{
		"/api/governance/api-keys":            "APIKeys",
		"/api/governance/api-keys/abc/rotate": "APIKeys",
		"/api/governance/roles":               "RBAC",
		"/api/governance/other":               "Governance",
	} {
		if got := rbacResourceForPath(path); got != want {
			t.Errorf("rbacResourceForPath(%q) = %q, want %q", path, got, want)
		}
	}
}
