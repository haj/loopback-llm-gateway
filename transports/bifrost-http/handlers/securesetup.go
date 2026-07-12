// This file contains the secure-setup handler: a
// setup-status endpoint that detects insecure defaults (RBAC fail-open,
// dashboard auth disabled, inference auth off, no users/assignments) and a
// one-click enforce endpoint that seeds the convenience editor/viewer roles,
// creates an admin-role assignment, persists the enforcement flag, and flips
// the live RBAC middleware — all in one transaction.
//
// SAFETY:
//   - Everything is additive and opt-in. No migration seeds rows; nothing
//     changes until the operator clicks enforce.
//   - The local-admin bypass in rbac_middleware.go is untouched, so enforce
//     can never lock the operator out.
//   - Enabling requires at least one role assignment (existing or created via
//     assign_user_id) because enforcement is inert with zero assignments —
//     silently "enforcing" nothing would be security theater.
//   - Disabling clears the persisted flag but cannot override a truthy
//     LOOPBACK_RBAC_ENABLED env var (reported as source "env", 409 on
//     attempts) so infrastructure-pinned enforcement stays pinned.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

// SecureSetupHandler serves the guided secure-setup flow.
type SecureSetupHandler struct {
	configStore    configstore.ConfigStore
	rbacMiddleware *RBACMiddleware
	// clientConfig resolves the live client config for the inference-auth and
	// CORS signals. May be nil (tests): those signals then report their
	// zero values.
	clientConfig func() *configstore.ClientConfig
}

// NewSecureSetupHandler creates a secure-setup handler.
func NewSecureSetupHandler(configStore configstore.ConfigStore, rbacMiddleware *RBACMiddleware, clientConfig func() *configstore.ClientConfig) (*SecureSetupHandler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	if rbacMiddleware == nil {
		return nil, fmt.Errorf("rbac middleware is required")
	}
	return &SecureSetupHandler{configStore: configStore, rbacMiddleware: rbacMiddleware, clientConfig: clientConfig}, nil
}

// RegisterRoutes wires the setup-status and enforce endpoints.
func (h *SecureSetupHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/rbac/setup-status", lib.ChainMiddlewares(h.getSetupStatus, middlewares...))
	r.POST("/api/governance/rbac/enforce", lib.ChainMiddlewares(h.enforce, middlewares...))
}

// editorPermissions / viewerPermissions are the permission sets seeded for the
// convenience roles. Editor gets everything except Delete; viewer is
// read-only. Both are wildcard-resource.
func editorPermissions() []configstoreTables.TablePermission {
	return []configstoreTables.TablePermission{
		{Resource: configstoreTables.RbacWildcard, Operation: configstoreTables.RbacOperationRead},
		{Resource: configstoreTables.RbacWildcard, Operation: configstoreTables.RbacOperationView},
		{Resource: configstoreTables.RbacWildcard, Operation: configstoreTables.RbacOperationDownload},
		{Resource: configstoreTables.RbacWildcard, Operation: configstoreTables.RbacOperationCreate},
		{Resource: configstoreTables.RbacWildcard, Operation: configstoreTables.RbacOperationUpdate},
	}
}

func viewerPermissions() []configstoreTables.TablePermission {
	return []configstoreTables.TablePermission{
		{Resource: configstoreTables.RbacWildcard, Operation: configstoreTables.RbacOperationRead},
		{Resource: configstoreTables.RbacWildcard, Operation: configstoreTables.RbacOperationView},
		{Resource: configstoreTables.RbacWildcard, Operation: configstoreTables.RbacOperationDownload},
	}
}

