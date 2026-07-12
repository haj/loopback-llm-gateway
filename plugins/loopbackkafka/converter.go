package loopbackkafka

import (
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
)

// TelemetryEvent is the JSON document published to Kafka for one completed
// request. It is a self-contained snapshot — every field is copied out of the
// *Trace before publishing, so nothing references the pooled trace after Inject
// returns (per the ObservabilityPlugin retention contract).
type TelemetryEvent struct {
	Schema     string         `json:"schema"`      // event schema version
	Source     string         `json:"source"`      // "loopback-gateway"
	RequestID  string         `json:"request_id"`
	TraceID    string         `json:"trace_id"`
	SessionID  string         `json:"session_id,omitempty"`
	StartTime  time.Time      `json:"start_time"`
	EndTime    time.Time      `json:"end_time"`
	DurationMs float64        `json:"duration_ms"`
	Provider   string         `json:"provider,omitempty"`
	Model      string         `json:"model,omitempty"`
	Status     string         `json:"status"` // "ok" or "error"
	StatusMsg  string         `json:"status_msg,omitempty"`
	Usage      *UsageSummary  `json:"usage,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"` // trace-level attributes (non-content)
	Spans      []SpanSummary  `json:"spans,omitempty"`
}

// UsageSummary holds the rolled-up token/cost totals for the request.
type UsageSummary struct {
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	Cost         float64 `json:"cost,omitempty"`
}

