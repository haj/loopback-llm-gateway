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
import { Textarea } from "@/components/ui/textarea";
import { getErrorMessage } from "@/lib/store";
import { useGetMCPClientsQuery } from "@/lib/store/apis/mcpApi";
import {
	useCreateMCPToolGroupMutation,
	useDeleteMCPToolGroupMutation,
	useGetMCPToolGroupsQuery,
	useUpdateMCPToolGroupMutation,
} from "@/lib/store/apis/mcpToolGroupsApi";
import { MCPToolGroup, MCPToolGroupScope, MCPToolRef } from "@/lib/types/mcpToolGroups";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Plus, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

const SCOPES: { value: MCPToolGroupScope; label: string }[] = [
	{ value: "global", label: "Global" },
	{ value: "virtual_key", label: "Virtual Key" },
	{ value: "team", label: "Team" },
	{ value: "customer", label: "Customer" },
];

// Draft mirrors the editable shape of a group for the create/edit dialog.
interface Draft {
	id?: string;
	name: string;
	description: string;
	enabled: boolean;
	scope: MCPToolGroupScope;
	scope_id: string;
	tools: MCPToolRef[];
}

function emptyDraft(): Draft {
	return { name: "", description: "", enabled: true, scope: "team", scope_id: "", tools: [] };
}

function draftFromGroup(g: MCPToolGroup): Draft {
	return {
		id: g.id,
		name: g.name,
		description: g.description ?? "",
		enabled: g.enabled,
		scope: g.scope,
		scope_id: g.scope_id ?? "",
		tools: g.tools.map((t) => ({ ...t })),
	};
}

