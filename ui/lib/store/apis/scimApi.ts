import { baseApi } from "./baseApi";

// SSO/SCIM endpoints for the Loopback Gateway first slice (Keycloak provider).
//
// These talk to the open-source backend under `/api/scim`. The base query
// already prefixes `/api`, so URLs here are relative to that.
//
// Everything is default-OFF: when SSO is not configured the login-config and
// status endpoints report `enabled: false`, and the UI shows only password auth.
// Provisioned users/groups are populated by the sync engine (POST /scim/sync)
// or just-in-time when a valid IdP JWT is presented.

// Public login-page config: just enough for the login view to decide whether to
// offer an SSO option.
export interface SCIMLoginConfig {
	enabled: boolean;
	provider?: string;
	issuer_url?: string;
	authorization_endpoint?: string;
	client_id?: string;
	auth_mode?: string;
}

export interface SCIMStatus {
	enabled: boolean;
	configured: boolean;
	provider?: string;
	valid?: boolean;
	error?: string;
	// True when the inbound /scim/v2 provisioning endpoint is live (the token
	// itself is never returned).
	provisioning_enabled?: boolean;
}

export interface SCIMUser {
	id: string;
	provider: string;
	external_id: string;
	user_name: string;
	email: string;
	display_name: string;
	active: boolean;
	groups: string[];
	managed_user_id?: string;
	// Attribute-mapping outcome from the last provisioning pass.
	mapped_role_id?: string;
	mapped_team_ids?: string[];
	mapped_business_unit_id?: string;
	last_synced_at: string;
	created_at: string;
	updated_at: string;
}

export interface SCIMGroup {
	id: string;
	provider: string;
	external_id: string;
	display_name: string;
	members: string[];
	last_synced_at: string;
	created_at: string;
	updated_at: string;
}

export interface SCIMSyncResult {
	users_synced: number;
	users_deactivated: number;
	groups_synced: number;
	errors?: string[];
}

export const scimApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		// PUBLIC — used by the login page (route is whitelisted server-side).
		getSCIMLoginConfig: builder.query<SCIMLoginConfig, void>({
			query: () => ({ url: "/scim/oauth/config" }),
			providesTags: ["SCIMProviders"],
		}),
		getSCIMStatus: builder.query<SCIMStatus, void>({
			query: () => ({ url: "/scim/status" }),
			providesTags: ["SCIMProviders"],
		}),
		getSCIMUsers: builder.query<{ users: SCIMUser[]; total: number; count: number }, { search?: string } | void>({
			query: (params) => {
				const qs = new URLSearchParams();
				if (params?.search) qs.set("search", params.search);
				const s = qs.toString();
				return { url: `/scim/users${s ? `?${s}` : ""}` };
			},
			providesTags: ["SCIMProviders"],
		}),
		getSCIMGroups: builder.query<{ groups: SCIMGroup[]; total: number; count: number }, { search?: string } | void>({
			query: (params) => {
				const qs = new URLSearchParams();
				if (params?.search) qs.set("search", params.search);
				const s = qs.toString();
				return { url: `/scim/groups${s ? `?${s}` : ""}` };
			},
			providesTags: ["SCIMProviders"],
		}),
		triggerSCIMSync: builder.mutation<{ result: SCIMSyncResult }, void>({
			query: () => ({ url: "/scim/sync", method: "POST" }),
			invalidatesTags: ["SCIMProviders", "Users"],
		}),
	}),
});

export const {
	useGetSCIMLoginConfigQuery,
	useGetSCIMStatusQuery,
	useGetSCIMUsersQuery,
	useGetSCIMGroupsQuery,
	useTriggerSCIMSyncMutation,
} = scimApi;