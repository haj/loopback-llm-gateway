package alerting

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testEvent() Event {
	return Event{
		Type:      EventTypeBudgetExceeded,
		Severity:  SeverityWarning,
		Title:     "Budget exceeded",
		Message:   "Virtual key vk-1 exceeded its monthly budget",
		Provider:  "openai",
		DedupKey:  "budget.exceeded|vk:vk-1",
		Fields:    map[string]string{"virtual_key": "vk-1"},
		Timestamp: time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
	}
}

// captureServer records the last request body and headers.
type capture struct {
	body    []byte
	headers http.Header
	hits    atomic.Int32
}

func captureServer(t *testing.T, status int, c *capture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		c.body = body
		c.headers = r.Header.Clone()
		c.hits.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSlackSender_PayloadShape(t *testing.T) {
	var c capture
	srv := captureServer(t, 200, &c)
	channel := &configstoreTables.TableAlertChannel{
		ID: "ch-slack", Type: configstoreTables.AlertChannelTypeSlack, EndpointURL: srv.URL,
	}

	status, err := SendTest(channel, testEvent())
	require.NoError(t, err)
	assert.Equal(t, 200, status)

	var payload map[string]string
	require.NoError(t, json.Unmarshal(c.body, &payload))
	assert.Contains(t, payload["text"], "*Budget exceeded*")
	assert.Contains(t, payload["text"], "vk-1")
	assert.Equal(t, "application/json", c.headers.Get("Content-Type"))
}

func TestPagerDutySender_EventsV2Shape(t *testing.T) {
	var c capture
	srv := captureServer(t, 202, &c)
	channel := &configstoreTables.TableAlertChannel{
		ID:          "ch-pd",
		Type:        configstoreTables.AlertChannelTypePagerDuty,
		EndpointURL: srv.URL, // base-URL override: hermetic test never touches pagerduty.com
		Secret:      "routing-key-123",
	}

	status, err := SendTest(channel, testEvent())
	require.NoError(t, err)
	assert.Equal(t, 202, status)

	var payload struct {
		RoutingKey  string `json:"routing_key"`
		EventAction string `json:"event_action"`
		DedupKey    string `json:"dedup_key"`
		Payload     struct {
			Summary       string            `json:"summary"`
			Severity      string            `json:"severity"`
			Source        string            `json:"source"`
			CustomDetails map[string]string `json:"custom_details"`
		} `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(c.body, &payload))
	assert.Equal(t, "routing-key-123", payload.RoutingKey)
	assert.Equal(t, "trigger", payload.EventAction)
	assert.Equal(t, "budget.exceeded|vk:vk-1", payload.DedupKey)
	assert.Equal(t, "Budget exceeded", payload.Payload.Summary)
	assert.Equal(t, "warning", payload.Payload.Severity)
	assert.Equal(t, "loopback-gateway", payload.Payload.Source)
	assert.Equal(t, "vk-1", payload.Payload.CustomDetails["virtual_key"])
}

func TestWebhookSender_SignsBodyWithSecret(t *testing.T) {
	var c capture
	srv := captureServer(t, 200, &c)
	channel := &configstoreTables.TableAlertChannel{
		ID: "ch-hook", Type: configstoreTables.AlertChannelTypeWebhook, EndpointURL: srv.URL, Secret: "signing-key",
	}

	_, err := SendTest(channel, testEvent())
	require.NoError(t, err)

	// The body is the Event itself.
	var got Event
	require.NoError(t, json.Unmarshal(c.body, &got))
	assert.Equal(t, EventTypeBudgetExceeded, got.Type)

	// The signature verifies against the exact bytes received.
	mac := hmac.New(sha256.New, []byte("signing-key"))
	mac.Write(c.body)
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), c.headers.Get(webhookSignatureHeader))
}

func TestWebhookSender_NoSignatureWithoutSecret(t *testing.T) {
	var c capture
	srv := captureServer(t, 200, &c)
	channel := &configstoreTables.TableAlertChannel{
		ID: "ch-hook", Type: configstoreTables.AlertChannelTypeWebhook, EndpointURL: srv.URL,
	}
	_, err := SendTest(channel, testEvent())
	require.NoError(t, err)
	assert.Empty(t, c.headers.Get(webhookSignatureHeader))
}

func TestDeliver_RetriesTransientThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	channel := &configstoreTables.TableAlertChannel{
		ID: "ch", Type: configstoreTables.AlertChannelTypeWebhook, EndpointURL: srv.URL,
	}

	var slept []time.Duration
	status, err := deliver(srv.Client(), channel, testEvent(), func(d time.Duration) { slept = append(slept, d) })
	require.NoError(t, err)
	assert.Equal(t, 200, status)
	assert.Equal(t, int32(3), hits.Load())
	// Backoffs follow 1s/4s bases (plus jitter <= 25%).
	require.Len(t, slept, 2)
	assert.GreaterOrEqual(t, slept[0], time.Second)
	assert.Less(t, slept[0], 1300*time.Millisecond)
	assert.GreaterOrEqual(t, slept[1], 4*time.Second)
	assert.Less(t, slept[1], 5200*time.Millisecond)
}

func TestDeliver_PermanentFailureNotRetried(t *testing.T) {
	var c capture
	srv := captureServer(t, 400, &c)
	channel := &configstoreTables.TableAlertChannel{
		ID: "ch", Type: configstoreTables.AlertChannelTypeWebhook, EndpointURL: srv.URL,
	}
	status, err := deliver(srv.Client(), channel, testEvent(), func(time.Duration) {})
	assert.Error(t, err)
	assert.Equal(t, 400, status)
	assert.Equal(t, int32(1), c.hits.Load(), "4xx (non-429) must not be retried")
}

func TestDeliver_RetriesRateLimitUntilBudgetExhausted(t *testing.T) {
	var c capture
	srv := captureServer(t, 429, &c)
	channel := &configstoreTables.TableAlertChannel{
		ID: "ch", Type: configstoreTables.AlertChannelTypeWebhook, EndpointURL: srv.URL,
	}
	status, err := deliver(srv.Client(), channel, testEvent(), func(time.Duration) {})
	assert.Error(t, err)
	assert.Equal(t, 429, status)
	assert.Equal(t, int32(senderMaxAttempts), c.hits.Load(), "429 must be retried up to the attempt budget")
}

func TestValidateEndpointURL(t *testing.T) {
	assert.NoError(t, ValidateEndpointURL("https://hooks.slack.com/services/x"))
	assert.NoError(t, ValidateEndpointURL("http://localhost:9999/hook"))
	assert.Error(t, ValidateEndpointURL(""))
	assert.Error(t, ValidateEndpointURL("ftp://example.com/x"))
	assert.Error(t, ValidateEndpointURL("not a url"))
	assert.Error(t, ValidateEndpointURL("/relative/path"))
}
