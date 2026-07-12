// This file contains HTTP handlers for the Loopback Gateway user & org
// management UI. It exposes CRUD over managed users
// (configstore.TableUser) and flat business units
// (configstore.TableBusinessUnit). Both entities reuse the shared governance
// budget / rate-limit tables by FK pointer; users may additionally be attached
// to one or more virtual keys.
//
// It follows the guardrails.go handler patterns (RegisterRoutes + per-resource
// CRUD with SendJSON/SendError) and reuses validateRateLimit from governance.go.
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

// UserManagementHandler manages HTTP requests for users and business units.
type UserManagementHandler struct {
	configStore configstore.ConfigStore
}

// NewUserManagementHandler creates a user & org management handler.
func NewUserManagementHandler(configStore configstore.ConfigStore) (*UserManagementHandler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &UserManagementHandler{configStore: configStore}, nil
}

// RegisterRoutes wires the user and business-unit CRUD endpoints.
func (h *UserManagementHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/users", lib.ChainMiddlewares(h.getUsers, middlewares...))
	r.POST("/api/governance/users", lib.ChainMiddlewares(h.createUser, middlewares...))
	r.GET("/api/governance/users/{id}", lib.ChainMiddlewares(h.getUser, middlewares...))
	r.PUT("/api/governance/users/{id}", lib.ChainMiddlewares(h.updateUser, middlewares...))
	r.DELETE("/api/governance/users/{id}", lib.ChainMiddlewares(h.deleteUser, middlewares...))

	r.GET("/api/governance/business-units", lib.ChainMiddlewares(h.getBusinessUnits, middlewares...))
	r.POST("/api/governance/business-units", lib.ChainMiddlewares(h.createBusinessUnit, middlewares...))
	r.GET("/api/governance/business-units/{id}", lib.ChainMiddlewares(h.getBusinessUnit, middlewares...))
	r.PUT("/api/governance/business-units/{id}", lib.ChainMiddlewares(h.updateBusinessUnit, middlewares...))
	r.DELETE("/api/governance/business-units/{id}", lib.ChainMiddlewares(h.deleteBusinessUnit, middlewares...))
}

// ---- request payloads ----

// budgetInput is the create/update shape for an entity's referenced budget. A
// zero MaxLimit with an empty ResetDuration on update signals removal.
type budgetInput struct {
	MaxLimit      float64 `json:"max_limit"`
	ResetDuration string  `json:"reset_duration"`
}

// rateLimitInput is the create/update shape for an entity's referenced rate
// limit. All-nil limit fields on update signal removal.
type rateLimitInput struct {
	TokenMaxLimit        *int64  `json:"token_max_limit,omitempty"`
	TokenResetDuration   *string `json:"token_reset_duration,omitempty"`
	RequestMaxLimit      *int64  `json:"request_max_limit,omitempty"`
	RequestResetDuration *string `json:"request_reset_duration,omitempty"`
}

type createUserRequest struct {
	Name           string          `json:"name"`
	Email          string          `json:"email"`
	Status         string          `json:"status,omitempty"`
	BusinessUnitID *string         `json:"business_unit_id,omitempty"`
	VirtualKeyIDs  []string        `json:"virtual_key_ids,omitempty"`
	Budget         *budgetInput    `json:"budget,omitempty"`
	RateLimit      *rateLimitInput `json:"rate_limit,omitempty"`
}

type updateUserRequest struct {
	Name           *string         `json:"name,omitempty"`
	Email          *string         `json:"email,omitempty"`
	Status         *string         `json:"status,omitempty"`
	BusinessUnitID *string         `json:"business_unit_id,omitempty"`
	VirtualKeyIDs  *[]string       `json:"virtual_key_ids,omitempty"`
	Budget         *budgetInput    `json:"budget,omitempty"`
	RateLimit      *rateLimitInput `json:"rate_limit,omitempty"`
}

type createBusinessUnitRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Budget      *budgetInput    `json:"budget,omitempty"`
	RateLimit   *rateLimitInput `json:"rate_limit,omitempty"`
}

type updateBusinessUnitRequest struct {
	Name        *string         `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	Budget      *budgetInput    `json:"budget,omitempty"`
	RateLimit   *rateLimitInput `json:"rate_limit,omitempty"`
}

// ---- user handlers ----

func (h *UserManagementHandler) getUsers(ctx *fasthttp.RequestCtx) {
	params := configstore.UsersQueryParams{
		Search:         string(ctx.QueryArgs().Peek("search")),
		Status:         string(ctx.QueryArgs().Peek("status")),
		BusinessUnitID: string(ctx.QueryArgs().Peek("business_unit_id")),
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

	users, total, err := h.configStore.GetUsers(ctx, params)
	if err != nil {
		logger.Error("failed to retrieve users: %v", err)
		SendError(ctx, 500, "Failed to retrieve users")
		return
	}
	SendJSON(ctx, map[string]any{
		"users":  users,
		"total":  total,
		"count":  len(users),
		"offset": params.Offset,
	})
}

func (h *UserManagementHandler) getUser(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	user, err := h.configStore.GetUser(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "User not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve user")
		return
	}
	SendJSON(ctx, map[string]any{"user": user})
}

func (h *UserManagementHandler) createUser(ctx *fasthttp.RequestCtx) {
	var req createUserRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		SendError(ctx, 400, "name is required")
		return
	}
	if strings.TrimSpace(req.Email) == "" {
		SendError(ctx, 400, "email is required")
		return
	}
	status := req.Status
	if status == "" {
		status = configstoreTables.UserStatusActive
	}
	if !configstoreTables.IsValidUserStatus(status) {
		SendError(ctx, 400, fmt.Sprintf("Invalid status %q", status))
		return
	}
	if err := h.validateAttachments(ctx, req.BusinessUnitID, req.VirtualKeyIDs); err != "" {
		SendError(ctx, 400, err)
		return
	}

	var user configstoreTables.TableUser
	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		budgetID, err := h.applyBudget(ctx, tx, nil, req.Budget)
		if err != nil {
			return err
		}
		rateLimitID, err := h.applyRateLimit(ctx, tx, nil, req.RateLimit)
		if err != nil {
			return err
		}
		user = configstoreTables.TableUser{
			ID:             uuid.NewString(),
			Name:           req.Name,
			Email:          req.Email,
			Status:         status,
			BusinessUnitID: req.BusinessUnitID,
			BudgetID:       budgetID,
			RateLimitID:    rateLimitID,
			VirtualKeyIDs:  req.VirtualKeyIDs,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		return h.configStore.CreateUser(ctx, &user, tx)
	}); err != nil {
		if errors.Is(err, configstore.ErrAlreadyExists) {
			SendError(ctx, 409, "A user with this email already exists")
			return
		}
		logger.Error("failed to create user: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to create user: %v", err))
		return
	}
	created, err := h.configStore.GetUser(ctx, user.ID)
	if err != nil {
		created = &user
	}
	recordAudit(ctx, h.configStore, AuditActionUserCreate, configstoreTables.AuditOutcomeSuccess, user.ID)
	SendJSON(ctx, map[string]any{
		"message": "User created successfully",
		"user":    created,
	})
}

func (h *UserManagementHandler) updateUser(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var req updateUserRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	user, err := h.configStore.GetUser(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "User not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve user")
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			SendError(ctx, 400, "name cannot be empty")
			return
		}
		user.Name = *req.Name
	}
	if req.Email != nil {
		if strings.TrimSpace(*req.Email) == "" {
			SendError(ctx, 400, "email cannot be empty")
			return
		}
		user.Email = *req.Email
	}
	if req.Status != nil {
		if !configstoreTables.IsValidUserStatus(*req.Status) {
			SendError(ctx, 400, fmt.Sprintf("Invalid status %q", *req.Status))
			return
		}
		user.Status = *req.Status
	}
	var vkIDs []string
	if req.VirtualKeyIDs != nil {
		vkIDs = *req.VirtualKeyIDs
	}
	if errMsg := h.validateAttachments(ctx, req.BusinessUnitID, vkIDs); errMsg != "" {
		SendError(ctx, 400, errMsg)
		return
	}
	if req.BusinessUnitID != nil {
		// An empty string detaches the user from any business unit.
		if strings.TrimSpace(*req.BusinessUnitID) == "" {
			user.BusinessUnitID = nil
		} else {
			user.BusinessUnitID = req.BusinessUnitID
		}
	}
	if req.VirtualKeyIDs != nil {
		user.VirtualKeyIDs = *req.VirtualKeyIDs
	}

	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		budgetID, err := h.applyBudget(ctx, tx, user.BudgetID, req.Budget)
		if err != nil {
			return err
		}
		user.BudgetID = budgetID
		rateLimitID, err := h.applyRateLimit(ctx, tx, user.RateLimitID, req.RateLimit)
		if err != nil {
			return err
		}
		user.RateLimitID = rateLimitID
		user.UpdatedAt = time.Now()
		// Strip preloaded associations so Save does not attempt to upsert them.
		user.Budget = nil
		user.RateLimit = nil
		user.BusinessUnit = nil
		return h.configStore.UpdateUser(ctx, user, tx)
	}); err != nil {
		if errors.Is(err, configstore.ErrAlreadyExists) {
			SendError(ctx, 409, "A user with this email already exists")
			return
		}
		logger.Error("failed to update user: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to update user: %v", err))
		return
	}
	updated, err := h.configStore.GetUser(ctx, user.ID)
	if err != nil {
		updated = user
	}
	recordAudit(ctx, h.configStore, AuditActionUserUpdate, configstoreTables.AuditOutcomeSuccess, user.ID)
	SendJSON(ctx, map[string]any{
		"message": "User updated successfully",
		"user":    updated,
	})
}

func (h *UserManagementHandler) deleteUser(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	user, err := h.configStore.GetUser(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "User not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve user")
		return
	}
	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		if err := h.configStore.DeleteUser(ctx, id, tx); err != nil {
			return err
		}
		if user.BudgetID != nil {
			if err := h.configStore.DeleteBudget(ctx, *user.BudgetID, tx); err != nil {
				return err
			}
		}
		if user.RateLimitID != nil {
			if err := h.configStore.DeleteRateLimit(ctx, *user.RateLimitID, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		logger.Error("failed to delete user: %v", err)
		SendError(ctx, 500, "Failed to delete user")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionUserDelete, configstoreTables.AuditOutcomeSuccess, id)
	SendJSON(ctx, map[string]any{"message": "User deleted successfully"})
}

// ---- business unit handlers ----

func (h *UserManagementHandler) getBusinessUnits(ctx *fasthttp.RequestCtx) {
	params := configstore.BusinessUnitsQueryParams{
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

	units, total, err := h.configStore.GetBusinessUnits(ctx, params)
	if err != nil {
		logger.Error("failed to retrieve business units: %v", err)
		SendError(ctx, 500, "Failed to retrieve business units")
		return
	}
	SendJSON(ctx, map[string]any{
		"business_units": units,
		"total":          total,
		"count":          len(units),
		"offset":         params.Offset,
	})
}

func (h *UserManagementHandler) getBusinessUnit(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	unit, err := h.configStore.GetBusinessUnit(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Business unit not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve business unit")
		return
	}
	SendJSON(ctx, map[string]any{"business_unit": unit})
}

func (h *UserManagementHandler) createBusinessUnit(ctx *fasthttp.RequestCtx) {
	var req createBusinessUnitRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		SendError(ctx, 400, "name is required")
		return
	}

	var unit configstoreTables.TableBusinessUnit
	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		budgetID, err := h.applyBudget(ctx, tx, nil, req.Budget)
		if err != nil {
			return err
		}
		rateLimitID, err := h.applyRateLimit(ctx, tx, nil, req.RateLimit)
		if err != nil {
			return err
		}
		unit = configstoreTables.TableBusinessUnit{
			ID:          uuid.NewString(),
			Name:        req.Name,
			Description: req.Description,
			BudgetID:    budgetID,
			RateLimitID: rateLimitID,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		return h.configStore.CreateBusinessUnit(ctx, &unit, tx)
	}); err != nil {
		if errors.Is(err, configstore.ErrAlreadyExists) {
			SendError(ctx, 409, "A business unit with this name already exists")
			return
		}
		logger.Error("failed to create business unit: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to create business unit: %v", err))
		return
	}
	created, err := h.configStore.GetBusinessUnit(ctx, unit.ID)
	if err != nil {
		created = &unit
	}
	SendJSON(ctx, map[string]any{
		"message":       "Business unit created successfully",
		"business_unit": created,
	})
}

func (h *UserManagementHandler) updateBusinessUnit(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var req updateBusinessUnitRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	unit, err := h.configStore.GetBusinessUnit(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Business unit not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve business unit")
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			SendError(ctx, 400, "name cannot be empty")
			return
		}
		unit.Name = *req.Name
	}
	if req.Description != nil {
		unit.Description = *req.Description
	}

	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		budgetID, err := h.applyBudget(ctx, tx, unit.BudgetID, req.Budget)
		if err != nil {
			return err
		}
		unit.BudgetID = budgetID
		rateLimitID, err := h.applyRateLimit(ctx, tx, unit.RateLimitID, req.RateLimit)
		if err != nil {
			return err
		}
		unit.RateLimitID = rateLimitID
		unit.UpdatedAt = time.Now()
		unit.Budget = nil
		unit.RateLimit = nil
		return h.configStore.UpdateBusinessUnit(ctx, unit, tx)
	}); err != nil {
		if errors.Is(err, configstore.ErrAlreadyExists) {
			SendError(ctx, 409, "A business unit with this name already exists")
			return
		}
		logger.Error("failed to update business unit: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to update business unit: %v", err))
		return
	}
	updated, err := h.configStore.GetBusinessUnit(ctx, unit.ID)
	if err != nil {
		updated = unit
	}
	SendJSON(ctx, map[string]any{
		"message":       "Business unit updated successfully",
		"business_unit": updated,
	})
}

func (h *UserManagementHandler) deleteBusinessUnit(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	unit, err := h.configStore.GetBusinessUnit(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Business unit not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve business unit")
		return
	}
	if err := h.configStore.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		if err := h.configStore.DeleteBusinessUnit(ctx, id, tx); err != nil {
			return err
		}
		if unit.BudgetID != nil {
			if err := h.configStore.DeleteBudget(ctx, *unit.BudgetID, tx); err != nil {
				return err
			}
		}
		if unit.RateLimitID != nil {
			if err := h.configStore.DeleteRateLimit(ctx, *unit.RateLimitID, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Business unit not found")
			return
		}
		logger.Error("failed to delete business unit: %v", err)
		SendError(ctx, 500, "Failed to delete business unit")
		return
	}
	SendJSON(ctx, map[string]any{"message": "Business unit deleted successfully"})
}

// ---- helpers ----

// validateAttachments verifies that the referenced business unit and virtual
// keys exist. Returns a non-empty error message on failure. A nil/empty
// businessUnitID is allowed (no assignment).
func (h *UserManagementHandler) validateAttachments(ctx context.Context, businessUnitID *string, vkIDs []string) string {
	if businessUnitID != nil && strings.TrimSpace(*businessUnitID) != "" {
		if _, err := h.configStore.GetBusinessUnit(ctx, *businessUnitID); err != nil {
			if errors.Is(err, configstore.ErrNotFound) {
				return fmt.Sprintf("business unit %q not found", *businessUnitID)
			}
			return "failed to validate business unit"
		}
	}
	for _, vkID := range vkIDs {
		if strings.TrimSpace(vkID) == "" {
			return "virtual key id cannot be empty"
		}
		if _, err := h.configStore.GetVirtualKey(ctx, vkID); err != nil {
			if errors.Is(err, configstore.ErrNotFound) {
				return fmt.Sprintf("virtual key %q not found", vkID)
			}
			return "failed to validate virtual key"
		}
	}
	return ""
}

// applyBudget creates, updates, or deletes the referenced budget row and returns
// the resulting budget ID pointer. A nil input leaves the current ID untouched;
// an input with zero MaxLimit and empty ResetDuration removes the budget.
func (h *UserManagementHandler) applyBudget(ctx context.Context, tx *gorm.DB, currentID *string, in *budgetInput) (*string, error) {
	if in == nil {
		return currentID, nil
	}
	if in.MaxLimit <= 0 && strings.TrimSpace(in.ResetDuration) == "" {
		if currentID != nil {
			if err := h.configStore.DeleteBudget(ctx, *currentID, tx); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if currentID != nil {
		var b configstoreTables.TableBudget
		if err := tx.First(&b, "id = ?", *currentID).Error; err != nil {
			return nil, err
		}
		b.MaxLimit = in.MaxLimit
		b.ResetDuration = in.ResetDuration
		if err := h.configStore.UpdateBudget(ctx, &b, tx); err != nil {
			return nil, err
		}
		return currentID, nil
	}
	b := &configstoreTables.TableBudget{
		ID:            uuid.NewString(),
		MaxLimit:      in.MaxLimit,
		ResetDuration: in.ResetDuration,
		LastReset:     time.Now(),
	}
	if err := h.configStore.CreateBudget(ctx, b, tx); err != nil {
		return nil, err
	}
	return &b.ID, nil
}

// applyRateLimit creates, updates, or deletes the referenced rate-limit row and
// returns the resulting rate-limit ID pointer. A nil input leaves the current ID
// untouched; an input with all-nil max-limit fields removes the rate limit.
func (h *UserManagementHandler) applyRateLimit(ctx context.Context, tx *gorm.DB, currentID *string, in *rateLimitInput) (*string, error) {
	if in == nil {
		return currentID, nil
	}
	if in.TokenMaxLimit == nil && in.RequestMaxLimit == nil {
		if currentID != nil {
			if err := h.configStore.DeleteRateLimit(ctx, *currentID, tx); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if currentID != nil {
		var rl configstoreTables.TableRateLimit
		if err := tx.First(&rl, "id = ?", *currentID).Error; err != nil {
			return nil, err
		}
		rl.TokenMaxLimit = in.TokenMaxLimit
		rl.TokenResetDuration = in.TokenResetDuration
		rl.RequestMaxLimit = in.RequestMaxLimit
		rl.RequestResetDuration = in.RequestResetDuration
		if err := validateRateLimit(&rl); err != nil {
			return nil, err
		}
		if err := h.configStore.UpdateRateLimit(ctx, &rl, tx); err != nil {
			return nil, err
		}
		return currentID, nil
	}
	rl := &configstoreTables.TableRateLimit{
		ID:                   uuid.NewString(),
		TokenMaxLimit:        in.TokenMaxLimit,
		TokenResetDuration:   in.TokenResetDuration,
		RequestMaxLimit:      in.RequestMaxLimit,
		RequestResetDuration: in.RequestResetDuration,
		TokenLastReset:       time.Now(),
		RequestLastReset:     time.Now(),
	}
	if err := validateRateLimit(rl); err != nil {
		return nil, err
	}
	if err := h.configStore.CreateRateLimit(ctx, rl, tx); err != nil {
		return nil, err
	}
	return &rl.ID, nil
}
