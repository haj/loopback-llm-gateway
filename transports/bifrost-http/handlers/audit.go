// This file contains the shared audit-log recording helper and the read-only
// HTTP handler backing the Loopback Gateway audit-logs UI.
//
// recordAudit is the single entry point every governance mutation point calls
// (virtual key / team / customer / user / guardrail / PII rule create-update-
// delete). It captures the actor and originating IP from the request context,
// signs the event with an HMAC key, and appends it to the append-only audit log
// via the ConfigStore. Failures are logged but never block the originating
// request — auditing is best-effort and must not break a successful mutation.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/alerting"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// Audit action identifiers. Format is "<entity>.<verb>" so the UI can group and
// filter by entity. Kept as constants so mutation points share one spelling.
const (
	AuditActionVirtualKeyCreate = "virtual_key.create"
	AuditActionVirtualKeyUpdate = "virtual_key.update"
	AuditActionVirtualKeyDelete = "virtual_key.delete"

	AuditActionTeamCreate = "team.create"
	AuditActionTeamUpdate = "team.update"
	AuditActionTeamDelete = "team.delete"

	AuditActionCustomerCreate = "customer.create"
	AuditActionCustomerUpdate = "customer.update"
	AuditActionCustomerDelete = "customer.delete"

	AuditActionUserCreate = "user.create"
	AuditActionUserUpdate = "user.update"
	AuditActionUserDelete = "user.delete"

	AuditActionGuardrailCreate = "guardrail.create"
	AuditActionGuardrailUpdate = "guardrail.update"
	AuditActionGuardrailDelete = "guardrail.delete"

	AuditActionPIIRuleCreate = "pii_rule.create"
	AuditActionPIIRuleUpdate = "pii_rule.update"
	AuditActionPIIRuleDelete = "pii_rule.delete"

	AuditActionMCPToolGroupCreate = "mcp_tool_group.create"
	AuditActionMCPToolGroupUpdate = "mcp_tool_group.update"
	AuditActionMCPToolGroupDelete = "mcp_tool_group.delete"

	AuditActionPromptDeploymentCreate = "prompt_deployment.create"
	AuditActionPromptDeploymentUpdate = "prompt_deployment.update"
	AuditActionPromptDeploymentDelete = "prompt_deployment.delete"

	AuditActionCircuitBreakerCreate = "circuit_breaker.create"
	AuditActionCircuitBreakerUpdate = "circuit_breaker.update"
	AuditActionCircuitBreakerDelete = "circuit_breaker.delete"
	AuditActionCircuitBreakerReset  = "circuit_breaker.reset"

	AuditActionVaultRefresh        = "vault.refresh"
	AuditActionVaultTestConnection = "vault.test_connection"

	AuditActionAuditSettingsUpdate = "audit_settings.update"
	AuditActionAuditLogPrune       = "audit_log.prune"

	AuditActionAlertChannelCreate = "alert_channel.create"
	AuditActionAlertChannelUpdate = "alert_channel.update"
	AuditActionAlertChannelDelete = "alert_channel.delete"
	AuditActionAlertChannelTest   = "alert_channel.test"

	AuditActionJWTAuthCreate = "jwt_auth.create"
	AuditActionJWTAuthUpdate = "jwt_auth.update"
	AuditActionJWTAuthDelete = "jwt_auth.delete"

	AuditActionRBACEnforce = "rbac.enforce"
)

// auditHMACEnvVar is the environment variable that supplies the audit-log HMAC
// signing key. When unset, a fixed development key is used and a warning is
// logged so the signature is still well-formed in local/test builds.
const auditHMACEnvVar = "LOOPBACK_GATEWAY_AUDIT_HMAC_KEY"

var (
	auditKeyOnce sync.Once
	auditKey     []byte
)

