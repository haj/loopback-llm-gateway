package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

// mockVault is a minimal hermetic HashiCorp Vault KV v2 emulator: token auth,
// AppRole login, data/metadata endpoints, and KV v2 JSON response shapes.
type mockVault struct {
	t         *testing.T
	mount     string
	namespace string

	mu          sync.Mutex
	secrets     map[string]map[string]any
	validTokens map[string]bool
	roleID      string
	secretID    string
	logins      int
	lastHeaders http.Header
}

func newMockVault(t *testing.T) *mockVault {
	return &mockVault{
		t:           t,
		mount:       "secret",
		secrets:     map[string]map[string]any{},
		validTokens: map[string]bool{},
	}
}

func (m *mockVault) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(m.handle))
}

func (m *mockVault) handle(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastHeaders = r.Header.Clone()

	if r.URL.Path == "/v1/auth/approle/login" && r.Method == http.MethodPost {
		var body struct {
			RoleID   string `json:"role_id"`
			SecretID string `json:"secret_id"`
		}
		require.NoError(m.t, json.NewDecoder(r.Body).Decode(&body))
		if body.RoleID != m.roleID || body.SecretID != m.secretID {
			m.writeErrors(w, http.StatusBadRequest, "invalid role or secret ID")
			return
		}
		m.logins++
		token := fmt.Sprintf("approle-token-%d", m.logins)
		m.validTokens[token] = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"auth": map[string]any{"client_token": token},
		})
		return
	}

	// Everything else requires a valid token.
	if !m.validTokens[r.Header.Get("X-Vault-Token")] {
		m.writeErrors(w, http.StatusForbidden, "permission denied")
		return
	}

	if r.URL.Path == "/v1/auth/token/lookup-self" && r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "test"}})
		return
	}

	dataPrefix := "/v1/" + m.mount + "/data/"
	metaPrefix := "/v1/" + m.mount + "/metadata/"
	switch {
	case strings.HasPrefix(r.URL.Path, dataPrefix):
		path := strings.TrimPrefix(r.URL.Path, dataPrefix)
		switch r.Method {
		case http.MethodGet:
			data, ok := m.secrets[path]
			if !ok {
				m.writeErrors(w, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"data":     data,
					"metadata": map[string]any{"version": 1},
				},
			})
		case http.MethodPost, http.MethodPut:
			var body struct {
				Data map[string]any `json:"data"`
			}
			require.NoError(m.t, json.NewDecoder(r.Body).Decode(&body))
			m.secrets[path] = body.Data
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"version": 1},
			})
		default:
			m.writeErrors(w, http.StatusMethodNotAllowed)
		}
	case strings.HasPrefix(r.URL.Path, metaPrefix) && r.Method == http.MethodDelete:
		path := strings.TrimPrefix(r.URL.Path, metaPrefix)
		if _, ok := m.secrets[path]; !ok {
			m.writeErrors(w, http.StatusNotFound)
			return
		}
		delete(m.secrets, path)
		w.WriteHeader(http.StatusNoContent)
	default:
		m.writeErrors(w, http.StatusNotFound)
	}
}

func (m *mockVault) writeErrors(w http.ResponseWriter, status int, errs ...string) {
	w.WriteHeader(status)
	if len(errs) == 0 {
		errs = []string{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": errs})
}

func (m *mockVault) revokeAllTokens() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.validTokens = map[string]bool{}
}

func (m *mockVault) header(key string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastHeaders.Get(key)
}

func sv(value string) *schemas.SecretVar { return schemas.NewSecretVar(value) }

func tokenBackend(t *testing.T, mock *mockVault, url string, mutate func(*HashiCorpConfig)) *hashiCorpBackend {
	t.Helper()
	mock.mu.Lock()
	mock.validTokens["unit-test-token"] = true
	mock.mu.Unlock()
	cfg := &HashiCorpConfig{
		Address: sv(url),
		Token:   sv("unit-test-token"),
	}
	if mutate != nil {
		mutate(cfg)
	}
	b, err := newHashiCorpBackend(cfg)
	require.NoError(t, err)
	return b
}

func TestHashiCorpReadWriteDeleteRoundTrip(t *testing.T) {
	mock := newMockVault(t)
	srv := mock.server()
	defer srv.Close()
	b := tokenBackend(t, mock, srv.URL, nil)
	ctx := context.Background()

	require.NoError(t, b.PutSecret(ctx, "bifrost/config_keys/key-1/value", map[string]string{"value": "sk-live-1"}))

	data, err := b.GetSecret(ctx, "bifrost/config_keys/key-1/value")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"value": "sk-live-1"}, data)
	require.Equal(t, "unit-test-token", mock.header("X-Vault-Token"))
	require.Equal(t, "true", mock.header("X-Vault-Request"))

	require.NoError(t, b.DeleteSecret(ctx, "bifrost/config_keys/key-1/value"))
	_, err = b.GetSecret(ctx, "bifrost/config_keys/key-1/value")
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, b.DeleteSecret(ctx, "bifrost/config_keys/key-1/value"), ErrNotFound)
}

