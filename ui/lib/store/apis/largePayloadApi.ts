import { LargePayloadConfig } from "@enterprise/lib/types/largePayload";
import { baseApi } from "./baseApi";

// Large-payload streaming configuration endpoints.
//
// These talk to the open-source Loopback Gateway backend at
// `/api/large-payload-config` (GET/PUT). The base query already prefixes
// requests with `/api`, so the URLs here are relative to that.
export const largePayloadApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getLargePayloadConfig: builder.query<LargePayloadConfig, void>({
			query: () => ({
				url: "/large-payload-config",
			}),
			providesTags: ["LargePayloadConfig"],
		}),
		updateLargePayloadConfig: builder.mutation<{ status: string; message: string }, LargePayloadConfig>({
			query: (data) => ({
				url: "/large-payload-config",
				method: "PUT",
				body: data,
			}),
			invalidatesTags: ["LargePayloadConfig"],
		}),
	}),
});

export const { useGetLargePayloadConfigQuery, useUpdateLargePayloadConfigMutation } = largePayloadApi;