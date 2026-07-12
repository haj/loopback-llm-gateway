package configstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/maximhq/bifrost/framework/vault"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// stubVaultHooks installs store/remove hooks that mimic the enterprise vault
// registry: store records the path and rewrites the value to "vault.<path>";
// remove records the deleted path. Hooks are restored on cleanup.
func stubVaultHooks(t *testing.T) (stored map[string]string, removed *[]string) {
	t.Helper()
	stored = make(map[string]string)
	rem := []string{}
	prevStore, prevRemove := schemas.VaultStoreHook, schemas.VaultRemoveHook
	schemas.VaultStoreHook = func(_ context.Context, path string, value *string) error {
		stored[path] = *value
		*value = "vault." + path
		return nil
	}
	schemas.VaultRemoveHook = func(_ context.Context, path string) error {
		rem = append(rem, path)
		return nil
	}
	t.Cleanup(func() {
		schemas.VaultStoreHook = prevStore
		schemas.VaultRemoveHook = prevRemove
	})
	return stored, &rem
}

func TestVaultCallbacks_AutoStoreAndRemove(t *testing.T) {
	stored, removed := stubVaultHooks(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	RegisterVaultCallbacks(db)
	require.NoError(t, db.AutoMigrate(&tables.TableMCPClient{}))

	client := &tables.TableMCPClient{
		ClientID:       "client-1",
		Name:           "test-client",
		ConnectionType: "http",
		AuthType:       "headers",
		Headers: map[string]schemas.SecretVar{
			"Authorization": {Val: "secret-token"},
		},
	}
	require.NoError(t, db.Create(client).Error)

	// The global store callback should have pushed the plaintext header to vault
	// before BeforeSave serialized Headers into HeadersJSON.
	headerPath := "bifrost/config_mcp_clients/client-1/headers/Authorization"
	require.Equal(t, "secret-token", stored[headerPath], "header secret not stored to vault")

	// HeadersJSON persisted in the row should hold the vault ref, not plaintext.
	var row tables.TableMCPClient
	require.NoError(t, db.First(&row, "client_id = ?", "client-1").Error)
	var headers map[string]string
	require.NoError(t, json.Unmarshal([]byte(row.HeadersJSON), &headers))
	require.Equal(t, "vault."+headerPath, headers["Authorization"], "HeadersJSON should store vault ref")

	// Deleting the row should trigger the global remove callback. Load first so
	// the model has its Headers populated for the reflection walk.
	var toDelete tables.TableMCPClient
	require.NoError(t, db.First(&toDelete, "client_id = ?", "client-1").Error)
	require.NoError(t, db.Delete(&toDelete).Error)

	found := false
	for _, p := range *removed {
		if p == headerPath {
			found = true
		}
	}
	require.True(t, found, "expected vault remove for %q, got %v", headerPath, *removed)
}

// TestVaultCallbacks_SelfManagedStoresPlaintext verifies that TableKey, whose
// SecretVar columns are populated inside BeforeSave, stores the PLAINTEXT secret to
// vault and persists a vault ref — both when encryption is off and when it is on.
// With encryption on, the inline vault store must run before encryption so the vault
// holds plaintext (not ciphertext) and the column holds the ref (not encrypted data).
func TestVaultCallbacks_SelfManagedStoresPlaintext(t *testing.T) {
	cases := []struct {
		name          string
		encryptionKey string
	}{
		{"encryption off", ""},
		{"encryption on", "test-encryption-key"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored, _ := stubVaultHooks(t)
			encrypt.Init(tc.encryptionKey, bifrost.NewDefaultLogger(schemas.LogLevelInfo))
			t.Cleanup(func() { encrypt.Init("", bifrost.NewDefaultLogger(schemas.LogLevelInfo)) })

			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			RegisterVaultCallbacks(db)
			require.NoError(t, db.AutoMigrate(&tables.TableKey{}))

			keyID := fmt.Sprintf("key-%d", i)
			key := &tables.TableKey{
				Name:     fmt.Sprintf("k%d", i),
				KeyID:    keyID,
				Provider: "bedrock",
				Value:    schemas.SecretVar{Val: "primary-value"},
				Models:   schemas.WhiteList{"*"},
				BedrockKeyConfig: &schemas.BedrockKeyConfig{
					SecretKey: schemas.SecretVar{Val: "bedrock-secret"},
				},
			}
			require.NoError(t, db.Create(key).Error)

			// The vault must receive PLAINTEXT, regardless of encryption state.
			secretPath := fmt.Sprintf("bifrost/config_keys/%s/bedrock_secret_key", keyID)
			require.Equal(t, "bedrock-secret", stored[secretPath], "vault must store plaintext, not ciphertext")

			// The persisted column should hold the vault ref (which is never re-encrypted).
			var row tables.TableKey
			require.NoError(t, db.First(&row, "key_id = ?", keyID).Error)
			require.NotNil(t, row.BedrockSecretKey)
			require.Equal(t, "vault."+secretPath, row.BedrockSecretKey.GetRawRef(), "column should store vault ref")
		})
	}
}

// memBackend is a minimal in-memory vault.Backend for round-trip tests.
type memBackend struct {
	secrets map[string]map[string]string
}

