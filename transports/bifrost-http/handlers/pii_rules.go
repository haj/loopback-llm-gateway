// This file contains HTTP handlers for the Loopback Gateway PII redactor UI. It
// exposes CRUD over scoped PII redaction rules backed by configstore.TablePIIRule
// and, on every mutation, rebuilds the live Redactor (regex rules) and Presidio
// text-transformer set (presidio rules) and pushes them into the running
// loopback-guard plugin via its runtime Set* setters (no restart required).
//
// It mirrors the guardrails.go handler (which itself follows governance.go's
// RegisterRoutes + per-resource CRUD with SendJSON/SendError pattern).
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

// PIIRulesHandler manages HTTP requests for PII redaction rules.
type PIIRulesHandler struct {
	configStore configstore.ConfigStore
	// pluginResolver returns the live loopback-guard plugin instance, or nil
	// when the plugin is not currently loaded. Resolved lazily so plugin
	// reloads via /api/plugins are honored rather than capturing a stale ref.
	pluginResolver func() *loopbackguard.Plugin
}

// NewPIIRulesHandler creates a PII rules handler. pluginResolver may be nil
// (e.g. in tests); when nil, mutations persist but no live plugin is updated.
func NewPIIRulesHandler(configStore configstore.ConfigStore, pluginResolver func() *loopbackguard.Plugin) (*PIIRulesHandler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &PIIRulesHandler{configStore: configStore, pluginResolver: pluginResolver}, nil
}

// RegisterRoutes wires the PII rule CRUD endpoints.
func (h *PIIRulesHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/pii-rules", lib.ChainMiddlewares(h.getPIIRules, middlewares...))
	r.POST("/api/governance/pii-rules", lib.ChainMiddlewares(h.createPIIRule, middlewares...))
	r.GET("/api/governance/pii-rules/{id}", lib.ChainMiddlewares(h.getPIIRule, middlewares...))
	r.PUT("/api/governance/pii-rules/{id}", lib.ChainMiddlewares(h.updatePIIRule, middlewares...))
	r.DELETE("/api/governance/pii-rules/{id}", lib.ChainMiddlewares(h.deletePIIRule, middlewares...))
	r.POST("/api/governance/pii-rules/{id}/test", lib.ChainMiddlewares(h.testPIIRule, middlewares...))
}

// ---- request payloads ----

type createPIIRuleRequest struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Type        string  `json:"type"`
	Enabled     *bool   `json:"enabled,omitempty"`
	Scope       string  `json:"scope,omitempty"`
	ScopeID     *string `json:"scope_id,omitempty"`
	Order       *int    `json:"order,omitempty"`

	RegexPattern     string `json:"regex_pattern,omitempty"`
	RegexReplacement string `json:"regex_replacement,omitempty"`

	PresidioBaseURL        string  `json:"presidio_base_url,omitempty"`
	PresidioEntityType     string  `json:"presidio_entity_type,omitempty"`
	PresidioScoreThreshold float64 `json:"presidio_score_threshold,omitempty"`
}

type updatePIIRuleRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
	Order       *int    `json:"order,omitempty"`

	RegexPattern     *string `json:"regex_pattern,omitempty"`
	RegexReplacement *string `json:"regex_replacement,omitempty"`

	PresidioBaseURL        *string  `json:"presidio_base_url,omitempty"`
	PresidioEntityType     *string  `json:"presidio_entity_type,omitempty"`
	PresidioScoreThreshold *float64 `json:"presidio_score_threshold,omitempty"`
}

type testPIIRuleRequest struct {
	Text string `json:"text"`
}

// ---- handlers ----

