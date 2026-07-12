// This file contains HTTP handlers for the Loopback Gateway RBAC & access
// control UI. It exposes CRUD over roles (with their inline permission grants)
// and role assignments that bind a role to a managed user (configstore.TableUser).
//
// It follows the usermanagement.go / guardrails.go handler patterns
// (RegisterRoutes + per-resource CRUD with SendJSON/SendError) and uses the
// ConfigStore RBAC methods added alongside this handler.
package handlers

import (
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

// RBACHandler manages HTTP requests for roles, permissions and role assignments.
type RBACHandler struct {
	configStore configstore.ConfigStore
	// rbacMiddleware is the live enforcement instance; getMyPermissions mirrors
	// its runtime state (env OR persisted flag OR secure-setup toggle) instead
	// of re-deriving from env alone. May be nil (tests): falls back to env.
	rbacMiddleware *RBACMiddleware
}

// NewRBACHandler creates an RBAC handler. rbacMiddleware may be nil (tests).
func NewRBACHandler(configStore configstore.ConfigStore, rbacMiddleware *RBACMiddleware) (*RBACHandler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &RBACHandler{configStore: configStore, rbacMiddleware: rbacMiddleware}, nil
}

// RegisterRoutes wires the role and role-assignment CRUD endpoints.
func (h *RBACHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/roles", lib.ChainMiddlewares(h.getRoles, middlewares...))
	r.POST("/api/governance/roles", lib.ChainMiddlewares(h.createRole, middlewares...))
	r.GET("/api/governance/roles/{id}", lib.ChainMiddlewares(h.getRole, middlewares...))
	r.PUT("/api/governance/roles/{id}", lib.ChainMiddlewares(h.updateRole, middlewares...))
	r.DELETE("/api/governance/roles/{id}", lib.ChainMiddlewares(h.deleteRole, middlewares...))

	r.GET("/api/governance/role-assignments", lib.ChainMiddlewares(h.getRoleAssignments, middlewares...))
	r.POST("/api/governance/role-assignments", lib.ChainMiddlewares(h.createRoleAssignment, middlewares...))
	r.DELETE("/api/governance/role-assignments/{id}", lib.ChainMiddlewares(h.deleteRoleAssignment, middlewares...))

	// Self permissions: lets the dashboard render the same allow/deny decisions
	// the RBAC middleware enforces. A GET, so it is never gated by RBAC.
	r.GET("/api/governance/rbac/permissions/me", lib.ChainMiddlewares(h.getMyPermissions, middlewares...))
}

// getMyPermissions returns the acting principal's effective RBAC state so the UI
// can mirror server-side enforcement. It is intentionally permissive in exactly
// the same cases the middleware is: RBAC disabled, local admin, or no
// assignments configured all return allow_all=true.
func (h *RBACHandler) getMyPermissions(ctx *fasthttp.RequestCtx) {
	// Mirror the live middleware (env OR persisted flag OR runtime toggle) so
	// the dashboard reflects a one-click enforce without a restart.
	enabled := rbacEnabledFromEnv()
	if h.rbacMiddleware != nil {
		enabled = h.rbacMiddleware.IsEnabled()
	}
	isLocalAdmin := false
	if v, ok := ctx.UserValue(schemas.IsLocalAdminContextKey).(bool); ok {
		isLocalAdmin = v
	}

	resp := map[string]any{
		"enabled":     enabled,
		"allow_all":   true,
		"permissions": []permissionInput{},
	}

	// Fully permissive when RBAC is off or the caller is the local admin.
	if !enabled || isLocalAdmin {
		SendJSON(ctx, resp)
		return
	}

	// Permissive until at least one assignment exists.
	count, err := h.configStore.CountRoleAssignments(ctx)
	if err != nil || count == 0 {
		SendJSON(ctx, resp)
		return
	}

	userID, _ := ctx.UserValue(schemas.BifrostContextKeyUserID).(string)
	userID = strings.TrimSpace(userID)
	if userID == "" {
		// Enabled and configured, but principal is unidentifiable: deny by default
		// (matches the middleware's fail-closed branch).
		resp["allow_all"] = false
		SendJSON(ctx, resp)
		return
	}

	assignments, err := h.configStore.GetRoleAssignmentsByUser(ctx, userID)
	if err != nil {
		// Mirror the middleware's fail-open on store error.
		SendJSON(ctx, resp)
		return
	}
	perms := make([]permissionInput, 0)
	seen := make(map[string]bool)
	for i := range assignments {
		role := assignments[i].Role
		if role == nil {
			continue
		}
		for j := range role.Permissions {
			p := role.Permissions[j]
			key := p.Resource + "\x00" + p.Operation
			if seen[key] {
				continue
			}
			seen[key] = true
			perms = append(perms, permissionInput{Resource: p.Resource, Operation: p.Operation})
		}
	}
	resp["allow_all"] = false
	resp["permissions"] = perms
	SendJSON(ctx, resp)
}

// ---- request payloads ----

type permissionInput struct {
	Resource  string `json:"resource"`
	Operation string `json:"operation"`
}

type createRoleRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Permissions []permissionInput `json:"permissions,omitempty"`
}

