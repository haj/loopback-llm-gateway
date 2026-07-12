// Types for the Loopback Gateway RBAC & access control UI. These mirror the
// backend payloads exposed by transports/bifrost-http/handlers/rbac.go.

// A single (resource, operation) grant. Resource/Operation accept the wildcard
// "*" which matches every resource/operation respectively.
export interface Permission {
	id?: string;
	role_id?: string;
	resource: string;
	operation: string;
	created_at?: string;
}

export interface Role {
	id: string;
	name: string;
	description: string;
	is_system: boolean;
	permissions: Permission[];
	created_at: string;
	updated_at: string;
}

export interface RoleAssignment {
	id: string;
	role_id: string;
	user_id: string;
	created_at: string;
	role?: Role;
	user?: {
		id: string;
		name: string;
		email: string;
	};
}

// ---- query params ----

export interface ListRolesParams {
	search?: string;
	limit?: number;
	offset?: number;
}

export interface ListRoleAssignmentsParams {
	user_id?: string;
	role_id?: string;
	limit?: number;
	offset?: number;
}

// ---- request bodies ----

export interface PermissionInput {
	resource: string;
	operation: string;
}

export interface CreateRoleRequest {
	name: string;
	description?: string;
	permissions?: PermissionInput[];
}

export interface UpdateRoleRequest {
	id: string;
	name?: string;
	description?: string;
	permissions?: PermissionInput[];
}

export interface CreateRoleAssignmentRequest {
	role_id: string;
	user_id: string;
}

// ---- responses ----

export interface ListRolesResponse {
	roles: Role[];
	total: number;
	count: number;
	offset: number;
}

export interface RoleResponse {
	message?: string;
	role: Role;
}

export interface ListRoleAssignmentsResponse {
	role_assignments: RoleAssignment[];
	total: number;
	count: number;
	offset: number;
}

export interface RoleAssignmentResponse {
	message?: string;
	role_assignment: RoleAssignment;
}

// Effective permissions for the acting principal, used by rbacContext to mirror
// server-side enforcement. allow_all=true means the UI should permit everything
// (RBAC disabled, local admin, or no assignments configured).
export interface MyPermissionsResponse {
	enabled: boolean;
	allow_all: boolean;
	permissions: PermissionInput[];
}
// Deployment security posture reported by GET /governance/rbac/setup-status.
// Drives the secure-setup wizard, the fail-open banner, and the onboarding
// checklist's Enforce RBAC step.
export interface RbacSetupStatusResponse {
	rbac: {
		enforcing: boolean;
		source: "env" | "config" | "none";
		assignment_count: number;
		roles_seeded: Record<string, boolean>;
	};
	dashboard_auth: { enabled: boolean };
	inference_auth: { enforced: boolean };
	cors: { restricted: boolean };
	users: { count: number };
	insecure: boolean;
}

// EnforceRbacRequest is the POST /governance/rbac/enforce body. Enabling with
// zero existing assignments requires assign_user_id (the user granted the
// admin role).
export interface EnforceRbacRequest {
	enabled: boolean;
	assign_user_id?: string;
}