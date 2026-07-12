# loopbackkafka — Kafka data connector

`loopbackkafka` is the Loopback Gateway's Kafka **data connector**: an
observability plugin that publishes completed request/response telemetry to a
configured Kafka topic. It is the reusable **template** for the other Wave‑3 data
connectors.

Clean‑room, Apache‑2.0. New code only; user‑facing strings say "Loopback
Gateway".

## Architecture

It mirrors the `plugins/otel` connector pattern:

```
Init (client lifecycle)
  -> Inject(trace)            (ObservabilityPlugin; async, post-response)
  -> convertTraceToEvent      (event/span conversion -> JSON TelemetryEvent)
  -> producer                 (transport: batching + backpressure + retries)
  -> Kafka topic              (github.com/segmentio/kafka-go)
```

| File           | Responsibility                                                            |
| -------------- | ------------------------------------------------------------------------- |
| `main.go`      | `Config`, `Plugin`, `Init`, plugin-interface hooks, `Inject`, config marshalling/redaction |
| `producer.go`  | Transport layer: bounded queue (backpressure), size/time batching, retrying writer, Kafka writer construction (incl. optional SASL/PLAIN) |
| `converter.go` | `*schemas.Trace` -> `TelemetryEvent` JSON projection + partition key      |

### Interfaces implemented (real Bifrost contracts)

- `schemas.ObservabilityPlugin` — receives completed traces via `Inject`.
- `schemas.LLMPlugin` / `schemas.HTTPTransportPlugin` — no-op hooks for plugin indexing.
- `schemas.ConfigMarshallerPlugin` — `MarshalConfigForStorage` / `RedactConfig` so
  the SASL password is stored as `env.VAR`/literal and masked in API responses.

### Delivery semantics

- **Backpressure:** `Inject` offers each event to a bounded in-memory queue
  without blocking; a full queue **drops** (counted) rather than stalling the
  request-completion path. Observability is best-effort.
- **Batching:** a single background goroutine accumulates events and flushes when
  the batch reaches `batch_size` **or** `batch_timeout_ms` elapses. The remainder
  is drained and flushed on `Cleanup`.
- **Retries:** each batch write is retried up to `max_retries` with exponential
  backoff; a batch that still fails is dropped (counted).

## Configuration

```json
{
  "brokers": ["broker-1:9092", "broker-2:9092"],
  "topic": "loopback.telemetry",
  "batch_size": 100,
  "batch_timeout_ms": 1000,
  "queue_size": 10000,
  "max_retries": 3,
  "retry_backoff_ms": 200,
  "required_acks": "one",
  "sasl_username": "user",
  "sasl_password": "env.KAFKA_PASSWORD",
  "disable_content_logging": false,
  "plugin_span_filter": { "mode": "exclude", "plugins": ["logging"] }
}
```

Only `brokers` and `topic` are required; everything else has safe defaults. The
plugin enable flag lives on the wrapping `PluginConfig`, set from the UI.

## Event schema (`loopback.kafka.telemetry.v1`)

Each completed request is published as one JSON document keyed by request ID
(falling back to trace ID), with `request_id`, `trace_id`, `session_id`,
timestamps/duration, rolled-up `provider`/`model`/`status`/`usage`, trace-level
attributes, and a per-span summary. With `disable_content_logging`, message and
tool content attributes are stripped and only metadata remains.

## Deferred (not built — clone this pattern)

The **BigQuery**, **Datadog**, and **PubSub** connectors are intentionally
deferred. Each will clone this `Init -> convert -> producer` pipeline, swapping
only the transport client and the event encoding.

## Tests

`go test ./...` covers the converter (rollups, content stripping, error status,
key fallback), the producer (full-batch / timeout / close flush, retry-then-
success, drop-after-max-retries, backpressure drop), and the plugin (Init
validation, `Inject` delivery via a fake writer, config redaction/marshalling).
Live Kafka integration is exercised separately.
