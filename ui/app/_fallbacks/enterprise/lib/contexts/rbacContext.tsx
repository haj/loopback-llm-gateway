import { useGetMyPermissionsQuery } from "@/lib/store/apis/rbacApi";
import { createContext, useCallback, useContext, useMemo } from "react";

// RBAC Resource Names (must match backend definitions in
// transports/bifrost-http/handlers/rbac_middleware.go and the RbacResource enum
// the dashboard ships).
export enum RbacResource {
	GuardrailsConfig = "GuardrailsConfig",
	GuardrailsProviders = "GuardrailsProviders",
	GuardrailRules = "GuardrailRules",
	UserProvisioning = "UserProvisioning",
	Cluster = "Cluster",
	Settings = "Settings",
	Users = "Users",
	Logs = "Logs",
	Observability = "Observability",
	Dashboard = "Dashboard",
	VirtualKeys = "VirtualKeys",
	ModelProvider = "ModelProvider",
	Plugins = "Plugins",
	MCPGateway = "MCPGateway",
	MCPToolGroups = "MCPToolGroups",
	MCPLogs = "MCPLogs",
	AdaptiveRouter = "AdaptiveRouter",
	AuditLogs = "AuditLogs",
	AlertChannels = "AlertChannels",
	CircuitBreaker = "CircuitBreaker",
	JWTAuth = "JWTAuth",
	Customers = "Customers",
	Teams = "Teams",
	RBAC = "RBAC",
	Governance = "Governance",
	RoutingRules = "RoutingRules",
	PIIRedactor = "PIIRedactor",
	PromptRepository = "PromptRepository",
	PromptDeploymentStrategy = "PromptDeploymentStrategy",
	SkillsRepository = "SkillsRepository",
	AccessProfiles = "AccessProfiles",
	APIKeys = "APIKeys",
	Inference = "Inference",
	Metrics = "Metrics",
	FeatureFlags = "FeatureFlags",
}

// RBAC Operation Names (must match backend definitions).
export enum RbacOperation {
	Read = "Read",
	View = "View",
	Create = "Create",
	Update = "Update",
	Delete = "Delete",
	Download = "Download",
}

// Wildcard, matching both resource and operation on the backend.
const WILDCARD = "*";

interface RbacContextType {
	isAllowed: (resource: RbacResource, operation: RbacOperation) => boolean;
	permissions: Record<string, Record<string, boolean>>;
	isLoading: boolean;
	refetch: () => void;
}

const RbacContext = createContext<RbacContextType | null>(null);

// allowEverything is the permissive context used while RBAC is disabled, the
// caller is the local admin, or no role assignments are configured. This is the
// safety default so RBAC never bricks an existing deployment.
const allowEverything: RbacContextType = {
	isAllowed: () => true,
	permissions: {},
	isLoading: false,
	refetch: () => {},
};

// RbacProvider resolves the acting principal's effective permissions from the
// gateway (/api/governance/rbac/permissions/me) and exposes wildcard-aware
// allow/deny decisions that mirror server-side enforcement. It is fail-open: if
// RBAC is off, the caller is a local admin, no assignments exist, or the request
// errors, it allows everything.
export function RbacProvider({ children }: { children: React.ReactNode }) {
	const { data, isLoading, isError, refetch } = useGetMyPermissionsQuery();

	const value = useMemo<RbacContextType>(() => {
		// Fail-open on error or while RBAC reports allow_all (disabled / admin /
		// unconfigured). data is undefined until the first response resolves.
		if (isError || !data || data.allow_all) {
			return { ...allowEverything, isLoading, refetch };
		}

		const grants = data.permissions ?? [];

		// Build a nested lookup for the legacy `permissions` shape consumers read.
		const permissions: Record<string, Record<string, boolean>> = {};
		for (const grant of grants) {
			if (!permissions[grant.resource]) {
				permissions[grant.resource] = {};
			}
			permissions[grant.resource][grant.operation] = true;
		}

		const isAllowed = (resource: RbacResource, operation: RbacOperation): boolean => {
			for (const grant of grants) {
				const resourceOK = grant.resource === WILDCARD || grant.resource === resource;
				const operationOK = grant.operation === WILDCARD || grant.operation === operation;
				if (resourceOK && operationOK) {
					return true;
				}
			}
			return false;
		};

		return { isAllowed, permissions, isLoading, refetch };
	}, [data, isError, isLoading, refetch]);

	return <RbacContext.Provider value={value}>{children}</RbacContext.Provider>;
}

// useRbac returns whether the current principal may perform operation on
// resource. Defaults to allow while the permission query is still loading so the
// UI never flashes a denied state for an operator who is actually permitted.
export function useRbac(resource: RbacResource, operation: RbacOperation): boolean {
	const context = useContext(RbacContext);
	if (!context) {
		return true;
	}
	if (context.isLoading) {
		return true;
	}
	return context.isAllowed(resource, operation);
}

// useRbacContext exposes the full RBAC context. Outside a provider it returns the
// permissive default.
export function useRbacContext() {
	const context = useContext(RbacContext);
	if (!context) {
		return allowEverything;
	}
	return context;
}