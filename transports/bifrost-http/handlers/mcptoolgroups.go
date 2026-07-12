// This file contains HTTP handlers for the Loopback Gateway MCP tool groups
// governance UI. It exposes CRUD over named, scoped groups of MCP tool
// references backed by configstore.TableMCPToolGroup. A tool group bundles MCP
// tools and binds the bundle to a virtual key or team; the gateway's MCP
// visibility filtering (EntityMCPToolGroup) consumes the persisted rows at
// request time, so no live plugin reload is required here.
//
// It follows the guardrails.go / governance.go handler patterns
// (RegisterRoutes + per-resource CRUD with SendJSON/SendError).
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
)

// MCPToolGroupsHandler manages HTTP requests for MCP tool group configuration.
type MCPToolGroupsHandler struct {
	configStore configstore.ConfigStore
}

// NewMCPToolGroupsHandler creates an MCP tool groups handler.
func NewMCPToolGroupsHandler(configStore configstore.ConfigStore) (*MCPToolGroupsHandler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &MCPToolGroupsHandler{configStore: configStore}, nil
}

// RegisterRoutes wires the MCP tool group CRUD endpoints.
func (h *MCPToolGroupsHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/mcp-tool-groups", lib.ChainMiddlewares(h.getToolGroups, middlewares...))
	r.POST("/api/governance/mcp-tool-groups", lib.ChainMiddlewares(h.createToolGroup, middlewares...))
	r.GET("/api/governance/mcp-tool-groups/{id}", lib.ChainMiddlewares(h.getToolGroup, middlewares...))
	r.PUT("/api/governance/mcp-tool-groups/{id}", lib.ChainMiddlewares(h.updateToolGroup, middlewares...))
	r.DELETE("/api/governance/mcp-tool-groups/{id}", lib.ChainMiddlewares(h.deleteToolGroup, middlewares...))
}

// ---- request payloads ----

type mcpToolRefInput struct {
	ClientID string `json:"client_id"`
	ToolName string `json:"tool_name"`
}

type createMCPToolGroupRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Scope       string            `json:"scope,omitempty"`
	ScopeID     *string           `json:"scope_id,omitempty"`
	Tools       []mcpToolRefInput `json:"tools,omitempty"`
}

type updateMCPToolGroupRequest struct {
	Name        *string            `json:"name,omitempty"`
	Description *string            `json:"description,omitempty"`
	Enabled     *bool              `json:"enabled,omitempty"`
	Tools       *[]mcpToolRefInput `json:"tools,omitempty"`
}

// ---- handlers ----

func (h *MCPToolGroupsHandler) getToolGroups(ctx *fasthttp.RequestCtx) {
	params := configstore.MCPToolGroupsQueryParams{
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

	groups, total, err := h.configStore.GetMCPToolGroups(ctx, params)
	if err != nil {
		logger.Error("failed to retrieve mcp tool groups: %v", err)
		SendError(ctx, 500, "Failed to retrieve MCP tool groups")
		return
	}
	for i := range groups {
		h.resolveScopeName(ctx, &groups[i])
	}
	SendJSON(ctx, map[string]any{
		"tool_groups": groups,
		"total":       total,
		"count":       len(groups),
		"offset":      params.Offset,
	})
}

func (h *MCPToolGroupsHandler) getToolGroup(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	group, err := h.configStore.GetMCPToolGroup(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "MCP tool group not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve MCP tool group")
		return
	}
	h.resolveScopeName(ctx, group)
	SendJSON(ctx, map[string]any{"tool_group": group})
}

func (h *MCPToolGroupsHandler) createToolGroup(ctx *fasthttp.RequestCtx) {
	var req createMCPToolGroupRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		SendError(ctx, 400, "name is required")
		return
	}
	if req.Scope == "" {
		req.Scope = configstoreTables.MCPToolGroupScopeGlobal
	}
	if !configstoreTables.IsValidMCPToolGroupScope(req.Scope) {
		SendError(ctx, 400, fmt.Sprintf("Invalid scope %q", req.Scope))
		return
	}
	if req.Scope == configstoreTables.MCPToolGroupScopeGlobal {
		req.ScopeID = nil
	} else if req.ScopeID == nil || strings.TrimSpace(*req.ScopeID) == "" {
		SendError(ctx, 400, "scope_id is required when scope is not global")
		return
	}

	tools, errMsg := normalizeToolRefs(req.Tools)
	if errMsg != "" {
		SendError(ctx, 400, errMsg)
		return
	}

	group := &configstoreTables.TableMCPToolGroup{
		ID:          uuid.NewString(),
		Name:        req.Name,
		Description: strings.TrimSpace(req.Description),
		Enabled:     boolOrDefault(req.Enabled, true),
		Scope:       req.Scope,
		ScopeID:     req.ScopeID,
		Tools:       tools,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	h.applyScopeOwnership(group)

	if err := h.configStore.CreateMCPToolGroup(ctx, group); err != nil {
		logger.Error("failed to create mcp tool group: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to create MCP tool group: %v", err))
		return
	}
	h.resolveScopeName(ctx, group)
	recordAudit(ctx, h.configStore, AuditActionMCPToolGroupCreate, configstoreTables.AuditOutcomeSuccess, group.ID)
	SendJSON(ctx, map[string]any{
		"message":    "MCP tool group created successfully",
		"tool_group": group,
	})
}

