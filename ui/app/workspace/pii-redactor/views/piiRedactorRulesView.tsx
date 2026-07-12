import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alertDialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { getErrorMessage } from "@/lib/store";
import {
	useCreatePIIRuleMutation,
	useDeletePIIRuleMutation,
	useGetPIIRulesQuery,
	useTestPIIRuleMutation,
	useUpdatePIIRuleMutation,
} from "@/lib/store/apis/piiRulesApi";
import { PII_RULE_TYPES, PIIRule, PIIRuleScope, PIIRuleType } from "@/lib/types/piiRules";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

const SCOPES: { value: PIIRuleScope; label: string }[] = [
	{ value: "global", label: "Global" },
	{ value: "virtual_key", label: "Virtual Key" },
	{ value: "team", label: "Team" },
	{ value: "customer", label: "Customer" },
];

// Draft mirrors the editable shape of a rule for the create/edit dialog. Both
// discriminator field groups are kept so switching type preserves prior input.
interface Draft {
	id?: string;
	name: string;
	description: string;
	type: PIIRuleType;
	enabled: boolean;
	scope: PIIRuleScope;
	scope_id: string;
	order: number;
	regex_pattern: string;
	regex_replacement: string;
	presidio_base_url: string;
	presidio_entity_type: string;
	presidio_score_threshold: number;
}

function emptyDraft(): Draft {
	return {
		name: "",
		description: "",
		type: "regex",
		enabled: true,
		scope: "global",
		scope_id: "",
		order: 0,
		regex_pattern: "",
		regex_replacement: "[REDACTED]",
		presidio_base_url: "http://localhost:5002",
		presidio_entity_type: "",
		presidio_score_threshold: 0.5,
	};
}

function draftFromRule(r: PIIRule): Draft {
	return {
		id: r.id,
		name: r.name,
		description: r.description ?? "",
		type: r.type,
		enabled: r.enabled,
		scope: r.scope,
		scope_id: r.scope_id ?? "",
		order: r.order ?? 0,
		regex_pattern: r.regex_pattern ?? "",
		regex_replacement: r.regex_replacement ?? "[REDACTED]",
		presidio_base_url: r.presidio_base_url ?? "http://localhost:5002",
		presidio_entity_type: r.presidio_entity_type ?? "",
		presidio_score_threshold: r.presidio_score_threshold ?? 0.5,
	};
}

