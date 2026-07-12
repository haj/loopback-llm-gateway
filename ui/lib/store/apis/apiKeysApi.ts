import {
	APIKeyResponse,
	APIKeySecretResponse,
	CreateAPIKeyRequest,
	ListAPIKeysParams,
	ListAPIKeysResponse,
	UpdateAPIKeyRequest,
} from "@/lib/types/apikeys";
import { baseApi } from "./baseApi";

// RTK-query slice for admin API keys (scope-based management-API credentials).
// Follows rbacApi's providesTags/invalidatesTags cache-coherence pattern using
// the shared "APIKeys" tag declared in baseApi.
export const apiKeysApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getAPIKeys: builder.query<ListAPIKeysResponse, ListAPIKeysParams | void>({
			query: (params) => {
				const search = new URLSearchParams();
				if (params) {
					if (params.search) search.set("search", params.search);
					if (params.status) search.set("status", params.status);
					if (typeof params.limit === "number") search.set("limit", String(params.limit));
					if (typeof params.offset === "number") search.set("offset", String(params.offset));
				}
				const qs = search.toString();
				return { url: `/governance/api-keys${qs ? `?${qs}` : ""}` };
			},
			providesTags: ["APIKeys"],
		}),

		getAPIKey: builder.query<APIKeyResponse, string>({
			query: (id) => ({ url: `/governance/api-keys/${encodeURIComponent(id)}` }),
			providesTags: ["APIKeys"],
		}),

		createAPIKey: builder.mutation<APIKeySecretResponse, CreateAPIKeyRequest>({
			query: (body) => ({
				url: "/governance/api-keys",
				method: "POST",
				body,
			}),
			invalidatesTags: ["APIKeys"],
		}),

		updateAPIKey: builder.mutation<APIKeyResponse, UpdateAPIKeyRequest>({
			query: ({ id, ...body }) => ({
				url: `/governance/api-keys/${encodeURIComponent(id)}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["APIKeys"],
		}),

		rotateAPIKey: builder.mutation<APIKeySecretResponse, string>({
			query: (id) => ({
				url: `/governance/api-keys/${encodeURIComponent(id)}/rotate`,
				method: "POST",
			}),
			invalidatesTags: ["APIKeys"],
		}),

		revokeAPIKey: builder.mutation<APIKeyResponse, string>({
			query: (id) => ({
				url: `/governance/api-keys/${encodeURIComponent(id)}/revoke`,
				method: "POST",
			}),
			invalidatesTags: ["APIKeys"],
		}),

		deleteAPIKey: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/api-keys/${encodeURIComponent(id)}`,
				method: "DELETE",
			}),
			invalidatesTags: ["APIKeys"],
		}),
	}),
});

export const {
	useGetAPIKeysQuery,
	useGetAPIKeyQuery,
	useCreateAPIKeyMutation,
	useUpdateAPIKeyMutation,
	useRotateAPIKeyMutation,
	useRevokeAPIKeyMutation,
	useDeleteAPIKeyMutation,
} = apiKeysApi;