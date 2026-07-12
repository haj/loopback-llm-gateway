package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAlertChannelsHandler builds a handler over a real SQLite configstore
// (full migration chain, including add_alert_channels_table). The dispatcher
// is nil: mutations persist and reload is a no-op, matching the constructor
// contract for tests.
func newAlertChannelsHandler(t *testing.T) (*AlertChannelsHandler, configstore.ConfigStore) {
	t.Helper()
	store := newAuditTestStore(t)
	h, err := NewAlertChannelsHandler(store, nil)
	require.NoError(t, err)
	return h, store
}

// createTestChannel POSTs a channel and returns its ID.
func createTestChannel(t *testing.T, h *AlertChannelsHandler, body string) string {
	t.Helper()
	ctx := auditRequestCtx("POST", "/api/alerting/channels", []byte(body))
	h.createChannel(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode(), "body: %s", ctx.Response.Body())
	var resp struct {
		Channel struct {
			ID string `json:"id"`
		} `json:"channel"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	return resp.Channel.ID
}

func TestAlertChannels_CreateValidation(t *testing.T) {
	h, _ := newAlertChannelsHandler(t)
	cases := []struct {
		name string
		body string
	}{
		{"malformed json", `{nope`},
		{"missing name", `{"type": "slack", "endpoint_url": "https://hooks.slack.com/x"}`},
		{"unknown type", `{"name": "x", "type": "carrier-pigeon"}`},
		{"slack without url", `{"name": "x", "type": "slack"}`},
		{"webhook bad scheme", `{"name": "x", "type": "webhook", "endpoint_url": "ftp://example.com"}`},
		{"pagerduty without routing key", `{"name": "x", "type": "pagerduty"}`},
		{"unknown event type", `{"name": "x", "type": "slack", "endpoint_url": "https://hooks.slack.com/x", "event_types": ["nonsense.event"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := auditRequestCtx("POST", "/api/alerting/channels", []byte(tc.body))
			h.createChannel(ctx)
			assert.Equal(t, 400, ctx.Response.StatusCode())
		})
	}
}

func TestAlertChannels_CRUDAndSecretRedaction(t *testing.T) {
	h, store := newAlertChannelsHandler(t)

	id := createTestChannel(t, h,
		`{"name": "ops-pd", "type": "pagerduty", "secret": "routing-key-xyz", "event_types": ["budget.exceeded"]}`)

	// List: the secret never appears anywhere in the response body.
	ctx := auditRequestCtx("GET", "/api/alerting/channels", nil)
	h.listChannels(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode())
	assert.NotContains(t, string(ctx.Response.Body()), "routing-key-xyz")
	assert.Contains(t, string(ctx.Response.Body()), `"has_secret":true`)

	// Get by ID: same redaction.
	getCtx := auditRequestCtx("GET", "/api/alerting/channels/"+id, nil)
	getCtx.SetUserValue("id", id)
	h.getChannel(getCtx)
	require.Equal(t, 200, getCtx.Response.StatusCode())
	assert.NotContains(t, string(getCtx.Response.Body()), "routing-key-xyz")

	// Update WITHOUT a secret field: the stored secret is preserved.
	updCtx := auditRequestCtx("PUT", "/api/alerting/channels/"+id, []byte(`{"name": "ops-pd-renamed"}`))
	updCtx.SetUserValue("id", id)
	h.updateChannel(updCtx)
	require.Equal(t, 200, updCtx.Response.StatusCode(), "body: %s", updCtx.Response.Body())
	stored, err := store.GetAlertChannel(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ops-pd-renamed", stored.Name)
	assert.Equal(t, "routing-key-xyz", stored.Secret, "omitted secret must preserve the stored value")

	// Update WITH a secret replaces it.
	updCtx = auditRequestCtx("PUT", "/api/alerting/channels/"+id, []byte(`{"secret": "routing-key-2"}`))
	updCtx.SetUserValue("id", id)
	h.updateChannel(updCtx)
	require.Equal(t, 200, updCtx.Response.StatusCode())
	stored, err = store.GetAlertChannel(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "routing-key-2", stored.Secret)

	// Audit rows exist for every mutation.
	logs, _, err := store.GetAuditLogs(ctx, configstore.AuditLogsQueryParams{Target: id})
	require.NoError(t, err)
	actions := map[string]int{}
	for _, row := range logs {
		actions[row.Action]++
	}
	assert.Equal(t, 1, actions[AuditActionAlertChannelCreate])
	assert.Equal(t, 2, actions[AuditActionAlertChannelUpdate])

	// Delete, then 404 on re-delete.
	delCtx := auditRequestCtx("DELETE", "/api/alerting/channels/"+id, nil)
	delCtx.SetUserValue("id", id)
	h.deleteChannel(delCtx)
	require.Equal(t, 200, delCtx.Response.StatusCode())
	delCtx = auditRequestCtx("DELETE", "/api/alerting/channels/"+id, nil)
	delCtx.SetUserValue("id", id)
	h.deleteChannel(delCtx)
	assert.Equal(t, 404, delCtx.Response.StatusCode())
}

func TestAlertChannels_TestFire(t *testing.T) {
	h, store := newAlertChannelsHandler(t)

	var received atomic404OK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.hits++
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	id := createTestChannel(t, h, fmt.Sprintf(`{"name": "hook", "type": "webhook", "endpoint_url": %q}`, srv.URL))

	testCtx := auditRequestCtx("POST", "/api/alerting/channels/"+id+"/test", nil)
	testCtx.SetUserValue("id", id)
	h.testChannel(testCtx)
	require.Equal(t, 200, testCtx.Response.StatusCode())

	var resp struct {
		Status     string `json:"status"`
		HTTPStatus int    `json:"http_status"`
		Error      string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(testCtx.Response.Body(), &resp))
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, 200, resp.HTTPStatus)
	assert.Empty(t, resp.Error)
	assert.Equal(t, 1, received.hits)

	// The attempt is stamped on the channel row.
	stored, err := store.GetAlertChannel(testCtx, id)
	require.NoError(t, err)
	assert.Equal(t, "ok", stored.LastStatus)
	require.NotNil(t, stored.LastAttemptAt)

	// A failing destination reports failed without a handler error.
	srv.Close()
	testCtx = auditRequestCtx("POST", "/api/alerting/channels/"+id+"/test", nil)
	testCtx.SetUserValue("id", id)
	h.testChannel(testCtx)
	require.Equal(t, 200, testCtx.Response.StatusCode())
	require.NoError(t, json.Unmarshal(testCtx.Response.Body(), &resp))
	assert.Equal(t, "failed", resp.Status)
	assert.NotEmpty(t, resp.Error)
}

type atomic404OK struct{ hits int }
