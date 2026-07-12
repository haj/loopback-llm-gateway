// Types for the Loopback Gateway admin API keys UI. These mirror the backend
// payloads exposed by transports/bifrost-http/handlers/apikeys.go. Admin API
// keys are distinct from governance virtual keys: they authenticate the
// management API and carry (resource, operation) scopes reusing the RBAC
// permission vocabulary (see ui/lib/types/rbac.ts PermissionInput).

import { PermissionInput } from "./rbac";

export type AdminAPIKeyStatus = "active" | "revoked";

// A single (resource, operation) scope grant on a key. Resource/Operation
// accept the "*" wildcard.
export interface AdminAPIKeyScope {
	id?: string;
	api_key_id?: string;
	resource: string;
	operation: string;
	created_at?: string;
}

// Redacted key representation: the secret is never returned after creation —
// only the short display prefix.
export interface AdminAPIKey {
	id: string;
	name: string;
	key_prefix: string;
	status: AdminAPIKeyStatus;
	expires_at?: string | null;
	last_used_at?: string | null;
	rotated_from_id?: string | null;
	created_by: string;
	scopes: AdminAPIKeyScope[];
	created_at: string;
	updated_at: string;
}

// ---- query params ----

export interface ListAPIKeysParams {
	search?: string;
	status?: AdminAPIKeyStatus;
	limit?: number;
	offset?: number;
}

// ---- request bodies ----

export interface CreateAPIKeyRequest {
	name: string;
	expires_at?: string;
	scopes?: PermissionInput[];
}

export interface UpdateAPIKeyRequest {
	id: string;
	name?: string;
	expires_at?: string;
	clear_expiry?: boolean;
	scopes?: PermissionInput[];
}

// ---- responses ----

export interface ListAPIKeysResponse {
	api_keys: AdminAPIKey[];
	total: number;
	count: number;
	offset: number;
}

export interface APIKeyResponse {
	message?: string;
	api_key: AdminAPIKey;
}

// Returned by create and rotate only: `key` is the plaintext secret, shown
// exactly once and never recoverable afterwards.
export interface APIKeySecretResponse extends APIKeyResponse {
	key: string;
}