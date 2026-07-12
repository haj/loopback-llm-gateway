// Admin API keys management UI for the open-source Loopback Gateway build.
// Manages scope-based admin-plane API keys ("lbk_" bearer tokens for the
// management API — distinct from governance virtual keys). Scopes reuse the
// RBAC resource/operation vocabulary. Secrets are shown exactly once, on
// create and rotate. Mirrors the rbacView.tsx patterns (RTK-query hooks +
// dialog-driven CRUD).
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
import { useCopyToClipboard } from "@/hooks/useCopyToClipboard";
import { getErrorMessage } from "@/lib/store";
import {
	useCreateAPIKeyMutation,
	useDeleteAPIKeyMutation,
	useGetAPIKeysQuery,
	useRevokeAPIKeyMutation,
	useRotateAPIKeyMutation,
} from "@/lib/store/apis/apiKeysApi";
import { AdminAPIKey } from "@/lib/types/apikeys";
import { PermissionInput } from "@/lib/types/rbac";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Copy, KeyRound, Plus, RefreshCw, ShieldOff, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

const WILDCARD = "*";
const RESOURCE_OPTIONS = [WILDCARD, ...Object.values(RbacResource)];
const OPERATION_OPTIONS = [WILDCARD, ...Object.values(RbacOperation)];

interface KeyDraft {
	name: string;
	expiresAt: string; // yyyy-mm-dd or ""
	scopes: PermissionInput[];
}

function emptyKeyDraft(): KeyDraft {
	return { name: "", expiresAt: "", scopes: [{ resource: WILDCARD, operation: WILDCARD }] };
}

function formatDate(value?: string | null): string {
	if (!value) return "—";
	const d = new Date(value);
	return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString();
}

