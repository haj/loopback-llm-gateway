package tables

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// JWTAuthClaimMapping is one claim→virtual-key mapping rule persisted inside
// TableJWTAuthConfig. Mirrors framework/sso.JWTClaimVKMapping.
type JWTAuthClaimMapping struct {
	// Claim is the dot-path into the verified claim set.
	Claim string `json:"claim"`
	// Value is the exact value to match ("*" = any present value).
	Value string `json:"value"`
	// VirtualKeyID is the governance virtual key attributed on match.
	VirtualKeyID string `json:"virtual_key_id"`
}

// TableJWTAuthConfig is one trusted external JWT issuer for the data-plane
// JWT→virtual-key auth feature. ADDITIVE and DEFAULT-OFF:
// with zero enabled rows the JWT middleware snapshot is nil and the request
// path is byte-for-byte unchanged.
//
// The row references virtual keys by ID only — never their values — so this
// table carries no secrets. Follows the column conventions of
// TableCircuitBreakerConfig (ConfigHash + CreatedAt/UpdatedAt indexing).
type TableJWTAuthConfig struct {
	ID   string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name string `gorm:"type:varchar(255)" json:"name"`
	// Enabled gates this issuer. A disabled row is persisted but removed from
	// the live middleware snapshot.
	Enabled bool `gorm:"not null;default:false" json:"enabled"`
	// Issuer is the exact expected `iss` claim value. One config per issuer.
	Issuer string `gorm:"type:varchar(512);not null;uniqueIndex" json:"issuer"`
	// JWKSURL is the issuer's key-set endpoint.
	JWKSURL string `gorm:"type:varchar(1024);not null" json:"jwks_url"`
	// Audience, when set, is enforced against the `aud` claim.
	Audience string `gorm:"type:varchar(512)" json:"audience"`
	// RejectInvalid returns 401 for failed verification instead of the default
	// fall-through.
	RejectInvalid bool `gorm:"not null;default:false" json:"reject_invalid"`

	// ClaimMappingsJSON is the serialized []JWTAuthClaimMapping. The runtime
	// ClaimMappings field is the source of truth on write (BeforeSave
	// serializes it) and repopulated on read (AfterFind deserializes it).
	ClaimMappingsJSON string `gorm:"type:text" json:"-"`
	// DefaultVirtualKeyID is the fallback attribution when no rule matches
	// (empty = none).
	DefaultVirtualKeyID string `gorm:"type:varchar(255)" json:"default_virtual_key_id"`

	// ConfigHash detects changes synced from a config.json file.
	ConfigHash string `gorm:"type:varchar(255);null" json:"config_hash"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`

	// ClaimMappings is the runtime, non-persisted parsed rule list.
	ClaimMappings []JWTAuthClaimMapping `gorm:"-" json:"claim_mappings"`
}

// TableName sets the table name.
func (TableJWTAuthConfig) TableName() string { return "governance_jwt_auth_configs" }

// BeforeSave validates the issuer endpoints and mapping rules, and serializes
// the rule list.
func (c *TableJWTAuthConfig) BeforeSave(tx *gorm.DB) error {
	c.Issuer = strings.TrimSpace(c.Issuer)
	c.JWKSURL = strings.TrimSpace(c.JWKSURL)
	if c.Issuer == "" {
		return fmt.Errorf("jwt auth config issuer cannot be empty")
	}
	if c.JWKSURL == "" {
		return fmt.Errorf("jwt auth config jwks_url cannot be empty")
	}
	for i, m := range c.ClaimMappings {
		if strings.TrimSpace(m.Claim) == "" {
			return fmt.Errorf("jwt auth config claim mapping %d must name a claim", i)
		}
		if strings.TrimSpace(m.VirtualKeyID) == "" {
			return fmt.Errorf("jwt auth config claim mapping %d must name a virtual_key_id", i)
		}
	}
	if len(c.ClaimMappings) == 0 {
		c.ClaimMappingsJSON = ""
	} else {
		raw, err := json.Marshal(c.ClaimMappings)
		if err != nil {
			return fmt.Errorf("failed to serialize jwt auth claim mappings: %w", err)
		}
		c.ClaimMappingsJSON = string(raw)
	}
	return nil
}

// AfterFind deserializes the mapping rules.
func (c *TableJWTAuthConfig) AfterFind(tx *gorm.DB) error {
	if c.ClaimMappingsJSON != "" {
		if err := json.Unmarshal([]byte(c.ClaimMappingsJSON), &c.ClaimMappings); err != nil {
			return fmt.Errorf("failed to parse jwt auth claim mappings: %w", err)
		}
	}
	return nil
}
