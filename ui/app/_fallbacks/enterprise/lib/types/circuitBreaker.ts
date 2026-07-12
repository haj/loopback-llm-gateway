// Types for the per-provider circuit breaker feature.
// These mirror the backend payloads served by
// /api/governance/circuit-breakers (configstore.TableCircuitBreakerConfig) and
// /api/governance/circuit-breakers/state (core CircuitBreakerStatus).

export type CircuitState = "closed" | "open" | "half_open";

// A persisted per-provider circuit breaker policy.
export interface CircuitBreakerPolicy {
	id: string;
	provider: string;
	enabled: boolean;
	failure_threshold: number;
	cooldown_seconds: number;
	half_open_probes: number;
	config_hash?: string;
	created_at: string;
	updated_at: string;
}

// Payload to create a new policy.
export interface CreateCircuitBreakerPolicy {
	provider: string;
	enabled?: boolean;
	failure_threshold?: number;
	cooldown_seconds?: number;
	half_open_probes?: number;
}

// Payload to update an existing policy (all fields optional).
export interface UpdateCircuitBreakerPolicy {
	enabled?: boolean;
	failure_threshold?: number;
	cooldown_seconds?: number;
	half_open_probes?: number;
}

// Live, in-memory breaker state reported by the core engine.
export interface CircuitBreakerStateEntry {
	provider: string;
	state: CircuitState;
	enabled: boolean;
	consecutive_failures: number;
	failure_threshold: number;
	cooldown_seconds: number;
	half_open_probes: number;
	total_trips: number;
	opened_at?: string;
	last_state_change: string;
}

export const DefaultCircuitBreakerPolicy: CreateCircuitBreakerPolicy = {
	provider: "",
	enabled: true,
	failure_threshold: 5,
	cooldown_seconds: 30,
	half_open_probes: 1,
};