// This file contains the inbound SCIM 2.0 REST surface:
// /scim/v2/Users, /scim/v2/Groups, and /scim/v2/ServiceProviderConfig, the
// endpoints an IdP (Okta, Entra, Keycloak) pushes provisioning changes to.
//
// SECURITY MODEL — this prefix is whitelisted past the session auth middleware
// (IdPs cannot hold dashboard sessions), so the bearer check here is the ONLY
// gate:
//   - requireProvisioningAuth runs FIRST in every handler.
//   - Unconfigured provisioning (disabled, or no token) returns 404 before any
//     body parsing — the endpoint is indistinguishable from absent, and the
//     default-off request path is byte-for-byte unchanged.
//   - The bearer token compares in constant time (crypto/subtle).
//
// Semantics: users/groups persist into the same SCIM mirror tables the pull
// sync uses; user mutations re-run ProvisionFromIdentity so managed-user
// linking and attribute mappings apply identically across all three
// provisioning entry points. DELETE is soft for users (Active=false, mirroring
// the sync engine's never-hard-delete semantics) and hard for groups.
package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/sso"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// scimContentType is the SCIM media type (RFC 7644 §3.1).
const scimContentType = "application/scim+json"

// AuditActionSCIMProvision is recorded on every inbound SCIM mutation.
const AuditActionSCIMProvision = "scim.provision"

// SCIMV2Handler serves the inbound SCIM 2.0 provisioning API.
type SCIMV2Handler struct {
	configStore  configstore.ConfigStore
	scimResolver func() *sso.SCIMConfig
}

// NewSCIMV2Handler creates the inbound SCIM handler.
func NewSCIMV2Handler(configStore configstore.ConfigStore, scimResolver func() *sso.SCIMConfig) (*SCIMV2Handler, error) {
	if configStore == nil {
		return nil, fmt.Errorf("config store is required")
	}
	return &SCIMV2Handler{configStore: configStore, scimResolver: scimResolver}, nil
}

// RegisterRoutes wires the SCIM 2.0 endpoints.
func (h *SCIMV2Handler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/scim/v2/ServiceProviderConfig", lib.ChainMiddlewares(h.serviceProviderConfig, middlewares...))
	r.GET("/scim/v2/Users", lib.ChainMiddlewares(h.listUsers, middlewares...))
	r.POST("/scim/v2/Users", lib.ChainMiddlewares(h.createUser, middlewares...))
	r.GET("/scim/v2/Users/{id}", lib.ChainMiddlewares(h.getUser, middlewares...))
	r.PATCH("/scim/v2/Users/{id}", lib.ChainMiddlewares(h.patchUser, middlewares...))
	r.DELETE("/scim/v2/Users/{id}", lib.ChainMiddlewares(h.deleteUser, middlewares...))
	r.GET("/scim/v2/Groups", lib.ChainMiddlewares(h.listGroups, middlewares...))
	r.POST("/scim/v2/Groups", lib.ChainMiddlewares(h.createGroup, middlewares...))
	r.GET("/scim/v2/Groups/{id}", lib.ChainMiddlewares(h.getGroup, middlewares...))
	r.PATCH("/scim/v2/Groups/{id}", lib.ChainMiddlewares(h.patchGroup, middlewares...))
	r.DELETE("/scim/v2/Groups/{id}", lib.ChainMiddlewares(h.deleteGroup, middlewares...))
}

// sendSCIM writes a SCIM JSON response.
func sendSCIM(ctx *fasthttp.RequestCtx, status int, body any) {
	ctx.SetStatusCode(status)
	ctx.SetContentType(scimContentType)
	data, err := json.Marshal(body)
	if err != nil {
		ctx.SetStatusCode(500)
		return
	}
	ctx.SetBody(data)
}

// sendSCIMError writes an RFC 7644 error body.
func sendSCIMError(ctx *fasthttp.RequestCtx, status int, scimType, detail string) {
	sendSCIM(ctx, status, sso.SCIMErrorBody(status, scimType, detail))
}

