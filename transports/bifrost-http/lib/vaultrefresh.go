// This file implements hot rotation of vault-backed provider secrets for the
// Loopback Gateway vault/secret-manager sync (config_store.vault_store).
//
// RefreshVaultSecrets re-resolves every vault-backed SecretVar held in
// c.Providers (Key.Value plus the Azure/Vertex/Bedrock key-config fields) and,
// for providers whose secrets changed, atomically swaps the updated config in
// and pushes it into the live core engine via client.UpdateProvider — the same
// atomic-pointer hot-reload path the provider CRUD handlers use (docs/ARCHITECTURE.md
// gotcha 14: never mutate shared slices in place). Resolve failures keep the
// last-good value so a flapping vault can never blank out provider credentials.
package lib

import (
	"context"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/vault"
)

// VaultRuntime bundles the OSS vault registry, its effective (file/env-only)
// config, and refresh state. Held on Config.Vault; nil when vault is disabled.
type VaultRuntime struct {
	Registry *vault.Registry
	Config   *vault.Config

	mu          sync.Mutex
	lastRefresh time.Time
	lastErrors  []string
	stopSync    chan struct{}
}

// LastRefresh returns the completion time of the most recent refresh (zero if
// none has run) and the errors it recorded.
func (v *VaultRuntime) LastRefresh() (time.Time, []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lastRefresh, append([]string(nil), v.lastErrors...)
}

// recordRefresh stores the outcome of a refresh run.
func (v *VaultRuntime) recordRefresh(errs []string) {
	v.mu.Lock()
	v.lastRefresh = time.Now()
	v.lastErrors = append([]string(nil), errs...)
	v.mu.Unlock()
}

// VaultRefreshResult reports what a refresh run touched.
type VaultRefreshResult struct {
	// ProvidersUpdated lists providers whose configuration was swapped and
	// pushed into the live engine because at least one secret rotated.
	ProvidersUpdated []string `json:"providers_updated"`
	// KeysRechecked counts provider keys holding at least one vault-backed secret.
	KeysRechecked int `json:"keys_rechecked"`
	// SecretsUpdated counts individual secret values that changed.
	SecretsUpdated int `json:"secrets_updated"`
	// Errors lists resolve/update failures. Failed secrets keep their last-good value.
	Errors []string `json:"errors"`
}

// RefreshVaultSecrets re-resolves vault-backed provider secrets and hot-swaps
// changed provider configs into the live engine. Safe to call at any time;
// no-op (with an explanatory error entry) when vault is disabled.
//
// TODO(vault): governance virtual keys and MCP client headers re-resolve on
// their next DB read; extending timed refresh to those caches is a follow-up.
// Key alias overrides (KeyAliases SecretVars) are also out of scope here.
func (c *Config) RefreshVaultSecrets(ctx context.Context) VaultRefreshResult {
	res := VaultRefreshResult{ProvidersUpdated: []string{}, Errors: []string{}}
	if c.Vault == nil || c.Vault.Registry == nil {
		res.Errors = append(res.Errors, "vault is not enabled")
		return res
	}
	registry := c.Vault.Registry
	// Drop cached secrets so this run observes rotations.
	registry.InvalidateCache()

	var changed []schemas.ModelProvider
	c.Mu.Lock()
	for provider, providerConfig := range c.Providers {
		if len(providerConfig.Keys) == 0 {
			continue
		}
		// Clone before mutating: concurrent readers may hold the current slice
		// (atomic-swap semantics, docs/ARCHITECTURE.md, "Gotchas" §14).
		keys := make([]schemas.Key, len(providerConfig.Keys))
		providerChanged := false
		for i := range providerConfig.Keys {
			keys[i] = cloneKeySecrets(providerConfig.Keys[i])
			if c.refreshKeySecrets(ctx, registry, &keys[i], &res) {
				providerChanged = true
			}
		}
		if providerChanged {
			updated := providerConfig
			updated.Keys = keys
			c.Providers[provider] = updated
			changed = append(changed, provider)
			res.ProvidersUpdated = append(res.ProvidersUpdated, string(provider))
		}
	}
	client := c.client
	c.Mu.Unlock()

	// Push changed configs into the live engine outside the lock
	// (UpdateProvider re-reads config under its own locking).
	if client != nil {
		for _, provider := range changed {
			if err := client.UpdateProvider(provider); err != nil {
				res.Errors = append(res.Errors, "failed to update provider "+string(provider)+": "+err.Error())
			}
		}
	}
	if len(changed) > 0 && logger != nil {
		logger.Info("vault refresh: %d secret(s) rotated across %d provider(s)", res.SecretsUpdated, len(changed))
	}
	c.Vault.recordRefresh(res.Errors)
	return res
}

