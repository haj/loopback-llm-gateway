// Prompt deployments management UI. This feature is part of the open-source
// Loopback Gateway build, so the @enterprise alias resolves here in OSS builds
// and renders the real management surface instead of an upsell stub.
//
// A deployment is a named, weighted traffic-routing strategy for the selected
// prompt: it maps to a set of weighted version refs the gateway's prompts plugin
// uses to split live requests across versions, falling back to the latest
// version when a referenced version is deleted. Clients select a deployment with
// the x-bf-prompt-deployment header (and omit x-bf-prompt-version so routing
// applies).
import { usePromptContext } from "@/components/prompts/context";
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
import { getErrorMessage } from "@/lib/store";
import {
	useCreatePromptDeploymentMutation,
	useDeletePromptDeploymentMutation,
	useGetPromptDeploymentsQuery,
	useUpdatePromptDeploymentMutation,
} from "@/lib/store/apis/promptDeploymentsApi";
import { useGetVersionsQuery } from "@/lib/store/apis/promptsApi";
import { PromptDeployment, PromptDeploymentVersionRef } from "@/lib/types/prompts";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

interface Draft {
	id?: string;
	name: string;
	enabled: boolean;
	versions: PromptDeploymentVersionRef[];
}

function emptyDraft(): Draft {
	return { name: "", enabled: true, versions: [] };
}

function draftFromDeployment(d: PromptDeployment): Draft {
	return {
		id: d.id,
		name: d.name,
		enabled: d.enabled,
		versions: (d.versions ?? []).map((v) => ({ ...v })),
	};
}

// trafficShare returns the percentage of traffic a ref receives given the total
// weight of all positive-weight refs.
function trafficShare(weight: number, total: number): string {
	if (total <= 0 || weight <= 0) return "0%";
	return `${Math.round((weight / total) * 100)}%`;
}