// requireProvisioningAuth is the single gate for every /scim/v2 handler. 404
// while unconfigured (before reading anything), 401 on a bad token. Returns
// the active SCIM config on success.
func (h *SCIMV2Handler) requireProvisioningAuth(ctx *fasthttp.RequestCtx) (*sso.SCIMConfig, bool) {
	var scim *sso.SCIMConfig
	if h.scimResolver != nil {
		scim = h.scimResolver()
	}
	if scim == nil || !scim.Enabled || !scim.Provisioning.InboundEnabled() {
		sendSCIMError(ctx, 404, "", "Not found")
		return nil, false
	}
	auth := string(ctx.Request.Header.Peek("Authorization"))
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		sendSCIMError(ctx, 401, "", "Bearer authentication required")
		return nil, false
	}
	presented := strings.TrimSpace(auth[7:])
	expected := scim.Provisioning.ResolvedBearerToken()
	if subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) != 1 {
		sendSCIMError(ctx, 401, "", "Invalid bearer token")
		return nil, false
	}
	return scim, true
}

// provider returns the provider scope for mirror rows.
func scimProvider(scim *sso.SCIMConfig) string {
	return strings.ToLower(strings.TrimSpace(scim.Provider))
}

// listParams extracts filter / startIndex / count.
func listParams(ctx *fasthttp.RequestCtx) (filter *sso.SCIMFilter, startIndex, count int, err error) {
	startIndex, count = 1, 100
	if v := string(ctx.QueryArgs().Peek("startIndex")); v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 {
			startIndex = n
		}
	}
	if v := string(ctx.QueryArgs().Peek("count")); v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n >= 0 {
			count = n
		}
	}
	if v := string(ctx.QueryArgs().Peek("filter")); v != "" {
		filter, err = sso.ParseSCIMFilter(v)
		if err != nil {
			return nil, 0, 0, err
		}
	}
	return filter, startIndex, count, nil
}

// paginate slices resources per startIndex (1-based) and count.
func paginate(resources []map[string]any, startIndex, count int) []map[string]any {
	if startIndex < 1 {
		startIndex = 1
	}
	offset := startIndex - 1
	if offset >= len(resources) {
		return []map[string]any{}
	}
	end := offset + count
	if end > len(resources) {
		end = len(resources)
	}
	return resources[offset:end]
}

