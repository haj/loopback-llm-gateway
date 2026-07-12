// Secure-setup wizard: a guided checklist over the
// deployment's security posture (dashboard auth, inference auth, RBAC
// enforcement) with a one-click RBAC enforce action. The enforce action seeds
// the editor/viewer convenience roles, grants a chosen managed user the admin
// role, and flips enforcement live — the local admin always retains access
// via the middleware's password-auth bypass.
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { CheckCircle2, CircleAlert, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { getErrorMessage } from "@/lib/store";
import { useEnforceRbacMutation, useGetRbacSetupStatusQuery } from "@/lib/store/apis/rbacApi";
import { useGetUsersQuery } from "@/lib/store/apis/userManagementApi";

// statusRow renders one posture line with a pass/fail icon.
function StatusRow({ ok, label, testid }: { ok: boolean; label: string; testid?: string }) {
	return (
		<div className="flex items-center gap-2 text-sm" data-testid={testid}>
			{ok ? <CheckCircle2 className="h-4 w-4 text-green-600" /> : <CircleAlert className="h-4 w-4 text-amber-600" />}
			<span>{label}</span>
		</div>
	);
}

export default function SecureSetupWizard() {
	const { data: status, isLoading, isError, error } = useGetRbacSetupStatusQuery();
	const { data: usersData } = useGetUsersQuery();
	const [enforceRbac, { isLoading: isEnforcing }] = useEnforceRbacMutation();

	const [selectedUserID, setSelectedUserID] = useState("");
	const [confirmOpen, setConfirmOpen] = useState(false);

	const users = usersData?.users ?? [];
	const rbac = status?.rbac;
	const needsUserPick = (rbac?.assignment_count ?? 0) === 0;
	const envPinned = rbac?.source === "env";

	const handleEnforce = async () => {
		try {
			await enforceRbac({
				enabled: true,
				...(needsUserPick ? { assign_user_id: selectedUserID } : {}),
			}).unwrap();
			toast.success("RBAC enforcement enabled. The local admin always retains access.");
			setConfirmOpen(false);
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	const handleDisable = async () => {
		try {
			await enforceRbac({ enabled: false }).unwrap();
			toast.success("RBAC enforcement disabled.");
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	if (isLoading) {
		return <p className="text-muted-foreground text-sm">Loading security posture...</p>;
	}
	if (isError || !status) {
		return <p className="text-sm text-red-500">Failed to load security posture: {getErrorMessage(error)}</p>;
	}

	return (
		<div className="w-full space-y-4">
			<div>
				<h2 className="text-lg font-semibold tracking-tight">Secure Setup</h2>
				<p className="text-muted-foreground text-sm">
					Lock down the management plane step by step. Loopback Gateway ships permissive so nothing bricks on first run — production
					deployments should complete every step below.
				</p>
			</div>

			{/* Posture summary */}
			<Card data-testid="secure-setup-status-card">
				<CardHeader>
					<CardTitle className="flex items-center gap-2 text-base">
						<ShieldCheck className="h-4 w-4" /> Security posture
						{status.insecure ? <Badge variant="destructive">Needs attention</Badge> : <Badge variant="success">Locked down</Badge>}
					</CardTitle>
				</CardHeader>
				<CardContent className="space-y-2">
					<StatusRow
						ok={status.dashboard_auth.enabled}
						label={
							status.dashboard_auth.enabled
								? "Dashboard authentication is enabled"
								: "Dashboard authentication is DISABLED — anyone who can reach the UI has full control"
						}
						testid="secure-setup-dashboard-auth-row"
					/>
					<StatusRow
						ok={status.inference_auth.enforced}
						label={
							status.inference_auth.enforced ? "Inference requests require auth" : "Inference endpoints accept unauthenticated requests"
						}
						testid="secure-setup-inference-auth-row"
					/>
					<StatusRow
						ok={rbac?.enforcing ?? false}
						label={
							rbac?.enforcing
								? `RBAC is enforcing (source: ${rbac.source}, ${rbac.assignment_count} assignment${rbac.assignment_count === 1 ? "" : "s"})`
								: "RBAC is fail-open: every authenticated user can mutate configuration"
						}
						testid="secure-setup-rbac-row"
					/>
				</CardContent>
			</Card>

			{/* Step cards */}
			<div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
				<Card>
					<CardHeader>
						<CardTitle className="text-base">1. Dashboard auth & CORS</CardTitle>
						<CardDescription>
							Set an admin username/password and restrict allowed origins so only trusted hosts can reach the dashboard.
						</CardDescription>
					</CardHeader>
					<CardContent>
						<Button variant="outline" size="sm" asChild>
							<Link to="/workspace/config/security">Open security settings</Link>
						</Button>
					</CardContent>
				</Card>

				<Card>
					<CardHeader>
						<CardTitle className="text-base">2. Enforce auth on inference</CardTitle>
						<CardDescription>
							Require a virtual key, API key, or user token on every inference request so usage is always attributed and governed.
						</CardDescription>
					</CardHeader>
					<CardContent>
						<Button variant="outline" size="sm" asChild>
							<Link to="/workspace/config/security">Open security settings</Link>
						</Button>
					</CardContent>
				</Card>
			</div>

			{/* RBAC enforce card */}
			<Card>
				<CardHeader>
					<CardTitle className="text-base">3. Enforce role-based access control</CardTitle>
					<CardDescription>
						One click seeds two convenience roles — <span className="font-medium">editor</span> (read + create/update, no delete) and{" "}
						<span className="font-medium">viewer</span> (read-only) — alongside the built-in <span className="font-medium">admin</span> role
						(full access), grants a managed user the admin role, and turns enforcement on. The local admin (password login) always retains
						access, so this can never lock you out.
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-3">
					{rbac?.enforcing ? (
						<div className="flex items-center gap-3">
							<Badge variant="success">Enforcing</Badge>
							{envPinned ? (
								<span className="text-muted-foreground text-sm">
									Pinned on by the LOOPBACK_RBAC_ENABLED environment variable — disable it there if needed.
								</span>
							) : (
								<Button
									variant="outline"
									size="sm"
									onClick={handleDisable}
									disabled={isEnforcing}
									data-testid="secure-setup-disable-button"
								>
									Disable enforcement
								</Button>
							)}
						</div>
					) : (
						<>
							{needsUserPick && (
								<div className="flex flex-col gap-1">
									<Label>Grant the admin role to</Label>
									{users.length === 0 ? (
										<p className="text-muted-foreground text-sm">
											No managed users exist yet.{" "}
											<Link to="/workspace/governance/users" className="font-medium underline underline-offset-2">
												Create one first
											</Link>{" "}
											— enforcement needs at least one user who can administer the gateway.
										</p>
									) : (
										<Select value={selectedUserID} onValueChange={setSelectedUserID}>
											<SelectTrigger className="w-72" data-testid="secure-setup-user-select">
												<SelectValue placeholder="Select a managed user" />
											</SelectTrigger>
											<SelectContent>
												{users.map((user) => (
													<SelectItem key={user.id} value={user.id}>
														{user.name} ({user.email})
													</SelectItem>
												))}
											</SelectContent>
										</Select>
									)}
								</div>
							)}
							<Button
								onClick={() => setConfirmOpen(true)}
								disabled={isEnforcing || (needsUserPick && selectedUserID === "")}
								data-testid="secure-setup-enforce-button"
							>
								Enforce RBAC
							</Button>
						</>
					)}
				</CardContent>
			</Card>

			<Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
				<DialogContent data-testid="secure-setup-confirm-dialog">
					<DialogHeader>
						<DialogTitle>Enforce RBAC?</DialogTitle>
						<DialogDescription>
							Once enforcing, users without a role granting a permission are denied mutating requests (reads stay open). The local admin
							(password login) always bypasses RBAC, so you cannot lock yourself out. You can adjust roles and assignments any time under
							Governance → RBAC.
						</DialogDescription>
					</DialogHeader>
					<DialogFooter>
						<Button variant="ghost" onClick={() => setConfirmOpen(false)}>
							Cancel
						</Button>
						<Button onClick={handleEnforce} disabled={isEnforcing} data-testid="secure-setup-confirm-button">
							{isEnforcing ? "Enforcing..." : "Enforce"}
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</div>
	);
}