// refreshKeySecrets re-resolves every vault-backed SecretVar reachable from
// key (which must already be a private clone). Returns true when any value
// changed. Failed resolves keep the last-good value and append to res.Errors.
func (c *Config) refreshKeySecrets(ctx context.Context, registry *vault.Registry, key *schemas.Key, res *VaultRefreshResult) bool {
	secretVars := []*schemas.SecretVar{&key.Value}
	if azure := key.AzureKeyConfig; azure != nil {
		secretVars = append(secretVars, &azure.Endpoint, azure.ClientID, azure.ClientSecret, azure.TenantID)
	}
	if vertex := key.VertexKeyConfig; vertex != nil {
		secretVars = append(secretVars, &vertex.ProjectID, &vertex.ProjectNumber, &vertex.Region, &vertex.AuthCredentials)
	}
	if bedrock := key.BedrockKeyConfig; bedrock != nil {
		secretVars = append(secretVars,
			&bedrock.AccessKey, &bedrock.SecretKey,
			bedrock.SessionToken, bedrock.Region, bedrock.ARN,
			bedrock.RoleARN, bedrock.ExternalID, bedrock.RoleSessionName)
	}

	keyHasVaultSecret := false
	keyChanged := false
	for _, sv := range secretVars {
		if sv == nil || !sv.IsFromVault() || sv.GetRawRef() == "" {
			continue
		}
		keyHasVaultSecret = true
		resolved := sv.GetRawRef()
		if err := registry.ResolveString(ctx, &resolved); err != nil {
			// Keep-last-good: never blank a credential because vault flapped.
			res.Errors = append(res.Errors, "key "+key.ID+": "+err.Error())
			continue
		}
		if resolved != sv.GetValue() {
			sv.Val = resolved
			res.SecretsUpdated++
			keyChanged = true
		}
	}
	if keyHasVaultSecret {
		res.KeysRechecked++
	}
	return keyChanged
}

// cloneKeySecrets returns a copy of key whose secret-bearing nested configs are
// deep-copied so they can be mutated without racing readers of the original.
// Non-secret pointers (BatchS3Config, aliases, etc.) are shared — they are
// never mutated by the refresh path.
func cloneKeySecrets(key schemas.Key) schemas.Key {
	if key.AzureKeyConfig != nil {
		azure := *key.AzureKeyConfig
		azure.ClientID = cloneSecretVarPtr(azure.ClientID)
		azure.ClientSecret = cloneSecretVarPtr(azure.ClientSecret)
		azure.TenantID = cloneSecretVarPtr(azure.TenantID)
		key.AzureKeyConfig = &azure
	}
	if key.VertexKeyConfig != nil {
		vertex := *key.VertexKeyConfig
		key.VertexKeyConfig = &vertex
	}
	if key.BedrockKeyConfig != nil {
		bedrock := *key.BedrockKeyConfig
		bedrock.SessionToken = cloneSecretVarPtr(bedrock.SessionToken)
		bedrock.Region = cloneSecretVarPtr(bedrock.Region)
		bedrock.ARN = cloneSecretVarPtr(bedrock.ARN)
		bedrock.RoleARN = cloneSecretVarPtr(bedrock.RoleARN)
		bedrock.ExternalID = cloneSecretVarPtr(bedrock.ExternalID)
		bedrock.RoleSessionName = cloneSecretVarPtr(bedrock.RoleSessionName)
		key.BedrockKeyConfig = &bedrock
	}
	return key
}

// cloneSecretVarPtr copies a *SecretVar (nil-safe).
func cloneSecretVarPtr(sv *schemas.SecretVar) *schemas.SecretVar {
	if sv == nil {
		return nil
	}
	cp := *sv
	return &cp
}

// StartVaultSync starts the optional background rotation ticker
// (config_store.vault_store.sync_interval). No-op when vault is disabled or
// the interval is unset/zero (the default — manual refresh only). Idempotent.
func (c *Config) StartVaultSync(ctx context.Context) {
	v := c.Vault
	if v == nil || v.Registry == nil {
		return
	}
	interval := v.Config.EffectiveSyncInterval()
	if interval <= 0 {
		return
	}
	v.mu.Lock()
	if v.stopSync != nil {
		v.mu.Unlock()
		return // already running
	}
	stop := make(chan struct{})
	v.stopSync = stop
	v.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.RefreshVaultSecrets(ctx)
			case <-stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	if logger != nil {
		logger.Info("vault sync started (interval %s)", interval)
	}
}

// StopVaultSync stops the background rotation ticker if it is running.
func (c *Config) StopVaultSync() {
	v := c.Vault
	if v == nil {
		return
	}
	v.mu.Lock()
	if v.stopSync != nil {
		close(v.stopSync)
		v.stopSync = nil
	}
	v.mu.Unlock()
}
