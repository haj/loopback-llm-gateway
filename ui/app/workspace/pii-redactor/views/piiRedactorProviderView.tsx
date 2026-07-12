import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { getErrorMessage } from "@/lib/store";
import { useGetPIIRulesQuery } from "@/lib/store/apis/piiRulesApi";
import { PII_RULE_TYPES, PIIRuleType } from "@/lib/types/piiRules";

// PiiRedactorProviderView lists the two PII redaction "providers" shipped by the
// Loopback Gateway guard plugin — the in-process regex Redactor and the
// self-hosted Microsoft Presidio analyzer — and shows how many rules currently
// reference each. It is the catalog companion to the rules view (where rules are
// created and enabled).
export default function PiiRedactorProviderView() {
	const { data, isLoading, isError, error } = useGetPIIRulesQuery();

	// Count enabled rules per provider type.
	const usage: Record<string, { active: number; total: number }> = {};
	for (const rule of data?.pii_rules ?? []) {
		const entry = (usage[rule.type] ??= { active: 0, total: 0 });
		entry.total += 1;
		if (rule.enabled) entry.active += 1;
	}

	return (
		<div className="w-full space-y-6">
			<div>
				<h2 className="text-lg font-semibold tracking-tight">PII redactor providers</h2>
				<p className="text-muted-foreground text-sm">
					Redaction backends available in the Loopback Gateway guard plugin. Add and enable rules from the Rules tab.
				</p>
			</div>

			{isLoading && <p className="text-muted-foreground text-sm">Loading rule usage...</p>}
			{isError && <p className="text-sm text-red-500">Failed to load PII rules: {getErrorMessage(error)}</p>}

			<div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
				{PII_RULE_TYPES.map((t) => (
					<ProviderCard key={t.value} type={t.value} label={t.label} description={t.description} usage={usage[t.value]} />
				))}
			</div>
		</div>
	);
}

interface ProviderCardProps {
	type: PIIRuleType;
	label: string;
	description: string;
	usage?: { active: number; total: number };
}

function ProviderCard({ type, label, description, usage }: ProviderCardProps) {
	const active = usage?.active ?? 0;
	const total = usage?.total ?? 0;
	return (
		<Card>
			<CardHeader className="pb-2">
				<div className="flex items-center justify-between gap-2">
					<CardTitle className="text-sm">{label}</CardTitle>
					<Badge variant={active > 0 ? "secondary" : "outline"} className="text-xs">
						{active > 0 ? `${active} active` : total > 0 ? `${total} disabled` : "unused"}
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