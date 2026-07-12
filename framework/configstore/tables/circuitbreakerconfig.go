package tables

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Circuit breaker config defaults. Mirror the core engine defaults
// (core/circuitbreaker.go) so a row created with zero values behaves the same
// whether the engine or the DB supplies the fallback.
const (
	DefaultCircuitBreakerFailureThreshold = 5
	DefaultCircuitBreakerCooldownSeconds  = 30
	DefaultCircuitBreakerHalfOpenProbes   = 1
)

// TableCircuitBreakerConfig is the persisted, per-provider circuit breaker
// policy backing the Loopback Gateway circuit-breaker UI. Exactly one row may
// exist per provider (Provider is unique); the HTTP handler pushes each enabled
// row into the running core engine via Bifrost.ConfigureCircuitBreaker so the
// breaker state machine in core/circuitbreaker.go enforces it on the hot path.
//
// Scope is intentionally per-provider only for this first slice. Per-VK / per
// team scoping and distributed (cluster-wide) breaker state are deferred — this
// table and the engine keep state per-instance. Follows the column conventions
// of TableGuardrailConfig (ConfigHash + CreatedAt/UpdatedAt indexing).
type TableCircuitBreakerConfig struct {
	ID string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	// Provider is the Bifrost provider short name (e.g. "openai"). One policy
	// per provider.
	Provider string `gorm:"type:varchar(255);not null;uniqueIndex" json:"provider"`
	// Enabled gates enforcement. A disabled policy is persisted but removed from
	// the live engine (allow-all for that provider).
	Enabled bool `gorm:"not null;default:true" json:"enabled"`
	// FailureThreshold is the consecutive-failure count that trips the breaker
	// OPEN.
	FailureThreshold int `gorm:"not null;default:5" json:"failure_threshold"`
	// CooldownSeconds is how long the breaker stays OPEN before allowing a
	// HALF_OPEN probe.
	CooldownSeconds int `gorm:"not null;default:30" json:"cooldown_seconds"`
	// HalfOpenProbes is the number of concurrent trial requests allowed (and the
	// consecutive successes needed to close) while HALF_OPEN.
	HalfOpenProbes int `gorm:"not null;default:1" json:"half_open_probes"`

	// ConfigHash detects changes synced from a config.json file.
	ConfigHash string `gorm:"type:varchar(255);null" json:"config_hash"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`
}

// TableName sets the table name.
func (TableCircuitBreakerConfig) TableName() string {
	return "governance_circuit_breaker_configs"
}

// BeforeSave validates the provider and clamps numeric fields to sane defaults
// so a malformed row can never produce a breaker that traps every request
// permanently (e.g. a zero threshold).
func (c *TableCircuitBreakerConfig) BeforeSave(tx *gorm.DB) error {
	c.Provider = strings.TrimSpace(c.Provider)
	if c.Provider == "" {
		return fmt.Errorf("circuit breaker config provider cannot be empty")
	}
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = DefaultCircuitBreakerFailureThreshold
	}
	if c.CooldownSeconds <= 0 {
		c.CooldownSeconds = DefaultCircuitBreakerCooldownSeconds
	}
	if c.HalfOpenProbes <= 0 {
		c.HalfOpenProbes = DefaultCircuitBreakerHalfOpenProbes
	}
	return nil
}
