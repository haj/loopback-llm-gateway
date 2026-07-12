package bifrost

import (
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

const cbTestProvider schemas.ModelProvider = "openai"

// setClock overrides the registry clock for deterministic tests. Must be called
// after the registry exists (i.e. after the first ConfigureCircuitBreaker).
func setClock(b *Bifrost, t time.Time) *time.Time {
	reg := b.circuitBreakers.Load()
	cur := t
	reg.now = func() time.Time { return cur }
	return &cur
}

// TestCircuitBreaker_DefaultOff verifies the hot path is a complete no-op when
// the feature is unconfigured: allow-all, record never panics.
func TestCircuitBreaker_DefaultOff(t *testing.T) {
	b := &Bifrost{}
	allowed, isProbe := b.circuitBreakerAllow(cbTestProvider)
	if !allowed || isProbe {
		t.Fatalf("unconfigured breaker must allow (allowed=%v isProbe=%v)", allowed, isProbe)
	}
	// Must not panic.
	b.circuitBreakerRecord(cbTestProvider, false, false)
	b.circuitBreakerRecord(cbTestProvider, true, true)
	if states := b.GetCircuitBreakerStates(); len(states) != 0 {
		t.Fatalf("expected no states, got %d", len(states))
	}
}

// TestCircuitBreaker_DisabledConfig verifies an explicitly disabled breaker
// allows all traffic and records nothing.
func TestCircuitBreaker_DisabledConfig(t *testing.T) {
	b := &Bifrost{}
	b.ConfigureCircuitBreaker(cbTestProvider, CircuitBreakerConfig{Enabled: false, FailureThreshold: 1})
	for i := 0; i < 5; i++ {
		allowed, _ := b.circuitBreakerAllow(cbTestProvider)
		if !allowed {
			t.Fatal("disabled breaker must always allow")
		}
		b.circuitBreakerRecord(cbTestProvider, false, false)
	}
}

// TestCircuitBreaker_TripsAfterThreshold drives the full state machine:
// CLOSED -> OPEN -> HALF_OPEN -> CLOSED, and a failed probe re-opening.
func TestCircuitBreaker_TripsAfterThreshold(t *testing.T) {
	b := &Bifrost{}
	b.ConfigureCircuitBreaker(cbTestProvider, CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 3,
		Cooldown:         50 * time.Millisecond,
		HalfOpenProbes:   1,
	})
	clk := setClock(b, time.Unix(0, 0))

	// Two failures: still CLOSED.
	for i := 0; i < 2; i++ {
		allowed, _ := b.circuitBreakerAllow(cbTestProvider)
		if !allowed {
			t.Fatalf("attempt %d should be allowed while CLOSED", i)
		}
		b.circuitBreakerRecord(cbTestProvider, false, false)
	}
	if got := b.GetCircuitBreakerStates()[0].State; got != CircuitClosed {
		t.Fatalf("expected CLOSED after 2 failures, got %s", got)
	}

	// Third failure trips OPEN.
	allowed, _ := b.circuitBreakerAllow(cbTestProvider)
	if !allowed {
		t.Fatal("third attempt should be allowed (it is the one that trips)")
	}
	b.circuitBreakerRecord(cbTestProvider, false, false)
	if got := b.GetCircuitBreakerStates()[0].State; got != CircuitOpen {
		t.Fatalf("expected OPEN after threshold, got %s", got)
	}

	// While OPEN within cooldown: deny (fail fast).
	if allowed, _ := b.circuitBreakerAllow(cbTestProvider); allowed {
		t.Fatal("OPEN breaker within cooldown must deny")
	}

	// Advance past cooldown: next request is a HALF_OPEN probe.
	*clk = clk.Add(60 * time.Millisecond)
	allowed, isProbe := b.circuitBreakerAllow(cbTestProvider)
	if !allowed || !isProbe {
		t.Fatalf("after cooldown expected probe (allowed=%v isProbe=%v)", allowed, isProbe)
	}
	if got := b.GetCircuitBreakerStates()[0].State; got != CircuitHalfOpen {
		t.Fatalf("expected HALF_OPEN, got %s", got)
	}

	// A second concurrent attempt while HALF_OPEN (probes=1) is denied.
	if allowed, _ := b.circuitBreakerAllow(cbTestProvider); allowed {
		t.Fatal("second concurrent half-open attempt must be denied (probes=1)")
	}

	// Probe succeeds -> CLOSED.
	b.circuitBreakerRecord(cbTestProvider, true, true)
	if got := b.GetCircuitBreakerStates()[0].State; got != CircuitClosed {
		t.Fatalf("expected CLOSED after successful probe, got %s", got)
	}

	// Trip again, then fail the probe -> re-OPEN.
	for i := 0; i < 3; i++ {
		b.circuitBreakerAllow(cbTestProvider)
		b.circuitBreakerRecord(cbTestProvider, false, false)
	}
	if got := b.GetCircuitBreakerStates()[0].State; got != CircuitOpen {
		t.Fatalf("expected OPEN after re-tripping, got %s", got)
	}
	*clk = clk.Add(60 * time.Millisecond)
	allowed, isProbe = b.circuitBreakerAllow(cbTestProvider)
	if !allowed || !isProbe {
		t.Fatal("expected half-open probe after second cooldown")
	}
	b.circuitBreakerRecord(cbTestProvider, false, isProbe) // failed probe
	if got := b.GetCircuitBreakerStates()[0].State; got != CircuitOpen {
		t.Fatalf("failed probe must re-open, got %s", got)
	}
	if trips := b.GetCircuitBreakerStates()[0].TotalTrips; trips != 3 {
		t.Fatalf("expected 3 total trips, got %d", trips)
	}
}