func (h *SCIMV2Handler) serviceProviderConfig(ctx *fasthttp.RequestCtx) {
	if _, ok := h.requireProvisioningAuth(ctx); !ok {
		return
	}
	sendSCIM(ctx, 200, map[string]any{
		"schemas":          []any{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
		"patch":            map[string]any{"supported": true},
		"filter":           map[string]any{"supported": true, "maxResults": 1000},
		"bulk":             map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"changePassword":   map[string]any{"supported": false},
		"sort":             map[string]any{"supported": false},
		"etag":             map[string]any{"supported": false},
		"authenticationSchemes": []any{map[string]any{
			"type": "oauthbearertoken", "name": "Bearer Token",
			"description": "Static bearer token configured in scim_config.provisioning",
		}},
	})
}

// loadProviderUsers loads every mirror row for the provider (org-scale).
func (h *SCIMV2Handler) loadProviderUsers(ctx *fasthttp.RequestCtx, provider string) ([]configstoreTables.TableSCIMUser, error) {
	users, _, err := h.configStore.GetSCIMUsers(ctx, configstore.SCIMUsersQueryParams{Provider: provider, Limit: 10000})
	return users, err
}

func (h *SCIMV2Handler) listUsers(ctx *fasthttp.RequestCtx) {
	scim, ok := h.requireProvisioningAuth(ctx)
	if !ok {
		return
	}
	filter, startIndex, count, err := listParams(ctx)
	if err != nil {
		sendSCIMError(ctx, 400, sso.SCIMErrInvalidSyntax, err.Error())
		return
	}
	users, err := h.loadProviderUsers(ctx, scimProvider(scim))
	if err != nil {
		sendSCIMError(ctx, 500, "", "Failed to list users")
		return
	}
	var resources []map[string]any
	for i := range users {
		resource := sso.SCIMUserResource(&users[i])
		if filter == nil || filter.Matches(resource) {
			resources = append(resources, resource)
		}
	}
	sendSCIM(ctx, 200, sso.SCIMListResponse(paginate(resources, startIndex, count), len(resources), startIndex))
}

func (h *SCIMV2Handler) getUser(ctx *fasthttp.RequestCtx) {
	if _, ok := h.requireProvisioningAuth(ctx); !ok {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	user, err := h.configStore.GetSCIMUser(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			sendSCIMError(ctx, 404, "", "User not found")
			return
		}
		sendSCIMError(ctx, 500, "", "Failed to load user")
		return
	}
	sendSCIM(ctx, 200, sso.SCIMUserResource(user))
}

// provisionFromRow re-runs the shared provisioning path for a mirror row so
// managed-user linking and attribute mappings stay consistent with JWT login
// and pull sync.
func (h *SCIMV2Handler) provisionFromRow(ctx *fasthttp.RequestCtx, scim *sso.SCIMConfig, row *configstoreTables.TableSCIMUser) (*configstoreTables.TableSCIMUser, error) {
	var raw map[string]any
	if row.RawAttributesJSON != "" {
		_ = json.Unmarshal([]byte(row.RawAttributesJSON), &raw)
	}
	identity := &sso.Identity{
		Provider:    scimProvider(scim),
		Subject:     row.ExternalID,
		Email:       row.Email,
		UserName:    row.UserName,
		DisplayName: row.DisplayName,
		Groups:      row.Groups,
		Raw:         raw,
	}
	mappings := sso.AttributeMappings{}
	if p, err := sso.NewProvider(scim); err == nil && p != nil {
		mappings = p.Mappings()
	}
	if _, err := sso.ProvisionFromIdentity(ctx, h.configStore, identity, row.Active, mappings); err != nil {
		return nil, err
	}
	return h.configStore.GetSCIMUserByExternalID(ctx, scimProvider(scim), row.ExternalID)
}

func (h *SCIMV2Handler) createUser(ctx *fasthttp.RequestCtx) {
	scim, ok := h.requireProvisioningAuth(ctx)
	if !ok {
		return
	}
	var resource map[string]any
	if err := json.Unmarshal(ctx.PostBody(), &resource); err != nil {
		sendSCIMError(ctx, 400, sso.SCIMErrInvalidSyntax, "Malformed JSON body")
		return
	}
	row := &configstoreTables.TableSCIMUser{Active: true}
	sso.ApplySCIMUserResource(row, resource)
	if row.ExternalID == "" {
		row.ExternalID = row.UserName
	}
	if row.ExternalID == "" {
		sendSCIMError(ctx, 400, sso.SCIMErrInvalidSyntax, "userName or externalId is required")
		return
	}
	raw, _ := json.Marshal(resource)
	row.RawAttributesJSON = string(raw)

	saved, err := h.provisionFromRow(ctx, scim, row)
	if err != nil {
		sendSCIMError(ctx, 500, "", "Failed to provision user")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionSCIMProvision, configstoreTables.AuditOutcomeSuccess, "user:"+saved.ExternalID)
	sendSCIM(ctx, 201, sso.SCIMUserResource(saved))
}

func (h *SCIMV2Handler) patchUser(ctx *fasthttp.RequestCtx) {
	scim, ok := h.requireProvisioningAuth(ctx)
	if !ok {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	user, err := h.configStore.GetSCIMUser(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			sendSCIMError(ctx, 404, "", "User not found")
			return
		}
		sendSCIMError(ctx, 500, "", "Failed to load user")
		return
	}

	patch, err := sso.ParsePatchRequest(ctx.PostBody())
	if err != nil {
		sendSCIMPatchError(ctx, err)
		return
	}
	resource := sso.SCIMUserResource(user)
	if err := sso.ApplyPatch(resource, patch); err != nil {
		sendSCIMPatchError(ctx, err)
		return
	}
	sso.ApplySCIMUserResource(user, resource)

	saved, err := h.provisionFromRow(ctx, scim, user)
	if err != nil {
		sendSCIMError(ctx, 500, "", "Failed to persist user")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionSCIMProvision, configstoreTables.AuditOutcomeSuccess, "user:"+saved.ExternalID)
	sendSCIM(ctx, 200, sso.SCIMUserResource(saved))
}

func (h *SCIMV2Handler) deleteUser(ctx *fasthttp.RequestCtx) {
	scim, ok := h.requireProvisioningAuth(ctx)
	if !ok {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	user, err := h.configStore.GetSCIMUser(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			sendSCIMError(ctx, 404, "", "User not found")
			return
		}
		sendSCIMError(ctx, 500, "", "Failed to load user")
		return
	}
	// Soft delete: Active=false, mirroring the sync engine's never-hard-delete
	// semantics (the managed-user link and audit trail survive re-enable).
	user.Active = false
	if _, err := h.provisionFromRow(ctx, scim, user); err != nil {
		sendSCIMError(ctx, 500, "", "Failed to deactivate user")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionSCIMProvision, configstoreTables.AuditOutcomeSuccess, "user:"+user.ExternalID+":deactivated")
	ctx.SetStatusCode(204)
}

// sendSCIMPatchError maps a patch error to its RFC status.
func sendSCIMPatchError(ctx *fasthttp.RequestCtx, err error) {
	var patchErr *sso.SCIMPatchError
	if errors.As(err, &patchErr) {
		status := 400
		if patchErr.ScimType == sso.SCIMErrNoTarget {
			status = 404
		}
		sendSCIMError(ctx, status, patchErr.ScimType, patchErr.Detail)
		return
	}
	sendSCIMError(ctx, 400, sso.SCIMErrInvalidSyntax, err.Error())
}

func (h *SCIMV2Handler) listGroups(ctx *fasthttp.RequestCtx) {
	scim, ok := h.requireProvisioningAuth(ctx)
	if !ok {
		return
	}
	filter, startIndex, count, err := listParams(ctx)
	if err != nil {
		sendSCIMError(ctx, 400, sso.SCIMErrInvalidSyntax, err.Error())
		return
	}
	groups, _, err := h.configStore.GetSCIMGroups(ctx, configstore.SCIMGroupsQueryParams{Provider: scimProvider(scim), Limit: 10000})
	if err != nil {
		sendSCIMError(ctx, 500, "", "Failed to list groups")
		return
	}
	var resources []map[string]any
	for i := range groups {
		resource := sso.SCIMGroupResource(&groups[i])
		if filter == nil || filter.Matches(resource) {
			resources = append(resources, resource)
		}
	}
	sendSCIM(ctx, 200, sso.SCIMListResponse(paginate(resources, startIndex, count), len(resources), startIndex))
}

// findGroupByRowID loads a group mirror row by its SCIM id (our row ID).
func (h *SCIMV2Handler) findGroupByRowID(ctx *fasthttp.RequestCtx, provider, id string) (*configstoreTables.TableSCIMGroup, error) {
	groups, _, err := h.configStore.GetSCIMGroups(ctx, configstore.SCIMGroupsQueryParams{Provider: provider, Limit: 10000})
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].ID == id {
			return &groups[i], nil
		}
	}
	return nil, configstore.ErrNotFound
}

