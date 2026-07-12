import {
	GetAuditLogSettingsResponse,
	ListAuditLogsParams,
	ListAuditLogsResponse,
	PruneAuditLogsResponse,
	UpdateAuditLogSettingsRequest,
	VerifyAuditLogsResponse,
} from "@/lib/types/audit";
import { baseApi } from "./baseApi";

// RTK-query slice for the append-only audit log. The trail itself is written
// by the gateway's recordAudit helper at governance mutation points, so there
// are no row-level create/update/delete endpoints — the mutations here manage
// the retention/export settings singleton and trigger maintenance operations
// (prune, verify). Follows guardrailsApi's providesTags cache-coherence
// pattern using the shared "AuditLogs" / "AuditLogSettings" tags.
export const auditLogsApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getAuditLogs: builder.query<ListAuditLogsResponse, ListAuditLogsParams | void>({
			query: (params) => {
				const search = new URLSearchParams();
				if (params) {
					if (params.action) search.set("action", params.action);
					if (params.outcome) search.set("outcome", params.outcome);
					if (params.actor) search.set("actor", params.actor);
					if (params.target) search.set("target", params.target);
					if (params.search) search.set("search", params.search);
					if (typeof params.limit === "number") search.set("limit", String(params.limit));
					if (typeof params.offset === "number") search.set("offset", String(params.offset));
				}
				const qs = search.toString();
				return { url: `/governance/audit-logs${qs ? `?${qs}` : ""}` };
			},
			providesTags: ["AuditLogs"],
		}),
		getAuditLogSettings: builder.query<GetAuditLogSettingsResponse, void>({
			query: () => ({ url: "/governance/audit-logs/settings" }),
			providesTags: ["AuditLogSettings"],
		}),
		updateAuditLogSettings: builder.mutation<GetAuditLogSettingsResponse, UpdateAuditLogSettingsRequest>({
			query: (body) => ({
				url: "/governance/audit-logs/settings",
				method: "PUT",
				body,
			}),
			// The update itself is recorded in the trail (audit_settings.update),
			// so the log list must refresh too.
			invalidatesTags: ["AuditLogSettings", "AuditLogs"],
		}),
		pruneAuditLogs: builder.mutation<PruneAuditLogsResponse, void>({
			query: () => ({
				url: "/governance/audit-logs/prune",
				method: "POST",
			}),
			invalidatesTags: ["AuditLogs"],
		}),
		verifyAuditLogs: builder.query<VerifyAuditLogsResponse, void>({
			query: () => ({ url: "/governance/audit-logs/verify" }),
		}),
	}),
});

export const {
	useGetAuditLogsQuery,
	useGetAuditLogSettingsQuery,
	useUpdateAuditLogSettingsMutation,
	usePruneAuditLogsMutation,
	useLazyVerifyAuditLogsQuery,
} = auditLogsApi;