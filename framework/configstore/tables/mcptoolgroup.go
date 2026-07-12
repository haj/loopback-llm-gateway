package tables

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// MCP tool group scope values. Scope determines where a tool group applies and
// which principals can see it. Mirrors the guardrail-config scope model
// (guardrailconfig.go) so the UI and governance entities share one mental
// model. A tool group is primarily scoped to a virtual key or team, but global
// and customer scopes are accepted for parity with the other governance
// entities.
const (
	MCPToolGroupScopeGlobal     = "global"
	MCPToolGroupScopeVirtualKey = "virtual_key"
	MCPToolGroupScopeTeam       = "team"
	MCPToolGroupScopeCustomer   = "customer"
)

// validMCPToolGroupScopes is the set of accepted scope values for a tool group.
var validMCPToolGroupScopes = map[string]bool{
	MCPToolGroupScopeGlobal:     true,
	MCPToolGroupScopeVirtualKey: true,
	MCPToolGroupScopeTeam:       true,
	MCPToolGroupScopeCustomer:   true,
}

// IsValidMCPToolGroupScope reports whether scope is a recognized tool group scope.
func IsValidMCPToolGroupScope(scope string) bool {
	return validMCPToolGroupScopes[scope]
}

// MCPToolRef is a single reference to an MCP tool inside a tool group. ClientID
// identifies the MCP client (server) the tool belongs to and ToolName is the
// tool's name as discovered on that client. Refs are stored serialized in
// TableMCPToolGroup.ToolsJSON; they are not their own table.
type MCPToolRef struct {
	// ClientID is the MCP client (server) ID that owns the tool.
	ClientID string `json:"client_id"`
	// ToolName is the tool's name as exposed by that MCP client.
	ToolName string `json:"tool_name"`
}

// TableMCPToolGroup is a named, scoped collection of MCP tool references. A tool
// group lets an operator bundle a set of MCP tools and bind that bundle to a
// virtual key or team so the gateway's MCP visibility filtering can include or
// exclude the whole group at once. The tool list is stored as JSON so new tool
// references need no schema migration. Scope/ScopeID follow the guardrail-config
// pattern; the polymorphic owner FK pointers follow the budget pattern so a
// group can be hung off a VK, team, or customer for cascade deletes.
type TableMCPToolGroup struct {
	ID          string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name        string `gorm:"type:varchar(255);not null;index" json:"name"`
	Description string `gorm:"type:text" json:"description"`
	Enabled     bool   `gorm:"not null;default:true" json:"enabled"`

	// Scope determines where this group applies: "global" (default),
	// "virtual_key", "team" or "customer".
	Scope string `gorm:"type:varchar(50);not null;default:'global';index:idx_mcp_tool_group_scope,priority:1" json:"scope"`
	// ScopeID is the target of a non-global scope (e.g. the VK/team/customer ID).
	// NULL for global.
	ScopeID *string `gorm:"type:varchar(255);index:idx_mcp_tool_group_scope,priority:2" json:"scope_id,omitempty"`

	// ToolsJSON is the serialized []MCPToolRef. The runtime Tools field is the
	// source of truth on write (BeforeSave serializes it) and is repopulated on
	// read (AfterFind deserializes it).
	ToolsJSON string `gorm:"type:text" json:"-"`

	// Owner FKs: a group may be owned by at most one VK, team, or customer so a
	// cascade delete cleans it up. Mutually exclusive with each other; mirrors
	// TableBudget's polymorphic ownership.
	VirtualKeyID *string `gorm:"type:varchar(255);index" json:"virtual_key_id,omitempty"`
	TeamID       *string `gorm:"type:varchar(255);index" json:"team_id,omitempty"`
	CustomerID   *string `gorm:"type:varchar(255);index" json:"customer_id,omitempty"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`

	// Tools is the runtime, non-persisted parsed list. Populated from ToolsJSON
	// by AfterFind and serialized back by BeforeSave.
	Tools []MCPToolRef `gorm:"-" json:"tools"`
	// ScopeName is an API-only, non-persisted human-readable name for the scope
	// target (e.g. the VK's name) so the UI renders a label, not an opaque ID.
	ScopeName string `gorm:"-" json:"scope_name,omitempty"`
}

// TableName sets the table name.
func (TableMCPToolGroup) TableName() string { return "governance_mcp_tool_groups" }

// BeforeSave validates scope/ownership and serializes the runtime Tools list
// into ToolsJSON.
func (g *TableMCPToolGroup) BeforeSave(tx *gorm.DB) error {
	// Default and validate scope. Global is the implicit default.
	if strings.TrimSpace(g.Scope) == "" {
		g.Scope = MCPToolGroupScopeGlobal
	}
	if !IsValidMCPToolGroupScope(g.Scope) {
		return fmt.Errorf("invalid scope %q for mcp tool group", g.Scope)
	}
	// Enforce scope_id rules: global must not carry one; non-global requires it.
	if g.Scope == MCPToolGroupScopeGlobal {
		g.ScopeID = nil
	} else if g.ScopeID == nil || strings.TrimSpace(*g.ScopeID) == "" {
		return fmt.Errorf("scope_id is required when scope is %q", g.Scope)
	}

	// A group belongs to at most one owner type.
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
		return fmt.Errorf("mcp tool group cannot have more than one owner (virtual key/team/customer)")
	}

	if strings.TrimSpace(g.Name) == "" {
		return fmt.Errorf("mcp tool group name cannot be empty")
	}

	// Serialize the runtime list. A nil list persists as an empty array so reads
	// are always well-formed JSON.
	if g.Tools == nil {
		g.ToolsJSON = "[]"
	} else {
		data, err := json.Marshal(g.Tools)
		if err != nil {
			return fmt.Errorf("failed to serialize mcp tool refs: %w", err)
		}
		g.ToolsJSON = string(data)
	}
	return nil
}

// AfterFind deserializes ToolsJSON back into the runtime Tools list.
func (g *TableMCPToolGroup) AfterFind(tx *gorm.DB) error {
	if strings.TrimSpace(g.ToolsJSON) == "" {
		g.Tools = []MCPToolRef{}
		return nil
	}
	if err := json.Unmarshal([]byte(g.ToolsJSON), &g.Tools); err != nil {
		return fmt.Errorf("failed to deserialize mcp tool refs: %w", err)
	}
	if g.Tools == nil {
		g.Tools = []MCPToolRef{}
	}
	return nil
}
