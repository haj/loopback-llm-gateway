// FailOpenBanner warns that RBAC is fail-open (not enforcing) with a CTA to
// the secure-setup wizard. Rendered next to TrialExpiryBanner in clientLayout.
// Cookie-dismissible for ~30 days (pattern from onboardingWidget's
// hide-for-me), and the status query is skipped entirely once dismissed so
// the banner costs nothing on subsequent page loads.
import { Link } from "@tanstack/react-router";
import { ShieldAlert, X } from "lucide-react";
import { useCookies } from "react-cookie";
import { useGetRbacSetupStatusQuery } from "@/lib/store/apis/rbacApi";

const FAILOPEN_DISMISSED_COOKIE = "loopback_failopen_dismissed";

export default function FailOpenBanner() {
	const [cookies, setCookie] = useCookies([FAILOPEN_DISMISSED_COOKIE]);
	const dismissed = !!cookies[FAILOPEN_DISMISSED_COOKIE];
	const { data: status } = useGetRbacSetupStatusQuery(undefined, { skip: dismissed });

	if (dismissed || !status || status.rbac.enforcing) {
		return null;
	}

	const dismiss = () => {
		const expires = new Date();
		expires.setDate(expires.getDate() + 30);
		setCookie(FAILOPEN_DISMISSED_COOKIE, "1", { path: "/", expires });
	};

	return (
		<div
			className="sticky top-0 z-10 flex w-full items-center justify-center gap-2 rounded-tl-md rounded-tr-md bg-amber-500/10 px-4 py-2 text-xs font-medium text-amber-700 dark:text-amber-400"
			role="status"
			data-testid="rbac-failopen-banner"
		>
			<ShieldAlert className="h-3.5 w-3.5" strokeWidth={2} />
			<span>
				RBAC is not enforcing: every authenticated user can change gateway configuration.{" "}
				<Link to="/workspace/secure-setup" className="font-semibold underline underline-offset-2" data-testid="rbac-failopen-banner-cta">
					Run secure setup
				</Link>
			</span>
			<button
				type="button"
				onClick={dismiss}
				className="ml-2 rounded p-0.5 hover:bg-amber-500/20"
				aria-label="Dismiss"
				data-testid="rbac-failopen-banner-dismiss"
			>
				<X className="h-3.5 w-3.5" strokeWidth={2} />
			</button>
		</div>
	);
}