package tables

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// PII rule scope values. Scope determines where a PII redaction rule applies.
// Mirrors the guardrail-config scope model (see guardrailconfig.go) so the UI
// and governance entities share a single mental model.
const (
	PIIRuleScopeGlobal     = "global"
	PIIRuleScopeVirtualKey = "virtual_key"
	PIIRuleScopeTeam       = "team"
	PIIRuleScopeCustomer   = "customer"
)

// PII rule type discriminator values. Type selects which redaction mechanism a
// rule drives in the loopback-guard plugin:
//   - "regex"    -> an in-process Redactor RedactRule (redact.go)
//   - "presidio" -> a PresidioClient text transformer (presidio.go)
const (
	PIIRuleTypeRegex    = "regex"
	PIIRuleTypePresidio = "presidio"
)

// validPIIRuleScopes is the set of accepted scope values, identical to the
// guardrail-config scopes so a rule can be hung off a VK, team, or customer for
// cascade deletes.
var validPIIRuleScopes = map[string]bool{
	PIIRuleScopeGlobal:     true,
	PIIRuleScopeVirtualKey: true,
	PIIRuleScopeTeam:       true,
	PIIRuleScopeCustomer:   true,
}

// validPIIRuleTypes is the set of accepted rule type discriminators.
var validPIIRuleTypes = map[string]bool{
	PIIRuleTypeRegex:    true,
	PIIRuleTypePresidio: true,
}

// IsValidPIIRuleScope reports whether scope is a recognized PII rule scope.
func IsValidPIIRuleScope(scope string) bool { return validPIIRuleScopes[scope] }

// IsValidPIIRuleType reports whether t is a recognized PII rule type.
func IsValidPIIRuleType(t string) bool { return validPIIRuleTypes[t] }

// TablePIIRule is a single, scoped PII redaction rule for the loopback-guard
// plugin. Unlike TableGuardrailConfig (which stores a JSON list of polymorphic
// items), a PII rule is a flat, columnar row with a Type discriminator: regex
// rules carry RegexPattern/RegexReplacement, presidio rules carry the analyzer
// URL, entity type, and score threshold. This columnar shape matches the
// pii-config spec's per-rule API contract.
//
// Scope/ScopeID and the polymorphic owner FK pointers follow the guardrail
// config pattern (guardrailconfig.go), which in turn follows the budget pattern
// (budget.go), so a rule can be cascade-deleted with its owning VK/team/customer.
type TablePIIRule struct {
	ID          string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name        string `gorm:"type:varchar(255);not null;index" json:"name"`
	Description string `gorm:"type:text" json:"description,omitempty"`

	// Type is the rule discriminator: "regex" or "presidio".
	Type string `gorm:"type:varchar(50);not null;index" json:"type"`
	// Enabled toggles enforcement without deleting the rule.
	Enabled bool `gorm:"not null;default:true" json:"enabled"`

	// Scope determines where this rule applies: "global" (default),
	// "virtual_key", "team" or "customer".
	Scope string `gorm:"type:varchar(50);not null;default:'global';index:idx_pii_rule_scope,priority:1" json:"scope"`
	// ScopeID is the target of a non-global scope (e.g. the VK/team/customer ID).
	// NULL for global.
	ScopeID *string `gorm:"type:varchar(255);index:idx_pii_rule_scope,priority:2" json:"scope_id,omitempty"`

	// --- regex discriminator fields (Type == "regex") ---
	// RegexPattern is a Go regexp the Redactor matches against message text.
	RegexPattern string `gorm:"type:text" json:"regex_pattern,omitempty"`
	// RegexReplacement is the literal string each match is replaced with.
	RegexReplacement string `gorm:"type:varchar(255)" json:"regex_replacement,omitempty"`

	// --- presidio discriminator fields (Type == "presidio") ---
	// PresidioBaseURL is the analyzer endpoint (e.g. http://localhost:5002).
	PresidioBaseURL string `gorm:"type:varchar(512)" json:"presidio_base_url,omitempty"`
	// PresidioEntityType is the PII entity this rule targets (e.g. PERSON, EMAIL).
	// Informational/grouping metadata: the connector masks every entity the
	// analyzer reports at or above the threshold.
	PresidioEntityType string `gorm:"type:varchar(100)" json:"presidio_entity_type,omitempty"`
	// PresidioScoreThreshold is the minimum analyzer confidence (0..1) to mask.
	PresidioScoreThreshold float64 `gorm:"type:real;default:0.5" json:"presidio_score_threshold,omitempty"`

	// RuleOrder is the execution order; lower runs first. Column is "rule_order"
	// because ORDER is a reserved SQL keyword. JSON stays "order" for the API.
	RuleOrder int `gorm:"column:rule_order;not null;default:0;index" json:"order"`

	// Owner FKs: a rule may be owned by at most one VK, team, or customer so a
	// cascade delete cleans it up. Mutually exclusive; mirrors TableGuardrailConfig.
	VirtualKeyID *string `gorm:"type:varchar(255);index" json:"virtual_key_id,omitempty"`
	TeamID       *string `gorm:"type:varchar(255);index" json:"team_id,omitempty"`
	CustomerID   *string `gorm:"type:varchar(255);index" json:"customer_id,omitempty"`

	// Config hash detects changes synced from a config.json file.
	ConfigHash string `gorm:"type:varchar(255);null" json:"config_hash"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`

	// ScopeName is an API-only, non-persisted human-readable name for the scope
	// target (e.g. the VK's name) so the UI renders a label, not an opaque ID.
	ScopeName string `gorm:"-" json:"scope_name,omitempty"`
}

