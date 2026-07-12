import { baseApi } from "./baseApi";

// Vault / secret-manager sync endpoints (Loopback Gateway OSS backend).
//
// Vault backend configuration is file/env-only (config_store.vault_store in
// config.json) — the config store itself may hold vault references, so
// credentials are never editable from the dashboard. These endpoints expose
// status + operations only: a status snapshot, a manual refresh that
// re-resolves rotated provider secrets into the live engine without a restart,
// and a connectivity test. The base query already prefixes requests with
// `/api`, so URLs here are relative to that.

export interface VaultCacheStatus {
	entries: number;
	ttl_seconds: number;
}

export interface VaultStatus {
	enabled: boolean;
	type?: string;
	prefix?: string;
	access_mode?: string;
	sync_interval?: string;
	healthy?: boolean;
	health_error?: string;
	cache?: VaultCacheStatus;
	last_refresh?: string;
	last_refresh_errors?: string[];
	last_error?: string;
	last_error_at?: string;
}

export interface VaultRefreshResponse {
	message: string;
	providers_updated: string[];
	keys_rechecked: number;
	secrets_updated: number;
	errors: string[];
}

export interface VaultTestConnectionResponse {
	healthy: boolean;
	message: string;
}

// Tag registered locally so baseApi.ts stays untouched.
const vaultBaseApi = baseApi.enhanceEndpoints({ addTagTypes: ["VaultStatus"] });

export const vaultApi = vaultBaseApi.injectEndpoints({
	endpoints: (builder) => ({
		getVaultStatus: builder.query<VaultStatus, void>({
			query: () => ({ url: "/vault/status" }),
			providesTags: ["VaultStatus"],
		}),
		refreshVault: builder.mutation<VaultRefreshResponse, void>({
			query: () => ({
				url: "/vault/refresh",
				method: "POST",
			}),
			invalidatesTags: ["VaultStatus"],
		}),
		testVaultConnection: builder.mutation<VaultTestConnectionResponse, void>({
			query: () => ({
				url: "/vault/test-connection",
				method: "POST",
			}),
			invalidatesTags: ["VaultStatus"],
		}),
	}),
});

export const { useGetVaultStatusQuery, useRefreshVaultMutation, useTestVaultConnectionMutation } = vaultApi;