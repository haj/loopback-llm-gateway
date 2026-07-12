// This file contains the Microsoft Entra ID (Azure AD) OIDC provider: JWT
// validation against the tenant's v2.0 JWKS and a Directory over Microsoft
// Graph (client-credentials token, @odata.nextLink paging).
// Field names bind config.schema.json's entra_config exactly.
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

// entraClouds maps the cloud environment to its login and Graph hosts.
var entraClouds = map[string]struct{ login, graph string }{
	"commercial": {login: "login.microsoftonline.com", graph: "graph.microsoft.com"},
	"gcc-high":   {login: "login.microsoftonline.us", graph: "graph.microsoft.us"},
	"dod":        {login: "login.microsoftonline.us", graph: "dod-graph.microsoft.us"},
}

// EntraConfig is the Entra ID provider configuration (schema entra_config).
type EntraConfig struct {
	TenantID     string `json:"tenantId"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Cloud        string `json:"cloud,omitempty"` // commercial|gcc-high|dod
	Audience     string `json:"audience,omitempty"`
	AppIDURI     string `json:"appIdUri,omitempty"`

	UserIDField  string `json:"userIdField,omitempty"`
	TeamIDsField string `json:"teamIdsField,omitempty"`
	RolesField   string `json:"rolesField,omitempty"`

	AttributeRoleMappings         []AttributeRoleMapping         `json:"attributeRoleMappings,omitempty"`
	AttributeTeamMappings         []AttributeTeamMapping         `json:"attributeTeamMappings,omitempty"`
	AttributeBusinessUnitMappings []AttributeBusinessUnitMapping `json:"attributeBusinessUnitMappings,omitempty"`
}

// Entra parses and returns the typed Entra configuration with defaults applied
// and secrets env-resolved.
func (c *SCIMConfig) Entra() (*EntraConfig, error) {
	ec := &EntraConfig{}
	if err := parseProviderBlock(c, ProviderEntra, ec); err != nil {
		return nil, err
	}
	ec.TenantID = strings.TrimSpace(ec.TenantID)
	ec.ClientID = strings.TrimSpace(ec.ClientID)
	ec.ClientSecret = resolveEnv(ec.ClientSecret)
	ec.Cloud = strings.ToLower(defaultField(ec.Cloud, "commercial"))
	ec.UserIDField = defaultField(ec.UserIDField, "oid")
	ec.TeamIDsField = defaultField(ec.TeamIDsField, "groups")
	ec.RolesField = defaultField(ec.RolesField, "roles")
	return ec, nil
}

// Validate ensures the required Entra fields are present. tenantId "common"
// is rejected in this slice: a multi-tenant app cannot pin a single issuer,
// and weakening issuer validation is not an acceptable trade.
func (ec *EntraConfig) Validate() error {
	var missing []string
	if ec.TenantID == "" {
		missing = append(missing, "tenantId")
	}
	if ec.ClientID == "" {
		missing = append(missing, "clientId")
	}
	if len(missing) > 0 {
		return fmt.Errorf("sso: entra config missing required fields: %s", strings.Join(missing, ", "))
	}
	if strings.EqualFold(ec.TenantID, "common") {
		return fmt.Errorf("sso: entra tenantId \"common\" is not supported (a multi-tenant issuer cannot be pinned); configure a specific tenant ID")
	}
	if _, ok := entraClouds[ec.Cloud]; !ok {
		return fmt.Errorf("sso: entra cloud must be commercial, gcc-high or dod, got %q", ec.Cloud)
	}
	return nil
}

func (ec *EntraConfig) hosts() struct{ login, graph string } { return entraClouds[ec.Cloud] }

// ---- OIDCProvider ----

func (ec *EntraConfig) Name() string { return ProviderEntra }

// IssuerURL is the v2.0 issuer for the tenant.
func (ec *EntraConfig) IssuerURL() string {
	return fmt.Sprintf("https://%s/%s/v2.0", ec.hosts().login, ec.TenantID)
}

// JWKSURL is the tenant's v2.0 discovery key-set endpoint.
func (ec *EntraConfig) JWKSURL() string {
	return fmt.Sprintf("https://%s/%s/discovery/v2.0/keys", ec.hosts().login, ec.TenantID)
}

// ExpectedAudience prefers the explicit audience, then the App ID URI (v1.0
// tokens), then the client ID (the v2.0 default).
func (ec *EntraConfig) ExpectedAudience() string {
	if a := strings.TrimSpace(ec.Audience); a != "" {
		return a
	}
	if a := strings.TrimSpace(ec.AppIDURI); a != "" {
		return a
	}
	return ec.ClientID
}

func (ec *EntraConfig) ClaimFields() ClaimFields {
	return ClaimFields{UserID: ec.UserIDField, TeamIDs: ec.TeamIDsField, Roles: ec.RolesField}
}

func (ec *EntraConfig) Mappings() AttributeMappings {
	return AttributeMappings{
		Roles:         ec.AttributeRoleMappings,
		Teams:         ec.AttributeTeamMappings,
		BusinessUnits: ec.AttributeBusinessUnitMappings,
	}
}

// ---- Directory (Microsoft Graph) ----

// Directory returns a Microsoft Graph directory client.
func (ec *EntraConfig) Directory(httpClient *http.Client) (Directory, error) {
	if ec.ClientSecret == "" {
		return nil, fmt.Errorf("sso: entra directory sync requires clientSecret")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	hosts := ec.hosts()
	return &entraDirectory{
		tokenURL:   fmt.Sprintf("https://%s/%s/oauth2/v2.0/token", hosts.login, ec.TenantID),
		graphBase:  "https://" + hosts.graph,
		clientID:   ec.ClientID,
		secret:     ec.ClientSecret,
		httpClient: httpClient,
	}, nil
}

type entraDirectory struct {
	tokenURL   string
	graphBase  string
	clientID   string
	secret     string
	httpClient *http.Client
}

// graphToken obtains an app-only Graph access token via client credentials.
func (d *entraDirectory) graphToken(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", d.clientID)
	form.Set("client_secret", d.secret)
	form.Set("scope", strings.TrimSuffix(d.graphBase, "/")+"/.default")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sso: entra token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sso: entra token request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("sso: entra token response missing access_token")
	}
	return tok.AccessToken, nil
}

// graphUser / graphGroup are the Graph representations we consume.
type graphUser struct {
	ID                string `json:"id"`
	UserPrincipalName string `json:"userPrincipalName"`
	Mail              string `json:"mail"`
	DisplayName       string `json:"displayName"`
	AccountEnabled    *bool  `json:"accountEnabled"`
}

type graphGroup struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// listGraph pages through a Graph collection, decoding each page's `value`
// into out via the visit callback and following @odata.nextLink.
func (d *entraDirectory) listGraph(ctx context.Context, token, firstURL string, visit func(raw json.RawMessage) error) error {
	pageURL := firstURL
	for pageURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		resp, err := d.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("sso: graph GET failed: %w", err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("sso: graph GET %s returned %d: %s", pageURL, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var page struct {
			Value    []json.RawMessage `json:"value"`
			NextLink string            `json:"@odata.nextLink"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("sso: failed to parse graph page: %w", err)
		}
		for _, raw := range page.Value {
			if err := visit(raw); err != nil {
				return err
			}
		}
		pageURL = page.NextLink
	}
	return nil
}

