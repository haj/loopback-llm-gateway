// Package loopbackkafka is the Loopback Gateway's Kafka data connector: an
// observability plugin that publishes completed request/response telemetry to a
// configured Kafka topic.
//
// It implements Bifrost's real plugin contract — schemas.ObservabilityPlugin
// (BasePlugin + Inject) so it receives completed traces asynchronously from the
// tracing middleware, plus the no-op LLMPlugin / HTTPTransportPlugin hooks
// required for plugin indexing, and schemas.ConfigMarshallerPlugin for
// persistence/redaction of secrets. No invented APIs.
//
// Architecture (mirrors plugins/otel):
//
//	Init (client lifecycle) -> Inject -> convertTraceToEvent (event conversion)
//	  -> producer (transport: batching + backpressure + retries) -> Kafka topic
//
// This is the reusable TEMPLATE for the other Wave-3 data connectors. The
// BigQuery, Datadog, and PubSub connectors are intentionally DEFERRED; each will
// clone this same Init -> convert -> producer pipeline, swapping only the
// transport client and the event encoding.
//
// Clean-room Apache-2.0: new code only. User-facing strings say "Loopback
// Gateway", not "Bifrost".
package loopbackkafka

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	kafka "github.com/segmentio/kafka-go"
)

// PluginName is the stable identifier returned by GetName() and used to register
// the connector as a built-in plugin.
const PluginName = "loopback-kafka"

// logger is the package logger, set in Init.
var logger schemas.Logger

// Default tuning for the async producer. These keep the connector safe to enable
// with only brokers+topic configured: a bounded in-memory queue (backpressure),
// time/size-bounded batches, and a small bounded retry budget.
const (
	defaultBatchSize      = 100
	defaultBatchTimeoutMs = 1000
	defaultQueueSize      = 10000
	defaultMaxRetries     = 3
	defaultRetryBackoffMs = 200
	defaultRequiredAcks   = "one"
)

// Config is the persistence-friendly configuration for the Kafka connector.
//
// The UI config form drives the first three fields (brokers, topic, and the
// PluginConfig.Enabled flag that wraps this object). The remaining fields tune
// the transport and have safe defaults, so a minimal {"brokers":[...],"topic":...}
// config is fully functional.
type Config struct {
	// Brokers is the list of "host:port" Kafka bootstrap brokers. Required.
	Brokers []string `json:"brokers"`
	// Topic is the destination topic for telemetry events. Required.
	Topic string `json:"topic"`

	// Batching / backpressure / retry tuning (transport layer).
	BatchSize      int `json:"batch_size,omitempty"`       // messages per batch (default 100)
	BatchTimeoutMs int `json:"batch_timeout_ms,omitempty"` // max time before a partial batch flushes (default 1000)
	QueueSize      int `json:"queue_size,omitempty"`       // bounded in-memory queue; full queue drops (default 10000)
	MaxRetries     int `json:"max_retries,omitempty"`      // per-batch write retries (default 3)
	RetryBackoffMs int `json:"retry_backoff_ms,omitempty"` // base backoff between retries (default 200)

	// RequiredAcks controls durability: "none", "one" (default), or "all".
	RequiredAcks string `json:"required_acks,omitempty"`

	// Optional SASL/PLAIN authentication. Username is not a secret; Password is a
	// SecretVar so it is redacted in API responses and stored as "env.VAR" or a
	// literal in the config store.
	SASLUsername string             `json:"sasl_username,omitempty"`
	SASLPassword *schemas.SecretVar `json:"sasl_password,omitempty"`

	// DisableContentLogging drops message/input/output content from emitted events,
	// keeping only metadata (provider, model, tokens, latency, status).
	DisableContentLogging bool `json:"disable_content_logging,omitempty"`

	// PluginSpanFilter selects which plugin spans are included in the per-trace
	// span summary. Shared across all observability connectors.
	PluginSpanFilter *schemas.PluginSpanFilter `json:"plugin_span_filter,omitempty"`
}

