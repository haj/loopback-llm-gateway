package lib

import (
	"context"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/vault"
	"github.com/stretchr/testify/require"
)

// saveVaultHooks snapshots the four global vault hooks and restores them on
// cleanup (the stubVaultHooks pattern from configstore's vault_callbacks_test).
func saveVaultHooks(t *testing.T) {
	t.Helper()
	prevResolve, prevStore, prevRemove, prevPrefix :=
		schemas.VaultResolveHook, schemas.VaultStoreHook, schemas.VaultRemoveHook, schemas.VaultPrefixHook
	t.Cleanup(func() {
		schemas.VaultResolveHook = prevResolve
		schemas.VaultStoreHook = prevStore
		schemas.VaultRemoveHook = prevRemove
		schemas.VaultPrefixHook = prevPrefix
	})
}

// memVaultBackend is a minimal in-memory vault.Backend for refresh tests.
type memVaultBackend struct {
	secrets map[string]map[string]string
}

func (m *memVaultBackend) Name() string { return "mem" }

func (m *memVaultBackend) GetSecret(_ context.Context, path string) (map[string]string, error) {
	data, ok := m.secrets[path]
	if !ok {
		return nil, fmt.Errorf("%w: %s", vault.ErrNotFound, path)
	}
	return data, nil
}

func (m *memVaultBackend) PutSecret(_ context.Context, path string, data map[string]string) error {
	m.secrets[path] = data
	return nil
}

func (m *memVaultBackend) DeleteSecret(_ context.Context, path string) error {
	delete(m.secrets, path)
	return nil
}

func (m *memVaultBackend) Ping(_ context.Context) error { return nil }

func newMemRegistry(t *testing.T, backend *memVaultBackend) *vault.Registry {
	t.Helper()
	reg, err := vault.NewRegistryWithBackend(&vault.Config{
		Enabled:    true,
		Type:       vault.TypeHashiCorp,
		AccessMode: vault.AccessModeReadAndWrite,
	}, backend, nil)
	require.NoError(t, err)
	return reg
}

// vaultSecretVar builds a resolved vault-backed SecretVar via the registry, the
// same way SecretVar.Scan does on a DB read.
func vaultSecretVar(t *testing.T, reg *vault.Registry, ref string) schemas.SecretVar {
	t.Helper()
	saveVaultHooks(t)
	schemas.VaultResolveHook = reg.ResolveString
	var sv schemas.SecretVar
	require.NoError(t, sv.Scan(ref))
	require.True(t, sv.IsFromVault())
	return sv
}