// auditHMACKey returns the HMAC signing key for audit events, resolved once from
// the environment. The key is process-stable so signatures remain verifiable
// for the lifetime of the deployment.
func auditHMACKey() []byte {
	auditKeyOnce.Do(func() {
		if v := os.Getenv(auditHMACEnvVar); v != "" {
			auditKey = []byte(v)
			return
		}
		if logger != nil {
			logger.Warn("audit log HMAC key (%s) not set; using a default development key. Set %s in production for tamper-evident audit logs.", auditHMACEnvVar, auditHMACEnvVar)
		}
		auditKey = []byte("loopback-gateway-default-audit-key")
	})
	return auditKey
}

// auditActor derives the acting identity from the request context. The auth
// middleware sets the user name/ID on the context; the password-authenticated
// local admin is reported as "local-admin". Falls back to "unknown".
func auditActor(ctx *fasthttp.RequestCtx) string {
	if name, ok := ctx.UserValue(schemas.BifrostContextKeyUserName).(string); ok && name != "" {
		return name
	}
	if id, ok := ctx.UserValue(schemas.BifrostContextKeyUserID).(string); ok && id != "" {
		return id
	}
	if isLocal, ok := ctx.UserValue(schemas.IsLocalAdminContextKey).(bool); ok && isLocal {
		return "local-admin"
	}
	return "unknown"
}

// auditIP returns the originating client IP, preferring a trusted forwarded
// header and falling back to the TCP peer address.
func auditIP(ctx *fasthttp.RequestCtx) string {
	if fwd := clientForwardedIP(ctx); fwd != "" {
		return fwd
	}
	return ctx.RemoteAddr().String()
}

// appendAuditLog signs, persists, and fans out one audit event. It is the
// single write seam shared by recordAudit (request-path governance mutations)
// and the retention worker (system prune-marker events): after a successful
// CreateAuditLog the entry is offered, nil-safely and non-blockingly, to every
// registered AuditSink (see SetAuditSink in auditexport.go) — the export
// pipeline today, the alert-channels publisher later. With no sinks registered
// the write path is byte-for-byte what it was before the seam existed.
func appendAuditLog(ctx context.Context, store configstore.ConfigStore, entry *configstoreTables.TableAuditLog) error {
	entry.Sign(auditHMACKey())
	if err := store.CreateAuditLog(ctx, entry); err != nil {
		return err
	}
	auditFanOut(entry)
	publishAuditAlert(entry)
	return nil
}

// publishAuditAlert offers a persisted audit event to the alerting pipeline as
// an audit.mutation alert. Nil-safe and non-blocking (Publish drops rather
// than stalls). No DedupKey: governance mutations are individually meaningful
// and low-volume, so suppression would hide real changes. Alert deliveries
// themselves are NOT audited — that would recurse.
func publishAuditAlert(entry *configstoreTables.TableAuditLog) {
	if alertPublisher == nil || entry == nil {
		return
	}
	severity := alerting.SeverityInfo
	if entry.Outcome == configstoreTables.AuditOutcomeFailure {
		severity = alerting.SeverityWarning
	}
	alertPublisher.Publish(alerting.Event{
		Type:     alerting.EventTypeAuditMutation,
		Severity: severity,
		Title:    "Governance mutation: " + entry.Action,
		Message:  fmt.Sprintf("%s (%s) by %s", entry.Action, entry.Outcome, entry.Actor),
		Fields: map[string]string{
			"action":  entry.Action,
			"outcome": entry.Outcome,
			"actor":   entry.Actor,
			"target":  entry.Target,
			"ip":      entry.IP,
		},
		Timestamp: entry.Timestamp,
	})
}