func (h *SCIMV2Handler) getGroup(ctx *fasthttp.RequestCtx) {
	scim, ok := h.requireProvisioningAuth(ctx)
	if !ok {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	group, err := h.findGroupByRowID(ctx, scimProvider(scim), id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			sendSCIMError(ctx, 404, "", "Group not found")
			return
		}
		sendSCIMError(ctx, 500, "", "Failed to load group")
		return
	}
	sendSCIM(ctx, 200, sso.SCIMGroupResource(group))
}

func (h *SCIMV2Handler) createGroup(ctx *fasthttp.RequestCtx) {
	scim, ok := h.requireProvisioningAuth(ctx)
	if !ok {
		return
	}
	var resource map[string]any
	if err := json.Unmarshal(ctx.PostBody(), &resource); err != nil {
		sendSCIMError(ctx, 400, sso.SCIMErrInvalidSyntax, "Malformed JSON body")
		return
	}
	group := &configstoreTables.TableSCIMGroup{
		Provider:     scimProvider(scim),
		LastSyncedAt: time.Now(),
	}
	sso.ApplySCIMGroupResource(group, resource)
	if group.ExternalID == "" {
		group.ExternalID = firstNonEmptyString(group.DisplayName, uuid.NewString())
	}
	if group.DisplayName == "" {
		sendSCIMError(ctx, 400, sso.SCIMErrInvalidSyntax, "displayName is required")
		return
	}
	raw, _ := json.Marshal(resource)
	group.RawAttributesJSON = string(raw)
	if err := h.configStore.UpsertSCIMGroup(ctx, group); err != nil {
		sendSCIMError(ctx, 500, "", "Failed to create group")
		return
	}
	saved, err := h.configStore.GetSCIMGroupByExternalID(ctx, group.Provider, group.ExternalID)
	if err != nil {
		saved = group
	}
	recordAudit(ctx, h.configStore, AuditActionSCIMProvision, configstoreTables.AuditOutcomeSuccess, "group:"+group.ExternalID)
	sendSCIM(ctx, 201, sso.SCIMGroupResource(saved))
}

