package tables

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SCIM provider identifiers. Keycloak, Okta, and Entra are all implemented
// (see framework/sso: keycloak.go, okta.go, entra.go).
const (
	SCIMProviderKeycloak = "keycloak"
	SCIMProviderOkta     = "okta"
	SCIMProviderEntra    = "entra"
)

// TableSCIMUser is a user provisioned from an external identity provider
// (Keycloak in this slice) through the SCIM sync engine. It is the durable
// mirror of the IdP's view of a user: it records the IdP-side identifiers and
// attributes, and links to the managed Wave-2 user (TableUser) that the gateway
// actually authorizes against. The link lets a valid IdP JWT be mapped onto an
// existing managed user without duplicating budget / rate-limit / VK machinery.
//
// It mirrors the JSON-column-with-runtime-slice pattern used by TableUser
// (Groups <-> GroupsJSON) so new IdP group shapes need no schema migration.
type TableSCIMUser struct {
	ID string `gorm:"primaryKey;type:varchar(255)" json:"id"`

	// Provider identifies the source IdP (e.g. "keycloak"). Together with
	// ExternalID it uniquely identifies a synced principal.
	Provider string `gorm:"type:varchar(50);not null;index:idx_scim_user_provider_external,unique,priority:1" json:"provider"`
	// ExternalID is the IdP-side stable identifier (the JWT "sub" / Keycloak
	// user id). Unique per provider.
	ExternalID string `gorm:"type:varchar(255);not null;index:idx_scim_user_provider_external,unique,priority:2" json:"external_id"`

	UserName    string `gorm:"type:varchar(255);index" json:"user_name"`
	Email       string `gorm:"type:varchar(255);index" json:"email"`
	DisplayName string `gorm:"type:varchar(255)" json:"display_name"`

	// Active reflects the IdP enablement state. A deactivation sync flips this to
	// false (it never hard-deletes) so the audit trail and the managed-user link
	// survive a re-enable. No GORM default is set: a false value must persist as
	// false on insert (a default:true tag would make GORM treat the zero value
	// as "unset" and apply the default, silently re-activating deactivated users).
	Active bool `gorm:"not null;index" json:"active"`

	// GroupsJSON is the serialized []string of IdP group names this user belongs
	// to. Runtime source of truth is Groups (BeforeSave serializes, AfterFind
	// deserializes), mirroring TableUser.VirtualKeyIDs.
	GroupsJSON string `gorm:"type:text" json:"-"`

	// RawAttributesJSON stores the raw provider claim/attribute payload for
	// debugging and future mapping rules without a migration.
	RawAttributesJSON string `gorm:"type:text" json:"-"`

	// ManagedUserID links this synced identity to a managed TableUser. NULL until
	// the sync engine (or first JWT login) resolves/creates the managed user.
	ManagedUserID *string `gorm:"type:varchar(255);index" json:"managed_user_id,omitempty"`

	// Mapped* record the attribute-mapping outcome applied on the last
	// provisioning pass: the role assigned, the resolved
	// team IDs linked, and the business unit set on the managed user. NULL /
	// empty when no rule matched. MappedTeamIDsJSON follows the GroupsJSON
	// pattern (runtime MappedTeamIDs is the source of truth).
	MappedRoleID         *string `gorm:"type:varchar(255)" json:"mapped_role_id,omitempty"`
	MappedTeamIDsJSON    string  `gorm:"type:text" json:"-"`
	MappedBusinessUnitID *string `gorm:"type:varchar(255)" json:"mapped_business_unit_id,omitempty"`

	LastSyncedAt time.Time `gorm:"index" json:"last_synced_at"`
	CreatedAt    time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt    time.Time `gorm:"index;not null" json:"updated_at"`

	// Groups is the runtime, non-persisted parsed group-name list.
	Groups []string `gorm:"-" json:"groups"`
	// MappedTeamIDs is the runtime, non-persisted parsed mapped-team list.
	MappedTeamIDs []string `gorm:"-" json:"mapped_team_ids"`
}

// TableName sets the table name.
func (TableSCIMUser) TableName() string { return "scim_users" }

