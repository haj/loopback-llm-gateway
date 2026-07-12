package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// secureSetupTestEnv wires a real SQLite configstore (full migration chain,
// which seeds the system admin role), a live RBAC middleware, and the handler.
type secureSetupTestEnv struct {
	store      configstore.ConfigStore
	middleware *RBACMiddleware
	handler    *SecureSetupHandler
}

func newSecureSetupEnv(t *testing.T) *secureSetupTestEnv {
	t.Helper()
	store := newAuditTestStore(t)
	middleware := NewRBACMiddleware(store)
	h, err := NewSecureSetupHandler(store, middleware, nil)
	require.NoError(t, err)
	return &secureSetupTestEnv{store: store, middleware: middleware, handler: h}
}

func (e *secureSetupTestEnv) createUser(t *testing.T, id string) {
	t.Helper()
	require.NoError(t, e.store.CreateUser(context.Background(), &configstoreTables.TableUser{
		ID:        id,
		Name:      "Operator " + id,
		Email:     id + "@example.com",
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}))
}

type setupStatusResponse struct {
	RBAC struct {
		Enforcing       bool            `json:"enforcing"`
		Source          string          `json:"source"`
		AssignmentCount int64           `json:"assignment_count"`
		RolesSeeded     map[string]bool `json:"roles_seeded"`
	} `json:"rbac"`
	DashboardAuth struct {
		Enabled bool `json:"enabled"`
	} `json:"dashboard_auth"`
	Insecure bool `json:"insecure"`
}

func (e *secureSetupTestEnv) status(t *testing.T) setupStatusResponse {
	t.Helper()
	ctx := auditRequestCtx("GET", "/api/governance/rbac/setup-status", nil)
	e.handler.getSetupStatus(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode(), "body: %s", ctx.Response.Body())
	var resp setupStatusResponse
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	return resp
}

func (e *secureSetupTestEnv) enforce(t *testing.T, body string) *fasthttp.RequestCtx {
	t.Helper()
	ctx := auditRequestCtx("POST", "/api/governance/rbac/enforce", []byte(body))
	e.handler.enforce(ctx)
	return ctx
}

// gatedMutation drives a mutating governance request through the RBAC
// middleware and reports (nextCalled, statusCode).
func gatedMutation(t *testing.T, m *RBACMiddleware, localAdmin bool, userID string) (bool, int) {
	t.Helper()
	ctx := auditRequestCtx("POST", "/api/governance/virtual-keys", []byte(`{}`))
	if localAdmin {
		ctx.SetUserValue(schemas.IsLocalAdminContextKey, true)
	}
	if userID != "" {
		ctx.SetUserValue(schemas.BifrostContextKeyUserID, userID)
	}
	nextCalled := false
	m.Middleware()(func(c *fasthttp.RequestCtx) { nextCalled = true })(ctx)
	return nextCalled, ctx.Response.StatusCode()
}

func TestSecureSetup_FreshStoreReportsFailOpen(t *testing.T) {
	env := newSecureSetupEnv(t)

	status := env.status(t)
	assert.False(t, status.RBAC.Enforcing)
	assert.Equal(t, "none", status.RBAC.Source)
	assert.Zero(t, status.RBAC.AssignmentCount)
	assert.True(t, status.RBAC.RolesSeeded["admin"], "the migration seeds the system admin role")
	assert.False(t, status.RBAC.RolesSeeded["editor"], "editor must NOT be seeded before enforce")
	assert.False(t, status.RBAC.RolesSeeded["viewer"])
	assert.True(t, status.Insecure)

	// Default-off invariant: an anonymous mutating request passes untouched.
	nextCalled, _ := gatedMutation(t, env.middleware, false, "")
	assert.True(t, nextCalled, "fail-open middleware must allow mutations while unconfigured")
}

func TestSecureSetup_EnforceRequiresAnAssignment(t *testing.T) {
	env := newSecureSetupEnv(t)

	ctx := env.enforce(t, `{"enabled": true}`)
	assert.Equal(t, 400, ctx.Response.StatusCode())
	assert.False(t, env.middleware.IsEnabled(), "a rejected enforce must not flip the middleware")

	// Unknown user is also a 400.
	ctx = env.enforce(t, `{"enabled": true, "assign_user_id": "nobody"}`)
	assert.Equal(t, 400, ctx.Response.StatusCode())
	assert.False(t, env.middleware.IsEnabled())

	// Nothing persisted by the failed attempts.
	_, err := env.store.GetConfig(context.Background(), configstoreTables.ConfigRBACEnforcementKey)
	assert.ErrorIs(t, err, configstore.ErrNotFound)
}

