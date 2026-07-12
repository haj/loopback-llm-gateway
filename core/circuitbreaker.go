// Package bifrost — per-provider circuit breaker.
//
// This file implements a per-instance, per-provider circuit breaker state
// machine (closed/open/half-open) that lets Loopback Gateway stop hammering a
// provider that is failing and instead trip straight to the configured fallback
// chain. It is wired into the request hot path in bifrost.go (handleRequest /
// handleStreamRequest) immediately before the primary provider attempt.
//
// SAFETY / SEMANTICS (critical — this touches the hot path):
//   - OPT-IN, DEFAULT-OFF. The registry pointer on Bifrost is nil until a
//     provider is explicitly configured. While nil (or while a provider has no
//     enabled config), circuitBreakerAllow always returns allowed=true and
//     circuitBreakerRecord is a no-op. Existing request behavior is therefore
//     unchanged when the feature is unconfigured.
//   - NEVER PANICS. Every accessor guards nil receivers / missing entries.
//   - NEVER BLOCKS a request: the only synchronization is short, in-memory
//     mutex sections — no I/O, no waiting on cooldown. An OPEN breaker fails
//     fast (the caller trips to fallbacks) rather than sleeping.
//
// DEFERRED (explicitly out of scope for this slice): distributed / cluster-wide
// breaker state. State here is strictly per-instance / in-memory; each gateway
// instance maintains its own view. See DELIVERY notes.
package bifrost

import (
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// CircuitState is the breaker's current state for a provider.
type CircuitState string

const (
	// CircuitClosed is the normal state: requests flow to the provider.
	CircuitClosed CircuitState = "closed"
	// CircuitOpen means the provider is considered dead: primary attempts are
	// short-circuited to the fallback chain without calling the provider.
	CircuitOpen CircuitState = "open"
	// CircuitHalfOpen is the trial state after the cooldown elapses: a limited
	// number of probe requests are allowed through to test recovery.
	CircuitHalfOpen CircuitState = "half_open"
)

// Defaults applied when a config field is left at its zero value.
const (
	defaultCBFailureThreshold = 5
	defaultCBCooldown         = 30 * time.Second
	defaultCBHalfOpenProbes   = 1
)

// CircuitBreakerConfig is the per-provider configuration for the breaker.
type CircuitBreakerConfig struct {
	// Enabled gates the breaker for this provider. When false, the provider is
	// treated as having no breaker (allow-all, record no-op).
	Enabled bool
	// FailureThreshold is the number of consecutive failures that trips the
	// breaker from CLOSED to OPEN. Defaults to 5 when <= 0.
	FailureThreshold int
	// Cooldown is how long the breaker stays OPEN before allowing a probe
	// (OPEN -> HALF_OPEN). Defaults to 30s when <= 0.
	Cooldown time.Duration
	// HalfOpenProbes is the number of probe requests allowed concurrently while
	// HALF_OPEN; that many consecutive probe successes also closes the breaker.
	// Defaults to 1 when <= 0.
	HalfOpenProbes int
}

// withDefaults returns a copy with zero-valued fields filled in.
func (c CircuitBreakerConfig) withDefaults() CircuitBreakerConfig {
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = defaultCBFailureThreshold
	}
	if c.Cooldown <= 0 {
		c.Cooldown = defaultCBCooldown
	}
	if c.HalfOpenProbes <= 0 {
		c.HalfOpenProbes = defaultCBHalfOpenProbes
	}
	return c
}

// CircuitBreakerListener observes breaker state transitions (e.g. the
// alerting bridge in the HTTP transport). It is invoked on its own goroutine
// AFTER the breaker mutex is released — implementations may do I/O, but must
// tolerate concurrent and slightly out-of-order calls under races.
type CircuitBreakerListener func(provider schemas.ModelProvider, from, to CircuitState)

// SetCircuitBreakerListener installs (or, with nil, removes) the transition
// listener. ADDITIVE and DEFAULT-OFF: with no listener installed the hot path
// is byte-for-byte unchanged.
func (bifrost *Bifrost) SetCircuitBreakerListener(listener CircuitBreakerListener) {
	if bifrost == nil {
		return
	}
	if listener == nil {
		bifrost.circuitBreakerListener.Store(nil)
		return
	}
	bifrost.circuitBreakerListener.Store(&listener)
}

