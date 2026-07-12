package vault

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeBackend is an in-memory Backend that counts calls.
type fakeBackend struct {
	mu      sync.Mutex
	secrets map[string]map[string]string
	gets    int
	puts    int
	deletes int
	pingErr error
	getErr  error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{secrets: map[string]map[string]string{}}
}

func (f *fakeBackend) Name() string { return "fake" }

func (f *fakeBackend) GetSecret(_ context.Context, path string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.getErr != nil {
		return nil, f.getErr
	}
	data, ok := f.secrets[path]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	out := make(map[string]string, len(data))
	for k, v := range data {
		out[k] = v
	}
	return out, nil
}

func (f *fakeBackend) PutSecret(_ context.Context, path string, data map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	cp := make(map[string]string, len(data))
	for k, v := range data {
		cp[k] = v
	}
	f.secrets[path] = cp
	return nil
}

func (f *fakeBackend) DeleteSecret(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	if _, ok := f.secrets[path]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	delete(f.secrets, path)
	return nil
}

func (f *fakeBackend) Ping(_ context.Context) error { return f.pingErr }

func (f *fakeBackend) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

func newTestRegistry(t *testing.T, backend Backend, mutate func(*Config)) *Registry {
	t.Helper()
	cfg := &Config{
		Enabled:    true,
		Type:       TypeHashiCorp,
		AccessMode: AccessModeReadAndWrite,
	}
	if mutate != nil {
		mutate(cfg)
	}
	reg, err := NewRegistryWithBackend(cfg, backend, nil)
	require.NoError(t, err)
	return reg
}

func TestResolveStringNonVaultValuesAreNoOps(t *testing.T) {
	reg := newTestRegistry(t, newFakeBackend(), nil)
	for _, in := range []string{"", "plain-secret", "env.MY_VAR"} {
		v := in
		require.NoError(t, reg.ResolveString(context.Background(), &v))
		require.Equal(t, in, v, "non-vault value must be untouched")
	}
	require.NoError(t, reg.ResolveString(context.Background(), nil))
}

func TestResolveStringDefaultValueKey(t *testing.T) {
	backend := newFakeBackend()
	backend.secrets["app/openai"] = map[string]string{"value": "sk-live-123"}
	reg := newTestRegistry(t, backend, nil)

	v := "vault.app/openai"
	require.NoError(t, reg.ResolveString(context.Background(), &v))
	require.Equal(t, "sk-live-123", v)
}

func TestResolveStringFragmentKey(t *testing.T) {
	backend := newFakeBackend()
	backend.secrets["shared/creds"] = map[string]string{"value": "default", "api_key": "fragment-secret"}
	reg := newTestRegistry(t, backend, nil)

	v := "vault.shared/creds#api_key"
	require.NoError(t, reg.ResolveString(context.Background(), &v))
	require.Equal(t, "fragment-secret", v)

	missing := "vault.shared/creds#nope"
	err := reg.ResolveString(context.Background(), &missing)
	require.Error(t, err)
	require.Contains(t, err.Error(), `key "nope"`)
	require.Equal(t, "vault.shared/creds#nope", missing, "value must be untouched on error")
}

func TestResolveStringNotFound(t *testing.T) {
	reg := newTestRegistry(t, newFakeBackend(), nil)
	v := "vault.missing/path"
	err := reg.ResolveString(context.Background(), &v)
	require.ErrorIs(t, err, ErrNotFound)
	require.Equal(t, "vault.missing/path", v)
}

func TestResolveStringCacheHitAndExpiry(t *testing.T) {
	backend := newFakeBackend()
	backend.secrets["p/s"] = map[string]string{"value": "one"}
	reg := newTestRegistry(t, backend, func(c *Config) { c.CacheTTL = "1h" })

	for range 3 {
		v := "vault.p/s"
		require.NoError(t, reg.ResolveString(context.Background(), &v))
		require.Equal(t, "one", v)
	}
	require.Equal(t, 1, backend.getCount(), "cached resolves must not hit the backend")

	// Expire by swapping in a tiny-TTL registry sharing the backend.
	shortReg := newTestRegistry(t, backend, func(c *Config) { c.CacheTTL = "1ns" })
	v := "vault.p/s"
	require.NoError(t, shortReg.ResolveString(context.Background(), &v))
	time.Sleep(2 * time.Millisecond)
	v = "vault.p/s"
	require.NoError(t, shortReg.ResolveString(context.Background(), &v))
	require.Equal(t, 3, backend.getCount(), "expired entries must re-hit the backend")
}

func TestResolveStringCacheDisabled(t *testing.T) {
	backend := newFakeBackend()
	backend.secrets["p/s"] = map[string]string{"value": "one"}
	reg := newTestRegistry(t, backend, func(c *Config) { c.CacheTTL = "0s" })

	for range 2 {
		v := "vault.p/s"
		require.NoError(t, reg.ResolveString(context.Background(), &v))
	}
	require.Equal(t, 2, backend.getCount(), "ttl=0 must disable caching")
}