// TestCircuitBreaker_SuccessResetsFailures verifies a success in CLOSED clears
// the consecutive-failure counter so transient blips don't trip the breaker.
func TestCircuitBreaker_SuccessResetsFailures(t *testing.T) {
	b := &Bifrost{}
	b.ConfigureCircuitBreaker(cbTestProvider, CircuitBreakerConfig{Enabled: true, FailureThreshold: 3, Cooldown: time.Second, HalfOpenProbes: 1})
	setClock(b, time.Unix(0, 0))
	b.circuitBreakerRecord(cbTestProvider, false, false)
	b.circuitBreakerRecord(cbTestProvider, false, false)
	b.circuitBreakerRecord(cbTestProvider, true, false) // reset
	b.circuitBreakerRecord(cbTestProvider, false, false)
	b.circuitBreakerRecord(cbTestProvider, false, false)
	if got := b.GetCircuitBreakerStates()[0].State; got != CircuitClosed {
		t.Fatalf("expected CLOSED (success reset counter), got %s", got)
	}
}

// TestCircuitBreaker_ConcurrentSafe runs allow/record from many goroutines to
// surface races under -race; it asserts only that nothing panics/deadlocks.
func TestCircuitBreaker_ConcurrentSafe(t *testing.T) {
	b := &Bifrost{}
	b.ConfigureCircuitBreaker(cbTestProvider, CircuitBreakerConfig{Enabled: true, FailureThreshold: 5, Cooldown: time.Millisecond, HalfOpenProbes: 2})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				allowed, isProbe := b.circuitBreakerAllow(cbTestProvider)
				if allowed {
					b.circuitBreakerRecord(cbTestProvider, j%2 == 0, isProbe)
				}
			}
		}(i)
	}
	wg.Wait()
}

// cbTransition is one observed listener callback.
type cbTransition struct {
	provider schemas.ModelProvider
	from, to CircuitState
}

// listenTransitions installs a listener that streams transitions to a channel.
func listenTransitions(b *Bifrost) chan cbTransition {
	ch := make(chan cbTransition, 16)
	b.SetCircuitBreakerListener(func(provider schemas.ModelProvider, from, to CircuitState) {
		ch <- cbTransition{provider: provider, from: from, to: to}
	})
	return ch
}

