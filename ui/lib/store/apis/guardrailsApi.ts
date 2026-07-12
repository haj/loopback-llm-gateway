import {
	CreateGuardrailRequest,
	GuardrailConfigResponse,
	ListGuardrailsParams,
	ListGuardrailsResponse,
	UpdateGuardrailRequest,
} from "@/lib/types/guardrails";
import { baseApi } from "./baseApi";

// RTK-query slice for guardrail configs. Follows governanceApi's
// providesTags/invalidatesTags cache-coherence pattern using the shared
// "Guardrails" tag declared in baseApi.
export const guardrailsApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getGuardrails: builder.query<ListGuardrailsResponse, ListGuardrailsParams | void>({
			query: (params) => {
				const search = new URLSearchParams();
				if (params) {
					if (params.scope) search.set("scope", params.scope);
					if (params.scope_id) search.set("scope_id", params.scope_id);
					if (params.search) search.set("search", params.search);
					if (typeof params.limit === "number") search.set("limit", String(params.limit));
					if (typeof params.offset === "number") search.set("offset", String(params.offset));
				}
				const qs = search.toString();
				return { url: `/governance/guardrails${qs ? `?${qs}` : ""}` };
			},
			providesTags: ["Guardrails"],
		}),

		getGuardrail: builder.query<GuardrailConfigResponse, string>({
			query: (id) => ({ url: `/governance/guardrails/${encodeURIComponent(id)}` }),
			providesTags: ["Guardrails"],
		}),

		createGuardrail: builder.mutation<GuardrailConfigResponse, CreateGuardrailRequest>({
			query: (body) => ({
				url: "/governance/guardrails",
				method: "POST",
				body,
			}),
			invalidatesTags: ["Guardrails"],
		}),

		updateGuardrail: builder.mutation<GuardrailConfigResponse, UpdateGuardrailRequest>({
			query: ({ id, ...body }) => ({
				url: `/governance/guardrails/${encodeURIComponent(id)}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["Guardrails"],
		}),

		deleteGuardrail: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/guardrails/${encodeURIComponent(id)}`,
				method: "DELETE",
			}),
			invalidatesTags: ["Guardrails"],
		}),
	}),
});

export const {
	useGetGuardrailsQuery,
	useGetGuardrailQuery,
	useCreateGuardrailMutation,
	useUpdateGuardrailMutation,
	useDeleteGuardrailMutation,
} = guardrailsApi;