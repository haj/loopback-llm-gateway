package vault

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"golang.org/x/sync/singleflight"
)

// ErrNotFound is returned by backends when the secret at a path does not exist.
var ErrNotFound = errors.New("vault: secret not found")

// ErrWriteDisabled is returned by write operations when access_mode is read_only.
var ErrWriteDisabled = errors.New("vault: write access disabled (access_mode is read_only)")

// defaultSecretKey is the JSON key used for non-fragment references: a ref
// "vault.path/to/secret" resolves data["value"], while "vault.path#api_key"
// resolves data["api_key"]. StoreString symmetrically writes {"value": <secret>}.
const defaultSecretKey = "value"

// Backend abstracts a secret-manager over flat string maps so HashiCorp Vault
// KV v2, AWS Secrets Manager, GCP Secret Manager, etc. are interchangeable.
// Paths are logical, slash-separated and exclude any backend mount prefix.
type Backend interface {
	// Name returns the backend type identifier (e.g. "hashicorp-vault").
	Name() string
	// GetSecret reads the secret at path. Returns ErrNotFound (possibly
	// wrapped) when the path does not exist.
	GetSecret(ctx context.Context, path string) (map[string]string, error)
	// PutSecret creates or overwrites the secret at path.
	PutSecret(ctx context.Context, path string, data map[string]string) error
	// DeleteSecret permanently removes the secret at path. Returns ErrNotFound
	// (possibly wrapped) when the path does not exist.
	DeleteSecret(ctx context.Context, path string) error
	// Ping verifies connectivity and credentials.
	Ping(ctx context.Context) error
}

// cacheEntry is an immutable resolved secret with an expiry.
type cacheEntry struct {
	data    map[string]string
	expires time.Time
}

// Registry resolves and stores vault references through a Backend, with a TTL
// read cache and singleflight de-duplication (SecretVar resolution runs
// synchronously inside config loads and DB row scans, so a cold or slow
// backend must not be hit once per field). Its methods match the
// schemas.Vault*Hook signatures so they can be wired directly.
type Registry struct {
	backend      Backend
	prefix       string
	writeEnabled bool
	ttl          time.Duration
	logger       schemas.Logger

	group singleflight.Group
	cache sync.Map // path (string) -> cacheEntry

	mu          sync.Mutex
	lastError   string
	lastErrorAt time.Time
}

// Status is a point-in-time snapshot of the registry for the status endpoint.
type Status struct {
	Backend      string        `json:"backend"`
	Prefix       string        `json:"prefix"`
	WriteEnabled bool          `json:"write_enabled"`
	CacheEntries int           `json:"cache_entries"`
	CacheTTL     time.Duration `json:"cache_ttl"`
	LastError    string        `json:"last_error,omitempty"`
	LastErrorAt  *time.Time    `json:"last_error_at,omitempty"`
}

// NewRegistry validates cfg, constructs the configured backend, and returns a
// ready registry. logger may be nil.
func NewRegistry(cfg *Config, logger schemas.Logger) (*Registry, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, errors.New("vault: config is nil or disabled")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var backend Backend
	switch cfg.Type {
	case TypeHashiCorp:
		b, err := newHashiCorpBackend(cfg.HashiCorp)
		if err != nil {
			return nil, err
		}
		backend = b
	default:
		// Validate already rejects everything else; keep a guard for safety.
		return nil, fmt.Errorf("vault: backend type %q is not implemented", cfg.Type)
	}
	return NewRegistryWithBackend(cfg, backend, logger)
}

// NewRegistryWithBackend builds a registry over an explicit backend. Used by
// tests and by future backend implementations living outside this package.
func NewRegistryWithBackend(cfg *Config, backend Backend, logger schemas.Logger) (*Registry, error) {
	if backend == nil {
		return nil, errors.New("vault: backend is required")
	}
	return &Registry{
		backend:      backend,
		prefix:       cfg.EffectivePrefix(),
		writeEnabled: cfg.WriteEnabled(),
		ttl:          cfg.EffectiveCacheTTL(),
		logger:       logger,
	}, nil
}

// Prefix returns the configured vault path prefix. Wired to schemas.VaultPrefixHook.
func (r *Registry) Prefix() string { return r.prefix }

// WriteEnabled reports whether store/remove operations are permitted.
func (r *Registry) WriteEnabled() bool { return r.writeEnabled }

// splitRef splits a bare reference (no "vault." prefix) into its secret path
// and JSON key. "path/to/secret#api_key" -> ("path/to/secret", "api_key");
// without a fragment the key defaults to "value".
func splitRef(ref string) (path, key string) {
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, defaultSecretKey
}

