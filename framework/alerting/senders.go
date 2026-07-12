// This file contains the per-destination senders (Slack incoming webhook,
// PagerDuty Events v2, generic signed webhook) and the shared retry policy.
// Senders run only on the dispatcher's worker goroutine (or a handler's
// synchronous test-fire) — never on the request hot path — so plain net/http
// with contexts is appropriate here, unlike the fasthttp provider clients.
package alerting

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// defaultPagerDutyURL is the public PagerDuty Events v2 enqueue endpoint. A
// channel's EndpointURL overrides it (hermetic tests point it at httptest).
const defaultPagerDutyURL = "https://events.pagerduty.com/v2/enqueue"

// webhookSignatureHeader carries the hex HMAC-SHA256 of the request body,
// computed with the channel Secret, so receivers can authenticate generic
// webhook deliveries (mirrors the audit-log HMAC pattern).
const webhookSignatureHeader = "X-Loopback-Signature"

// Retry policy: 4 attempts total with 1s/4s/16s backoff plus jitter. Only
// transient failures (429, 5xx, network errors) are retried; a 4xx other than
// 429 is a configuration problem that retrying cannot fix.
const (
	senderMaxAttempts = 4
	senderBaseBackoff = time.Second
	senderTimeout     = 10 * time.Second
)

// ValidateEndpointURL rejects endpoint URLs that are not absolute http/https
// URLs with a host. EndpointURL is admin-supplied (RBAC-gated), but the
// scheme/host check keeps obviously malformed or non-HTTP targets out.
func ValidateEndpointURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("endpoint URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("endpoint URL must include a host")
	}
	return nil
}

// buildRequest constructs the destination-specific HTTP request for one event.
// Errors here are permanent (bad channel configuration), never retried.
func buildRequest(ctx context.Context, channel *configstoreTables.TableAlertChannel, event Event) (*http.Request, error) {
	switch channel.Type {
	case configstoreTables.AlertChannelTypeSlack:
		return buildSlackRequest(ctx, channel, event)
	case configstoreTables.AlertChannelTypePagerDuty:
		return buildPagerDutyRequest(ctx, channel, event)
	case configstoreTables.AlertChannelTypeWebhook:
		return buildWebhookRequest(ctx, channel, event)
	default:
		return nil, fmt.Errorf("unsupported alert channel type %q", channel.Type)
	}
}

// buildSlackRequest posts a Slack incoming-webhook message: headline plus a
// fields block rendered as simple lines (no Slack SDK dependency).
func buildSlackRequest(ctx context.Context, channel *configstoreTables.TableAlertChannel, event Event) (*http.Request, error) {
	text := fmt.Sprintf("*%s*\n%s", event.Title, event.Message)
	for k, v := range event.Fields {
		text += fmt.Sprintf("\n• %s: %s", k, v)
	}
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal slack payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, channel.EndpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// pagerDutySeverity maps event severities onto PagerDuty's accepted set
// (critical|error|warning|info).
func pagerDutySeverity(severity string) string {
	switch severity {
	case SeverityCritical:
		return "critical"
	case SeverityWarning:
		return "warning"
	default:
		return "info"
	}
}

// buildPagerDutyRequest posts a PagerDuty Events v2 trigger. The channel
// Secret is the integration routing key; EndpointURL overrides the public
// enqueue URL (used by hermetic tests).
func buildPagerDutyRequest(ctx context.Context, channel *configstoreTables.TableAlertChannel, event Event) (*http.Request, error) {
	endpoint := channel.EndpointURL
	if endpoint == "" {
		endpoint = defaultPagerDutyURL
	}
	details := map[string]string{"message": event.Message}
	for k, v := range event.Fields {
		details[k] = v
	}
	payload := map[string]any{
		"routing_key":  channel.Secret,
		"event_action": "trigger",
		"dedup_key":    event.DedupKey,
		"payload": map[string]any{
			"summary":        event.Title,
			"severity":       pagerDutySeverity(event.Severity),
			"source":         "loopback-gateway",
			"component":      event.Provider,
			"timestamp":      event.Timestamp.UTC().Format(time.RFC3339),
			"custom_details": details,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pagerduty payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// buildWebhookRequest posts the Event itself as JSON. When the channel has a
// Secret, the body is signed with HMAC-SHA256 and the hex digest is sent in
// X-Loopback-Signature so receivers can verify authenticity offline.
func buildWebhookRequest(ctx context.Context, channel *configstoreTables.TableAlertChannel, event Event) (*http.Request, error) {
	body, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal webhook payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, channel.EndpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if channel.Secret != "" {
		mac := hmac.New(sha256.New, []byte(channel.Secret))
		mac.Write(body)
		req.Header.Set(webhookSignatureHeader, hex.EncodeToString(mac.Sum(nil)))
	}
	return req, nil
}

// retryableStatus reports whether an HTTP status is worth retrying: 429 and
// 5xx are transient; other 4xx are permanent configuration errors.
func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// sendOnce performs a single delivery attempt and returns the HTTP status (0
// on network error).
func sendOnce(client *http.Client, channel *configstoreTables.TableAlertChannel, event Event) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), senderTimeout)
	defer cancel()
	req, err := buildRequest(ctx, channel, event)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("alert delivery returned HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// SendTest performs one synchronous delivery attempt (no retries) for the
// handler's test-fire endpoint. Returns the HTTP status (0 on network error).
func SendTest(channel *configstoreTables.TableAlertChannel, event Event) (int, error) {
	client := &http.Client{Timeout: senderTimeout}
	return sendOnce(client, channel, event)
}

// deliver sends with the retry policy. sleep is injectable so tests assert
// backoff without waiting. Returns the final HTTP status and error.
func deliver(client *http.Client, channel *configstoreTables.TableAlertChannel, event Event, sleep func(time.Duration)) (int, error) {
	var (
		status int
		err    error
	)
	for attempt := 0; attempt < senderMaxAttempts; attempt++ {
		if attempt > 0 {
			// 1s, 4s, 16s plus up to 25% jitter.
			backoff := senderBaseBackoff << (2 * (attempt - 1))
			backoff += time.Duration(rand.Int63n(int64(backoff)/4 + 1))
			sleep(backoff)
		}
		status, err = sendOnce(client, channel, event)
		if err == nil {
			return status, nil
		}
		// Permanent failures: request-construction errors and non-retryable
		// HTTP statuses.
		if status != 0 && !retryableStatus(status) {
			return status, err
		}
	}
	return status, err
}
