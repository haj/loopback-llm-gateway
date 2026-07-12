import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
	getErrorMessage,
	KAFKA_CONNECTOR_PLUGIN,
	parseBrokers,
	selectKafkaConnectorForm,
	selectKafkaConnectorIsDirty,
	setKafkaConnectorForm,
	updateKafkaConnectorForm,
	useAppDispatch,
	useAppSelector,
	useGetPluginsQuery,
	useUpdatePluginMutation,
} from "@/lib/store";
import { Loader2, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

interface KafkaConnectorViewProps {
	onDelete?: () => void;
	isDeleting?: boolean;
}

// KafkaConnectorView is the real configuration form for the Loopback Gateway
// Kafka data connector. It edits the built-in "loopback-kafka" plugin: a list of
// bootstrap brokers, the destination topic, and an enable toggle. Completed
// request/response telemetry is streamed as JSON to the configured topic.
export default function KafkaConnectorView({ onDelete, isDeleting }: KafkaConnectorViewProps) {
	const dispatch = useAppDispatch();
	const form = useAppSelector(selectKafkaConnectorForm);
	const isDirty = useAppSelector(selectKafkaConnectorIsDirty);

	const { data: plugins, isLoading } = useGetPluginsQuery();
	const [updatePlugin] = useUpdatePluginMutation();
	const [isSaving, setIsSaving] = useState(false);

	// Hydrate the form from the loaded plugin config (if the connector already exists).
	useEffect(() => {
		const existing = plugins?.find((p) => p.name === KAFKA_CONNECTOR_PLUGIN);
		if (!existing) {
			return;
		}
		const cfg = (existing.config as { brokers?: string[]; topic?: string }) ?? {};
		dispatch(
			setKafkaConnectorForm({
				enabled: existing.enabled ?? false,
				brokers: Array.isArray(cfg.brokers) ? cfg.brokers.join("\n") : "",
				topic: cfg.topic ?? "",
			}),
		);
	}, [plugins, dispatch]);

	const brokers = parseBrokers(form.brokers);
	const canSave = isDirty && brokers.length > 0 && form.topic.trim().length > 0 && !isSaving;

	const handleSave = () => {
		setIsSaving(true);
		updatePlugin({
			name: KAFKA_CONNECTOR_PLUGIN,
			data: {
				enabled: form.enabled,
				config: {
					brokers,
					topic: form.topic.trim(),
				},
			},
		})
			.unwrap()
			.then(() => {
				toast.success("Kafka connector configuration saved");
			})
			.catch((err) => {
				toast.error("Failed to save Kafka connector configuration", {
					description: getErrorMessage(err),
				});
			})
			.finally(() => setIsSaving(false));
	};

	return (
		<Card data-testid="kafka-connector-form">
			<CardHeader>
				<div className="flex items-center justify-between gap-4">
					<div className="space-y-1">
						<CardTitle className="flex items-center gap-2">
							<img src="/images/kafka-logo.svg" alt="Kafka" width={24} height={24} />
							Kafka data connector
						</CardTitle>
						<CardDescription>
							Stream completed request/response telemetry as JSON to a Kafka topic for real-time analytics, alerting, and downstream
							processing.
						</CardDescription>
					</div>
					<div className="flex items-center gap-2">
						<Label htmlFor="kafka-enabled" className="text-muted-foreground text-xs">
							Enabled
						</Label>
						<Switch
							id="kafka-enabled"
							checked={form.enabled}
							disabled={isLoading}
							onCheckedChange={(checked) => dispatch(updateKafkaConnectorForm({ enabled: checked }))}
						/>
					</div>
				</div>
			</CardHeader>
			<CardContent className="space-y-4">
				<div className="space-y-2">
					<Label htmlFor="kafka-brokers">Brokers</Label>
					<Textarea
						id="kafka-brokers"
						placeholder={"broker-1:9092\nbroker-2:9092"}
						value={form.brokers}
						disabled={isLoading}
						rows={3}
						onChange={(e) => dispatch(updateKafkaConnectorForm({ brokers: e.target.value }))}
					/>
					<p className="text-muted-foreground text-xs">
						One bootstrap broker per line (or comma-separated), e.g. <code>host:9092</code>.
					</p>
				</div>

				<div className="space-y-2">
					<Label htmlFor="kafka-topic">Topic</Label>
					<Input
						id="kafka-topic"
						placeholder="loopback.telemetry"
						value={form.topic}
						disabled={isLoading}
						onChange={(e) => dispatch(updateKafkaConnectorForm({ topic: e.target.value }))}
					/>
				</div>

				<div className="flex items-center justify-between pt-2">
					{onDelete ? (
						<Button variant="outline" type="button" onClick={onDelete} disabled={isDeleting}>
							{isDeleting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />}
							Remove connector
						</Button>
					) : (
						<span />
					)}
					<Button type="button" onClick={handleSave} disabled={!canSave}>
						{isSaving ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
						Save
					</Button>
				</div>
			</CardContent>
		</Card>
	);
}