// SpanSummary is the per-span projection included in the event.
type SpanSummary struct {
	SpanID     string         `json:"span_id"`
	ParentID   string         `json:"parent_id,omitempty"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	StartTime  time.Time      `json:"start_time"`
	EndTime    time.Time      `json:"end_time"`
	DurationMs float64        `json:"duration_ms"`
	Status     string         `json:"status"`
	StatusMsg  string         `json:"status_msg,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// EventSchemaVersion identifies the wire format of the published events so
// downstream consumers can evolve safely.
const EventSchemaVersion = "loopback.kafka.telemetry.v1"

// EventSource is the source identifier stamped on every event.
const EventSource = "loopback-gateway"

// convertTraceToEvent projects a completed trace into a JSON telemetry event and
// returns the encoded payload plus the partition key (request ID, falling back to
// trace ID). When disableContentLogging is true, message/input/output and tool
// content attributes are dropped, keeping only metadata. The span filter selects
// which plugin spans are included.
func convertTraceToEvent(trace *schemas.Trace, disableContentLogging bool, filter *schemas.PluginSpanFilter) ([]byte, []byte, error) {
	evt := TelemetryEvent{
		Schema:    EventSchemaVersion,
		Source:    EventSource,
		RequestID: trace.RequestID,
		TraceID:   trace.TraceID,
		StartTime: trace.StartTime,
		EndTime:   trace.EndTime,
	}
	if !trace.EndTime.IsZero() && !trace.StartTime.IsZero() {
		evt.DurationMs = float64(trace.EndTime.Sub(trace.StartTime).Microseconds()) / 1000.0
	}

	if sid := stringAttr(trace.Attributes, schemas.TraceAttrSessionID); sid != "" {
		evt.SessionID = sid
	}
	evt.Attributes = copyNonContentAttrs(trace.Attributes, disableContentLogging)

	// Project spans (filtered), and roll up provider/model/usage/status from the
	// final LLM-call/retry attempt span — mirroring how the OTEL connector derives
	// its trace-level metrics.
	var finalSpan *schemas.Span
	for _, span := range trace.Spans {
		if span == nil {
			continue
		}
		if !filter.ShouldExportSpan(span) {
			continue
		}
		evt.Spans = append(evt.Spans, summarizeSpan(span, disableContentLogging))
		if span.Kind == schemas.SpanKindLLMCall || span.Kind == schemas.SpanKindRetry {
			if finalSpan == nil || span.EndTime.After(finalSpan.EndTime) {
				finalSpan = span
			}
		}
	}
	if finalSpan == nil {
		finalSpan = trace.RootSpan
	}

	evt.Status = "ok"
	if finalSpan != nil {
		evt.Provider = stringAttr(finalSpan.Attributes, schemas.AttrProviderName)
		evt.Model = stringAttr(finalSpan.Attributes, schemas.AttrRequestModel)
		if finalSpan.Status == schemas.SpanStatusError {
			evt.Status = "error"
			evt.StatusMsg = finalSpan.StatusMsg
		}
		evt.Usage = summarizeUsage(finalSpan.Attributes)
	}

	payload, err := sonic.Marshal(&evt)
	if err != nil {
		return nil, nil, err
	}
	key := evt.RequestID
	if key == "" {
		key = evt.TraceID
	}
	return payload, []byte(key), nil
}

// summarizeSpan projects a single span, optionally dropping content attributes.
func summarizeSpan(span *schemas.Span, disableContentLogging bool) SpanSummary {
	s := SpanSummary{
		SpanID:     span.SpanID,
		ParentID:   span.ParentID,
		Name:       span.Name,
		Kind:       string(span.Kind),
		StartTime:  span.StartTime,
		EndTime:    span.EndTime,
		Status:     spanStatusString(span.Status),
		StatusMsg:  span.StatusMsg,
		Attributes: copyNonContentAttrs(span.Attributes, disableContentLogging),
	}
	if !span.EndTime.IsZero() && !span.StartTime.IsZero() {
		s.DurationMs = float64(span.EndTime.Sub(span.StartTime).Microseconds()) / 1000.0
	}
	return s
}

// summarizeUsage extracts token/cost totals from a span's attributes, preferring
// the new attribute names and falling back to the deprecated ones.
func summarizeUsage(attrs map[string]any) *UsageSummary {
	if attrs == nil {
		return nil
	}
	in := intAttr(attrs, schemas.AttrInputTokens)
	if in == 0 {
		in = intAttr(attrs, schemas.AttrPromptTokens)
	}
	out := intAttr(attrs, schemas.AttrOutputTokens)
	if out == 0 {
		out = intAttr(attrs, schemas.AttrCompletionTokens)
	}
	cost := floatAttr(attrs, schemas.AttrUsageCost)
	if in == 0 && out == 0 && cost == 0 {
		return nil
	}
	return &UsageSummary{InputTokens: in, OutputTokens: out, Cost: cost}
}

// copyNonContentAttrs returns a shallow copy of attrs, dropping content-bearing
// keys when content logging is disabled. Returns nil for an empty result so the
// field is omitted from JSON.
func copyNonContentAttrs(attrs map[string]any, disableContentLogging bool) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		if disableContentLogging && isContentAttribute(k) {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isContentAttribute reports whether an attribute key carries message/input/output
// content or tool definitions/arguments/results, which are dropped when content
// logging is disabled.
func isContentAttribute(key string) bool {
	switch key {
	case schemas.AttrInputMessages, schemas.AttrOutputMessages,
		schemas.AttrInputText, schemas.AttrInputSpeech, schemas.AttrInputEmbedding,
		schemas.AttrTools, schemas.AttrRespTools,
		schemas.AttrToolName, schemas.AttrToolCallID,
		schemas.AttrToolCallArguments, schemas.AttrToolCallResult, schemas.AttrToolType,
		schemas.AttrToolChoiceType, schemas.AttrToolChoiceName,
		schemas.AttrRespToolChoiceType, schemas.AttrRespToolChoiceName:
		return true
	default:
		return false
	}
}

func spanStatusString(s schemas.SpanStatus) string {
	switch s {
	case schemas.SpanStatusOk:
		return "ok"
	case schemas.SpanStatusError:
		return "error"
	default:
		return "unset"
	}
}

func stringAttr(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	if v, ok := attrs[key].(string); ok {
		return v
	}
	return ""
}

func intAttr(attrs map[string]any, key string) int {
	if attrs == nil {
		return 0
	}
	switch v := attrs[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func floatAttr(attrs map[string]any, key string) float64 {
	if attrs == nil {
		return 0
	}
	switch v := attrs[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
