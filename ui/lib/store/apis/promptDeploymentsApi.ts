import {
	CreatePromptDeploymentRequest,
	DeletePromptDeploymentResponse,
	GetPromptDeploymentResponse,
	ListPromptDeploymentsResponse,
	PromptDeploymentMutationResponse,
	UpdatePromptDeploymentRequest,
} from "@/lib/types/prompts";
import { baseApi } from "./baseApi";

// RTK-query slice for prompt deployments. Follows guardrailsApi's
// providesTags/invalidatesTags cache-coherence pattern using the shared
// "PromptDeployments" tag declared in baseApi. Mutations are keyed by the owning
// prompt so a deployment change only refetches that prompt's deployment list.
export const promptDeploymentsApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getPromptDeployments: builder.query<ListPromptDeploymentsResponse, string>({
			query: (promptId) => ({ url: `/prompt-repo/prompts/${encodeURIComponent(promptId)}/deployments` }),
			providesTags: (result, error, promptId) => [{ type: "PromptDeployments", id: promptId }],
		}),

		getPromptDeployment: builder.query<GetPromptDeploymentResponse, string>({
			query: (id) => ({ url: `/prompt-repo/deployments/${encodeURIComponent(id)}` }),
			providesTags: (result, error, id) => [{ type: "PromptDeployments", id }],
		}),

		createPromptDeployment: builder.mutation<PromptDeploymentMutationResponse, CreatePromptDeploymentRequest>({
			query: ({ promptId, ...body }) => ({
				url: `/prompt-repo/prompts/${encodeURIComponent(promptId)}/deployments`,
				method: "POST",
				body,
			}),
			invalidatesTags: (result, error, { promptId }) => [{ type: "PromptDeployments", id: promptId }],
		}),

		updatePromptDeployment: builder.mutation<PromptDeploymentMutationResponse, UpdatePromptDeploymentRequest>({
			query: ({ id, promptId: _promptId, ...body }) => ({
				url: `/prompt-repo/deployments/${encodeURIComponent(id)}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: (result, error, { id, promptId }) => [
				{ type: "PromptDeployments", id },
				{ type: "PromptDeployments", id: promptId },
			],
		}),

		deletePromptDeployment: builder.mutation<DeletePromptDeploymentResponse, { id: string; promptId: string }>({
			query: ({ id }) => ({
				url: `/prompt-repo/deployments/${encodeURIComponent(id)}`,
				method: "DELETE",
			}),
			invalidatesTags: (result, error, { id, promptId }) => [
				{ type: "PromptDeployments", id },
				{ type: "PromptDeployments", id: promptId },
			],
		}),
	}),
});

export const {
	useGetPromptDeploymentsQuery,
	useGetPromptDeploymentQuery,
	useCreatePromptDeploymentMutation,
	useUpdatePromptDeploymentMutation,
	useDeletePromptDeploymentMutation,
} = promptDeploymentsApi;