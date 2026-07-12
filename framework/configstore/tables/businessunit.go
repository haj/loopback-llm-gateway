package tables

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TableBusinessUnit is a flat (non-nested) organizational grouping for users in
// the Loopback Gateway user & org management UI. Unlike teams/customers it does
// not nest: a business unit has no parent and owns no child business units. It
// reuses the shared governance budget / rate-limit tables by FK pointer so an
// org-wide spending limit can be attached without duplicating that machinery.
type TableBusinessUnit struct {
	ID          string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name        string `gorm:"type:varchar(255);not null;uniqueIndex:idx_governance_business_units_name" json:"name"`
	Description string `gorm:"type:text" json:"description"`

	// Budget / rate-limit reuse: a business unit references at most one budget
	// and one rate-limit row in the shared governance tables (governance_budgets
	// / governance_rate_limits). Referenced, not owned, so the handler deletes
	// the referenced rows explicitly on business-unit delete.
	BudgetID    *string `gorm:"type:varchar(255);index" json:"budget_id,omitempty"`
	RateLimitID *string `gorm:"type:varchar(255);index" json:"rate_limit_id,omitempty"`

	// Config hash detects changes synced from a config.json file.
	ConfigHash string `gorm:"type:varchar(255);null" json:"config_hash"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`

	// Relationships (read-side hydration).
	Budget    *TableBudget    `gorm:"foreignKey:BudgetID" json:"budget,omitempty"`
	RateLimit *TableRateLimit `gorm:"foreignKey:RateLimitID" json:"rate_limit,omitempty"`

	// UserCount is computed (not a DB column) via a correlated subquery in the
	// query layer; hence excluded from migration.
	UserCount int64 `gorm:"->;-:migration" json:"user_count"`
}

// TableName sets the table name.
func (TableBusinessUnit) TableName() string { return "governance_business_units" }

// BeforeSave trims and validates the name.
func (b *TableBusinessUnit) BeforeSave(tx *gorm.DB) error {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return fmt.Errorf("business unit name cannot be empty")
	}
	return nil
}
