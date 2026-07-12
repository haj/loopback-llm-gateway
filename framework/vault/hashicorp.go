package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// defaultMountPath is the standard KV v2 mount when hashicorp.mount_path is unset.
const defaultMountPath = "secret"

// httpTimeout bounds every Vault API call. SecretVar resolution runs
// synchronously inside config loads and DB scans, so this must stay short — a
// down Vault should fail fast, not stall startup.
const httpTimeout = 5 * time.Second

// hashiCorpBackend talks to HashiCorp Vault's KV v2 HTTP API with plain
// net/http (no vault SDK). Auth is a static token or AppRole login
// (role_id/secret_id) with automatic re-login on 403.
type hashiCorpBackend struct {
	address   string // e.g. https://vault.example.com (no trailing slash)
	namespace string
	mount     string
	client    *http.Client

	roleID   string
	secretID string

	mu    sync.Mutex // guards token
	token string
}

// newHashiCorpBackend builds the backend from the hashicorp config block.
func newHashiCorpBackend(cfg *HashiCorpConfig) (*hashiCorpBackend, error) {
	if cfg == nil {
		return nil, fmt.Errorf("vault: hashicorp config is required")
	}
	address := strings.TrimRight(strings.TrimSpace(cfg.Address.GetValue()), "/")
	if address == "" {
		return nil, fmt.Errorf("vault: hashicorp.address is required")
	}
	if _, err := url.Parse(address); err != nil {
		return nil, fmt.Errorf("vault: invalid hashicorp.address %q: %w", address, err)
	}
	mount := strings.Trim(strings.TrimSpace(cfg.MountPath.GetValue()), "/")
	if mount == "" {
		mount = defaultMountPath
	}
	b := &hashiCorpBackend{
		address:   address,
		namespace: strings.TrimSpace(cfg.Namespace.GetValue()),
		mount:     mount,
		client:    &http.Client{Timeout: httpTimeout},
		roleID:    cfg.RoleID.GetValue(),
		secretID:  cfg.SecretID.GetValue(),
		token:     cfg.Token.GetValue(),
	}
	if b.token == "" && (b.roleID == "" || b.secretID == "") {
		return nil, fmt.Errorf("vault: hashicorp requires either token or role_id + secret_id (AppRole)")
	}
	return b, nil
}

// Name implements Backend.
func (b *hashiCorpBackend) Name() string { return TypeHashiCorp }

