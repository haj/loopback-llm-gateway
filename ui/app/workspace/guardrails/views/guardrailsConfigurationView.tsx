import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { TagInput } from "@/components/ui/tagInput";
import { getErrorMessage } from "@/lib/store";
import {
	useCreateGuardrailMutation,
	useDeleteGuardrailMutation,
	useGetGuardrailsQuery,
	useUpdateGuardrailMutation,
} from "@/lib/store/apis/guardrailsApi";
import {
	GUARDRAIL_DEFINITIONS,
	GuardrailConfig,
	GuardrailItem,
	GuardrailParams,
	GuardrailScope,
	GuardrailType,
	guardrailDefinition,
} from "@/lib/types/guardrails";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

const SCOPES: { value: GuardrailScope; label: string }[] = [
	{ value: "global", label: "Global" },
	{ value: "virtual_key", label: "Virtual Key" },
	{ value: "team", label: "Team" },
	{ value: "customer", label: "Customer" },
];

// Draft mirrors the editable shape of a config for the create/edit dialog.
interface Draft {
	id?: string;
	name: string;
	enabled: boolean;
	scope: GuardrailScope;
	scope_id: string;
	guardrails: GuardrailItem[];
}

function emptyDraft(): Draft {
	return { name: "", enabled: true, scope: "global", scope_id: "", guardrails: [] };
}

function draftFromConfig(c: GuardrailConfig): Draft {
	return {
		id: c.id,
		name: c.name,
		enabled: c.enabled,
		scope: c.scope,
		scope_id: c.scope_id ?? "",
		guardrails: c.guardrails.map((g) => ({ ...g, params: { ...(g.params ?? {}) } })),
	};
}

