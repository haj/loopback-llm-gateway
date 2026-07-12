import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { getErrorMessage } from "@/lib/store";
import { useGetGuardrailsQuery } from "@/lib/store/apis/guardrailsApi";
import { GUARDRAIL_DEFINITIONS, GuardrailType } from "@/lib/types/guardrails";

// guardrailsProviderView lists the built-in guardrail "providers" — the 15
// guardrail types shipped by the Loopback Gateway guard plugin — and shows how
// many configs currently reference each. It is the catalog companion to the
// configuration view (where configs are created and enabled).
export default function GuardrailsProviderView() {
	const { data, isLoading, isError, error } = useGetGuardrailsQuery();

	// Count enabled usages of each guardrail type across all configs.
	const usage: Record<string, { active: number; total: number }> = {};
	for (const config of data?.guardrails ?? []) {
		for (const g of config.guardrails) {
			const entry = (usage[g.type] ??= { active: 0, total: 0 });
			entry.total += 1;
			if (g.enabled && config.enabled) entry.active += 1;
		}
	}

	const textProviders = GUARDRAIL_DEFINITIONS.filter((d) => d.target === "text");
	const requestProviders = GUARDRAIL_DEFINITIONS.filter((d) => d.target === "request");

	return (
		<div className="w-full space-y-6">
			<div>
				<h2 className="text-lg font-semibold tracking-tight">Guardrail providers</h2>
				<p className="text-muted-foreground text-sm">
					Built-in guardrail checks available in the Loopback Gateway guard plugin. Add and enable them from the Configuration tab.
				</p>
			</div>

			{isLoading && <p className="text-muted-foreground text-sm">Loading guardrail usage...</p>}
			{isError && <p className="text-sm text-red-500">Failed to load guardrails: {getErrorMessage(error)}</p>}

			<ProviderSection title="Text guardrails" subtitle="Inspect the request message content." definitions={textProviders} usage={usage} />
			<ProviderSection
				title="Request guardrails"
				subtitle="Inspect the request envelope (model, request type)."
				definitions={requestProviders}
				usage={usage}
			/>
		</div>
	);
}

interface ProviderSectionProps {
	title: string;
	subtitle: string;
	definitions: typeof GUARDRAIL_DEFINITIONS;
	usage: Record<string, { active: number; total: number }>;
}

function ProviderSection({ title, subtitle, definitions, usage }: ProviderSectionProps) {
	return (
		<div className="space-y-3">
			<div>
				<h3 className="text-sm font-semibold">{title}</h3>
				<p className="text-muted-foreground text-xs">{subtitle}</p>
			</div>
			<div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
				{definitions.map((d) => (
					<ProviderCard key={d.type} type={d.type} label={d.label} description={d.description} usage={usage[d.type]} />
				))}
			</div>
		</div>
	);
}

interface ProviderCardProps {
	type: GuardrailType;
	label: string;
	description: string;
	usage?: { active: number; total: number };
}

function ProviderCard({ type, label, description, usage }: ProviderCardProps) {
	const active = usage?.active ?? 0;
	return (
		<Card>
			<CardHeader className="pb-2">
				<div className="flex items-center justify-between gap-2">
					<CardTitle className="text-sm">{label}</CardTitle>
					<Badge variant={active > 0 ? "secondary" : "outline"} className="text-xs">
						{active > 0 ? `${active} active` : "unused"}
					</Badge>
				</div>
				<CardDescription className="font-mono text-xs">{type}</CardDescription>
			</CardHeader>
			<CardContent>
				<p className="text-muted-foreground text-xs">{description}</p>
			</CardContent>
		</Card>
	);
}