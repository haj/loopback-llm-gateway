// This file contains HTTP handlers for the Loopback Gateway JWT auth UI
//. It exposes CRUD over trusted external JWT issuers
// backed by configstore.TableJWTAuthConfig and, on every mutation, rebuilds
// the live JWTVKAuthMiddleware snapshot so changes take effect without a
// restart.
//
// It follows the circuitbreaker.go handler patterns (RegisterRoutes +
// SendJSON/SendError + recordAudit + startup ApplyAll). The feature is
// OPT-IN / default-off: until an enabled issuer row exists, the middleware
// snapshot is nil and the inference path is unchanged.
//
// Responses reference virtual keys by ID only — VK values never appear in
// this API.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// JWTAuthHandler manages HTTP requests for JWT auth issuer configs.
type JWTAuthHandler struct {
	configStore configstore.ConfigStore
	// middleware is the live snapshot holder, rebuilt after every mutation.
	// May be nil (tests); mutations then persist without a live re-apply.
	middleware *JWTVKAuthMiddleware
}

// NewJWTAuthHandler creates a JWT auth handler. middleware may be nil (tests).
func NewJWTAuthHandler(configStore configstore.ConfigStore, middleware *JWTVKAuthMiddleware) (*JWTAuthHandler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &JWTAuthHandler{configStore: configStore, middleware: middleware}, nil
}

// RegisterRoutes wires the JWT auth CRUD endpoints.
func (h *JWTAuthHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/jwt-auth", lib.ChainMiddlewares(h.listConfigs, middlewares...))
	r.POST("/api/governance/jwt-auth", lib.ChainMiddlewares(h.createConfig, middlewares...))
	r.GET("/api/governance/jwt-auth/{id}", lib.ChainMiddlewares(h.getConfig, middlewares...))
	r.PUT("/api/governance/jwt-auth/{id}", lib.ChainMiddlewares(h.updateConfig, middlewares...))
	r.DELETE("/api/governance/jwt-auth/{id}", lib.ChainMiddlewares(h.deleteConfig, middlewares...))
}

// ApplyAll pushes every persisted row into the live middleware snapshot.
// Called at startup and after every mutation. Best-effort: a load failure
// logs and leaves the previous snapshot in place.
func (h *JWTAuthHandler) ApplyAll(ctx context.Context) {
	if h.middleware == nil {
		return
	}
	configs, err := h.configStore.GetJWTAuthConfigs(ctx)
	if err != nil {
		logger.Error("failed to load jwt auth configs for middleware apply: %v", err)
		return
	}
	h.middleware.SetConfigs(configs)
}

// ---- request payloads ----

// jwtAuthInput is the create/update payload. Pointer fields distinguish
// "leave unchanged" from explicit zero values on update.
type jwtAuthInput struct {
	Name                *string                              `json:"name,omitempty"`
	Enabled             *bool                                `json:"enabled,omitempty"`
	Issuer              *string                              `json:"issuer,omitempty"`
	JWKSURL             *string                              `json:"jwks_url,omitempty"`
	Audience            *string                              `json:"audience,omitempty"`
	RejectInvalid       *bool                                `json:"reject_invalid,omitempty"`
	ClaimMappings       *[]configstoreTables.JWTAuthClaimMapping `json:"claim_mappings,omitempty"`
	DefaultVirtualKeyID *string                              `json:"default_virtual_key_id,omitempty"`
}

// validateJWTAuthConfig returns a client-facing message for invalid configs
// ("" when acceptable), verifying that every referenced virtual key exists
// (FK-by-convention: the table itself doesn't enforce it).
func (h *JWTAuthHandler) validateJWTAuthConfig(ctx context.Context, config *configstoreTables.TableJWTAuthConfig) string {
	if strings.TrimSpace(config.Issuer) == "" {
		return "issuer is required"
	}
	if strings.TrimSpace(config.JWKSURL) == "" {
		return "jwks_url is required"
	}
	if !strings.HasPrefix(config.JWKSURL, "http://") && !strings.HasPrefix(config.JWKSURL, "https://") {
		return "jwks_url must be an http(s) URL"
	}
	referenced := map[string]bool{}
	for i, m := range config.ClaimMappings {
		if strings.TrimSpace(m.Claim) == "" {
			return fmt.Sprintf("claim mapping %d: claim is required", i+1)
		}
		if strings.TrimSpace(m.Value) == "" {
			return fmt.Sprintf("claim mapping %d: value is required (use \"*\" to match any)", i+1)
		}
		if strings.TrimSpace(m.VirtualKeyID) == "" {
			return fmt.Sprintf("claim mapping %d: virtual_key_id is required", i+1)
		}
		referenced[m.VirtualKeyID] = true
	}
	if config.DefaultVirtualKeyID != "" {
		referenced[config.DefaultVirtualKeyID] = true
	}
	for vkID := range referenced {
		if _, err := h.configStore.GetVirtualKey(ctx, vkID); err != nil {
			if errors.Is(err, configstore.ErrNotFound) {
				return fmt.Sprintf("virtual key %q does not exist", vkID)
			}
			logger.Error("failed to verify virtual key %s for jwt auth config: %v", vkID, err)
			return "failed to verify referenced virtual keys"
		}
	}
	return ""
}

