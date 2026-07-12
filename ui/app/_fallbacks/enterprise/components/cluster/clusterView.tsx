import { Layers } from "lucide-react";
import ContactUsView from "../views/contactUsView";

export default function ClusterPage() {
	return (
		<div className="h-full w-full">
			<ContactUsView
				className="mx-auto min-h-[80vh]"
				icon={<Layers className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />}
				title="Unlock cluster mode to scale reliably"
				description="This feature is a part of the Loopback Gateway enterprise edition."
				readmeLink="https://docs.loopback.gateway/enterprise/clustering"
			/>
		</div>
	);
}