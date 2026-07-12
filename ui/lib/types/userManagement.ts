// Types for the Loopback Gateway user & org management UI. These mirror the
// backend transports/bifrost-http/handlers/usermanagement.go contract and the
// configstore TableUser / TableBusinessUnit shapes.

// UserStatus mirrors the backend status enum.
export type UserStatus = "active" | "inactive";

// Budget is the shared governance budget shape (subset used by this UI).
export interface UserBudget {
	id: string;
	max_limit: number;
	reset_duration: string;
	current_usage?: number;
}

// RateLimit is the shared governance rate-limit shape (subset used by this UI).
export interface UserRateLimit {
	id: string;
	token_max_limit?: number | null;
	token_reset_duration?: string | null;
	request_max_limit?: number | null;
	request_reset_duration?: string | null;
}

// BudgetInput is the create/update payload for an entity's budget. A zero
// max_limit with an empty reset_duration removes the budget on update.
export interface BudgetInput {
	max_limit: number;
	reset_duration: string;
}

// RateLimitInput is the create/update payload for an entity's rate limit. All
// null max-limit fields remove the rate limit on update.
export interface RateLimitInput {
	token_max_limit?: number | null;
	token_reset_duration?: string | null;
	request_max_limit?: number | null;
	request_reset_duration?: string | null;
}

// BusinessUnit is a flat (non-nested) organizational grouping for users.
export interface BusinessUnit {
	id: string;
	name: string;
	description: string;
	budget_id?: string | null;
	rate_limit_id?: string | null;
	budget?: UserBudget | null;
	rate_limit?: UserRateLimit | null;
	user_count: number;
	created_at: string;
	updated_at: string;
}

// User is a managed user, optionally assigned to a business unit and attached to
// one or more virtual keys.
export interface User {
	id: string;
	name: string;
	email: string;
	status: UserStatus;
	business_unit_id?: string | null;
	budget_id?: string | null;
	rate_limit_id?: string | null;
	virtual_key_ids: string[];
	budget?: UserBudget | null;
	rate_limit?: UserRateLimit | null;
	business_unit?: BusinessUnit | null;
	created_at: string;
	updated_at: string;
}

// ---- request payloads ----

export interface CreateUserRequest {
	name: string;
	email: string;
	status?: UserStatus;
	business_unit_id?: string | null;
	virtual_key_ids?: string[];
	budget?: BudgetInput;
	rate_limit?: RateLimitInput;
}

export interface UpdateUserRequest {
	id: string;
	name?: string;
	email?: string;
	status?: UserStatus;
	business_unit_id?: string | null;
	virtual_key_ids?: string[];
	budget?: BudgetInput;
	rate_limit?: RateLimitInput;
}

export interface CreateBusinessUnitRequest {
	name: string;
	description?: string;
	budget?: BudgetInput;
	rate_limit?: RateLimitInput;
}

export interface UpdateBusinessUnitRequest {
	id: string;
	name?: string;
	description?: string;
	budget?: BudgetInput;
	rate_limit?: RateLimitInput;
}

// ---- list params & responses ----

export interface ListUsersParams {
	search?: string;
	status?: UserStatus;
	business_unit_id?: string;
	limit?: number;
	offset?: number;
}

export interface ListUsersResponse {
	users: User[];
	total: number;
	count: number;
	offset: number;
}

export interface UserResponse {
	user: User;
	message?: string;
}

export interface ListBusinessUnitsParams {
	search?: string;
	limit?: number;
	offset?: number;
}

export interface ListBusinessUnitsResponse {
	business_units: BusinessUnit[];
	total: number;
	count: number;
	offset: number;
}

export interface BusinessUnitResponse {
	business_unit: BusinessUnit;
	message?: string;
}