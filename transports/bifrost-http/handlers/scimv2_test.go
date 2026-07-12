package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/sso"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

const scimV2TestToken = "scim-test-bearer-token"

// newSCIMV2Handler builds the inbound SCIM handler over a real SQLite
// configstore with provisioning enabled.
func newSCIMV2Handler(t *testing.T) (*SCIMV2Handler, configstore.ConfigStore) {
	t.Helper()
	store := newAuditTestStore(t)
	scim := &sso.SCIMConfig{
		Enabled:  true,
		Provider: "okta",
		Provisioning: &sso.SCIMProvisioningConfig{
			Enabled:     true,
			BearerToken: scimV2TestToken,
		},
	}
	h, err := NewSCIMV2Handler(store, func() *sso.SCIMConfig { return scim })
	require.NoError(t, err)
	return h, store
}

// scimRequest builds an authenticated SCIM request ctx.
func scimRequest(method, uri, token string, body []byte) *fasthttp.RequestCtx {
	ctx := auditRequestCtx(method, uri, body)
	if token != "" {
		ctx.Request.Header.Set("Authorization", "Bearer "+token)
	}
	return ctx
}

func TestSCIMV2_404WhenUnconfigured(t *testing.T) {
	store := newAuditTestStore(t)
	cases := []*sso.SCIMConfig{
		nil,
		{Enabled: false, Provider: "okta", Provisioning: &sso.SCIMProvisioningConfig{Enabled: true, BearerToken: "x"}},
		{Enabled: true, Provider: "okta"}, // no provisioning block
		{Enabled: true, Provider: "okta", Provisioning: &sso.SCIMProvisioningConfig{Enabled: false, BearerToken: "x"}},
		{Enabled: true, Provider: "okta", Provisioning: &sso.SCIMProvisioningConfig{Enabled: true}}, // no token
	}
	for i, scim := range cases {
		cfg := scim
		h, err := NewSCIMV2Handler(store, func() *sso.SCIMConfig { return cfg })
		require.NoError(t, err)
		ctx := scimRequest("GET", "/scim/v2/Users", "whatever", nil)
		h.listUsers(ctx)
		assert.Equal(t, 404, ctx.Response.StatusCode(), "case %d: unconfigured provisioning must 404", i)
	}
}

func TestSCIMV2_401OnBadToken(t *testing.T) {
	h, _ := newSCIMV2Handler(t)

	ctx := scimRequest("GET", "/scim/v2/Users", "wrong-token", nil)
	h.listUsers(ctx)
	assert.Equal(t, 401, ctx.Response.StatusCode())

	ctx = scimRequest("GET", "/scim/v2/Users", "", nil)
	h.listUsers(ctx)
	assert.Equal(t, 401, ctx.Response.StatusCode())
}

// createSCIMUser POSTs a user resource and returns its SCIM id.
func createSCIMUser(t *testing.T, h *SCIMV2Handler, userName, email string) string {
	t.Helper()
	body := fmt.Sprintf(`{
		"schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
		"externalId": "ext-%s",
		"userName": %q,
		"displayName": "User %s",
		"active": true,
		"emails": [{"value": %q, "type": "work", "primary": true}]
	}`, userName, userName, userName, email)
	ctx := scimRequest("POST", "/scim/v2/Users", scimV2TestToken, []byte(body))
	h.createUser(ctx)
	require.Equal(t, 201, ctx.Response.StatusCode(), "body: %s", ctx.Response.Body())
	var resource map[string]any
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resource))
	id, _ := resource["id"].(string)
	require.NotEmpty(t, id)
	return id
}

func TestSCIMV2_CreateProvisionsManagedUser(t *testing.T) {
	h, store := newSCIMV2Handler(t)
	id := createSCIMUser(t, h, "bjensen", "bjensen@example.com")

	// The mirror row links to a managed user created just-in-time.
	row, err := store.GetSCIMUser(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, row.ManagedUserID)
	managed, err := store.GetUser(context.Background(), *row.ManagedUserID)
	require.NoError(t, err)
	assert.Equal(t, "bjensen@example.com", managed.Email)

	// An audit event recorded the provisioning.
	logs, _, err := store.GetAuditLogs(context.Background(), configstore.AuditLogsQueryParams{Action: AuditActionSCIMProvision})
	require.NoError(t, err)
	assert.NotEmpty(t, logs)
}

