import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { LargePayloadConfig } from "@enterprise/lib/types/largePayload";

export interface LargePayloadSettingsFragmentProps {
	config: LargePayloadConfig;
	onConfigChange: (config: LargePayloadConfig) => void;
	controlsDisabled: boolean;
}

const MIN_BYTES = 1024;

// Numeric byte fields exposed by the panel, in the order they should render.
const BYTE_FIELDS: {
	key: Exclude<keyof LargePayloadConfig, "enabled">;
	label: string;
	description: string;
	testId: string;
}[] = [
	{
		key: "request_threshold_bytes",
		label: "Request Threshold",
		description: "Request bodies larger than this switch to streaming mode instead of being buffered in memory.",
		testId: "large-payload-request-threshold-input",
	},
	{
		key: "response_threshold_bytes",
		label: "Response Threshold",
		description: "Response bodies larger than this are streamed straight through to the client.",
		testId: "large-payload-response-threshold-input",
	},
	{
		key: "prefetch_size_bytes",
		label: "Prefetch Size",
		description: "How many bytes are read ahead while streaming a large payload.",
		testId: "large-payload-prefetch-size-input",
	},
	{
		key: "max_payload_bytes",
		label: "Max Payload Size",
		description: "Hard upper bound on an accepted payload. Must be at least as large as the request and response thresholds.",
		testId: "large-payload-max-payload-input",
	},
	{
		key: "truncated_log_bytes",
		label: "Truncated Log Size",
		description: "Maximum number of payload bytes written to logs before truncation.",
		testId: "large-payload-truncated-log-input",
	},
];

// Render a byte count as a compact human-readable hint (e.g. "10 MB").
function formatBytes(bytes: number): string {
	if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
	const units = ["B", "KB", "MB", "GB", "TB"];
	let value = bytes;
	let unitIndex = 0;
	while (value >= 1024 && unitIndex < units.length - 1) {
		value /= 1024;
		unitIndex += 1;
	}
	const rounded = Number.isInteger(value) ? value : Math.round(value * 100) / 100;
	return `${rounded} ${units[unitIndex]}`;
}

export default function LargePayloadSettingsFragment({ config, onConfigChange, controlsDisabled }: LargePayloadSettingsFragmentProps) {
	const handleFieldChange = (key: keyof LargePayloadConfig, value: number) => {
		onConfigChange({ ...config, [key]: value });
	};

	return (
		<div className="space-y-4">
			<div>
				<h3 className="text-lg font-semibold tracking-tight">Large Payload Streaming</h3>
				<p className="text-muted-foreground text-sm">
					Stream oversized request and response bodies instead of buffering them in memory. Changes require a restart to take effect.
				</p>
			</div>

			{/* Enable toggle */}
			<div className="flex items-center justify-between space-x-2">
				<div className="space-y-0.5">
					<label htmlFor="large-payload-enabled" className="text-sm font-medium">
						Enable Large Payload Streaming
					</label>
					<p className="text-muted-foreground text-sm">
						When enabled, payloads above the configured thresholds are streamed through Loopback Gateway rather than fully buffered.
					</p>
				</div>
				<Switch
					id="large-payload-enabled"
					data-testid="large-payload-enabled-switch"
					size="md"
					checked={config.enabled}
					onCheckedChange={(checked) => onConfigChange({ ...config, enabled: checked })}
					disabled={controlsDisabled}
				/>
			</div>

			{config.enabled && (
				<div className="space-y-4 border-l-2 pl-4">
					{BYTE_FIELDS.map((field) => {
						const value = config[field.key];
						return (
							<div key={field.key} className="flex items-center justify-between space-x-2">
								<div className="space-y-0.5">
									<label htmlFor={`large-payload-${field.key}`} className="text-sm font-medium">
										{field.label} (bytes)
									</label>
									<p className="text-muted-foreground text-sm">
										{field.description} Currently <b>{formatBytes(value)}</b>.
									</p>
								</div>
								<Input
									id={`large-payload-${field.key}`}
									data-testid={field.testId}
									type="number"
									min={MIN_BYTES}
									step={MIN_BYTES}
									className="w-40"
									value={value}
									onChange={(e) => handleFieldChange(field.key, parseInt(e.target.value, 10) || 0)}
									disabled={controlsDisabled}
								/>
							</div>
						);
					})}
				</div>
			)}
		</div>
	);
}