export default function APIKeysView() {
	const canCreate = useRbac(RbacResource.APIKeys, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.APIKeys, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.APIKeys, RbacOperation.Delete);

	const { data, isLoading, isError, error } = useGetAPIKeysQuery();
	const [createKey] = useCreateAPIKeyMutation();
	const [rotateKey] = useRotateAPIKeyMutation();
	const [revokeKey] = useRevokeAPIKeyMutation();
	const [deleteKey] = useDeleteAPIKeyMutation();

	const [createOpen, setCreateOpen] = useState(false);
	const [draft, setDraft] = useState<KeyDraft>(emptyKeyDraft());
	const [saving, setSaving] = useState(false);

	// One-time secret reveal (set after create/rotate, cleared on dismiss).
	const [revealedSecret, setRevealedSecret] = useState<string | null>(null);

	const [rotateTarget, setRotateTarget] = useState<AdminAPIKey | null>(null);
	const [revokeTarget, setRevokeTarget] = useState<AdminAPIKey | null>(null);
	const [deleteTarget, setDeleteTarget] = useState<AdminAPIKey | null>(null);

	const keys = data?.api_keys ?? [];

	async function handleCreate() {
		if (!draft.name.trim()) {
			toast.error("Key name is required");
			return;
		}
		const scopes = draft.scopes.filter((s) => s.resource && s.operation);
		setSaving(true);
		try {
			const resp = await createKey({
				name: draft.name,
				expires_at: draft.expiresAt ? new Date(draft.expiresAt).toISOString() : undefined,
				scopes,
			}).unwrap();
			setCreateOpen(false);
			setDraft(emptyKeyDraft());
			setRevealedSecret(resp.key);
			toast.success("API key created");
		} catch (err) {
			toast.error(getErrorMessage(err));
		} finally {
			setSaving(false);
		}
	}

	async function handleRotate() {
		if (!rotateTarget) return;
		try {
			const resp = await rotateKey(rotateTarget.id).unwrap();
			setRevealedSecret(resp.key);
			toast.success("API key rotated — the old secret no longer works");
		} catch (err) {
			toast.error(getErrorMessage(err));
		} finally {
			setRotateTarget(null);
		}
	}

	async function handleRevoke() {
		if (!revokeTarget) return;
		try {
			await revokeKey(revokeTarget.id).unwrap();
			toast.success("API key revoked");
		} catch (err) {
			toast.error(getErrorMessage(err));
		} finally {
			setRevokeTarget(null);
		}
	}

	async function handleDelete() {
		if (!deleteTarget) return;
		try {
			await deleteKey(deleteTarget.id).unwrap();
			toast.success("API key deleted");
		} catch (err) {
			toast.error(getErrorMessage(err));
		} finally {
			setDeleteTarget(null);
		}
	}

	return (
		<div className="w-full space-y-4 overflow-auto">
			<section className="space-y-4">
				<div className="flex items-start justify-between gap-4">
					<div>
						<h2 className="flex items-center gap-2 text-lg font-semibold tracking-tight">
							<KeyRound className="size-5" /> Admin API keys
						</h2>
						<p className="text-muted-foreground text-sm">
							Long-lived bearer tokens ("lbk_…") for automating the management API. Each key carries resource/operation scopes from the RBAC
							vocabulary; requests outside a key's scopes are rejected. These are separate from inference virtual keys.
						</p>
					</div>
					<Button
						onClick={() => {
							setDraft(emptyKeyDraft());
							setCreateOpen(true);
						}}
						disabled={!canCreate}
						data-testid="api-key-create-button"
					>
						<Plus className="size-4" /> New API key
					</Button>
				</div>

				{isLoading && <p className="text-muted-foreground text-sm">Loading API keys...</p>}
				{isError && <p className="text-sm text-red-500">Failed to load API keys: {getErrorMessage(error)}</p>}

				{!isLoading && !isError && (
					<div className="overflow-auto rounded-sm border">
						<Table data-testid="api-keys-table">
							<TableHeader>
								<TableRow className="bg-muted/50">
									<TableHead className="font-semibold">Name</TableHead>
									<TableHead className="font-semibold">Key</TableHead>
									<TableHead className="font-semibold">Scopes</TableHead>
									<TableHead className="w-px font-semibold">Status</TableHead>
									<TableHead className="font-semibold">Last used</TableHead>
									<TableHead className="font-semibold">Expires</TableHead>
									<TableHead className="w-px font-semibold" />
								</TableRow>
							</TableHeader>
							<TableBody>
								{keys.length === 0 ? (
									<TableRow data-testid="api-keys-table-empty-state">
										<TableCell colSpan={7} className="h-24 text-center">
											<span className="text-muted-foreground text-sm">No API keys yet. Nothing changes until one is created.</span>
										</TableCell>
									</TableRow>
								) : (
									keys.map((k) => (
										<TableRow key={k.id} className="group hover:bg-muted/50 transition-colors">
											<TableCell className="align-top font-medium">{k.name}</TableCell>
											<TableCell className="align-top font-mono text-xs">{k.key_prefix}…</TableCell>
											<TableCell className="align-top">
												<div className="flex flex-wrap gap-1">
													{(k.scopes ?? []).length === 0 ? (
														<span className="text-muted-foreground text-xs">none (grants nothing)</span>
													) : (
														(k.scopes ?? []).map((s, i) => (
															<Badge key={`${s.resource}-${s.operation}-${i}`} variant="outline" className="text-xs">
																{s.resource}:{s.operation}
															</Badge>
														))
													)}
												</div>
											</TableCell>
											<TableCell className="w-px align-top">
												<Badge variant={k.status === "active" ? "secondary" : "destructive"} className="capitalize">
													{k.status}
												</Badge>
											</TableCell>
											<TableCell className="text-muted-foreground align-top text-sm">{formatDate(k.last_used_at)}</TableCell>
											<TableCell className="text-muted-foreground align-top text-sm">{formatDate(k.expires_at)}</TableCell>
											<TableCell className="w-px align-top">
												<div className="flex items-center gap-1">
													<Button
														variant="outline"
														size="sm"
														disabled={!canUpdate}
														onClick={() => setRotateTarget(k)}
														data-testid={`api-key-rotate-button-${k.id}`}
													>
														<RefreshCw className="size-4" /> Rotate
													</Button>
													<Button
														variant="outline"
														size="sm"
														disabled={!canUpdate || k.status === "revoked"}
														onClick={() => setRevokeTarget(k)}
														data-testid={`api-key-revoke-button-${k.id}`}
													>
														<ShieldOff className="size-4" /> Revoke
													</Button>
													<Button
														variant="ghost"
														size="icon"
														disabled={!canDelete}
														onClick={() => setDeleteTarget(k)}
														aria-label={`Delete ${k.name}`}
														data-testid={`api-key-delete-button-${k.id}`}
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

			<CreateKeyDialog
				open={createOpen}
				onOpenChange={setCreateOpen}
				draft={draft}
				setDraft={setDraft}
				onSave={handleCreate}
				saving={saving}
			/>

			<SecretRevealDialog secret={revealedSecret} onDismiss={() => setRevealedSecret(null)} />

			<AlertDialog open={!!rotateTarget} onOpenChange={(o) => !o && setRotateTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Rotate this key?</AlertDialogTitle>
						<AlertDialogDescription>
							A new secret is minted for "{rotateTarget?.name}" with the same scopes and expiry. The current secret stops working
							immediately. The new secret is shown once.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction onClick={handleRotate} data-testid="api-key-rotate-confirm-button">
							Rotate
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>

			<AlertDialog open={!!revokeTarget} onOpenChange={(o) => !o && setRevokeTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Revoke this key?</AlertDialogTitle>
						<AlertDialogDescription>
							"{revokeTarget?.name}" will stop authenticating immediately. The entry is kept for audit; rotating it later mints a fresh
							working secret.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction onClick={handleRevoke} data-testid="api-key-revoke-confirm-button">
							Revoke
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>

			<AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete this key?</AlertDialogTitle>
						<AlertDialogDescription>
							This permanently removes "{deleteTarget?.name}" and its scopes. This cannot be undone.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction onClick={handleDelete} data-testid="api-key-delete-confirm-button">
							Delete
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}

interface CreateKeyDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	draft: KeyDraft;
	setDraft: (d: KeyDraft) => void;
	onSave: () => void;
	saving: boolean;
}

function CreateKeyDialog({ open, onOpenChange, draft, setDraft, onSave, saving }: CreateKeyDialogProps) {
	function addScope() {
		setDraft({ ...draft, scopes: [...draft.scopes, { resource: WILDCARD, operation: WILDCARD }] });
	}

	function updateScope(index: number, patch: Partial<PermissionInput>) {
		const next = draft.scopes.slice();
		next[index] = { ...next[index], ...patch };
		setDraft({ ...draft, scopes: next });
	}

	function removeScope(index: number) {
		setDraft({ ...draft, scopes: draft.scopes.filter((_, i) => i !== index) });
	}

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
				<DialogHeader>
					<DialogTitle>New admin API key</DialogTitle>
					<DialogDescription>
						The key only allows requests matching its scopes ("*" is a wildcard). The secret is shown once after creation.
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4">
					<div className="space-y-2">
						<Label htmlFor="api-key-name">Name</Label>
						<Input
							id="api-key-name"
							value={draft.name}
							onChange={(e) => setDraft({ ...draft, name: e.target.value })}
							placeholder="e.g. ci-deployer"
							data-testid="api-key-name-input"
						/>
					</div>
					<div className="space-y-2">
						<Label htmlFor="api-key-expiry">Expiry (optional)</Label>
						<Input
							id="api-key-expiry"
							type="date"
							value={draft.expiresAt}
							onChange={(e) => setDraft({ ...draft, expiresAt: e.target.value })}
							data-testid="api-key-expiry-input"
						/>
					</div>

					<div className="space-y-2">
						<div className="flex items-center justify-between">
							<Label>Scopes</Label>
							<Button type="button" variant="outline" size="sm" onClick={addScope} data-testid="api-key-scope-add-button">
								<Plus className="size-4" /> Add scope
							</Button>
						</div>
						{draft.scopes.length === 0 ? (
							<p className="text-muted-foreground text-sm">No scopes. This key grants nothing until you add one.</p>
						) : (
							<ScrollArea className="max-h-64 rounded-sm border p-2">
								<div className="space-y-2">
									{draft.scopes.map((s, i) => (
										<div key={i} className="flex items-center gap-2">
											<Select value={s.resource} onValueChange={(v) => updateScope(i, { resource: v })}>
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
											<Select value={s.operation} onValueChange={(v) => updateScope(i, { operation: v })}>
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
											<Button variant="ghost" size="icon" onClick={() => removeScope(i)} aria-label="Remove scope">
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
					<Button onClick={onSave} disabled={saving} data-testid="api-key-create-submit-button">
						{saving ? "Creating..." : "Create key"}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

interface SecretRevealDialogProps {
	secret: string | null;
	onDismiss: () => void;
}

function SecretRevealDialog({ secret, onDismiss }: SecretRevealDialogProps) {
	const { copy } = useCopyToClipboard({ successMessage: "API key copied" });

	return (
		<Dialog open={!!secret} onOpenChange={(o) => !o && onDismiss()}>
			<DialogContent className="sm:max-w-xl">
				<DialogHeader>
					<DialogTitle>Copy your API key</DialogTitle>
					<DialogDescription>
						This is the only time the key is shown. Store it securely — it cannot be recovered, only rotated.
					</DialogDescription>
				</DialogHeader>
				<div className="flex items-center gap-2">
					<code
						className="bg-muted flex-1 overflow-x-auto rounded-sm border p-2 font-mono text-xs break-all"
						data-testid="api-key-secret-value"
					>
						{secret}
					</code>
					<Button
						variant="outline"
						size="icon"
						onClick={() => secret && copy(secret)}
						aria-label="Copy API key"
						data-testid="api-key-secret-copy-button"
					>
						<Copy className="size-4" />
					</Button>
				</div>
				<DialogFooter>
					<Button onClick={onDismiss} data-testid="api-key-secret-done-button">
						I have stored the key
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}