export default function GuardrailsConfigurationView() {
	const canCreate = useRbac(RbacResource.GuardrailsConfig, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.GuardrailsConfig, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.GuardrailsConfig, RbacOperation.Delete);

	const { data, isLoading, isError, error } = useGetGuardrailsQuery();
	const [createGuardrail] = useCreateGuardrailMutation();
	const [updateGuardrail] = useUpdateGuardrailMutation();
	const [deleteGuardrail] = useDeleteGuardrailMutation();

	const [dialogOpen, setDialogOpen] = useState(false);
	const [draft, setDraft] = useState<Draft>(emptyDraft());
	const [saving, setSaving] = useState(false);
	const [deleteTarget, setDeleteTarget] = useState<GuardrailConfig | null>(null);

	const configs = data?.guardrails ?? [];

	function openCreate() {
		setDraft(emptyDraft());
		setDialogOpen(true);
	}

	function openEdit(c: GuardrailConfig) {
		setDraft(draftFromConfig(c));
		setDialogOpen(true);
	}

	async function handleToggleEnabled(c: GuardrailConfig, enabled: boolean) {
		try {
			await updateGuardrail({ id: c.id, enabled }).unwrap();
			toast.success(`${c.name} ${enabled ? "enabled" : "disabled"}`);
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
		setSaving(true);
		try {
			if (draft.id) {
				await updateGuardrail({
					id: draft.id,
					name: draft.name,
					enabled: draft.enabled,
					guardrails: draft.guardrails,
				}).unwrap();
				toast.success("Guardrail config updated");
			} else {
				await createGuardrail({
					name: draft.name,
					enabled: draft.enabled,
					scope: draft.scope,
					scope_id: draft.scope === "global" ? null : draft.scope_id.trim(),
					guardrails: draft.guardrails,
				}).unwrap();
				toast.success("Guardrail config created");
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
			await deleteGuardrail(deleteTarget.id).unwrap();
			toast.success("Guardrail config deleted");
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
					<h2 className="text-lg font-semibold tracking-tight">Guardrails</h2>
					<p className="text-muted-foreground text-sm">
						Configure input guardrails enforced by the Loopback Gateway guard plugin. Enabled configs are applied to live requests
						immediately.
					</p>
				</div>
				<Button onClick={openCreate} disabled={!canCreate} data-testid="create-guardrail-button">
					<Plus className="size-4" /> New guardrail config
				</Button>
			</div>

			{isLoading && <p className="text-muted-foreground text-sm">Loading guardrail configs...</p>}
			{isError && <p className="text-sm text-red-500">Failed to load guardrails: {getErrorMessage(error)}</p>}

			{!isLoading && !isError && (
				<div className="overflow-auto rounded-sm border">
					<Table data-testid="guardrails-table">
						<TableHeader>
							<TableRow className="bg-muted/50">
								<TableHead className="font-semibold">Name</TableHead>
								<TableHead className="font-semibold">Scope</TableHead>
								<TableHead className="font-semibold">Guardrails</TableHead>
								<TableHead className="w-px text-right font-semibold">Enabled</TableHead>
								<TableHead className="w-px font-semibold" />
							</TableRow>
						</TableHeader>
						<TableBody>
							{configs.length === 0 ? (
								<TableRow data-testid="guardrails-table-empty-state">
									<TableCell colSpan={5} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">
											No guardrail configs yet. Create one to start enforcing input guardrails.
										</span>
									</TableCell>
								</TableRow>
							) : (
								configs.map((c) => (
									<TableRow key={c.id} className="group hover:bg-muted/50 transition-colors">
										<TableCell className="align-top font-medium">{c.name}</TableCell>
										<TableCell className="align-top">
											<Badge variant="outline" className="capitalize">
												{c.scope.replace("_", " ")}
											</Badge>
											{c.scope !== "global" && <span className="text-muted-foreground ml-2 text-xs">{c.scope_name || c.scope_id}</span>}
										</TableCell>
										<TableCell className="align-top">
											<div className="flex flex-wrap gap-1">
												{c.guardrails.length === 0 && <span className="text-muted-foreground text-xs">none</span>}
												{c.guardrails.map((g) => (
													<Badge key={g.id || g.type} variant={g.enabled ? "secondary" : "outline"} className="text-xs">
														{guardrailDefinition(g.type)?.label ?? g.type}
													</Badge>
												))}
											</div>
										</TableCell>
										<TableCell className="w-px text-right align-top">
											<Switch
												size="md"
												checked={c.enabled}
												disabled={!canUpdate}
												onAsyncCheckedChange={(checked) => handleToggleEnabled(c, checked)}
											/>
										</TableCell>
										<TableCell className="w-px align-top">
											<div className="flex items-center gap-1">
												<Button variant="outline" size="sm" disabled={!canUpdate} onClick={() => openEdit(c)}>
													Edit
												</Button>
												<Button
													variant="ghost"
													size="icon"
													disabled={!canDelete}
													onClick={() => setDeleteTarget(c)}
													aria-label={`Delete ${c.name}`}
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

			<GuardrailDialog
				open={dialogOpen}
				onOpenChange={setDialogOpen}
				draft={draft}
				setDraft={setDraft}
				onSave={handleSave}
				saving={saving}
			/>

			<AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete guardrail config?</AlertDialogTitle>
						<AlertDialogDescription>
							This will remove "{deleteTarget?.name}" and stop enforcing its guardrails on live requests. This cannot be undone.
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

interface GuardrailDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	draft: Draft;
	setDraft: (d: Draft) => void;
	onSave: () => void;
	saving: boolean;
}

function GuardrailDialog({ open, onOpenChange, draft, setDraft, onSave, saving }: GuardrailDialogProps) {
	const isEdit = !!draft.id;

	function addGuardrail(type: GuardrailType) {
		setDraft({
			...draft,
			guardrails: [...draft.guardrails, { type, enabled: true, params: {} }],
		});
	}

	function updateItem(index: number, next: GuardrailItem) {
		const guardrails = draft.guardrails.slice();
		guardrails[index] = next;
		setDraft({ ...draft, guardrails });
	}

	function removeItem(index: number) {
		setDraft({ ...draft, guardrails: draft.guardrails.filter((_, i) => i !== index) });
	}

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
				<DialogHeader>
					<DialogTitle>{isEdit ? "Edit guardrail config" : "New guardrail config"}</DialogTitle>
					<DialogDescription>
						Group one or more guardrails under a named, scoped config. The 15 guardrail types check the request text or envelope and block
						on violation.
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4">
					<div className="space-y-2">
						<Label htmlFor="guardrail-name">Name</Label>
						<Input
							id="guardrail-name"
							value={draft.name}
							onChange={(e) => setDraft({ ...draft, name: e.target.value })}
							placeholder="e.g. Block secrets on prod"
						/>
					</div>

					<div className="grid grid-cols-2 gap-4">
						<div className="space-y-2">
							<Label>Scope</Label>
							<Select value={draft.scope} disabled={isEdit} onValueChange={(v) => setDraft({ ...draft, scope: v as GuardrailScope })}>
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
								<Label htmlFor="guardrail-scope-id">Scope target ID</Label>
								<Input
									id="guardrail-scope-id"
									value={draft.scope_id}
									disabled={isEdit}
									onChange={(e) => setDraft({ ...draft, scope_id: e.target.value })}
									placeholder="virtual key / team / customer ID"
								/>
							</div>
						)}
					</div>

					<div className="flex items-center gap-2">
						<Switch size="md" checked={draft.enabled} onCheckedChange={(checked) => setDraft({ ...draft, enabled: checked })} />
						<Label>Enabled</Label>
					</div>

					<div className="space-y-3">
						<div className="flex items-center justify-between">
							<Label>Guardrails</Label>
							<AddGuardrailMenu onAdd={addGuardrail} />
						</div>
						{draft.guardrails.length === 0 && <p className="text-muted-foreground text-sm">No guardrails added yet.</p>}
						{draft.guardrails.map((item, index) => (
							<GuardrailItemEditor
								key={item.id || `${item.type}-${index}`}
								item={item}
								onChange={(next) => updateItem(index, next)}
								onRemove={() => removeItem(index)}
							/>
						))}
					</div>
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

function AddGuardrailMenu({ onAdd }: { onAdd: (type: GuardrailType) => void }) {
	return (
		<Select value="" onValueChange={(v) => v && onAdd(v as GuardrailType)}>
			<SelectTrigger className="w-56">
				<SelectValue placeholder="Add guardrail..." />
			</SelectTrigger>
			<SelectContent>
				{GUARDRAIL_DEFINITIONS.map((d) => (
					<SelectItem key={d.type} value={d.type}>
						{d.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

interface GuardrailItemEditorProps {
	item: GuardrailItem;
	onChange: (next: GuardrailItem) => void;
	onRemove: () => void;
}

function GuardrailItemEditor({ item, onChange, onRemove }: GuardrailItemEditorProps) {
	const def = guardrailDefinition(item.type);
	const params = item.params ?? {};

	function setParam<K extends keyof GuardrailParams>(key: K, value: GuardrailParams[K]) {
		onChange({ ...item, params: { ...params, [key]: value } });
	}

	return (
		<div className="space-y-3 rounded-sm border p-3">
			<div className="flex items-center justify-between gap-2">
				<div className="flex items-center gap-2">
					<span className="text-sm font-medium">{def?.label ?? item.type}</span>
					<Badge variant="outline" className="text-xs capitalize">
						{def?.target ?? "text"}
					</Badge>
				</div>
				<div className="flex items-center gap-2">
					<Switch size="md" checked={item.enabled} onCheckedChange={(checked) => onChange({ ...item, enabled: checked })} />
					<Button variant="ghost" size="icon" onClick={onRemove} aria-label="Remove guardrail">
						<Trash2 className="size-4 text-red-500" />
					</Button>
				</div>
			</div>
			{def?.description && <p className="text-muted-foreground text-xs">{def.description}</p>}
			<div className="grid grid-cols-2 gap-3">
				{def?.fields.map((field) => (
					<div key={String(field.key)} className="space-y-1.5">
						<Label className="text-xs">{field.label}</Label>
						{field.kind === "stringList" && (
							<TagInput
								value={(params[field.key] as string[]) ?? []}
								onValueChange={(v) => setParam(field.key, v as GuardrailParams[typeof field.key])}
								placeholder={field.placeholder}
							/>
						)}
						{field.kind === "text" && (
							<Input
								value={(params[field.key] as string) ?? ""}
								onChange={(e) => setParam(field.key, e.target.value as GuardrailParams[typeof field.key])}
								placeholder={field.placeholder}
							/>
						)}
						{field.kind === "number" && (
							<Input
								type="number"
								value={params[field.key] === undefined ? "" : String(params[field.key])}
								onChange={(e) =>
									setParam(field.key, (e.target.value === "" ? undefined : Number(e.target.value)) as GuardrailParams[typeof field.key])
								}
							/>
						)}
						{field.kind === "operator" && (
							<Select
								value={(params[field.key] as string) ?? "none"}
								onValueChange={(v) => setParam(field.key, v as GuardrailParams[typeof field.key])}
							>
								<SelectTrigger>
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value="any">any</SelectItem>
									<SelectItem value="all">all</SelectItem>
									<SelectItem value="none">none</SelectItem>
								</SelectContent>
							</Select>
						)}
						{field.kind === "boolean" && (
							<div className="flex h-9 items-center">
								<Switch
									size="md"
									checked={!!params[field.key]}
									onCheckedChange={(checked) => setParam(field.key, checked as GuardrailParams[typeof field.key])}
								/>
							</div>
						)}
					</div>
				))}
			</div>
		</div>
	);
}