// recordAudit appends a single signed audit event for a governance mutation.
// It is the shared entry point for every mutation point. Best-effort: a failure
// to persist the event is logged but never propagated, so auditing cannot break
// an otherwise successful request.
func recordAudit(ctx *fasthttp.RequestCtx, store configstore.ConfigStore, action, outcome, target string) {
	if store == nil {
		return
	}
	entry := &configstoreTables.TableAuditLog{
		ID:        uuid.NewString(),
		Action:    action,
		Outcome:   outcome,
		Actor:     auditActor(ctx),
		IP:        auditIP(ctx),
		Target:    target,
		Timestamp: time.Now(),
	}
	if err := appendAuditLog(ctx, store, entry); err != nil {
		logger.Warn("failed to record audit log for %s (%s): %v", action, target, err)
	}
}

// AuditLogsHandler serves the audit-logs UI: querying the append-only trail,
// the retention/export settings singleton, the NDJSON export download, manual
// pruning, and signature verification.
//
// Note: transports/config.schema.json contains an unwired upstream
// audit_logs_config $def (retention_days / hmac_key). Retention and export are
// deliberately configured through the configstore settings row (matching the
// circuit-breaker precedent), not config.json; the schema stub is left alone.
type AuditLogsHandler struct {
	configStore configstore.ConfigStore
	// retention backs the manual prune endpoint and (started by the server) the
	// scheduled retention routine.
	retention *AuditRetentionWorker
}

// NewAuditLogsHandler creates an audit-logs handler.
func NewAuditLogsHandler(configStore configstore.ConfigStore) (*AuditLogsHandler, error) {
	if configStore == nil {
		return nil, errAuditConfigStoreRequired
	}
	return &AuditLogsHandler{
		configStore: configStore,
		retention:   NewAuditRetentionWorker(configStore),
	}, nil
}

// RetentionWorker exposes the handler's retention worker so the server can
// start/stop its background routine.
func (h *AuditLogsHandler) RetentionWorker() *AuditRetentionWorker { return h.retention }

var errAuditConfigStoreRequired = errAudit("config store is required")

// errAudit is a tiny error type so the constructor needs no extra import.
type errAudit string

func (e errAudit) Error() string { return string(e) }

// RegisterRoutes wires the audit-log read endpoints.
func (h *AuditLogsHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/governance/audit-logs", lib.ChainMiddlewares(h.getAuditLogs, middlewares...))
	r.GET("/api/governance/audit-logs/settings", lib.ChainMiddlewares(h.getSettings, middlewares...))
	r.GET("/api/governance/audit-logs/export", lib.ChainMiddlewares(h.exportAuditLogs, middlewares...))
	r.GET("/api/governance/audit-logs/verify", lib.ChainMiddlewares(h.verifyAuditLogs, middlewares...))
}

// RegisterMutatingRoutes wires the endpoints that change state (settings update
// and manual prune). Registered by the server with the RBAC-gated mutating
// middleware chain, mirroring the other governance handlers.
func (h *AuditLogsHandler) RegisterMutatingRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.PUT("/api/governance/audit-logs/settings", lib.ChainMiddlewares(h.updateSettings, middlewares...))
	r.POST("/api/governance/audit-logs/prune", lib.ChainMiddlewares(h.pruneAuditLogs, middlewares...))
}

// parseAuditLogsFilterParams extracts the shared filter query parameters
// (action/outcome/actor/target/search plus since/until RFC3339 bounds). Reports
// a 400 and returns false on malformed input.
func parseAuditLogsFilterParams(ctx *fasthttp.RequestCtx) (configstore.AuditLogsQueryParams, bool) {
	params := configstore.AuditLogsQueryParams{
		Action:  string(ctx.QueryArgs().Peek("action")),
		Outcome: string(ctx.QueryArgs().Peek("outcome")),
		Actor:   string(ctx.QueryArgs().Peek("actor")),
		Target:  string(ctx.QueryArgs().Peek("target")),
		Search:  string(ctx.QueryArgs().Peek("search")),
	}
	if v := string(ctx.QueryArgs().Peek("since")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			SendError(ctx, 400, "Invalid since parameter: must be an RFC3339 timestamp")
			return params, false
		}
		params.Since = &t
	}
	if v := string(ctx.QueryArgs().Peek("until")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			SendError(ctx, 400, "Invalid until parameter: must be an RFC3339 timestamp")
			return params, false
		}
		params.Until = &t
	}
	return params, true
}