// notifyCircuitBreakerTransition invokes the listener (if any) on a fresh
// goroutine. Callers must have released pb.mu first — the NEVER-BLOCKS
// invariant of this file extends to the listener.
func (bifrost *Bifrost) notifyCircuitBreakerTransition(provider schemas.ModelProvider, from, to CircuitState) {
	if bifrost == nil || from == to {
		return
	}
	lp := bifrost.circuitBreakerListener.Load()
	if lp == nil {
		return
	}
	listener := *lp
	go listener(provider, from, to)
}

// CircuitBreakerStatus is a point-in-time snapshot of a provider breaker,
// returned by GetCircuitBreakerStates for the management API / UI.
type CircuitBreakerStatus struct {
	Provider            string       `json:"provider"`
	State               CircuitState `json:"state"`
	Enabled             bool         `json:"enabled"`
	ConsecutiveFailures int          `json:"consecutive_failures"`
	FailureThreshold    int          `json:"failure_threshold"`
	CooldownSeconds     float64      `json:"cooldown_seconds"`
	HalfOpenProbes      int          `json:"half_open_probes"`
	TotalTrips          int64        `json:"total_trips"`
	OpenedAt            *time.Time   `json:"opened_at,omitempty"`
	LastStateChange     time.Time    `json:"last_state_change"`
}

// providerBreaker is the runtime state for a single provider. All fields are
// guarded by mu.
type providerBreaker struct {
	mu                  sync.Mutex
	cfg                 CircuitBreakerConfig
	state               CircuitState
	consecutiveFailures int
	halfOpenSuccesses   int
	halfOpenInFlight    int
	openedAt            time.Time
	lastStateChange     time.Time
	totalTrips          int64
}

// circuitBreakerRegistry holds all per-provider breakers for one instance.
type circuitBreakerRegistry struct {
	mu       sync.RWMutex
	breakers map[schemas.ModelProvider]*providerBreaker
	// now is an injectable clock (tests override it; production uses time.Now).
	now func() time.Time
}

func newCircuitBreakerRegistry() *circuitBreakerRegistry {
	return &circuitBreakerRegistry{
		breakers: make(map[schemas.ModelProvider]*providerBreaker),
		now:      time.Now,
	}
}

// ----- public API (called by the transport/config layer) -----

// ConfigureCircuitBreaker installs or updates the breaker config for a provider.
// It lazily creates the registry on first use, making the feature opt-in: until
// this is called for at least one provider, the hot path sees a nil registry and
// is a complete no-op. Disabling a provider's breaker (cfg.Enabled == false)
// retains the entry but resets it to CLOSED so a later re-enable starts clean.
func (bifrost *Bifrost) ConfigureCircuitBreaker(provider schemas.ModelProvider, cfg CircuitBreakerConfig) {
	if bifrost == nil || provider == "" {
		return
	}
	reg := bifrost.circuitBreakers.Load()
	if reg == nil {
		fresh := newCircuitBreakerRegistry()
		if bifrost.circuitBreakers.CompareAndSwap(nil, fresh) {
			reg = fresh
		} else {
			reg = bifrost.circuitBreakers.Load()
		}
	}
	if reg == nil {
		return
	}
	cfg = cfg.withDefaults()

	reg.mu.Lock()
	pb := reg.breakers[provider]
	if pb == nil {
		pb = &providerBreaker{state: CircuitClosed, lastStateChange: reg.now()}
		reg.breakers[provider] = pb
	}
	reg.mu.Unlock()

	pb.mu.Lock()
	pb.cfg = cfg
	// A disabled or reconfigured breaker resets to a clean CLOSED state.
	pb.state = CircuitClosed
	pb.consecutiveFailures = 0
	pb.halfOpenSuccesses = 0
	pb.halfOpenInFlight = 0
	pb.openedAt = time.Time{}
	pb.lastStateChange = reg.now()
	pb.mu.Unlock()
}