func TestSecureSetup_EnforceSeedsRolesAssignsAdminAndFlips(t *testing.T) {
	env := newSecureSetupEnv(t)
	env.createUser(t, "user-1")

	ctx := env.enforce(t, `{"enabled": true, "assign_user_id": "user-1"}`)
	require.Equal(t, 200, ctx.Response.StatusCode(), "body: %s", ctx.Response.Body())
	assert.True(t, env.middleware.IsEnabled())

	// Editor/viewer roles exist with the documented permission sets.
	editor, err := env.store.GetRoleByName(context.Background(), configstoreTables.DefaultEditorRoleName)
	require.NoError(t, err)
	assert.False(t, editor.IsSystem, "convenience roles are operator-editable")
	editorOps := map[string]bool{}
	for _, p := range editor.Permissions {
		assert.Equal(t, configstoreTables.RbacWildcard, p.Resource)
		editorOps[p.Operation] = true
	}
	assert.True(t, editorOps["Create"] && editorOps["Update"] && editorOps["Read"])
	assert.False(t, editorOps["Delete"], "editor must not get Delete")

	viewer, err := env.store.GetRoleByName(context.Background(), configstoreTables.DefaultViewerRoleName)
	require.NoError(t, err)
	viewerOps := map[string]bool{}
	for _, p := range viewer.Permissions {
		viewerOps[p.Operation] = true
	}
	assert.False(t, viewerOps["Create"] || viewerOps["Update"] || viewerOps["Delete"], "viewer is read-only")

	// The admin assignment exists and the flag persisted.
	assignments, err := env.store.GetRoleAssignmentsByUser(context.Background(), "user-1")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	cfg, err := env.store.GetConfig(context.Background(), configstoreTables.ConfigRBACEnforcementKey)
	require.NoError(t, err)
	assert.Equal(t, "true", cfg.Value)

	// Status now reports enforcing via the persisted flag.
	status := env.status(t)
	assert.True(t, status.RBAC.Enforcing)
	assert.Equal(t, "config", status.RBAC.Source)
	assert.Equal(t, int64(1), status.RBAC.AssignmentCount)
	assert.True(t, status.RBAC.RolesSeeded["editor"])

	// Idempotency: a second enforce changes nothing and does not error.
	ctx = env.enforce(t, `{"enabled": true, "assign_user_id": "user-1"}`)
	require.Equal(t, 200, ctx.Response.StatusCode())
	assignments, err = env.store.GetRoleAssignmentsByUser(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Len(t, assignments, 1, "re-enforce must not duplicate assignments")
	_, total, err := env.store.GetRoles(context.Background(), configstore.RolesQueryParams{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total, "admin + editor + viewer, no duplicates")

	// An audit trail entry recorded the enforcement.
	logs, _, err := env.store.GetAuditLogs(context.Background(), configstore.AuditLogsQueryParams{Action: AuditActionRBACEnforce})
	require.NoError(t, err)
	assert.NotEmpty(t, logs)
}

func TestSecureSetup_PersistedFlagSurvivesRestart(t *testing.T) {
	env := newSecureSetupEnv(t)
	env.createUser(t, "user-1")
	ctx := env.enforce(t, `{"enabled": true, "assign_user_id": "user-1"}`)
	require.Equal(t, 200, ctx.Response.StatusCode())

	// A fresh middleware over the same store — the restart path — reads the
	// persisted flag as enabled.
	fresh := NewRBACMiddleware(env.store)
	assert.True(t, fresh.IsEnabled(), "a fresh middleware must pick up the persisted enforcement flag")
}

func TestSecureSetup_EnforcementGatesButLocalAdminBypasses(t *testing.T) {
	env := newSecureSetupEnv(t)
	env.createUser(t, "user-1")
	ctx := env.enforce(t, `{"enabled": true, "assign_user_id": "user-1"}`)
	require.Equal(t, 200, ctx.Response.StatusCode())

	// Local admin always passes.
	nextCalled, _ := gatedMutation(t, env.middleware, true, "")
	assert.True(t, nextCalled, "local admin must bypass RBAC")

	// An unattributed principal on a gated mutating route is denied.
	nextCalled, statusCode := gatedMutation(t, env.middleware, false, "")
	assert.False(t, nextCalled)
	assert.Equal(t, 403, statusCode)

	// The assigned admin user passes.
	nextCalled, _ = gatedMutation(t, env.middleware, false, "user-1")
	assert.True(t, nextCalled, "the admin-assigned user must be allowed")
}

func TestSecureSetup_DisableResetsFlagAndMiddleware(t *testing.T) {
	env := newSecureSetupEnv(t)
	env.createUser(t, "user-1")
	ctx := env.enforce(t, `{"enabled": true, "assign_user_id": "user-1"}`)
	require.Equal(t, 200, ctx.Response.StatusCode())
	require.True(t, env.middleware.IsEnabled())

	ctx = env.enforce(t, `{"enabled": false}`)
	require.Equal(t, 200, ctx.Response.StatusCode(), "body: %s", ctx.Response.Body())
	assert.False(t, env.middleware.IsEnabled())

	cfg, err := env.store.GetConfig(context.Background(), configstoreTables.ConfigRBACEnforcementKey)
	require.NoError(t, err)
	assert.Equal(t, "false", cfg.Value)

	// A restart honors the disable.
	fresh := NewRBACMiddleware(env.store)
	assert.False(t, fresh.IsEnabled())
}

func TestSecureSetup_DisableBlockedWhenEnvPinsEnforcement(t *testing.T) {
	t.Setenv("LOOPBACK_RBAC_ENABLED", "true")
	env := newSecureSetupEnv(t)

	ctx := env.enforce(t, `{"enabled": false}`)
	assert.Equal(t, 409, ctx.Response.StatusCode(), "env-pinned enforcement must not be disableable from the UI")
	assert.True(t, env.middleware.IsEnabled())

	status := env.status(t)
	assert.Equal(t, "env", status.RBAC.Source)
}
