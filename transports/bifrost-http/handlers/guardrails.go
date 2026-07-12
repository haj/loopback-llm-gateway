// This file contains HTTP handlers for the Loopback Gateway guardrails
// configuration UI. It exposes CRUD over named, scoped guardrail configs backed
// by configstore.TableGuardrailConfig and, on every mutation, rebuilds the live
// guardrail list and pushes it into the running loopback-guard plugin via its
// runtime Set* setters (no restart required).
//
// It follows the governance.go handler patterns (RegisterRoutes + per-resource
// CRUD with SendJSON/SendError).
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
	"github.com/maximhq/bifrost/plugins/loopbackguard"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// GuardrailsHandler manages HTTP requests for guardrail configuration.
type GuardrailsHandler struct {
	configStore configstore.ConfigStore
	// pluginResolver returns the live loopback-guard plugin instance, or nil
	// when the plugin is not currently loaded. Resolved lazily so plugin
	// reloads via /api/plugins are honored rather than capturing a stale ref.
	pluginResolver func() *loopbackguard.Plugin
}

// NewGuardrailsHandler creates a guardrails handler. pluginResolver may be nil
// (e.g. in tests); when nil, mutations persist but no live plugin is updated.
func NewGuardrailsHandler(configStore configstore.ConfigStore, pluginResolver func() *loopbackguard.Plugin) (*GuardrailsHandler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &GuardrailsHandler{configStore: configStore, pluginResolver: pluginResolver}, nil
}

// RegisterRoutes wires the guardrail config CRUD endpoints.
func (h *GuardrailsHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/guardrails", lib.ChainMiddlewares(h.getGuardrails, middlewares...))
	r.POST("/api/governance/guardrails", lib.ChainMiddlewares(h.createGuardrail, middlewares...))
	r.GET("/api/governance/guardrails/{id}", lib.ChainMiddlewares(h.getGuardrail, middlewares...))
	r.PUT("/api/governance/guardrails/{id}", lib.ChainMiddlewares(h.updateGuardrail, middlewares...))
	r.DELETE("/api/governance/guardrails/{id}", lib.ChainMiddlewares(h.deleteGuardrail, middlewares...))
}

// ---- request payloads ----