// RemoveCircuitBreaker drops the breaker for a provider (back to allow-all).
func (bifrost *Bifrost) RemoveCircuitBreaker(provider schemas.ModelProvider) {
	if bifrost == nil {
		return
	}
	reg := bifrost.circuitBreakers.Load()
	if reg == nil {
		return
	}
	reg.mu.Lock()
	delete(reg.breakers, provider)
	reg.mu.Unlock()
}

// ResetCircuitBreaker forces a provider's breaker back to CLOSED (manual
// recovery from the UI). No-op if unconfigured.
func (bifrost *Bifrost) ResetCircuitBreaker(provider schemas.ModelProvider) {
	if bifrost == nil {
		return
	}
	reg := bifrost.circuitBreakers.Load()
	if reg == nil {
		return
	}
	pb := reg.get(provider)
	if pb == nil {
		return
	}
	pb.mu.Lock()
	pb.state = CircuitClosed
	pb.consecutiveFailures = 0
	pb.halfOpenSuccesses = 0
	pb.halfOpenInFlight = 0
	pb.openedAt = time.Time{}
	pb.lastStateChange = reg.now()
	pb.mu.Unlock()
}

// GetCircuitBreakerStates returns a snapshot of every configured breaker. Empty
// when the feature is unconfigured.
func (bifrost *Bifrost) GetCircuitBreakerStates() []CircuitBreakerStatus {
	if bifrost == nil {
		return nil
	}
	reg := bifrost.circuitBreakers.Load()
	if reg == nil {
		return nil
	}
	reg.mu.RLock()
	out := make([]CircuitBreakerStatus, 0, len(reg.breakers))
	for provider, pb := range reg.breakers {
		pb.mu.Lock()
		status := CircuitBreakerStatus{
			Provider:            string(provider),
			State:               pb.state,
			Enabled:             pb.cfg.Enabled,
			ConsecutiveFailures: pb.consecutiveFailures,
			FailureThreshold:    pb.cfg.FailureThreshold,
			CooldownSeconds:     pb.cfg.Cooldown.Seconds(),
			HalfOpenProbes:      pb.cfg.HalfOpenProbes,
			TotalTrips:          pb.totalTrips,
			LastStateChange:     pb.lastStateChange,
		}
		if !pb.openedAt.IsZero() {
			opened := pb.openedAt
			status.OpenedAt = &opened
		}
		pb.mu.Unlock()
		out = append(out, status)
	}
	reg.mu.RUnlock()
	return out
}

func (reg *circuitBreakerRegistry) get(provider schemas.ModelProvider) *providerBreaker {
	reg.mu.RLock()
	pb := reg.breakers[provider]
	reg.mu.RUnlock()
	return pb
}

// ----- hot-path internals (called from handleRequest / handleStreamRequest) -----

// circuitBreakerAllow decides whether the primary attempt for provider may
// proceed. Returns allowed=true when the request should call the provider, and
// isProbe=true when this grant is a HALF_OPEN trial that must be reported back
// via circuitBreakerRecord. Default-off: a nil registry / missing / disabled
// breaker always allows (allowed=true, isProbe=false) and never blocks.
func (bifrost *Bifrost) circuitBreakerAllow(provider schemas.ModelProvider) (allowed bool, isProbe bool) {
	reg := bifrost.circuitBreakers.Load()
	if reg == nil {
		return true, false
	}
	pb := reg.get(provider)
	if pb == nil {
		return true, false
	}

	pb.mu.Lock()
	// Transition capture for the listener. Defer order (LIFO): `to` is read
	// while the mutex is still held, then the mutex unlocks, then the notify
	// fires — so the listener never runs under pb.mu.
	var from, to CircuitState
	defer func() { bifrost.notifyCircuitBreakerTransition(provider, from, to) }()
	defer pb.mu.Unlock()
	from = pb.state
	to = pb.state
	defer func() { to = pb.state }()
	if !pb.cfg.Enabled {
		return true, false
	}

	switch pb.state {
	case CircuitClosed:
		return true, false
	case CircuitOpen:
		// Stay OPEN (fail fast) until the cooldown elapses, then promote one
		// request to a HALF_OPEN probe.
		if reg.now().Sub(pb.openedAt) < pb.cfg.Cooldown {
			return false, false
		}
		pb.state = CircuitHalfOpen
		pb.halfOpenSuccesses = 0
		pb.halfOpenInFlight = 1
		pb.lastStateChange = reg.now()
		return true, true
	case CircuitHalfOpen:
		// Allow up to HalfOpenProbes concurrent probes; deny the rest.
		if pb.halfOpenInFlight < pb.cfg.HalfOpenProbes {
			pb.halfOpenInFlight++
			return true, true
		}
		return false, false
	default:
		return true, false
	}
}

