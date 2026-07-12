import {
	BusinessUnitResponse,
	CreateBusinessUnitRequest,
	CreateUserRequest,
	ListBusinessUnitsParams,
	ListBusinessUnitsResponse,
	ListUsersParams,
	ListUsersResponse,
	UpdateBusinessUnitRequest,
	UpdateUserRequest,
	UserResponse,
} from "@/lib/types/userManagement";
import { baseApi } from "./baseApi";

// RTK-query slice for users and business units. Follows guardrailsApi's
// providesTags/invalidatesTags cache-coherence pattern using the shared "Users"
// and "BusinessUnits" tags declared in baseApi. Mutating a user invalidates
// BusinessUnits too so business-unit user counts stay fresh.
export const userManagementApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getUsers: builder.query<ListUsersResponse, ListUsersParams | void>({
			query: (params) => {
				const search = new URLSearchParams();
				if (params) {
					if (params.search) search.set("search", params.search);
					if (params.status) search.set("status", params.status);
					if (params.business_unit_id) search.set("business_unit_id", params.business_unit_id);
					if (typeof params.limit === "number") search.set("limit", String(params.limit));
					if (typeof params.offset === "number") search.set("offset", String(params.offset));
				}
				const qs = search.toString();
				return { url: `/governance/users${qs ? `?${qs}` : ""}` };
			},
			providesTags: ["Users"],
		}),

		getUser: builder.query<UserResponse, string>({
			query: (id) => ({ url: `/governance/users/${encodeURIComponent(id)}` }),
			providesTags: ["Users"],
		}),

		createUser: builder.mutation<UserResponse, CreateUserRequest>({
			query: (body) => ({
				url: "/governance/users",
				method: "POST",
				body,
			}),
			invalidatesTags: ["Users", "BusinessUnits"],
		}),

		updateUser: builder.mutation<UserResponse, UpdateUserRequest>({
			query: ({ id, ...body }) => ({
				url: `/governance/users/${encodeURIComponent(id)}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["Users", "BusinessUnits"],
		}),

		deleteUser: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/users/${encodeURIComponent(id)}`,
				method: "DELETE",
			}),
			invalidatesTags: ["Users", "BusinessUnits"],
		}),

		getBusinessUnits: builder.query<ListBusinessUnitsResponse, ListBusinessUnitsParams | void>({
			query: (params) => {
				const search = new URLSearchParams();
				if (params) {
					if (params.search) search.set("search", params.search);
					if (typeof params.limit === "number") search.set("limit", String(params.limit));
					if (typeof params.offset === "number") search.set("offset", String(params.offset));
				}
				const qs = search.toString();
				return { url: `/governance/business-units${qs ? `?${qs}` : ""}` };
			},
			providesTags: ["BusinessUnits"],
		}),

		getBusinessUnit: builder.query<BusinessUnitResponse, string>({
			query: (id) => ({ url: `/governance/business-units/${encodeURIComponent(id)}` }),
			providesTags: ["BusinessUnits"],
		}),

		createBusinessUnit: builder.mutation<BusinessUnitResponse, CreateBusinessUnitRequest>({
			query: (body) => ({
				url: "/governance/business-units",
				method: "POST",
				body,
			}),
			invalidatesTags: ["BusinessUnits"],
		}),

		updateBusinessUnit: builder.mutation<BusinessUnitResponse, UpdateBusinessUnitRequest>({
			query: ({ id, ...body }) => ({
				url: `/governance/business-units/${encodeURIComponent(id)}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["BusinessUnits"],
		}),

		deleteBusinessUnit: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/business-units/${encodeURIComponent(id)}`,
				method: "DELETE",
			}),
			invalidatesTags: ["BusinessUnits", "Users"],
		}),
	}),
});

export const {
	useGetUsersQuery,
	useGetUserQuery,
	useCreateUserMutation,
	useUpdateUserMutation,
	useDeleteUserMutation,
	useGetBusinessUnitsQuery,
	useGetBusinessUnitQuery,
	useCreateBusinessUnitMutation,
	useUpdateBusinessUnitMutation,
	useDeleteBusinessUnitMutation,
} = userManagementApi;