type updateRoleRequest struct {
	Name        *string            `json:"name,omitempty"`
	Description *string            `json:"description,omitempty"`
	Permissions *[]permissionInput `json:"permissions,omitempty"`
}

type createRoleAssignmentRequest struct {
	RoleID string `json:"role_id"`
	UserID string `json:"user_id"`
}

// ---- role handlers ----

func (h *RBACHandler) getRoles(ctx *fasthttp.RequestCtx) {
	params := configstore.RolesQueryParams{
		Search: string(ctx.QueryArgs().Peek("search")),
	}
	if v := string(ctx.QueryArgs().Peek("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			SendError(ctx, 400, "Invalid limit parameter: must be a non-negative number")
			return
		}
		params.Limit = n
	}
	if v := string(ctx.QueryArgs().Peek("offset")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			SendError(ctx, 400, "Invalid offset parameter: must be a non-negative number")
			return
		}
		params.Offset = n
	}

	roles, total, err := h.configStore.GetRoles(ctx, params)
	if err != nil {
		logger.Error("failed to retrieve roles: %v", err)
		SendError(ctx, 500, "Failed to retrieve roles")
		return
	}
	SendJSON(ctx, map[string]any{
		"roles":  roles,
		"total":  total,
		"count":  len(roles),
		"offset": params.Offset,
	})
}

func (h *RBACHandler) getRole(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	role, err := h.configStore.GetRole(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Role not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve role")
		return
	}
	SendJSON(ctx, map[string]any{"role": role})
}

func (h *RBACHandler) createRole(ctx *fasthttp.RequestCtx) {
	var req createRoleRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		SendError(ctx, 400, "name is required")
		return
	}
	perms, errMsg := normalizePermissions(req.Permissions)
	if errMsg != "" {
		SendError(ctx, 400, errMsg)
		return
	}

	var role configstoreTables.TableRole
	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		role = configstoreTables.TableRole{
			ID:          uuid.NewString(),
			Name:        req.Name,
			Description: req.Description,
			IsSystem:    false,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := h.configStore.CreateRole(ctx, &role, tx); err != nil {
			return err
		}
		return h.configStore.ReplaceRolePermissions(ctx, role.ID, stampPermissions(role.ID, perms), tx)
	}); err != nil {
		if errors.Is(err, configstore.ErrAlreadyExists) {
			SendError(ctx, 409, "A role with this name already exists")
			return
		}
		logger.Error("failed to create role: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to create role: %v", err))
		return
	}
	created, err := h.configStore.GetRole(ctx, role.ID)
	if err != nil {
		created = &role
	}
	SendJSON(ctx, map[string]any{
		"message": "Role created successfully",
		"role":    created,
	})
}

func (h *RBACHandler) updateRole(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var req updateRoleRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	role, err := h.configStore.GetRole(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Role not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve role")
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			SendError(ctx, 400, "name cannot be empty")
			return
		}
		// Renaming a system role would break the seed lookup that guarantees a
		// full-access role always exists.
		if role.IsSystem && strings.TrimSpace(*req.Name) != role.Name {
			SendError(ctx, 400, "the built-in system role cannot be renamed")
			return
		}
		role.Name = *req.Name
	}
	if req.Description != nil {
		role.Description = *req.Description
	}

	var perms []configstoreTables.TablePermission
	if req.Permissions != nil {
		normalized, errMsg := normalizePermissions(*req.Permissions)
		if errMsg != "" {
			SendError(ctx, 400, errMsg)
			return
		}
		perms = normalized
	}

	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		role.UpdatedAt = time.Now()
		if err := h.configStore.UpdateRole(ctx, role, tx); err != nil {
			return err
		}
		if req.Permissions != nil {
			return h.configStore.ReplaceRolePermissions(ctx, role.ID, stampPermissions(role.ID, perms), tx)
		}
		return nil
	}); err != nil {
		if errors.Is(err, configstore.ErrAlreadyExists) {
			SendError(ctx, 409, "A role with this name already exists")
			return
		}
		logger.Error("failed to update role: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to update role: %v", err))
		return
	}
	updated, err := h.configStore.GetRole(ctx, role.ID)
	if err != nil {
		updated = role
	}
	SendJSON(ctx, map[string]any{
		"message": "Role updated successfully",
		"role":    updated,
	})
}