func TestInvalidateCacheForcesReRead(t *testing.T) {
	backend := newFakeBackend()
	backend.secrets["p/s"] = map[string]string{"value": "one"}
	reg := newTestRegistry(t, backend, func(c *Config) { c.CacheTTL = "1h" })

	v := "vault.p/s"
	require.NoError(t, reg.ResolveString(context.Background(), &v))
	require.Equal(t, "one", v)

	// Rotate the secret behind the cache.
	backend.secrets["p/s"] = map[string]string{"value": "two"}
	v = "vault.p/s"
	require.NoError(t, reg.ResolveString(context.Background(), &v))
	require.Equal(t, "one", v, "cached value expected before invalidation")

	reg.InvalidateCache()
	v = "vault.p/s"
	require.NoError(t, reg.ResolveString(context.Background(), &v))
	require.Equal(t, "two", v, "invalidation must surface the rotated value")
}

func TestStoreStringRewritesToVaultRef(t *testing.T) {
	backend := newFakeBackend()
	reg := newTestRegistry(t, backend, nil)

	v := "plaintext-secret"
	require.NoError(t, reg.StoreString(context.Background(), "bifrost/config_keys/key-1/value", &v))
	require.Equal(t, "vault.bifrost/config_keys/key-1/value", v)
	require.Equal(t, map[string]string{"value": "plaintext-secret"}, backend.secrets["bifrost/config_keys/key-1/value"])

	// The stored secret must resolve back to the plaintext.
	ref := "vault.bifrost/config_keys/key-1/value"
	require.NoError(t, reg.ResolveString(context.Background(), &ref))
	require.Equal(t, "plaintext-secret", ref)
}

func TestReadOnlyModeRejectsWrites(t *testing.T) {
	backend := newFakeBackend()
	backend.secrets["ro/path"] = map[string]string{"value": "x"}
	reg := newTestRegistry(t, backend, func(c *Config) { c.AccessMode = AccessModeReadOnly })

	v := "secret"
	require.ErrorIs(t, reg.StoreString(context.Background(), "some/path", &v), ErrWriteDisabled)
	require.Equal(t, "secret", v, "value must not be rewritten on refused store")
	require.ErrorIs(t, reg.Remove(context.Background(), "ro/path"), ErrWriteDisabled)

	// Reads still work in read_only mode.
	ref := "vault.ro/path"
	require.NoError(t, reg.ResolveString(context.Background(), &ref))
	require.Equal(t, "x", ref)
}

func TestRemoveToleratesMissingSecret(t *testing.T) {
	backend := newFakeBackend()
	backend.secrets["a/b"] = map[string]string{"value": "x"}
	reg := newTestRegistry(t, backend, nil)

	require.NoError(t, reg.Remove(context.Background(), "a/b"))
	require.NotContains(t, backend.secrets, "a/b")
	require.NoError(t, reg.Remove(context.Background(), "a/b"), "missing secret is not an error")
}

func TestRegistryStatus(t *testing.T) {
	backend := newFakeBackend()
	backend.secrets["p/s"] = map[string]string{"value": "one"}
	reg := newTestRegistry(t, backend, func(c *Config) { c.Prefix = "custom"; c.CacheTTL = "10m" })

	require.Equal(t, "custom", reg.Prefix())

	v := "vault.p/s"
	require.NoError(t, reg.ResolveString(context.Background(), &v))
	st := reg.Status()
	require.Equal(t, "fake", st.Backend)
	require.Equal(t, "custom", st.Prefix)
	require.True(t, st.WriteEnabled)
	require.Equal(t, 1, st.CacheEntries)
	require.Equal(t, 10*time.Minute, st.CacheTTL)
	require.Empty(t, st.LastError)

	// A failed resolve surfaces in the status snapshot.
	bad := "vault.missing/one"
	require.Error(t, reg.ResolveString(context.Background(), &bad))
	st = reg.Status()
	require.Contains(t, st.LastError, "missing/one")
	require.NotNil(t, st.LastErrorAt)
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	var nilCfg *Config
	require.Equal(t, DefaultPrefix, nilCfg.EffectivePrefix())
	require.Equal(t, AccessModeReadOnly, nilCfg.EffectiveAccessMode())
	require.False(t, nilCfg.WriteEnabled())
	require.Equal(t, DefaultCacheTTL, nilCfg.EffectiveCacheTTL())
	require.Equal(t, time.Duration(0), nilCfg.EffectiveSyncInterval())
	require.NoError(t, nilCfg.Validate())

	cfg := &Config{Enabled: true, Type: "aws-secrets-manager"}
	require.ErrorContains(t, cfg.Validate(), "not implemented")

	cfg = &Config{Enabled: true, Type: "bogus"}
	require.ErrorContains(t, cfg.Validate(), "unknown backend type")

	cfg = &Config{Enabled: true, Type: TypeHashiCorp}
	require.ErrorContains(t, cfg.Validate(), "hashicorp config block")

	cfg = &Config{Enabled: true, Type: TypeHashiCorp, AccessMode: "sideways"}
	require.ErrorContains(t, cfg.Validate(), "access_mode")

	cfg = &Config{Enabled: true, Type: TypeHashiCorp, SyncInterval: "soon", HashiCorp: &HashiCorpConfig{}}
	require.ErrorContains(t, cfg.Validate(), "sync_interval")

	cfg = &Config{Enabled: true, Type: TypeHashiCorp, SyncInterval: "5m", CacheTTL: "1m"}
	require.Equal(t, 5*time.Minute, cfg.EffectiveSyncInterval())
	require.Equal(t, time.Minute, cfg.EffectiveCacheTTL())
}
