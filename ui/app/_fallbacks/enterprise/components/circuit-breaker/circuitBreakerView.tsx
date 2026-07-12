// Per-provider circuit breaker management view.
//
// The circuit breaker is now part of the open-source Loopback Gateway build, so
// this replaces the previous upsell stub with a real management UI backed by the
// RTK-query slice in @/lib/store/apis/circuitBreakerApi. It lists per-provider
// policies, shows the live engine state (closed/open/half-open), and supports
// create / edit / enable-toggle / delete / manual reset.
//
// The feature is OPT-IN / default-off on the backend: with no policy rows the
// engine hot path is a complete no-op. Creating a policy here is what arms it.
import { useMemo, useState } from "react";
import { Activity, Plus, RotateCcw, Trash2, Zap } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getErrorMessage } from "@/lib/store";
import {
	useCreateCircuitBreakerMutation,
	useDeleteCircuitBreakerMutation,
	useGetCircuitBreakersQuery,
	useGetCircuitBreakerStateQuery,
	useResetCircuitBreakerMutation,
	useUpdateCircuitBreakerMutation,
} from "@/lib/store/apis/circuitBreakerApi";
import {
	CircuitBreakerStateEntry,
	CircuitState,
	CreateCircuitBreakerPolicy,
	DefaultCircuitBreakerPolicy,
} from "@enterprise/lib/types/circuitBreaker";

function stateBadgeVariant(state: CircuitState): "success" | "destructive" | "warning" {
	switch (state) {
		case "open":
			return "destructive";
		case "half_open":
			return "warning";
		default:
			return "success";
	}
}

function stateLabel(state: CircuitState): string {
	switch (state) {
		case "open":
			return "Open";
		case "half_open":
			return "Half-open";
		default:
			return "Closed";
	}
}