func (h *MCPToolGroupsHandler) updateToolGroup(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var req updateMCPToolGroupRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	group, err := h.configStore.GetMCPToolGroup(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "MCP tool group not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve MCP tool group")
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			SendError(ctx, 400, "name cannot be empty")
			return
		}
		group.Name = *req.Name
	}
	if req.Description != nil {
		group.Description = strings.TrimSpace(*req.Description)
	}
	if req.Enabled != nil {
		group.Enabled = *req.Enabled
	}
	if req.Tools != nil {
		tools, errMsg := normalizeToolRefs(*req.Tools)
		if errMsg != "" {
			SendError(ctx, 400, errMsg)
			return
		}
		group.Tools = tools
	}
	group.UpdatedAt = time.Now()

	if err := h.configStore.UpdateMCPToolGroup(ctx, group); err != nil {
		logger.Error("failed to update mcp tool group: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to update MCP tool group: %v", err))
		return
	}
	h.resolveScopeName(ctx, group)
	recordAudit(ctx, h.configStore, AuditActionMCPToolGroupUpdate, configstoreTables.AuditOutcomeSuccess, group.ID)
	SendJSON(ctx, map[string]any{
		"message":    "MCP tool group updated successfully",
		"tool_group": group,
	})
}

func (h *MCPToolGroupsHandler) deleteToolGroup(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	// Resolve first through the scoped read so a caller that cannot see the
	// group gets 404 rather than silently deleting an out-of-scope row.
	if _, err := h.configStore.GetMCPToolGroup(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "MCP tool group not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve MCP tool group")
		return
	}
	if err := h.configStore.DeleteMCPToolGroup(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "MCP tool group not found")
			return
		}
		logger.Error("failed to delete mcp tool group: %v", err)
		SendError(ctx, 500, "Failed to delete MCP tool group")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionMCPToolGroupDelete, configstoreTables.AuditOutcomeSuccess, id)
	SendJSON(ctx, map[string]any{"message": "MCP tool group deleted successfully"})
}

// ---- helpers ----

// applyScopeOwnership sets the polymorphic owner FK matching the scope so a
// cascade delete of the owner cleans up the group.
func (h *MCPToolGroupsHandler) applyScopeOwnership(g *configstoreTables.TableMCPToolGroup) {
	g.VirtualKeyID, g.TeamID, g.CustomerID = nil, nil, nil
	if g.ScopeID == nil {
		return
	}
	switch g.Scope {
	case configstoreTables.MCPToolGroupScopeVirtualKey:
		g.VirtualKeyID = g.ScopeID
	case configstoreTables.MCPToolGroupScopeTeam:
		g.TeamID = g.ScopeID
	case configstoreTables.MCPToolGroupScopeCustomer:
		g.CustomerID = g.ScopeID
	}
}

// resolveScopeName populates the transient ScopeName for a non-global group so
// the UI can render a label instead of an opaque scope_id. Failures are
// non-fatal (ScopeName stays empty).
func (h *MCPToolGroupsHandler) resolveScopeName(ctx context.Context, g *configstoreTables.TableMCPToolGroup) {
	if g == nil || g.ScopeID == nil || *g.ScopeID == "" {
		return
	}
	switch g.Scope {
	case configstoreTables.MCPToolGroupScopeVirtualKey:
		if vk, err := h.configStore.GetVirtualKey(ctx, *g.ScopeID); err == nil && vk != nil {
			g.ScopeName = vk.Name
		}
	case configstoreTables.MCPToolGroupScopeTeam:
		if t, err := h.configStore.GetTeam(ctx, *g.ScopeID); err == nil && t != nil {
			g.ScopeName = t.Name
		}
	case configstoreTables.MCPToolGroupScopeCustomer:
		if cust, err := h.configStore.GetCustomer(ctx, *g.ScopeID); err == nil && cust != nil {
			g.ScopeName = cust.Name
		}
	}
}

// normalizeToolRefs validates and de-duplicates the requested tool references.
// The second return value is a non-empty error message on validation failure.
func normalizeToolRefs(in []mcpToolRefInput) ([]configstoreTables.MCPToolRef, string) {
	out := make([]configstoreTables.MCPToolRef, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, ref := range in {
		clientID := strings.TrimSpace(ref.ClientID)
		toolName := strings.TrimSpace(ref.ToolName)
		if clientID == "" {
			return nil, "each tool reference must have a client_id"
		}
		if toolName == "" {
			return nil, "each tool reference must have a tool_name"
		}
		key := clientID + "\x00" + toolName
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, configstoreTables.MCPToolRef{ClientID: clientID, ToolName: toolName})
	}
	return out, ""
}
