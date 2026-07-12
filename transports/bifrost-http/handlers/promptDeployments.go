// This file contains HTTP handlers for the Loopback Gateway prompt deployments
// UI. It exposes CRUD over named, weighted traffic-routing strategies backed by
// configstore.TablePromptDeployment and, on every mutation, refreshes the
// prompts plugin's in-memory cache (which also rebuilds the deployment routing
// table) so live traffic routing reflects the change without a restart.
//
// It follows the guardrails.go handler patterns (RegisterRoutes + per-resource
// CRUD with SendJSON/SendError) and reuses the prompts plugin reloader.
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

// PromptDeploymentsHandler manages HTTP requests for prompt deployments.
type PromptDeploymentsHandler struct {
	store configstore.ConfigStore
	// reloader refreshes the prompts plugin cache (and, with the deployment-aware
	// resolver, the live routing table) after a mutation. Optional; nil when the
	// prompts plugin is not loaded, in which case mutations persist but no live
	// routing is updated until the next restart.
	reloader PromptCacheReloader
}

// NewPromptDeploymentsHandler creates a prompt deployments handler. reloader may
// be nil (e.g. in tests or when the prompts plugin is not loaded).
func NewPromptDeploymentsHandler(store configstore.ConfigStore, reloader PromptCacheReloader) (*PromptDeploymentsHandler, error) {
	if store == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &PromptDeploymentsHandler{store: store, reloader: reloader}, nil
}

// RegisterRoutes wires the prompt deployment CRUD endpoints.
func (h *PromptDeploymentsHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/prompt-repo/prompts/{id}/deployments", lib.ChainMiddlewares(h.listDeployments, middlewares...))
	r.POST("/api/prompt-repo/prompts/{id}/deployments", lib.ChainMiddlewares(h.createDeployment, middlewares...))
	r.GET("/api/prompt-repo/deployments/{id}", lib.ChainMiddlewares(h.getDeployment, middlewares...))
	r.PUT("/api/prompt-repo/deployments/{id}", lib.ChainMiddlewares(h.updateDeployment, middlewares...))
	r.DELETE("/api/prompt-repo/deployments/{id}", lib.ChainMiddlewares(h.deleteDeployment, middlewares...))
}

// ---- request payloads ----

type deploymentVersionRefInput struct {
	VersionNumber int `json:"version_number"`
	Weight        int `json:"weight"`
}

type createPromptDeploymentRequest struct {
	Name     string                       `json:"name"`
	Enabled  *bool                        `json:"enabled,omitempty"`
	Versions []deploymentVersionRefInput `json:"versions,omitempty"`
}

type updatePromptDeploymentRequest struct {
	Name     *string                       `json:"name,omitempty"`
	Enabled  *bool                         `json:"enabled,omitempty"`
	Versions *[]deploymentVersionRefInput `json:"versions,omitempty"`
}

// ---- handlers ----

func (h *PromptDeploymentsHandler) listDeployments(ctx *fasthttp.RequestCtx) {
	promptID, _ := ctx.UserValue("id").(string)
	deployments, err := h.store.GetPromptDeployments(ctx, promptID)
	if err != nil {
		logger.Error("failed to retrieve prompt deployments: %v", err)
		SendError(ctx, 500, "Failed to retrieve prompt deployments")
		return
	}
	SendJSON(ctx, map[string]any{
		"deployments": deployments,
		"count":       len(deployments),
	})
}

func (h *PromptDeploymentsHandler) getDeployment(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	deployment, err := h.store.GetPromptDeployment(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Prompt deployment not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve prompt deployment")
		return
	}
	SendJSON(ctx, map[string]any{"deployment": deployment})
}

