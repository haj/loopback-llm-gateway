import { Button } from "@/components/ui/button";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { getErrorMessage } from "@/lib/store";
import {
	useGetAuditLogSettingsQuery,
	useLazyVerifyAuditLogsQuery,
	usePruneAuditLogsMutation,
	useUpdateAuditLogSettingsMutation,
} from "@/lib/store/apis/auditLogsApi";
import { cn } from "@/lib/utils";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { zodResolver } from "@hookform/resolvers/zod";
import { useEffect } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

// Schema for the retention/export settings form. Mirrors the backend
// validation in handlers/audit.go (validateAuditLogSettings) so rejections
// surface inline instead of as a 400 round-trip.
const auditSettingsSchema = z
	.object({
		retention_max_age_days: z.number().int("Must be a whole number").min(0, "Must be zero (unlimited) or positive"),
		retention_max_rows: z.number().int("Must be a whole number").min(0, "Must be zero (unlimited) or positive"),
		export_enabled: z.boolean(),
		export_type: z.enum(["file", "syslog"]).or(z.literal("")),
		export_file_path: z.string(),
		syslog_network: z.enum(["", "udp", "tcp"]),
		syslog_address: z.string(),
		syslog_tag: z.string(),
	})
	.superRefine((data, ctx) => {
		if (!data.export_enabled) {
			return;
		}
		if (data.export_type === "") {
			ctx.addIssue({ code: "custom", path: ["export_type"], message: "Select a destination to enable export" });
		}
		if (data.export_type === "file" && data.export_file_path.trim() === "") {
			ctx.addIssue({ code: "custom", path: ["export_file_path"], message: "File path is required for file export" });
		}
		if (data.export_type === "syslog" && data.syslog_network !== "" && data.syslog_address.trim() === "") {
			ctx.addIssue({ code: "custom", path: ["syslog_address"], message: "Address is required for udp/tcp syslog" });
		}
	});

type AuditSettingsFormValues = z.infer<typeof auditSettingsSchema>;

const defaultValues: AuditSettingsFormValues = {
	retention_max_age_days: 0,
	retention_max_rows: 0,
	export_enabled: false,
	export_type: "",
	export_file_path: "",
	syslog_network: "",
	syslog_address: "",
	syslog_tag: "",
};