func (h *SCIMV2Handler) patchGroup(ctx *fasthttp.RequestCtx) {
	scim, ok := h.requireProvisioningAuth(ctx)
	if !ok {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	group, err := h.findGroupByRowID(ctx, scimProvider(scim), id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			sendSCIMError(ctx, 404, "", "Group not found")
			return
		}
		sendSCIMError(ctx, 500, "", "Failed to load group")
		return
	}

	patch, err := sso.ParsePatchRequest(ctx.PostBody())
	if err != nil {
		sendSCIMPatchError(ctx, err)
		return
	}
	resource := sso.SCIMGroupResource(group)
	if err := sso.ApplyPatch(resource, patch); err != nil {
		sendSCIMPatchError(ctx, err)
		return
	}
	sso.ApplySCIMGroupResource(group, resource)
	group.LastSyncedAt = time.Now()

	if err := h.configStore.UpsertSCIMGroup(ctx, group); err != nil {
		sendSCIMError(ctx, 500, "", "Failed to persist group")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionSCIMProvision, configstoreTables.AuditOutcomeSuccess, "group:"+group.ExternalID)
	sendSCIM(ctx, 200, sso.SCIMGroupResource(group))
}

func (h *SCIMV2Handler) deleteGroup(ctx *fasthttp.RequestCtx) {
	scim, ok := h.requireProvisioningAuth(ctx)
	if !ok {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	group, err := h.findGroupByRowID(ctx, scimProvider(scim), id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			sendSCIMError(ctx, 404, "", "Group not found")
			return
		}
		sendSCIMError(ctx, 500, "", "Failed to load group")
		return
	}
	if err := h.configStore.DeleteSCIMGroup(ctx, group.ID); err != nil {
		sendSCIMError(ctx, 500, "", "Failed to delete group")
		return
	}
	recordAudit(ctx, h.configStore, AuditActionSCIMProvision, configstoreTables.AuditOutcomeSuccess, "group:"+group.ExternalID+":deleted")
	ctx.SetStatusCode(204)
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
