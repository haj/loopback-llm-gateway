// SSO/SCIM management UI for the open-source Loopback Gateway build
// (Keycloak, Okta, and Entra ID providers). It surfaces the effective SSO
// status, the inbound /scim/v2 provisioning endpoint state, lets an admin
// trigger a directory pull sync, and lists the users/groups provisioned from
// the IdP with their attribute-mapping outcomes. Mirrors the rbacView.tsx
// patterns (RTK-query hooks + table layout).
//
// Default-OFF: when SSO is not configured the view explains how to enable it and
// the sync/list actions stay disabled. Password auth is unaffected.
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { getErrorMessage } from "@/lib/store";
import { useGetSCIMGroupsQuery, useGetSCIMStatusQuery, useGetSCIMUsersQuery, useTriggerSCIMSyncMutation } from "@/lib/store/apis/scimApi";
import { RefreshCw } from "lucide-react";
import { toast } from "sonner";

export default function SCIMView() {
	const { data: status, isLoading: statusLoading } = useGetSCIMStatusQuery();
	const enabled = !!status?.enabled;

	const { data: usersData, isFetching: usersFetching } = useGetSCIMUsersQuery(undefined, { skip: !enabled });
	const { data: groupsData, isFetching: groupsFetching } = useGetSCIMGroupsQuery(undefined, { skip: !enabled });
	const [triggerSync, { isLoading: syncing }] = useTriggerSCIMSyncMutation();

	const handleSync = async () => {
		try {
			const res = await triggerSync().unwrap();
			const r = res.result;
			toast.success(
				`Sync complete: ${r.users_synced} active, ${r.users_deactivated} deactivated, ${r.groups_synced} groups` +
					(r.errors && r.errors.length ? ` (${r.errors.length} errors)` : ""),
			);
		} catch (e) {
			toast.error(getErrorMessage(e));
		}
	};

	return (
		<div className="h-full w-full space-y-6 p-6">
			<div className="flex items-start justify-between">
				<div className="space-y-1">
					<h1 className="text-xl font-semibold">SSO &amp; SCIM provisioning</h1>
					<p className="text-muted-foreground text-sm">
						Single sign-on and user provisioning for Loopback Gateway. Keycloak, Okta, and Microsoft Entra ID are supported.
					</p>
				</div>
				<Button onClick={handleSync} disabled={!enabled || syncing} isLoading={syncing}>
					<RefreshCw className="mr-2 h-4 w-4" />
					Sync now
				</Button>
			</div>

			<Card>
				<CardHeader>
					<CardTitle className="flex items-center gap-2 text-base">
						Status
						{statusLoading ? null : enabled ? <Badge variant="default">Enabled</Badge> : <Badge variant="secondary">Disabled</Badge>}
						{enabled && status?.provider && (
							<Badge variant="outline" data-testid="scim-provider-badge">
								{status.provider}
							</Badge>
						)}
						{enabled && status?.valid === false && <Badge variant="destructive">Misconfigured</Badge>}
					</CardTitle>
					<CardDescription>
						{enabled
							? `Provider: ${status?.provider ?? "unknown"}. Valid IdP-issued JWTs are accepted as an additional login path; password login keeps working.`
							: "SSO is off. Add a scim_config section (provider: keycloak, enabled: true) to config.json to enable it. Until then only password authentication is active."}
					</CardDescription>
				</CardHeader>
				{enabled && status?.valid === false && status?.error && (
					<CardContent>
						<p className="text-destructive text-sm">{status.error}</p>
					</CardContent>
				)}
			</Card>

			{enabled && (
				<Card data-testid="scim-provisioning-card">
					<CardHeader>
						<CardTitle className="flex items-center gap-2 text-base">
							SCIM provisioning endpoint
							{status?.provisioning_enabled ? (
								<Badge variant="default" data-testid="scim-provisioning-status">
									Enabled
								</Badge>
							) : (
								<Badge variant="secondary" data-testid="scim-provisioning-status">
									Disabled
								</Badge>
							)}
						</CardTitle>
						<CardDescription>
							{status?.provisioning_enabled
								? "IdPs can push user and group changes to /scim/v2 (Users, Groups) authenticated with the configured bearer token."
								: "Inbound push provisioning is off. Set scim_config.provisioning { enabled: true, bearerToken } to let the IdP push changes to /scim/v2; pull sync via \u201cSync now\u201d works either way."}
						</CardDescription>
					</CardHeader>
				</Card>
			)}

			{enabled && (
				<Tabs defaultValue="users" className="w-full">
					<TabsList>
						<TabsTrigger value="users">Users ({usersData?.total ?? 0})</TabsTrigger>
						<TabsTrigger value="groups">Groups ({groupsData?.total ?? 0})</TabsTrigger>
					</TabsList>

					<TabsContent value="users">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>User</TableHead>
									<TableHead>Email</TableHead>
									<TableHead>Groups</TableHead>
									<TableHead>Mapped</TableHead>
									<TableHead>Status</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{(usersData?.users ?? []).map((u) => (
									<TableRow key={u.id}>
										<TableCell className="font-medium">{u.display_name || u.user_name}</TableCell>
										<TableCell>{u.email}</TableCell>
										<TableCell>
											<div className="flex flex-wrap gap-1">
												{(u.groups ?? []).map((g) => (
													<Badge key={g} variant="outline">
														{g}
													</Badge>
												))}
											</div>
										</TableCell>
										<TableCell data-testid={`scim-user-mapped-${u.id}`}>
											<div className="flex flex-wrap gap-1">
												{u.mapped_role_id && <Badge variant="secondary">role</Badge>}
												{(u.mapped_team_ids?.length ?? 0) > 0 && <Badge variant="secondary">{u.mapped_team_ids?.length} team(s)</Badge>}
												{u.mapped_business_unit_id && <Badge variant="secondary">BU</Badge>}
												{!u.mapped_role_id && (u.mapped_team_ids?.length ?? 0) === 0 && !u.mapped_business_unit_id && (
													<span className="text-muted-foreground text-xs">—</span>
												)}
											</div>
										</TableCell>
										<TableCell>
											{u.active ? <Badge variant="default">Active</Badge> : <Badge variant="secondary">Inactive</Badge>}
										</TableCell>
									</TableRow>
								))}
								{!usersFetching && (usersData?.users?.length ?? 0) === 0 && (
									<TableRow>
										<TableCell colSpan={5} className="text-muted-foreground text-center text-sm">
											No provisioned users yet. Click &quot;Sync now&quot; to pull from the IdP, or users are created just-in-time on first
											SSO login.
										</TableCell>
									</TableRow>
								)}
							</TableBody>
						</Table>
					</TabsContent>

					<TabsContent value="groups">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>Group</TableHead>
									<TableHead>Members</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{(groupsData?.groups ?? []).map((g) => (
									<TableRow key={g.id}>
										<TableCell className="font-medium">{g.display_name}</TableCell>
										<TableCell>{g.members?.length ?? 0}</TableCell>
									</TableRow>
								))}
								{!groupsFetching && (groupsData?.groups?.length ?? 0) === 0 && (
									<TableRow>
										<TableCell colSpan={2} className="text-muted-foreground text-center text-sm">
											No provisioned groups yet.
										</TableCell>
									</TableRow>
								)}
							</TableBody>
						</Table>
					</TabsContent>
				</Tabs>
			)}
		</div>
	);
}