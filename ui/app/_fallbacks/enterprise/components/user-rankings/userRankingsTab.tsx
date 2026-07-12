import { Users } from "lucide-react";
import ContactUsView from "../views/contactUsView";

export default function UserRankingsTab() {
	return (
		<div className="h-full w-full">
			<ContactUsView
				className="mx-auto min-h-[80vh]"
				icon={<Users className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />}
				title="Unlock user rankings for better visibility"
				description="This feature is a part of the Loopback Gateway enterprise edition."
				readmeLink="https://docs.loopback.gateway/enterprise/user-rankings"
				testIdPrefix="user-rankings"
			/>
		</div>
	);
}