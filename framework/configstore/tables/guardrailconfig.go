package tables

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Guardrail config scope values. Scope determines where a guardrail config
// applies. Mirrors the model-config scope model (see modelconfig.go) so the UI
// and governance entities share a single mental model.
const (
	GuardrailScopeGlobal     = "global"
	GuardrailScopeVirtualKey = "virtual_key"
	GuardrailScopeTeam       = "team"
	GuardrailScopeCustomer   = "customer"
)

// validGuardrailScopes is the set of accepted scope values for a guardrail
// config. Unlike model configs, the OSS build accepts all four scopes because
// the loopback-guard plugin stores them verbatim; live enforcement currently
// applies enabled global configs (see the HTTP handler's reload path).
var validGuardrailScopes = map[string]bool{
	GuardrailScopeGlobal:     true,
	GuardrailScopeVirtualKey: true,
	GuardrailScopeTeam:       true,
	GuardrailScopeCustomer:   true,
}

// IsValidGuardrailScope reports whether scope is a recognized guardrail scope.
func IsValidGuardrailScope(scope string) bool {
	return validGuardrailScopes[scope]
}

// GuardrailItem is a single, polymorphic guardrail entry inside a config. Type
// selects which guardrail runs (e.g. "contains", "regexMatch", "modelWhitelist")
// and Params carries that guardrail's type-specific configuration as raw JSON.
// Items are stored serialized in TableGuardrailConfig.GuardrailsJSON; they are
// not their own table. Params is intentionally schema-less here — the HTTP layer
// that builds live loopback-guard guardrails owns per-type validation.
type GuardrailItem struct {
	// ID identifies this item within its parent config (UI editing convenience).
	ID string `json:"id,omitempty"`
	// Type is the guardrail type identifier (see the loopback-guard plugin).
	Type string `json:"type"`
	// Enabled toggles this individual guardrail without removing it.
	Enabled bool `json:"enabled"`
	// Params holds the guardrail-specific parameters (e.g. words, min/max).
	Params map[string]any `json:"params,omitempty"`
}

// TableGuardrailConfig is a named, scoped collection of guardrails for the
// loopback-guard plugin. The guardrail list is stored as JSON (polymorphic per
// item) so new guardrail types need no schema migration. Scope/ScopeID follow
// the model-config pattern (modelconfig.go); the polymorphic owner FK pointers
// follow the budget pattern (budget.go) so a config can be hung off a VK, team,
// or customer for cascade deletes.
type TableGuardrailConfig struct {
	ID      string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name    string `gorm:"type:varchar(255);not null;index" json:"name"`
	Enabled bool   `gorm:"not null;default:true" json:"enabled"`

	// Scope determines where this config applies: "global" (default),
	// "virtual_key", "team" or "customer".
	Scope string `gorm:"type:varchar(50);not null;default:'global';index:idx_guardrail_scope,priority:1" json:"scope"`
	// ScopeID is the target of a non-global scope (e.g. the VK/team/customer ID).
	// NULL for global.
	ScopeID *string `gorm:"type:varchar(255);index:idx_guardrail_scope,priority:2" json:"scope_id,omitempty"`

	// GuardrailsJSON is the serialized []GuardrailItem. The runtime Guardrails
	// field is the source of truth on write (BeforeSave serializes it) and is
	// repopulated on read (AfterFind deserializes it).
	GuardrailsJSON string `gorm:"type:text" json:"-"`

	// Owner FKs: a config may be owned by at most one VK, team, or customer so a
	// cascade delete cleans it up. Mutually exclusive with each other; mirrors
	// TableBudget's polymorphic ownership.
	VirtualKeyID *string `gorm:"type:varchar(255);index" json:"virtual_key_id,omitempty"`
	TeamID       *string `gorm:"type:varchar(255);index" json:"team_id,omitempty"`
	CustomerID   *string `gorm:"type:varchar(255);index" json:"customer_id,omitempty"`

	// Config hash detects changes synced from a config.json file.
	ConfigHash string `gorm:"type:varchar(255);null" json:"config_hash"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`

	// Guardrails is the runtime, non-persisted parsed list. Populated from
	// GuardrailsJSON by AfterFind and serialized back by BeforeSave.
	Guardrails []GuardrailItem `gorm:"-" json:"guardrails"`
	// ScopeName is an API-only, non-persisted human-readable name for the scope
	// target (e.g. the VK's name) so the UI renders a label, not an opaque ID.
	ScopeName string `gorm:"-" json:"scope_name,omitempty"`
}

// TableName sets the table name.
func (TableGuardrailConfig) TableName() string { return "governance_guardrail_configs" }

// BeforeSave validates scope/ownership and serializes the runtime Guardrails
// list into GuardrailsJSON.
func (g *TableGuardrailConfig) BeforeSave(tx *gorm.DB) error {
	// Default and validate scope. Global is the implicit default.
	if strings.TrimSpace(g.Scope) == "" {
		g.Scope = GuardrailScopeGlobal
	}
	if !IsValidGuardrailScope(g.Scope) {
		return fmt.Errorf("invalid scope %q for guardrail config", g.Scope)
	}
	// Enforce scope_id rules: global must not carry one; non-global requires it.
	if g.Scope == GuardrailScopeGlobal {
		g.ScopeID = nil
	} else if g.ScopeID == nil || strings.TrimSpace(*g.ScopeID) == "" {
		return fmt.Errorf("scope_id is required when scope is %q", g.Scope)
	}

	// A config belongs to at most one owner type.
	owners := 0
	if g.VirtualKeyID != nil {
		owners++
	}
	if g.TeamID != nil {
		owners++
	}
	if g.CustomerID != nil {
		owners++
	}
	if owners > 1 {
		return fmt.Errorf("guardrail config cannot have more than one owner (virtual key/team/customer)")
	}

	if strings.TrimSpace(g.Name) == "" {
		return fmt.Errorf("guardrail config name cannot be empty")
	}

	// Serialize the runtime list. A nil list persists as an empty array so reads
	// are always well-formed JSON.
	if g.Guardrails == nil {
		g.GuardrailsJSON = "[]"
	} else {
		data, err := json.Marshal(g.Guardrails)
		if err != nil {
			return fmt.Errorf("failed to serialize guardrails: %w", err)
		}
		g.GuardrailsJSON = string(data)
	}
	return nil
}

// AfterFind deserializes GuardrailsJSON back into the runtime Guardrails list.
func (g *TableGuardrailConfig) AfterFind(tx *gorm.DB) error {
	if strings.TrimSpace(g.GuardrailsJSON) == "" {
		g.Guardrails = []GuardrailItem{}
		return nil
	}
	if err := json.Unmarshal([]byte(g.GuardrailsJSON), &g.Guardrails); err != nil {
		return fmt.Errorf("failed to deserialize guardrails: %w", err)
	}
	if g.Guardrails == nil {
		g.Guardrails = []GuardrailItem{}
	}
	return nil
}