// roleExists reports whether a role with the given name exists.
func (h *SecureSetupHandler) roleExists(ctx context.Context, name string) (bool, error) {
	_, err := h.configStore.GetRoleByName(ctx, name)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// persistedEnforcement reads the stored flag (fail-open on missing/error).
func (h *SecureSetupHandler) persistedEnforcement(ctx context.Context) bool {
	cfg, err := h.configStore.GetConfig(ctx, configstoreTables.ConfigRBACEnforcementKey)
	if err != nil {
		return false
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(cfg.Value))
	return err == nil && enabled
}

// getSetupStatus reports the deployment's security posture. Never gated (GET).
func (h *SecureSetupHandler) getSetupStatus(ctx *fasthttp.RequestCtx) {
	enforcing := h.rbacMiddleware.IsEnabled()
	source := "none"
	if rbacEnabledFromEnv() {
		source = "env"
	} else if h.persistedEnforcement(ctx) {
		source = "config"
	}

	assignmentCount, err := h.configStore.CountRoleAssignments(ctx)
	if err != nil {
		logger.Warn("secure-setup: failed to count role assignments: %v", err)
	}

	rolesSeeded := map[string]bool{}
	for _, name := range []string{configstoreTables.DefaultAdminRoleName, configstoreTables.DefaultEditorRoleName, configstoreTables.DefaultViewerRoleName} {
		exists, err := h.roleExists(ctx, name)
		if err != nil {
			logger.Warn("secure-setup: failed to check role %s: %v", name, err)
		}
		rolesSeeded[name] = exists
	}

	dashboardAuth := false
	if cfg, err := h.configStore.GetConfig(ctx, configstoreTables.ConfigIsAuthEnabledKey); err == nil {
		if v, err := strconv.ParseBool(strings.TrimSpace(cfg.Value)); err == nil {
			dashboardAuth = v
		}
	}

	inferenceAuth := false
	corsRestricted := true // OSS CORS allows localhost + configured origins only
	var userCount int64
	if h.clientConfig != nil {
		if cc := h.clientConfig(); cc != nil {
			inferenceAuth = cc.EnforceAuthOnInference
		}
	}
	if _, total, err := h.configStore.GetUsers(ctx, configstore.UsersQueryParams{Limit: 1}); err == nil {
		userCount = total
	}

	// The headline signal: any of these means the management plane is more
	// open than a production deployment should be.
	insecure := !enforcing || !dashboardAuth || !inferenceAuth

	SendJSON(ctx, map[string]any{
		"rbac": map[string]any{
			"enforcing":        enforcing,
			"source":           source,
			"assignment_count": assignmentCount,
			"roles_seeded":     rolesSeeded,
		},
		"dashboard_auth": map[string]any{"enabled": dashboardAuth},
		"inference_auth": map[string]any{"enforced": inferenceAuth},
		"cors":           map[string]any{"restricted": corsRestricted},
		"users":          map[string]any{"count": userCount},
		"insecure":       insecure,
	})
}

// enforceInput is the POST body.
type enforceInput struct {
	Enabled      bool   `json:"enabled"`
	AssignUserID string `json:"assign_user_id,omitempty"`
}

// enforce enables (or disables) RBAC enforcement. Enabling seeds the
// editor/viewer convenience roles idempotently, creates an admin-role
// assignment when assign_user_id is given, persists the flag, and flips the
// live middleware — one transaction, then the runtime toggle.
func (h *SecureSetupHandler) enforce(ctx *fasthttp.RequestCtx) {
	var req enforceInput
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}

	if !req.Enabled {
		if rbacEnabledFromEnv() {
			SendError(ctx, 409, "RBAC enforcement is pinned on by the LOOPBACK_RBAC_ENABLED environment variable and cannot be disabled from the UI")
			return
		}
		if err := h.configStore.UpdateConfig(ctx, &configstoreTables.TableGovernanceConfig{
			Key:   configstoreTables.ConfigRBACEnforcementKey,
			Value: "false",
		}); err != nil {
			logger.Error("secure-setup: failed to persist rbac disable: %v", err)
			SendError(ctx, 500, "Failed to persist RBAC enforcement state")
			return
		}
		h.rbacMiddleware.SetEnabled(false)
		recordAudit(ctx, h.configStore, AuditActionRBACEnforce, configstoreTables.AuditOutcomeSuccess, "enabled=false")
		SendJSON(ctx, map[string]any{"message": "RBAC enforcement disabled", "enforcing": false})
		return
	}

	// Enabling. Enforcement with zero assignments is inert (the middleware
	// stays fail-open), so require an existing assignment or one to create.
	assignmentCount, err := h.configStore.CountRoleAssignments(ctx)
	if err != nil {
		logger.Error("secure-setup: failed to count role assignments: %v", err)
		SendError(ctx, 500, "Failed to inspect role assignments")
		return
	}
	req.AssignUserID = strings.TrimSpace(req.AssignUserID)
	if assignmentCount == 0 && req.AssignUserID == "" {
		SendError(ctx, 400, "Enforcement requires at least one role assignment: pass assign_user_id to grant a managed user the admin role")
		return
	}

	var assignUser *configstoreTables.TableUser
	if req.AssignUserID != "" {
		assignUser, err = h.configStore.GetUser(ctx, req.AssignUserID)
		if err != nil {
			if errors.Is(err, configstore.ErrNotFound) {
				SendError(ctx, 400, fmt.Sprintf("user %q does not exist", req.AssignUserID))
				return
			}
			logger.Error("secure-setup: failed to load user %s: %v", req.AssignUserID, err)
			SendError(ctx, 500, "Failed to verify user")
			return
		}
	}

	adminRole, err := h.configStore.GetRoleByName(ctx, configstoreTables.DefaultAdminRoleName)
	if err != nil {
		logger.Error("secure-setup: failed to load admin role: %v", err)
		SendError(ctx, 500, "Failed to load the seeded admin role")
		return
	}

	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// Seed editor/viewer idempotently (IsSystem=false: operators may edit
		// or delete them; a re-run must not resurrect or duplicate).
		for _, seed := range []struct {
			name        string
			description string
			perms       []configstoreTables.TablePermission
		}{
			{configstoreTables.DefaultEditorRoleName, "Convenience role seeded by secure setup: read and modify everything except deletions.", editorPermissions()},
			{configstoreTables.DefaultViewerRoleName, "Convenience role seeded by secure setup: read-only access.", viewerPermissions()},
		} {
			exists, err := h.roleExists(ctx, seed.name)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			role := &configstoreTables.TableRole{
				ID:          uuid.NewString(),
				Name:        seed.name,
				Description: seed.description,
				IsSystem:    false,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			if err := h.configStore.CreateRole(ctx, role, tx); err != nil {
				return err
			}
			if err := h.configStore.ReplaceRolePermissions(ctx, role.ID, stampPermissions(role.ID, seed.perms), tx); err != nil {
				return err
			}
		}

		// Admin assignment for the chosen user (idempotent: skip when it
		// already holds the admin role).
		if assignUser != nil {
			existing, err := h.configStore.GetRoleAssignmentsByUser(ctx, assignUser.ID)
			if err != nil {
				return err
			}
			alreadyAdmin := false
			for i := range existing {
				if existing[i].RoleID == adminRole.ID {
					alreadyAdmin = true
					break
				}
			}
			if !alreadyAdmin {
				if err := h.configStore.CreateRoleAssignment(ctx, &configstoreTables.TableRoleAssignment{
					ID:        uuid.NewString(),
					RoleID:    adminRole.ID,
					UserID:    assignUser.ID,
					CreatedAt: time.Now(),
				}, tx); err != nil {
					return err
				}
			}
		}

		return h.configStore.UpdateConfig(ctx, &configstoreTables.TableGovernanceConfig{
			Key:   configstoreTables.ConfigRBACEnforcementKey,
			Value: "true",
		}, tx)
	}); err != nil {
		logger.Error("secure-setup: enforce transaction failed: %v", err)
		SendError(ctx, 500, "Failed to enable RBAC enforcement")
		return
	}

	// Only flip the live middleware after everything is durably committed.
	h.rbacMiddleware.SetEnabled(true)
	recordAudit(ctx, h.configStore, AuditActionRBACEnforce, configstoreTables.AuditOutcomeSuccess, "enabled=true")

	SendJSON(ctx, map[string]any{
		"message":   "RBAC enforcement enabled. The local admin always retains access.",
		"enforcing": true,
	})
}
