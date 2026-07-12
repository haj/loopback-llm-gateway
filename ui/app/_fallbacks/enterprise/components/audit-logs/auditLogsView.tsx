// The audit-logs UI is now part of the open-source Loopback Gateway build. The
// @enterprise alias resolves here in OSS builds, so we re-export the real
// workspace view (a filterable table over the append-only audit trail) instead
// of an upsell stub.
export { default } from "@/app/workspace/audit-logs/views/auditLogsView";