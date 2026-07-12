// RBAC & access control management UI for the open-source Loopback Gateway
// build. It manages roles (each a set of resource/operation permission grants)
// and role assignments that bind a role to a managed user. Mirrors the
// usersView.tsx patterns (RTK-query hooks + dialog-driven CRUD).
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
import { ScrollArea } from "@/components/ui/scrollArea";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getErrorMessage } from "@/lib/store";
import {
	useCreateRoleAssignmentMutation,
	useCreateRoleMutation,
	useDeleteRoleAssignmentMutation,
	useDeleteRoleMutation,
	useGetRoleAssignmentsQuery,
	useGetRolesQuery,
	useUpdateRoleMutation,
} from "@/lib/store/apis/rbacApi";
import { useGetUsersQuery } from "@/lib/store/apis/userManagementApi";
import { PermissionInput, Role, RoleAssignment } from "@/lib/types/rbac";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

const WILDCARD = "*";
const RESOURCE_OPTIONS = [WILDCARD, ...Object.values(RbacResource)];
const OPERATION_OPTIONS = [WILDCARD, ...Object.values(RbacOperation)];

interface RoleDraft {
	id?: string;
	name: string;
	description: string;
	isSystem: boolean;
	permissions: PermissionInput[];
}

function emptyRoleDraft(): RoleDraft {
	return { name: "", description: "", isSystem: false, permissions: [] };
}

function draftFromRole(r: Role): RoleDraft {
	return {
		id: r.id,
		name: r.name,
		description: r.description,
		isSystem: r.is_system,
		permissions: (r.permissions ?? []).map((p) => ({ resource: p.resource, operation: p.operation })),
	};
}

