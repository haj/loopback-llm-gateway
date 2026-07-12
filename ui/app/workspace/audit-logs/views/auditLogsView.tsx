import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getErrorMessage } from "@/lib/store";
import { useGetAuditLogsQuery } from "@/lib/store/apis/auditLogsApi";
import { AuditLog, AuditOutcome } from "@/lib/types/audit";
import { useMemo, useState } from "react";

// ACTIONS lists the audit action identifiers emitted by the governance mutation
// points (see transports/bifrost-http/handlers/audit.go). Used to populate the
// action filter; the "" value clears the filter.
// "all" is the Radix Select sentinel for "no filter"; empty-string item values
// are not allowed. It is mapped to an unset filter before querying.
const ACTIONS: { value: string; label: string }[] = [
	{ value: "all", label: "All actions" },
	{ value: "virtual_key.create", label: "Virtual key created" },
	{ value: "virtual_key.update", label: "Virtual key updated" },
	{ value: "virtual_key.delete", label: "Virtual key deleted" },
	{ value: "team.create", label: "Team created" },
	{ value: "team.update", label: "Team updated" },
	{ value: "team.delete", label: "Team deleted" },
	{ value: "customer.create", label: "Customer created" },
	{ value: "customer.update", label: "Customer updated" },
	{ value: "customer.delete", label: "Customer deleted" },
	{ value: "user.create", label: "User created" },
	{ value: "user.update", label: "User updated" },
	{ value: "user.delete", label: "User deleted" },
	{ value: "guardrail.create", label: "Guardrail created" },
	{ value: "guardrail.update", label: "Guardrail updated" },
	{ value: "guardrail.delete", label: "Guardrail deleted" },
	{ value: "pii_rule.create", label: "PII rule created" },
	{ value: "pii_rule.update", label: "PII rule updated" },
	{ value: "pii_rule.delete", label: "PII rule deleted" },
	{ value: "mcp_tool_group.create", label: "MCP tool group created" },
	{ value: "mcp_tool_group.update", label: "MCP tool group updated" },
	{ value: "mcp_tool_group.delete", label: "MCP tool group deleted" },
	{ value: "prompt_deployment.create", label: "Prompt deployment created" },
	{ value: "prompt_deployment.update", label: "Prompt deployment updated" },
	{ value: "prompt_deployment.delete", label: "Prompt deployment deleted" },
	{ value: "circuit_breaker.create", label: "Circuit breaker created" },
	{ value: "circuit_breaker.update", label: "Circuit breaker updated" },
	{ value: "circuit_breaker.delete", label: "Circuit breaker deleted" },
	{ value: "circuit_breaker.reset", label: "Circuit breaker reset" },
	{ value: "vault.refresh", label: "Vault refreshed" },
	{ value: "vault.test_connection", label: "Vault connection tested" },
	{ value: "audit_settings.update", label: "Audit settings updated" },
	{ value: "audit_log.prune", label: "Audit log pruned" },
];

const OUTCOMES: { value: string; label: string }[] = [
	{ value: "all", label: "All outcomes" },
	{ value: "success", label: "Success" },
	{ value: "failure", label: "Failure" },
];

function outcomeVariant(outcome: AuditOutcome): "success" | "destructive" {
	return outcome === "success" ? "success" : "destructive";
}

function formatTimestamp(ts: string): string {
	const d = new Date(ts);
	if (Number.isNaN(d.getTime())) {
		return ts;
	}
	return d.toLocaleString();
}

function actionLabel(action: string): string {
	return ACTIONS.find((a) => a.value === action)?.label ?? action;
}