// TableName sets the table name.
func (TablePIIRule) TableName() string { return "governance_pii_rules" }

// BeforeSave validates scope/type/ownership and per-type required fields, and
// applies sane defaults (replacement, threshold).
func (r *TablePIIRule) BeforeSave(tx *gorm.DB) error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("pii rule name cannot be empty")
	}

	// Default and validate scope. Global is the implicit default.
	if strings.TrimSpace(r.Scope) == "" {
		r.Scope = PIIRuleScopeGlobal
	}
	if !IsValidPIIRuleScope(r.Scope) {
		return fmt.Errorf("invalid scope %q for pii rule", r.Scope)
	}
	if r.Scope == PIIRuleScopeGlobal {
		r.ScopeID = nil
	} else if r.ScopeID == nil || strings.TrimSpace(*r.ScopeID) == "" {
		return fmt.Errorf("scope_id is required when scope is %q", r.Scope)
	}

	// Validate the type discriminator and its required fields.
	if !IsValidPIIRuleType(r.Type) {
		return fmt.Errorf("invalid type %q for pii rule (want %q or %q)", r.Type, PIIRuleTypeRegex, PIIRuleTypePresidio)
	}
	switch r.Type {
	case PIIRuleTypeRegex:
		if strings.TrimSpace(r.RegexPattern) == "" {
			return fmt.Errorf("regex_pattern is required when type is %q", PIIRuleTypeRegex)
		}
		if strings.TrimSpace(r.RegexReplacement) == "" {
			r.RegexReplacement = "[REDACTED]"
		}
		// Presidio fields are meaningless for a regex rule.
		r.PresidioBaseURL, r.PresidioEntityType, r.PresidioScoreThreshold = "", "", 0
	case PIIRuleTypePresidio:
		if strings.TrimSpace(r.PresidioBaseURL) == "" {
			return fmt.Errorf("presidio_base_url is required when type is %q", PIIRuleTypePresidio)
		}
		if r.PresidioScoreThreshold <= 0 || r.PresidioScoreThreshold > 1 {
			r.PresidioScoreThreshold = 0.5
		}
		// Regex fields are meaningless for a presidio rule.
		r.RegexPattern, r.RegexReplacement = "", ""
	}

	// A rule belongs to at most one owner type.
	owners := 0
	if r.VirtualKeyID != nil {
		owners++
	}
	if r.TeamID != nil {
		owners++
	}
	if r.CustomerID != nil {
		owners++
	}
	if owners > 1 {
		return fmt.Errorf("pii rule cannot have more than one owner (virtual key/team/customer)")
	}
	return nil
}