// ---- handlers ----

func (h *JWTAuthHandler) listConfigs(ctx *fasthttp.RequestCtx) {
	configs, err := h.configStore.GetJWTAuthConfigs(ctx)
	if err != nil {
		logger.Error("failed to retrieve jwt auth configs: %v", err)
		SendError(ctx, 500, "Failed to retrieve JWT auth configs")
		return
	}
	SendJSON(ctx, map[string]any{
		"jwt_auth_configs": configs,
		"count":            len(configs),
	})
}

func (h *JWTAuthHandler) getConfig(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	config, err := h.configStore.GetJWTAuthConfig(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "JWT auth config not found")
			return
		}
		logger.Error("failed to retrieve jwt auth config %s: %v", id, err)
		SendError(ctx, 500, "Failed to retrieve JWT auth config")
		return
	}
	SendJSON(ctx, map[string]any{"jwt_auth_config": config})
}

func (h *JWTAuthHandler) createConfig(ctx *fasthttp.RequestCtx) {
	var req jwtAuthInput
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}

	config := &configstoreTables.TableJWTAuthConfig{
		ID:        uuid.NewString(),
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	applyJWTAuthInput(config, &req)

	if msg := h.validateJWTAuthConfig(ctx, config); msg != "" {
		SendError(ctx, 400, msg)
		return
	}
	if err := h.configStore.CreateJWTAuthConfig(ctx, config); err != nil {
		if errors.Is(err, configstore.ErrAlreadyExists) {
			SendError(ctx, 409, "A JWT auth config for this issuer already exists")
			return
		}
		logger.Error("failed to create jwt auth config: %v", err)
		SendError(ctx, 500, "Failed to create JWT auth config")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionJWTAuthCreate, configstoreTables.AuditOutcomeSuccess, config.ID)
	h.ApplyAll(ctx)
	SendJSON(ctx, map[string]any{
		"message":         "JWT auth config created successfully",
		"jwt_auth_config": config,
	})
}

func (h *JWTAuthHandler) updateConfig(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	config, err := h.configStore.GetJWTAuthConfig(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "JWT auth config not found")
			return
		}
		logger.Error("failed to retrieve jwt auth config %s: %v", id, err)
		SendError(ctx, 500, "Failed to retrieve JWT auth config")
		return
	}

	var req jwtAuthInput
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	applyJWTAuthInput(config, &req)
	config.UpdatedAt = time.Now()

	if msg := h.validateJWTAuthConfig(ctx, config); msg != "" {
		SendError(ctx, 400, msg)
		return
	}
	if err := h.configStore.UpdateJWTAuthConfig(ctx, config); err != nil {
		logger.Error("failed to update jwt auth config %s: %v", id, err)
		SendError(ctx, 500, "Failed to update JWT auth config")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionJWTAuthUpdate, configstoreTables.AuditOutcomeSuccess, config.ID)
	h.ApplyAll(ctx)
	SendJSON(ctx, map[string]any{
		"message":         "JWT auth config updated successfully",
		"jwt_auth_config": config,
	})
}

func (h *JWTAuthHandler) deleteConfig(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if err := h.configStore.DeleteJWTAuthConfig(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "JWT auth config not found")
			return
		}
		logger.Error("failed to delete jwt auth config %s: %v", id, err)
		SendError(ctx, 500, "Failed to delete JWT auth config")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionJWTAuthDelete, configstoreTables.AuditOutcomeSuccess, id)
	h.ApplyAll(ctx)
	SendJSON(ctx, map[string]any{"message": "JWT auth config deleted successfully"})
}

// applyJWTAuthInput copies the set fields of the input onto the row.
func applyJWTAuthInput(config *configstoreTables.TableJWTAuthConfig, req *jwtAuthInput) {
	if req.Name != nil {
		config.Name = strings.TrimSpace(*req.Name)
	}
	if req.Enabled != nil {
		config.Enabled = *req.Enabled
	}
	if req.Issuer != nil {
		config.Issuer = strings.TrimSpace(*req.Issuer)
	}
	if req.JWKSURL != nil {
		config.JWKSURL = strings.TrimSpace(*req.JWKSURL)
	}
	if req.Audience != nil {
		config.Audience = strings.TrimSpace(*req.Audience)
	}
	if req.RejectInvalid != nil {
		config.RejectInvalid = *req.RejectInvalid
	}
	if req.ClaimMappings != nil {
		config.ClaimMappings = *req.ClaimMappings
	}
	if req.DefaultVirtualKeyID != nil {
		config.DefaultVirtualKeyID = strings.TrimSpace(*req.DefaultVirtualKeyID)
	}
}
