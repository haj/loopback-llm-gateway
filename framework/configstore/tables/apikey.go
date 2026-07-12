package tables

import (
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

// Admin API key status values. A revoked key is soft-disabled: the row (and its
// scopes) remain for audit/lineage, but the key middleware rejects it.
const (
	AdminAPIKeyStatusActive  = "active"
	AdminAPIKeyStatusRevoked = "revoked"
)

// AdminAPIKeyPrefix is the plaintext prefix of every admin-plane API key. The
// key middleware only engages on bearer tokens carrying this prefix, so all
// other credentials (sessions, basic auth, IdP JWTs, governance virtual keys)
// pass through it byte-for-byte untouched.
const AdminAPIKeyPrefix = "lbk_"

// TableAdminAPIKey is a long-lived admin-plane API key for the dashboard /
// management API. It is entirely DISTINCT from governance virtual keys
// (TableVirtualKey, x-bf-vk), which authenticate inference traffic only.
//
// The plaintext secret is NEVER persisted: Value is a transient (gorm:"-")
// carrier used exactly once at create/rotate time; BeforeSave derives the
// SHA-256 ValueHash from it (mirroring SessionsTable.TokenHash /
// TableVirtualKey.ValueHash) and only the hash plus a short display prefix are
// stored. Lookups go through the unique value_hash index.
type TableAdminAPIKey struct {
	ID   string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name string `gorm:"type:varchar(255);not null" json:"name"`

	// KeyPrefix is the first characters of the plaintext key ("lbk_ab12…"),
	// retained purely so the UI can help operators identify a key. It is not a
	// credential.
	KeyPrefix string `gorm:"type:varchar(32)" json:"key_prefix"`

	// Value transiently holds the plaintext key between generation and save so
	// BeforeSave can hash it. It is never written to the database or to JSON.
	Value string `gorm:"-" json:"-"`

	// ValueHash is the hex SHA-256 of the plaintext key; the only secret
	// material at rest.
	ValueHash string `gorm:"type:varchar(64);index:idx_admin_api_key_value_hash,unique" json:"-"`

	// Status is "active" or "revoked".
	Status string `gorm:"type:varchar(20);not null;default:'active'" json:"status"`

	ExpiresAt  *time.Time `gorm:"index" json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`

	// RotatedFromID records rotation lineage: the ID of the key this one
	// replaced (nil for keys minted directly).
	RotatedFromID *string `gorm:"type:varchar(255)" json:"rotated_from_id,omitempty"`

	// CreatedBy is the acting principal captured at create time (audit
	// attribution only).
	CreatedBy string `gorm:"type:varchar(255)" json:"created_by"`

	// Scopes is the child grant list, hydrated on read and cascade-deleted with
	// the key. Scopes reuse the RBAC permission vocabulary verbatim.
	Scopes []TableAdminAPIKeyScope `gorm:"foreignKey:APIKeyID;constraint:OnDelete:CASCADE" json:"scopes"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`
}

// TableName sets the table name.
func (TableAdminAPIKey) TableName() string { return "governance_admin_api_keys" }

// BeforeSave validates the row and computes the value hash from the transient
// plaintext (SessionsTable/TableVirtualKey pattern: hash before anything else so
// the indexed lookup always reflects the plaintext).
func (k *TableAdminAPIKey) BeforeSave(tx *gorm.DB) error {
	k.Name = strings.TrimSpace(k.Name)
	if k.Name == "" {
		return fmt.Errorf("api key name cannot be empty")
	}
	if k.Status == "" {
		k.Status = AdminAPIKeyStatusActive
	}
	if k.Status != AdminAPIKeyStatusActive && k.Status != AdminAPIKeyStatusRevoked {
		return fmt.Errorf("invalid api key status %q", k.Status)
	}
	if k.Value != "" {
		k.ValueHash = encrypt.HashSHA256(k.Value)
	}
	if k.ValueHash == "" {
		return fmt.Errorf("api key value hash cannot be empty")
	}
	return nil
}

// IsExpired reports whether the key has an expiry in the past relative to now.
func (k *TableAdminAPIKey) IsExpired(now time.Time) bool {
	return k.ExpiresAt != nil && now.After(*k.ExpiresAt)
}

// TableAdminAPIKeyScope is a single (resource, operation) grant belonging to an
// admin API key. It is a child of TableAdminAPIKey and cascade-deleted with its
// parent. Resource/Operation use the exact RBAC permission vocabulary
// (TablePermission): resource names from the dashboard's RbacResource enum plus
// the "*" wildcard, and operations validated by IsValidRbacOperation.
type TableAdminAPIKeyScope struct {
	ID       string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	APIKeyID string `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_admin_api_key_scope_unique,priority:1" json:"api_key_id"`

	// Resource is the protected resource name (e.g. "Users", "GuardrailsConfig")
	// or the wildcard "*".
	Resource string `gorm:"type:varchar(255);not null;uniqueIndex:idx_admin_api_key_scope_unique,priority:2" json:"resource"`
	// Operation is one of Read/View/Create/Update/Delete/Download, or "*".
	Operation string `gorm:"type:varchar(50);not null;uniqueIndex:idx_admin_api_key_scope_unique,priority:3" json:"operation"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
}

// TableName sets the table name.
func (TableAdminAPIKeyScope) TableName() string { return "governance_admin_api_key_scopes" }

// BeforeSave validates the resource and operation (same rules as
// TablePermission).
func (s *TableAdminAPIKeyScope) BeforeSave(tx *gorm.DB) error {
	s.Resource = strings.TrimSpace(s.Resource)
	s.Operation = strings.TrimSpace(s.Operation)
	if s.Resource == "" {
		return fmt.Errorf("api key scope resource cannot be empty")
	}
	if !IsValidRbacOperation(s.Operation) {
		return fmt.Errorf("invalid api key scope operation %q", s.Operation)
	}
	return nil
}

// Matches reports whether this scope grants the given (resource, operation)
// request, honoring wildcards on either field. Shares GrantMatches with
// TablePermission.Matches so key scopes and role permissions can never diverge.
func (s *TableAdminAPIKeyScope) Matches(resource, operation string) bool {
	return GrantMatches(s.Resource, s.Operation, resource, operation)
}