type guardrailItemInput struct {
	ID      string         `json:"id,omitempty"`
	Type    string         `json:"type"`
	Enabled *bool          `json:"enabled,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
}

type createGuardrailRequest struct {
	Name       string               `json:"name"`
	Enabled    *bool                `json:"enabled,omitempty"`
	Scope      string               `json:"scope,omitempty"`
	ScopeID    *string              `json:"scope_id,omitempty"`
	Guardrails []guardrailItemInput `json:"guardrails,omitempty"`
}

type updateGuardrailRequest struct {
	Name       *string               `json:"name,omitempty"`
	Enabled    *bool                 `json:"enabled,omitempty"`
	Guardrails *[]guardrailItemInput `json:"guardrails,omitempty"`
}

// ---- handlers ----

func (h *GuardrailsHandler) getGuardrails(ctx *fasthttp.RequestCtx) {
	params := configstore.GuardrailConfigsQueryParams{
		Search:  string(ctx.QueryArgs().Peek("search")),
		Scope:   string(ctx.QueryArgs().Peek("scope")),
		ScopeID: string(ctx.QueryArgs().Peek("scope_id")),
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

	configs, total, err := h.configStore.GetGuardrailConfigs(ctx, params)
	if err != nil {
		logger.Error("failed to retrieve guardrail configs: %v", err)
		SendError(ctx, 500, "Failed to retrieve guardrail configs")
		return
	}
	for i := range configs {
		h.resolveScopeName(ctx, &configs[i])
	}
	SendJSON(ctx, map[string]any{
		"guardrails": configs,
		"total":      total,
		"count":      len(configs),
		"offset":     params.Offset,
	})
}

func (h *GuardrailsHandler) getGuardrail(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	config, err := h.configStore.GetGuardrailConfig(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Guardrail config not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve guardrail config")
		return
	}
	h.resolveScopeName(ctx, config)
	SendJSON(ctx, map[string]any{"guardrail_config": config})
}

func (h *GuardrailsHandler) createGuardrail(ctx *fasthttp.RequestCtx) {
	var req createGuardrailRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		SendError(ctx, 400, "name is required")
		return
	}
	if req.Scope == "" {
		req.Scope = configstoreTables.GuardrailScopeGlobal
	}
	if !configstoreTables.IsValidGuardrailScope(req.Scope) {
		SendError(ctx, 400, fmt.Sprintf("Invalid scope %q", req.Scope))
		return
	}
	if req.Scope == configstoreTables.GuardrailScopeGlobal {
		req.ScopeID = nil
	} else if req.ScopeID == nil || strings.TrimSpace(*req.ScopeID) == "" {
		SendError(ctx, 400, "scope_id is required when scope is not global")
		return
	}

	items, errMsg := normalizeItems(req.Guardrails)
	if errMsg != "" {
		SendError(ctx, 400, errMsg)
		return
	}
	// Validate that every enabled item produces a real guardrail (e.g. regex compiles).
	if _, _, err := buildGuardrailsFromItems(items); err != nil {
		SendError(ctx, 400, fmt.Sprintf("Invalid guardrail configuration: %v", err))
		return
	}

	config := &configstoreTables.TableGuardrailConfig{
		ID:         uuid.NewString(),
		Name:       req.Name,
		Enabled:    boolOrDefault(req.Enabled, true),
		Scope:      req.Scope,
		ScopeID:    req.ScopeID,
		Guardrails: items,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	h.applyScopeOwnership(config)

	if err := h.configStore.CreateGuardrailConfig(ctx, config); err != nil {
		logger.Error("failed to create guardrail config: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to create guardrail config: %v", err))
		return
	}
	h.reloadPlugin(ctx)
	h.resolveScopeName(ctx, config)
	recordAudit(ctx, h.configStore, AuditActionGuardrailCreate, configstoreTables.AuditOutcomeSuccess, config.ID)
	SendJSON(ctx, map[string]any{
		"message":          "Guardrail config created successfully",
		"guardrail_config": config,
	})
}

func (h *GuardrailsHandler) updateGuardrail(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var req updateGuardrailRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	config, err := h.configStore.GetGuardrailConfig(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Guardrail config not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve guardrail config")
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			SendError(ctx, 400, "name cannot be empty")
			return
		}
		config.Name = *req.Name
	}
	if req.Enabled != nil {
		config.Enabled = *req.Enabled
	}
	if req.Guardrails != nil {
		items, errMsg := normalizeItems(*req.Guardrails)
		if errMsg != "" {
			SendError(ctx, 400, errMsg)
			return
		}
		if _, _, err := buildGuardrailsFromItems(items); err != nil {
			SendError(ctx, 400, fmt.Sprintf("Invalid guardrail configuration: %v", err))
			return
		}
		config.Guardrails = items
	}
	config.UpdatedAt = time.Now()

	if err := h.configStore.UpdateGuardrailConfig(ctx, config); err != nil {
		logger.Error("failed to update guardrail config: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to update guardrail config: %v", err))
		return
	}
	h.reloadPlugin(ctx)
	h.resolveScopeName(ctx, config)
	recordAudit(ctx, h.configStore, AuditActionGuardrailUpdate, configstoreTables.AuditOutcomeSuccess, config.ID)
	SendJSON(ctx, map[string]any{
		"message":          "Guardrail config updated successfully",
		"guardrail_config": config,
	})
}

func (h *GuardrailsHandler) deleteGuardrail(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if err := h.configStore.DeleteGuardrailConfig(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Guardrail config not found")
			return
		}
		logger.Error("failed to delete guardrail config: %v", err)
		SendError(ctx, 500, "Failed to delete guardrail config")
		return
	}
	h.reloadPlugin(ctx)
	recordAudit(ctx, h.configStore, AuditActionGuardrailDelete, configstoreTables.AuditOutcomeSuccess, id)
	SendJSON(ctx, map[string]any{"message": "Guardrail config deleted successfully"})
}

// ---- helpers ----

func boolOrDefault(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// applyScopeOwnership sets the polymorphic owner FK matching the scope so a
// cascade delete of the owner cleans up the config.
func (h *GuardrailsHandler) applyScopeOwnership(c *configstoreTables.TableGuardrailConfig) {
	c.VirtualKeyID, c.TeamID, c.CustomerID = nil, nil, nil
	if c.ScopeID == nil {
		return
	}
	switch c.Scope {
	case configstoreTables.GuardrailScopeVirtualKey:
		c.VirtualKeyID = c.ScopeID
	case configstoreTables.GuardrailScopeTeam:
		c.TeamID = c.ScopeID
	case configstoreTables.GuardrailScopeCustomer:
		c.CustomerID = c.ScopeID
	}
}

// resolveScopeName populates the transient ScopeName for a non-global config so
// the UI can render a label instead of an opaque scope_id. Failures are
// non-fatal (ScopeName stays empty).
func (h *GuardrailsHandler) resolveScopeName(ctx context.Context, c *configstoreTables.TableGuardrailConfig) {
	if c == nil || c.ScopeID == nil || *c.ScopeID == "" {
		return
	}
	switch c.Scope {
	case configstoreTables.GuardrailScopeVirtualKey:
		if vk, err := h.configStore.GetVirtualKey(ctx, *c.ScopeID); err == nil && vk != nil {
			c.ScopeName = vk.Name
		}
	case configstoreTables.GuardrailScopeTeam:
		if t, err := h.configStore.GetTeam(ctx, *c.ScopeID); err == nil && t != nil {
			c.ScopeName = t.Name
		}
	case configstoreTables.GuardrailScopeCustomer:
		if cust, err := h.configStore.GetCustomer(ctx, *c.ScopeID); err == nil && cust != nil {
			c.ScopeName = cust.Name
		}
	}
}

// normalizeItems assigns IDs, defaults Enabled to true, validates the type, and
// converts the request inputs into stored GuardrailItem values. The second
// return value is a non-empty error message on validation failure.
func normalizeItems(in []guardrailItemInput) ([]configstoreTables.GuardrailItem, string) {
	out := make([]configstoreTables.GuardrailItem, 0, len(in))
	for _, item := range in {
		t := strings.TrimSpace(item.Type)
		if t == "" {
			return nil, "each guardrail must have a type"
		}
		if !isKnownGuardrailType(t) {
			return nil, fmt.Sprintf("unknown guardrail type %q", t)
		}
		id := item.ID
		if id == "" {
			id = uuid.NewString()
		}
		out = append(out, configstoreTables.GuardrailItem{
			ID:      id,
			Type:    t,
			Enabled: boolOrDefault(item.Enabled, true),
			Params:  item.Params,
		})
	}
	return out, ""
}

// reloadPlugin rebuilds the live guardrail set from every enabled config and
// pushes it into the running loopback-guard plugin. Best-effort: failures are
// logged but do not fail the API request (the DB is already the source of
// truth and a later restart re-derives the same state).
func (h *GuardrailsHandler) reloadPlugin(ctx context.Context) {
	if h.pluginResolver == nil {
		return
	}
	plugin := h.pluginResolver()
	if plugin == nil {
		return
	}
	configs, err := h.configStore.GetEnabledGuardrailConfigs(ctx)
	if err != nil {
		logger.Error("failed to load enabled guardrail configs for plugin reload: %v", err)
		return
	}
	var text []loopbackguard.Guardrail
	var reqs []loopbackguard.RequestGuardrail
	for i := range configs {
		if !configs[i].Enabled {
			continue
		}
		t, r, berr := buildGuardrailsFromItems(configs[i].Guardrails)
		if berr != nil {
			// Skip a malformed config rather than dropping every guardrail.
			logger.Warn("skipping guardrail config %s during reload: %v", configs[i].ID, berr)
			continue
		}
		text = append(text, t...)
		reqs = append(reqs, r...)
	}
	plugin.SetGuardrails(text)
	plugin.SetRequestGuardrails(reqs...)
}
