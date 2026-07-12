// This file contains HTTP handlers for the Loopback Gateway alert-channels UI.
// It exposes CRUD over alert channels backed by configstore.TableAlertChannel
// plus a synchronous test-fire endpoint, and on every mutation records an
// audit event and reloads the framework/alerting dispatcher so changes take
// effect without a restart.
//
// It follows the circuitbreaker.go handler patterns (RegisterRoutes +
// SendJSON/SendError + recordAudit). The feature is OPT-IN / default-off:
// until a channel is created here, alerting.Dispatcher.Publish is a no-op (see
// framework/alerting).
//
// Secrets (PagerDuty routing keys, webhook signing keys) are encrypted at rest
// by the table hooks and NEVER serialized in responses — clients see only a
// has_secret flag; an update that omits secret preserves the stored value.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// alertEventTypeSet is the set of event types a channel filter may reference.
var alertEventTypeSet = map[string]bool{
	alerting.EventTypeAuditMutation:        true,
	alerting.EventTypeBudgetExceeded:       true,
	alerting.EventTypeRateLimitExceeded:    true,
	alerting.EventTypeCircuitBreakerOpen:   true,
	alerting.EventTypeCircuitBreakerClosed: true,
}

// AlertChannelsHandler manages HTTP requests for alert channels.
type AlertChannelsHandler struct {
	configStore configstore.ConfigStore
	// dispatcher is reloaded after every mutation so channel changes apply
	// live. May be nil (tests); mutations then persist without a live reload.
	dispatcher *alerting.Dispatcher
}

// NewAlertChannelsHandler creates an alert-channels handler. dispatcher may be
// nil (e.g. tests).
func NewAlertChannelsHandler(configStore configstore.ConfigStore, dispatcher *alerting.Dispatcher) (*AlertChannelsHandler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &AlertChannelsHandler{configStore: configStore, dispatcher: dispatcher}, nil
}

// RegisterRoutes wires the read endpoints.
func (h *AlertChannelsHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/alerting/channels", lib.ChainMiddlewares(h.listChannels, middlewares...))
	r.GET("/api/alerting/channels/{id}", lib.ChainMiddlewares(h.getChannel, middlewares...))
}

// RegisterMutatingRoutes wires the endpoints that change state, registered by
// the server with the RBAC-gated mutating middleware chain. The test-fire
// endpoint is mutating: it sends to an external service and stamps the
// channel's last-delivery status.
func (h *AlertChannelsHandler) RegisterMutatingRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.POST("/api/alerting/channels", lib.ChainMiddlewares(h.createChannel, middlewares...))
	r.PUT("/api/alerting/channels/{id}", lib.ChainMiddlewares(h.updateChannel, middlewares...))
	r.DELETE("/api/alerting/channels/{id}", lib.ChainMiddlewares(h.deleteChannel, middlewares...))
	r.POST("/api/alerting/channels/{id}/test", lib.ChainMiddlewares(h.testChannel, middlewares...))
}

// ---- request/response payloads ----

// alertChannelInput is the create/update payload. Pointer fields distinguish
// "leave unchanged" from explicit zero values on update; on create, nil
// pointers take defaults. A nil Secret on update preserves the stored secret.
type alertChannelInput struct {
	Name        *string   `json:"name,omitempty"`
	Type        *string   `json:"type,omitempty"`
	Enabled     *bool     `json:"enabled,omitempty"`
	EndpointURL *string   `json:"endpoint_url,omitempty"`
	Secret      *string   `json:"secret,omitempty"`
	EventTypes  *[]string `json:"event_types,omitempty"`
}

// alertChannelResponse wraps a channel with the has_secret flag. The secret
// itself is excluded by the table's json:"-" tag; this wrapper only signals
// its presence so the UI can render "configured" without ever seeing it.
type alertChannelResponse struct {
	*configstoreTables.TableAlertChannel
	HasSecret bool `json:"has_secret"`
}

func toAlertChannelResponse(channel *configstoreTables.TableAlertChannel) alertChannelResponse {
	return alertChannelResponse{TableAlertChannel: channel, HasSecret: channel.Secret != ""}
}