func (h *RBACHandler) deleteRole(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	role, err := h.configStore.GetRole(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Role not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve role")
		return
	}
	// The built-in system role must remain so a full-access role always exists.
	if role.IsSystem {
		SendError(ctx, 400, "the built-in system role cannot be deleted")
		return
	}
	if err := h.configStore.DeleteRole(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Role not found")
			return
		}
		logger.Error("failed to delete role: %v", err)
		SendError(ctx, 500, "Failed to delete role")
		return
	}
	SendJSON(ctx, map[string]any{"message": "Role deleted successfully"})
}

// ---- role assignment handlers ----

func (h *RBACHandler) getRoleAssignments(ctx *fasthttp.RequestCtx) {
	params := configstore.RoleAssignmentsQueryParams{
		UserID: string(ctx.QueryArgs().Peek("user_id")),
		RoleID: string(ctx.QueryArgs().Peek("role_id")),
	}
	if v := string(ctx.QueryArgs().Peek("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			SendError(ctx, 400, "Invalid limit parameter: must be a non-negative number")
			return
		}
		params.Limit = n
	}
	if v := string(ctx.QueryArgs().Peek("offset")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			SendError(ctx, 400, "Invalid offset parameter: must be a non-negative number")
			return
		}
		params.Offset = n
	}

	assignments, total, err := h.configStore.GetRoleAssignments(ctx, params)
	if err != nil {
		logger.Error("failed to retrieve role assignments: %v", err)
		SendError(ctx, 500, "Failed to retrieve role assignments")
		return
	}
	SendJSON(ctx, map[string]any{
		"role_assignments": assignments,
		"total":            total,
		"count":            len(assignments),
		"offset":           params.Offset,
	})
}

func (h *RBACHandler) createRoleAssignment(ctx *fasthttp.RequestCtx) {
	var req createRoleAssignmentRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if strings.TrimSpace(req.RoleID) == "" {
		SendError(ctx, 400, "role_id is required")
		return
	}
	if strings.TrimSpace(req.UserID) == "" {
		SendError(ctx, 400, "user_id is required")
		return
	}
	// Validate that both the role and the user exist before binding them.
	if _, err := h.configStore.GetRole(ctx, req.RoleID); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 400, fmt.Sprintf("role %q not found", req.RoleID))
			return
		}
		SendError(ctx, 500, "Failed to validate role")
		return
	}
	if _, err := h.configStore.GetUser(ctx, req.UserID); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 400, fmt.Sprintf("user %q not found", req.UserID))
			return
		}
		SendError(ctx, 500, "Failed to validate user")
		return
	}

	assignment := &configstoreTables.TableRoleAssignment{
		ID:        uuid.NewString(),
		RoleID:    req.RoleID,
		UserID:    req.UserID,
		CreatedAt: time.Now(),
	}
	if err := h.configStore.CreateRoleAssignment(ctx, assignment); err != nil {
		if errors.Is(err, configstore.ErrAlreadyExists) {
			SendError(ctx, 409, "This user already has that role")
			return
		}
		logger.Error("failed to create role assignment: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to create role assignment: %v", err))
		return
	}
	created, err := h.configStore.GetRoleAssignment(ctx, assignment.ID)
	if err != nil {
		created = assignment
	}
	SendJSON(ctx, map[string]any{
		"message":         "Role assignment created successfully",
		"role_assignment": created,
	})
}

func (h *RBACHandler) deleteRoleAssignment(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if err := h.configStore.DeleteRoleAssignment(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Role assignment not found")
			return
		}
		logger.Error("failed to delete role assignment: %v", err)
		SendError(ctx, 500, "Failed to delete role assignment")
		return
	}
	SendJSON(ctx, map[string]any{"message": "Role assignment deleted successfully"})
}

// ---- helpers ----

// normalizePermissions validates and de-duplicates the permission inputs. The
// second return value is a non-empty error message on validation failure.
func normalizePermissions(in []permissionInput) ([]configstoreTables.TablePermission, string) {
	out := make([]configstoreTables.TablePermission, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, p := range in {
		resource := strings.TrimSpace(p.Resource)
		operation := strings.TrimSpace(p.Operation)
		if resource == "" {
			return nil, "each permission must have a resource"
		}
		if !configstoreTables.IsValidRbacOperation(operation) {
			return nil, fmt.Sprintf("invalid permission operation %q", operation)
		}
		key := resource + "\x00" + operation
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, configstoreTables.TablePermission{
			Resource:  resource,
			Operation: operation,
		})
	}
	return out, ""
}

// stampPermissions assigns a fresh ID, the owning role ID, and a creation
// timestamp to each permission before insert.
func stampPermissions(roleID string, perms []configstoreTables.TablePermission) []configstoreTables.TablePermission {
	now := time.Now()
	out := make([]configstoreTables.TablePermission, 0, len(perms))
	for _, p := range perms {
		out = append(out, configstoreTables.TablePermission{
			ID:        uuid.NewString(),
			RoleID:    roleID,
			Resource:  p.Resource,
			Operation: p.Operation,
			CreatedAt: now,
		})
	}
	return out
}