// BeforeSave normalizes fields and serializes the runtime Groups list.
func (u *TableSCIMUser) BeforeSave(tx *gorm.DB) error {
	u.Provider = strings.ToLower(strings.TrimSpace(u.Provider))
	u.ExternalID = strings.TrimSpace(u.ExternalID)
	u.Email = strings.ToLower(strings.TrimSpace(u.Email))
	if u.Provider == "" {
		return fmt.Errorf("scim user provider cannot be empty")
	}
	if u.ExternalID == "" {
		return fmt.Errorf("scim user external_id cannot be empty")
	}
	if u.Groups == nil {
		u.GroupsJSON = "[]"
	} else {
		data, err := json.Marshal(u.Groups)
		if err != nil {
			return fmt.Errorf("failed to serialize scim user groups: %w", err)
		}
		u.GroupsJSON = string(data)
	}
	if u.MappedTeamIDs == nil {
		u.MappedTeamIDsJSON = "[]"
	} else {
		data, err := json.Marshal(u.MappedTeamIDs)
		if err != nil {
			return fmt.Errorf("failed to serialize scim user mapped team ids: %w", err)
		}
		u.MappedTeamIDsJSON = string(data)
	}
	return nil
}

// AfterFind deserializes GroupsJSON and MappedTeamIDsJSON back into their
// runtime lists.
func (u *TableSCIMUser) AfterFind(tx *gorm.DB) error {
	u.Groups = []string{}
	if strings.TrimSpace(u.GroupsJSON) != "" {
		if err := json.Unmarshal([]byte(u.GroupsJSON), &u.Groups); err != nil {
			return fmt.Errorf("failed to deserialize scim user groups: %w", err)
		}
		if u.Groups == nil {
			u.Groups = []string{}
		}
	}
	u.MappedTeamIDs = []string{}
	if strings.TrimSpace(u.MappedTeamIDsJSON) != "" {
		if err := json.Unmarshal([]byte(u.MappedTeamIDsJSON), &u.MappedTeamIDs); err != nil {
			return fmt.Errorf("failed to deserialize scim user mapped team ids: %w", err)
		}
		if u.MappedTeamIDs == nil {
			u.MappedTeamIDs = []string{}
		}
	}
	return nil
}

// TableSCIMGroup is a group provisioned from an external identity provider via
// the SCIM sync engine. Membership is stored as a serialized []string of member
// external user IDs (Members <-> MembersJSON) following the same pattern as
// TableSCIMUser.Groups.
type TableSCIMGroup struct {
	ID string `gorm:"primaryKey;type:varchar(255)" json:"id"`

	Provider   string `gorm:"type:varchar(50);not null;index:idx_scim_group_provider_external,unique,priority:1" json:"provider"`
	ExternalID string `gorm:"type:varchar(255);not null;index:idx_scim_group_provider_external,unique,priority:2" json:"external_id"`

	DisplayName string `gorm:"type:varchar(255);index" json:"display_name"`

	// MembersJSON is the serialized []string of member external user IDs.
	MembersJSON string `gorm:"type:text" json:"-"`
	// RawAttributesJSON stores the raw provider payload.
	RawAttributesJSON string `gorm:"type:text" json:"-"`

	LastSyncedAt time.Time `gorm:"index" json:"last_synced_at"`
	CreatedAt    time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt    time.Time `gorm:"index;not null" json:"updated_at"`

	// Members is the runtime, non-persisted member external-id list.
	Members []string `gorm:"-" json:"members"`
}

// TableName sets the table name.
func (TableSCIMGroup) TableName() string { return "scim_groups" }

// BeforeSave normalizes fields and serializes the runtime Members list.
func (g *TableSCIMGroup) BeforeSave(tx *gorm.DB) error {
	g.Provider = strings.ToLower(strings.TrimSpace(g.Provider))
	g.ExternalID = strings.TrimSpace(g.ExternalID)
	if g.Provider == "" {
		return fmt.Errorf("scim group provider cannot be empty")
	}
	if g.ExternalID == "" {
		return fmt.Errorf("scim group external_id cannot be empty")
	}
	if g.Members == nil {
		g.MembersJSON = "[]"
	} else {
		data, err := json.Marshal(g.Members)
		if err != nil {
			return fmt.Errorf("failed to serialize scim group members: %w", err)
		}
		g.MembersJSON = string(data)
	}
	return nil
}

// AfterFind deserializes MembersJSON back into the runtime Members list.
func (g *TableSCIMGroup) AfterFind(tx *gorm.DB) error {
	if strings.TrimSpace(g.MembersJSON) == "" {
		g.Members = []string{}
		return nil
	}
	if err := json.Unmarshal([]byte(g.MembersJSON), &g.Members); err != nil {
		return fmt.Errorf("failed to deserialize scim group members: %w", err)
	}
	if g.Members == nil {
		g.Members = []string{}
	}
	return nil
}