// validateAlertChannel returns a client-facing message for invalid channels,
// or "" when acceptable.
func validateAlertChannel(channel *configstoreTables.TableAlertChannel) string {
	if strings.TrimSpace(channel.Name) == "" {
		return "name is required"
	}
	switch channel.Type {
	case configstoreTables.AlertChannelTypeSlack, configstoreTables.AlertChannelTypeWebhook:
		if err := alerting.ValidateEndpointURL(channel.EndpointURL); err != nil {
			return fmt.Sprintf("invalid endpoint_url: %v", err)
		}
	case configstoreTables.AlertChannelTypePagerDuty:
		// EndpointURL is an optional Events API override for PagerDuty.
		if channel.EndpointURL != "" {
			if err := alerting.ValidateEndpointURL(channel.EndpointURL); err != nil {
				return fmt.Sprintf("invalid endpoint_url: %v", err)
			}
		}
		if channel.Secret == "" {
			return "secret (the PagerDuty routing key) is required for pagerduty channels"
		}
	default:
		return fmt.Sprintf("type must be %q, %q or %q",
			configstoreTables.AlertChannelTypeSlack, configstoreTables.AlertChannelTypePagerDuty, configstoreTables.AlertChannelTypeWebhook)
	}
	for _, et := range channel.EventTypes {
		if !alertEventTypeSet[et] {
			return fmt.Sprintf("unknown event type %q", et)
		}
	}
	return ""
}

// reload refreshes the live dispatcher after a mutation. Nil-safe.
func (h *AlertChannelsHandler) reload(ctx *fasthttp.RequestCtx) {
	if h.dispatcher == nil {
		return
	}
	if err := h.dispatcher.Reload(ctx); err != nil {
		logger.Warn("failed to reload alert channels into dispatcher: %v", err)
	}
}

// ---- handlers ----

func (h *AlertChannelsHandler) listChannels(ctx *fasthttp.RequestCtx) {
	channels, err := h.configStore.GetAlertChannels(ctx)
	if err != nil {
		logger.Error("failed to retrieve alert channels: %v", err)
		SendError(ctx, 500, "Failed to retrieve alert channels")
		return
	}
	out := make([]alertChannelResponse, len(channels))
	for i := range channels {
		out[i] = toAlertChannelResponse(&channels[i])
	}
	SendJSON(ctx, map[string]any{
		"channels": out,
		"count":    len(out),
	})
}

func (h *AlertChannelsHandler) getChannel(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	channel, err := h.configStore.GetAlertChannel(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Alert channel not found")
			return
		}
		logger.Error("failed to retrieve alert channel %s: %v", id, err)
		SendError(ctx, 500, "Failed to retrieve alert channel")
		return
	}
	SendJSON(ctx, map[string]any{"channel": toAlertChannelResponse(channel)})
}

func (h *AlertChannelsHandler) createChannel(ctx *fasthttp.RequestCtx) {
	var req alertChannelInput
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}

	channel := &configstoreTables.TableAlertChannel{
		ID:        uuid.NewString(),
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if req.Name != nil {
		channel.Name = strings.TrimSpace(*req.Name)
	}
	if req.Type != nil {
		channel.Type = strings.TrimSpace(*req.Type)
	}
	if req.Enabled != nil {
		channel.Enabled = *req.Enabled
	}
	if req.EndpointURL != nil {
		channel.EndpointURL = strings.TrimSpace(*req.EndpointURL)
	}
	if req.Secret != nil {
		channel.Secret = *req.Secret
	}
	if req.EventTypes != nil {
		channel.EventTypes = *req.EventTypes
	}

	if msg := validateAlertChannel(channel); msg != "" {
		SendError(ctx, 400, msg)
		return
	}
	if err := h.configStore.CreateAlertChannel(ctx, channel); err != nil {
		logger.Error("failed to create alert channel: %v", err)
		SendError(ctx, 500, "Failed to create alert channel")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionAlertChannelCreate, configstoreTables.AuditOutcomeSuccess, channel.ID)
	h.reload(ctx)
	SendJSON(ctx, map[string]any{
		"message": "Alert channel created successfully",
		"channel": toAlertChannelResponse(channel),
	})
}

