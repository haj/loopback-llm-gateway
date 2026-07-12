// Alert channels management view.
//
// Alert channels are now part of the open-source Loopback Gateway build, so
// this replaces the previous upsell stub with a real management UI backed by
// the RTK-query slice in @/lib/store/apis/alertChannelsApi. It lists channels
// (Slack / PagerDuty / generic webhook), supports create / edit /
// enable-toggle / delete, per-event-type filtering, and a per-row synchronous
// test-fire that surfaces the delivery result as a toast.
//
// The feature is OPT-IN / default-off on the backend: with zero channel rows
// the alerting dispatcher's Publish is a no-op. Creating a channel here is
// what arms it. Channel secrets are write-only — the API reports only
// has_secret, and an edit that leaves the secret blank preserves the stored
// value.
import { useState } from "react";
import { Pencil, Plus, Send, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getErrorMessage } from "@/lib/store";
import {
	useCreateAlertChannelMutation,
	useDeleteAlertChannelMutation,
	useGetAlertChannelsQuery,
	useTestAlertChannelMutation,
	useUpdateAlertChannelMutation,
} from "@/lib/store/apis/alertChannelsApi";
import { ALERT_EVENT_TYPES, AlertChannel, AlertChannelInput, AlertChannelType, AlertEventType } from "@/lib/types/alerting";

const CHANNEL_TYPES: { value: AlertChannelType; label: string }[] = [
	{ value: "slack", label: "Slack" },
	{ value: "pagerduty", label: "PagerDuty" },
	{ value: "webhook", label: "Webhook" },
];

interface ChannelFormState {
	name: string;
	type: AlertChannelType;
	endpoint_url: string;
	secret: string;
	event_types: AlertEventType[];
}

const emptyForm: ChannelFormState = {
	name: "",
	type: "slack",
	endpoint_url: "",
	secret: "",
	event_types: [],
};

function eventTypeLabel(value: string): string {
	return ALERT_EVENT_TYPES.find((e) => e.value === value)?.label ?? value;
}

function lastStatusBadge(channel: AlertChannel) {
	if (!channel.last_status) {
		return <span className="text-muted-foreground text-sm">Never fired</span>;
	}
	const variant = channel.last_status === "ok" ? "success" : "destructive";
	return (
		<Badge variant={variant} title={channel.last_error || undefined} data-testid={`alert-channel-status-${channel.id}`}>
			{channel.last_status === "ok" ? "Delivered" : "Failed"}
		</Badge>
	);
}