func (h *AuditLogsHandler) getAuditLogs(ctx *fasthttp.RequestCtx) {
	params, ok := parseAuditLogsFilterParams(ctx)
	if !ok {
		return
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

	logs, total, err := h.configStore.GetAuditLogs(ctx, params)
	if err != nil {
		logger.Error("failed to retrieve audit logs: %v", err)
		SendError(ctx, 500, "Failed to retrieve audit logs")
		return
	}
	SendJSON(ctx, map[string]any{
		"audit_logs": logs,
		"total":      total,
		"count":      len(logs),
		"offset":     params.Offset,
	})
}

// ---- retention/export settings ----

// auditLogSettingsInput is the PUT settings payload. Pointer fields distinguish
// "leave unchanged" from explicit zero values.
type auditLogSettingsInput struct {
	RetentionMaxAgeDays *int    `json:"retention_max_age_days,omitempty"`
	RetentionMaxRows    *int64  `json:"retention_max_rows,omitempty"`
	ExportEnabled       *bool   `json:"export_enabled,omitempty"`
	ExportType          *string `json:"export_type,omitempty"`
	ExportFilePath      *string `json:"export_file_path,omitempty"`
	SyslogNetwork       *string `json:"syslog_network,omitempty"`
	SyslogAddress       *string `json:"syslog_address,omitempty"`
	SyslogTag           *string `json:"syslog_tag,omitempty"`
}

// getSettings returns the singleton settings row, or an all-defaults row when
// the feature has never been configured (default-off).
func (h *AuditLogsHandler) getSettings(ctx *fasthttp.RequestCtx) {
	settings, err := h.configStore.GetAuditLogSettings(ctx)
	if err != nil {
		if !errors.Is(err, configstore.ErrNotFound) {
			logger.Error("failed to retrieve audit log settings: %v", err)
			SendError(ctx, 500, "Failed to retrieve audit log settings")
			return
		}
		settings = &configstoreTables.TableAuditLogSettings{ID: configstoreTables.AuditLogSettingsID}
	}
	SendJSON(ctx, map[string]any{"settings": settings})
}

// updateSettings validates and upserts the settings row, records an
// audit_settings.update event, and hot-swaps the export sink to match.
func (h *AuditLogsHandler) updateSettings(ctx *fasthttp.RequestCtx) {
	var req auditLogSettingsInput
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}

	settings, err := h.configStore.GetAuditLogSettings(ctx)
	if err != nil {
		if !errors.Is(err, configstore.ErrNotFound) {
			logger.Error("failed to retrieve audit log settings: %v", err)
			SendError(ctx, 500, "Failed to retrieve audit log settings")
			return
		}
		settings = &configstoreTables.TableAuditLogSettings{
			ID:        configstoreTables.AuditLogSettingsID,
			CreatedAt: time.Now(),
		}
	}
	if req.RetentionMaxAgeDays != nil {
		settings.RetentionMaxAgeDays = *req.RetentionMaxAgeDays
	}
	if req.RetentionMaxRows != nil {
		settings.RetentionMaxRows = *req.RetentionMaxRows
	}
	if req.ExportEnabled != nil {
		settings.ExportEnabled = *req.ExportEnabled
	}
	if req.ExportType != nil {
		settings.ExportType = strings.TrimSpace(*req.ExportType)
	}
	if req.ExportFilePath != nil {
		settings.ExportFilePath = strings.TrimSpace(*req.ExportFilePath)
	}
	if req.SyslogNetwork != nil {
		settings.SyslogNetwork = strings.TrimSpace(*req.SyslogNetwork)
	}
	if req.SyslogAddress != nil {
		settings.SyslogAddress = strings.TrimSpace(*req.SyslogAddress)
	}
	if req.SyslogTag != nil {
		settings.SyslogTag = strings.TrimSpace(*req.SyslogTag)
	}
	settings.UpdatedAt = time.Now()

	if msg := validateAuditLogSettings(settings); msg != "" {
		SendError(ctx, 400, msg)
		return
	}

	// Prove the export destination is buildable BEFORE persisting, so a bad
	// path/address is rejected instead of saved. The probe is closed
	// immediately; applyAuditExportSettings builds the live one after the save.
	if settings.ExportEnabled {
		probe, err := buildAuditExportDestination(settings)
		if err != nil {
			SendError(ctx, 400, fmt.Sprintf("Invalid export destination: %v", err))
			return
		}
		if closeErr := probe.Close(); closeErr != nil {
			logger.Warn("failed to close audit export destination probe: %v", closeErr)
		}
	}

	if err := h.configStore.UpdateAuditLogSettings(ctx, settings); err != nil {
		logger.Error("failed to update audit log settings: %v", err)
		SendError(ctx, 500, "Failed to update audit log settings")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionAuditSettingsUpdate, configstoreTables.AuditOutcomeSuccess, settings.ID)
	if err := applyAuditExportSettings(settings); err != nil {
		// Settings persisted but the live sink swap failed; surface it.
		logger.Error("failed to apply audit export settings: %v", err)
		SendError(ctx, 500, fmt.Sprintf("Settings saved but export could not be enabled: %v", err))
		return
	}
	SendJSON(ctx, map[string]any{
		"message":  "Audit log settings updated successfully",
		"settings": settings,
	})
}