// GetSecret implements Backend over GET /v1/<mount>/data/<path> (KV v2).
func (b *hashiCorpBackend) GetSecret(ctx context.Context, path string) (map[string]string, error) {
	status, body, err := b.do(ctx, http.MethodGet, b.dataURL(path), nil)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
	default:
		return nil, vaultAPIError("read", path, status, body)
	}
	// KV v2 read shape: {"data": {"data": {...}, "metadata": {...}}}
	var parsed struct {
		Data struct {
			Data map[string]json.RawMessage `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("vault: malformed KV v2 read response for %q: %w", path, err)
	}
	if parsed.Data.Data == nil {
		// Deleted-but-not-destroyed KV v2 versions return data: null.
		return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	out := make(map[string]string, len(parsed.Data.Data))
	for k, raw := range parsed.Data.Data {
		out[k] = rawToString(raw)
	}
	return out, nil
}

// PutSecret implements Backend over POST /v1/<mount>/data/<path> (KV v2).
func (b *hashiCorpBackend) PutSecret(ctx context.Context, path string, data map[string]string) error {
	payload := map[string]any{"data": data}
	status, body, err := b.do(ctx, http.MethodPost, b.dataURL(path), payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return vaultAPIError("write", path, status, body)
	}
	return nil
}

// DeleteSecret implements Backend over DELETE /v1/<mount>/metadata/<path>,
// which permanently removes all versions and metadata of the secret.
func (b *hashiCorpBackend) DeleteSecret(ctx context.Context, path string) error {
	status, body, err := b.do(ctx, http.MethodDelete, b.metadataURL(path), nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	default:
		return vaultAPIError("delete", path, status, body)
	}
}

// Ping validates connectivity and credentials via GET /v1/auth/token/lookup-self.
func (b *hashiCorpBackend) Ping(ctx context.Context) error {
	status, body, err := b.do(ctx, http.MethodGet, b.address+"/v1/auth/token/lookup-self", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return vaultAPIError("ping", "auth/token/lookup-self", status, body)
	}
	return nil
}

// ---- internals ----

// dataURL builds the KV v2 data endpoint for a logical secret path.
func (b *hashiCorpBackend) dataURL(path string) string {
	return b.address + "/v1/" + b.mount + "/data/" + escapePath(path)
}

// metadataURL builds the KV v2 metadata endpoint for a logical secret path.
func (b *hashiCorpBackend) metadataURL(path string) string {
	return b.address + "/v1/" + b.mount + "/metadata/" + escapePath(path)
}

// escapePath escapes each slash-separated segment of a logical secret path
// while preserving the slashes themselves.
func escapePath(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

// do performs one authenticated request. On 403 with AppRole credentials
// configured, it re-logs-in once and retries (expired/revoked token recovery).
func (b *hashiCorpBackend) do(ctx context.Context, method, rawURL string, payload any) (int, []byte, error) {
	if err := b.ensureToken(ctx); err != nil {
		return 0, nil, err
	}
	status, body, err := b.doOnce(ctx, method, rawURL, payload)
	if err != nil {
		return 0, nil, err
	}
	if status == http.StatusForbidden && b.roleID != "" && b.secretID != "" {
		if err := b.appRoleLogin(ctx); err != nil {
			return 0, nil, err
		}
		return b.doOnce(ctx, method, rawURL, payload)
	}
	return status, body, nil
}

// doOnce performs a single HTTP round-trip with the current token.
func (b *hashiCorpBackend) doOnce(ctx context.Context, method, rawURL string, payload any) (int, []byte, error) {
	var reqBody io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("vault: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reqBody)
	if err != nil {
		return 0, nil, fmt.Errorf("vault: build request: %w", err)
	}
	req.Header.Set("X-Vault-Request", "true")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.namespace != "" {
		req.Header.Set("X-Vault-Namespace", b.namespace)
	}
	b.mu.Lock()
	token := b.token
	b.mu.Unlock()
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("vault: request %s %s: %w", method, rawURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("vault: read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// ensureToken logs in via AppRole when no token is held yet.
func (b *hashiCorpBackend) ensureToken(ctx context.Context) error {
	b.mu.Lock()
	hasToken := b.token != ""
	b.mu.Unlock()
	if hasToken {
		return nil
	}
	return b.appRoleLogin(ctx)
}

// appRoleLogin exchanges role_id/secret_id for a client token via
// POST /v1/auth/approle/login and stores it for subsequent requests.
func (b *hashiCorpBackend) appRoleLogin(ctx context.Context) error {
	if b.roleID == "" || b.secretID == "" {
		return fmt.Errorf("vault: token rejected and no AppRole credentials configured")
	}
	payload := map[string]string{"role_id": b.roleID, "secret_id": b.secretID}
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("vault: marshal approle login: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.address+"/v1/auth/approle/login", bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("vault: build approle login: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Vault-Request", "true")
	if b.namespace != "" {
		req.Header.Set("X-Vault-Namespace", b.namespace)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("vault: approle login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("vault: read approle login response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return vaultAPIError("approle login", "auth/approle/login", resp.StatusCode, body)
	}
	var parsed struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("vault: malformed approle login response: %w", err)
	}
	if parsed.Auth.ClientToken == "" {
		return fmt.Errorf("vault: approle login returned no client token")
	}
	b.mu.Lock()
	b.token = parsed.Auth.ClientToken
	b.mu.Unlock()
	return nil
}

// rawToString renders a KV value as a string: JSON strings verbatim, everything
// else (numbers, bools, nested objects) as compact JSON.
func rawToString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(bytes.TrimSpace(raw))
}

// vaultAPIError shapes a non-2xx Vault response into an error, including the
// API's "errors" array when present.
func vaultAPIError(op, path string, status int, body []byte) error {
	var parsed struct {
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && len(parsed.Errors) > 0 {
		return fmt.Errorf("vault: %s %q: status %d: %s", op, path, status, strings.Join(parsed.Errors, "; "))
	}
	return fmt.Errorf("vault: %s %q: unexpected status %d", op, path, status)
}