// configForStorage is the persisted form: *SecretVar fields flattened to plain
// strings ("env.VAR" or the literal value) for DB/config-file persistence.
type configForStorage struct {
	Brokers               []string                  `json:"brokers"`
	Topic                 string                    `json:"topic"`
	BatchSize             int                       `json:"batch_size,omitempty"`
	BatchTimeoutMs        int                       `json:"batch_timeout_ms,omitempty"`
	QueueSize             int                       `json:"queue_size,omitempty"`
	MaxRetries            int                       `json:"max_retries,omitempty"`
	RetryBackoffMs        int                       `json:"retry_backoff_ms,omitempty"`
	RequiredAcks          string                    `json:"required_acks,omitempty"`
	SASLUsername          string                    `json:"sasl_username,omitempty"`
	SASLPassword          string                    `json:"sasl_password,omitempty"`
	DisableContentLogging bool                      `json:"disable_content_logging,omitempty"`
	PluginSpanFilter      *schemas.PluginSpanFilter `json:"plugin_span_filter,omitempty"`
}

// MarshalForStorage serializes Config to JSON with SASLPassword flattened to a
// plain string for persistence. For HTTP API responses use json.Marshal directly
// so clients receive the full SecretVar object.
func (c *Config) MarshalForStorage() ([]byte, error) {
	out := configForStorage{
		Brokers:               c.Brokers,
		Topic:                 c.Topic,
		BatchSize:             c.BatchSize,
		BatchTimeoutMs:        c.BatchTimeoutMs,
		QueueSize:             c.QueueSize,
		MaxRetries:            c.MaxRetries,
		RetryBackoffMs:        c.RetryBackoffMs,
		RequiredAcks:          c.RequiredAcks,
		SASLUsername:          c.SASLUsername,
		SASLPassword:          schemas.SecretVarAsString(c.SASLPassword),
		DisableContentLogging: c.DisableContentLogging,
		PluginSpanFilter:      c.PluginSpanFilter,
	}
	return sonic.Marshal(out)
}

// Redacted returns a copy of the config safe for API responses: the SASL password
// literal is masked while "env." references are preserved. Brokers/topic are not
// secrets and pass through unchanged.
func (c *Config) Redacted() *Config {
	if c == nil {
		return nil
	}
	rc := *c
	if c.SASLPassword != nil {
		rc.SASLPassword = c.SASLPassword.Redacted()
	}
	return &rc
}

// Plugin is the Kafka observability connector. A completed trace is converted to
// a JSON telemetry event and handed to the async producer for batched, retried
// delivery to the configured topic.
type Plugin struct {
	topic                 string
	producer              *producer
	disableContentLogging bool
	pluginSpanFilter      *schemas.PluginSpanFilter
}

// Compile-time assertions that we satisfy the real Bifrost plugin interfaces.
var (
	_ schemas.ObservabilityPlugin   = (*Plugin)(nil)
	_ schemas.LLMPlugin             = (*Plugin)(nil)
	_ schemas.HTTPTransportPlugin   = (*Plugin)(nil)
	_ schemas.ConfigMarshallerPlugin = (*Plugin)(nil)
)

// Init is the config-driven, built-in entrypoint used by the Loopback Gateway
// server to register the Kafka connector. It validates config, opens the Kafka
// writer, and starts the async producer goroutine.
func Init(ctx context.Context, config *Config, _logger schemas.Logger) (*Plugin, error) {
	if _logger != nil {
		logger = _logger
	}
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}
	if len(config.Brokers) == 0 {
		return nil, fmt.Errorf("at least one kafka broker is required")
	}
	if config.Topic == "" {
		return nil, fmt.Errorf("kafka topic is required")
	}
	if err := config.PluginSpanFilter.Validate(); err != nil {
		return nil, err
	}

	pcfg := producerConfig{
		brokers:        config.Brokers,
		topic:          config.Topic,
		batchSize:      orDefault(config.BatchSize, defaultBatchSize),
		batchTimeoutMs: orDefault(config.BatchTimeoutMs, defaultBatchTimeoutMs),
		queueSize:      orDefault(config.QueueSize, defaultQueueSize),
		maxRetries:     orDefault(config.MaxRetries, defaultMaxRetries),
		retryBackoffMs: orDefault(config.RetryBackoffMs, defaultRetryBackoffMs),
		requiredAcks:   requiredAcksOrDefault(config.RequiredAcks),
		saslUsername:   config.SASLUsername,
		saslPassword:   config.SASLPassword.GetValue(),
	}

	writer, err := newKafkaWriter(pcfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka writer: %w", err)
	}

	p := &Plugin{
		topic:                 config.Topic,
		producer:              newProducer(writer, pcfg),
		disableContentLogging: config.DisableContentLogging,
		pluginSpanFilter:      config.PluginSpanFilter,
	}
	if logger != nil {
		logger.Info("[loopback-kafka] connector initialized: topic=%s brokers=%v", config.Topic, config.Brokers)
	}
	return p, nil
}

