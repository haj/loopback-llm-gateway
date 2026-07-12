// Types for the Loopback Gateway alert-channels UI. These mirror the backend
// transports/bifrost-http/handlers/alertchannels.go contract and the
// configstore TableAlertChannel shape. Channel secrets (PagerDuty routing
// keys, webhook signing keys) are never returned by the API — only the
// has_secret flag signals their presence.

// AlertChannelType mirrors the backend destination enum.
export type AlertChannelType = "slack" | "pagerduty" | "webhook";

// AlertEventType mirrors framework/alerting's event-type constants. An empty
// event_types filter on a channel admits all of them.
export type AlertEventType =
	| "audit.mutation"
	| "budget.exceeded"
	| "rate_limit.exceeded"
	| "circuit_breaker.open"
	| "circuit_breaker.closed";

export const ALERT_EVENT_TYPES: { value: AlertEventType; label: string }[] = [
	{ value: "audit.mutation", label: "Governance mutations" },
	{ value: "budget.exceeded", label: "Budget exceeded" },
	{ value: "rate_limit.exceeded", label: "Rate limit exceeded" },
	{ value: "circuit_breaker.open", label: "Circuit breaker opened" },
	{ value: "circuit_breaker.closed", label: "Circuit breaker recovered" },
];

// AlertChannel is one configured destination as returned by the API.
export interface AlertChannel {
	id: string;
	name: string;
	type: AlertChannelType;
	enabled: boolean;
	endpoint_url: string;
	event_types: AlertEventType[] | null;
	has_secret: boolean;
	last_attempt_at?: string;
	last_status?: string;
	last_error?: string;
	created_at: string;
	updated_at: string;
}

// AlertChannelInput is the create/update payload. Omitted fields are left
// unchanged on update; an omitted secret preserves the stored one.
export interface AlertChannelInput {
	name?: string;
	type?: AlertChannelType;
	enabled?: boolean;
	endpoint_url?: string;
	secret?: string;
	event_types?: AlertEventType[];
}

export interface TestFireResult {
	status: "ok" | "failed";
	http_status: number;
	error: string;
}