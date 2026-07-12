package loopbackkafka

import (
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
)

func sampleTrace() *schemas.Trace {
	start := time.Now().Add(-2 * time.Second)
	end := time.Now()
	root := &schemas.Span{
		SpanID:    "root1",
		Name:      "http.request",
		Kind:      schemas.SpanKindHTTPRequest,
		StartTime: start,
		EndTime:   end,
		Status:    schemas.SpanStatusOk,
	}
	llm := &schemas.Span{
		SpanID:    "llm1",
		ParentID:  "root1",
		Name:      "llm.call",
		Kind:      schemas.SpanKindLLMCall,
		StartTime: start.Add(10 * time.Millisecond),
		EndTime:   end.Add(-10 * time.Millisecond),
		Status:    schemas.SpanStatusOk,
		Attributes: map[string]any{
			schemas.AttrProviderName:  "openai",
			schemas.AttrRequestModel:  "gpt-4o",
			schemas.AttrInputTokens:   100,
			schemas.AttrOutputTokens:  50,
			schemas.AttrUsageCost:     0.0021,
			schemas.AttrInputMessages: "secret user prompt",
		},
	}
	return &schemas.Trace{
		RequestID: "req-123",
		TraceID:   "trace-abc",
		RootSpan:  root,
		Spans:     []*schemas.Span{root, llm},
		StartTime: start,
		EndTime:   end,
		Attributes: map[string]any{
			schemas.TraceAttrSessionID: "sess-9",
		},
	}
}

func decode(t *testing.T, payload []byte) TelemetryEvent {
	t.Helper()
	var evt TelemetryEvent
	if err := sonic.Unmarshal(payload, &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	return evt
}

func TestConvertTraceToEvent(t *testing.T) {
	payload, key, err := convertTraceToEvent(sampleTrace(), false, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if string(key) != "req-123" {
		t.Fatalf("expected key req-123, got %q", string(key))
	}
	evt := decode(t, payload)
	if evt.Schema != EventSchemaVersion || evt.Source != EventSource {
		t.Fatalf("unexpected schema/source: %+v", evt)
	}
	if evt.RequestID != "req-123" || evt.TraceID != "trace-abc" {
		t.Fatalf("bad ids: %+v", evt)
	}
	if evt.SessionID != "sess-9" {
		t.Fatalf("expected session id from trace attr, got %q", evt.SessionID)
	}
	if evt.Provider != "openai" || evt.Model != "gpt-4o" {
		t.Fatalf("provider/model not rolled up: %+v", evt)
	}
	if evt.Status != "ok" {
		t.Fatalf("expected ok status, got %q", evt.Status)
	}
	if evt.Usage == nil || evt.Usage.InputTokens != 100 || evt.Usage.OutputTokens != 50 || evt.Usage.Cost != 0.0021 {
		t.Fatalf("usage not summarized: %+v", evt.Usage)
	}
	if len(evt.Spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(evt.Spans))
	}
	if evt.DurationMs <= 0 {
		t.Fatalf("expected positive duration, got %f", evt.DurationMs)
	}
}

func TestConvertDisableContentLogging(t *testing.T) {
	payload, _, err := convertTraceToEvent(sampleTrace(), true, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	evt := decode(t, payload)
	// Metadata is preserved...
	if evt.Provider != "openai" || evt.Usage == nil || evt.Usage.InputTokens != 100 {
		t.Fatalf("metadata dropped unexpectedly: %+v", evt)
	}
	// ...but the input-messages content attribute is stripped from every span.
	for _, s := range evt.Spans {
		if _, ok := s.Attributes[schemas.AttrInputMessages]; ok {
			t.Fatalf("content attribute leaked with content logging disabled: %+v", s.Attributes)
		}
	}
}

func TestConvertErrorStatus(t *testing.T) {
	tr := sampleTrace()
	tr.Spans[1].Status = schemas.SpanStatusError
	tr.Spans[1].StatusMsg = "boom"
	payload, _, err := convertTraceToEvent(tr, false, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	evt := decode(t, payload)
	if evt.Status != "error" || evt.StatusMsg != "boom" {
		t.Fatalf("expected error status, got %q / %q", evt.Status, evt.StatusMsg)
	}
}

func TestConvertKeyFallsBackToTraceID(t *testing.T) {
	tr := sampleTrace()
	tr.RequestID = ""
	_, key, err := convertTraceToEvent(tr, false, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if string(key) != "trace-abc" {
		t.Fatalf("expected fallback key trace-abc, got %q", string(key))
	}
}
