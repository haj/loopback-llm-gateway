// This file contains HTTP handlers for the Loopback Gateway per-provider circuit
// breaker UI. It exposes CRUD over circuit-breaker policies backed by
// configstore.TableCircuitBreakerConfig and, on every mutation, pushes the
// resulting policy into the running core engine via the core circuit-breaker API
// (Bifrost.ConfigureCircuitBreaker / RemoveCircuitBreaker) so changes take
// effect without a restart. It also exposes the live breaker state and a manual
// reset.
//
// It follows the guardrails.go handler patterns (RegisterRoutes + per-resource
// CRUD with SendJSON/SendError + recordAudit). The breaker is OPT-IN /
// default-off: until a policy is created here, the core hot path is a complete
// no-op (see core/circuitbreaker.go).
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
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// CircuitBreakerHandler manages HTTP requests for circuit-breaker policies.
type CircuitBreakerHandler struct {
	configStore configstore.ConfigStore
	// clientResolver returns the live core engine, or nil when unavailable
	// (e.g. tests). Resolved lazily so the handler never captures a stale ref.
	// When nil, mutations still persist but no live engine is updated.
	clientResolver func() *bifrost.Bifrost
}

// NewCircuitBreakerHandler creates a circuit-breaker handler. clientResolver may
// be nil (e.g. in tests); when nil, mutations persist but the live engine is not
// updated.
func NewCircuitBreakerHandler(configStore configstore.ConfigStore, clientResolver func() *bifrost.Bifrost) (*CircuitBreakerHandler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &CircuitBreakerHandler{configStore: configStore, clientResolver: clientResolver}, nil
}

// RegisterRoutes wires the circuit-breaker CRUD + state endpoints.
func (h *CircuitBreakerHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/circuit-breakers", lib.ChainMiddlewares(h.listPolicies, middlewares...))
	r.POST("/api/governance/circuit-breakers", lib.ChainMiddlewares(h.createPolicy, middlewares...))
	r.GET("/api/governance/circuit-breakers/state", lib.ChainMiddlewares(h.getState, middlewares...))
	r.GET("/api/governance/circuit-breakers/{id}", lib.ChainMiddlewares(h.getPolicy, middlewares...))
	r.PUT("/api/governance/circuit-breakers/{id}", lib.ChainMiddlewares(h.updatePolicy, middlewares...))
	r.DELETE("/api/governance/circuit-breakers/{id}", lib.ChainMiddlewares(h.deletePolicy, middlewares...))
	r.POST("/api/governance/circuit-breakers/{id}/reset", lib.ChainMiddlewares(h.resetPolicy, middlewares...))
}

// ApplyAllToEngine re-pushes every enabled policy into the core engine. Called
// at startup so persisted policies are live without a mutation. Best-effort.
func (h *CircuitBreakerHandler) ApplyAllToEngine(ctx context.Context) {
	client := h.client()
	if client == nil {
		return
	}
	configs, err := h.configStore.GetCircuitBreakerConfigs(ctx)
	if err != nil {
		logger.Error("failed to load circuit breaker configs for engine apply: %v", err)
		return
	}
	for i := range configs {
		h.applyToEngine(&configs[i])
	}
}

// ---- request payloads ----

type circuitBreakerInput struct {
	Provider         string `json:"provider"`
	Enabled          *bool  `json:"enabled,omitempty"`
	FailureThreshold int    `json:"failure_threshold,omitempty"`
	CooldownSeconds  int    `json:"cooldown_seconds,omitempty"`
	HalfOpenProbes   int    `json:"half_open_probes,omitempty"`
}

type circuitBreakerUpdateInput struct {
	Enabled          *bool `json:"enabled,omitempty"`
	FailureThreshold *int  `json:"failure_threshold,omitempty"`
	CooldownSeconds  *int  `json:"cooldown_seconds,omitempty"`
	HalfOpenProbes   *int  `json:"half_open_probes,omitempty"`
}

// ---- handlers ----

func (h *CircuitBreakerHandler) listPolicies(ctx *fasthttp.RequestCtx) {
	configs, err := h.configStore.GetCircuitBreakerConfigs(ctx)
	if err != nil {
		logger.Error("failed to retrieve circuit breaker configs: %v", err)
		SendError(ctx, 500, "Failed to retrieve circuit breaker configs")
		return
	}
	SendJSON(ctx, map[string]any{
		"circuit_breakers": configs,
		"count":            len(configs),
	})
}

func (h *CircuitBreakerHandler) getPolicy(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	config, err := h.configStore.GetCircuitBreakerConfig(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Circuit breaker config not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve circuit breaker config")
		return
	}
	SendJSON(ctx, map[string]any{"circuit_breaker": config})
}

// getState returns the live, in-memory breaker state from the core engine
// (closed/open/half-open + counters). Empty when the feature is unconfigured.
func (h *CircuitBreakerHandler) getState(ctx *fasthttp.RequestCtx) {
	client := h.client()
	var states []bifrost.CircuitBreakerStatus
	if client != nil {
		states = client.GetCircuitBreakerStates()
	}
	SendJSON(ctx, map[string]any{
		"states": states,
		"count":  len(states),
	})
}

