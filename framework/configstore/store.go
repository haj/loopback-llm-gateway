// Package configstore provides a persistent configuration store for Bifrost.
package configstore

import (
	"context"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/objectstore"
	"github.com/maximhq/bifrost/framework/vectorstore"
	"gorm.io/gorm"
)

// VirtualKeyQueryParams holds pagination, filtering, and search parameters for virtual key queries.
type VirtualKeyQueryParams struct {
	Limit                              int
	Offset                             int
	Search                             string
	CustomerID                         string
	TeamID                             string
	SortBy                             string // name, budget_spent, created_at, status (default: created_at)
	Order                              string // asc, desc (default: asc)
	Export                             bool   // When true, skip default pagination limits (caller controls limit)
	ExcludeAccessProfileManagedVirtual bool   // When true, exclude VKs managed through enterprise access profiles
	ExcludeAssignedVirtualKeys         bool   // When true, exclude VKs that already have any user assignment
	ForUserAssignment                  bool   // When true, exclude VKs assigned to any entity (team, customer, access profile, or user) — intended for user-assignment pickers
}

// ModelConfigsQueryParams holds pagination, filtering, and search parameters for model configs queries.
type ModelConfigsQueryParams struct {
	Limit    int
	Offset   int
	Search   string
	Scope    string // optional; filters to an exact scope value (e.g. "global", "virtual_key")
	Provider string // optional; filters to an exact provider value (e.g. "openai")
}

// GuardrailConfigsQueryParams holds pagination, filtering, and search parameters for guardrail config queries.
type GuardrailConfigsQueryParams struct {
	Limit   int
	Offset  int
	Search  string // optional; case-insensitive match on name
	Scope   string // optional; filters to an exact scope value (e.g. "global", "virtual_key")
	ScopeID string // optional; filters to an exact scope target ID
}

// MCPToolGroupsQueryParams holds pagination, filtering, and search parameters
// for MCP tool group queries.
type MCPToolGroupsQueryParams struct {
	Limit   int
	Offset  int
	Search  string // optional; case-insensitive match on name
	Scope   string // optional; filters to an exact scope value (e.g. "team", "virtual_key")
	ScopeID string // optional; filters to an exact scope target ID
}

// AuditLogsQueryParams holds pagination and filtering parameters for audit log
// queries. The audit log is append-only; only reads are filtered.
type AuditLogsQueryParams struct {
	Limit   int
	Offset  int
	Action  string     // optional; filters to an exact action value (e.g. "virtual_key.create")
	Outcome string     // optional; filters to an exact outcome ("success" or "failure")
	Actor   string     // optional; case-insensitive match on actor
	Target  string     // optional; exact match on target ID
	Search  string     // optional; case-insensitive match on action, actor, or target
	Since   *time.Time // optional; only rows with timestamp >= Since
	Until   *time.Time // optional; only rows with timestamp <= Until
}

// UsersQueryParams holds pagination, filtering, and search parameters for user queries.
type UsersQueryParams struct {
	Limit          int
	Offset         int
	Search         string // optional; case-insensitive match on name or email
	Status         string // optional; filters to an exact status value ("active" or "inactive")
	BusinessUnitID string // optional; filters to users in an exact business unit
}

// SCIMUsersQueryParams holds pagination/filtering for synced SCIM users.
type SCIMUsersQueryParams struct {
	Limit    int
	Offset   int
	Search   string // optional; case-insensitive match on user_name / email / display_name
	Provider string // optional; filters to an exact provider ("keycloak")
	Active   *bool  // optional; filters by active state
}

// SCIMGroupsQueryParams holds pagination/filtering for synced SCIM groups.
type SCIMGroupsQueryParams struct {
	Limit    int
	Offset   int
	Search   string // optional; case-insensitive match on display_name
	Provider string // optional; filters to an exact provider ("keycloak")
}

// BusinessUnitsQueryParams holds pagination, filtering, and search parameters for business unit queries.
type BusinessUnitsQueryParams struct {
	Limit  int
	Offset int
	Search string // optional; case-insensitive match on name
}

// RolesQueryParams holds pagination, filtering, and search parameters for RBAC role queries.
type RolesQueryParams struct {
	Limit  int
	Offset int
	Search string // optional; case-insensitive match on name
}

// RoleAssignmentsQueryParams holds pagination and filtering parameters for RBAC
// role-assignment queries.
type RoleAssignmentsQueryParams struct {
	Limit  int
	Offset int
	UserID string // optional; filters to assignments for an exact user
	RoleID string // optional; filters to assignments for an exact role
}

// APIKeysQueryParams holds pagination, filtering, and search parameters for
// admin API key queries.
type APIKeysQueryParams struct {
	Limit  int
	Offset int
	Search string // optional; case-insensitive match on name
	Status string // optional; filters to an exact status ("active" or "revoked")
}

// PIIRulesQueryParams holds pagination, filtering, and search parameters for PII rule queries.
type PIIRulesQueryParams struct {
	Limit   int
	Offset  int
	Search  string // optional; case-insensitive match on name
	Scope   string // optional; filters to an exact scope value (e.g. "global", "virtual_key")
	ScopeID string // optional; filters to an exact scope target ID
	Type    string // optional; filters to an exact type value ("regex" or "presidio")
}

// SkillListQueryParams holds pagination, filtering, and search parameters for skill repository queries.
type SkillListQueryParams struct {
	Limit  int
	Offset int
	Search string
	SortBy string // name, updated_at, created_at (default: created_at)
	Order  string // asc, desc (default: desc)
}

type SkillVersionListQueryParams struct {
	Limit  int
	Offset int
	SortBy string // version, created_at (default: created_at)
	Order  string // asc, desc (default: desc)
	Search string // substring match on the version string (optional)
}

// RoutingRulesQueryParams holds pagination, filtering, and search parameters for routing rules queries.
type RoutingRulesQueryParams struct {
	Limit  int
	Offset int
	Search string
}

// MCPClientsQueryParams holds pagination, filtering, and search parameters for MCP client queries.
type MCPClientsQueryParams struct {
	Limit  int
	Offset int
	Search string
}

// MCPLibraryQueryParams holds pagination, filtering, search, and sort
// parameters for MCP library catalog queries. All fields are optional — an
// empty struct returns the first default-sized page ordered by name.
type MCPLibraryQueryParams struct {
	Limit           int
	Offset          int
	Search          string   // matches name/description/publisher (case-insensitive)
	Categories      []string // exact category filter(s), OR semantics
	ConnectionTypes []string // exact connection_type filter(s) (http | stdio | sse)
	AuthTypes       []string // exact auth_type filter(s)
	Tags            []string // match rows carrying any of these tags
	SortBy          string   // name, category, publisher, created_at, updated_at (default: name)
	Order           string   // asc, desc (default: asc)
}

// MCPLibraryFilterData holds the distinct facet values surfaced by the filter
// sidebar on the MCP library page. Populated via GetMCPLibraryFilterData.
type MCPLibraryFilterData struct {
	Categories      []string `json:"categories"`
	ConnectionTypes []string `json:"connection_types"`
	AuthTypes       []string `json:"auth_types"`
	Tags            []string `json:"tags"`
}

// TeamsQueryParams holds pagination, filtering, and search parameters for team queries.
type TeamsQueryParams struct {
	Limit      int
	Offset     int
	Search     string
	CustomerID string
}

// CustomersQueryParams holds pagination, filtering, and search parameters for customer queries.
type CustomersQueryParams struct {
	Limit  int
	Offset int
	Search string
}