export default function PiiRedactorRulesView() {
	const canCreate = useRbac(RbacResource.PIIRedactor, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.PIIRedactor, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.PIIRedactor, RbacOperation.Delete);

	const { data, isLoading, isError, error } = useGetPIIRulesQuery();
	const [createPIIRule] = useCreatePIIRuleMutation();
	const [updatePIIRule] = useUpdatePIIRuleMutation();
	const [deletePIIRule] = useDeletePIIRuleMutation();

	const [dialogOpen, setDialogOpen] = useState(false);
	const [draft, setDraft] = useState<Draft>(emptyDraft());
	const [saving, setSaving] = useState(false);
	const [deleteTarget, setDeleteTarget] = useState<PIIRule | null>(null);

	const rules = data?.pii_rules ?? [];

	function openCreate() {
		setDraft(emptyDraft());
		setDialogOpen(true);
	}

	function openEdit(r: PIIRule) {
		setDraft(draftFromRule(r));
		setDialogOpen(true);
	}

	async function handleToggleEnabled(r: PIIRule, enabled: boolean) {
		try {
			await updatePIIRule({ id: r.id, enabled }).unwrap();
			toast.success(`${r.name} ${enabled ? "enabled" : "disabled"}`);
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	}

	async function handleSave() {
		if (!draft.name.trim()) {
			toast.error("Name is required");
			return;
		}
		if (draft.scope !== "global" && !draft.scope_id.trim()) {
			toast.error("A scope target ID is required for non-global scopes");
			return;
		}
		if (draft.type === "regex" && !draft.regex_pattern.trim()) {
			toast.error("A regex pattern is required for a regex rule");
			return;
		}
		if (draft.type === "presidio" && !draft.presidio_base_url.trim()) {
			toast.error("A Presidio analyzer URL is required for a presidio rule");
			return;
		}
		setSaving(true);
		try {
			if (draft.id) {
				await updatePIIRule({
					id: draft.id,
					name: draft.name,
					description: draft.description,
					enabled: draft.enabled,
					order: draft.order,
					regex_pattern: draft.regex_pattern,
					regex_replacement: draft.regex_replacement,
					presidio_base_url: draft.presidio_base_url,
					presidio_entity_type: draft.presidio_entity_type,
					presidio_score_threshold: draft.presidio_score_threshold,
				}).unwrap();
				toast.success("PII rule updated");
			} else {
				await createPIIRule({
					name: draft.name,
					description: draft.description,
					type: draft.type,
					enabled: draft.enabled,
					scope: draft.scope,
					scope_id: draft.scope === "global" ? null : draft.scope_id.trim(),
					order: draft.order,
					regex_pattern: draft.regex_pattern,
					regex_replacement: draft.regex_replacement,
					presidio_base_url: draft.presidio_base_url,
					presidio_entity_type: draft.presidio_entity_type,
					presidio_score_threshold: draft.presidio_score_threshold,
				}).unwrap();
				toast.success("PII rule created");
			}
			setDialogOpen(false);
		} catch (err) {
			toast.error(getErrorMessage(err));
		} finally {
			setSaving(false);
		}
	}

	async function handleDelete() {
		if (!deleteTarget) return;
		try {
			await deletePIIRule(deleteTarget.id).unwrap();
			toast.success("PII rule deleted");
		} catch (err) {
			toast.error(getErrorMessage(err));
		} finally {
			setDeleteTarget(null);
		}
	}

	return (
		<div className="w-full space-y-4">
			<div className="flex items-start justify-between gap-4">
				<div>
					<h2 className="text-lg font-semibold tracking-tight">PII Redactor Rules</h2>
					<p className="text-muted-foreground text-sm">
						Mask PII and secrets in request messages before they reach the provider. Regex rules run in-process; Presidio rules call a
						self-hosted analyzer. Enabled rules apply to live requests immediately.
					</p>
				</div>
				<Button onClick={openCreate} disabled={!canCreate} data-testid="create-pii-rule-button">
					<Plus className="size-4" /> New PII rule
				</Button>
			</div>

			{isLoading && <p className="text-muted-foreground text-sm">Loading PII rules...</p>}
			{isError && <p className="text-sm text-red-500">Failed to load PII rules: {getErrorMessage(error)}</p>}

			{!isLoading && !isError && (
				<div className="overflow-auto rounded-sm border">
					<Table data-testid="pii-rules-table">
						<TableHeader>
							<TableRow className="bg-muted/50">
								<TableHead className="font-semibold">Name</TableHead>
								<TableHead className="font-semibold">Type</TableHead>
								<TableHead className="font-semibold">Pattern / Entity</TableHead>
								<TableHead className="font-semibold">Scope</TableHead>
								<TableHead className="w-px text-right font-semibold">Order</TableHead>
								<TableHead className="w-px text-right font-semibold">Enabled</TableHead>
								<TableHead className="w-px font-semibold" />
							</TableRow>
						</TableHeader>
						<TableBody>
							{rules.length === 0 ? (
								<TableRow data-testid="pii-rules-table-empty-state">
									<TableCell colSpan={7} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">No PII rules yet. Create one to start redacting sensitive data.</span>
									</TableCell>
								</TableRow>
							) : (
								rules.map((r) => (
									<TableRow key={r.id} className="group hover:bg-muted/50 transition-colors">
										<TableCell className="align-top font-medium">
											{r.name}
											{r.description && <p className="text-muted-foreground text-xs font-normal">{r.description}</p>}
										</TableCell>
										<TableCell className="align-top">
											<Badge variant={r.type === "presidio" ? "secondary" : "outline"} className="capitalize">
												{r.type}
											</Badge>
										</TableCell>
										<TableCell className="align-top">
											<code className="text-muted-foreground text-xs break-all">
												{r.type === "regex" ? r.regex_pattern : r.presidio_entity_type || "all entities"}
											</code>
										</TableCell>
										<TableCell className="align-top">
											<Badge variant="outline" className="capitalize">
												{r.scope.replace("_", " ")}
											</Badge>
											{r.scope !== "global" && <span className="text-muted-foreground ml-2 text-xs">{r.scope_name || r.scope_id}</span>}
										</TableCell>
										<TableCell className="w-px text-right align-top tabular-nums">{r.order}</TableCell>
										<TableCell className="w-px text-right align-top">
											<Switch
												size="md"
												checked={r.enabled}
												disabled={!canUpdate}
												onAsyncCheckedChange={(checked) => handleToggleEnabled(r, checked)}
											/>
										</TableCell>
										<TableCell className="w-px align-top">
											<div className="flex items-center gap-1">
												<Button variant="outline" size="sm" disabled={!canUpdate} onClick={() => openEdit(r)}>
													Edit
												</Button>
												<Button
													variant="ghost"
													size="icon"
													disabled={!canDelete}
													onClick={() => setDeleteTarget(r)}
													aria-label={`Delete ${r.name}`}
												>
													<Trash2 className="size-4 text-red-500" />
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

			<PiiRuleDialog open={dialogOpen} onOpenChange={setDialogOpen} draft={draft} setDraft={setDraft} onSave={handleSave} saving={saving} />

			<AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete PII rule?</AlertDialogTitle>
						<AlertDialogDescription>
							This will remove "{deleteTarget?.name}" and stop redacting with it on live requests. This cannot be undone.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction onClick={handleDelete}>Delete</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}

interface PiiRuleDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	draft: Draft;
	setDraft: (d: Draft) => void;
	onSave: () => void;
	saving: boolean;
}

function PiiRuleDialog({ open, onOpenChange, draft, setDraft, onSave, saving }: PiiRuleDialogProps) {
	const isEdit = !!draft.id;
	const [testPIIRule, { isLoading: testing }] = useTestPIIRuleMutation();
	const [testText, setTestText] = useState("");
	const [testResult, setTestResult] = useState<string | null>(null);

	async function runTest() {
		if (!draft.id) return;
		try {
			const res = await testPIIRule({ id: draft.id, text: testText }).unwrap();
			setTestResult(res.warning ? `${res.redacted}\n\n(${res.warning})` : res.redacted);
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	}

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
				<DialogHeader>
					<DialogTitle>{isEdit ? "Edit PII rule" : "New PII rule"}</DialogTitle>
					<DialogDescription>
						A PII rule masks sensitive text before the request reaches the provider. Choose regex for structured PII or Presidio for
						free-text entities.
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4">
					<div className="space-y-2">
						<Label htmlFor="pii-name">Name</Label>
						<Input
							id="pii-name"
							value={draft.name}
							onChange={(e) => setDraft({ ...draft, name: e.target.value })}
							placeholder="e.g. Mask email addresses"
						/>
					</div>

					<div className="space-y-2">
						<Label htmlFor="pii-description">Description</Label>
						<Input
							id="pii-description"
							value={draft.description}
							onChange={(e) => setDraft({ ...draft, description: e.target.value })}
							placeholder="Optional"
						/>
					</div>

					<div className="grid grid-cols-2 gap-4">
						<div className="space-y-2">
							<Label>Type</Label>
							<Select value={draft.type} disabled={isEdit} onValueChange={(v) => setDraft({ ...draft, type: v as PIIRuleType })}>
								<SelectTrigger>
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									{PII_RULE_TYPES.map((t) => (
										<SelectItem key={t.value} value={t.value}>
											{t.label}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						<div className="space-y-2">
							<Label htmlFor="pii-order">Execution order</Label>
							<Input
								id="pii-order"
								type="number"
								value={String(draft.order)}
								onChange={(e) => setDraft({ ...draft, order: Number(e.target.value) || 0 })}
							/>
						</div>
					</div>

					<p className="text-muted-foreground text-xs">{PII_RULE_TYPES.find((t) => t.value === draft.type)?.description}</p>

					<div className="grid grid-cols-2 gap-4">
						<div className="space-y-2">
							<Label>Scope</Label>
							<Select value={draft.scope} disabled={isEdit} onValueChange={(v) => setDraft({ ...draft, scope: v as PIIRuleScope })}>
								<SelectTrigger>
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									{SCOPES.map((s) => (
										<SelectItem key={s.value} value={s.value}>
											{s.label}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						{draft.scope !== "global" && (
							<div className="space-y-2">
								<Label htmlFor="pii-scope-id">Scope target ID</Label>
								<Input
									id="pii-scope-id"
									value={draft.scope_id}
									disabled={isEdit}
									onChange={(e) => setDraft({ ...draft, scope_id: e.target.value })}
									placeholder="virtual key / team / customer ID"
								/>
							</div>
						)}
					</div>

					{draft.type === "regex" ? (
						<div className="space-y-4 rounded-sm border p-3">
							<div className="space-y-2">
								<Label htmlFor="pii-pattern">Regex pattern</Label>
								<Input
									id="pii-pattern"
									value={draft.regex_pattern}
									onChange={(e) => setDraft({ ...draft, regex_pattern: e.target.value })}
									placeholder="[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}"
									className="font-mono"
								/>
							</div>
							<div className="space-y-2">
								<Label htmlFor="pii-replacement">Replacement</Label>
								<Input
									id="pii-replacement"
									value={draft.regex_replacement}
									onChange={(e) => setDraft({ ...draft, regex_replacement: e.target.value })}
									placeholder="[REDACTED]"
									className="font-mono"
								/>
							</div>
						</div>
					) : (
						<div className="space-y-4 rounded-sm border p-3">
							<div className="space-y-2">
								<Label htmlFor="pii-presidio-url">Presidio analyzer URL</Label>
								<Input
									id="pii-presidio-url"
									value={draft.presidio_base_url}
									onChange={(e) => setDraft({ ...draft, presidio_base_url: e.target.value })}
									placeholder="http://localhost:5002"
									className="font-mono"
								/>
							</div>
							<div className="grid grid-cols-2 gap-4">
								<div className="space-y-2">
									<Label htmlFor="pii-entity">Entity type</Label>
									<Input
										id="pii-entity"
										value={draft.presidio_entity_type}
										onChange={(e) => setDraft({ ...draft, presidio_entity_type: e.target.value })}
										placeholder="PERSON, EMAIL, ..."
									/>
								</div>
								<div className="space-y-2">
									<Label htmlFor="pii-threshold">Score threshold</Label>
									<Input
										id="pii-threshold"
										type="number"
										step="0.05"
										min="0"
										max="1"
										value={String(draft.presidio_score_threshold)}
										onChange={(e) => setDraft({ ...draft, presidio_score_threshold: Number(e.target.value) || 0 })}
									/>
								</div>
							</div>
						</div>
					)}

					<div className="flex items-center gap-2">
						<Switch size="md" checked={draft.enabled} onCheckedChange={(checked) => setDraft({ ...draft, enabled: checked })} />
						<Label>Enabled</Label>
					</div>

					{isEdit && (
						<div className="space-y-2 rounded-sm border p-3">
							<Label htmlFor="pii-test">Test against sample text</Label>
							<Textarea
								id="pii-test"
								value={testText}
								onChange={(e) => setTestText(e.target.value)}
								placeholder="Paste sample text containing PII to preview redaction."
								rows={3}
							/>
							<div className="flex items-center gap-2">
								<Button variant="outline" size="sm" onClick={runTest} disabled={testing || !testText.trim()}>
									{testing ? "Testing..." : "Run test"}
								</Button>
							</div>
							{testResult !== null && <pre className="bg-muted overflow-auto rounded-sm p-2 text-xs whitespace-pre-wrap">{testResult}</pre>}
						</div>
					)}
				</div>

				<DialogFooter>
					<Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
						Cancel
					</Button>
					<Button onClick={onSave} disabled={saving}>
						{saving ? "Saving..." : isEdit ? "Save changes" : "Create"}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}