// ResolveString resolves a "vault.<path>[#jsonKey]" reference in place,
// replacing *value with the secret. Non-vault values (and nil) are left
// untouched and return nil, matching the schemas.VaultResolveHook contract.
func (r *Registry) ResolveString(ctx context.Context, value *string) error {
	if r == nil || value == nil {
		return nil
	}
	ref, ok := strings.CutPrefix(*value, "vault.")
	if !ok {
		return nil
	}
	path, key := splitRef(ref)
	if path == "" {
		return r.fail(fmt.Errorf("vault: empty secret path in reference %q", *value))
	}
	data, err := r.getData(ctx, path)
	if err != nil {
		return r.fail(fmt.Errorf("vault: resolve %q: %w", path, err))
	}
	secret, ok := data[key]
	if !ok {
		return r.fail(fmt.Errorf("vault: resolve %q: key %q not present in secret", path, key))
	}
	*value = secret
	return nil
}

// StoreString stores the plaintext *value at path (as {"value": <secret>}) and
// rewrites *value to the canonical "vault.<path>" reference, matching the
// schemas.VaultStoreHook contract.
func (r *Registry) StoreString(ctx context.Context, path string, value *string) error {
	if r == nil || value == nil {
		return nil
	}
	if !r.writeEnabled {
		return ErrWriteDisabled
	}
	path = strings.TrimPrefix(path, "vault.")
	if path == "" {
		return r.fail(errors.New("vault: store: empty secret path"))
	}
	data := map[string]string{defaultSecretKey: *value}
	if err := r.backend.PutSecret(ctx, path, data); err != nil {
		return r.fail(fmt.Errorf("vault: store %q: %w", path, err))
	}
	if r.ttl > 0 {
		r.cache.Store(path, cacheEntry{data: data, expires: time.Now().Add(r.ttl)})
	}
	*value = "vault." + path
	return nil
}

// Remove deletes the secret at path (best-effort by contract — callers ignore
// errors). A missing secret is not an error. Matches schemas.VaultRemoveHook.
func (r *Registry) Remove(ctx context.Context, path string) error {
	if r == nil {
		return nil
	}
	if !r.writeEnabled {
		return ErrWriteDisabled
	}
	path = strings.TrimPrefix(path, "vault.")
	if path == "" {
		return nil
	}
	r.cache.Delete(path)
	if err := r.backend.DeleteSecret(ctx, path); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return r.fail(fmt.Errorf("vault: remove %q: %w", path, err))
	}
	return nil
}

// Ping verifies backend connectivity and credentials.
func (r *Registry) Ping(ctx context.Context) error {
	if r == nil {
		return errors.New("vault: registry is nil")
	}
	if err := r.backend.Ping(ctx); err != nil {
		return r.fail(err)
	}
	return nil
}

// BackendName returns the backend type identifier.
func (r *Registry) BackendName() string { return r.backend.Name() }

// InvalidateCache drops every cached secret so subsequent resolves hit the
// backend. Called by the manual/periodic refresh path before re-resolving.
func (r *Registry) InvalidateCache() {
	r.cache.Range(func(k, _ any) bool {
		r.cache.Delete(k)
		return true
	})
}

// Status returns a snapshot for the status endpoint.
func (r *Registry) Status() Status {
	now := time.Now()
	entries := 0
	r.cache.Range(func(_, v any) bool {
		if e, ok := v.(cacheEntry); ok && now.Before(e.expires) {
			entries++
		}
		return true
	})
	s := Status{
		Backend:      r.backend.Name(),
		Prefix:       r.prefix,
		WriteEnabled: r.writeEnabled,
		CacheEntries: entries,
		CacheTTL:     r.ttl,
	}
	r.mu.Lock()
	if r.lastError != "" {
		s.LastError = r.lastError
		at := r.lastErrorAt
		s.LastErrorAt = &at
	}
	r.mu.Unlock()
	return s
}

// getData returns the secret map at path, from cache when fresh, otherwise via
// a singleflight-deduplicated backend read. The returned map must be treated
// as read-only (it is shared with the cache).
func (r *Registry) getData(ctx context.Context, path string) (map[string]string, error) {
	if r.ttl > 0 {
		if v, ok := r.cache.Load(path); ok {
			if e, ok := v.(cacheEntry); ok && time.Now().Before(e.expires) {
				return e.data, nil
			}
			r.cache.Delete(path)
		}
	}
	v, err, _ := r.group.Do(path, func() (any, error) {
		data, err := r.backend.GetSecret(ctx, path)
		if err != nil {
			return nil, err
		}
		if r.ttl > 0 {
			r.cache.Store(path, cacheEntry{data: data, expires: time.Now().Add(r.ttl)})
		}
		return data, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(map[string]string), nil
}

// fail records err as the registry's last error (surfaced by Status) and
// returns it, logging at warn level when a logger is present.
func (r *Registry) fail(err error) error {
	r.mu.Lock()
	r.lastError = err.Error()
	r.lastErrorAt = time.Now()
	r.mu.Unlock()
	if r.logger != nil {
		r.logger.Warn("%v", err)
	}
	return err
}