func TestHashiCorpNamespaceHeader(t *testing.T) {
	mock := newMockVault(t)
	srv := mock.server()
	defer srv.Close()
	b := tokenBackend(t, mock, srv.URL, func(c *HashiCorpConfig) { c.Namespace = sv("team-a") })

	require.NoError(t, b.PutSecret(context.Background(), "x/y", map[string]string{"value": "v"}))
	require.Equal(t, "team-a", mock.header("X-Vault-Namespace"))
}

func TestHashiCorpNonStringValuesAreStringified(t *testing.T) {
	mock := newMockVault(t)
	srv := mock.server()
	defer srv.Close()
	mock.mu.Lock()
	mock.secrets["typed/secret"] = map[string]any{"value": "plain", "port": 5432, "tls": true}
	mock.mu.Unlock()
	b := tokenBackend(t, mock, srv.URL, nil)

	data, err := b.GetSecret(context.Background(), "typed/secret")
	require.NoError(t, err)
	require.Equal(t, "plain", data["value"])
	require.Equal(t, "5432", data["port"])
	require.Equal(t, "true", data["tls"])
}

func TestHashiCorpCustomMountPath(t *testing.T) {
	mock := newMockVault(t)
	mock.mount = "kv-apps"
	srv := mock.server()
	defer srv.Close()
	b := tokenBackend(t, mock, srv.URL, func(c *HashiCorpConfig) { c.MountPath = sv("kv-apps") })
	ctx := context.Background()

	require.NoError(t, b.PutSecret(ctx, "a/b", map[string]string{"value": "v"}))
	data, err := b.GetSecret(ctx, "a/b")
	require.NoError(t, err)
	require.Equal(t, "v", data["value"])
}

func TestHashiCorpPing(t *testing.T) {
	mock := newMockVault(t)
	srv := mock.server()
	defer srv.Close()
	b := tokenBackend(t, mock, srv.URL, nil)
	require.NoError(t, b.Ping(context.Background()))

	bad := tokenBackend(t, mock, srv.URL, func(c *HashiCorpConfig) { c.Token = sv("wrong-token") })
	require.Error(t, bad.Ping(context.Background()))
}

func TestHashiCorpAppRoleLoginAndReLoginOn403(t *testing.T) {
	mock := newMockVault(t)
	mock.roleID = "role-123"
	mock.secretID = "secret-456"
	srv := mock.server()
	defer srv.Close()

	cfg := &HashiCorpConfig{
		Address:  sv(srv.URL),
		RoleID:   sv("role-123"),
		SecretID: sv("secret-456"),
	}
	b, err := newHashiCorpBackend(cfg)
	require.NoError(t, err)
	ctx := context.Background()

	// First call triggers an AppRole login.
	require.NoError(t, b.PutSecret(ctx, "a/b", map[string]string{"value": "v1"}))
	mock.mu.Lock()
	require.Equal(t, 1, mock.logins)
	mock.mu.Unlock()

	// Revoke the token server-side: the next call must re-login and succeed.
	mock.revokeAllTokens()
	data, err := b.GetSecret(ctx, "a/b")
	require.NoError(t, err)
	require.Equal(t, "v1", data["value"])
	mock.mu.Lock()
	require.Equal(t, 2, mock.logins, "expected exactly one re-login after 403")
	mock.mu.Unlock()
}

func TestHashiCorpConfigValidation(t *testing.T) {
	_, err := newHashiCorpBackend(nil)
	require.Error(t, err)

	_, err = newHashiCorpBackend(&HashiCorpConfig{})
	require.ErrorContains(t, err, "address")

	_, err = newHashiCorpBackend(&HashiCorpConfig{Address: sv("http://127.0.0.1:8200")})
	require.ErrorContains(t, err, "token or role_id")

	b, err := newHashiCorpBackend(&HashiCorpConfig{
		Address: sv("http://127.0.0.1:8200/"),
		Token:   sv("tok"),
	})
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:8200", b.address)
	require.Equal(t, "secret", b.mount)
	require.Equal(t, TypeHashiCorp, b.Name())
}

func TestNewRegistryEndToEndOverMockVault(t *testing.T) {
	mock := newMockVault(t)
	mock.mu.Lock()
	mock.validTokens["e2e-token"] = true
	mock.secrets["bifrost/e2e"] = map[string]any{"value": "resolved-plaintext"}
	mock.mu.Unlock()
	srv := mock.server()
	defer srv.Close()

	cfg := &Config{
		Enabled:    true,
		Type:       TypeHashiCorp,
		AccessMode: AccessModeReadAndWrite,
		HashiCorp: &HashiCorpConfig{
			Address: sv(srv.URL),
			Token:   sv("e2e-token"),
		},
	}
	reg, err := NewRegistry(cfg, nil)
	require.NoError(t, err)
	require.Equal(t, TypeHashiCorp, reg.BackendName())

	v := "vault.bifrost/e2e"
	require.NoError(t, reg.ResolveString(context.Background(), &v))
	require.Equal(t, "resolved-plaintext", v)

	stored := "new-secret"
	require.NoError(t, reg.StoreString(context.Background(), "bifrost/e2e-2", &stored))
	require.Equal(t, "vault.bifrost/e2e-2", stored)
	require.NoError(t, reg.Ping(context.Background()))
}