func TestInitVaultDefaultOff(t *testing.T) {
	saveVaultHooks(t)
	schemas.VaultResolveHook = nil
	schemas.VaultStoreHook = nil
	schemas.VaultRemoveHook = nil
	schemas.VaultPrefixHook = nil

	cases := []struct {
		name       string
		configData ConfigData
	}{
		{"no config_store section", ConfigData{}},
		{"config_store without vault_store", ConfigData{ConfigStoreConfig: &configstore.Config{Enabled: true}}},
		{"vault_store disabled", ConfigData{ConfigStoreConfig: &configstore.Config{
			VaultStore: &vault.Config{Enabled: false, Type: vault.TypeHashiCorp},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := &Config{}
			require.NoError(t, initVault(config, &tc.configData))
			require.Nil(t, config.Vault, "Config.Vault must stay nil when vault is off")
			require.Nil(t, schemas.VaultResolveHook, "resolve hook must stay nil")
			require.Nil(t, schemas.VaultStoreHook, "store hook must stay nil")
			require.Nil(t, schemas.VaultRemoveHook, "remove hook must stay nil")
			require.Nil(t, schemas.VaultPrefixHook, "prefix hook must stay nil")
		})
	}
}

func TestInitVaultWiresHooksByAccessMode(t *testing.T) {
	hashicorp := &vault.HashiCorpConfig{
		Address: schemas.NewSecretVar("http://127.0.0.1:8200"),
		Token:   schemas.NewSecretVar("unit-test-token"),
	}

	t.Run("read_only wires resolve+prefix only", func(t *testing.T) {
		saveVaultHooks(t)
		config := &Config{}
		configData := ConfigData{ConfigStoreConfig: &configstore.Config{
			VaultStore: &vault.Config{
				Enabled:    true,
				Type:       vault.TypeHashiCorp,
				AccessMode: vault.AccessModeReadOnly,
				Prefix:     "custom-prefix",
				HashiCorp:  hashicorp,
			},
		}}
		require.NoError(t, initVault(config, &configData))
		require.NotNil(t, config.Vault)
		require.NotNil(t, config.Vault.Registry)
		require.NotNil(t, schemas.VaultResolveHook)
		require.NotNil(t, schemas.VaultPrefixHook)
		require.Nil(t, schemas.VaultStoreHook, "read_only must not wire the store hook")
		require.Nil(t, schemas.VaultRemoveHook, "read_only must not wire the remove hook")
		require.Equal(t, "custom-prefix", schemas.VaultPrefix())
		require.False(t, schemas.VaultStoreWriteEnabled())
	})

	t.Run("read_and_write wires all four", func(t *testing.T) {
		saveVaultHooks(t)
		config := &Config{}
		configData := ConfigData{ConfigStoreConfig: &configstore.Config{
			VaultStore: &vault.Config{
				Enabled:    true,
				Type:       vault.TypeHashiCorp,
				AccessMode: vault.AccessModeReadAndWrite,
				HashiCorp:  hashicorp,
			},
		}}
		require.NoError(t, initVault(config, &configData))
		require.NotNil(t, schemas.VaultResolveHook)
		require.NotNil(t, schemas.VaultPrefixHook)
		require.NotNil(t, schemas.VaultStoreHook)
		require.NotNil(t, schemas.VaultRemoveHook)
		require.True(t, schemas.VaultStoreWriteEnabled())
	})

	t.Run("invalid config fails startup", func(t *testing.T) {
		saveVaultHooks(t)
		config := &Config{}
		configData := ConfigData{ConfigStoreConfig: &configstore.Config{
			VaultStore: &vault.Config{Enabled: true, Type: "aws-secrets-manager"},
		}}
		require.Error(t, initVault(config, &configData))
		require.Nil(t, config.Vault)
	})
}

func TestRefreshVaultSecretsRotatesChangedKeys(t *testing.T) {
	backend := &memVaultBackend{secrets: map[string]map[string]string{
		"bifrost/keys/openai": {"value": "old-openai-secret"},
		"bifrost/keys/azure":  {"value": "old-azure-secret"},
	}}
	reg := newMemRegistry(t, backend)

	openaiKey := schemas.Key{
		ID:    "key-openai",
		Value: vaultSecretVar(t, reg, "vault.bifrost/keys/openai"),
	}
	azureKey := schemas.Key{
		ID:    "key-azure",
		Value: schemas.SecretVar{Val: "plain-azure-key"},
		AzureKeyConfig: &schemas.AzureKeyConfig{
			Endpoint:     schemas.SecretVar{Val: "https://example.azure.com"},
			ClientSecret: new(vaultSecretVar(t, reg, "vault.bifrost/keys/azure")),
		},
	}
	plainKey := schemas.Key{ID: "key-plain", Value: schemas.SecretVar{Val: "plain"}}

	config := &Config{
		Providers: map[schemas.ModelProvider]configstore.ProviderConfig{
			schemas.OpenAI:    {Keys: []schemas.Key{openaiKey}},
			schemas.Azure:     {Keys: []schemas.Key{azureKey}},
			schemas.Anthropic: {Keys: []schemas.Key{plainKey}},
		},
		Vault: &VaultRuntime{Registry: reg, Config: &vault.Config{Enabled: true}},
	}
	originalOpenAIKeys := config.Providers[schemas.OpenAI].Keys

	// Rotate both secrets behind the registry cache.
	backend.secrets["bifrost/keys/openai"] = map[string]string{"value": "new-openai-secret"}
	backend.secrets["bifrost/keys/azure"] = map[string]string{"value": "new-azure-secret"}

	res := config.RefreshVaultSecrets(context.Background())
	require.Empty(t, res.Errors)
	require.Equal(t, 2, res.KeysRechecked)
	require.Equal(t, 2, res.SecretsUpdated)
	require.ElementsMatch(t, []string{"openai", "azure"}, res.ProvidersUpdated)

	require.Equal(t, "new-openai-secret", config.Providers[schemas.OpenAI].Keys[0].Value.GetValue())
	require.Equal(t, "vault.bifrost/keys/openai", config.Providers[schemas.OpenAI].Keys[0].Value.GetRawRef(), "ref must survive rotation")
	require.Equal(t, "new-azure-secret", config.Providers[schemas.Azure].Keys[0].AzureKeyConfig.ClientSecret.GetValue())
	require.Equal(t, "plain-azure-key", config.Providers[schemas.Azure].Keys[0].Value.GetValue(), "non-vault values untouched")
	require.Equal(t, "plain", config.Providers[schemas.Anthropic].Keys[0].Value.GetValue())

	// Atomic-swap semantics: the previously-held slice was not mutated in place.
	require.Equal(t, "old-openai-secret", originalOpenAIKeys[0].Value.GetValue(),
		"refresh must clone, never mutate the shared keys slice")

	// Second refresh with no rotation: nothing to update.
	res = config.RefreshVaultSecrets(context.Background())
	require.Empty(t, res.Errors)
	require.Equal(t, 0, res.SecretsUpdated)
	require.Empty(t, res.ProvidersUpdated)

	// Refresh state is recorded on the runtime.
	last, errs := config.Vault.LastRefresh()
	require.False(t, last.IsZero())
	require.Empty(t, errs)
}

func TestRefreshVaultSecretsKeepsLastGoodOnFailure(t *testing.T) {
	backend := &memVaultBackend{secrets: map[string]map[string]string{
		"bifrost/keys/openai": {"value": "good-secret"},
	}}
	reg := newMemRegistry(t, backend)

	key := schemas.Key{ID: "key-1", Value: vaultSecretVar(t, reg, "vault.bifrost/keys/openai")}
	config := &Config{
		Providers: map[schemas.ModelProvider]configstore.ProviderConfig{
			schemas.OpenAI: {Keys: []schemas.Key{key}},
		},
		Vault: &VaultRuntime{Registry: reg, Config: &vault.Config{Enabled: true}},
	}

	// Simulate a vault outage for this path.
	delete(backend.secrets, "bifrost/keys/openai")

	res := config.RefreshVaultSecrets(context.Background())
	require.Len(t, res.Errors, 1)
	require.Contains(t, res.Errors[0], "key-1")
	require.Equal(t, 0, res.SecretsUpdated)
	require.Empty(t, res.ProvidersUpdated)
	require.Equal(t, "good-secret", config.Providers[schemas.OpenAI].Keys[0].Value.GetValue(),
		"resolve failure must keep the last-good value")

	_, errs := config.Vault.LastRefresh()
	require.Len(t, errs, 1)
}

func TestRefreshVaultSecretsDisabled(t *testing.T) {
	config := &Config{}
	res := config.RefreshVaultSecrets(context.Background())
	require.Equal(t, []string{"vault is not enabled"}, res.Errors)
	require.Empty(t, res.ProvidersUpdated)
}

func TestStartVaultSyncNoOpWhenDisabledOrUnset(t *testing.T) {
	// Disabled: nil runtime.
	config := &Config{}
	config.StartVaultSync(context.Background())
	config.StopVaultSync()

	// Enabled but no sync_interval: manual refresh only.
	backend := &memVaultBackend{secrets: map[string]map[string]string{}}
	reg := newMemRegistry(t, backend)
	config = &Config{Vault: &VaultRuntime{Registry: reg, Config: &vault.Config{Enabled: true}}}
	config.StartVaultSync(context.Background())
	config.Vault.mu.Lock()
	require.Nil(t, config.Vault.stopSync, "no ticker without sync_interval")
	config.Vault.mu.Unlock()

	// With an interval the ticker starts and Stop is idempotent.
	config = &Config{Vault: &VaultRuntime{Registry: reg, Config: &vault.Config{Enabled: true, SyncInterval: "1h"}}}
	config.StartVaultSync(context.Background())
	config.Vault.mu.Lock()
	require.NotNil(t, config.Vault.stopSync)
	config.Vault.mu.Unlock()
	config.StartVaultSync(context.Background()) // idempotent
	config.StopVaultSync()
	config.StopVaultSync()
}