func (h *PromptDeploymentsHandler) createDeployment(ctx *fasthttp.RequestCtx) {
	promptID, _ := ctx.UserValue("id").(string)
	if strings.TrimSpace(promptID) == "" {
		SendError(ctx, 400, "prompt id is required")
		return
	}
	// Confirm the owning prompt exists so we never strand a deployment.
	if _, err := h.store.GetPromptByID(ctx, promptID); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Prompt not found")
			return
		}
		SendError(ctx, 500, "Failed to verify prompt")
		return
	}

	var req createPromptDeploymentRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		SendError(ctx, 400, "name is required")
		return
	}
	refs, errMsg := normalizeDeploymentVersions(req.Versions)
	if errMsg != "" {
		SendError(ctx, 400, errMsg)
		return
	}

	deployment := &configstoreTables.TablePromptDeployment{
		ID:        uuid.NewString(),
		PromptID:  promptID,
		Name:      strings.TrimSpace(req.Name),
		Enabled:   boolOrDefault(req.Enabled, true),
		Versions:  refs,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := h.store.CreatePromptDeployment(ctx, deployment); err != nil {
		logger.Error("failed to create prompt deployment: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to create prompt deployment: %v", err))
		return
	}
	h.reload(ctx)
	recordAudit(ctx, h.store, AuditActionPromptDeploymentCreate, configstoreTables.AuditOutcomeSuccess, deployment.ID)
	SendJSON(ctx, map[string]any{
		"message":    "Prompt deployment created successfully",
		"deployment": deployment,
	})
}

func (h *PromptDeploymentsHandler) updateDeployment(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var req updatePromptDeploymentRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	deployment, err := h.store.GetPromptDeployment(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Prompt deployment not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve prompt deployment")
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			SendError(ctx, 400, "name cannot be empty")
			return
		}
		deployment.Name = strings.TrimSpace(*req.Name)
	}
	if req.Enabled != nil {
		deployment.Enabled = *req.Enabled
	}
	if req.Versions != nil {
		refs, errMsg := normalizeDeploymentVersions(*req.Versions)
		if errMsg != "" {
			SendError(ctx, 400, errMsg)
			return
		}
		deployment.Versions = refs
	}
	deployment.UpdatedAt = time.Now()

	if err := h.store.UpdatePromptDeployment(ctx, deployment); err != nil {
		logger.Error("failed to update prompt deployment: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to update prompt deployment: %v", err))
		return
	}
	h.reload(ctx)
	recordAudit(ctx, h.store, AuditActionPromptDeploymentUpdate, configstoreTables.AuditOutcomeSuccess, deployment.ID)
	SendJSON(ctx, map[string]any{
		"message":    "Prompt deployment updated successfully",
		"deployment": deployment,
	})
}

func (h *PromptDeploymentsHandler) deleteDeployment(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if err := h.store.DeletePromptDeployment(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Prompt deployment not found")
			return
		}
		logger.Error("failed to delete prompt deployment: %v", err)
		SendError(ctx, 500, "Failed to delete prompt deployment")
		return
	}
	h.reload(ctx)
	recordAudit(ctx, h.store, AuditActionPromptDeploymentDelete, configstoreTables.AuditOutcomeSuccess, id)
	SendJSON(ctx, map[string]any{"message": "Prompt deployment deleted successfully"})
}

// ---- helpers ----

// normalizeDeploymentVersions validates and converts the request refs into
// stored PromptDeploymentVersionRef values. The second return value is a
// non-empty error message on validation failure.
func normalizeDeploymentVersions(in []deploymentVersionRefInput) ([]configstoreTables.PromptDeploymentVersionRef, string) {
	out := make([]configstoreTables.PromptDeploymentVersionRef, 0, len(in))
	seen := make(map[int]struct{}, len(in))
	for _, ref := range in {
		if ref.VersionNumber <= 0 {
			return nil, "each deployment version ref must reference a positive version_number"
		}
		if ref.Weight < 0 {
			return nil, "deployment version ref weight cannot be negative"
		}
		if _, dup := seen[ref.VersionNumber]; dup {
			return nil, fmt.Sprintf("duplicate version_number %d in deployment", ref.VersionNumber)
		}
		seen[ref.VersionNumber] = struct{}{}
		out = append(out, configstoreTables.PromptDeploymentVersionRef{
			VersionNumber: ref.VersionNumber,
			Weight:        ref.Weight,
		})
	}
	return out, ""
}

// reload refreshes the prompts plugin cache and deployment routing table after a
// mutation. Best-effort: failures are logged but do not fail the API request
// (the DB is the source of truth and a restart re-derives the same state).
func (h *PromptDeploymentsHandler) reload(ctx context.Context) {
	if h.reloader == nil {
		return
	}
	if err := h.reloader.Reload(ctx); err != nil {
		logger.Error("failed to reload prompt cache after deployment mutation: %v", err)
	}
}
