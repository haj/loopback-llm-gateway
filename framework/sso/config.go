// Package sso implements SSO / SCIM for Loopback Gateway: a strict IdP JWT
// validator (validated against the provider JWKS), a directory pull-sync
// engine, an inbound SCIM 2.0 filter/PATCH toolkit, and an attribute→role/
// team/business-unit mapping engine. Keycloak, Okta, and Entra ID are
// implemented behind the OIDCProvider abstraction (provider.go).
//
// SECURITY MODEL: everything in this package is default-OFF. A nil/disabled
// SCIMConfig produces a nil Validator, and the HTTP auth middleware skips the
// JWT branch entirely, so existing password / session auth is never affected.
// SSO is strictly an ADDITIONAL authentication path that only activates once an
// operator configures and enables a provider.
package sso

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Provider identifiers mirrored from the config schema (scim_config.provider).
const (
	ProviderKeycloak = "keycloak"
	ProviderOkta     = "okta"
	ProviderEntra    = "entra"
)

// ErrProviderNotImplemented is kept for callers that still reference it; all
// schema-recognized providers (keycloak, okta, entra) are now implemented, so
// it is only returned for typed accessors called with the wrong provider.
var ErrProviderNotImplemented = fmt.Errorf("sso: provider not implemented")

// AttributeRoleMapping maps a JWT claim value to a gateway role (first match
// wins). Mirrors scim_attribute_role_mappings in config.schema.json.
type AttributeRoleMapping struct {
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
	Role      string `json:"role"`
}

// AttributeTeamMapping maps a JWT claim value to a gateway team slug (all
// matches apply; value "*" is pass-through). Mirrors
// scim_attribute_team_mappings.
type AttributeTeamMapping struct {
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
	Team      string `json:"team,omitempty"`
}

// AttributeBusinessUnitMapping maps a JWT claim value to a gateway business unit
// slug. Mirrors scim_attribute_business_unit_mappings.
type AttributeBusinessUnitMapping struct {
	Attribute    string `json:"attribute"`
	Value        string `json:"value"`
	BusinessUnit string `json:"business_unit,omitempty"`
}

