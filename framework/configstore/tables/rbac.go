package tables

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// RBAC operation values for the Loopback Gateway roles & permissions UI. These
// mirror the RbacOperation enum used by the dashboard (rbacContext.tsx). The
// wildcard RbacWildcard ("*") matches every operation (used by the seeded admin
// role) and is accepted for both Operation and Resource.
const (
	RbacOperationRead     = "Read"
	RbacOperationView     = "View"
	RbacOperationCreate   = "Create"
	RbacOperationUpdate   = "Update"
	RbacOperationDelete   = "Delete"
	RbacOperationDownload = "Download"

	// RbacWildcard matches any resource or operation.
	RbacWildcard = "*"
)

// validRbacOperations is the set of accepted operation values for a permission,
// in addition to the wildcard.
var validRbacOperations = map[string]bool{
	RbacOperationRead:     true,
	RbacOperationView:     true,
	RbacOperationCreate:   true,
	RbacOperationUpdate:   true,
	RbacOperationDelete:   true,
	RbacOperationDownload: true,
}

// IsValidRbacOperation reports whether op is a recognized operation or the
// wildcard.
func IsValidRbacOperation(op string) bool {
	return op == RbacWildcard || validRbacOperations[op]
}

// GrantMatches reports whether a (grantResource, grantOperation) grant covers a
// requested (resource, operation), honoring the "*" wildcard on either grant
// field. It is the single wildcard-matching implementation shared by RBAC role
// permissions (TablePermission) and admin API key scopes (TableAdminAPIKeyScope)
// so both use the exact same permission vocabulary and semantics.
func GrantMatches(grantResource, grantOperation, resource, operation string) bool {
	resourceOK := grantResource == RbacWildcard || strings.EqualFold(grantResource, resource)
	operationOK := grantOperation == RbacWildcard || strings.EqualFold(grantOperation, operation)
	return resourceOK && operationOK
}

// DefaultAdminRoleName is the name of the system role seeded by the RBAC
// migration. It carries a single wildcard permission (every resource, every
// operation) so an operator has a ready-made full-access role to assign. The
// local-admin (password) auth path always bypasses RBAC regardless, so seeding
// this role can never lock anyone out.
const DefaultAdminRoleName = "admin"

// DefaultEditorRoleName / DefaultViewerRoleName are convenience roles seeded
// by the secure-setup enforce action (NOT by migration — they appear only when
// the operator opts in). Editor grants Read/View/Download/Create/Update on
// every resource (no Delete); viewer grants Read/View/Download only. Both are
// created with IsSystem=false so operators can edit or delete them.
const (
	DefaultEditorRoleName = "editor"
	DefaultViewerRoleName = "viewer"
)

// TablePermission is a single (resource, operation) grant belonging to a role.
// It is a child of TableRole (governance_roles) and is cascade-deleted with its
// parent. The (role_id, resource, operation) tuple is unique so a role cannot
// carry the same grant twice. Resource/Operation accept the wildcard "*".
type TablePermission struct {
	ID     string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	RoleID string `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_rbac_permission_unique,priority:1" json:"role_id"`

	// Resource is the protected resource name (e.g. "Users", "GuardrailsConfig")
	// or the wildcard "*". Mirrors the RbacResource enum on the dashboard.
	Resource string `gorm:"type:varchar(255);not null;uniqueIndex:idx_rbac_permission_unique,priority:2" json:"resource"`
	// Operation is one of Read/View/Create/Update/Delete/Download, or "*".
	Operation string `gorm:"type:varchar(50);not null;uniqueIndex:idx_rbac_permission_unique,priority:3" json:"operation"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
}

// TableName sets the table name.
func (TablePermission) TableName() string { return "governance_permissions" }

// BeforeSave validates the resource and operation.
func (p *TablePermission) BeforeSave(tx *gorm.DB) error {
	p.Resource = strings.TrimSpace(p.Resource)
	p.Operation = strings.TrimSpace(p.Operation)
	if p.Resource == "" {
		return fmt.Errorf("permission resource cannot be empty")
	}
	if !IsValidRbacOperation(p.Operation) {
		return fmt.Errorf("invalid permission operation %q", p.Operation)
	}
	return nil
}

// Matches reports whether this permission grants the given (resource, operation)
// request, honoring wildcards on either field. Delegates to the shared
// GrantMatches helper.
func (p *TablePermission) Matches(resource, operation string) bool {
	return GrantMatches(p.Resource, p.Operation, resource, operation)
}

// TableRole is a named collection of permissions. Users are granted a role via a
// TableRoleAssignment. The seeded admin role has IsSystem=true so it cannot be
// deleted, guaranteeing a full-access role always exists. Follows the
// guardrail-config table conventions (string PK, indexed timestamps).
type TableRole struct {
	ID          string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name        string `gorm:"type:varchar(255);not null;uniqueIndex:idx_governance_roles_name" json:"name"`
	Description string `gorm:"type:text" json:"description"`

	// IsSystem marks a built-in role (the seeded admin role). System roles cannot
	// be deleted via the API so an operator always retains a full-access role.
	IsSystem bool `gorm:"not null;default:false" json:"is_system"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`

	// Permissions is the child grant list, hydrated on read and cascade-deleted
	// with the role.
	Permissions []TablePermission `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE" json:"permissions"`
}

// TableName sets the table name.
func (TableRole) TableName() string { return "governance_roles" }

// BeforeSave trims and validates the role name.
func (r *TableRole) BeforeSave(tx *gorm.DB) error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return fmt.Errorf("role name cannot be empty")
	}
	return nil
}

// TableRoleAssignment binds a role to a managed user (TableUser). The
// (role_id, user_id) tuple is unique so a user cannot hold the same role twice.
// Both FKs cascade-delete so removing a role or a user cleans up the assignment.
type TableRoleAssignment struct {
	ID     string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	RoleID string `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_rbac_assignment_unique,priority:1" json:"role_id"`
	UserID string `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_rbac_assignment_unique,priority:2" json:"user_id"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`

	// Relationships (read-side hydration). Role.Permissions is preloaded by the
	// store so the middleware can resolve a user's effective permissions in one
	// query path.
	Role *TableRole `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE" json:"role,omitempty"`
	User *TableUser `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"user,omitempty"`
}

// TableName sets the table name.
func (TableRoleAssignment) TableName() string { return "governance_role_assignments" }

// BeforeSave validates the FKs.
func (a *TableRoleAssignment) BeforeSave(tx *gorm.DB) error {
	a.RoleID = strings.TrimSpace(a.RoleID)
	a.UserID = strings.TrimSpace(a.UserID)
	if a.RoleID == "" {
		return fmt.Errorf("role assignment role_id cannot be empty")
	}
	if a.UserID == "" {
		return fmt.Errorf("role assignment user_id cannot be empty")
	}
	return nil
}