func (h *CircuitBreakerHandler) createPolicy(ctx *fasthttp.RequestCtx) {
	var req circuitBreakerInput
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	req.Provider = strings.TrimSpace(req.Provider)
	if req.Provider == "" {
		SendError(ctx, 400, "provider is required")
		return
	}
	// One policy per provider.
	if existing, err := h.configStore.GetCircuitBreakerConfigByProvider(ctx, req.Provider); err == nil && existing != nil {
		SendError(ctx, 409, fmt.Sprintf("A circuit breaker policy already exists for provider %q", req.Provider))
		return
	} else if err != nil && !errors.Is(err, configstore.ErrNotFound) {
		SendError(ctx, 500, "Failed to check existing circuit breaker config")
		return
	}

	config := &configstoreTables.TableCircuitBreakerConfig{
		ID:               uuid.NewString(),
		Provider:         req.Provider,
		Enabled:          boolOrDefault(req.Enabled, true),
		FailureThreshold: req.FailureThreshold,
		CooldownSeconds:  req.CooldownSeconds,
		HalfOpenProbes:   req.HalfOpenProbes,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := h.configStore.CreateCircuitBreakerConfig(ctx, config); err != nil {
		logger.Error("failed to create circuit breaker config: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to create circuit breaker config: %v", err))
		return
	}
	h.applyToEngine(config)
	recordAudit(ctx, h.configStore, AuditActionCircuitBreakerCreate, configstoreTables.AuditOutcomeSuccess, config.ID)
	SendJSON(ctx, map[string]any{
		"message":         "Circuit breaker policy created successfully",
		"circuit_breaker": config,
	})
}

func (h *CircuitBreakerHandler) updatePolicy(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var req circuitBreakerUpdateInput
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	config, err := h.configStore.GetCircuitBreakerConfig(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Circuit breaker config not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve circuit breaker config")
		return
	}
	if req.Enabled != nil {
		config.Enabled = *req.Enabled
	}
	if req.FailureThreshold != nil {
		config.FailureThreshold = *req.FailureThreshold
	}
	if req.CooldownSeconds != nil {
		config.CooldownSeconds = *req.CooldownSeconds
	}
	if req.HalfOpenProbes != nil {
		config.HalfOpenProbes = *req.HalfOpenProbes
	}
	config.UpdatedAt = time.Now()

	if err := h.configStore.UpdateCircuitBreakerConfig(ctx, config); err != nil {
		logger.Error("failed to update circuit breaker config: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to update circuit breaker config: %v", err))
		return
	}
	h.applyToEngine(config)
	recordAudit(ctx, h.configStore, AuditActionCircuitBreakerUpdate, configstoreTables.AuditOutcomeSuccess, config.ID)
	SendJSON(ctx, map[string]any{
		"message":         "Circuit breaker policy updated successfully",
		"circuit_breaker": config,
	})
}

func (h *CircuitBreakerHandler) deletePolicy(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	// Load first so we know which provider to remove from the engine.
	config, err := h.configStore.GetCircuitBreakerConfig(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Circuit breaker config not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve circuit breaker config")
		return
	}
	if err := h.configStore.DeleteCircuitBreakerConfig(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Circuit breaker config not found")
			return
		}
		logger.Error("failed to delete circuit breaker config: %v", err)
		SendError(ctx, 500, "Failed to delete circuit breaker config")
		return
	}
	if client := h.client(); client != nil {
		client.RemoveCircuitBreaker(schemas.ModelProvider(config.Provider))
	}
	recordAudit(ctx, h.configStore, AuditActionCircuitBreakerDelete, configstoreTables.AuditOutcomeSuccess, id)
	SendJSON(ctx, map[string]any{"message": "Circuit breaker policy deleted successfully"})
}

// resetPolicy forces the breaker for a policy's provider back to CLOSED in the
// live engine (manual recovery). It does not change the persisted policy.
func (h *CircuitBreakerHandler) resetPolicy(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	config, err := h.configStore.GetCircuitBreakerConfig(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Circuit breaker config not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve circuit breaker config")
		return
	}
	if client := h.client(); client != nil {
		client.ResetCircuitBreaker(schemas.ModelProvider(config.Provider))
	}
	recordAudit(ctx, h.configStore, AuditActionCircuitBreakerReset, configstoreTables.AuditOutcomeSuccess, id)
	SendJSON(ctx, map[string]any{"message": "Circuit breaker reset successfully"})
}

// ---- helpers ----

func (h *CircuitBreakerHandler) client() *bifrost.Bifrost {
	if h.clientResolver == nil {
		return nil
	}
	return h.clientResolver()
}

// applyToEngine pushes a single policy into the live engine. A disabled policy
// is removed (allow-all for that provider). Best-effort: a nil engine (tests) is
// a no-op since the DB is the source of truth and startup re-applies.
func (h *CircuitBreakerHandler) applyToEngine(config *configstoreTables.TableCircuitBreakerConfig) {
	client := h.client()
	if client == nil || config == nil {
		return
	}
	provider := schemas.ModelProvider(config.Provider)
	if !config.Enabled {
		client.RemoveCircuitBreaker(provider)
		return
	}
	client.ConfigureCircuitBreaker(provider, bifrost.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: config.FailureThreshold,
		Cooldown:         time.Duration(config.CooldownSeconds) * time.Second,
		HalfOpenProbes:   config.HalfOpenProbes,
	})
}