// GetName returns the plugin name. (BasePlugin)
func (p *Plugin) GetName() string { return PluginName }

// Cleanup flushes and closes the producer on shutdown. (BasePlugin)
func (p *Plugin) Cleanup() error {
	if p.producer != nil {
		return p.producer.close()
	}
	return nil
}

// Inject receives a completed trace, converts it to a telemetry event, and
// enqueues it for asynchronous, batched delivery to Kafka. It never blocks the
// caller: a full queue drops the event (counted) rather than stalling the
// request-completion path. (ObservabilityPlugin)
//
// Per the interface contract, the *Trace must not be retained after Inject
// returns: convertTraceToEvent copies everything needed into an owned []byte.
func (p *Plugin) Inject(_ context.Context, trace *schemas.Trace) error {
	if trace == nil || p.producer == nil {
		return nil
	}
	payload, key, err := convertTraceToEvent(trace, p.disableContentLogging, p.pluginSpanFilter)
	if err != nil {
		if logger != nil {
			logger.Warn("[loopback-kafka] failed to convert trace %s: %v", trace.TraceID, err)
		}
		return nil // observability is best-effort; never fail the request path
	}
	p.producer.enqueue(kafka.Message{Key: key, Value: payload})
	return nil
}

// --- ConfigMarshallerPlugin ---

// MarshalConfigForStorage implements schemas.ConfigMarshallerPlugin: it converts
// the raw API config map into the canonical DB-storage form (SecretVar flattened).
func (p *Plugin) MarshalConfigForStorage(raw map[string]any) (map[string]any, error) {
	c, err := configFromMap(raw)
	if err != nil {
		return raw, err
	}
	normalized, err := c.MarshalForStorage()
	if err != nil {
		return raw, err
	}
	var out map[string]any
	if err := sonic.Unmarshal(normalized, &out); err != nil {
		return raw, err
	}
	return out, nil
}

// RedactConfig implements schemas.ConfigMarshallerPlugin: it masks secret values
// (SASL password) for API responses.
func (p *Plugin) RedactConfig(raw map[string]any) (map[string]any, error) {
	c, err := configFromMap(raw)
	if err != nil {
		return nil, err
	}
	b, err := sonic.Marshal(c.Redacted())
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := sonic.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// --- LLMPlugin no-op hooks (required for plugin indexing) ---

// PreRequestHook is a no-op; telemetry is delivered via Inject.
func (p *Plugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}

// PreLLMHook passes the request through unchanged.
func (p *Plugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}

// PostLLMHook passes the response through unchanged.
func (p *Plugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, bifrostErr, nil
}

// --- HTTPTransportPlugin no-op hooks ---

// HTTPTransportPreHook is not used for this connector.
func (p *Plugin) HTTPTransportPreHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	return nil, nil
}

// HTTPTransportPostHook is not used for this connector.
func (p *Plugin) HTTPTransportPostHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest, _ *schemas.HTTPResponse) error {
	return nil
}

// HTTPTransportStreamChunkHook passes through streaming chunks unchanged.
func (p *Plugin) HTTPTransportStreamChunkHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	return chunk, nil
}

// configFromMap round-trips a raw map[string]any into a typed *Config via JSON.
func configFromMap(raw map[string]any) (*Config, error) {
	b, err := sonic.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := sonic.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// orDefault returns v when positive, otherwise def.
func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