export default function PromptDeploymentView(_props?: { omitTitle?: boolean }) {
	const { selectedPromptId } = usePromptContext();
	const canCreate = useRbac(RbacResource.PromptDeploymentStrategy, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.PromptDeploymentStrategy, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.PromptDeploymentStrategy, RbacOperation.Delete);

	const { data, isLoading, isError, error } = useGetPromptDeploymentsQuery(selectedPromptId ?? "", {
		skip: !selectedPromptId,
	});
	const { data: versionsData } = useGetVersionsQuery(selectedPromptId ?? "", { skip: !selectedPromptId });

	const [createDeployment] = useCreatePromptDeploymentMutation();
	const [updateDeployment] = useUpdatePromptDeploymentMutation();
	const [deleteDeployment] = useDeletePromptDeploymentMutation();

	const [dialogOpen, setDialogOpen] = useState(false);
	const [draft, setDraft] = useState<Draft>(emptyDraft());
	const [saving, setSaving] = useState(false);
	const [deleteTarget, setDeleteTarget] = useState<PromptDeployment | null>(null);

	if (!selectedPromptId) {
		return null;
	}

	const deployments = data?.deployments ?? [];
	const versions = versionsData?.versions ?? [];

	function openCreate() {
		setDraft(emptyDraft());
		setDialogOpen(true);
	}

	function openEdit(d: PromptDeployment) {
		setDraft(draftFromDeployment(d));
		setDialogOpen(true);
	}

	async function handleToggleEnabled(d: PromptDeployment, enabled: boolean) {
		try {
			await updateDeployment({ id: d.id, promptId: selectedPromptId!, enabled }).unwrap();
			toast.success(`${d.name} ${enabled ? "enabled" : "disabled"}`);
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	}

	async function handleSave() {
		if (!draft.name.trim()) {
			toast.error("Name is required");
			return;
		}
		const refs = draft.versions.filter((v) => v.version_number > 0);
		if (refs.length === 0) {
			toast.error("Add at least one version with a weight");
			return;
		}
		if (refs.every((v) => v.weight <= 0)) {
			toast.error("At least one version must have a positive weight");
			return;
		}
		setSaving(true);
		try {
			if (draft.id) {
				await updateDeployment({
					id: draft.id,
					promptId: selectedPromptId!,
					name: draft.name.trim(),
					enabled: draft.enabled,
					versions: refs,
				}).unwrap();
				toast.success("Deployment updated");
			} else {
				await createDeployment({
					promptId: selectedPromptId!,
					name: draft.name.trim(),
					enabled: draft.enabled,
					versions: refs,
				}).unwrap();
				toast.success("Deployment created");
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
			await deleteDeployment({ id: deleteTarget.id, promptId: selectedPromptId! }).unwrap();
			toast.success("Deployment deleted");
		} catch (err) {
			toast.error(getErrorMessage(err));
		} finally {
			setDeleteTarget(null);
		}
	}

	return (
		<div className="flex w-full flex-col gap-3">
			<div className="flex items-center justify-between gap-2">
				<p className="text-muted-foreground text-xs">
					Split live traffic across versions. Requests select a deployment with the{" "}
					<code className="text-[10px]">x-bf-prompt-deployment</code> header.
				</p>
			</div>

			<Button
				size="sm"
				variant="outline"
				className="w-full"
				onClick={openCreate}
				disabled={!canCreate}
				data-testid="create-deployment-button"
			>
				<Plus className="size-4" /> New deployment
			</Button>

			{isLoading && <p className="text-muted-foreground text-xs">Loading deployments...</p>}
			{isError && <p className="text-xs text-red-500">Failed to load deployments: {getErrorMessage(error)}</p>}

			{!isLoading && !isError && deployments.length === 0 && (
				<p className="text-muted-foreground text-xs" data-testid="deployments-empty-state">
					No deployments yet. Create one to route traffic across versions.
				</p>
			)}

			<div className="flex flex-col gap-2">
				{deployments.map((d) => {
					const total = (d.versions ?? []).reduce((sum, v) => sum + Math.max(0, v.weight), 0);
					return (
						<div key={d.id} className="rounded-md border p-3" data-testid="deployment-card">
							<div className="flex items-start justify-between gap-2">
								<div className="min-w-0">
									<div className="truncate text-sm font-medium">{d.name}</div>
									<div className="text-muted-foreground text-[11px]">
										{(d.versions ?? []).length} version{(d.versions ?? []).length === 1 ? "" : "s"}
									</div>
								</div>
								<Switch
									size="md"
									checked={d.enabled}
									disabled={!canUpdate}
									onAsyncCheckedChange={(checked) => handleToggleEnabled(d, checked)}
								/>
							</div>

							<div className="mt-2 flex flex-wrap gap-1">
								{(d.versions ?? []).map((v) => (
									<Badge key={v.version_number} variant="secondary" className="text-[10px]">
										v{v.version_number} · {trafficShare(v.weight, total)}
									</Badge>
								))}
							</div>

							<div className="mt-2 flex items-center gap-1">
								<Button variant="outline" size="sm" disabled={!canUpdate} onClick={() => openEdit(d)}>
									Edit
								</Button>
								<Button
									variant="ghost"
									size="icon"
									disabled={!canDelete}
									onClick={() => setDeleteTarget(d)}
									aria-label={`Delete ${d.name}`}
								>
									<Trash2 className="size-4 text-red-500" />
								</Button>
							</div>
						</div>
					);
				})}
			</div>

			<DeploymentDialog
				open={dialogOpen}
				onOpenChange={setDialogOpen}
				draft={draft}
				setDraft={setDraft}
				onSave={handleSave}
				saving={saving}
				availableVersions={versions.map((v) => ({ number: v.version_number, label: v.commit_message }))}
			/>

			<AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete deployment?</AlertDialogTitle>
						<AlertDialogDescription>
							This will remove "{deleteTarget?.name}" and stop routing live traffic through it. This cannot be undone.
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

interface DeploymentDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	draft: Draft;
	setDraft: (d: Draft) => void;
	onSave: () => void;
	saving: boolean;
	availableVersions: { number: number; label: string }[];
}

function DeploymentDialog({ open, onOpenChange, draft, setDraft, onSave, saving, availableVersions }: DeploymentDialogProps) {
	const isEdit = !!draft.id;
	const total = draft.versions.reduce((sum, v) => sum + Math.max(0, v.weight), 0);

	function addVersionRef() {
		const used = new Set(draft.versions.map((v) => v.version_number));
		const next = availableVersions.find((v) => !used.has(v.number));
		setDraft({
			...draft,
			versions: [...draft.versions, { version_number: next?.number ?? 0, weight: 1 }],
		});
	}

	function updateRef(index: number, next: PromptDeploymentVersionRef) {
		const versions = draft.versions.slice();
		versions[index] = next;
		setDraft({ ...draft, versions });
	}

	function removeRef(index: number) {
		setDraft({ ...draft, versions: draft.versions.filter((_, i) => i !== index) });
	}

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
				<DialogHeader>
					<DialogTitle>{isEdit ? "Edit deployment" : "New deployment"}</DialogTitle>
					<DialogDescription>
						Name the deployment and assign weights to one or more versions. Weights are relative; traffic is split in proportion. If a
						referenced version is deleted, its traffic falls back to the latest version.
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4">
					<div className="space-y-2">
						<Label htmlFor="deployment-name">Name</Label>
						<Input
							id="deployment-name"
							value={draft.name}
							onChange={(e) => setDraft({ ...draft, name: e.target.value })}
							placeholder="e.g. production"
						/>
					</div>

					<div className="flex items-center gap-2">
						<Switch size="md" checked={draft.enabled} onCheckedChange={(checked) => setDraft({ ...draft, enabled: checked })} />
						<Label>Enabled</Label>
					</div>

					<div className="space-y-3">
						<div className="flex items-center justify-between">
							<Label>Version weights</Label>
							<Button variant="outline" size="sm" onClick={addVersionRef} disabled={availableVersions.length === 0}>
								<Plus className="size-4" /> Add version
							</Button>
						</div>
						{draft.versions.length === 0 && <p className="text-muted-foreground text-sm">No versions added yet.</p>}
						{draft.versions.map((ref, index) => (
							<div key={index} className="flex items-center gap-2 rounded-sm border p-2">
								<div className="flex-1 space-y-1">
									<Label className="text-xs">Version</Label>
									<Select
										value={ref.version_number ? String(ref.version_number) : ""}
										onValueChange={(v) => updateRef(index, { ...ref, version_number: Number(v) })}
									>
										<SelectTrigger>
											<SelectValue placeholder="Select version" />
										</SelectTrigger>
										<SelectContent>
											{availableVersions.map((v) => (
												<SelectItem key={v.number} value={String(v.number)}>
													v{v.number}
													{v.label ? ` — ${v.label}` : ""}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
								</div>
								<div className="w-20 space-y-1">
									<Label className="text-xs">Weight</Label>
									<Input
										type="number"
										min={0}
										value={String(ref.weight)}
										onChange={(e) => updateRef(index, { ...ref, weight: Math.max(0, Number(e.target.value) || 0) })}
									/>
								</div>
								<div className="text-muted-foreground w-12 shrink-0 pt-5 text-right text-xs">{trafficShare(ref.weight, total)}</div>
								<Button variant="ghost" size="icon" className="mt-4" onClick={() => removeRef(index)} aria-label="Remove version">
									<Trash2 className="size-4 text-red-500" />
								</Button>
							</div>
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