func (h *PIIRulesHandler) getPIIRules(ctx *fasthttp.RequestCtx) {
	params := configstore.PIIRulesQueryParams{
		Search:  string(ctx.QueryArgs().Peek("search")),
		Scope:   string(ctx.QueryArgs().Peek("scope")),
		ScopeID: string(ctx.QueryArgs().Peek("scope_id")),
		Type:    string(ctx.QueryArgs().Peek("type")),
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

	rules, total, err := h.configStore.GetPIIRules(ctx, params)
	if err != nil {
		logger.Error("failed to retrieve pii rules: %v", err)
		SendError(ctx, 500, "Failed to retrieve PII rules")
		return
	}
	for i := range rules {
		h.resolveScopeName(ctx, &rules[i])
	}
	SendJSON(ctx, map[string]any{
		"pii_rules": rules,
		"total":     total,
		"count":     len(rules),
		"offset":    params.Offset,
	})
}

func (h *PIIRulesHandler) getPIIRule(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	rule, err := h.configStore.GetPIIRule(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "PII rule not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve PII rule")
		return
	}
	h.resolveScopeName(ctx, rule)
	SendJSON(ctx, map[string]any{"pii_rule": rule})
}

func (h *PIIRulesHandler) createPIIRule(ctx *fasthttp.RequestCtx) {
	var req createPIIRuleRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		SendError(ctx, 400, "name is required")
		return
	}
	if !configstoreTables.IsValidPIIRuleType(req.Type) {
		SendError(ctx, 400, fmt.Sprintf("Invalid type %q (want %q or %q)", req.Type, configstoreTables.PIIRuleTypeRegex, configstoreTables.PIIRuleTypePresidio))
		return
	}
	if req.Scope == "" {
		req.Scope = configstoreTables.PIIRuleScopeGlobal
	}
	if !configstoreTables.IsValidPIIRuleScope(req.Scope) {
		SendError(ctx, 400, fmt.Sprintf("Invalid scope %q", req.Scope))
		return
	}
	if req.Scope == configstoreTables.PIIRuleScopeGlobal {
		req.ScopeID = nil
	} else if req.ScopeID == nil || strings.TrimSpace(*req.ScopeID) == "" {
		SendError(ctx, 400, "scope_id is required when scope is not global")
		return
	}

	rule := &configstoreTables.TablePIIRule{
		ID:                     uuid.NewString(),
		Name:                   strings.TrimSpace(req.Name),
		Description:            req.Description,
		Type:                   req.Type,
		Enabled:                boolOrDefault(req.Enabled, true),
		Scope:                  req.Scope,
		ScopeID:                req.ScopeID,
		RegexPattern:           req.RegexPattern,
		RegexReplacement:       req.RegexReplacement,
		PresidioBaseURL:        strings.TrimSpace(req.PresidioBaseURL),
		PresidioEntityType:     strings.TrimSpace(req.PresidioEntityType),
		PresidioScoreThreshold: req.PresidioScoreThreshold,
		RuleOrder:              intOrDefault(req.Order, 0),
		CreatedAt:              time.Now(),
		UpdatedAt:              time.Now(),
	}
	h.applyScopeOwnership(rule)

	if errMsg := validatePIIRule(rule); errMsg != "" {
		SendError(ctx, 400, errMsg)
		return
	}

	if err := h.configStore.CreatePIIRule(ctx, rule); err != nil {
		logger.Error("failed to create pii rule: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to create PII rule: %v", err))
		return
	}
	h.reloadPlugin(ctx)
	h.resolveScopeName(ctx, rule)
	recordAudit(ctx, h.configStore, AuditActionPIIRuleCreate, configstoreTables.AuditOutcomeSuccess, rule.ID)
	SendJSON(ctx, map[string]any{
		"message":  "PII rule created successfully",
		"pii_rule": rule,
	})
}

func (h *PIIRulesHandler) updatePIIRule(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var req updatePIIRuleRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	rule, err := h.configStore.GetPIIRule(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "PII rule not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve PII rule")
		return
	}

	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			SendError(ctx, 400, "name cannot be empty")
			return
		}
		rule.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		rule.Description = *req.Description
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if req.Order != nil {
		rule.RuleOrder = *req.Order
	}
	if req.RegexPattern != nil {
		rule.RegexPattern = *req.RegexPattern
	}
	if req.RegexReplacement != nil {
		rule.RegexReplacement = *req.RegexReplacement
	}
	if req.PresidioBaseURL != nil {
		rule.PresidioBaseURL = strings.TrimSpace(*req.PresidioBaseURL)
	}
	if req.PresidioEntityType != nil {
		rule.PresidioEntityType = strings.TrimSpace(*req.PresidioEntityType)
	}
	if req.PresidioScoreThreshold != nil {
		rule.PresidioScoreThreshold = *req.PresidioScoreThreshold
	}
	rule.UpdatedAt = time.Now()

	if errMsg := validatePIIRule(rule); errMsg != "" {
		SendError(ctx, 400, errMsg)
		return
	}

	if err := h.configStore.UpdatePIIRule(ctx, rule); err != nil {
		logger.Error("failed to update pii rule: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Failed to update PII rule: %v", err))
		return
	}
	h.reloadPlugin(ctx)
	h.resolveScopeName(ctx, rule)
	recordAudit(ctx, h.configStore, AuditActionPIIRuleUpdate, configstoreTables.AuditOutcomeSuccess, rule.ID)
	SendJSON(ctx, map[string]any{
		"message":  "PII rule updated successfully",
		"pii_rule": rule,
	})
}