// MCPSessionsFilterParams is the filter set shared across the four
// MCP-sessions list methods (oauth tokens, pending oauth sessions,
// per-user header credentials, pending per-user header flows).
//
// Pagination is intentionally omitted: the four sources are merged and
// de-duped in the handler before the page slice, so per-table LIMIT/OFFSET
// would not compose into a correct global page. These methods are filter
// pushdown only; the handler paginates the merged result.
//
// Search is a case-insensitive substring matched against the MCP client's
// name/client_id, the row's identity columns (user_id, session_id), and
// the virtual key's id/name (joined). Empty filter slices match all
// values for that field.
type MCPSessionsFilterParams struct {
	Search       string
	Statuses     []string
	AuthModes    []string // matched against auth_mode (tokens, credentials) or flow_mode (sessions, flows)
	MCPClientIDs []string
	// MatchedUserIDs is an optional set of user_ids that should be treated
	// as a positive search hit alongside Search. Callers that maintain a
	// user directory (display names, emails) resolve the search string
	// against that directory and pass the resulting user_ids in here so
	// rows owned by those users surface even though the search columns on
	// these tables only carry the opaque user_id. When non-empty the
	// filter ORs `{table}.user_id IN (matched)` into the search WHERE.
	// Only consulted when Search is non-empty.
	MatchedUserIDs []string
}

// PricingOverrideFilters holds the filters for pricing overrides.
type PricingOverrideFilters struct {
	ScopeKind     *string
	VirtualKeyID  *string
	ProviderID    *string
	ProviderKeyID *string
}

// PricingOverridesQueryParams holds pagination, filtering, and search parameters for pricing override queries.
type PricingOverridesQueryParams struct {
	Limit         int
	Offset        int
	Search        string
	ScopeKind     *string
	VirtualKeyID  *string
	ProviderID    *string
	ProviderKeyID *string
}

