package tables

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// User status values for the Loopback Gateway user & org management UI.
const (
	UserStatusActive   = "active"
	UserStatusInactive = "inactive"
)

// validUserStatuses is the set of accepted status values for a user.
var validUserStatuses = map[string]bool{
	UserStatusActive:   true,
	UserStatusInactive: true,
}

// IsValidUserStatus reports whether status is a recognized user status.
func IsValidUserStatus(status string) bool { return validUserStatuses[status] }

// TableUser is a managed user in the Loopback Gateway user & org management UI.
// It is flat: a user belongs to at most one business unit (no nesting) and may
// be attached to one or more virtual keys. It reuses the shared governance
// budget / rate-limit tables by FK pointer (mirroring the customer pattern) so
// per-user spending limits do not duplicate that machinery. The model-config
// "user" scope (ModelConfigScopeUser) addresses the same identity, so per-user
// model overrides can be hung off a user's ID without a separate concept.
type TableUser struct {
	ID     string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name   string `gorm:"type:varchar(255);not null;index" json:"name"`
	Email  string `gorm:"type:varchar(255);not null;uniqueIndex:idx_governance_users_email" json:"email"`
	Status string `gorm:"type:varchar(50);not null;default:'active';index" json:"status"`

	// BusinessUnitID is a flat (non-nested) association to a business unit. NULL
	// when the user is not assigned to one.
	BusinessUnitID *string `gorm:"type:varchar(255);index" json:"business_unit_id,omitempty"`

	// Budget / rate-limit reuse: a user references at most one budget and one
	// rate-limit row in the shared governance tables. Referenced, not owned, so
	// the handler deletes the referenced rows explicitly on user delete.
	BudgetID    *string `gorm:"type:varchar(255);index" json:"budget_id,omitempty"`
	RateLimitID *string `gorm:"type:varchar(255);index" json:"rate_limit_id,omitempty"`

	// VirtualKeyIDsJSON is the serialized []string of virtual key IDs this user
	// is attached to. The runtime VirtualKeyIDs slice is the source of truth on
	// write (BeforeSave serializes it) and is repopulated on read (AfterFind).
	VirtualKeyIDsJSON string `gorm:"type:text" json:"-"`

	// Config hash detects changes synced from a config.json file.
	ConfigHash string `gorm:"type:varchar(255);null" json:"config_hash"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`

	// Relationships (read-side hydration).
	Budget       *TableBudget       `gorm:"foreignKey:BudgetID" json:"budget,omitempty"`
	RateLimit    *TableRateLimit    `gorm:"foreignKey:RateLimitID" json:"rate_limit,omitempty"`
	BusinessUnit *TableBusinessUnit `gorm:"foreignKey:BusinessUnitID" json:"business_unit,omitempty"`

	// VirtualKeyIDs is the runtime, non-persisted attached virtual-key list.
	// Populated from VirtualKeyIDsJSON by AfterFind and serialized back by
	// BeforeSave.
	VirtualKeyIDs []string `gorm:"-" json:"virtual_key_ids"`
}

// TableName sets the table name.
func (TableUser) TableName() string { return "governance_users" }

// BeforeSave normalizes/validates fields and serializes the runtime
// VirtualKeyIDs list into VirtualKeyIDsJSON.
func (u *TableUser) BeforeSave(tx *gorm.DB) error {
	u.Name = strings.TrimSpace(u.Name)
	u.Email = strings.ToLower(strings.TrimSpace(u.Email))
	if u.Name == "" {
		return fmt.Errorf("user name cannot be empty")
	}
	if u.Email == "" {
		return fmt.Errorf("user email cannot be empty")
	}
	if strings.TrimSpace(u.Status) == "" {
		u.Status = UserStatusActive
	}
	if !IsValidUserStatus(u.Status) {
		return fmt.Errorf("invalid user status %q", u.Status)
	}

	// Serialize the runtime list. A nil list persists as an empty array so reads
	// are always well-formed JSON.
	if u.VirtualKeyIDs == nil {
		u.VirtualKeyIDsJSON = "[]"
	} else {
		data, err := json.Marshal(u.VirtualKeyIDs)
		if err != nil {
			return fmt.Errorf("failed to serialize virtual key ids: %w", err)
		}
		u.VirtualKeyIDsJSON = string(data)
	}
	return nil
}

// AfterFind deserializes VirtualKeyIDsJSON back into the runtime VirtualKeyIDs.
func (u *TableUser) AfterFind(tx *gorm.DB) error {
	if strings.TrimSpace(u.VirtualKeyIDsJSON) == "" {
		u.VirtualKeyIDs = []string{}
		return nil
	}
	if err := json.Unmarshal([]byte(u.VirtualKeyIDsJSON), &u.VirtualKeyIDs); err != nil {
		return fmt.Errorf("failed to deserialize virtual key ids: %w", err)
	}
	if u.VirtualKeyIDs == nil {
		u.VirtualKeyIDs = []string{}
	}
	return nil
}
