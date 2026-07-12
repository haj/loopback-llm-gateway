import { createFileRoute } from "@tanstack/react-router";
import VaultPage from "./page";

export const Route = createFileRoute("/workspace/config/vault")({
	component: VaultPage,
});