export default function CircuitBreakerView() {
	const { data: policiesData, isLoading, isError, error } = useGetCircuitBreakersQuery();
	// Poll the live engine state every few seconds so trips/recoveries surface.
	const { data: stateData } = useGetCircuitBreakerStateQuery(undefined, { pollingInterval: 5000 });

	const [createPolicy, { isLoading: isCreating }] = useCreateCircuitBreakerMutation();
	const [updatePolicy] = useUpdateCircuitBreakerMutation();
	const [deletePolicy] = useDeleteCircuitBreakerMutation();
	const [resetPolicy] = useResetCircuitBreakerMutation();

	const [form, setForm] = useState<CreateCircuitBreakerPolicy>({ ...DefaultCircuitBreakerPolicy });

	const policies = policiesData?.circuit_breakers ?? [];
	const stateByProvider = useMemo(() => {
		const map = new Map<string, CircuitBreakerStateEntry>();
		for (const s of stateData?.states ?? []) {
			map.set(s.provider, s);
		}
		return map;
	}, [stateData]);

	const handleCreate = async () => {
		if (!form.provider.trim()) {
			toast.error("Provider is required");
			return;
		}
		try {
			await createPolicy({
				provider: form.provider.trim(),
				enabled: form.enabled,
				failure_threshold: form.failure_threshold,
				cooldown_seconds: form.cooldown_seconds,
				half_open_probes: form.half_open_probes,
			}).unwrap();
			toast.success(`Circuit breaker created for ${form.provider.trim()}`);
			setForm({ ...DefaultCircuitBreakerPolicy });
		} catch (e) {
			toast.error(getErrorMessage(e));
		}
	};

	const handleToggle = async (id: string, enabled: boolean) => {
		try {
			await updatePolicy({ id, body: { enabled } }).unwrap();
			toast.success(enabled ? "Circuit breaker enabled" : "Circuit breaker disabled");
		} catch (e) {
			toast.error(getErrorMessage(e));
		}
	};

	const handleDelete = async (id: string, provider: string) => {
		try {
			await deletePolicy(id).unwrap();
			toast.success(`Circuit breaker for ${provider} deleted`);
		} catch (e) {
			toast.error(getErrorMessage(e));
		}
	};

	const handleReset = async (id: string, provider: string) => {
		try {
			await resetPolicy(id).unwrap();
			toast.success(`Circuit breaker for ${provider} reset to closed`);
		} catch (e) {
			toast.error(getErrorMessage(e));
		}
	};

	return (
		<div className="space-y-6 py-4">
			<div className="flex items-center gap-3">
				<Zap className="h-6 w-6" strokeWidth={1.5} />
				<div>
					<h1 className="text-xl font-semibold tracking-tight">Circuit Breaker</h1>
					<p className="text-muted-foreground text-sm">
						Trip traffic away from a failing provider to your fallback chain. Per-provider, per-instance, and opt-in: a provider with no
						policy here behaves exactly as before.
					</p>
				</div>
			</div>

			{/* Create policy */}
			<Card>
				<CardHeader>
					<CardTitle className="text-base">Add provider policy</CardTitle>
					<CardDescription>
						The breaker trips OPEN after the failure threshold, fails fast to fallbacks for the cooldown, then admits a limited number of
						half-open probes to test recovery.
					</CardDescription>
				</CardHeader>
				<CardContent>
					<div className="flex flex-wrap items-end gap-4">
						<div className="space-y-1">
							<Label htmlFor="cb-provider">Provider</Label>
							<Input
								id="cb-provider"
								className="w-44"
								placeholder="e.g. openai"
								value={form.provider}
								onChange={(e) => setForm({ ...form, provider: e.target.value })}
							/>
						</div>
						<div className="space-y-1">
							<Label htmlFor="cb-threshold">Failure threshold</Label>
							<Input
								id="cb-threshold"
								type="number"
								min={1}
								className="w-36"
								value={form.failure_threshold}
								onChange={(e) => setForm({ ...form, failure_threshold: parseInt(e.target.value, 10) || 0 })}
							/>
						</div>
						<div className="space-y-1">
							<Label htmlFor="cb-cooldown">Cooldown (seconds)</Label>
							<Input
								id="cb-cooldown"
								type="number"
								min={1}
								className="w-36"
								value={form.cooldown_seconds}
								onChange={(e) => setForm({ ...form, cooldown_seconds: parseInt(e.target.value, 10) || 0 })}
							/>
						</div>
						<div className="space-y-1">
							<Label htmlFor="cb-probes">Half-open probes</Label>
							<Input
								id="cb-probes"
								type="number"
								min={1}
								className="w-36"
								value={form.half_open_probes}
								onChange={(e) => setForm({ ...form, half_open_probes: parseInt(e.target.value, 10) || 0 })}
							/>
						</div>
						<div className="flex items-center gap-2 pb-2">
							<Switch
								id="cb-enabled"
								checked={form.enabled ?? true}
								onCheckedChange={(checked) => setForm({ ...form, enabled: checked })}
							/>
							<Label htmlFor="cb-enabled">Enabled</Label>
						</div>
						<Button onClick={handleCreate} disabled={isCreating}>
							<Plus className="h-4 w-4" />
							Add policy
						</Button>
					</div>
				</CardContent>
			</Card>

			{/* Policies + live state */}
			<Card>
				<CardHeader>
					<CardTitle className="flex items-center gap-2 text-base">
						<Activity className="h-4 w-4" />
						Provider policies
					</CardTitle>
					<CardDescription>Live state is polled from the running engine and is per-instance.</CardDescription>
				</CardHeader>
				<CardContent>
					{isLoading && <p className="text-muted-foreground text-sm">Loading policies…</p>}
					{isError && <p className="text-sm text-red-500">Failed to load policies: {getErrorMessage(error)}</p>}
					{!isLoading && !isError && policies.length === 0 && (
						<p className="text-muted-foreground text-sm">No circuit breaker policies configured. The feature is off until you add one.</p>
					)}
					{policies.length > 0 && (
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>Provider</TableHead>
									<TableHead>Live state</TableHead>
									<TableHead>Threshold</TableHead>
									<TableHead>Cooldown</TableHead>
									<TableHead>Probes</TableHead>
									<TableHead>Trips</TableHead>
									<TableHead>Enabled</TableHead>
									<TableHead className="text-right">Actions</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{policies.map((p) => {
									const live = stateByProvider.get(p.provider);
									const state: CircuitState = live?.state ?? "closed";
									return (
										<TableRow key={p.id}>
											<TableCell className="font-medium">{p.provider}</TableCell>
											<TableCell>
												{p.enabled ? (
													<Badge variant={stateBadgeVariant(state)}>{stateLabel(state)}</Badge>
												) : (
													<Badge variant="secondary">Off</Badge>
												)}
												{live && live.consecutive_failures > 0 && (
													<span className="text-muted-foreground ml-2 text-xs">{live.consecutive_failures} fail(s)</span>
												)}
											</TableCell>
											<TableCell>{p.failure_threshold}</TableCell>
											<TableCell>{p.cooldown_seconds}s</TableCell>
											<TableCell>{p.half_open_probes}</TableCell>
											<TableCell>{live?.total_trips ?? 0}</TableCell>
											<TableCell>
												<Switch checked={p.enabled} onCheckedChange={(checked) => handleToggle(p.id, checked)} />
											</TableCell>
											<TableCell className="text-right">
												<div className="flex justify-end gap-2">
													<Button variant="outline" size="sm" onClick={() => handleReset(p.id, p.provider)} title="Reset to closed">
														<RotateCcw className="h-4 w-4" />
														Reset
													</Button>
													<Button variant="destructive" size="sm" onClick={() => handleDelete(p.id, p.provider)} title="Delete policy">
														<Trash2 className="h-4 w-4" />
													</Button>
												</div>
											</TableCell>
										</TableRow>
									);
								})}
							</TableBody>
						</Table>
					)}
				</CardContent>
			</Card>
		</div>
	);
}