func (h *PIIRulesHandler) deletePIIRule(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if err := h.configStore.DeletePIIRule(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "PII rule not found")
			return
		}
		logger.Error("failed to delete pii rule: %v", err)
		SendError(ctx, 500, "Failed to delete PII rule")
		return
	}
	h.reloadPlugin(ctx)
	recordAudit(ctx, h.configStore, AuditActionPIIRuleDelete, configstoreTables.AuditOutcomeSuccess, id)
	SendJSON(ctx, map[string]any{"message": "PII rule deleted successfully"})
}

// testPIIRule applies a single stored rule to sample text and returns the
// redacted result. Useful for previewing a regex pattern before relying on it.
// Presidio rules require a reachable analyzer, so a transport error is surfaced
// (fail-open: original text returned) rather than failing the request.
func (h *PIIRulesHandler) testPIIRule(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	var req testPIIRuleRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	rule, err := h.configStore.GetPIIRule(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "PII rule not found")
			return
		}
		SendError(ctx, 500, "Failed to retrieve PII rule")
		return
	}

	switch rule.Type {
	case configstoreTables.PIIRuleTypeRegex:
		built, berr := loopbackguard.NewRegexReplaceRule(rule.Name, rule.RegexPattern, rule.RegexReplacement)
		if berr != nil {
			SendError(ctx, 400, fmt.Sprintf("Invalid regex pattern: %v", berr))
			return
		}
		redactor := loopbackguard.NewRedactor(built)
		redacted, matches := redactor.Redact(req.Text)
		SendJSON(ctx, map[string]any{
			"input":    req.Text,
			"redacted": redacted,
			"matched":  matches > 0,
			"type":     rule.Type,
		})
	case configstoreTables.PIIRuleTypePresidio:
		client := loopbackguard.NewPresidioClient(rule.PresidioBaseURL)
		if rule.PresidioScoreThreshold > 0 && rule.PresidioScoreThreshold <= 1 {
			client.ScoreThreshold = rule.PresidioScoreThreshold
		}
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		redacted, terr := client.Transform(tctx, req.Text)
		resp := map[string]any{
			"input":    req.Text,
			"redacted": redacted,
			"matched":  redacted != req.Text,
			"type":     rule.Type,
		}
		if terr != nil {
			resp["warning"] = fmt.Sprintf("presidio analyzer unreachable (fail-open): %v", terr)
		}
		SendJSON(ctx, resp)
	default:
		SendError(ctx, 400, fmt.Sprintf("Unsupported rule type %q", rule.Type))
	}
}

// ---- helpers ----