// KeycloakConfig is the Keycloak provider configuration. Field names match the
// JSON keys in config.schema.json's keycloak_config so a single config.json
// section drives both schema validation and this Go struct.
type KeycloakConfig struct {
	ServerURL    string `json:"serverUrl"`
	Realm        string `json:"realm"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Audience     string `json:"audience,omitempty"`

	UserIDField  string `json:"userIdField,omitempty"`
	TeamIDsField string `json:"teamIdsField,omitempty"`
	RolesField   string `json:"rolesField,omitempty"`

	AttributeRoleMappings         []AttributeRoleMapping         `json:"attributeRoleMappings,omitempty"`
	AttributeTeamMappings         []AttributeTeamMapping         `json:"attributeTeamMappings,omitempty"`
	AttributeBusinessUnitMappings []AttributeBusinessUnitMapping `json:"attributeBusinessUnitMappings,omitempty"`
}

// SCIMProvisioningConfig gates the inbound SCIM 2.0 endpoint (/scim/v2).
// Default-OFF: while disabled or without a bearer token, the endpoint returns
// 404 and the request path is unchanged.
type SCIMProvisioningConfig struct {
	Enabled bool `json:"enabled"`
	// BearerToken authenticates inbound SCIM requests (constant-time
	// compared). Supports the env.VAR convention.
	BearerToken string `json:"bearerToken"`
}

// ResolvedBearerToken returns the env-resolved provisioning token.
func (p *SCIMProvisioningConfig) ResolvedBearerToken() string {
	if p == nil {
		return ""
	}
	return resolveEnv(p.BearerToken)
}

// InboundEnabled reports whether the inbound SCIM endpoint is usable: enabled
// AND a non-empty token (an enabled endpoint without a token would be open).
func (p *SCIMProvisioningConfig) InboundEnabled() bool {
	return p != nil && p.Enabled && p.ResolvedBearerToken() != ""
}

// SCIMConfig is the top-level scim_config section. Config holds the raw
// provider-specific block; use Keycloak()/Okta()/Entra() (or the NewProvider
// factory) to obtain the typed, defaulted configuration.
type SCIMConfig struct {
	Enabled      bool                    `json:"enabled"`
	Provider     string                  `json:"provider"`
	Config       json.RawMessage         `json:"config"`
	Provisioning *SCIMProvisioningConfig `json:"provisioning,omitempty"`
}

// resolveEnv resolves an "env.VAR" reference to its environment value. A plain
// string (no "env." prefix) is returned unchanged. This matches the convention
// used elsewhere for secrets (e.g. clientSecret: "env.KEYCLOAK_CLIENT_SECRET").
func resolveEnv(v string) string {
	v = strings.TrimSpace(v)
	if after, ok := strings.CutPrefix(v, "env."); ok {
		return strings.TrimSpace(os.Getenv(strings.TrimSpace(after)))
	}
	return v
}

// defaultField returns def when field is empty (after trimming).
func defaultField(field, def string) string {
	if strings.TrimSpace(field) == "" {
		return def
	}
	return strings.TrimSpace(field)
}

// Keycloak parses and returns the typed Keycloak configuration with defaults
// applied and the client secret env-resolved. It returns an error if the
// provider is not keycloak or the block is malformed.
func (c *SCIMConfig) Keycloak() (*KeycloakConfig, error) {
	if c == nil {
		return nil, fmt.Errorf("sso: nil scim config")
	}
	if strings.ToLower(strings.TrimSpace(c.Provider)) != ProviderKeycloak {
		return nil, ErrProviderNotImplemented
	}
	kc := &KeycloakConfig{}
	if len(c.Config) > 0 {
		if err := json.Unmarshal(c.Config, kc); err != nil {
			return nil, fmt.Errorf("sso: failed to parse keycloak config: %w", err)
		}
	}
	kc.ServerURL = strings.TrimRight(strings.TrimSpace(kc.ServerURL), "/")
	kc.Realm = strings.TrimSpace(kc.Realm)
	kc.ClientID = strings.TrimSpace(kc.ClientID)
	kc.ClientSecret = resolveEnv(kc.ClientSecret)
	kc.UserIDField = defaultField(kc.UserIDField, "sub")
	kc.TeamIDsField = defaultField(kc.TeamIDsField, "groups")
	kc.RolesField = defaultField(kc.RolesField, "realm_access.roles")
	return kc, nil
}

// Validate checks that the configuration is internally consistent. A disabled
// config always validates (default-OFF must never block startup). An enabled
// config must name a supported provider with its required fields present.
func (c *SCIMConfig) Validate() error {
	if c == nil || !c.Enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(c.Provider)) {
	case ProviderKeycloak:
		kc, err := c.Keycloak()
		if err != nil {
			return err
		}
		return kc.Validate()
	case ProviderOkta:
		oc, err := c.Okta()
		if err != nil {
			return err
		}
		return oc.Validate()
	case ProviderEntra:
		ec, err := c.Entra()
		if err != nil {
			return err
		}
		return ec.Validate()
	default:
		return fmt.Errorf("sso: unknown provider %q", c.Provider)
	}
}

// Validate ensures the required Keycloak fields are present.
func (kc *KeycloakConfig) Validate() error {
	var missing []string
	if kc.ServerURL == "" {
		missing = append(missing, "serverUrl")
	}
	if kc.Realm == "" {
		missing = append(missing, "realm")
	}
	if kc.ClientID == "" {
		missing = append(missing, "clientId")
	}
	if kc.ClientSecret == "" {
		missing = append(missing, "clientSecret")
	}
	if len(missing) > 0 {
		return fmt.Errorf("sso: keycloak config missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// IssuerURL is the OIDC issuer for the realm (serverUrl/realms/{realm}). It is
// the expected "iss" claim and the base for JWKS / token / admin endpoints.
func (kc *KeycloakConfig) IssuerURL() string {
	return fmt.Sprintf("%s/realms/%s", kc.ServerURL, kc.Realm)
}

// JWKSURL is the realm's JSON Web Key Set endpoint used to verify token
// signatures.
func (kc *KeycloakConfig) JWKSURL() string {
	return kc.IssuerURL() + "/protocol/openid-connect/certs"
}

// TokenURL is the realm's OIDC token endpoint (used for the admin
// client-credentials grant by the sync engine).
func (kc *KeycloakConfig) TokenURL() string {
	return kc.IssuerURL() + "/protocol/openid-connect/token"
}

// AdminUsersURL is the Keycloak Admin REST API users endpoint for the realm.
func (kc *KeycloakConfig) AdminUsersURL() string {
	return fmt.Sprintf("%s/admin/realms/%s/users", kc.ServerURL, kc.Realm)
}

// AdminGroupsURL is the Keycloak Admin REST API groups endpoint for the realm.
func (kc *KeycloakConfig) AdminGroupsURL() string {
	return fmt.Sprintf("%s/admin/realms/%s/groups", kc.ServerURL, kc.Realm)
}

// ExpectedAudience returns the audience to enforce on incoming tokens. When
// unset in config it defaults to the clientId. An empty result means "do not
// enforce audience" (signature + issuer + expiry are still always enforced).
func (kc *KeycloakConfig) ExpectedAudience() string {
	if strings.TrimSpace(kc.Audience) != "" {
		return strings.TrimSpace(kc.Audience)
	}
	return kc.ClientID
}

// ---- OIDCProvider (see provider.go) ----

// Name identifies the provider.
func (kc *KeycloakConfig) Name() string { return ProviderKeycloak }

// ClaimFields names the claims projected into Identity.
func (kc *KeycloakConfig) ClaimFields() ClaimFields {
	return ClaimFields{UserID: kc.UserIDField, TeamIDs: kc.TeamIDsField, Roles: kc.RolesField}
}

// Mappings returns the attribute→role/team/BU rules.
func (kc *KeycloakConfig) Mappings() AttributeMappings {
	return AttributeMappings{
		Roles:         kc.AttributeRoleMappings,
		Teams:         kc.AttributeTeamMappings,
		BusinessUnits: kc.AttributeBusinessUnitMappings,
	}
}
