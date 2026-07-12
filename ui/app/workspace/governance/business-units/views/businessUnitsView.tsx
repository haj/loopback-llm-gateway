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
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { getErrorMessage } from "@/lib/store";
import {
	useCreateBusinessUnitMutation,
	useDeleteBusinessUnitMutation,
	useGetBusinessUnitsQuery,
	useUpdateBusinessUnitMutation,
} from "@/lib/store/apis/userManagementApi";
import { BudgetInput, BusinessUnit, RateLimitInput } from "@/lib/types/userManagement";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { BudgetEditor, RateLimitEditor } from "../../users/views/usersView";

// Draft mirrors the editable shape of a business unit for the create/edit dialog.
interface Draft {
	id?: string;
	name: string;
	description: string;
	budgetEnabled: boolean;
	budget: BudgetInput;
	rateLimitEnabled: boolean;
	rateLimit: RateLimitInput;
}

function emptyDraft(): Draft {
	return {
		name: "",
		description: "",
		budgetEnabled: false,
		budget: { max_limit: 0, reset_duration: "1M" },
		rateLimitEnabled: false,
		rateLimit: {},
	};
}

function draftFromUnit(b: BusinessUnit): Draft {
	return {
		id: b.id,
		name: b.name,
		description: b.description,
		budgetEnabled: !!b.budget,
		budget: {
			max_limit: b.budget?.max_limit ?? 0,
			reset_duration: b.budget?.reset_duration ?? "1M",
		},
		rateLimitEnabled: !!b.rate_limit,
		rateLimit: {
			token_max_limit: b.rate_limit?.token_max_limit ?? null,
			token_reset_duration: b.rate_limit?.token_reset_duration ?? null,
			request_max_limit: b.rate_limit?.request_max_limit ?? null,
			request_reset_duration: b.rate_limit?.request_reset_duration ?? null,
		},
	};
}

export function BusinessUnitsView() {
	const canCreate = useRbac(RbacResource.Customers, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.Customers, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.Customers, RbacOperation.Delete);

	const { data, isLoading, isError, error } = useGetBusinessUnitsQuery();
	const [createBusinessUnit] = useCreateBusinessUnitMutation();
	const [updateBusinessUnit] = useUpdateBusinessUnitMutation();
	const [deleteBusinessUnit] = useDeleteBusinessUnitMutation();

	const [dialogOpen, setDialogOpen] = useState(false);
	const [draft, setDraft] = useState<Draft>(emptyDraft());
	const [saving, setSaving] = useState(false);
	const [deleteTarget, setDeleteTarget] = useState<BusinessUnit | null>(null);

	const units = data?.business_units ?? [];

	function openCreate() {
		setDraft(emptyDraft());
		setDialogOpen(true);
	}

	function openEdit(b: BusinessUnit) {
		setDraft(draftFromUnit(b));
		setDialogOpen(true);
	}

	async function handleSave() {
		if (!draft.name.trim()) {
			toast.error("Name is required");
			return;
		}
		const budget = draft.budgetEnabled ? draft.budget : { max_limit: 0, reset_duration: "" };
		const rateLimit = draft.rateLimitEnabled ? draft.rateLimit : { token_max_limit: null, request_max_limit: null };
		setSaving(true);
		try {
			if (draft.id) {
				await updateBusinessUnit({
					id: draft.id,
					name: draft.name,
					description: draft.description,
					budget,
					rate_limit: rateLimit,
				}).unwrap();
				toast.success("Business unit updated");
			} else {
				await createBusinessUnit({
					name: draft.name,
					description: draft.description,
					...(draft.budgetEnabled ? { budget } : {}),
					...(draft.rateLimitEnabled ? { rate_limit: rateLimit } : {}),
				}).unwrap();
				toast.success("Business unit created");
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
			await deleteBusinessUnit(deleteTarget.id).unwrap();
			toast.success("Business unit deleted");
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
					<h2 className="text-lg font-semibold tracking-tight">Business units</h2>
					<p className="text-muted-foreground text-sm">
						Group users into flat business units and set an org-wide budget or rate limit for each.
					</p>
				</div>
				<Button onClick={openCreate} disabled={!canCreate} data-testid="create-business-unit-button">
					<Plus className="size-4" /> New business unit
				</Button>
			</div>

			{isLoading && <p className="text-muted-foreground text-sm">Loading business units...</p>}
			{isError && <p className="text-sm text-red-500">Failed to load business units: {getErrorMessage(error)}</p>}

			{!isLoading && !isError && (
				<div className="overflow-auto rounded-sm border">
					<Table data-testid="business-units-table">
						<TableHeader>
							<TableRow className="bg-muted/50">
								<TableHead className="font-semibold">Name</TableHead>
								<TableHead className="font-semibold">Description</TableHead>
								<TableHead className="w-px font-semibold">Users</TableHead>
								<TableHead className="w-px font-semibold" />
							</TableRow>
						</TableHeader>
						<TableBody>
							{units.length === 0 ? (
								<TableRow data-testid="business-units-table-empty-state">
									<TableCell colSpan={4} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">No business units yet. Create one to start grouping users.</span>
									</TableCell>
								</TableRow>
							) : (
								units.map((b) => (
									<TableRow key={b.id} className="group hover:bg-muted/50 transition-colors">
										<TableCell className="align-top font-medium">{b.name}</TableCell>
										<TableCell className="align-top">
											{b.description ? (
												<span className="text-sm">{b.description}</span>
											) : (
												<span className="text-muted-foreground text-xs">—</span>
											)}
										</TableCell>
										<TableCell className="w-px align-top">
											<Badge variant="secondary">{b.user_count}</Badge>
										</TableCell>
										<TableCell className="w-px align-top">
											<div className="flex items-center gap-1">
												<Button variant="outline" size="sm" disabled={!canUpdate} onClick={() => openEdit(b)}>
													Edit
												</Button>
												<Button
													variant="ghost"
													size="icon"
													disabled={!canDelete}
													onClick={() => setDeleteTarget(b)}
													aria-label={`Delete ${b.name}`}
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

			<Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
				<DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
					<DialogHeader>
						<DialogTitle>{draft.id ? "Edit business unit" : "New business unit"}</DialogTitle>
						<DialogDescription>
							Business units are flat: they group users without nesting. Attach an optional shared budget or rate limit.
						</DialogDescription>
					</DialogHeader>

					<div className="space-y-4">
						<div className="space-y-2">
							<Label htmlFor="bu-name">Name</Label>
							<Input
								id="bu-name"
								value={draft.name}
								onChange={(e) => setDraft({ ...draft, name: e.target.value })}
								placeholder="e.g. Platform Engineering"
							/>
						</div>
						<div className="space-y-2">
							<Label htmlFor="bu-description">Description</Label>
							<Textarea
								id="bu-description"
								value={draft.description}
								onChange={(e) => setDraft({ ...draft, description: e.target.value })}
								placeholder="Optional description"
							/>
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
						<Button variant="outline" onClick={() => setDialogOpen(false)} disabled={saving}>
							Cancel
						</Button>
						<Button onClick={handleSave} disabled={saving}>
							{saving ? "Saving..." : draft.id ? "Save changes" : "Create"}
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>

			<AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete business unit?</AlertDialogTitle>
						<AlertDialogDescription>
							This will remove "{deleteTarget?.name}". Users in it will be detached but not deleted. This cannot be undone.
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