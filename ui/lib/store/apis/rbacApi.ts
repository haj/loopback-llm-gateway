import {
	CreateRoleAssignmentRequest,
	CreateRoleRequest,
	EnforceRbacRequest,
	ListRoleAssignmentsParams,
	ListRoleAssignmentsResponse,
	ListRolesParams,
	ListRolesResponse,
	MyPermissionsResponse,
	RbacSetupStatusResponse,
	RoleAssignmentResponse,
	RoleResponse,
	UpdateRoleRequest,
} from "@/lib/types/rbac";
import { baseApi } from "./baseApi";

// RTK-query slice for RBAC roles and role assignments. Follows guardrailsApi's
// providesTags/invalidatesTags cache-coherence pattern using the shared "Roles",
// "Permissions" and "Users" tags declared in baseApi.
export const rbacApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getRoles: builder.query<ListRolesResponse, ListRolesParams | void>({
			query: (params) => {
				const search = new URLSearchParams();
				if (params) {
					if (params.search) search.set("search", params.search);
					if (typeof params.limit === "number") search.set("limit", String(params.limit));
					if (typeof params.offset === "number") search.set("offset", String(params.offset));
				}
				const qs = search.toString();
				return { url: `/governance/roles${qs ? `?${qs}` : ""}` };
			},
			providesTags: ["Roles"],
		}),

		getRole: builder.query<RoleResponse, string>({
			query: (id) => ({ url: `/governance/roles/${encodeURIComponent(id)}` }),
			providesTags: ["Roles"],
		}),

		createRole: builder.mutation<RoleResponse, CreateRoleRequest>({
			query: (body) => ({
				url: "/governance/roles",
				method: "POST",
				body,
			}),
			invalidatesTags: ["Roles", "Permissions"],
		}),

		updateRole: builder.mutation<RoleResponse, UpdateRoleRequest>({
			query: ({ id, ...body }) => ({
				url: `/governance/roles/${encodeURIComponent(id)}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["Roles", "Permissions"],
		}),

		deleteRole: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/roles/${encodeURIComponent(id)}`,
				method: "DELETE",
			}),
			invalidatesTags: ["Roles", "Permissions"],
		}),

		getRoleAssignments: builder.query<ListRoleAssignmentsResponse, ListRoleAssignmentsParams | void>({
			query: (params) => {
				const search = new URLSearchParams();
				if (params) {
					if (params.user_id) search.set("user_id", params.user_id);
					if (params.role_id) search.set("role_id", params.role_id);
					if (typeof params.limit === "number") search.set("limit", String(params.limit));
					if (typeof params.offset === "number") search.set("offset", String(params.offset));
				}
				const qs = search.toString();
				return { url: `/governance/role-assignments${qs ? `?${qs}` : ""}` };
			},
			providesTags: ["Roles"],
		}),

		createRoleAssignment: builder.mutation<RoleAssignmentResponse, CreateRoleAssignmentRequest>({
			query: (body) => ({
				url: "/governance/role-assignments",
				method: "POST",
				body,
			}),
			invalidatesTags: ["Roles", "Permissions"],
		}),

		deleteRoleAssignment: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/role-assignments/${encodeURIComponent(id)}`,
				method: "DELETE",
			}),
			invalidatesTags: ["Roles", "Permissions"],
		}),

		getMyPermissions: builder.query<MyPermissionsResponse, void>({
			query: () => ({ url: "/governance/rbac/permissions/me" }),
			providesTags: ["Permissions"],
		}),

		getRbacSetupStatus: builder.query<RbacSetupStatusResponse, void>({
			query: () => ({ url: "/governance/rbac/setup-status" }),
			providesTags: ["Permissions"],
		}),

		enforceRbac: builder.mutation<{ message: string; enforcing: boolean }, EnforceRbacRequest>({
			query: (body) => ({
				url: "/governance/rbac/enforce",
				method: "POST",
				body,
			}),
			// Invalidating "Permissions" re-fetches both the setup status and
			// /permissions/me, so rbacContext reflects enforcement live.
			invalidatesTags: ["Roles", "Permissions"],
		}),
	}),
});

export const {
	useGetRolesQuery,
	useGetRoleQuery,
	useCreateRoleMutation,
	useUpdateRoleMutation,
	useDeleteRoleMutation,
	useGetRoleAssignmentsQuery,
	useCreateRoleAssignmentMutation,
	useDeleteRoleAssignmentMutation,
	useGetMyPermissionsQuery,
	useGetRbacSetupStatusQuery,
	useEnforceRbacMutation,
} = rbacApi;