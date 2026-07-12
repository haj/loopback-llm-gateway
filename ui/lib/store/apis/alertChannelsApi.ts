import { AlertChannel, AlertChannelInput, TestFireResult } from "@/lib/types/alerting";
import { baseApi } from "./baseApi";

// Alert channel endpoints (model on circuitBreakerApi). These talk to the
// open-source Loopback Gateway backend at `/api/alerting/channels`; the base
// query already prefixes `/api`. Channels persist in the config store and are
// reloaded into the live framework/alerting dispatcher on every mutation, so
// changes apply without a restart. Secrets are write-only.
export const alertChannelsApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getAlertChannels: builder.query<{ channels: AlertChannel[]; count: number }, void>({
			query: () => ({ url: "/alerting/channels" }),
			providesTags: ["AlertChannels"],
		}),
		createAlertChannel: builder.mutation<{ message: string; channel: AlertChannel }, AlertChannelInput>({
			query: (data) => ({
				url: "/alerting/channels",
				method: "POST",
				body: data,
			}),
			invalidatesTags: ["AlertChannels"],
		}),
		updateAlertChannel: builder.mutation<{ message: string; channel: AlertChannel }, { id: string; body: AlertChannelInput }>({
			query: ({ id, body }) => ({
				url: `/alerting/channels/${id}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["AlertChannels"],
		}),
		deleteAlertChannel: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/alerting/channels/${id}`,
				method: "DELETE",
			}),
			invalidatesTags: ["AlertChannels"],
		}),
		testAlertChannel: builder.mutation<TestFireResult, string>({
			query: (id) => ({
				url: `/alerting/channels/${id}/test`,
				method: "POST",
			}),
			// The test-fire stamps last_attempt_at/last_status on the channel row.
			invalidatesTags: ["AlertChannels"],
		}),
	}),
});

export const {
	useGetAlertChannelsQuery,
	useCreateAlertChannelMutation,
	useUpdateAlertChannelMutation,
	useDeleteAlertChannelMutation,
	useTestAlertChannelMutation,
} = alertChannelsApi;