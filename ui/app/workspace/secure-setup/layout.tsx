import { createFileRoute, Outlet, useChildMatches } from "@tanstack/react-router";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import SecureSetupPage from "./page";

function RouteComponent() {
	const hasAccess = useRbac(RbacResource.RBAC, RbacOperation.View);
	const childMatches = useChildMatches();
	if (!hasAccess) {
		return <NoPermissionView entity="secure setup" />;
	}
	return childMatches.length === 0 ? <SecureSetupPage /> : <Outlet />;
}

export const Route = createFileRoute("/workspace/secure-setup")({
	component: RouteComponent,
});