// ConfigStore is the interface for the config store.
type ConfigStore interface {
	// Health check
	Ping(ctx context.Context) error

	// Encryption
	EncryptPlaintextRows(ctx context.Context) error

	// Client config CRUD
	UpdateClientConfig(ctx context.Context, config *ClientConfig) error
	GetClientConfig(ctx context.Context) (*ClientConfig, error)
	// Client config metadata (UI/admin preferences blob — bypasses config.json sync)
	GetClientMetadata(ctx context.Context) (map[string]any, error)
	UpdateClientMetadata(ctx context.Context, patch map[string]any) error

	// Framework config CRUD
	UpdateFrameworkConfig(ctx context.Context, config *tables.TableFrameworkConfig) error
	GetFrameworkConfig(ctx context.Context) (*tables.TableFrameworkConfig, error)

	// Feature flag overrides: list + upsert. Flags themselves are
	// code-declared (via featureflags.Register); only the toggle state
	// lives here. There is intentionally no Delete: removing a flag means
	// removing its Register() call in code.
	ListFeatureFlags(ctx context.Context) ([]tables.TableFeatureFlag, error)
	UpsertFeatureFlag(ctx context.Context, id string, enabled bool, updatedAt int64) error

	// Provider config CRUD
	UpdateProvidersConfig(ctx context.Context, providers map[schemas.ModelProvider]ProviderConfig, tx ...*gorm.DB) error
	AddProvider(ctx context.Context, provider schemas.ModelProvider, config ProviderConfig, tx ...*gorm.DB) error
	UpdateProvider(ctx context.Context, provider schemas.ModelProvider, config ProviderConfig, tx ...*gorm.DB) error
	DeleteProvider(ctx context.Context, provider schemas.ModelProvider, tx ...*gorm.DB) error
	GetProvidersConfig(ctx context.Context) (map[schemas.ModelProvider]ProviderConfig, error)
	GetProviderConfig(ctx context.Context, provider schemas.ModelProvider) (*ProviderConfig, error)
	GetProviderKeys(ctx context.Context, provider schemas.ModelProvider) ([]schemas.Key, error)
	GetProviderKey(ctx context.Context, provider schemas.ModelProvider, keyID string) (*schemas.Key, error)
	CreateProviderKey(ctx context.Context, provider schemas.ModelProvider, key schemas.Key, tx ...*gorm.DB) error
	UpdateProviderKey(ctx context.Context, provider schemas.ModelProvider, keyID string, key schemas.Key, tx ...*gorm.DB) error
	DeleteProviderKey(ctx context.Context, provider schemas.ModelProvider, keyID string, tx ...*gorm.DB) error
	GetProviders(ctx context.Context) ([]tables.TableProvider, error)
	GetProvider(ctx context.Context, provider schemas.ModelProvider) (*tables.TableProvider, error)
	UpdateStatus(ctx context.Context, provider schemas.ModelProvider, keyID string, status, errorMsg string) error

	// MCP config CRUD
	GetMCPConfig(ctx context.Context) (*schemas.MCPConfig, error)
	GetMCPClientByID(ctx context.Context, id string) (*tables.TableMCPClient, error)
	GetMCPClientConfigByID(ctx context.Context, id string) (*schemas.MCPClientConfig, error)
	GetMCPClientByName(ctx context.Context, name string) (*tables.TableMCPClient, error)
	GetMCPClientsPaginated(ctx context.Context, params MCPClientsQueryParams) ([]tables.TableMCPClient, int64, error)
	CreateMCPClientConfig(ctx context.Context, clientConfig *schemas.MCPClientConfig) error
	UpdateMCPClientConfig(ctx context.Context, id string, clientConfig *tables.TableMCPClient) error
	DeleteMCPClientConfig(ctx context.Context, id string) error

	// MCP library catalog (synced + org-custom)
	GetMCPLibraryPaginated(ctx context.Context, params MCPLibraryQueryParams) ([]tables.TableMCPLibrary, int64, error)
	GetMCPLibraryFilterData(ctx context.Context) (*MCPLibraryFilterData, error)
	UpsertMCPLibraryEntry(ctx context.Context, entry *tables.TableMCPLibrary, tx ...*gorm.DB) error
	// CreateCustomMCPLibraryEntry inserts an org-internal ("custom") library row.
	// Returns ErrAlreadyExists when the slug collides with an existing entry.
	CreateCustomMCPLibraryEntry(ctx context.Context, entry *tables.TableMCPLibrary) error
	// SoftDeleteMCPLibraryEntry tombstones a library row by ID (sets deleted_at)
	// so it is hidden from listings and never resurrected by the remote sync.
	SoftDeleteMCPLibraryEntry(ctx context.Context, id uint) error
	// DeleteMCPLibraryEntry removes a library row by ID, hard-deleting "custom"
	// rows (freeing their slug for re-add) and tombstoning "remote" rows.
	DeleteMCPLibraryEntry(ctx context.Context, id uint) error
	// GetProtectedMCPLibrarySlugs returns the slugs the remote sync must not
	// overwrite or recreate: custom rows and soft-deleted (tombstoned) rows.
	GetProtectedMCPLibrarySlugs(ctx context.Context) ([]string, error)

	// Vector store config CRUD
	UpdateVectorStoreConfig(ctx context.Context, config *vectorstore.Config) error
	GetVectorStoreConfig(ctx context.Context) (*vectorstore.Config, error)

	// Logs store config CRUD
	UpdateLogsStoreConfig(ctx context.Context, config *logstore.Config) error
	GetLogsStoreConfig(ctx context.Context) (*logstore.Config, error)

	// Config CRUD
	GetConfig(ctx context.Context, key string) (*tables.TableGovernanceConfig, error)
	UpdateConfig(ctx context.Context, config *tables.TableGovernanceConfig, tx ...*gorm.DB) error
	// GetComplexityAnalyzerConfig retrieves the persisted analyzer config, if configured.
	GetComplexityAnalyzerConfig(ctx context.Context) (*ComplexityAnalyzerConfig, error)
	// UpdateComplexityAnalyzerConfig persists the normalized analyzer config.
	UpdateComplexityAnalyzerConfig(ctx context.Context, config *ComplexityAnalyzerConfig, tx ...*gorm.DB) error

	// Plugins CRUD
	GetPlugins(ctx context.Context) ([]*tables.TablePlugin, error)
	GetPlugin(ctx context.Context, name string) (*tables.TablePlugin, error)
	CreatePlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error
	UpsertPlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error
	UpdatePlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error
	DeletePlugin(ctx context.Context, name string, tx ...*gorm.DB) error

	// Guardrail config CRUD (backs the loopback-guard guardrails UI)
	GetGuardrailConfigs(ctx context.Context, params GuardrailConfigsQueryParams) ([]tables.TableGuardrailConfig, int64, error)
	GetGuardrailConfig(ctx context.Context, id string) (*tables.TableGuardrailConfig, error)
	GetEnabledGuardrailConfigs(ctx context.Context) ([]tables.TableGuardrailConfig, error)
	CreateGuardrailConfig(ctx context.Context, config *tables.TableGuardrailConfig, tx ...*gorm.DB) error
	UpdateGuardrailConfig(ctx context.Context, config *tables.TableGuardrailConfig, tx ...*gorm.DB) error
	DeleteGuardrailConfig(ctx context.Context, id string, tx ...*gorm.DB) error

	// Circuit breaker config CRUD (backs the per-provider circuit-breaker UI).
	// One policy row per provider; the HTTP handler pushes enabled rows into the
	// running core engine via Bifrost.ConfigureCircuitBreaker.
	GetCircuitBreakerConfigs(ctx context.Context) ([]tables.TableCircuitBreakerConfig, error)
	GetCircuitBreakerConfig(ctx context.Context, id string) (*tables.TableCircuitBreakerConfig, error)
	GetCircuitBreakerConfigByProvider(ctx context.Context, provider string) (*tables.TableCircuitBreakerConfig, error)
	CreateCircuitBreakerConfig(ctx context.Context, config *tables.TableCircuitBreakerConfig, tx ...*gorm.DB) error
	UpdateCircuitBreakerConfig(ctx context.Context, config *tables.TableCircuitBreakerConfig, tx ...*gorm.DB) error
	DeleteCircuitBreakerConfig(ctx context.Context, id string, tx ...*gorm.DB) error

	// Prompt deployment CRUD (backs the prompt deployments UI). A deployment is a
	// named, weighted traffic-routing strategy owned by a single prompt; the
	// prompts plugin's deployment resolver reads enabled deployments to split
	// live traffic across versions with a latest-version fallback.
	GetPromptDeployments(ctx context.Context, promptID string) ([]tables.TablePromptDeployment, error)
	GetPromptDeployment(ctx context.Context, id string) (*tables.TablePromptDeployment, error)
	GetActivePromptDeployments(ctx context.Context) ([]tables.TablePromptDeployment, error)
	CreatePromptDeployment(ctx context.Context, deployment *tables.TablePromptDeployment, tx ...*gorm.DB) error
	UpdatePromptDeployment(ctx context.Context, deployment *tables.TablePromptDeployment, tx ...*gorm.DB) error
	DeletePromptDeployment(ctx context.Context, id string, tx ...*gorm.DB) error

	// MCP tool group CRUD (backs the MCP tool groups governance UI). Read paths
	// respect any QueryScope on ctx so MCP visibility filtering narrows the
	// returned groups to those the caller may see (EntityMCPToolGroup).
	GetMCPToolGroups(ctx context.Context, params MCPToolGroupsQueryParams) ([]tables.TableMCPToolGroup, int64, error)
	GetMCPToolGroup(ctx context.Context, id string) (*tables.TableMCPToolGroup, error)
	CreateMCPToolGroup(ctx context.Context, group *tables.TableMCPToolGroup, tx ...*gorm.DB) error
	UpdateMCPToolGroup(ctx context.Context, group *tables.TableMCPToolGroup, tx ...*gorm.DB) error
	DeleteMCPToolGroup(ctx context.Context, id string, tx ...*gorm.DB) error

	// PII rule CRUD (backs the loopback-guard PII redactor UI)
	GetPIIRules(ctx context.Context, params PIIRulesQueryParams) ([]tables.TablePIIRule, int64, error)
	GetPIIRule(ctx context.Context, id string) (*tables.TablePIIRule, error)
	GetEnabledPIIRules(ctx context.Context) ([]tables.TablePIIRule, error)
	CreatePIIRule(ctx context.Context, rule *tables.TablePIIRule, tx ...*gorm.DB) error
	UpdatePIIRule(ctx context.Context, rule *tables.TablePIIRule, tx ...*gorm.DB) error
	DeletePIIRule(ctx context.Context, id string, tx ...*gorm.DB) error

	// Audit log append/query (backs the audit-logs UI). The log is append-only:
	// rows are created and read, never updated or deleted.
	GetAuditLogs(ctx context.Context, params AuditLogsQueryParams) ([]tables.TableAuditLog, int64, error)
	CreateAuditLog(ctx context.Context, log *tables.TableAuditLog, tx ...*gorm.DB) error

	// Audit log export + retention (Loopback Gateway). GetAuditLogsSince is the
	// keyset-paginated iterator behind the NDJSON export/verify endpoints:
	// rows strictly after the (afterTimestamp, afterID) cursor, ordered
	// (timestamp ASC, id ASC), honoring the params filters (Limit/Offset in
	// params are ignored — limit governs the page size). DeleteAuditLogsBefore
	// and TrimAuditLogsToCount are the retention worker's batched pruning
	// primitives; both delete oldest-first and return the deleted rows'
	// signatures so the worker can anchor the deletion in a signed
	// "audit_log.prune" marker event (per-row HMAC signing means surviving rows
	// stay independently verifiable after any subset deletion).
	GetAuditLogSettings(ctx context.Context) (*tables.TableAuditLogSettings, error)
	UpdateAuditLogSettings(ctx context.Context, settings *tables.TableAuditLogSettings) error
	DeleteAuditLogsBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, []string, error)
	TrimAuditLogsToCount(ctx context.Context, maxRows int64, batchSize int) (int64, []string, error)
	GetAuditLogsSince(ctx context.Context, params AuditLogsQueryParams, afterTimestamp time.Time, afterID string, limit int) ([]tables.TableAuditLog, error)

	// Alert channel CRUD (backs the alert-channels UI and the
	// framework/alerting dispatcher). UpdateAlertChannelDeliveryStatus is the
	// dispatcher's narrow best-effort bookkeeping write — it touches only the
	// last-attempt columns so delivery status never races a concurrent admin
	// edit of the channel definition.
	GetAlertChannels(ctx context.Context) ([]tables.TableAlertChannel, error)
	GetAlertChannel(ctx context.Context, id string) (*tables.TableAlertChannel, error)
	CreateAlertChannel(ctx context.Context, channel *tables.TableAlertChannel, tx ...*gorm.DB) error
	UpdateAlertChannel(ctx context.Context, channel *tables.TableAlertChannel, tx ...*gorm.DB) error
	DeleteAlertChannel(ctx context.Context, id string, tx ...*gorm.DB) error
	UpdateAlertChannelDeliveryStatus(ctx context.Context, id string, attemptAt time.Time, status string, lastError string) error

	// JWT auth issuer config CRUD (backs the data-plane JWT→virtual-key auth
	// UI and the transport's JWT middleware snapshot).
	GetJWTAuthConfigs(ctx context.Context) ([]tables.TableJWTAuthConfig, error)
	GetJWTAuthConfig(ctx context.Context, id string) (*tables.TableJWTAuthConfig, error)
	CreateJWTAuthConfig(ctx context.Context, config *tables.TableJWTAuthConfig, tx ...*gorm.DB) error
	UpdateJWTAuthConfig(ctx context.Context, config *tables.TableJWTAuthConfig, tx ...*gorm.DB) error
	DeleteJWTAuthConfig(ctx context.Context, id string, tx ...*gorm.DB) error

	// User & business unit CRUD (backs the user & org management UI)
	GetUsers(ctx context.Context, params UsersQueryParams) ([]tables.TableUser, int64, error)
	GetUser(ctx context.Context, id string) (*tables.TableUser, error)
	CreateUser(ctx context.Context, user *tables.TableUser, tx ...*gorm.DB) error
	UpdateUser(ctx context.Context, user *tables.TableUser, tx ...*gorm.DB) error
	DeleteUser(ctx context.Context, id string, tx ...*gorm.DB) error

	GetBusinessUnits(ctx context.Context, params BusinessUnitsQueryParams) ([]tables.TableBusinessUnit, int64, error)
	GetBusinessUnit(ctx context.Context, id string) (*tables.TableBusinessUnit, error)
	CreateBusinessUnit(ctx context.Context, bu *tables.TableBusinessUnit, tx ...*gorm.DB) error
	UpdateBusinessUnit(ctx context.Context, bu *tables.TableBusinessUnit, tx ...*gorm.DB) error
	DeleteBusinessUnit(ctx context.Context, id string, tx ...*gorm.DB) error

	// SCIM provisioning CRUD (backs the SSO/SCIM sync engine — Keycloak in this
	// slice). UpsertSCIMUser/Group are idempotent on (provider, external_id) so
	// repeated syncs converge without duplicates.
	GetSCIMUsers(ctx context.Context, params SCIMUsersQueryParams) ([]tables.TableSCIMUser, int64, error)
	GetSCIMUser(ctx context.Context, id string) (*tables.TableSCIMUser, error)
	GetSCIMUserByExternalID(ctx context.Context, provider, externalID string) (*tables.TableSCIMUser, error)
	UpsertSCIMUser(ctx context.Context, user *tables.TableSCIMUser, tx ...*gorm.DB) error
	DeleteSCIMUser(ctx context.Context, id string, tx ...*gorm.DB) error
	GetSCIMGroups(ctx context.Context, params SCIMGroupsQueryParams) ([]tables.TableSCIMGroup, int64, error)
	GetSCIMGroupByExternalID(ctx context.Context, provider, externalID string) (*tables.TableSCIMGroup, error)
	UpsertSCIMGroup(ctx context.Context, group *tables.TableSCIMGroup, tx ...*gorm.DB) error
	DeleteSCIMGroup(ctx context.Context, id string, tx ...*gorm.DB) error

	// RBAC role / permission / assignment CRUD (backs the RBAC & access control UI)
	GetRoles(ctx context.Context, params RolesQueryParams) ([]tables.TableRole, int64, error)
	GetRole(ctx context.Context, id string) (*tables.TableRole, error)
	GetRoleByName(ctx context.Context, name string) (*tables.TableRole, error)
	CreateRole(ctx context.Context, role *tables.TableRole, tx ...*gorm.DB) error
	UpdateRole(ctx context.Context, role *tables.TableRole, tx ...*gorm.DB) error
	DeleteRole(ctx context.Context, id string, tx ...*gorm.DB) error
	ReplaceRolePermissions(ctx context.Context, roleID string, perms []tables.TablePermission, tx ...*gorm.DB) error

	GetRoleAssignments(ctx context.Context, params RoleAssignmentsQueryParams) ([]tables.TableRoleAssignment, int64, error)
	GetRoleAssignment(ctx context.Context, id string) (*tables.TableRoleAssignment, error)
	GetRoleAssignmentsByUser(ctx context.Context, userID string) ([]tables.TableRoleAssignment, error)
	CreateRoleAssignment(ctx context.Context, assignment *tables.TableRoleAssignment, tx ...*gorm.DB) error
	DeleteRoleAssignment(ctx context.Context, id string, tx ...*gorm.DB) error
	CountRoleAssignments(ctx context.Context) (int64, error)

	// Admin API key CRUD (backs the scope-based admin API keys UI). Keys are
	// stored hashed (value_hash); GetAPIKeyByValueHash is the middleware lookup
	// path and TouchAPIKeyLastUsed is its throttled usage stamp.
	GetAPIKeys(ctx context.Context, params APIKeysQueryParams) ([]tables.TableAdminAPIKey, int64, error)
	GetAPIKey(ctx context.Context, id string) (*tables.TableAdminAPIKey, error)
	GetAPIKeyByValueHash(ctx context.Context, valueHash string) (*tables.TableAdminAPIKey, error)
	CreateAPIKey(ctx context.Context, key *tables.TableAdminAPIKey, tx ...*gorm.DB) error
	UpdateAPIKey(ctx context.Context, key *tables.TableAdminAPIKey, tx ...*gorm.DB) error
	DeleteAPIKey(ctx context.Context, id string, tx ...*gorm.DB) error
	ReplaceAPIKeyScopes(ctx context.Context, keyID string, scopes []tables.TableAdminAPIKeyScope, tx ...*gorm.DB) error
	TouchAPIKeyLastUsed(ctx context.Context, id string, usedAt time.Time) error

	// Governance config CRUD
	GetVirtualKeys(ctx context.Context) ([]tables.TableVirtualKey, error)
	GetVirtualKeysPaginated(ctx context.Context, params VirtualKeyQueryParams) ([]tables.TableVirtualKey, int64, error)
	GetRedactedVirtualKeys(ctx context.Context, ids []string) ([]tables.TableVirtualKey, error) // leave ids empty to get all
	GetVirtualKey(ctx context.Context, id string) (*tables.TableVirtualKey, error)
	GetVirtualKeyByValue(ctx context.Context, value string) (*tables.TableVirtualKey, error)
	GetVirtualKeyQuotaByValue(ctx context.Context, value string) (*tables.TableVirtualKey, error)
	CreateVirtualKey(ctx context.Context, virtualKey *tables.TableVirtualKey, tx ...*gorm.DB) error
	UpdateVirtualKey(ctx context.Context, virtualKey *tables.TableVirtualKey, tx ...*gorm.DB) error
	DeleteVirtualKey(ctx context.Context, id string, tx ...*gorm.DB) error

	// Virtual key provider config CRUD
	GetVirtualKeyProviderConfigs(ctx context.Context, virtualKeyID string) ([]tables.TableVirtualKeyProviderConfig, error)
	CreateVirtualKeyProviderConfig(ctx context.Context, virtualKeyProviderConfig *tables.TableVirtualKeyProviderConfig, tx ...*gorm.DB) error
	UpdateVirtualKeyProviderConfig(ctx context.Context, virtualKeyProviderConfig *tables.TableVirtualKeyProviderConfig, tx ...*gorm.DB) error
	DeleteVirtualKeyProviderConfig(ctx context.Context, id uint, tx ...*gorm.DB) error

	// Virtual key MCP config CRUD
	GetVirtualKeyMCPConfigs(ctx context.Context, virtualKeyID string) ([]tables.TableVirtualKeyMCPConfig, error)
	GetVirtualKeyMCPConfigsByMCPClientID(ctx context.Context, mcpClientID uint) ([]tables.TableVirtualKeyMCPConfig, error)
	GetVirtualKeyMCPConfigsByMCPClientIDs(ctx context.Context, mcpClientIDs []uint) ([]tables.TableVirtualKeyMCPConfig, error)
	GetVirtualKeyMCPConfigsByMCPClientStringIDs(ctx context.Context, clientIDs []string) ([]tables.TableVirtualKeyMCPConfig, error)
	CreateVirtualKeyMCPConfig(ctx context.Context, virtualKeyMCPConfig *tables.TableVirtualKeyMCPConfig, tx ...*gorm.DB) error
	UpdateVirtualKeyMCPConfig(ctx context.Context, virtualKeyMCPConfig *tables.TableVirtualKeyMCPConfig, tx ...*gorm.DB) error
	DeleteVirtualKeyMCPConfig(ctx context.Context, id uint, tx ...*gorm.DB) error

	// Team CRUD
	GetTeams(ctx context.Context, customerID string) ([]tables.TableTeam, error)
	GetTeamsPaginated(ctx context.Context, params TeamsQueryParams) ([]tables.TableTeam, int64, error)
	GetTeam(ctx context.Context, id string) (*tables.TableTeam, error)
	GetTeamByName(ctx context.Context, name string, customerID string) (*tables.TableTeam, error)
	GetTeamBySourceID(ctx context.Context, sourceID string) (*tables.TableTeam, error)
	CreateTeam(ctx context.Context, team *tables.TableTeam, tx ...*gorm.DB) error
	UpdateTeam(ctx context.Context, team *tables.TableTeam, tx ...*gorm.DB) error
	DeleteTeam(ctx context.Context, id string, tx ...*gorm.DB) error

	// Customer CRUD
	GetCustomers(ctx context.Context) ([]tables.TableCustomer, error)
	GetCustomersPaginated(ctx context.Context, params CustomersQueryParams) ([]tables.TableCustomer, int64, error)
	GetCustomer(ctx context.Context, id string) (*tables.TableCustomer, error)
	CreateCustomer(ctx context.Context, customer *tables.TableCustomer, tx ...*gorm.DB) error
	UpdateCustomer(ctx context.Context, customer *tables.TableCustomer, tx ...*gorm.DB) error
	DeleteCustomer(ctx context.Context, id string, tx ...*gorm.DB) error

	// Rate limit CRUD
	GetRateLimits(ctx context.Context) ([]tables.TableRateLimit, error)
	GetRateLimit(ctx context.Context, id string, tx ...*gorm.DB) (*tables.TableRateLimit, error)
	CreateRateLimit(ctx context.Context, rateLimit *tables.TableRateLimit, tx ...*gorm.DB) error
	UpdateRateLimit(ctx context.Context, rateLimit *tables.TableRateLimit, tx ...*gorm.DB) error
	UpdateRateLimits(ctx context.Context, rateLimits []*tables.TableRateLimit, tx ...*gorm.DB) error
	DeleteRateLimit(ctx context.Context, id string, tx ...*gorm.DB) error

	// Budget CRUD
	GetBudgets(ctx context.Context) ([]tables.TableBudget, error)
	GetBudget(ctx context.Context, id string, tx ...*gorm.DB) (*tables.TableBudget, error)
	CreateBudget(ctx context.Context, budget *tables.TableBudget, tx ...*gorm.DB) error
	UpdateBudget(ctx context.Context, budget *tables.TableBudget, tx ...*gorm.DB) error
	UpdateBudgets(ctx context.Context, budgets []*tables.TableBudget, tx ...*gorm.DB) error
	DeleteBudget(ctx context.Context, id string, tx ...*gorm.DB) error
	UpdateBudgetUsage(ctx context.Context, id string, currentUsage float64, tx ...*gorm.DB) error
	UpdateRateLimitUsage(ctx context.Context, id string, tokenCurrentUsage int64, requestCurrentUsage int64, tx ...*gorm.DB) error

	// Routing Rules CRUD
	GetRoutingRules(ctx context.Context) ([]tables.TableRoutingRule, error)
	GetRoutingRulesByScope(ctx context.Context, scope string, scopeID string) ([]tables.TableRoutingRule, error)
	GetRoutingRule(ctx context.Context, id string) (*tables.TableRoutingRule, error)
	GetRedactedRoutingRules(ctx context.Context, ids []string) ([]tables.TableRoutingRule, error) // leave ids empty to get all
	GetRoutingRulesPaginated(ctx context.Context, params RoutingRulesQueryParams) ([]tables.TableRoutingRule, int64, error)
	CreateRoutingRule(ctx context.Context, rule *tables.TableRoutingRule, tx ...*gorm.DB) error
	UpdateRoutingRule(ctx context.Context, rule *tables.TableRoutingRule, tx ...*gorm.DB) error
	DeleteRoutingRule(ctx context.Context, id string, tx ...*gorm.DB) error

	// Model config CRUD
	GetModelConfigs(ctx context.Context) ([]tables.TableModelConfig, error)
	GetModelConfigsByScopeAndScopeIDs(ctx context.Context, scope string, scopeIDs []string) ([]tables.TableModelConfig, error)
	GetProviderGovernanceModelConfigs(ctx context.Context) ([]tables.TableModelConfig, error)
	GetModelConfigsPaginated(ctx context.Context, params ModelConfigsQueryParams) ([]tables.TableModelConfig, int64, error)
	GetModelConfig(ctx context.Context, scope string, scopeID *string, modelName string, provider *string) (*tables.TableModelConfig, error)
	GetModelConfigByID(ctx context.Context, id string) (*tables.TableModelConfig, error)
	CreateModelConfig(ctx context.Context, modelConfig *tables.TableModelConfig, tx ...*gorm.DB) error
	UpdateModelConfig(ctx context.Context, modelConfig *tables.TableModelConfig, tx ...*gorm.DB) error
	UpdateModelConfigs(ctx context.Context, modelConfigs []*tables.TableModelConfig, tx ...*gorm.DB) error
	DeleteModelConfig(ctx context.Context, id string, tx ...*gorm.DB) error
	// DeleteModelConfigsForScope deletes all model configs (and their owned budgets/rate-limits) for a scope owner. Must run inside the owner-delete transaction.
	DeleteModelConfigsForScope(ctx context.Context, tx *gorm.DB, scope, scopeID string) error

	// Governance config CRUD
	GetGovernanceConfig(ctx context.Context) (*GovernanceConfig, error)

	// Auth config CRUD
	GetAuthConfig(ctx context.Context) (*AuthConfig, error)
	UpdateAuthConfig(ctx context.Context, config *AuthConfig) error

	// Proxy config CRUD
	GetProxyConfig(ctx context.Context) (*tables.GlobalProxyConfig, error)
	UpdateProxyConfig(ctx context.Context, config *tables.GlobalProxyConfig) error

	// Large-payload streaming config CRUD
	GetLargePayloadConfig(ctx context.Context) (*tables.LargePayloadConfig, error)
	UpdateLargePayloadConfig(ctx context.Context, config *tables.LargePayloadConfig) error

	// Restart required config CRUD
	GetRestartRequiredConfig(ctx context.Context) (*tables.RestartRequiredConfig, error)
	SetRestartRequiredConfig(ctx context.Context, config *tables.RestartRequiredConfig) error
	ClearRestartRequiredConfig(ctx context.Context) error

	// Session CRUD
	GetSession(ctx context.Context, token string) (*tables.SessionsTable, error)
	CreateSession(ctx context.Context, session *tables.SessionsTable) error
	DeleteSession(ctx context.Context, token string) error
	FlushSessions(ctx context.Context) error

	// Temp token CRUD
	CreateTempToken(ctx context.Context, token *tables.TempToken, tx ...*gorm.DB) error
	GetTempTokenByHash(ctx context.Context, tokenHash string) (*tables.TempToken, error)
	// DeleteTempTokensByResourceID removes every row matching (scope, resource_id).
	// Used by lifecycle owners (e.g. OAuth provider on flow termination) to burn
	// the link as soon as the work it authorized is finished.
	DeleteTempTokensByResourceID(ctx context.Context, scope, resourceID string, tx ...*gorm.DB) (int64, error)
	DeleteExpiredTempTokens(ctx context.Context, before time.Time) (int64, error)

	// Model pricing CRUD
	GetModelPrices(ctx context.Context) ([]tables.TableModelPricing, error)
	UpsertModelPrices(ctx context.Context, pricing *tables.TableModelPricing, tx ...*gorm.DB) error
	DeleteModelPrices(ctx context.Context, tx ...*gorm.DB) error

	// UpsertModelPricingAttributes writes only the additional_attributes column
	// on the pricing rows keyed by (model, provider). Returns the number of
	// rows updated; 0 means no such pricing row exists.
	UpsertModelPricingAttributes(ctx context.Context, model, provider string, attrs map[string]string, tx ...*gorm.DB) (int64, error)

	// Governance pricing overrides CRUD
	GetPricingOverrides(ctx context.Context, filters PricingOverrideFilters) ([]tables.TablePricingOverride, error)
	GetPricingOverridesPaginated(ctx context.Context, params PricingOverridesQueryParams) ([]tables.TablePricingOverride, int64, error)
	GetPricingOverrideByID(ctx context.Context, id string) (*tables.TablePricingOverride, error)
	CreatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error
	UpdatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error
	DeletePricingOverride(ctx context.Context, id string, tx ...*gorm.DB) error

	// Model parameters
	GetModelParameters(ctx context.Context) ([]tables.TableModelParameters, error)
	GetModelParametersByModel(ctx context.Context, model string) (*tables.TableModelParameters, error)
	UpsertModelParameters(ctx context.Context, params *tables.TableModelParameters, tx ...*gorm.DB) error

	// Key management
	GetKeysByIDs(ctx context.Context, ids []string) ([]tables.TableKey, error)
	GetKeysByProvider(ctx context.Context, provider string) ([]tables.TableKey, error)
	GetAllRedactedKeys(ctx context.Context, ids []string) ([]schemas.Key, error) // leave ids empty to get all

	// Generic transaction manager
	ExecuteTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error

	// TryAcquireLock attempts to insert a lock row. Returns true if the lock was acquired.
	// If the lock already exists and is not expired, returns false.
	TryAcquireLock(ctx context.Context, lock *tables.TableDistributedLock) (bool, error)

	// GetLock retrieves a lock by its key. Returns nil if the lock doesn't exist.
	GetLock(ctx context.Context, lockKey string) (*tables.TableDistributedLock, error)

	// UpdateLockExpiry updates the expiration time for an existing lock.
	// Only succeeds if the holder ID matches the current lock holder.
	UpdateLockExpiry(ctx context.Context, lockKey, holderID string, expiresAt time.Time) error

	// ReleaseLock deletes a lock if the holder ID matches.
	// Returns true if the lock was released, false if it wasn't held by the given holder.
	ReleaseLock(ctx context.Context, lockKey, holderID string) (bool, error)

	// CleanupExpiredLockByKey atomically deletes a specific lock only if it has expired.
	// Returns true if an expired lock was deleted, false if the lock doesn't exist or hasn't expired.
	CleanupExpiredLockByKey(ctx context.Context, lockKey string) (bool, error)

	// CleanupExpiredLocks removes all locks that have expired.
	// Returns the number of locks cleaned up.
	CleanupExpiredLocks(ctx context.Context) (int64, error)

	// OAuth config CRUD
	GetOauthConfigByID(ctx context.Context, id string) (*tables.TableOauthConfig, error)
	GetOauthConfigsByIDs(ctx context.Context, ids []string) (map[string]*tables.TableOauthConfig, error)
	GetOauthConfigByState(ctx context.Context, state string) (*tables.TableOauthConfig, error)
	GetOauthConfigByTokenID(ctx context.Context, tokenID string) (*tables.TableOauthConfig, error)
	CreateOauthConfig(ctx context.Context, config *tables.TableOauthConfig) error
	UpdateOauthConfig(ctx context.Context, config *tables.TableOauthConfig) error

	// OAuth token CRUD
	GetOauthTokenByID(ctx context.Context, id string) (*tables.TableOauthToken, error)
	GetExpiringOauthTokens(ctx context.Context, before time.Time) ([]*tables.TableOauthToken, error)
	CreateOauthToken(ctx context.Context, token *tables.TableOauthToken) error
	UpdateOauthToken(ctx context.Context, token *tables.TableOauthToken) error
	DeleteOauthToken(ctx context.Context, id string) error

	// Per-user OAuth session CRUD
	GetOauthUserSessionByID(ctx context.Context, id string) (*tables.TableOauthUserSession, error)
	ClaimOauthUserSessionByState(ctx context.Context, state string) (*tables.TableOauthUserSession, error)
	// GetOauthUserSessionByModeIdentityAndMCPClient returns the canonical flow
	// row for an (identity, mcp_client) binding. Used at flow-init time as the
	// single source of truth: reauth updates this row in place rather than
	// inserting a new one. Returns (nil, nil) when no row exists.
	GetOauthUserSessionByModeIdentityAndMCPClient(ctx context.Context, mode schemas.MCPAuthMode, identity, mcpClientID string) (*tables.TableOauthUserSession, error)
	CreateOauthUserSession(ctx context.Context, session *tables.TableOauthUserSession) error
	UpdateOauthUserSession(ctx context.Context, session *tables.TableOauthUserSession) error

	// Per-user OAuth token CRUD
	// GetOauthUserTokenByMode looks up the active token row keyed by a single
	// identity dimension. Filters status='active'. identity is the user ID for
	// AuthModeUser, the VK row ID for AuthModeVK, and the session ID for
	// AuthModeSession.
	GetOauthUserTokenByMode(ctx context.Context, mode schemas.MCPAuthMode, identity, mcpClientID string) (*tables.TableOauthUserToken, error)
	CreateOauthUserToken(ctx context.Context, token *tables.TableOauthUserToken) error
	UpdateOauthUserToken(ctx context.Context, token *tables.TableOauthUserToken) error
	DeleteOauthUserToken(ctx context.Context, id string) error
	// DeleteOauthUserSession hard-deletes a single flow row by primary key.
	// Used by CompleteUserOAuthFlow on terminal transitions so completed,
	// failed, and expired-at-completion flows don't accumulate. The UI
	// treats 404 on flow-detail as "expired or completed".
	DeleteOauthUserSession(ctx context.Context, id string) error
	// DeleteOauthUserSessionsByModeIdentityAndMCPClient hard-deletes any flow
	// rows matching the given identity column + MCP client. Used by revoke
	// across all auth modes so subsequent OAuth init starts from a clean slate.
	DeleteOauthUserSessionsByModeIdentityAndMCPClient(ctx context.Context, mode schemas.MCPAuthMode, identity, mcpClientID string) error
	// MarkOauthUserTokenNeedsReauthByID flips status to 'needs_reauth'
	// on a single token row. Called by the refresh-failure path when
	// the upstream credential is permanently rejected: the row stays
	// (preserves audit + binding for re-auth), but is filtered from
	// active lookups so the next inference triggers a fresh OAuth
	// flow that upserts the row back to 'active'.
	MarkOauthUserTokenNeedsReauthByID(ctx context.Context, tokenID string) error
	// GetOauthUserTokenByID looks up a single token row by primary key.
	// Returns nil, nil when not found.
	GetOauthUserTokenByID(ctx context.Context, id string) (*tables.TableOauthUserToken, error)
	// ListOauthUserTokens returns token rows matching the supplied filters,
	// regardless of status. The sessions UI renders all three states
	// (active / orphaned / needs_reauth) with distinct affordances, so
	// hiding any of them by default would only break the user's ability
	// to act on rows that need their attention; status filtering is the
	// caller's responsibility via params.Statuses. Runtime token lookups
	// apply their own status='active' filter and don't go through this
	// method.
	ListOauthUserTokens(ctx context.Context, params MCPSessionsFilterParams) ([]tables.TableOauthUserToken, error)
	// ListPendingOauthUserSessions returns pending OAuth flow rows matching
	// the supplied filters. Companion to ListOauthUserTokens for the admin
	// view. Always restricted to status='pending' AND expires_at > now;
	// params.Statuses further narrows within that set.
	ListPendingOauthUserSessions(ctx context.Context, params MCPSessionsFilterParams) ([]tables.TableOauthUserSession, error)
	// DeleteExpiredOauthUserSessions hard-deletes pending OAuth flow rows
	// whose ExpiresAt has passed. Returns the number of rows removed.
	DeleteExpiredOauthUserSessions(ctx context.Context) (int64, error)
	// DeleteOrphanedOauthUserTokens hard-deletes token rows where status='orphaned'
	// and updated_at is older than olderThan. Returns the number of rows removed.
	DeleteOrphanedOauthUserTokens(ctx context.Context, olderThan time.Duration) (int64, error)

	// Per-user MCP header credential CRUD. Storage analog of per-user OAuth
	// tokens for MCPAuthTypePerUserHeaders clients. The row holds an encrypted
	// JSON blob of header_name → value pairs keyed by (auth_mode, identity,
	// mcp_client_id).
	GetMCPPerUserHeaderCredentialByMode(ctx context.Context, mode schemas.MCPAuthMode, identity, mcpClientID string) (*tables.TableMCPPerUserHeaderCredential, error)
	GetMCPPerUserHeaderCredentialByID(ctx context.Context, id string) (*tables.TableMCPPerUserHeaderCredential, error)
	UpsertMCPPerUserHeaderCredential(ctx context.Context, cred *tables.TableMCPPerUserHeaderCredential) error
	DeleteMCPPerUserHeaderCredential(ctx context.Context, id string) error
	// ListMCPPerUserHeaderCredentials returns credential rows matching the
	// supplied filters, regardless of status. Mirrors ListOauthUserTokens —
	// the sessions UI surfaces non-active states (needs_update / orphaned)
	// with distinct affordances, so status filtering is the caller's
	// responsibility via params.Statuses.
	ListMCPPerUserHeaderCredentials(ctx context.Context, params MCPSessionsFilterParams) ([]tables.TableMCPPerUserHeaderCredential, error)
	// MarkMCPPerUserHeaderCredentialsNeedsUpdate flips status to 'needs_update'
	// for every row tied to mcpClientID. Called when the admin changes
	// PerUserHeaderKeys on the MCP client config: existing user submissions
	// stay (so the UI can prefill known values) but are excluded from runtime
	// lookups until the user re-submits.
	MarkMCPPerUserHeaderCredentialsNeedsUpdate(ctx context.Context, mcpClientID string) error
	// DeleteOrphanedMCPPerUserHeaderCredentials hard-deletes rows where
	// status='orphaned' and updated_at is older than olderThan.
	DeleteOrphanedMCPPerUserHeaderCredentials(ctx context.Context, olderThan time.Duration) (int64, error)

	// Per-user-headers submission flow CRUD. Mirrors the OAuth user-session
	// surface — the resolver creates a pending flow row when the inline-401
	// fires, the submit endpoint deletes the row on success, and the sweep
	// worker reaps expired pending rows.
	CreateMCPPerUserHeaderFlow(ctx context.Context, flow *tables.TableMCPPerUserHeaderFlow) error
	GetMCPPerUserHeaderFlowByID(ctx context.Context, id string) (*tables.TableMCPPerUserHeaderFlow, error)
	// GetMCPPerUserHeaderFlowByModeIdentityAndMCPClient returns the canonical
	// pending flow row for the (mode, identity, mcp_client) triple, if any.
	// Companion to GetOauthUserSessionByModeIdentityAndMCPClient — used by
	// InitiateUserSubmissionFlow to keep at most one pending row per binding
	// (mirrors OAuth's single-row-per-binding invariant).
	GetMCPPerUserHeaderFlowByModeIdentityAndMCPClient(ctx context.Context, mode schemas.MCPAuthMode, identity, mcpClientID string) (*tables.TableMCPPerUserHeaderFlow, error)
	// UpdateMCPPerUserHeaderFlow updates a flow row in place. Used on the
	// reauth/re-init path to rotate ExpiresAt without spawning a new row.
	UpdateMCPPerUserHeaderFlow(ctx context.Context, flow *tables.TableMCPPerUserHeaderFlow) error
	// DeleteMCPPerUserHeaderFlowsByModeIdentityAndMCPClient hard-deletes any
	// pending flow rows for a binding. Called from revoke so a credential
	// delete also clears any in-flight resubmission flow for the same
	// (mode, identity, mcp_client). Mirrors
	// DeleteOauthUserSessionsByModeIdentityAndMCPClient.
	DeleteMCPPerUserHeaderFlowsByModeIdentityAndMCPClient(ctx context.Context, mode schemas.MCPAuthMode, identity, mcpClientID string) error
	DeleteMCPPerUserHeaderFlow(ctx context.Context, id string) error
	// ListPendingMCPPerUserHeaderFlows returns non-expired pending header
	// submission flow rows matching the supplied filters. Mirrors
	// ListPendingOauthUserSessions on the OAuth side. Always restricted to
	// status='pending' AND expires_at > now; params.Statuses further
	// narrows within that set. The implementation reads via ScopedDB(ctx),
	// so a query-scope stashed on ctx (e.g. by enterprise DAC) narrows the
	// result; with no scope, every matching pending row is returned.
	ListPendingMCPPerUserHeaderFlows(ctx context.Context, params MCPSessionsFilterParams) ([]tables.TableMCPPerUserHeaderFlow, error)
	// DeleteExpiredMCPPerUserHeaderFlows hard-deletes pending flow rows whose
	// ExpiresAt has passed. Returns the number of rows removed.
	DeleteExpiredMCPPerUserHeaderFlows(ctx context.Context) (int64, error)

	// Per-user credential reconciliation.
	//
	// Called whenever a VK ↔ MCP grant might have changed (direct
	// dashboard edit, AP propagation, SCIM auto-assign). Orphans
	// vk-keyed credentials whose MCP is no longer in the VK's effective
	// allowlist (explicit per-VK row ∪ MCPs with
	// AllowOnAllVirtualKeys=true) and reactivates orphaned rows when the
	// grant returns. Pending flow rows for lost grants are hard-deleted.
	//
	// Session-keyed rows are never touched — they carry no notion of
	// "lost access".
	//
	// Handlers should invoke both surfaces (OAuth + headers) after every
	// grant-change so both stay consistent.
	ReconcileOauthAfterVKChange(ctx context.Context, vkID string) error
	ReconcileMCPHeadersAfterVKChange(ctx context.Context, vkID string) error
	// MCP-side variants: called when the change originates on the MCP
	// client (vk_configs edit OR AllowOnAllVirtualKeys toggle). Each
	// re-evaluates every VK that holds a credential for the changed MCP.
	ReconcileOauthAfterMCPChange(ctx context.Context, mcpClientID string) error
	ReconcileMCPHeadersAfterMCPChange(ctx context.Context, mcpClientID string) error

	// Not found retry wrapper
	RetryOnNotFound(ctx context.Context, fn func(ctx context.Context) (any, error), maxRetries int, retryDelay time.Duration) (any, error)

	// Prompt Repository - Folders
	GetFolders(ctx context.Context) ([]tables.TableFolder, error)
	GetFolderByID(ctx context.Context, id string) (*tables.TableFolder, error)
	CreateFolder(ctx context.Context, folder *tables.TableFolder) error
	UpdateFolder(ctx context.Context, folder *tables.TableFolder) error
	DeleteFolder(ctx context.Context, id string) error

	// Prompt Repository - Prompts
	GetPrompts(ctx context.Context, folderID *string) ([]tables.TablePrompt, error)
	GetPromptByID(ctx context.Context, id string) (*tables.TablePrompt, error)
	CreatePrompt(ctx context.Context, prompt *tables.TablePrompt, tx ...*gorm.DB) error
	UpdatePrompt(ctx context.Context, prompt *tables.TablePrompt) error
	DeletePrompt(ctx context.Context, id string) error

	// Prompt Repository - Versions
	GetAllPromptVersions(ctx context.Context) ([]tables.TablePromptVersion, error)
	GetPromptVersions(ctx context.Context, promptID string) ([]tables.TablePromptVersion, error)
	GetPromptVersionByID(ctx context.Context, id uint) (*tables.TablePromptVersion, error)
	GetLatestPromptVersion(ctx context.Context, promptID string) (*tables.TablePromptVersion, error)
	CreatePromptVersion(ctx context.Context, version *tables.TablePromptVersion) error
	DeletePromptVersion(ctx context.Context, id uint) error

	// Skills Repository
	CreateSkill(ctx context.Context, skill *tables.TableSkill, version string, objectStore objectstore.ObjectStore) error
	GetSkill(ctx context.Context, id string) (*tables.TableSkill, error)
	GetSkillLean(ctx context.Context, id string) (*tables.TableSkill, error)
	GetSkillByName(ctx context.Context, name string) (*tables.TableSkill, error)
	GetSkillVersion(ctx context.Context, skillID, version string) (*tables.TableSkillVersion, error)
	ListSkillVersions(ctx context.Context, skillID string, params SkillVersionListQueryParams) ([]tables.TableSkillVersion, int64, error)
	UpdateSkill(ctx context.Context, skill *tables.TableSkill, version string, serve bool, objectStore objectstore.ObjectStore) error
	DeleteSkill(ctx context.Context, id string, objectStore objectstore.ObjectStore) error
	ListSkills(ctx context.Context, params SkillListQueryParams) ([]tables.TableSkill, int64, error)
	ShiftSkillVersion(ctx context.Context, skillID string, targetVersion string, objectStore objectstore.ObjectStore) error
	GetAllSkillsVersion(ctx context.Context) (string, error)
	BumpAllSkillsVersion(ctx context.Context, bump string) (string, error)
	CreateSkillFileBlob(ctx context.Context, blob *tables.TableSkillFileBlob) error
	CleanupOrphanSkillFileBlobs(ctx context.Context, force bool) (int64, error)
	UpdateSkillConfigHash(ctx context.Context, skillID string, configHash string) error

	// Prompt Repository - Sessions
	GetPromptSessions(ctx context.Context, promptID string) ([]tables.TablePromptSession, error)
	GetPromptSessionByID(ctx context.Context, id uint) (*tables.TablePromptSession, error)
	CreatePromptSession(ctx context.Context, session *tables.TablePromptSession) error
	UpdatePromptSession(ctx context.Context, session *tables.TablePromptSession) error
	RenamePromptSession(ctx context.Context, id uint, name string) error
	DeletePromptSession(ctx context.Context, id uint) error

	// DB returns the underlying database connection.
	DB() *gorm.DB

	// ScopedDB returns the underlying DB bound to ctx with any
	// QueryScope on ctx pre-applied. Use this in read paths that
	// should respect caller-driven row visibility; use DB().WithContext(ctx)
	// for writes and internal lookups that must bypass scoping.
	ScopedDB(ctx context.Context) *gorm.DB

	// RunMigration opens a throwaway *gorm.DB against the same
	// backing database, invokes fn with it, and closes the connection. Use
	// this for DDL (typically downstream-consumer migrations) that must not
	// leave cached prepared-statement plans on the runtime pool.
	//
	// After fn returns successfully, callers should invoke
	// RefreshConnectionPool if the migration altered tables the runtime pool
	// has already queried — otherwise SQLSTATE 0A000 can surface on reads
	// whose cached plans predate the DDL.
	//
	// For SQLite backends, this is a pass-through that runs fn on the
	// existing connection (no server-side plan cache, single-writer lock).
	RunMigration(ctx context.Context, fn func(context.Context, *gorm.DB) error) error

	// RefreshConnectionPool tears down the runtime pool and opens a fresh
	// one against the same configuration. In-flight queries on the old
	// pool complete before it closes; subsequent DB() calls return the new
	// pool, whose connections carry no cached plans. SQLite is a no-op.
	RefreshConnectionPool(ctx context.Context) error

	// Cleanup
	Close(ctx context.Context) error
}

// NewConfigStore creates a new config store based on the configuration
func NewConfigStore(ctx context.Context, config *Config, logger schemas.Logger) (ConfigStore, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if !config.Enabled {
		return nil, nil
	}
	logger.Info("connecting to %s database", config.Type)
	switch config.Type {
	case ConfigStoreTypeSQLite:
		if sqliteConfig, ok := config.Config.(*SQLiteConfig); ok {
			return newSqliteConfigStore(ctx, sqliteConfig, logger)
		}
		return nil, fmt.Errorf("invalid sqlite config: %T", config.Config)
	case ConfigStoreTypePostgres:
		if postgresConfig, ok := config.Config.(*PostgresConfig); ok {
			return newPostgresConfigStore(ctx, postgresConfig, logger)
		}
		return nil, fmt.Errorf("invalid postgres config: %T", config.Config)
	}
	return nil, fmt.Errorf("unsupported config store type: %s", config.Type)
}