// validateAuditLogSettings returns a client-facing message for invalid
// combinations, or "" when the settings are acceptable.
func validateAuditLogSettings(settings *configstoreTables.TableAuditLogSettings) string {
	if settings.RetentionMaxAgeDays < 0 {
		return "retention_max_age_days must be zero (unlimited) or positive"
	}
	if settings.RetentionMaxRows < 0 {
		return "retention_max_rows must be zero (unlimited) or positive"
	}
	switch settings.ExportType {
	case "", configstoreTables.AuditExportTypeFile, configstoreTables.AuditExportTypeSyslog:
	default:
		return fmt.Sprintf("export_type must be %q or %q", configstoreTables.AuditExportTypeFile, configstoreTables.AuditExportTypeSyslog)
	}
	switch settings.SyslogNetwork {
	case "", "udp", "tcp":
	default:
		return "syslog_network must be empty (local), \"udp\", or \"tcp\""
	}
	if settings.ExportEnabled {
		switch settings.ExportType {
		case configstoreTables.AuditExportTypeFile:
			if settings.ExportFilePath == "" {
				return "export_file_path is required when export_type is \"file\""
			}
		case configstoreTables.AuditExportTypeSyslog:
			if !syslogExportSupported {
				return "syslog export is not supported on windows"
			}
			if settings.SyslogNetwork != "" && settings.SyslogAddress == "" {
				return "syslog_address is required when syslog_network is \"udp\" or \"tcp\""
			}
		default:
			return "export_type is required when export is enabled"
		}
	}
	return ""
}

// ---- NDJSON export ----

// auditExportPageSize is the keyset page size for the streaming NDJSON export.
const auditExportPageSize = 500