func (m *memBackend) Name() string { return "mem" }

func (m *memBackend) GetSecret(_ context.Context, path string) (map[string]string, error) {
	data, ok := m.secrets[path]
	if !ok {
		return nil, fmt.Errorf("%w: %s", vault.ErrNotFound, path)
	}
	return data, nil
}

func (m *memBackend) PutSecret(_ context.Context, path string, data map[string]string) error {
	m.secrets[path] = data
	return nil
}

func (m *memBackend) DeleteSecret(_ context.Context, path string) error {
	if _, ok := m.secrets[path]; !ok {
		return fmt.Errorf("%w: %s", vault.ErrNotFound, path)
	}
	delete(m.secrets, path)
	return nil
}

func (m *memBackend) Ping(_ context.Context) error { return nil }

// TestVaultCallbacks_RealRegistryRoundTrip wires the schemas hooks to a real
// framework/vault Registry (as transports' initVault does) over an in-memory
// backend and asserts the full TableKey round-trip with encryption enabled:
// plaintext in → persisted column holds the vault ref (not ciphertext, proving
// encryption skipped the vault-owned field) → AfterFind re-resolves the
// plaintext through the registry.
func TestVaultCallbacks_RealRegistryRoundTrip(t *testing.T) {
	backend := &memBackend{secrets: map[string]map[string]string{}}
	reg, err := vault.NewRegistryWithBackend(&vault.Config{
		Enabled:    true,
		Type:       vault.TypeHashiCorp,
		AccessMode: vault.AccessModeReadAndWrite,
	}, backend, nil)
	require.NoError(t, err)

	prevResolve, prevStore, prevRemove, prevPrefix :=
		schemas.VaultResolveHook, schemas.VaultStoreHook, schemas.VaultRemoveHook, schemas.VaultPrefixHook
	schemas.VaultResolveHook = reg.ResolveString
	schemas.VaultStoreHook = reg.StoreString
	schemas.VaultRemoveHook = reg.Remove
	schemas.VaultPrefixHook = reg.Prefix
	t.Cleanup(func() {
		schemas.VaultResolveHook = prevResolve
		schemas.VaultStoreHook = prevStore
		schemas.VaultRemoveHook = prevRemove
		schemas.VaultPrefixHook = prevPrefix
	})

	encrypt.Init("round-trip-encryption-key", bifrost.NewDefaultLogger(schemas.LogLevelInfo))
	t.Cleanup(func() { encrypt.Init("", bifrost.NewDefaultLogger(schemas.LogLevelInfo)) })

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	RegisterVaultCallbacks(db)
	require.NoError(t, db.AutoMigrate(&tables.TableKey{}))

	key := &tables.TableKey{
		Name:     "round-trip",
		KeyID:    "rt-key-1",
		Provider: "openai",
		Value:    schemas.SecretVar{Val: "sk-plaintext-secret"},
		Models:   schemas.WhiteList{"*"},
	}
	require.NoError(t, db.Create(key).Error)

	// The vault holds the plaintext under the canonical owned path.
	vaultPath := "bifrost/config_keys/rt-key-1/value"
	require.Equal(t, map[string]string{"value": "sk-plaintext-secret"}, backend.secrets[vaultPath])

	// The raw persisted column holds the vault ref — not plaintext and not
	// ciphertext (encryptSecretVar must skip vault-backed fields).
	var rawValue string
	require.NoError(t, db.Raw("SELECT value FROM config_keys WHERE key_id = ?", "rt-key-1").Scan(&rawValue).Error)
	require.Equal(t, "vault."+vaultPath, rawValue)

	// AfterFind re-resolves the ref back to plaintext through the registry.
	var row tables.TableKey
	require.NoError(t, db.First(&row, "key_id = ?", "rt-key-1").Error)
	require.Equal(t, "sk-plaintext-secret", row.Value.GetValue())
	require.Equal(t, "vault."+vaultPath, row.Value.GetRawRef())
	require.True(t, row.Value.IsFromVault())

	// Deleting the row removes the owned secret from the backend.
	require.NoError(t, db.Delete(&row).Error)
	require.NotContains(t, backend.secrets, vaultPath)
}

func TestVaultCallbacks_NoOpWhenDisabled(t *testing.T) {
	// No hooks installed -> VaultStoreEnabled() is false -> callbacks no-op.
	prevStore := schemas.VaultStoreHook
	schemas.VaultStoreHook = nil
	t.Cleanup(func() { schemas.VaultStoreHook = prevStore })

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	RegisterVaultCallbacks(db)
	require.NoError(t, db.AutoMigrate(&tables.TableMCPClient{}))

	client := &tables.TableMCPClient{
		ClientID:       "client-2",
		Name:           "plain-client",
		ConnectionType: "http",
		AuthType:       "headers",
		Headers:        map[string]schemas.SecretVar{"Authorization": {Val: "plain-secret"}},
	}
	require.NoError(t, db.Create(client).Error)

	var row tables.TableMCPClient
	require.NoError(t, db.First(&row, "client_id = ?", "client-2").Error)
	require.False(t, strings.Contains(row.HeadersJSON, "vault."), "no vault ref expected when disabled: %s", row.HeadersJSON)
}
