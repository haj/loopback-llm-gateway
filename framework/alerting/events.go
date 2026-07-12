// Package alerting delivers operational alert events (audit mutations,
// governance threshold violations, circuit-breaker state changes) to
// admin-configured channels (Slack, PagerDuty, generic webhook).
//
// The package is ADDITIVE and DEFAULT-OFF: with zero configured channels,
// Publish is a cheap atomic check and the request path is byte-for-byte
// unchanged. Delivery is fully asynchronous — producers never block on channel
// I/O.
package alerting

import "time"

// Event types dispatched through the alerting pipeline. Channels can filter to
// a subset; an empty filter receives all types.
const (
	EventTypeAuditMutation        = "audit.mutation"
	EventTypeBudgetExceeded       = "budget.exceeded"
	EventTypeRateLimitExceeded    = "rate_limit.exceeded"
	EventTypeCircuitBreakerOpen   = "circuit_breaker.open"
	EventTypeCircuitBreakerClosed = "circuit_breaker.closed"
)

// Event severities. Mapped to PagerDuty Events v2 severities; carried verbatim
// in Slack/webhook payloads.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Event is one alert-worthy occurrence. DedupKey identifies the logical
// condition (e.g. "budget.exceeded|vk:<id>") so the dispatcher can suppress
// repeats within its dedup window — governance violations fire on every
// rejected request, so storms are the norm, not the exception.
type Event struct {
	// Type is one of the EventType constants.
	Type string `json:"type"`
	// Severity is one of the Severity constants.
	Severity string `json:"severity"`
	// Title is the short human-readable headline.
	Title string `json:"title"`
	// Message is the longer description.
	Message string `json:"message"`
	// Provider is the affected model provider, when applicable.
	Provider string `json:"provider,omitempty"`
	// DedupKey identifies the logical condition for suppression. Empty means
	// never suppressed.
	DedupKey string `json:"dedup_key,omitempty"`
	// Fields carries small structured details (virtual key ID, decision
	// reason, breaker state). Never put secrets here — fields are serialized
	// to external services verbatim.
	Fields map[string]string `json:"fields,omitempty"`
	// Timestamp is when the condition occurred.
	Timestamp time.Time `json:"timestamp"`
}

// Publisher is the narrow produce-side surface handed to event sources (the
// audit recorder, the governance plugin bridge, the circuit-breaker bridge).
// Publish must be non-blocking.
type Publisher interface {
	Publish(event Event)
}