// exportAuditLogs streams the filtered audit trail as NDJSON
// (application/x-ndjson), one signed row per line, iterating with the keyset
// cursor so arbitrarily large trails export without OFFSET degradation. This is
// the backfill path for rows written before live export was enabled — the live
// tail only covers events recorded while an export sink is installed.
func (h *AuditLogsHandler) exportAuditLogs(ctx *fasthttp.RequestCtx) {
	params, ok := parseAuditLogsFilterParams(ctx)
	if !ok {
		return
	}
	ctx.SetContentType("application/x-ndjson")
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="audit-logs.ndjson"`)

	var afterTS time.Time
	var afterID string
	for {
		page, err := h.configStore.GetAuditLogsSince(ctx, params, afterTS, afterID, auditExportPageSize)
		if err != nil {
			logger.Error("failed to export audit logs: %v", err)
			// Headers may already be written; terminate the stream.
			SendError(ctx, 500, "Failed to export audit logs")
			return
		}
		for i := range page {
			line, err := json.Marshal(&page[i])
			if err != nil {
				logger.Error("failed to marshal audit log %s for export: %v", page[i].ID, err)
				SendError(ctx, 500, "Failed to export audit logs")
				return
			}
			ctx.Write(line)      //nolint:errcheck
			ctx.WriteString("\n") //nolint:errcheck
		}
		if len(page) < auditExportPageSize {
			return
		}
		last := page[len(page)-1]
		afterTS, afterID = last.Timestamp, last.ID
	}
}

// ---- manual prune ----

// pruneAuditLogs runs one retention pass immediately (same code path as the
// scheduled worker), attributed to the calling admin in the signed
// audit_log.prune marker.
func (h *AuditLogsHandler) pruneAuditLogs(ctx *fasthttp.RequestCtx) {
	result, err := h.retention.pruneOnce(ctx, auditActor(ctx), auditIP(ctx))
	if err != nil {
		logger.Error("failed to prune audit logs: %v", err)
		SendError(ctx, 500, "Failed to prune audit logs")
		return
	}
	SendJSON(ctx, map[string]any{
		"message":          "Audit log prune completed",
		"deleted":          result.Deleted(),
		"deleted_by_age":   result.DeletedByAge,
		"deleted_by_rows":  result.DeletedByCount,
		"prune_marker_id":  result.MarkerID,
		"signature_digest": result.Digest,
	})
}

// ---- signature verification ----

// auditVerifyMaxRows caps how many rows one verify call recomputes; larger
// trails are verified in filtered slices (or offline from an export).
const auditVerifyMaxRows = 1000

// verifyAuditLogs recomputes each row's HMAC over its canonical event for a
// filtered slice of the trail and reports valid/invalid counts. Only meaningful
// when LOOPBACK_GATEWAY_AUDIT_HMAC_KEY is set to a real key (the development
// fallback key is well-known).
func (h *AuditLogsHandler) verifyAuditLogs(ctx *fasthttp.RequestCtx) {
	params, ok := parseAuditLogsFilterParams(ctx)
	if !ok {
		return
	}
	limit := auditVerifyMaxRows
	if v := string(ctx.QueryArgs().Peek("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > auditVerifyMaxRows {
			SendError(ctx, 400, fmt.Sprintf("Invalid limit parameter: must be between 1 and %d", auditVerifyMaxRows))
			return
		}
		limit = n
	}

	key := auditHMACKey()
	checked, valid := 0, 0
	invalidIDs := []string{}
	var afterTS time.Time
	var afterID string
	for checked < limit {
		pageSize := limit - checked
		if pageSize > auditExportPageSize {
			pageSize = auditExportPageSize
		}
		page, err := h.configStore.GetAuditLogsSince(ctx, params, afterTS, afterID, pageSize)
		if err != nil {
			logger.Error("failed to verify audit logs: %v", err)
			SendError(ctx, 500, "Failed to verify audit logs")
			return
		}
		for i := range page {
			checked++
			if page[i].VerifySignature(key) {
				valid++
			} else if len(invalidIDs) < 100 {
				invalidIDs = append(invalidIDs, page[i].ID)
			}
		}
		if len(page) < pageSize {
			break
		}
		last := page[len(page)-1]
		afterTS, afterID = last.Timestamp, last.ID
	}
	SendJSON(ctx, map[string]any{
		"checked":     checked,
		"valid":       valid,
		"invalid":     checked - valid,
		"invalid_ids": invalidIDs,
	})
}
