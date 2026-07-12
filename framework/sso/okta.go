// This file contains the Okta OIDC provider: JWT
// validation against the Okta authorization server's JWKS and a Directory over
// the Okta Admin API (SSWS token auth, Link rel="next" pagination). Field
// names bind config.schema.json's okta_config exactly.
package sso

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OktaConfig is the Okta provider configuration (schema okta_config).
type OktaConfig struct {
	Issuer     string `json:"issuerUrl"`
	AuthServerType string `json:"authServerType,omitempty"` // org|custom, auto-detected when empty
	ClientID       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	APIToken       string `json:"apiToken"`
	Audience       string `json:"audience,omitempty"`

	UserIDField  string `json:"userIdField,omitempty"`
	TeamIDsField string `json:"teamIdsField,omitempty"`
	RolesField   string `json:"rolesField,omitempty"`

	AttributeRoleMappings         []AttributeRoleMapping         `json:"attributeRoleMappings,omitempty"`
	AttributeTeamMappings         []AttributeTeamMapping         `json:"attributeTeamMappings,omitempty"`
	AttributeBusinessUnitMappings []AttributeBusinessUnitMapping `json:"attributeBusinessUnitMappings,omitempty"`
}

// Okta parses and returns the typed Okta configuration with defaults applied
// and secrets env-resolved.
func (c *SCIMConfig) Okta() (*OktaConfig, error) {
	oc := &OktaConfig{}
	if err := parseProviderBlock(c, ProviderOkta, oc); err != nil {
		return nil, err
	}
	oc.Issuer = strings.TrimRight(strings.TrimSpace(oc.Issuer), "/")
	oc.ClientID = strings.TrimSpace(oc.ClientID)
	oc.ClientSecret = resolveEnv(oc.ClientSecret)
	oc.APIToken = resolveEnv(oc.APIToken)
	oc.AuthServerType = strings.ToLower(strings.TrimSpace(oc.AuthServerType))
	oc.UserIDField = defaultField(oc.UserIDField, "sub")
	oc.TeamIDsField = defaultField(oc.TeamIDsField, "groups")
	oc.RolesField = defaultField(oc.RolesField, "roles")
	return oc, nil
}

// Validate ensures the required Okta fields are present. Note: apiToken is
// required by the schema but only needed for directory sync; token validation
// alone works without it, so only issuer/clientId are hard-required here.
func (oc *OktaConfig) Validate() error {
	var missing []string
	if oc.Issuer == "" {
		missing = append(missing, "issuerUrl")
	}
	if oc.ClientID == "" {
		missing = append(missing, "clientId")
	}
	if len(missing) > 0 {
		return fmt.Errorf("sso: okta config missing required fields: %s", strings.Join(missing, ", "))
	}
	if oc.AuthServerType != "" && oc.AuthServerType != "org" && oc.AuthServerType != "custom" {
		return fmt.Errorf("sso: okta authServerType must be \"org\" or \"custom\", got %q", oc.AuthServerType)
	}
	return nil
}

// serverType resolves org-vs-custom: explicit config wins; otherwise a
// "/oauth2/" path segment in the issuer means a custom authorization server
// (e.g. https://dev.okta.com/oauth2/default), a bare domain means the Org
// Authorization Server.
func (oc *OktaConfig) serverType() string {
	if oc.AuthServerType != "" {
		return oc.AuthServerType
	}
	if strings.Contains(oc.Issuer, "/oauth2/") {
		return "custom"
	}
	return "org"
}

// ---- OIDCProvider ----

func (oc *OktaConfig) Name() string      { return ProviderOkta }
func (oc *OktaConfig) IssuerURL() string { return oc.Issuer }

// JWKSURL derives the key-set endpoint: {issuer}/v1/keys for custom
// authorization servers, {issuer}/oauth2/v1/keys for the org server.
func (oc *OktaConfig) JWKSURL() string {
	if oc.serverType() == "custom" {
		return oc.Issuer + "/v1/keys"
	}
	return oc.Issuer + "/oauth2/v1/keys"
}

// ExpectedAudience enforces the configured audience when set; empty means
// signature/issuer/expiry only (Okta org-server tokens default aud to the org
// URL, custom servers to api://default — too deployment-specific to guess).
func (oc *OktaConfig) ExpectedAudience() string { return strings.TrimSpace(oc.Audience) }

func (oc *OktaConfig) ClaimFields() ClaimFields {
	return ClaimFields{UserID: oc.UserIDField, TeamIDs: oc.TeamIDsField, Roles: oc.RolesField}
}

