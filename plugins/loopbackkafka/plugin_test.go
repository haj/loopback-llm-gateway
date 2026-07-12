package loopbackkafka

import (
	"context"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	kafka "github.com/segmentio/kafka-go"
)

func TestInitValidation(t *testing.T) {
	if _, err := Init(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error for nil config")
	}
	if _, err := Init(context.Background(), &Config{Topic: "t"}, nil); err == nil {
		t.Fatal("expected error for missing brokers")
	}
	if _, err := Init(context.Background(), &Config{Brokers: []string{"localhost:9092"}}, nil); err == nil {
		t.Fatal("expected error for missing topic")
	}
	bad := &Config{Brokers: []string{"localhost:9092"}, Topic: "t", PluginSpanFilter: &schemas.PluginSpanFilter{Mode: "nonsense"}}
	if _, err := Init(context.Background(), bad, nil); err == nil {
		t.Fatal("expected error for invalid span filter mode")
	}
}

func TestInitSucceedsAndCleanup(t *testing.T) {
	p, err := Init(context.Background(), &Config{Brokers: []string{"localhost:9092"}, Topic: "telemetry"}, nil)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if p.GetName() != PluginName {
		t.Fatalf("unexpected name %q", p.GetName())
	}
	if err := p.Cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestInjectEnqueuesEvent(t *testing.T) {
	// Build a plugin with a fake writer so Inject delivery is observable offline.
	w := &fakeWriter{}
	p := &Plugin{
		topic:    "telemetry",
		producer: newProducer(w, producerConfig{batchSize: 1, batchTimeoutMs: 20, queueSize: 10, maxRetries: 1, retryBackoffMs: 1}),
	}
	defer p.Cleanup()

	if err := p.Inject(context.Background(), sampleTrace()); err != nil {
		t.Fatalf("inject: %v", err)
	}
	waitFor(t, func() bool { _, msgs, _ := w.totals(); return msgs == 1 })

	w.mu.Lock()
	msg := w.batches[0][0]
	w.mu.Unlock()
	if string(msg.Key) != "req-123" {
		t.Fatalf("expected key req-123, got %q", string(msg.Key))
	}
	var evt TelemetryEvent
	if err := sonic.Unmarshal(msg.Value, &evt); err != nil {
		t.Fatalf("payload not valid json: %v", err)
	}
	if evt.Provider != "openai" {
		t.Fatalf("event missing provider: %+v", evt)
	}
}

func TestInjectNilTraceIsNoop(t *testing.T) {
	w := &fakeWriter{}
	p := &Plugin{producer: newProducer(w, baseCfg())}
	defer p.Cleanup()
	if err := p.Inject(context.Background(), nil); err != nil {
		t.Fatalf("inject nil: %v", err)
	}
}

func TestRedactConfigMasksPassword(t *testing.T) {
	p := &Plugin{}
	raw := map[string]any{
		"brokers":       []any{"localhost:9092"},
		"topic":         "telemetry",
		"sasl_username": "user",
		"sasl_password": map[string]any{"value": "supersecret"},
	}
	out, err := p.RedactConfig(raw)
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	b, _ := sonic.Marshal(out)
	if containsSecret(string(b), "supersecret") {
		t.Fatalf("password leaked in redacted config: %s", string(b))
	}
}

func TestMarshalConfigForStorageFlattensSecret(t *testing.T) {
	p := &Plugin{}
	raw := map[string]any{
		"brokers":       []any{"localhost:9092"},
		"topic":         "telemetry",
		"sasl_password": "env.KAFKA_PASS",
	}
	out, err := p.MarshalConfigForStorage(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Stored form flattens the SecretVar to the "env.VAR" string convention.
	if s, ok := out["sasl_password"].(string); !ok || s != "env.KAFKA_PASS" {
		t.Fatalf("expected flattened env reference, got %#v", out["sasl_password"])
	}
}

// ensure kafka.Message type stays imported for the test helpers above.
var _ = kafka.Message{}

func containsSecret(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