func intOrDefault(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// validatePIIRule checks per-type required fields and (for regex) that the
// pattern compiles. Returns a non-empty error message on failure. This is the
// HTTP-layer guard; TablePIIRule.BeforeSave repeats the structural checks.
func validatePIIRule(r *configstoreTables.TablePIIRule) string {
	switch r.Type {
	case configstoreTables.PIIRuleTypeRegex:
		if strings.TrimSpace(r.RegexPattern) == "" {
			return "regex_pattern is required for a regex rule"
		}
		if _, err := loopbackguard.NewRegexReplaceRule(r.Name, r.RegexPattern, r.RegexReplacement); err != nil {
			return fmt.Sprintf("invalid regex_pattern: %v", err)
		}
	case configstoreTables.PIIRuleTypePresidio:
		if strings.TrimSpace(r.PresidioBaseURL) == "" {
			return "presidio_base_url is required for a presidio rule"
		}
		if r.PresidioScoreThreshold != 0 && (r.PresidioScoreThreshold < 0 || r.PresidioScoreThreshold > 1) {
			return "presidio_score_threshold must be between 0 and 1"
		}
	default:
		return fmt.Sprintf("invalid type %q", r.Type)
	}
	return ""
}

// applyScopeOwnership sets the polymorphic owner FK matching the scope so a
// cascade delete of the owner cleans up the rule.
func (h *PIIRulesHandler) applyScopeOwnership(r *configstoreTables.TablePIIRule) {
	r.VirtualKeyID, r.TeamID, r.CustomerID = nil, nil, nil
	if r.ScopeID == nil {
		return
	}
	switch r.Scope {
	case configstoreTables.PIIRuleScopeVirtualKey:
		r.VirtualKeyID = r.ScopeID
	case configstoreTables.PIIRuleScopeTeam:
		r.TeamID = r.ScopeID
	case configstoreTables.PIIRuleScopeCustomer:
		r.CustomerID = r.ScopeID
	}
}

// resolveScopeName populates the transient ScopeName for a non-global rule so
// the UI can render a label instead of an opaque scope_id. Failures are
// non-fatal (ScopeName stays empty).
func (h *PIIRulesHandler) resolveScopeName(ctx context.Context, r *configstoreTables.TablePIIRule) {
	if r == nil || r.ScopeID == nil || *r.ScopeID == "" {
		return
	}
	switch r.Scope {
	case configstoreTables.PIIRuleScopeVirtualKey:
		if vk, err := h.configStore.GetVirtualKey(ctx, *r.ScopeID); err == nil && vk != nil {
			r.ScopeName = vk.Name
		}
	case configstoreTables.PIIRuleScopeTeam:
		if t, err := h.configStore.GetTeam(ctx, *r.ScopeID); err == nil && t != nil {
			r.ScopeName = t.Name
		}
	case configstoreTables.PIIRuleScopeCustomer:
		if cust, err := h.configStore.GetCustomer(ctx, *r.ScopeID); err == nil && cust != nil {
			r.ScopeName = cust.Name
		}
	}
}

// reloadPlugin rebuilds the live Redactor (regex rules) and Presidio transformer
// set (presidio rules) from every enabled rule and pushes them into the running
// loopback-guard plugin. Best-effort: failures are logged but do not fail the
// API request (the DB is the source of truth and a later restart re-derives the
// same state). An invalid regex skips that one rule rather than dropping all.
func (h *PIIRulesHandler) reloadPlugin(ctx context.Context) {
	if h.pluginResolver == nil {
		return
	}
	plugin := h.pluginResolver()
	if plugin == nil {
		return
	}
	rules, err := h.configStore.GetEnabledPIIRules(ctx)
	if err != nil {
		logger.Error("failed to load enabled pii rules for plugin reload: %v", err)
		return
	}
	redactor, berr := buildRedactorFromRules(rules)
	if berr != nil {
		logger.Warn("skipping invalid regex rule(s) during pii reload: %v", berr)
		// Fall back to rebuilding from only the rules that compile.
		redactor = buildRedactorSkippingInvalid(rules)
	}
	plugin.SetRedactor(redactor)
	plugin.SetTransformers(buildPresidioTransformersFromRules(rules)...)
}

// buildRedactorSkippingInvalid is the resilient fallback used when one or more
// regex rules fail to compile: it builds a Redactor from only the valid ones so
// a single bad pattern cannot disable redaction entirely.
func buildRedactorSkippingInvalid(rules []configstoreTables.TablePIIRule) *loopbackguard.Redactor {
	valid := make([]configstoreTables.TablePIIRule, 0, len(rules))
	for _, r := range rules {
		if !r.Enabled || r.Type != configstoreTables.PIIRuleTypeRegex {
			continue
		}
		if _, err := loopbackguard.NewRegexReplaceRule(r.Name, r.RegexPattern, r.RegexReplacement); err == nil {
			valid = append(valid, r)
		}
	}
	redactor, _ := buildRedactorFromRules(valid)
	return redactor
}
