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

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
)

// SyncResult summarizes a sync engine run.
type SyncResult struct {
	UsersSynced      int      `json:"users_synced"`
	UsersDeactivated int      `json:"users_deactivated"`
	GroupsSynced     int      `json:"groups_synced"`
	Errors           []string `json:"errors,omitempty"`
}

// SyncEngine pulls users and groups from the configured IdP's directory and
// reconciles them into the configstore SCIM tables, linking each synced user
// to a managed Wave-2 user (TableUser). Provider-generic: any OIDCProvider
// implementing DirectoryProvider (Keycloak, Okta, Entra) plugs in.
type SyncEngine struct {
	provider  OIDCProvider
	directory Directory
	store     configstore.ConfigStore
}

// NewSyncEngine builds a directory sync engine for the configured provider.
// Returns (nil, nil) when the config is disabled (default-OFF) and an error
// when the provider does not support directory listing.
func NewSyncEngine(scim *SCIMConfig, store configstore.ConfigStore, httpClient *http.Client) (*SyncEngine, error) {
	p, err := NewProvider(scim)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	if store == nil {
		return nil, fmt.Errorf("sso: sync engine requires a config store")
	}
	dp, ok := p.(DirectoryProvider)
	if !ok {
		return nil, fmt.Errorf("sso: provider %s does not support directory sync", p.Name())
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	directory, err := dp.Directory(httpClient)
	if err != nil {
		return nil, err
	}
	return &SyncEngine{provider: p, directory: directory, store: store}, nil
}

// Sync runs a full reconcile of users then groups and returns a summary. It is
// best-effort: per-item failures are accumulated in SyncResult.Errors rather
// than aborting the whole run.
func (e *SyncEngine) Sync(ctx context.Context) (*SyncResult, error) {
	if e == nil {
		return nil, fmt.Errorf("sso: sync engine not configured")
	}
	res := &SyncResult{}
	mappings := e.provider.Mappings()

	users, err := e.directory.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range users {
		u := users[i]
		identity := &Identity{
			Provider:    e.provider.Name(),
			Subject:     u.ExternalID,
			Email:       u.Email,
			UserName:    u.UserName,
			DisplayName: u.DisplayName,
			Raw:         u.Raw,
		}
		if _, err := ProvisionFromIdentity(ctx, e.store, identity, u.Active, mappings); err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		if u.Active {
			res.UsersSynced++
		} else {
			res.UsersDeactivated++
		}
	}

	groups, err := e.directory.ListGroups(ctx)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res, nil
	}
	for _, g := range groups {
		raw, _ := json.Marshal(g.Raw)
		sg := &tables.TableSCIMGroup{
			Provider:          e.provider.Name(),
			ExternalID:        g.ExternalID,
			DisplayName:       g.DisplayName,
			RawAttributesJSON: string(raw),
			LastSyncedAt:      time.Now(),
		}
		if err := e.store.UpsertSCIMGroup(ctx, sg); err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		res.GroupsSynced++
	}
	return res, nil
}

// ---- Keycloak Directory (Admin REST API) ----

// Directory returns a Keycloak Admin API directory client.
func (kc *KeycloakConfig) Directory(httpClient *http.Client) (Directory, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &keycloakDirectory{cfg: kc, httpClient: httpClient}, nil
}

type keycloakDirectory struct {
	cfg        *KeycloakConfig
	httpClient *http.Client
}

// kcUser is the subset of the Keycloak Admin API user representation we consume.
type kcUser struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Enabled   bool   `json:"enabled"`
}

