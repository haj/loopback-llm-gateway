import { createFileRoute, Outlet, useChildMatches } from "@tanstack/react-router";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import JWTAuthPage from "./page";

function RouteComponent() {
	const hasAccess = useRbac(RbacResource.JWTAuth, RbacOperation.View);
	const childMatches = useChildMatches();
	if (!hasAccess) {
		return <NoPermissionView entity="JWT authentication" />;
	}
	return childMatches.length === 0 ? <JWTAuthPage /> : <Outlet />;
}

export const Route = createFileRoute("/workspace/jwt-auth")({
	component: RouteComponent,
});