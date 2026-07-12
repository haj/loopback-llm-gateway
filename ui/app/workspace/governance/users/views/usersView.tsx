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
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ScrollArea } from "@/components/ui/scrollArea";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getErrorMessage } from "@/lib/store";
import { useGetVirtualKeysQuery } from "@/lib/store/apis/governanceApi";
import {
	useCreateUserMutation,
	useDeleteUserMutation,
	useGetBusinessUnitsQuery,
	useGetUsersQuery,
	useUpdateUserMutation,
} from "@/lib/store/apis/userManagementApi";
import { BudgetInput, RateLimitInput, User, UserStatus } from "@/lib/types/userManagement";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

const NONE_BUSINESS_UNIT = "__none__";

// Draft mirrors the editable shape of a user for the create/edit dialog.
interface Draft {
	id?: string;
	name: string;
	email: string;
	status: UserStatus;
	businessUnitId: string;
	virtualKeyIds: string[];
	budgetEnabled: boolean;
	budget: BudgetInput;
	rateLimitEnabled: boolean;
	rateLimit: RateLimitInput;
}

function emptyDraft(): Draft {
	return {
		name: "",
		email: "",
		status: "active",
		businessUnitId: NONE_BUSINESS_UNIT,
		virtualKeyIds: [],
		budgetEnabled: false,
		budget: { max_limit: 0, reset_duration: "1M" },
		rateLimitEnabled: false,
		rateLimit: {},
	};
}

function draftFromUser(u: User): Draft {
	return {
		id: u.id,
		name: u.name,
		email: u.email,
		status: u.status,
		businessUnitId: u.business_unit_id || NONE_BUSINESS_UNIT,
		virtualKeyIds: u.virtual_key_ids ?? [],
		budgetEnabled: !!u.budget,
		budget: {
			max_limit: u.budget?.max_limit ?? 0,
			reset_duration: u.budget?.reset_duration ?? "1M",
		},
		rateLimitEnabled: !!u.rate_limit,
		rateLimit: {
			token_max_limit: u.rate_limit?.token_max_limit ?? null,
			token_reset_duration: u.rate_limit?.token_reset_duration ?? null,
			request_max_limit: u.rate_limit?.request_max_limit ?? null,
			request_reset_duration: u.rate_limit?.request_reset_duration ?? null,
		},
	};
}