func TestSCIMV2_ListAndFilter(t *testing.T) {
	h, _ := newSCIMV2Handler(t)
	createSCIMUser(t, h, "bjensen", "bjensen@example.com")
	createSCIMUser(t, h, "mjones", "mjones@other.example")

	// Unfiltered list.
	ctx := scimRequest("GET", "/scim/v2/Users", scimV2TestToken, nil)
	h.listUsers(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode())
	var list struct {
		Schemas      []string         `json:"schemas"`
		TotalResults int              `json:"totalResults"`
		StartIndex   int              `json:"startIndex"`
		ItemsPerPage int              `json:"itemsPerPage"`
		Resources    []map[string]any `json:"Resources"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &list))
	assert.Equal(t, []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"}, list.Schemas)
	assert.Equal(t, 2, list.TotalResults)
	assert.Equal(t, 2, list.ItemsPerPage)

	// userName eq filter (the query Okta/Entra actually send).
	ctx = scimRequest("GET", `/scim/v2/Users?filter=userName+eq+%22bjensen%22`, scimV2TestToken, nil)
	h.listUsers(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode())
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &list))
	require.Equal(t, 1, list.TotalResults)
	assert.Equal(t, "bjensen", list.Resources[0]["userName"])

	// Value-path filter.
	ctx = scimRequest("GET", `/scim/v2/Users?filter=emails%5Btype+eq+%22work%22+and+value+co+%22other.example%22%5D`, scimV2TestToken, nil)
	h.listUsers(ctx)
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &list))
	require.Equal(t, 1, list.TotalResults)
	assert.Equal(t, "mjones", list.Resources[0]["userName"])

	// Malformed filter is a SCIM 400.
	ctx = scimRequest("GET", `/scim/v2/Users?filter=userName+xy+%22z%22`, scimV2TestToken, nil)
	h.listUsers(ctx)
	assert.Equal(t, 400, ctx.Response.StatusCode())
}

func TestSCIMV2_PatchActiveFlipMirrorsRow(t *testing.T) {
	h, store := newSCIMV2Handler(t)
	id := createSCIMUser(t, h, "bjensen", "bjensen@example.com")

	patch := `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{"op": "replace", "path": "active", "value": false}]
	}`
	ctx := scimRequest("PATCH", "/scim/v2/Users/"+id, scimV2TestToken, []byte(patch))
	ctx.SetUserValue("id", id)
	h.patchUser(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode(), "body: %s", ctx.Response.Body())

	var resource map[string]any
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resource))
	assert.Equal(t, false, resource["active"])

	row, err := store.GetSCIMUser(context.Background(), id)
	require.NoError(t, err)
	assert.False(t, row.Active, "the deactivation must persist on the mirror row")
	assert.NotNil(t, row.ManagedUserID, "the managed-user link must survive deactivation")

	// DELETE is the same soft deactivation.
	id2 := createSCIMUser(t, h, "mjones", "mjones@example.com")
	delCtx := scimRequest("DELETE", "/scim/v2/Users/"+id2, scimV2TestToken, nil)
	delCtx.SetUserValue("id", id2)
	h.deleteUser(delCtx)
	assert.Equal(t, 204, delCtx.Response.StatusCode())
	row2, err := store.GetSCIMUser(context.Background(), id2)
	require.NoError(t, err)
	assert.False(t, row2.Active)
}

func TestSCIMV2_GroupLifecycleWithMemberPatch(t *testing.T) {
	h, _ := newSCIMV2Handler(t)

	// Create.
	body := `{
		"schemas": ["urn:ietf:params:scim:schemas:core:2.0:Group"],
		"externalId": "grp-eng",
		"displayName": "Engineering",
		"members": [{"value": "u1"}]
	}`
	ctx := scimRequest("POST", "/scim/v2/Groups", scimV2TestToken, []byte(body))
	h.createGroup(ctx)
	require.Equal(t, 201, ctx.Response.StatusCode(), "body: %s", ctx.Response.Body())
	var resource map[string]any
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resource))
	groupID, _ := resource["id"].(string)
	require.NotEmpty(t, groupID)

	// PATCH: add a member, then remove the original (Azure remove-with-value).
	patch := `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [
			{"op": "add", "path": "members", "value": [{"value": "u2"}]},
			{"op": "remove", "path": "members", "value": [{"value": "u1"}]}
		]
	}`
	patchCtx := scimRequest("PATCH", "/scim/v2/Groups/"+groupID, scimV2TestToken, []byte(patch))
	patchCtx.SetUserValue("id", groupID)
	h.patchGroup(patchCtx)
	require.Equal(t, 200, patchCtx.Response.StatusCode(), "body: %s", patchCtx.Response.Body())
	require.NoError(t, json.Unmarshal(patchCtx.Response.Body(), &resource))
	members := resource["members"].([]any)
	require.Len(t, members, 1)
	assert.Equal(t, "u2", members[0].(map[string]any)["value"])

	// DELETE removes the group; a follow-up GET 404s.
	delCtx := scimRequest("DELETE", "/scim/v2/Groups/"+groupID, scimV2TestToken, nil)
	delCtx.SetUserValue("id", groupID)
	h.deleteGroup(delCtx)
	assert.Equal(t, 204, delCtx.Response.StatusCode())
	getCtx := scimRequest("GET", "/scim/v2/Groups/"+groupID, scimV2TestToken, nil)
	getCtx.SetUserValue("id", groupID)
	h.getGroup(getCtx)
	assert.Equal(t, 404, getCtx.Response.StatusCode())
}

func TestSCIMV2_ServiceProviderConfigAdvertisesPatchAndFilter(t *testing.T) {
	h, _ := newSCIMV2Handler(t)
	ctx := scimRequest("GET", "/scim/v2/ServiceProviderConfig", scimV2TestToken, nil)
	h.serviceProviderConfig(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode())
	var cfg struct {
		Patch  struct{ Supported bool }
		Filter struct{ Supported bool }
		Bulk   struct{ Supported bool }
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &cfg))
	assert.True(t, cfg.Patch.Supported)
	assert.True(t, cfg.Filter.Supported)
	assert.False(t, cfg.Bulk.Supported)
}