export default function AlertChannelsView() {
	const { data, isLoading, isError, error } = useGetAlertChannelsQuery();
	const [createChannel, { isLoading: isCreating }] = useCreateAlertChannelMutation();
	const [updateChannel, { isLoading: isUpdating }] = useUpdateAlertChannelMutation();
	const [deleteChannel] = useDeleteAlertChannelMutation();
	const [testChannel, { isLoading: isTesting }] = useTestAlertChannelMutation();

	// editingID === "" means the form is a create form; otherwise it edits.
	const [editingID, setEditingID] = useState("");
	const [showForm, setShowForm] = useState(false);
	const [form, setForm] = useState<ChannelFormState>({ ...emptyForm });

	const channels = data?.channels ?? [];

	const startCreate = () => {
		setEditingID("");
		setForm({ ...emptyForm });
		setShowForm(true);
	};

	const startEdit = (channel: AlertChannel) => {
		setEditingID(channel.id);
		setForm({
			name: channel.name,
			type: channel.type,
			endpoint_url: channel.endpoint_url,
			secret: "", // write-only: blank means "keep the stored secret"
			event_types: channel.event_types ?? [],
		});
		setShowForm(true);
	};

	const toggleEventType = (value: AlertEventType, checked: boolean) => {
		setForm((f) => ({
			...f,
			event_types: checked ? [...f.event_types, value] : f.event_types.filter((e) => e !== value),
		}));
	};

	const handleSubmit = async () => {
		const body: AlertChannelInput = {
			name: form.name,
			type: form.type,
			endpoint_url: form.endpoint_url,
			event_types: form.event_types,
		};
		// Only send the secret when the admin typed one, so an untouched edit
		// preserves the stored value.
		if (form.secret !== "") {
			body.secret = form.secret;
		}
		try {
			if (editingID === "") {
				await createChannel(body).unwrap();
				toast.success("Alert channel created.");
			} else {
				await updateChannel({ id: editingID, body }).unwrap();
				toast.success("Alert channel updated.");
			}
			setShowForm(false);
			setForm({ ...emptyForm });
			setEditingID("");
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	const handleToggleEnabled = async (channel: AlertChannel, enabled: boolean) => {
		try {
			await updateChannel({ id: channel.id, body: { enabled } }).unwrap();
			toast.success(enabled ? "Alert channel enabled." : "Alert channel disabled.");
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	const handleDelete = async (channel: AlertChannel) => {
		try {
			await deleteChannel(channel.id).unwrap();
			toast.success(`Alert channel "${channel.name}" deleted.`);
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	const handleTest = async (channel: AlertChannel) => {
		try {
			const result = await testChannel(channel.id).unwrap();
			if (result.status === "ok") {
				toast.success(`Test alert delivered to "${channel.name}" (HTTP ${result.http_status}).`);
			} else {
				toast.error(`Test alert to "${channel.name}" failed: ${result.error}`);
			}
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	return (
		<div className="w-full space-y-4 p-1">
			<div className="flex items-start justify-between gap-4">
				<div>
					<h2 className="text-lg font-semibold tracking-tight">Alert Channels</h2>
					<p className="text-muted-foreground text-sm">
						Deliver governance mutations, budget and rate-limit violations, and circuit-breaker trips to Slack, PagerDuty, or a signed
						webhook. Repeats of the same condition are suppressed for five minutes per channel.
					</p>
				</div>
				<Button onClick={startCreate} data-testid="alert-channel-create-button">
					<Plus className="mr-1 h-4 w-4" /> Add channel
				</Button>
			</div>

			{showForm && (
				<Card data-testid="alert-channel-form">
					<CardHeader>
						<CardTitle className="text-base">{editingID === "" ? "New alert channel" : "Edit alert channel"}</CardTitle>
						<CardDescription>
							{form.type === "pagerduty"
								? "The secret is your PagerDuty Events v2 routing key. The endpoint URL is optional (defaults to the public PagerDuty API)."
								: form.type === "webhook"
									? "Events POST as JSON. With a secret set, deliveries carry an X-Loopback-Signature HMAC-SHA256 header."
									: "Paste your Slack incoming-webhook URL."}
						</CardDescription>
					</CardHeader>
					<CardContent className="space-y-4">
						<div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<div className="flex flex-col gap-1">
								<Label htmlFor="alert-channel-name">Name</Label>
								<Input
									id="alert-channel-name"
									value={form.name}
									onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
									placeholder="ops-alerts"
									data-testid="alert-channel-name-input"
								/>
							</div>
							<div className="flex flex-col gap-1">
								<Label>Type</Label>
								<Select value={form.type} onValueChange={(v) => setForm((f) => ({ ...f, type: v as AlertChannelType }))}>
									<SelectTrigger data-testid="alert-channel-type-select">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										{CHANNEL_TYPES.map((t) => (
											<SelectItem key={t.value} value={t.value}>
												{t.label}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							</div>
							<div className="flex flex-col gap-1">
								<Label htmlFor="alert-channel-endpoint">
									{form.type === "pagerduty" ? "Endpoint URL (optional override)" : "Endpoint URL"}
								</Label>
								<Input
									id="alert-channel-endpoint"
									value={form.endpoint_url}
									onChange={(e) => setForm((f) => ({ ...f, endpoint_url: e.target.value }))}
									placeholder={form.type === "slack" ? "https://hooks.slack.com/services/..." : "https://example.com/hook"}
									data-testid="alert-channel-endpoint-input"
								/>
							</div>
							<div className="flex flex-col gap-1">
								<Label htmlFor="alert-channel-secret">{form.type === "pagerduty" ? "Routing key" : "Signing secret (optional)"}</Label>
								<Input
									id="alert-channel-secret"
									type="password"
									value={form.secret}
									onChange={(e) => setForm((f) => ({ ...f, secret: e.target.value }))}
									placeholder={editingID !== "" ? "Leave blank to keep the current secret" : ""}
									data-testid="alert-channel-secret-input"
								/>
							</div>
						</div>
						<div className="flex flex-col gap-2">
							<Label>Event types (none selected = all events)</Label>
							<div className="flex flex-wrap gap-4">
								{ALERT_EVENT_TYPES.map((et) => (
									<label key={et.value} className="flex items-center gap-2 text-sm">
										<Checkbox
											checked={form.event_types.includes(et.value)}
											onCheckedChange={(checked) => toggleEventType(et.value, checked === true)}
											data-testid={`alert-channel-event-${et.value}`}
										/>
										{et.label}
									</label>
								))}
							</div>
						</div>
						<div className="flex gap-2">
							<Button onClick={handleSubmit} disabled={isCreating || isUpdating} data-testid="alert-channel-save-button">
								{editingID === "" ? "Create channel" : "Save changes"}
							</Button>
							<Button variant="ghost" onClick={() => setShowForm(false)}>
								Cancel
							</Button>
						</div>
					</CardContent>
				</Card>
			)}

			{isLoading && <p className="text-muted-foreground text-sm">Loading alert channels...</p>}
			{isError && <p className="text-sm text-red-500">Failed to load alert channels: {getErrorMessage(error)}</p>}

			{!isLoading && !isError && (
				<div className="overflow-auto rounded-sm border">
					<Table data-testid="alert-channels-table">
						<TableHeader>
							<TableRow className="bg-muted/50">
								<TableHead className="font-semibold">Name</TableHead>
								<TableHead className="font-semibold">Type</TableHead>
								<TableHead className="font-semibold">Events</TableHead>
								<TableHead className="font-semibold">Last delivery</TableHead>
								<TableHead className="font-semibold">Enabled</TableHead>
								<TableHead className="text-right font-semibold">Actions</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{channels.length === 0 ? (
								<TableRow data-testid="alert-channels-empty-state">
									<TableCell colSpan={6} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">No alert channels configured. Alerting is off until you add one.</span>
									</TableCell>
								</TableRow>
							) : (
								channels.map((channel) => (
									<TableRow key={channel.id} data-testid={`alert-channel-row-${channel.id}`}>
										<TableCell className="font-medium">{channel.name}</TableCell>
										<TableCell>
											<Badge variant="outline">{CHANNEL_TYPES.find((t) => t.value === channel.type)?.label ?? channel.type}</Badge>
										</TableCell>
										<TableCell>
											{!channel.event_types || channel.event_types.length === 0 ? (
												<span className="text-muted-foreground text-sm">All events</span>
											) : (
												<div className="flex flex-wrap gap-1">
													{channel.event_types.map((et) => (
														<Badge key={et} variant="secondary" className="text-xs">
															{eventTypeLabel(et)}
														</Badge>
													))}
												</div>
											)}
										</TableCell>
										<TableCell>{lastStatusBadge(channel)}</TableCell>
										<TableCell>
											<Switch
												checked={channel.enabled}
												onCheckedChange={(checked) => handleToggleEnabled(channel, checked)}
												data-testid={`alert-channel-enabled-switch-${channel.id}`}
											/>
										</TableCell>
										<TableCell className="text-right">
											<div className="flex justify-end gap-1">
												<Button
													variant="ghost"
													size="sm"
													onClick={() => handleTest(channel)}
													disabled={isTesting}
													title="Send test alert"
													data-testid={`alert-channel-test-button-${channel.id}`}
												>
													<Send className="h-4 w-4" />
												</Button>
												<Button
													variant="ghost"
													size="sm"
													onClick={() => startEdit(channel)}
													title="Edit"
													data-testid={`alert-channel-edit-button-${channel.id}`}
												>
													<Pencil className="h-4 w-4" />
												</Button>
												<Button
													variant="ghost"
													size="sm"
													onClick={() => handleDelete(channel)}
													title="Delete"
													data-testid={`alert-channel-delete-button-${channel.id}`}
												>
													<Trash2 className="h-4 w-4" />
												</Button>
											</div>
										</TableCell>
									</TableRow>
								))
							)}
						</TableBody>
					</Table>
				</div>
			)}
		</div>
	);
}