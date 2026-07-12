import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { getErrorMessage } from "@/lib/store";
import { useGetVaultStatusQuery, useRefreshVaultMutation, useTestVaultConnectionMutation } from "@/lib/store/apis/vaultApi";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { AlertTriangle, Loader2, RefreshCw, ShieldCheck, Unplug } from "lucide-react";
import { type ReactNode, useCallback } from "react";
import { toast } from "sonner";

const ACCESS_MODE_LABELS: Record<string, string> = {
	read_only: "Read only",
	read_and_write: "Read and write",
};

const BACKEND_LABELS: Record<string, string> = {
	"hashicorp-vault": "HashiCorp Vault (KV v2)",
	"aws-secrets-manager": "AWS Secrets Manager",
	"gcp-secret-manager": "GCP Secret Manager",
};

function formatTimestamp(iso?: string): string {
	if (!iso) return "Never";
	const date = new Date(iso);
	return Number.isNaN(date.getTime()) ? iso : date.toLocaleString();
}

function StatusRow({ label, value, testId }: { label: string; value: ReactNode; testId?: string }) {
	return (
		<div className="flex items-center justify-between gap-4 py-1.5">
			<span className="text-muted-foreground text-sm">{label}</span>
			<span className="text-sm font-medium" data-testid={testId}>
				{value}
			</span>
		</div>
	);
}