func (h *AlertChannelsHandler) updateChannel(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	channel, err := h.configStore.GetAlertChannel(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Alert channel not found")
			return
		}
		logger.Error("failed to retrieve alert channel %s: %v", id, err)
		SendError(ctx, 500, "Failed to retrieve alert channel")
		return
	}

	var req alertChannelInput
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if req.Name != nil {
		channel.Name = strings.TrimSpace(*req.Name)
	}
	if req.Type != nil {
		channel.Type = strings.TrimSpace(*req.Type)
	}
	if req.Enabled != nil {
		channel.Enabled = *req.Enabled
	}
	if req.EndpointURL != nil {
		channel.EndpointURL = strings.TrimSpace(*req.EndpointURL)
	}
	if req.Secret != nil {
		// Explicit secret (including "") replaces; omitted preserves.
		channel.Secret = *req.Secret
	}
	if req.EventTypes != nil {
		channel.EventTypes = *req.EventTypes
	}
	channel.UpdatedAt = time.Now()

	if msg := validateAlertChannel(channel); msg != "" {
		SendError(ctx, 400, msg)
		return
	}
	if err := h.configStore.UpdateAlertChannel(ctx, channel); err != nil {
		logger.Error("failed to update alert channel %s: %v", id, err)
		SendError(ctx, 500, "Failed to update alert channel")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionAlertChannelUpdate, configstoreTables.AuditOutcomeSuccess, channel.ID)
	h.reload(ctx)
	SendJSON(ctx, map[string]any{
		"message": "Alert channel updated successfully",
		"channel": toAlertChannelResponse(channel),
	})
}

func (h *AlertChannelsHandler) deleteChannel(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if err := h.configStore.DeleteAlertChannel(ctx, id); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Alert channel not found")
			return
		}
		logger.Error("failed to delete alert channel %s: %v", id, err)
		SendError(ctx, 500, "Failed to delete alert channel")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionAlertChannelDelete, configstoreTables.AuditOutcomeSuccess, id)
	h.reload(ctx)
	SendJSON(ctx, map[string]any{"message": "Alert channel deleted successfully"})
}

// testChannel fires one synchronous test alert (single attempt, sender
// timeout) and reports the outcome, also stamping the channel's last-delivery
// columns so the UI reflects the attempt.
func (h *AlertChannelsHandler) testChannel(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	channel, err := h.configStore.GetAlertChannel(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, 404, "Alert channel not found")
			return
		}
		logger.Error("failed to retrieve alert channel %s: %v", id, err)
		SendError(ctx, 500, "Failed to retrieve alert channel")
		return
	}

	event := alerting.Event{
		Type:      alerting.EventTypeAuditMutation,
		Severity:  alerting.SeverityInfo,
		Title:     "Test alert from Loopback Gateway",
		Message:   fmt.Sprintf("Test fire for alert channel %q requested by %s", channel.Name, auditActor(ctx)),
		Fields:    map[string]string{"channel_id": channel.ID, "channel_type": channel.Type},
		Timestamp: time.Now(),
	}
	attemptAt := time.Now()
	httpStatus, sendErr := alerting.SendTest(channel, event)

	status := "ok"
	errMsg := ""
	if sendErr != nil {
		status = "failed"
		errMsg = sendErr.Error()
	}
	if err := h.configStore.UpdateAlertChannelDeliveryStatus(ctx, channel.ID, attemptAt, status, errMsg); err != nil {
		logger.Warn("failed to record test-fire status for alert channel %s: %v", channel.ID, err)
	}
	recordAudit(ctx, h.configStore, AuditActionAlertChannelTest, configstoreTables.AuditOutcomeSuccess, channel.ID)

	SendJSON(ctx, map[string]any{
		"status":      status,
		"http_status": httpStatus,
		"error":       errMsg,
	})
}
