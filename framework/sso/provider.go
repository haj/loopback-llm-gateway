// This file contains the OIDC provider abstraction. The
// Keycloak-only first slice hardcoded *KeycloakConfig into the validator and
// sync engine; the OIDCProvider interface factors out exactly what those two
// consumers need — issuer/JWKS/audience for token validation, claim-field
// names for Identity projection, mapping rules, and an optional Directory for
// pull sync — so Okta and Entra drop in beside Keycloak.
package sso

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ClaimFields names the JWT claims projected into an Identity.
type ClaimFields struct {
	UserID  string
	TeamIDs string
	Roles   string
}

// OIDCProvider is one configured identity provider, sufficient for token
// validation and identity projection. Providers that also support directory
// pull sync additionally implement DirectoryProvider.
type OIDCProvider interface {
	// Name is the provider identifier (keycloak|okta|entra).
	Name() string
	// IssuerURL is the exact expected `iss` claim.
	IssuerURL() string
	// JWKSURL is the signing key-set endpoint.
	JWKSURL() string
	// ExpectedAudience is enforced when non-empty.
	ExpectedAudience() string
	// ClaimFields names the claims projected into Identity.
	ClaimFields() ClaimFields
	// Mappings returns the attribute→role/team/BU rules.
	Mappings() AttributeMappings
}

// DirectoryUser is one IdP directory entry, provider-neutral.
type DirectoryUser struct {
	ExternalID  string
	UserName    string
	Email       string
	DisplayName string
	Active      bool
	// Raw is the provider's original representation (persisted on the SCIM
	// row for debugging/attribute mapping).
	Raw map[string]any
}

// DirectoryGroup is one IdP group entry.
type DirectoryGroup struct {
	ExternalID  string
	DisplayName string
	Raw         map[string]any
}

// Directory lists users and groups from the IdP's admin API for pull sync.
type Directory interface {
	ListUsers(ctx context.Context) ([]DirectoryUser, error)
	ListGroups(ctx context.Context) ([]DirectoryGroup, error)
}

// DirectoryProvider is the optional pull-sync capability of a provider.
type DirectoryProvider interface {
	// Directory returns a directory client. httpClient may be nil.
	Directory(httpClient *http.Client) (Directory, error)
}

// NewProvider parses the SCIMConfig into the typed provider named by
// scim.Provider. Returns (nil, nil) for a nil/disabled config (default-OFF).
func NewProvider(scim *SCIMConfig) (OIDCProvider, error) {
	if scim == nil || !scim.Enabled {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(scim.Provider)) {
	case ProviderKeycloak:
		kc, err := scim.Keycloak()
		if err != nil {
			return nil, err
		}
		if err := kc.Validate(); err != nil {
			return nil, err
		}
		return kc, nil
	case ProviderOkta:
		oc, err := scim.Okta()
		if err != nil {
			return nil, err
		}
		if err := oc.Validate(); err != nil {
			return nil, err
		}
		return oc, nil
	case ProviderEntra:
		ec, err := scim.Entra()
		if err != nil {
			return nil, err
		}
		if err := ec.Validate(); err != nil {
			return nil, err
		}
		return ec, nil
	default:
		return nil, fmt.Errorf("sso: unknown provider %q", scim.Provider)
	}
}

// parseProviderBlock unmarshals the raw provider config block.
func parseProviderBlock(c *SCIMConfig, wantProvider string, out any) error {
	if c == nil {
		return fmt.Errorf("sso: nil scim config")
	}
	if strings.ToLower(strings.TrimSpace(c.Provider)) != wantProvider {
		return fmt.Errorf("sso: config provider is %q, not %q", c.Provider, wantProvider)
	}
	if len(c.Config) > 0 {
		if err := json.Unmarshal(c.Config, out); err != nil {
			return fmt.Errorf("sso: failed to parse %s config: %w", wantProvider, err)
		}
	}
	return nil
}