export default function VaultView() {
	const hasSettingsUpdateAccess = useRbac(RbacResource.Settings, RbacOperation.Update);
	const { data: status, isLoading, error } = useGetVaultStatusQuery();
	const [refreshVault, { isLoading: isRefreshing }] = useRefreshVaultMutation();
	const [testConnection, { isLoading: isTesting }] = useTestVaultConnectionMutation();

	const handleRefresh = useCallback(async () => {
		try {
			const result = await refreshVault().unwrap();
			if (result.errors.length > 0) {
				toast.error(`Refresh finished with ${result.errors.length} error(s): ${result.errors[0]}`);
				return;
			}
			if (result.secrets_updated > 0) {
				toast.success(
					`Rotated ${result.secrets_updated} secret(s) across ${result.providers_updated.length} provider(s) — no restart needed.`,
				);
			} else {
				toast.success(`Rechecked ${result.keys_rechecked} key(s); all secrets are up to date.`);
			}
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	}, [refreshVault]);

	const handleTestConnection = useCallback(async () => {
		try {
			await testConnection().unwrap();
			toast.success("Vault connection successful.");
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	}, [testConnection]);

	return (
		<div className="mx-auto h-[calc(100vh-50px)] w-full max-w-4xl space-y-4 overflow-y-auto">
			<div>
				<h2 className="text-lg font-semibold tracking-tight">Vault / Secret Manager</h2>
				<p className="text-muted-foreground text-sm">
					Resolve provider credentials from an external secret manager and rotate them without a restart.
				</p>
			</div>

			{isLoading ? (
				<div className="flex items-center justify-center rounded-sm border p-8" data-testid="vault-status-loading">
					<Loader2 className="text-muted-foreground h-5 w-5 animate-spin" aria-hidden />
					<span className="sr-only">Loading vault status</span>
				</div>
			) : error ? (
				<Alert variant="destructive" data-testid="vault-status-error">
					<AlertTriangle className="h-4 w-4" />
					<AlertDescription>Could not load vault status. {getErrorMessage(error)}</AlertDescription>
				</Alert>
			) : !status?.enabled ? (
				<Alert data-testid="vault-disabled-alert">
					<ShieldCheck className="h-4 w-4" />
					<AlertDescription>
						Vault integration is disabled. It is configured exclusively in <b>config.json</b> (or Helm values) under{" "}
						<code>config_store.vault_store</code> — never in the dashboard — because the config store itself may hold vault references. Set{" "}
						<code>enabled</code>, <code>type</code> (e.g. <code>hashicorp-vault</code>) and the backend credentials, then restart the
						gateway. Provider keys stored as <code>vault.&lt;path&gt;</code> references resolve at runtime; with{" "}
						<code>access_mode: read_and_write</code>, new plaintext keys are pushed to the vault automatically.
					</AlertDescription>
				</Alert>
			) : (
				<div className="space-y-4">
					<div className="space-y-2 rounded-sm border p-4">
						<div className="flex items-center justify-between">
							<div className="space-y-0.5">
								<span className="text-sm font-medium">Backend Status</span>
								<p className="text-muted-foreground text-sm">Live connection state of the configured secret manager.</p>
							</div>
							<Badge variant={status.healthy ? "default" : "destructive"} data-testid="vault-status-badge">
								{status.healthy ? "Healthy" : "Unreachable"}
							</Badge>
						</div>
						{!status.healthy && status.health_error && (
							<Alert variant="destructive" data-testid="vault-health-error-alert">
								<AlertTriangle className="h-4 w-4" />
								<AlertDescription>{status.health_error}</AlertDescription>
							</Alert>
						)}
						<div className="divide-y">
							<StatusRow label="Backend" value={BACKEND_LABELS[status.type ?? ""] ?? status.type} testId="vault-backend-type" />
							<StatusRow label="Path prefix" value={<code>{status.prefix}</code>} testId="vault-prefix" />
							<StatusRow
								label="Access mode"
								value={ACCESS_MODE_LABELS[status.access_mode ?? ""] ?? status.access_mode}
								testId="vault-access-mode"
							/>
							<StatusRow
								label="Background sync"
								value={status.sync_interval ? `Every ${status.sync_interval}` : "Manual refresh only"}
								testId="vault-sync-interval"
							/>
							<StatusRow
								label="Secret cache"
								value={`${status.cache?.entries ?? 0} entrie(s), TTL ${status.cache?.ttl_seconds ?? 0}s`}
								testId="vault-cache-stats"
							/>
							<StatusRow label="Last refresh" value={formatTimestamp(status.last_refresh)} testId="vault-last-refresh" />
						</div>
						{(status.last_refresh_errors?.length ?? 0) > 0 && (
							<Alert variant="destructive" data-testid="vault-refresh-errors-alert">
								<AlertTriangle className="h-4 w-4" />
								<AlertDescription>
									Last refresh reported {status.last_refresh_errors!.length} error(s). Affected keys keep their last-good value.
									<ul className="mt-1 list-disc pl-4">
										{status.last_refresh_errors!.slice(0, 5).map((message) => (
											<li key={message} className="break-all">
												{message}
											</li>
										))}
									</ul>
								</AlertDescription>
							</Alert>
						)}
					</div>

					<div className="flex items-center justify-between gap-4 rounded-sm border p-4">
						<div className="space-y-0.5">
							<span className="text-sm font-medium">Rotate secrets now</span>
							<p className="text-muted-foreground text-sm">
								Re-resolve every vault-backed provider key and hot-swap changed credentials into the running gateway — no restart.
							</p>
						</div>
						<div className="flex shrink-0 gap-2">
							<Button
								variant="outline"
								onClick={handleTestConnection}
								disabled={isTesting || !hasSettingsUpdateAccess}
								data-testid="vault-test-connection-button"
							>
								{isTesting ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden /> : <Unplug className="h-4 w-4" aria-hidden />}
								Test Connection
							</Button>
							<Button onClick={handleRefresh} disabled={isRefreshing || !hasSettingsUpdateAccess} data-testid="vault-refresh-button">
								{isRefreshing ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden /> : <RefreshCw className="h-4 w-4" aria-hidden />}
								Refresh Now
							</Button>
						</div>
					</div>

					<p className="text-muted-foreground text-xs">
						Backend credentials are managed in <code>config.json</code> (<code>config_store.vault_store</code>) and cannot be edited here.
					</p>
				</div>
			)}
		</div>
	);
}