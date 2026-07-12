// Types for the Loopback Gateway audit-logs UI. These mirror the backend
// transports/bifrost-http/handlers/audit.go contract and the configstore
// TableAuditLog shape. The audit log is append-only and tamper-evident: each
// row carries an HMAC signature computed by the gateway.

// AuditOutcome mirrors the backend outcome enum.
export type AuditOutcome = "success" | "failure";

// AuditLog is a single recorded governance mutation.
export interface AuditLog {
	id: string;
	action: string;
	outcome: AuditOutcome;
	actor: string;
	ip: string;
	target: string;
	timestamp: string;
	signature: string;
}

export interface ListAuditLogsResponse {
	audit_logs: AuditLog[];
	total: number;
	count: number;
	offset: number;
}

export interface ListAuditLogsParams {
	action?: string;
	outcome?: AuditOutcome | "";
	actor?: string;
	target?: string;
	search?: string;
	limit?: number;
	offset?: number;
}

// AuditExportType mirrors the backend export destination enum.
export type AuditExportType = "file" | "syslog";

// AuditLogSettings is the singleton retention/export configuration row
// (backend TableAuditLogSettings). Zero retention values mean unlimited;
// export_enabled=false is the default-off state.
export interface AuditLogSettings {
	id: string;
	retention_max_age_days: number;
	retention_max_rows: number;
	export_enabled: boolean;
	export_type: AuditExportType | "";
	export_file_path: string;
	syslog_network: "" | "udp" | "tcp";
	syslog_address: string;
	syslog_tag: string;
	created_at: string;
	updated_at: string;
}

export interface GetAuditLogSettingsResponse {
	settings: AuditLogSettings;
}

// UpdateAuditLogSettingsRequest is a partial PUT: omitted fields are left
// unchanged by the backend.
export interface UpdateAuditLogSettingsRequest {
	retention_max_age_days?: number;
	retention_max_rows?: number;
	export_enabled?: boolean;
	export_type?: string;
	export_file_path?: string;
	syslog_network?: string;
	syslog_address?: string;
	syslog_tag?: string;
}

export interface PruneAuditLogsResponse {
	message: string;
	deleted: number;
	deleted_by_age: number;
	deleted_by_rows: number;
	prune_marker_id: string;
	signature_digest: string;
}

export interface VerifyAuditLogsResponse {
	checked: number;
	valid: number;
	invalid: number;
	invalid_ids: string[];
}