func (oc *OktaConfig) Mappings() AttributeMappings {
	return AttributeMappings{
		Roles:         oc.AttributeRoleMappings,
		Teams:         oc.AttributeTeamMappings,
		BusinessUnits: oc.AttributeBusinessUnitMappings,
	}
}

// ---- Directory (Okta Admin API) ----

// adminBaseURL is the Okta org origin ({scheme}://{host}) — the Admin API
// lives at the org root regardless of which authorization server issues
// tokens.
func (oc *OktaConfig) adminBaseURL() (string, error) {
	u, err := url.Parse(oc.Issuer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("sso: okta issuerUrl %q is not an absolute URL", oc.Issuer)
	}
	return u.Scheme + "://" + u.Host, nil
}

// Directory returns an Okta Admin API directory client.
func (oc *OktaConfig) Directory(httpClient *http.Client) (Directory, error) {
	if oc.APIToken == "" {
		return nil, fmt.Errorf("sso: okta directory sync requires apiToken")
	}
	base, err := oc.adminBaseURL()
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &oktaDirectory{baseURL: base, apiToken: oc.APIToken, httpClient: httpClient}, nil
}

type oktaDirectory struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
}

// oktaUser is the subset of the Okta user representation we consume.
type oktaUser struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Profile struct {
		Login     string `json:"login"`
		Email     string `json:"email"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	} `json:"profile"`
}

type oktaGroup struct {
	ID      string `json:"id"`
	Profile struct {
		Name string `json:"name"`
	} `json:"profile"`
}

// getPaged GETs one page and returns the body plus the Link rel="next" URL
// (empty when this was the last page).
func (d *oktaDirectory) getPaged(ctx context.Context, pageURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "SSWS "+d.apiToken)
	req.Header.Set("Accept", "application/json")
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("sso: okta admin GET failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("sso: okta admin GET %s returned %d: %s", pageURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, oktaNextLink(resp.Header), nil
}

// oktaNextLink extracts the rel="next" URL from Link headers.
func oktaNextLink(h http.Header) string {
	for _, link := range h.Values("Link") {
		for _, part := range strings.Split(link, ",") {
			part = strings.TrimSpace(part)
			if !strings.Contains(part, `rel="next"`) {
				continue
			}
			start := strings.IndexByte(part, '<')
			end := strings.IndexByte(part, '>')
			if start >= 0 && end > start {
				return part[start+1 : end]
			}
		}
	}
	return ""
}

func (d *oktaDirectory) ListUsers(ctx context.Context) ([]DirectoryUser, error) {
	var out []DirectoryUser
	pageURL := d.baseURL + "/api/v1/users?limit=200"
	for pageURL != "" {
		body, next, err := d.getPaged(ctx, pageURL)
		if err != nil {
			return nil, err
		}
		var users []oktaUser
		if err := json.Unmarshal(body, &users); err != nil {
			return nil, fmt.Errorf("sso: failed to parse okta users: %w", err)
		}
		for _, u := range users {
			var raw map[string]any
			if b, err := json.Marshal(u); err == nil {
				_ = json.Unmarshal(b, &raw)
			}
			display := strings.TrimSpace(strings.TrimSpace(u.Profile.FirstName) + " " + strings.TrimSpace(u.Profile.LastName))
			out = append(out, DirectoryUser{
				ExternalID:  u.ID,
				UserName:    firstNonEmpty(u.Profile.Login, u.Profile.Email),
				Email:       strings.ToLower(strings.TrimSpace(u.Profile.Email)),
				DisplayName: firstNonEmpty(display, u.Profile.Login, u.Profile.Email),
				// Okta statuses: ACTIVE is the only fully-active state.
				Active: strings.EqualFold(u.Status, "ACTIVE"),
				Raw:    raw,
			})
		}
		pageURL = next
	}
	return out, nil
}

func (d *oktaDirectory) ListGroups(ctx context.Context) ([]DirectoryGroup, error) {
	var out []DirectoryGroup
	pageURL := d.baseURL + "/api/v1/groups?limit=200"
	for pageURL != "" {
		body, next, err := d.getPaged(ctx, pageURL)
		if err != nil {
			return nil, err
		}
		var groups []oktaGroup
		if err := json.Unmarshal(body, &groups); err != nil {
			return nil, fmt.Errorf("sso: failed to parse okta groups: %w", err)
		}
		for _, g := range groups {
			var raw map[string]any
			if b, err := json.Marshal(g); err == nil {
				_ = json.Unmarshal(b, &raw)
			}
			out = append(out, DirectoryGroup{ExternalID: g.ID, DisplayName: g.Profile.Name, Raw: raw})
		}
		pageURL = next
	}
	return out, nil
}