func (d *entraDirectory) ListUsers(ctx context.Context) ([]DirectoryUser, error) {
	token, err := d.graphToken(ctx)
	if err != nil {
		return nil, err
	}
	var out []DirectoryUser
	firstURL := d.graphBase + "/v1.0/users?$select=id,userPrincipalName,mail,displayName,accountEnabled&$top=200"
	err = d.listGraph(ctx, token, firstURL, func(rawMsg json.RawMessage) error {
		var u graphUser
		if err := json.Unmarshal(rawMsg, &u); err != nil {
			return err
		}
		var raw map[string]any
		_ = json.Unmarshal(rawMsg, &raw)
		// accountEnabled may be omitted when the app lacks the permission to
		// read it; treat missing as active (matching Graph's default view).
		active := u.AccountEnabled == nil || *u.AccountEnabled
		out = append(out, DirectoryUser{
			ExternalID:  u.ID,
			UserName:    firstNonEmpty(u.UserPrincipalName, u.Mail),
			Email:       strings.ToLower(strings.TrimSpace(firstNonEmpty(u.Mail, u.UserPrincipalName))),
			DisplayName: firstNonEmpty(u.DisplayName, u.UserPrincipalName),
			Active:      active,
			Raw:         raw,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (d *entraDirectory) ListGroups(ctx context.Context) ([]DirectoryGroup, error) {
	token, err := d.graphToken(ctx)
	if err != nil {
		return nil, err
	}
	var out []DirectoryGroup
	firstURL := d.graphBase + "/v1.0/groups?$select=id,displayName&$top=200"
	err = d.listGraph(ctx, token, firstURL, func(rawMsg json.RawMessage) error {
		var g graphGroup
		if err := json.Unmarshal(rawMsg, &g); err != nil {
			return err
		}
		var raw map[string]any
		_ = json.Unmarshal(rawMsg, &raw)
		out = append(out, DirectoryGroup{ExternalID: g.ID, DisplayName: g.DisplayName, Raw: raw})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