// expectTransition waits for the next listener callback and asserts its shape.
func expectTransition(t *testing.T, ch chan cbTransition, from, to CircuitState) {
	t.Helper()
	select {
	case got := <-ch:
		if got.provider != cbTestProvider || got.from != from || got.to != to {
			t.Fatalf("expected transition %s: %s->%s, got %s: %s->%s",
				cbTestProvider, from, to, got.provider, got.from, got.to)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for transition %s->%s", from, to)
	}
}

// expectNoTransition asserts the listener stays quiet.
func expectNoTransition(t *testing.T, ch chan cbTransition) {
	t.Helper()
	select {
	case got := <-ch:
		t.Fatalf("unexpected transition %s->%s", got.from, got.to)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestCircuitBreaker_ListenerObservesFullCycle walks CLOSED -> OPEN ->
// HALF_OPEN -> CLOSED and a failed probe re-open, asserting the listener sees
// each transition exactly once with correct from/to states.
func TestCircuitBreaker_ListenerObservesFullCycle(t *testing.T) {
	b := &Bifrost{}
	b.ConfigureCircuitBreaker(cbTestProvider, CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 2,
		Cooldown:         50 * time.Millisecond,
		HalfOpenProbes:   1,
	})
	clk := setClock(b, time.Unix(0, 0))
	ch := listenTransitions(b)

	// First failure: still CLOSED, no callback.
	b.circuitBreakerAllow(cbTestProvider)
	b.circuitBreakerRecord(cbTestProvider, false, false)
	expectNoTransition(t, ch)

	// Second failure trips CLOSED -> OPEN.
	b.circuitBreakerAllow(cbTestProvider)
	b.circuitBreakerRecord(cbTestProvider, false, false)
	expectTransition(t, ch, CircuitClosed, CircuitOpen)

	// Cooldown elapses: the next allow promotes OPEN -> HALF_OPEN.
	*clk = clk.Add(100 * time.Millisecond)
	allowed, isProbe := b.circuitBreakerAllow(cbTestProvider)
	if !allowed || !isProbe {
		t.Fatalf("expected a half-open probe grant (allowed=%v isProbe=%v)", allowed, isProbe)
	}
	expectTransition(t, ch, CircuitOpen, CircuitHalfOpen)

	// Probe succeeds: HALF_OPEN -> CLOSED.
	b.circuitBreakerRecord(cbTestProvider, true, true)
	expectTransition(t, ch, CircuitHalfOpen, CircuitClosed)

	// Trip again, then fail the probe: HALF_OPEN -> OPEN.
	b.circuitBreakerAllow(cbTestProvider)
	b.circuitBreakerRecord(cbTestProvider, false, false)
	b.circuitBreakerAllow(cbTestProvider)
	b.circuitBreakerRecord(cbTestProvider, false, false)
	expectTransition(t, ch, CircuitClosed, CircuitOpen)
	*clk = clk.Add(100 * time.Millisecond)
	b.circuitBreakerAllow(cbTestProvider)
	expectTransition(t, ch, CircuitOpen, CircuitHalfOpen)
	b.circuitBreakerRecord(cbTestProvider, false, true)
	expectTransition(t, ch, CircuitHalfOpen, CircuitOpen)
}

// TestCircuitBreaker_ListenerRemovable verifies a nil listener restores the
// silent default and that installing one never affects breaker decisions.
func TestCircuitBreaker_ListenerRemovable(t *testing.T) {
	b := &Bifrost{}
	b.ConfigureCircuitBreaker(cbTestProvider, CircuitBreakerConfig{
		Enabled: true, FailureThreshold: 1, Cooldown: time.Hour, HalfOpenProbes: 1,
	})
	ch := listenTransitions(b)
	b.SetCircuitBreakerListener(nil)

	b.circuitBreakerAllow(cbTestProvider)
	b.circuitBreakerRecord(cbTestProvider, false, false) // trips, silently
	expectNoTransition(t, ch)
	if got := b.GetCircuitBreakerStates()[0].State; got != CircuitOpen {
		t.Fatalf("breaker must trip regardless of listener, got %s", got)
	}

	// Nil-receiver safety.
	var nilB *Bifrost
	nilB.SetCircuitBreakerListener(func(schemas.ModelProvider, CircuitState, CircuitState) {})
}