export default function UsersView() {
	const canCreate = useRbac(RbacResource.Users, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.Users, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.Users, RbacOperation.Delete);

	const { data, isLoading, isError, error } = useGetUsersQuery();
	const { data: buData } = useGetBusinessUnitsQuery();
	const [createUser] = useCreateUserMutation();
	const [updateUser] = useUpdateUserMutation();
	const [deleteUser] = useDeleteUserMutation();

	const [dialogOpen, setDialogOpen] = useState(false);
	const [draft, setDraft] = useState<Draft>(emptyDraft());
	const [saving, setSaving] = useState(false);
	const [deleteTarget, setDeleteTarget] = useState<User | null>(null);

	const users = data?.users ?? [];
	const businessUnits = buData?.business_units ?? [];

	function openCreate() {
		setDraft(emptyDraft());
		setDialogOpen(true);
	}

	function openEdit(u: User) {
		setDraft(draftFromUser(u));
		setDialogOpen(true);
	}

	async function handleSave() {
		if (!draft.name.trim()) {
			toast.error("Name is required");
			return;
		}
		if (!draft.email.trim()) {
			toast.error("Email is required");
			return;
		}
		const businessUnitId = draft.businessUnitId === NONE_BUSINESS_UNIT ? "" : draft.businessUnitId;
		const budget = draft.budgetEnabled ? draft.budget : { max_limit: 0, reset_duration: "" };
		const rateLimit = draft.rateLimitEnabled ? draft.rateLimit : { token_max_limit: null, request_max_limit: null };
		setSaving(true);
		try {
			if (draft.id) {
				await updateUser({
					id: draft.id,
					name: draft.name,
					email: draft.email,
					status: draft.status,
					business_unit_id: businessUnitId,
					virtual_key_ids: draft.virtualKeyIds,
					budget,
					rate_limit: rateLimit,
				}).unwrap();
				toast.success("User updated");
			} else {
				await createUser({
					name: draft.name,
					email: draft.email,
					status: draft.status,
					business_unit_id: businessUnitId || null,
					virtual_key_ids: draft.virtualKeyIds,
					...(draft.budgetEnabled ? { budget } : {}),
					...(draft.rateLimitEnabled ? { rate_limit: rateLimit } : {}),
				}).unwrap();
				toast.success("User created");
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
			await deleteUser(deleteTarget.id).unwrap();
			toast.success("User deleted");
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
					<h2 className="text-lg font-semibold tracking-tight">Users</h2>
					<p className="text-muted-foreground text-sm">
						Manage users, assign them to a business unit, attach virtual keys, and set per-user budgets and rate limits.
					</p>
				</div>
				<Button onClick={openCreate} disabled={!canCreate} data-testid="create-user-button">
					<Plus className="size-4" /> New user
				</Button>
			</div>

			{isLoading && <p className="text-muted-foreground text-sm">Loading users...</p>}
			{isError && <p className="text-sm text-red-500">Failed to load users: {getErrorMessage(error)}</p>}

			{!isLoading && !isError && (
				<div className="overflow-auto rounded-sm border">
					<Table data-testid="users-table">
						<TableHeader>
							<TableRow className="bg-muted/50">
								<TableHead className="font-semibold">Name</TableHead>
								<TableHead className="font-semibold">Email</TableHead>
								<TableHead className="font-semibold">Business unit</TableHead>
								<TableHead className="font-semibold">Virtual keys</TableHead>
								<TableHead className="w-px font-semibold">Status</TableHead>
								<TableHead className="w-px font-semibold" />
							</TableRow>
						</TableHeader>
						<TableBody>
							{users.length === 0 ? (
								<TableRow data-testid="users-table-empty-state">
									<TableCell colSpan={6} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">No users yet. Create one to start managing access.</span>
									</TableCell>
								</TableRow>
							) : (
								users.map((u) => (
									<TableRow key={u.id} className="group hover:bg-muted/50 transition-colors">
										<TableCell className="align-top font-medium">{u.name}</TableCell>
										<TableCell className="align-top">{u.email}</TableCell>
										<TableCell className="align-top">
											{u.business_unit?.name ? (
												<Badge variant="outline">{u.business_unit.name}</Badge>
											) : (
												<span className="text-muted-foreground text-xs">none</span>
											)}
										</TableCell>
										<TableCell className="align-top">
											<span className="text-muted-foreground text-xs">
												{u.virtual_key_ids?.length ? `${u.virtual_key_ids.length} attached` : "none"}
											</span>
										</TableCell>
										<TableCell className="w-px align-top">
											<Badge variant={u.status === "active" ? "secondary" : "outline"} className="capitalize">
												{u.status}
											</Badge>
										</TableCell>
										<TableCell className="w-px align-top">
											<div className="flex items-center gap-1">
												<Button variant="outline" size="sm" disabled={!canUpdate} onClick={() => openEdit(u)}>
													Edit
												</Button>
												<Button
													variant="ghost"
													size="icon"
													disabled={!canDelete}
													onClick={() => setDeleteTarget(u)}
													aria-label={`Delete ${u.name}`}
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

			<UserDialog
				open={dialogOpen}
				onOpenChange={setDialogOpen}
				draft={draft}
				setDraft={setDraft}
				onSave={handleSave}
				saving={saving}
				businessUnits={businessUnits.map((b) => ({ id: b.id, name: b.name }))}
			/>

			<AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete user?</AlertDialogTitle>
						<AlertDialogDescription>
							This will permanently remove "{deleteTarget?.name}" and any per-user budget or rate limit. This cannot be undone.
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

interface UserDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	draft: Draft;
	setDraft: (d: Draft) => void;
	onSave: () => void;
	saving: boolean;
	businessUnits: { id: string; name: string }[];
}

function UserDialog({ open, onOpenChange, draft, setDraft, onSave, saving, businessUnits }: UserDialogProps) {
	const isEdit = !!draft.id;
	const { data: vkData } = useGetVirtualKeysQuery();
	const virtualKeys = vkData?.virtual_keys ?? [];

	function toggleVk(id: string, checked: boolean) {
		const set = new Set(draft.virtualKeyIds);
		if (checked) set.add(id);
		else set.delete(id);
		setDraft({ ...draft, virtualKeyIds: Array.from(set) });
	}

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
				<DialogHeader>
					<DialogTitle>{isEdit ? "Edit user" : "New user"}</DialogTitle>
					<DialogDescription>
						Users can be assigned to a business unit, attached to virtual keys, and given per-user budgets and rate limits.
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4">
					<div className="grid grid-cols-2 gap-4">
						<div className="space-y-2">
							<Label htmlFor="user-name">Name</Label>
							<Input
								id="user-name"
								value={draft.name}
								onChange={(e) => setDraft({ ...draft, name: e.target.value })}
								placeholder="e.g. Ada Lovelace"
							/>
						</div>
						<div className="space-y-2">
							<Label htmlFor="user-email">Email</Label>
							<Input
								id="user-email"
								type="email"
								value={draft.email}
								onChange={(e) => setDraft({ ...draft, email: e.target.value })}
								placeholder="e.g. ada@example.com"
							/>
						</div>
					</div>

					<div className="grid grid-cols-2 gap-4">
						<div className="space-y-2">
							<Label>Status</Label>
							<Select value={draft.status} onValueChange={(v) => setDraft({ ...draft, status: v as UserStatus })}>
								<SelectTrigger>
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value="active">Active</SelectItem>
									<SelectItem value="inactive">Inactive</SelectItem>
								</SelectContent>
							</Select>
						</div>
						<div className="space-y-2">
							<Label>Business unit</Label>
							<Select value={draft.businessUnitId} onValueChange={(v) => setDraft({ ...draft, businessUnitId: v })}>
								<SelectTrigger>
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value={NONE_BUSINESS_UNIT}>None</SelectItem>
									{businessUnits.map((b) => (
										<SelectItem key={b.id} value={b.id}>
											{b.name}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
					</div>

					<div className="space-y-2">
						<Label>Attached virtual keys</Label>
						{virtualKeys.length === 0 ? (
							<p className="text-muted-foreground text-sm">No virtual keys available.</p>
						) : (
							<ScrollArea className="h-40 rounded-sm border p-2">
								<div className="space-y-2">
									{virtualKeys.map((vk) => (
										<label key={vk.id} className="flex cursor-pointer items-center gap-2 text-sm">
											<Checkbox checked={draft.virtualKeyIds.includes(vk.id)} onCheckedChange={(c) => toggleVk(vk.id, !!c)} />
											<span>{vk.name}</span>
											{!vk.is_active && (
												<Badge variant="outline" className="text-xs">
													inactive
												</Badge>
											)}
										</label>
									))}
								</div>
							</ScrollArea>
						)}
					</div>

					<BudgetEditor
						enabled={draft.budgetEnabled}
						onEnabledChange={(v) => setDraft({ ...draft, budgetEnabled: v })}
						budget={draft.budget}
						onBudgetChange={(b) => setDraft({ ...draft, budget: b })}
					/>

					<RateLimitEditor
						enabled={draft.rateLimitEnabled}
						onEnabledChange={(v) => setDraft({ ...draft, rateLimitEnabled: v })}
						rateLimit={draft.rateLimit}
						onRateLimitChange={(r) => setDraft({ ...draft, rateLimit: r })}
					/>
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

// BudgetEditor and RateLimitEditor are shared between the users and business
// units views (re-exported below) so the budget / rate-limit reuse surface is
// identical for both entity types.

export interface BudgetEditorProps {
	enabled: boolean;
	onEnabledChange: (v: boolean) => void;
	budget: BudgetInput;
	onBudgetChange: (b: BudgetInput) => void;
}

export function BudgetEditor({ enabled, onEnabledChange, budget, onBudgetChange }: BudgetEditorProps) {
	return (
		<div className="space-y-3 rounded-sm border p-3">
			<div className="flex items-center gap-2">
				<Switch size="md" checked={enabled} onCheckedChange={onEnabledChange} />
				<Label>Budget</Label>
			</div>
			{enabled && (
				<div className="grid grid-cols-2 gap-3">
					<div className="space-y-1.5">
						<Label className="text-xs">Max limit (USD)</Label>
						<Input
							type="number"
							value={budget.max_limit === 0 ? "" : String(budget.max_limit)}
							onChange={(e) => onBudgetChange({ ...budget, max_limit: e.target.value === "" ? 0 : Number(e.target.value) })}
							placeholder="e.g. 100"
						/>
					</div>
					<div className="space-y-1.5">
						<Label className="text-xs">Reset duration</Label>
						<Input
							value={budget.reset_duration}
							onChange={(e) => onBudgetChange({ ...budget, reset_duration: e.target.value })}
							placeholder="e.g. 1M, 1w, 1d"
						/>
					</div>
				</div>
			)}
		</div>
	);
}

export interface RateLimitEditorProps {
	enabled: boolean;
	onEnabledChange: (v: boolean) => void;
	rateLimit: RateLimitInput;
	onRateLimitChange: (r: RateLimitInput) => void;
}

export function RateLimitEditor({ enabled, onEnabledChange, rateLimit, onRateLimitChange }: RateLimitEditorProps) {
	function num(v: number | null | undefined): string {
		return v === null || v === undefined ? "" : String(v);
	}
	return (
		<div className="space-y-3 rounded-sm border p-3">
			<div className="flex items-center gap-2">
				<Switch size="md" checked={enabled} onCheckedChange={onEnabledChange} />
				<Label>Rate limit</Label>
			</div>
			{enabled && (
				<div className="grid grid-cols-2 gap-3">
					<div className="space-y-1.5">
						<Label className="text-xs">Token max limit</Label>
						<Input
							type="number"
							value={num(rateLimit.token_max_limit)}
							onChange={(e) =>
								onRateLimitChange({
									...rateLimit,
									token_max_limit: e.target.value === "" ? null : Number(e.target.value),
								})
							}
						/>
					</div>
					<div className="space-y-1.5">
						<Label className="text-xs">Token reset duration</Label>
						<Input
							value={rateLimit.token_reset_duration ?? ""}
							onChange={(e) => onRateLimitChange({ ...rateLimit, token_reset_duration: e.target.value || null })}
							placeholder="e.g. 1h, 1d"
						/>
					</div>
					<div className="space-y-1.5">
						<Label className="text-xs">Request max limit</Label>
						<Input
							type="number"
							value={num(rateLimit.request_max_limit)}
							onChange={(e) =>
								onRateLimitChange({
									...rateLimit,
									request_max_limit: e.target.value === "" ? null : Number(e.target.value),
								})
							}
						/>
					</div>
					<div className="space-y-1.5">
						<Label className="text-xs">Request reset duration</Label>
						<Input
							value={rateLimit.request_reset_duration ?? ""}
							onChange={(e) => onRateLimitChange({ ...rateLimit, request_reset_duration: e.target.value || null })}
							placeholder="e.g. 1h, 1d"
						/>
					</div>
				</div>
			)}
		</div>
	);
}