export default function MCPToolGroupsView() {
	const canCreate = useRbac(RbacResource.MCPToolGroups, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.MCPToolGroups, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.MCPToolGroups, RbacOperation.Delete);

	const { data, isLoading, isError, error } = useGetMCPToolGroupsQuery();
	const [createToolGroup] = useCreateMCPToolGroupMutation();
	const [updateToolGroup] = useUpdateMCPToolGroupMutation();
	const [deleteToolGroup] = useDeleteMCPToolGroupMutation();

	const [dialogOpen, setDialogOpen] = useState(false);
	const [draft, setDraft] = useState<Draft>(emptyDraft());
	const [saving, setSaving] = useState(false);
	const [deleteTarget, setDeleteTarget] = useState<MCPToolGroup | null>(null);

	const groups = data?.tool_groups ?? [];

	function openCreate() {
		setDraft(emptyDraft());
		setDialogOpen(true);
	}

	function openEdit(g: MCPToolGroup) {
		setDraft(draftFromGroup(g));
		setDialogOpen(true);
	}

	async function handleToggleEnabled(g: MCPToolGroup, enabled: boolean) {
		try {
			await updateToolGroup({ id: g.id, enabled }).unwrap();
			toast.success(`${g.name} ${enabled ? "enabled" : "disabled"}`);
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
				await updateToolGroup({
					id: draft.id,
					name: draft.name,
					description: draft.description,
					enabled: draft.enabled,
					tools: draft.tools,
				}).unwrap();
				toast.success("MCP tool group updated");
			} else {
				await createToolGroup({
					name: draft.name,
					description: draft.description,
					enabled: draft.enabled,
					scope: draft.scope,
					scope_id: draft.scope === "global" ? null : draft.scope_id.trim(),
					tools: draft.tools,
				}).unwrap();
				toast.success("MCP tool group created");
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
			await deleteToolGroup(deleteTarget.id).unwrap();
			toast.success("MCP tool group deleted");
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
					<h2 className="text-lg font-semibold tracking-tight">MCP Tool Groups</h2>
					<p className="text-muted-foreground text-sm">
						Bundle MCP tools into named groups and bind them to a virtual key or team. Enabled groups are honored by the Loopback Gateway
						MCP visibility filtering when resolving which tools a caller may use.
					</p>
				</div>
				<Button onClick={openCreate} disabled={!canCreate} data-testid="create-tool-group-button">
					<Plus className="size-4" /> New tool group
				</Button>
			</div>

			{isLoading && <p className="text-muted-foreground text-sm">Loading MCP tool groups...</p>}
			{isError && <p className="text-sm text-red-500">Failed to load MCP tool groups: {getErrorMessage(error)}</p>}

			{!isLoading && !isError && (
				<div className="overflow-auto rounded-sm border">
					<Table data-testid="tool-groups-table">
						<TableHeader>
							<TableRow className="bg-muted/50">
								<TableHead className="font-semibold">Name</TableHead>
								<TableHead className="font-semibold">Scope</TableHead>
								<TableHead className="font-semibold">Tools</TableHead>
								<TableHead className="w-px text-right font-semibold">Enabled</TableHead>
								<TableHead className="w-px font-semibold" />
							</TableRow>
						</TableHeader>
						<TableBody>
							{groups.length === 0 ? (
								<TableRow data-testid="tool-groups-table-empty-state">
									<TableCell colSpan={5} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">
											No MCP tool groups yet. Create one to bundle and govern MCP tools.
										</span>
									</TableCell>
								</TableRow>
							) : (
								groups.map((g) => (
									<TableRow key={g.id} className="group hover:bg-muted/50 transition-colors">
										<TableCell className="align-top font-medium">
											{g.name}
											{g.description && <p className="text-muted-foreground text-xs font-normal">{g.description}</p>}
										</TableCell>
										<TableCell className="align-top">
											<Badge variant="outline" className="capitalize">
												{g.scope.replace("_", " ")}
											</Badge>
											{g.scope !== "global" && <span className="text-muted-foreground ml-2 text-xs">{g.scope_name || g.scope_id}</span>}
										</TableCell>
										<TableCell className="align-top">
											<div className="flex flex-wrap gap-1">
												{g.tools.length === 0 && <span className="text-muted-foreground text-xs">none</span>}
												{g.tools.map((t) => (
													<Badge key={`${t.client_id}:${t.tool_name}`} variant="secondary" className="text-xs">
														{t.tool_name}
													</Badge>
												))}
											</div>
										</TableCell>
										<TableCell className="w-px text-right align-top">
											<Switch
												size="md"
												checked={g.enabled}
												disabled={!canUpdate}
												onAsyncCheckedChange={(checked) => handleToggleEnabled(g, checked)}
											/>
										</TableCell>
										<TableCell className="w-px align-top">
											<div className="flex items-center gap-1">
												<Button variant="outline" size="sm" disabled={!canUpdate} onClick={() => openEdit(g)}>
													Edit
												</Button>
												<Button
													variant="ghost"
													size="icon"
													disabled={!canDelete}
													onClick={() => setDeleteTarget(g)}
													aria-label={`Delete ${g.name}`}
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

			<ToolGroupDialog
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
						<AlertDialogTitle>Delete MCP tool group?</AlertDialogTitle>
						<AlertDialogDescription>
							This will remove "{deleteTarget?.name}" and stop including its tools in MCP visibility resolution. This cannot be undone.
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

interface ToolGroupDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	draft: Draft;
	setDraft: (d: Draft) => void;
	onSave: () => void;
	saving: boolean;
}

function ToolGroupDialog({ open, onOpenChange, draft, setDraft, onSave, saving }: ToolGroupDialogProps) {
	const isEdit = !!draft.id;

	function removeTool(index: number) {
		setDraft({ ...draft, tools: draft.tools.filter((_, i) => i !== index) });
	}

	function addTool(ref: MCPToolRef) {
		const exists = draft.tools.some((t) => t.client_id === ref.client_id && t.tool_name === ref.tool_name);
		if (exists) return;
		setDraft({ ...draft, tools: [...draft.tools, ref] });
	}

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
				<DialogHeader>
					<DialogTitle>{isEdit ? "Edit MCP tool group" : "New MCP tool group"}</DialogTitle>
					<DialogDescription>
						Group one or more MCP tools under a named, scoped bundle. Bind it to a virtual key or team so MCP visibility filtering can
						include the whole group at once.
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4">
					<div className="space-y-2">
						<Label htmlFor="tool-group-name">Name</Label>
						<Input
							id="tool-group-name"
							value={draft.name}
							onChange={(e) => setDraft({ ...draft, name: e.target.value })}
							placeholder="e.g. Read-only filesystem tools"
						/>
					</div>

					<div className="space-y-2">
						<Label htmlFor="tool-group-description">Description</Label>
						<Textarea
							id="tool-group-description"
							value={draft.description}
							onChange={(e) => setDraft({ ...draft, description: e.target.value })}
							placeholder="Optional. What this group is for."
						/>
					</div>

					<div className="grid grid-cols-2 gap-4">
						<div className="space-y-2">
							<Label>Scope</Label>
							<Select value={draft.scope} disabled={isEdit} onValueChange={(v) => setDraft({ ...draft, scope: v as MCPToolGroupScope })}>
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
								<Label htmlFor="tool-group-scope-id">Scope target ID</Label>
								<Input
									id="tool-group-scope-id"
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
							<Label>Tools</Label>
							<AddToolMenu onAdd={addTool} />
						</div>
						{draft.tools.length === 0 && <p className="text-muted-foreground text-sm">No tools added yet.</p>}
						<div className="flex flex-wrap gap-2">
							{draft.tools.map((t, index) => (
								<Badge key={`${t.client_id}:${t.tool_name}`} variant="secondary" className="gap-1 text-xs">
									{t.tool_name}
									<span className="text-muted-foreground">({t.client_id})</span>
									<button type="button" onClick={() => removeTool(index)} aria-label={`Remove ${t.tool_name}`} className="ml-1">
										<Trash2 className="size-3 text-red-500" />
									</button>
								</Badge>
							))}
						</div>
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

// AddToolMenu lets the operator pick a discovered MCP tool (client + tool) to
// add to the group. Tools are sourced from the live MCP clients list.
function AddToolMenu({ onAdd }: { onAdd: (ref: MCPToolRef) => void }) {
	const { data } = useGetMCPClientsQuery();

	const options = useMemo(() => {
		const out: { value: string; label: string; ref: MCPToolRef }[] = [];
		for (const client of data?.clients ?? []) {
			const clientId = client.config.client_id;
			const clientName = client.config.name || clientId;
			for (const tool of client.tools ?? []) {
				out.push({
					value: `${clientId} ${tool.name}`,
					label: `${clientName} / ${tool.name}`,
					ref: { client_id: clientId, tool_name: tool.name },
				});
			}
		}
		return out;
	}, [data]);

	return (
		<Select
			value=""
			onValueChange={(v) => {
				const opt = options.find((o) => o.value === v);
				if (opt) onAdd(opt.ref);
			}}
		>
			<SelectTrigger className="w-72">
				<SelectValue placeholder={options.length ? "Add tool..." : "No MCP tools discovered"} />
			</SelectTrigger>
			<SelectContent>
				{options.map((o) => (
					<SelectItem key={o.value} value={o.value}>
						{o.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}