// kcGroup is the subset of the Keycloak Admin API group representation we
// consume.
type kcGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// adminToken obtains an admin access token via the client-credentials grant on
// the realm token endpoint.
func (d *keycloakDirectory) adminToken(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", d.cfg.ClientID)
	form.Set("client_secret", d.cfg.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.cfg.TokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sso: keycloak token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sso: keycloak token request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("sso: failed to parse keycloak token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("sso: keycloak token response missing access_token")
	}
	return tok.AccessToken, nil
}

// adminGetJSON performs an authenticated GET against an admin endpoint and
// decodes the JSON body into out.
func (d *keycloakDirectory) adminGetJSON(ctx context.Context, token, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sso: keycloak admin GET %s returned %d: %s", rawURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

func (d *keycloakDirectory) ListUsers(ctx context.Context) ([]DirectoryUser, error) {
	token, err := d.adminToken(ctx)
	if err != nil {
		return nil, err
	}
	const pageSize = 100
	var out []DirectoryUser
	for first := 0; ; first += pageSize {
		pageURL := fmt.Sprintf("%s?first=%d&max=%d", d.cfg.AdminUsersURL(), first, pageSize)
		var users []kcUser
		if err := d.adminGetJSON(ctx, token, pageURL, &users); err != nil {
			return nil, err
		}
		if len(users) == 0 {
			return out, nil
		}
		for _, u := range users {
			var raw map[string]any
			if b, err := json.Marshal(u); err == nil {
				_ = json.Unmarshal(b, &raw)
			}
			display := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
			if display == "" {
				display = firstNonEmpty(u.Username, u.Email)
			}
			out = append(out, DirectoryUser{
				ExternalID:  u.ID,
				UserName:    firstNonEmpty(u.Username, u.Email),
				Email:       strings.ToLower(strings.TrimSpace(u.Email)),
				DisplayName: display,
				Active:      u.Enabled,
				Raw:         raw,
			})
		}
		if len(users) < pageSize {
			return out, nil
		}
	}
}

func (d *keycloakDirectory) ListGroups(ctx context.Context) ([]DirectoryGroup, error) {
	token, err := d.adminToken(ctx)
	if err != nil {
		return nil, err
	}
	var groups []kcGroup
	if err := d.adminGetJSON(ctx, token, d.cfg.AdminGroupsURL(), &groups); err != nil {
		return nil, err
	}
	out := make([]DirectoryGroup, 0, len(groups))
	for _, g := range groups {
		var raw map[string]any
		if b, err := json.Marshal(g); err == nil {
			_ = json.Unmarshal(b, &raw)
		}
		out = append(out, DirectoryGroup{
			ExternalID:  g.ID,
			DisplayName: firstNonEmpty(g.Name, g.Path),
			Raw:         raw,
		})
	}
	return out, nil
}

// ---- shared provisioning ----

// ProvisionFromIdentity is the shared just-in-time provisioning routine used by
// the batch sync engine, the auth middleware's JWT branch, and the inbound
// SCIM endpoint. It:
//  1. resolves (or creates) the managed Wave-2 user backing this identity,
//  2. applies the attribute→role/team/BU mapping rules (best-effort: mapping
//     side effects never fail provisioning — auth must not break because a
//     mapped role is missing),
//  3. upserts the SCIM mirror row (recording the mapping outcome) linked to
//     that managed user, and
//  4. reflects the active flag (deactivation flips Active=false; it never
//     deletes, preserving the link and audit trail).
//
// Mapped role assignments are additive-only: removing a claim does NOT revoke
// a previously assigned role (documented drift — operators may also assign
// roles manually, and this routine must never delete those).
//
// It returns the managed user so the auth middleware can authorize against it.
func ProvisionFromIdentity(ctx context.Context, store configstore.ConfigStore, identity *Identity, active bool, mappings AttributeMappings) (*tables.TableUser, error) {
	if store == nil {
		return nil, fmt.Errorf("sso: provisioning requires a config store")
	}
	if identity == nil || strings.TrimSpace(identity.Subject) == "" {
		return nil, fmt.Errorf("sso: cannot provision from empty identity")
	}

	managed, err := resolveOrCreateManagedUser(ctx, store, identity)
	if err != nil {
		return nil, err
	}

	outcome := ApplyMappings(identity, mappings)
	mappedRoleID, mappedTeamIDs, mappedBUID := applyMappingOutcome(ctx, store, managed, outcome)

	raw, _ := json.Marshal(identity.Raw)
	scimUser := &tables.TableSCIMUser{
		Provider:          identity.Provider,
		ExternalID:        identity.Subject,
		UserName:          identity.UserName,
		Email:             identity.Email,
		DisplayName:       identity.DisplayName,
		Active:            active,
		Groups:            identity.Groups,
		RawAttributesJSON: string(raw),
		ManagedUserID:     &managed.ID,
		MappedRoleID:      mappedRoleID,
		MappedTeamIDs:     mappedTeamIDs,
		MappedBusinessUnitID: mappedBUID,
		LastSyncedAt:      time.Now(),
	}
	if err := store.UpsertSCIMUser(ctx, scimUser); err != nil {
		return nil, err
	}
	return managed, nil
}

// applyMappingOutcome performs the store side effects of a mapping outcome,
// best-effort (a missing role/team/BU is skipped, never an error). Returns
// what was actually applied for recording on the SCIM row.
func applyMappingOutcome(ctx context.Context, store configstore.ConfigStore, managed *tables.TableUser, outcome MappingOutcome) (mappedRoleID *string, mappedTeamIDs []string, mappedBUID *string) {
	if outcome.Empty() || managed == nil {
		return nil, nil, nil
	}

	// Role: assign when the named role exists and the user does not hold it.
	if outcome.RoleName != "" {
		if role, err := store.GetRoleByName(ctx, outcome.RoleName); err == nil && role != nil {
			assigned := false
			if existing, err := store.GetRoleAssignmentsByUser(ctx, managed.ID); err == nil {
				for i := range existing {
					if existing[i].RoleID == role.ID {
						assigned = true
						break
					}
				}
			}
			if !assigned {
				_ = store.CreateRoleAssignment(ctx, &tables.TableRoleAssignment{
					ID:        uuid.New().String(),
					RoleID:    role.ID,
					UserID:    managed.ID,
					CreatedAt: time.Now(),
				})
			}
			mappedRoleID = &role.ID
		}
	}

	// Teams: link-only — resolve refs against existing teams by IdP source ID,
	// then by name; unknown refs are skipped (recorded refs are resolved IDs).
	for _, ref := range outcome.TeamRefs {
		if team, err := store.GetTeamBySourceID(ctx, ref); err == nil && team != nil {
			mappedTeamIDs = append(mappedTeamIDs, team.ID)
			continue
		}
		if team, err := store.GetTeamByName(ctx, ref, ""); err == nil && team != nil {
			mappedTeamIDs = append(mappedTeamIDs, team.ID)
		}
	}

	// Business unit: resolve by name and place the managed user in it.
	if outcome.BusinessUnitName != "" {
		if units, _, err := store.GetBusinessUnits(ctx, configstore.BusinessUnitsQueryParams{Search: outcome.BusinessUnitName, Limit: 50}); err == nil {
			for i := range units {
				if strings.EqualFold(strings.TrimSpace(units[i].Name), outcome.BusinessUnitName) {
					if managed.BusinessUnitID == nil || *managed.BusinessUnitID != units[i].ID {
						managed.BusinessUnitID = &units[i].ID
						_ = store.UpdateUser(ctx, managed)
					}
					mappedBUID = &units[i].ID
					break
				}
			}
		}
	}

	return mappedRoleID, mappedTeamIDs, mappedBUID
}

// resolveOrCreateManagedUser finds the managed user for an identity (via the
// existing SCIM link, then by email) and creates one if none exists.
func resolveOrCreateManagedUser(ctx context.Context, store configstore.ConfigStore, identity *Identity) (*tables.TableUser, error) {
	// 1. Existing SCIM link.
	if existing, err := store.GetSCIMUserByExternalID(ctx, identity.Provider, identity.Subject); err == nil && existing != nil && existing.ManagedUserID != nil {
		if u, err := store.GetUser(ctx, *existing.ManagedUserID); err == nil && u != nil {
			return u, nil
		}
	}

	// 2. Match by email (TableUser.Email carries a unique index).
	email := strings.ToLower(strings.TrimSpace(identity.Email))
	if email == "" {
		// Synthesize a stable, namespaced email so the managed user is valid and
		// future logins resolve to the same row.
		email = fmt.Sprintf("%s@%s.sso.local", identity.Subject, identity.Provider)
	}
	users, _, err := store.GetUsers(ctx, configstore.UsersQueryParams{Search: email, Limit: 100})
	if err == nil {
		for i := range users {
			if strings.EqualFold(strings.TrimSpace(users[i].Email), email) {
				return &users[i], nil
			}
		}
	}

	// 3. Create a new managed user.
	name := firstNonEmpty(identity.DisplayName, identity.UserName, email)
	u := &tables.TableUser{
		ID:     uuid.New().String(),
		Name:   name,
		Email:  email,
		Status: tables.UserStatusActive,
	}
	if err := store.CreateUser(ctx, u); err != nil {
		return nil, fmt.Errorf("sso: failed to create managed user: %w", err)
	}
	return u, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