export default function AuditLogsView() {
	const [search, setSearch] = useState("");
	const [action, setAction] = useState("");
	const [outcome, setOutcome] = useState("");

	const params = useMemo(
		() => ({
			search: search.trim() || undefined,
			action: action || undefined,
			outcome: (outcome || undefined) as AuditOutcome | undefined,
			limit: 200,
		}),
		[search, action, outcome],
	);

	const { data, isLoading, isError, error, refetch, isFetching } = useGetAuditLogsQuery(params);
	const logs: AuditLog[] = data?.audit_logs ?? [];

	function clearFilters() {
		setSearch("");
		setAction("");
		setOutcome("");
	}

	const hasFilters = !!(search || action || outcome);

	return (
		<div className="w-full space-y-4">
			<div className="flex items-start justify-between gap-4">
				<div>
					<h2 className="text-lg font-semibold tracking-tight">Audit logs</h2>
					<p className="text-muted-foreground text-sm">
						Tamper-evident trail of governance changes recorded by the Loopback Gateway. Each entry is HMAC-signed at the time it is
						written.
					</p>
				</div>
				<Button variant="outline" onClick={() => refetch()} disabled={isFetching} data-testid="refresh-audit-logs">
					Refresh
				</Button>
			</div>

			<div className="flex flex-wrap items-end gap-3">
				<div className="flex flex-col gap-1">
					<Label htmlFor="audit-search" className="text-xs">
						Search
					</Label>
					<Input
						id="audit-search"
						placeholder="Action, actor, or target"
						value={search}
						onChange={(e) => setSearch(e.target.value)}
						className="w-64"
						data-testid="audit-search-input"
					/>
				</div>
				<div className="flex flex-col gap-1">
					<Label className="text-xs">Action</Label>
					<Select value={action || "all"} onValueChange={(v) => setAction(v === "all" ? "" : v)}>
						<SelectTrigger className="w-56" data-testid="audit-action-filter">
							<SelectValue placeholder="All actions" />
						</SelectTrigger>
						<SelectContent>
							{ACTIONS.map((a) => (
								<SelectItem key={a.value} value={a.value}>
									{a.label}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
				<div className="flex flex-col gap-1">
					<Label className="text-xs">Outcome</Label>
					<Select value={outcome || "all"} onValueChange={(v) => setOutcome(v === "all" ? "" : v)}>
						<SelectTrigger className="w-40" data-testid="audit-outcome-filter">
							<SelectValue placeholder="All outcomes" />
						</SelectTrigger>
						<SelectContent>
							{OUTCOMES.map((o) => (
								<SelectItem key={o.value} value={o.value}>
									{o.label}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
				{hasFilters && (
					<Button variant="ghost" size="sm" onClick={clearFilters}>
						Clear filters
					</Button>
				)}
			</div>

			{isLoading && <p className="text-muted-foreground text-sm">Loading audit logs...</p>}
			{isError && <p className="text-sm text-red-500">Failed to load audit logs: {getErrorMessage(error)}</p>}

			{!isLoading && !isError && (
				<div className="overflow-auto rounded-sm border">
					<Table data-testid="audit-logs-table">
						<TableHeader>
							<TableRow className="bg-muted/50">
								<TableHead className="font-semibold">Time</TableHead>
								<TableHead className="font-semibold">Action</TableHead>
								<TableHead className="font-semibold">Outcome</TableHead>
								<TableHead className="font-semibold">Actor</TableHead>
								<TableHead className="font-semibold">Target</TableHead>
								<TableHead className="font-semibold">IP</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{logs.length === 0 ? (
								<TableRow data-testid="audit-logs-empty-state">
									<TableCell colSpan={6} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">
											{hasFilters ? "No audit logs match the current filters." : "No audit logs recorded yet."}
										</span>
									</TableCell>
								</TableRow>
							) : (
								logs.map((log) => (
									<TableRow key={log.id} className="hover:bg-muted/50 transition-colors">
										<TableCell className="align-top text-xs whitespace-nowrap">{formatTimestamp(log.timestamp)}</TableCell>
										<TableCell className="align-top">
											<Badge variant="outline" className="font-mono text-xs">
												{actionLabel(log.action)}
											</Badge>
										</TableCell>
										<TableCell className="align-top">
											<Badge variant={outcomeVariant(log.outcome)} className="capitalize">
												{log.outcome}
											</Badge>
										</TableCell>
										<TableCell className="align-top text-sm">{log.actor}</TableCell>
										<TableCell className="text-muted-foreground align-top font-mono text-xs break-all">{log.target || "—"}</TableCell>
										<TableCell className="text-muted-foreground align-top font-mono text-xs">{log.ip || "—"}</TableCell>
									</TableRow>
								))
							)}
						</TableBody>
					</Table>
				</div>
			)}

			{!isLoading && !isError && data && (
				<p className="text-muted-foreground text-xs">
					Showing {logs.length} of {data.total} {data.total === 1 ? "entry" : "entries"}.
				</p>
			)}
		</div>
	);
}