export default function RBACView() {
	const canCreate = useRbac(RbacResource.RBAC, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.RBAC, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.RBAC, RbacOperation.Delete);

	const { data: rolesData, isLoading, isError, error } = useGetRolesQuery();
	const { data: assignmentsData } = useGetRoleAssignmentsQuery();
	const { data: usersData } = useGetUsersQuery();

	const [createRole] = useCreateRoleMutation();
	const [updateRole] = useUpdateRoleMutation();
	const [deleteRole] = useDeleteRoleMutation();
	const [createAssignment] = useCreateRoleAssignmentMutation();
	const [deleteAssignment] = useDeleteRoleAssignmentMutation();

	const [roleDialogOpen, setRoleDialogOpen] = useState(false);
	const [roleDraft, setRoleDraft] = useState<RoleDraft>(emptyRoleDraft());
	const [savingRole, setSavingRole] = useState(false);
	const [deleteRoleTarget, setDeleteRoleTarget] = useState<Role | null>(null);

	const [assignDialogOpen, setAssignDialogOpen] = useState(false);
	const [deleteAssignTarget, setDeleteAssignTarget] = useState<RoleAssignment | null>(null);

	const roles = rolesData?.roles ?? [];
	const assignments = assignmentsData?.role_assignments ?? [];
	const users = usersData?.users ?? [];

	function openCreateRole() {
		setRoleDraft(emptyRoleDraft());
		setRoleDialogOpen(true);
	}

	function openEditRole(r: Role) {
		setRoleDraft(draftFromRole(r));
		setRoleDialogOpen(true);
	}

	async function handleSaveRole() {
		if (!roleDraft.name.trim()) {
			toast.error("Role name is required");
			return;
		}
		const permissions = roleDraft.permissions.filter((p) => p.resource && p.operation);
		setSavingRole(true);
		try {
			if (roleDraft.id) {
				await updateRole({
					id: roleDraft.id,
					name: roleDraft.name,
					description: roleDraft.description,
					permissions,
				}).unwrap();
				toast.success("Role updated");
			} else {
				await createRole({
					name: roleDraft.name,
					description: roleDraft.description,
					permissions,
				}).unwrap();
				toast.success("Role created");
			}
			setRoleDialogOpen(false);
		} catch (err) {
			toast.error(getErrorMessage(err));
		} finally {
			setSavingRole(false);
		}
	}

	async function handleDeleteRole() {
		if (!deleteRoleTarget) return;
		try {
			await deleteRole(deleteRoleTarget.id).unwrap();
			toast.success("Role deleted");
		} catch (err) {
			toast.error(getErrorMessage(err));
		} finally {
			setDeleteRoleTarget(null);
		}
	}

	async function handleDeleteAssignment() {
		if (!deleteAssignTarget) return;
		try {
			await deleteAssignment(deleteAssignTarget.id).unwrap();
			toast.success("Assignment removed");
		} catch (err) {
			toast.error(getErrorMessage(err));
		} finally {
			setDeleteAssignTarget(null);
		}
	}

	function userLabel(a: RoleAssignment): string {
		if (a.user?.name) return `${a.user.name} (${a.user.email})`;
		const u = users.find((x) => x.id === a.user_id);
		return u ? `${u.name} (${u.email})` : a.user_id;
	}

	return (
		<div className="w-full space-y-8 overflow-auto">
			{/* Roles */}
			<section className="space-y-4">
				<div className="flex items-start justify-between gap-4">
					<div>
						<h2 className="text-lg font-semibold tracking-tight">Roles & permissions</h2>
						<p className="text-muted-foreground text-sm">
							Define roles as sets of resource/operation grants, then assign them to users. RBAC enforcement is fail-open: until it is
							enabled and at least one assignment exists, every request is permitted.
						</p>
					</div>
					<Button onClick={openCreateRole} disabled={!canCreate} data-testid="create-role-button">
						<Plus className="size-4" /> New role
					</Button>
				</div>

				{isLoading && <p className="text-muted-foreground text-sm">Loading roles...</p>}
				{isError && <p className="text-sm text-red-500">Failed to load roles: {getErrorMessage(error)}</p>}

				{!isLoading && !isError && (
					<div className="overflow-auto rounded-sm border">
						<Table data-testid="roles-table">
							<TableHeader>
								<TableRow className="bg-muted/50">
									<TableHead className="font-semibold">Name</TableHead>
									<TableHead className="font-semibold">Description</TableHead>
									<TableHead className="font-semibold">Permissions</TableHead>
									<TableHead className="w-px font-semibold">Type</TableHead>
									<TableHead className="w-px font-semibold" />
								</TableRow>
							</TableHeader>
							<TableBody>
								{roles.length === 0 ? (
									<TableRow data-testid="roles-table-empty-state">
										<TableCell colSpan={5} className="h-24 text-center">
											<span className="text-muted-foreground text-sm">No roles yet.</span>
										</TableCell>
									</TableRow>
								) : (
									roles.map((r) => (
										<TableRow key={r.id} className="group hover:bg-muted/50 transition-colors">
											<TableCell className="align-top font-medium">{r.name}</TableCell>
											<TableCell className="text-muted-foreground align-top text-sm">{r.description || "—"}</TableCell>
											<TableCell className="align-top">
												<div className="flex flex-wrap gap-1">
													{(r.permissions ?? []).length === 0 ? (
														<span className="text-muted-foreground text-xs">none</span>
													) : (
														(r.permissions ?? []).map((p, i) => (
															<Badge key={`${p.resource}-${p.operation}-${i}`} variant="outline" className="text-xs">
																{p.resource}:{p.operation}
															</Badge>
														))
													)}
												</div>
											</TableCell>
											<TableCell className="w-px align-top">
												<Badge variant={r.is_system ? "secondary" : "outline"} className="capitalize">
													{r.is_system ? "system" : "custom"}
												</Badge>
											</TableCell>
											<TableCell className="w-px align-top">
												<div className="flex items-center gap-1">
													<Button variant="outline" size="sm" disabled={!canUpdate} onClick={() => openEditRole(r)}>
														Edit
													</Button>
													<Button
														variant="ghost"
														size="icon"
														disabled={!canDelete || r.is_system}
														onClick={() => setDeleteRoleTarget(r)}
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
			</section>

			{/* Assignments */}
			<section className="space-y-4">
				<div className="flex items-start justify-between gap-4">
					<div>
						<h2 className="text-lg font-semibold tracking-tight">Role assignments</h2>
						<p className="text-muted-foreground text-sm">Bind a role to a managed user.</p>
					</div>
					<Button
						onClick={() => setAssignDialogOpen(true)}
						disabled={!canCreate || roles.length === 0 || users.length === 0}
						data-testid="create-assignment-button"
					>
						<Plus className="size-4" /> New assignment
					</Button>
				</div>

				<div className="overflow-auto rounded-sm border">
					<Table data-testid="assignments-table">
						<TableHeader>
							<TableRow className="bg-muted/50">
								<TableHead className="font-semibold">User</TableHead>
								<TableHead className="font-semibold">Role</TableHead>
								<TableHead className="w-px font-semibold" />
							</TableRow>
						</TableHeader>
						<TableBody>
							{assignments.length === 0 ? (
								<TableRow data-testid="assignments-table-empty-state">
									<TableCell colSpan={3} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">No assignments. While none exist, RBAC stays permissive.</span>
									</TableCell>
								</TableRow>
							) : (
								assignments.map((a) => (
									<TableRow key={a.id} className="group hover:bg-muted/50 transition-colors">
										<TableCell className="align-top">{userLabel(a)}</TableCell>
										<TableCell className="align-top">
											<Badge variant="outline">{a.role?.name ?? a.role_id}</Badge>
										</TableCell>
										<TableCell className="w-px align-top">
											<Button
												variant="ghost"
												size="icon"
												disabled={!canDelete}
												onClick={() => setDeleteAssignTarget(a)}
												aria-label="Remove assignment"
											>
												<Trash2 className="size-4 text-red-500" />
											</Button>
										</TableCell>
									</TableRow>
								))
							)}
						</TableBody>
					</Table>
				</div>
			</section>

			<RoleDialog
				open={roleDialogOpen}
				onOpenChange={setRoleDialogOpen}
				draft={roleDraft}
				setDraft={setRoleDraft}
				onSave={handleSaveRole}
				saving={savingRole}
			/>

			<AssignmentDialog
				open={assignDialogOpen}
				onOpenChange={setAssignDialogOpen}
				roles={roles.map((r) => ({ id: r.id, name: r.name }))}
				users={users.map((u) => ({ id: u.id, label: `${u.name} (${u.email})` }))}
				onCreate={async (roleId, userId) => {
					try {
						await createAssignment({ role_id: roleId, user_id: userId }).unwrap();
						toast.success("Assignment created");
						setAssignDialogOpen(false);
					} catch (err) {
						toast.error(getErrorMessage(err));
					}
				}}
			/>

			<AlertDialog open={!!deleteRoleTarget} onOpenChange={(o) => !o && setDeleteRoleTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete role?</AlertDialogTitle>
						<AlertDialogDescription>
							This permanently removes "{deleteRoleTarget?.name}" and all its assignments. This cannot be undone.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction onClick={handleDeleteRole}>Delete</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>

			<AlertDialog open={!!deleteAssignTarget} onOpenChange={(o) => !o && setDeleteAssignTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Remove assignment?</AlertDialogTitle>
						<AlertDialogDescription>This removes the role from the user. This cannot be undone.</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction onClick={handleDeleteAssignment}>Remove</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}

interface RoleDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	draft: RoleDraft;
	setDraft: (d: RoleDraft) => void;
	onSave: () => void;
	saving: boolean;
}

function RoleDialog({ open, onOpenChange, draft, setDraft, onSave, saving }: RoleDialogProps) {
	const isEdit = !!draft.id;

	function addPermission() {
		setDraft({
			...draft,
			permissions: [...draft.permissions, { resource: WILDCARD, operation: WILDCARD }],
		});
	}

	function updatePermission(index: number, patch: Partial<PermissionInput>) {
		const next = draft.permissions.slice();
		next[index] = { ...next[index], ...patch };
		setDraft({ ...draft, permissions: next });
	}

	function removePermission(index: number) {
		setDraft({ ...draft, permissions: draft.permissions.filter((_, i) => i !== index) });
	}

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
				<DialogHeader>
					<DialogTitle>{isEdit ? "Edit role" : "New role"}</DialogTitle>
					<DialogDescription>
						A role grants a set of resource/operation permissions. Use the "*" wildcard for full access.
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4">
					<div className="space-y-2">
						<Label htmlFor="role-name">Name</Label>
						<Input
							id="role-name"
							value={draft.name}
							disabled={draft.isSystem}
							onChange={(e) => setDraft({ ...draft, name: e.target.value })}
							placeholder="e.g. Auditor"
						/>
						{draft.isSystem && <p className="text-muted-foreground text-xs">The built-in system role cannot be renamed.</p>}
					</div>
					<div className="space-y-2">
						<Label htmlFor="role-description">Description</Label>
						<Input
							id="role-description"
							value={draft.description}
							onChange={(e) => setDraft({ ...draft, description: e.target.value })}
							placeholder="What this role is for"
						/>
					</div>

					<div className="space-y-2">
						<div className="flex items-center justify-between">
							<Label>Permissions</Label>
							<Button type="button" variant="outline" size="sm" onClick={addPermission}>
								<Plus className="size-4" /> Add permission
							</Button>
						</div>
						{draft.permissions.length === 0 ? (
							<p className="text-muted-foreground text-sm">No permissions. This role grants nothing until you add one.</p>
						) : (
							<ScrollArea className="max-h-64 rounded-sm border p-2">
								<div className="space-y-2">
									{draft.permissions.map((p, i) => (
										<div key={i} className="flex items-center gap-2">
											<Select value={p.resource} onValueChange={(v) => updatePermission(i, { resource: v })}>
												<SelectTrigger className="flex-1">
													<SelectValue />
												</SelectTrigger>
												<SelectContent>
													{RESOURCE_OPTIONS.map((r) => (
														<SelectItem key={r} value={r}>
															{r}
														</SelectItem>
													))}
												</SelectContent>
											</Select>
											<Select value={p.operation} onValueChange={(v) => updatePermission(i, { operation: v })}>
												<SelectTrigger className="w-40">
													<SelectValue />
												</SelectTrigger>
												<SelectContent>
													{OPERATION_OPTIONS.map((o) => (
														<SelectItem key={o} value={o}>
															{o}
														</SelectItem>
													))}
												</SelectContent>
											</Select>
											<Button variant="ghost" size="icon" onClick={() => removePermission(i)} aria-label="Remove permission">
												<Trash2 className="size-4 text-red-500" />
											</Button>
										</div>
									))}
								</div>
							</ScrollArea>
						)}
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

interface AssignmentDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	roles: { id: string; name: string }[];
	users: { id: string; label: string }[];
	onCreate: (roleId: string, userId: string) => void;
}

function AssignmentDialog({ open, onOpenChange, roles, users, onCreate }: AssignmentDialogProps) {
	const [userId, setUserId] = useState("");
	const [roleId, setRoleId] = useState("");

	return (
		<Dialog
			open={open}
			onOpenChange={(o) => {
				if (!o) {
					setUserId("");
					setRoleId("");
				}
				onOpenChange(o);
			}}
		>
			<DialogContent className="sm:max-w-md">
				<DialogHeader>
					<DialogTitle>New assignment</DialogTitle>
					<DialogDescription>Grant a role to a user.</DialogDescription>
				</DialogHeader>
				<div className="space-y-4">
					<div className="space-y-2">
						<Label>User</Label>
						<Select value={userId} onValueChange={setUserId}>
							<SelectTrigger>
								<SelectValue placeholder="Select a user" />
							</SelectTrigger>
							<SelectContent>
								{users.map((u) => (
									<SelectItem key={u.id} value={u.id}>
										{u.label}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
					<div className="space-y-2">
						<Label>Role</Label>
						<Select value={roleId} onValueChange={setRoleId}>
							<SelectTrigger>
								<SelectValue placeholder="Select a role" />
							</SelectTrigger>
							<SelectContent>
								{roles.map((r) => (
									<SelectItem key={r.id} value={r.id}>
										{r.name}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
				</div>
				<DialogFooter>
					<Button variant="outline" onClick={() => onOpenChange(false)}>
						Cancel
					</Button>
					<Button onClick={() => onCreate(roleId, userId)} disabled={!userId || !roleId}>
						Assign
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}