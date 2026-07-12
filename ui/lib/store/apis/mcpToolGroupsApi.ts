import {
	CreateMCPToolGroupRequest,
	ListMCPToolGroupsParams,
	ListMCPToolGroupsResponse,
	MCPToolGroupResponse,
	UpdateMCPToolGroupRequest,
} from "@/lib/types/mcpToolGroups";
import { baseApi } from "./baseApi";

// RTK-query slice for MCP tool groups. Follows guardrailsApi's
// providesTags/invalidatesTags cache-coherence pattern using the shared
// "MCPToolGroups" tag declared in baseApi.
export const mcpToolGroupsApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getMCPToolGroups: builder.query<ListMCPToolGroupsResponse, ListMCPToolGroupsParams | void>({
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
				return { url: `/governance/mcp-tool-groups${qs ? `?${qs}` : ""}` };
			},
			providesTags: ["MCPToolGroups"],
		}),

		getMCPToolGroup: builder.query<MCPToolGroupResponse, string>({
			query: (id) => ({ url: `/governance/mcp-tool-groups/${encodeURIComponent(id)}` }),
			providesTags: ["MCPToolGroups"],
		}),

		createMCPToolGroup: builder.mutation<MCPToolGroupResponse, CreateMCPToolGroupRequest>({
			query: (body) => ({
				url: "/governance/mcp-tool-groups",
				method: "POST",
				body,
			}),
			invalidatesTags: ["MCPToolGroups"],
		}),

		updateMCPToolGroup: builder.mutation<MCPToolGroupResponse, UpdateMCPToolGroupRequest>({
			query: ({ id, ...body }) => ({
				url: `/governance/mcp-tool-groups/${encodeURIComponent(id)}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["MCPToolGroups"],
		}),

		deleteMCPToolGroup: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/mcp-tool-groups/${encodeURIComponent(id)}`,
				method: "DELETE",
			}),
			invalidatesTags: ["MCPToolGroups"],
		}),
	}),
});

export const {
	useGetMCPToolGroupsQuery,
	useGetMCPToolGroupQuery,
	useCreateMCPToolGroupMutation,
	useUpdateMCPToolGroupMutation,
	useDeleteMCPToolGroupMutation,
} = mcpToolGroupsApi;