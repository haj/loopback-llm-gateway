import { createFileRoute, Outlet, useChildMatches } from "@tanstack/react-router";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import AlertChannelsPage from "./page";

function RouteComponent() {
	const hasAccess = useRbac(RbacResource.AlertChannels, RbacOperation.View);
	const childMatches = useChildMatches();
	if (!hasAccess) {
		return <NoPermissionView entity="alert channels" />;
	}
	return childMatches.length === 0 ? <AlertChannelsPage /> : <Outlet />;
}

export const Route = createFileRoute("/workspace/alert-channels")({
	component: RouteComponent,
});