// circuitBreakerRecord reports the outcome of an attempt that circuitBreakerAllow
// permitted. success=false trips the breaker once the failure threshold is hit;
// success advances/closes a HALF_OPEN breaker. isProbe must match the value
// returned by the corresponding circuitBreakerAllow call so in-flight probe
// accounting stays balanced. No-op when unconfigured.
func (bifrost *Bifrost) circuitBreakerRecord(provider schemas.ModelProvider, success bool, isProbe bool) {
	reg := bifrost.circuitBreakers.Load()
	if reg == nil {
		return
	}
	pb := reg.get(provider)
	if pb == nil {
		return
	}

	pb.mu.Lock()
	// Transition capture mirrors circuitBreakerAllow: `to` is read under the
	// mutex, notify fires after unlock, listener runs on its own goroutine.
	var from, to CircuitState
	defer func() { bifrost.notifyCircuitBreakerTransition(provider, from, to) }()
	defer pb.mu.Unlock()
	from = pb.state
	to = pb.state
	defer func() { to = pb.state }()
	if !pb.cfg.Enabled {
		return
	}

	if isProbe && pb.halfOpenInFlight > 0 {
		pb.halfOpenInFlight--
	}

	if success {
		pb.consecutiveFailures = 0
		switch pb.state {
		case CircuitHalfOpen:
			pb.halfOpenSuccesses++
			if pb.halfOpenSuccesses >= pb.cfg.HalfOpenProbes {
				pb.state = CircuitClosed
				pb.halfOpenSuccesses = 0
				pb.openedAt = time.Time{}
				pb.lastStateChange = reg.now()
			}
		case CircuitOpen:
			// A late success arriving while OPEN: ignore (cooldown governs).
		default:
			// CLOSED: nothing to do.
		}
		return
	}

	// Failure path.
	pb.consecutiveFailures++
	switch pb.state {
	case CircuitHalfOpen:
		// A failed probe re-opens immediately.
		pb.trip(reg.now())
	case CircuitClosed:
		if pb.consecutiveFailures >= pb.cfg.FailureThreshold {
			pb.trip(reg.now())
		}
	case CircuitOpen:
		// Already open.
	}
}

// trip moves the breaker to OPEN. Caller must hold pb.mu.
func (pb *providerBreaker) trip(now time.Time) {
	pb.state = CircuitOpen
	pb.openedAt = now
	pb.lastStateChange = now
	pb.halfOpenSuccesses = 0
	pb.halfOpenInFlight = 0
	pb.totalTrips++
}

// newCircuitBreakerOpenError synthesizes the primary error used when a request
// is short-circuited because the provider's breaker is OPEN. AllowFallbacks is
// explicitly true so the core fallback orchestration in handleRequest proceeds
// (or, with no fallbacks configured, the request fails fast without ever calling
// the dead provider).
func (bifrost *Bifrost) newCircuitBreakerOpenError(req *schemas.BifrostRequest, provider schemas.ModelProvider, model string) *schemas.BifrostError {
	allow := true
	berr := &schemas.BifrostError{
		IsBifrostError: true,
		AllowFallbacks: &allow,
		Error: &schemas.ErrorField{
			Message: "Loopback Gateway circuit breaker is open for provider " + string(provider) + "; skipping primary and trying fallbacks",
		},
	}
	berr.PopulateExtraFields(req.RequestType, provider, model, model)
	return berr
}
