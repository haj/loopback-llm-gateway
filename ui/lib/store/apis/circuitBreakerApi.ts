import {
	CircuitBreakerPolicy,
	CircuitBreakerStateEntry,
	CreateCircuitBreakerPolicy,
	UpdateCircuitBreakerPolicy,
} from "@enterprise/lib/types/circuitBreaker";
import { baseApi } from "./baseApi";

// Per-provider circuit breaker endpoints.
//
// These talk to the open-source Loopback Gateway backend at
// `/api/governance/circuit-breakers`. The base query already prefixes requests
// with `/api`, so the URLs here are relative to that. Policies persist in the
// config store and are pushed into the live core engine on every mutation;
// `/state` returns the engine's in-memory closed/open/half-open view.
export const circuitBreakerApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getCircuitBreakers: builder.query<{ circuit_breakers: CircuitBreakerPolicy[]; count: number }, void>({
			query: () => ({ url: "/governance/circuit-breakers" }),
			providesTags: ["CircuitBreakerPolicies"],
		}),
		getCircuitBreakerState: builder.query<{ states: CircuitBreakerStateEntry[]; count: number }, void>({
			query: () => ({ url: "/governance/circuit-breakers/state" }),
			providesTags: ["CircuitBreakerState"],
		}),
		createCircuitBreaker: builder.mutation<{ message: string; circuit_breaker: CircuitBreakerPolicy }, CreateCircuitBreakerPolicy>({
			query: (data) => ({
				url: "/governance/circuit-breakers",
				method: "POST",
				body: data,
			}),
			invalidatesTags: ["CircuitBreakerPolicies", "CircuitBreakerState"],
		}),
		updateCircuitBreaker: builder.mutation<
			{ message: string; circuit_breaker: CircuitBreakerPolicy },
			{ id: string; body: UpdateCircuitBreakerPolicy }
		>({
			query: ({ id, body }) => ({
				url: `/governance/circuit-breakers/${id}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["CircuitBreakerPolicies", "CircuitBreakerState"],
		}),
		deleteCircuitBreaker: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/circuit-breakers/${id}`,
				method: "DELETE",
			}),
			invalidatesTags: ["CircuitBreakerPolicies", "CircuitBreakerState"],
		}),
		resetCircuitBreaker: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/circuit-breakers/${id}/reset`,
				method: "POST",
			}),
			invalidatesTags: ["CircuitBreakerState"],
		}),
	}),
});

export const {
	useGetCircuitBreakersQuery,
	useGetCircuitBreakerStateQuery,
	useCreateCircuitBreakerMutation,
	useUpdateCircuitBreakerMutation,
	useDeleteCircuitBreakerMutation,
	useResetCircuitBreakerMutation,
} = circuitBreakerApi;