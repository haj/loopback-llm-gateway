import AuditSettingsView from "@/app/workspace/audit-logs/views/auditSettingsView";
import AuditLogsView from "@enterprise/components/audit-logs/auditLogsView";

export default function AuditLogsPage() {
	return (
		<div className="no-padding-parent mx-auto flex h-[calc(100dvh-1rem)] w-full flex-col space-y-4 overflow-y-auto p-4">
			<AuditSettingsView />
			<AuditLogsView />
		</div>
	);
}