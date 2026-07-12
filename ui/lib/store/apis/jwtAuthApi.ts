import { JWTAuthConfig, JWTAuthConfigInput } from "@/lib/types/jwtAuth";
import { baseApi } from "./baseApi";

// JWT auth issuer-config endpoints (model on circuitBreakerApi). These talk
// to the open-source Loopback Gateway backend at `/api/governance/jwt-auth`;
// the base query already prefixes `/api`. Configs persist in the config store
// and are pushed into the live JWT middleware snapshot on every mutation, so
// changes apply without a restart.
export const jwtAuthApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getJWTAuthConfigs: builder.query<{ jwt_auth_configs: JWTAuthConfig[]; count: number }, void>({
			query: () => ({ url: "/governance/jwt-auth" }),
			providesTags: ["JWTAuthConfigs"],
		}),
		createJWTAuthConfig: builder.mutation<{ message: string; jwt_auth_config: JWTAuthConfig }, JWTAuthConfigInput>({
			query: (data) => ({
				url: "/governance/jwt-auth",
				method: "POST",
				body: data,
			}),
			invalidatesTags: ["JWTAuthConfigs"],
		}),
		updateJWTAuthConfig: builder.mutation<{ message: string; jwt_auth_config: JWTAuthConfig }, { id: string; body: JWTAuthConfigInput }>({
			query: ({ id, body }) => ({
				url: `/governance/jwt-auth/${id}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["JWTAuthConfigs"],
		}),
		deleteJWTAuthConfig: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/jwt-auth/${id}`,
				method: "DELETE",
			}),
			invalidatesTags: ["JWTAuthConfigs"],
		}),
	}),
});

export const { useGetJWTAuthConfigsQuery, useCreateJWTAuthConfigMutation, useUpdateJWTAuthConfigMutation, useDeleteJWTAuthConfigMutation } =
	jwtAuthApi;