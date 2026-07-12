import {
	CreatePIIRuleRequest,
	ListPIIRulesParams,
	ListPIIRulesResponse,
	PIIRuleResponse,
	TestPIIRuleRequest,
	TestPIIRuleResponse,
	UpdatePIIRuleRequest,
} from "@/lib/types/piiRules";
import { baseApi } from "./baseApi";

// RTK-query slice for PII redactor rules. Follows guardrailsApi's
// providesTags/invalidatesTags cache-coherence pattern using the shared
// "PiiRules" tag declared in baseApi.
export const piiRulesApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getPIIRules: builder.query<ListPIIRulesResponse, ListPIIRulesParams | void>({
			query: (params) => {
				const search = new URLSearchParams();
				if (params) {
					if (params.scope) search.set("scope", params.scope);
					if (params.scope_id) search.set("scope_id", params.scope_id);
					if (params.type) search.set("type", params.type);
					if (params.search) search.set("search", params.search);
					if (typeof params.limit === "number") search.set("limit", String(params.limit));
					if (typeof params.offset === "number") search.set("offset", String(params.offset));
				}
				const qs = search.toString();
				return { url: `/governance/pii-rules${qs ? `?${qs}` : ""}` };
			},
			providesTags: ["PiiRules"],
		}),

		getPIIRule: builder.query<PIIRuleResponse, string>({
			query: (id) => ({ url: `/governance/pii-rules/${encodeURIComponent(id)}` }),
			providesTags: ["PiiRules"],
		}),

		createPIIRule: builder.mutation<PIIRuleResponse, CreatePIIRuleRequest>({
			query: (body) => ({
				url: "/governance/pii-rules",
				method: "POST",
				body,
			}),
			invalidatesTags: ["PiiRules"],
		}),

		updatePIIRule: builder.mutation<PIIRuleResponse, UpdatePIIRuleRequest>({
			query: ({ id, ...body }) => ({
				url: `/governance/pii-rules/${encodeURIComponent(id)}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["PiiRules"],
		}),

		deletePIIRule: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/pii-rules/${encodeURIComponent(id)}`,
				method: "DELETE",
			}),
			invalidatesTags: ["PiiRules"],
		}),

		testPIIRule: builder.mutation<TestPIIRuleResponse, TestPIIRuleRequest>({
			query: ({ id, text }) => ({
				url: `/governance/pii-rules/${encodeURIComponent(id)}/test`,
				method: "POST",
				body: { text },
			}),
		}),
	}),
});

export const {
	useGetPIIRulesQuery,
	useGetPIIRuleQuery,
	useCreatePIIRuleMutation,
	useUpdatePIIRuleMutation,
	useDeletePIIRuleMutation,
	useTestPIIRuleMutation,
} = piiRulesApi;