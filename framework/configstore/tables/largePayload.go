package tables

// ConfigLargePayloadKey is the governance_config key under which the
// large-payload streaming configuration is persisted as a JSON blob.
const ConfigLargePayloadKey = "large_payload_config"

// LargePayloadConfig holds the tunables for large-payload streaming.
//
// When a request or response body exceeds the configured threshold, Loopback
// Gateway switches to a streaming/pass-through mode instead of buffering the
// whole payload in memory. All sizes are expressed in bytes.
//
// Changing any of these values requires a server restart to take effect.
type LargePayloadConfig struct {
	// Enabled toggles large-payload streaming on or off.
	Enabled bool `json:"enabled"`
	// RequestThresholdBytes is the request body size above which streaming mode engages.
	RequestThresholdBytes int64 `json:"request_threshold_bytes"`
	// ResponseThresholdBytes is the response body size above which streaming mode engages.
	ResponseThresholdBytes int64 `json:"response_threshold_bytes"`
	// PrefetchSizeBytes is how many bytes are read ahead while streaming.
	PrefetchSizeBytes int64 `json:"prefetch_size_bytes"`
	// MaxPayloadBytes is the hard upper bound on an accepted payload.
	MaxPayloadBytes int64 `json:"max_payload_bytes"`
	// TruncatedLogBytes caps how many payload bytes are written to logs.
	TruncatedLogBytes int64 `json:"truncated_log_bytes"`
}

// DefaultLargePayloadConfig mirrors the UI fallback defaults so a fresh
// install (no persisted row) reports a sane configuration.
var DefaultLargePayloadConfig = LargePayloadConfig{
	Enabled:                false,
	RequestThresholdBytes:  10 * 1024 * 1024,  // 10MB
	ResponseThresholdBytes: 10 * 1024 * 1024,  // 10MB
	PrefetchSizeBytes:      64 * 1024,         // 64KB
	MaxPayloadBytes:        500 * 1024 * 1024, // 500MB
	TruncatedLogBytes:      1024 * 1024,       // 1MB
}