// AuditSettingsView is the retention/export settings card rendered above the
// audit-log table. Everything here is default-off: zero retention values mean
// unlimited, and export stays disabled until a destination is configured.
//
// The NDJSON download is the backfill path — the live export tail only covers
// events recorded while export is enabled, so pre-existing rows must be pulled
// through the download endpoint.
export default function AuditSettingsView() {
	const hasUpdateAccess = useRbac(RbacResource.AuditLogs, RbacOperation.Update);
	const { data } = useGetAuditLogSettingsQuery();
	const [updateSettings, { isLoading: isSaving }] = useUpdateAuditLogSettingsMutation();
	const [pruneAuditLogs, { isLoading: isPruning }] = usePruneAuditLogsMutation();
	const [verifyAuditLogs, { data: verifyResult, isFetching: isVerifying }] = useLazyVerifyAuditLogsQuery();

	const form = useForm<AuditSettingsFormValues>({
		resolver: zodResolver(auditSettingsSchema),
		mode: "onChange",
		defaultValues,
	});

	useEffect(() => {
		if (data?.settings) {
			form.reset({
				...defaultValues,
				retention_max_age_days: data.settings.retention_max_age_days,
				retention_max_rows: data.settings.retention_max_rows,
				export_enabled: data.settings.export_enabled,
				export_type: data.settings.export_type,
				export_file_path: data.settings.export_file_path,
				syslog_network: data.settings.syslog_network,
				syslog_address: data.settings.syslog_address,
				syslog_tag: data.settings.syslog_tag,
			});
		}
	}, [data, form]);

	const watchedExportEnabled = form.watch("export_enabled");
	const watchedExportType = form.watch("export_type");

	const onSubmit = async (values: AuditSettingsFormValues) => {
		try {
			await updateSettings(values).unwrap();
			toast.success("Audit log settings updated successfully.");
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const onPrune = async () => {
		try {
			const result = await pruneAuditLogs().unwrap();
			if (result.deleted === 0) {
				toast.info("Nothing to prune: no rows exceed the configured retention limits.");
			} else {
				toast.success(`Pruned ${result.deleted} audit log row(s); signed prune marker ${result.prune_marker_id} recorded.`);
			}
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const onVerify = async () => {
		try {
			const result = await verifyAuditLogs().unwrap();
			if (result.invalid === 0) {
				toast.success(`All ${result.checked} checked row(s) have valid signatures.`);
			} else {
				toast.error(`${result.invalid} of ${result.checked} checked row(s) failed signature verification.`);
			}
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	return (
		<div className="rounded-sm border p-4" data-testid="audit-settings-section">
			<div className="flex items-start justify-between gap-4">
				<div>
					<h3 className="text-base font-semibold tracking-tight">Retention & Export</h3>
					<p className="text-muted-foreground text-sm">
						Prune old audit rows on a schedule and stream new events to a file or syslog. Zero means unlimited; export is off until enabled.
						Deletions are anchored by signed prune-marker events.
					</p>
				</div>
				<div className="flex shrink-0 gap-2">
					<Button variant="outline" size="sm" onClick={onVerify} disabled={isVerifying} data-testid="audit-verify-button">
						{isVerifying ? "Verifying..." : "Verify signatures"}
					</Button>
					<Button variant="outline" size="sm" asChild data-testid="audit-export-download-button">
						<a href="/api/governance/audit-logs/export" download>
							Download NDJSON
						</a>
					</Button>
				</div>
			</div>
			{verifyResult && verifyResult.invalid > 0 && (
				<p className="text-destructive mt-2 text-sm" data-testid="audit-verify-invalid-summary">
					Tampered rows: {verifyResult.invalid_ids.join(", ")}
				</p>
			)}

			<Form {...form}>
				<form onSubmit={form.handleSubmit(onSubmit)} className="mt-4 space-y-4">
					<fieldset disabled={!hasUpdateAccess} className="space-y-4">
						<div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<FormField
								control={form.control}
								name="retention_max_age_days"
								render={({ field }) => (
									<FormItem>
										<FormLabel>Max age (days)</FormLabel>
										<FormControl>
											<Input
												type="number"
												min={0}
												data-testid="audit-retention-max-age-input"
												{...field}
												onChange={(e) => field.onChange(Number(e.target.value) || 0)}
											/>
										</FormControl>
										<FormMessage />
									</FormItem>
								)}
							/>
							<FormField
								control={form.control}
								name="retention_max_rows"
								render={({ field }) => (
									<FormItem>
										<FormLabel>Max rows</FormLabel>
										<FormControl>
											<Input
												type="number"
												min={0}
												data-testid="audit-retention-max-rows-input"
												{...field}
												onChange={(e) => field.onChange(Number(e.target.value) || 0)}
											/>
										</FormControl>
										<FormMessage />
									</FormItem>
								)}
							/>
						</div>

						<div className="flex items-center justify-between rounded-sm border p-3">
							<div className="space-y-0.5">
								<FormLabel className="text-sm font-medium">Live export</FormLabel>
								<p className="text-muted-foreground text-sm">
									Stream newly recorded audit events to the destination below. Rows written before enabling are only available via the
									NDJSON download.
								</p>
							</div>
							<FormField
								control={form.control}
								name="export_enabled"
								render={({ field }) => (
									<FormItem>
										<FormControl>
											<Switch checked={field.value} onCheckedChange={field.onChange} data-testid="audit-export-enabled-switch" />
										</FormControl>
									</FormItem>
								)}
							/>
						</div>

						<div className={cn("grid grid-cols-1 gap-4 sm:grid-cols-2", !watchedExportEnabled && "pointer-events-none opacity-50")}>
							<FormField
								control={form.control}
								name="export_type"
								render={({ field }) => (
									<FormItem>
										<FormLabel>Destination</FormLabel>
										<Select value={field.value} onValueChange={field.onChange}>
											<FormControl>
												<SelectTrigger data-testid="audit-export-type-select">
													<SelectValue placeholder="Select destination" />
												</SelectTrigger>
											</FormControl>
											<SelectContent>
												<SelectItem value="file">JSONL file</SelectItem>
												<SelectItem value="syslog">Syslog</SelectItem>
											</SelectContent>
										</Select>
										<FormMessage />
									</FormItem>
								)}
							/>
							{watchedExportType === "file" && (
								<FormField
									control={form.control}
									name="export_file_path"
									render={({ field }) => (
										<FormItem>
											<FormLabel>File path</FormLabel>
											<FormControl>
												<Input placeholder="/var/log/loopback/audit.jsonl" data-testid="audit-export-file-path-input" {...field} />
											</FormControl>
											<FormMessage />
										</FormItem>
									)}
								/>
							)}
							{watchedExportType === "syslog" && (
								<>
									<FormField
										control={form.control}
										name="syslog_network"
										render={({ field }) => (
											<FormItem>
												<FormLabel>Syslog network</FormLabel>
												<Select
													value={field.value === "" ? "local" : field.value}
													onValueChange={(v) => field.onChange(v === "local" ? "" : v)}
												>
													<FormControl>
														<SelectTrigger data-testid="audit-syslog-network-select">
															<SelectValue />
														</SelectTrigger>
													</FormControl>
													<SelectContent>
														<SelectItem value="local">Local socket</SelectItem>
														<SelectItem value="udp">UDP</SelectItem>
														<SelectItem value="tcp">TCP</SelectItem>
													</SelectContent>
												</Select>
												<FormMessage />
											</FormItem>
										)}
									/>
									<FormField
										control={form.control}
										name="syslog_address"
										render={({ field }) => (
											<FormItem>
												<FormLabel>Syslog address</FormLabel>
												<FormControl>
													<Input placeholder="logs.example.com:514" data-testid="audit-syslog-address-input" {...field} />
												</FormControl>
												<FormMessage />
											</FormItem>
										)}
									/>
									<FormField
										control={form.control}
										name="syslog_tag"
										render={({ field }) => (
											<FormItem>
												<FormLabel>Syslog tag</FormLabel>
												<FormControl>
													<Input placeholder="loopback-gateway-audit" data-testid="audit-syslog-tag-input" {...field} />
												</FormControl>
												<FormMessage />
											</FormItem>
										)}
									/>
								</>
							)}
						</div>

						<div className="flex gap-2">
							<Button type="submit" size="sm" disabled={isSaving} data-testid="audit-settings-save-button">
								{isSaving ? "Saving..." : "Save settings"}
							</Button>
							<Button type="button" variant="outline" size="sm" onClick={onPrune} disabled={isPruning} data-testid="audit-prune-now-button">
								{isPruning ? "Pruning..." : "Prune now"}
							</Button>
						</div>
					</fieldset>
				</form>
			</Form>
		</div>
	);
}