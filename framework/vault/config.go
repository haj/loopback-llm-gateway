// Package vault implements the OSS vault / secret-manager registry behind the
// core schemas hook seam (schemas.VaultResolveHook / VaultStoreHook /
// VaultRemoveHook / VaultPrefixHook). It resolves "vault.<path>[#jsonKey]"
// SecretVar references through a pluggable Backend, with HashiCorp Vault KV v2
// as the first implementation (plain net/http, no vault SDK). AWS / GCP / Azure
// secret managers can drop in later by implementing Backend.
//
// The package is default-off: nothing here runs unless config.json's
// config_store.vault_store section is present and enabled. Vault configuration
// is deliberately file/env-only (like encryption_key) because config-store rows
// may themselves hold vault references — a DB-backed vault config would be a
// bootstrap chicken-and-egg.
package vault

import (
	"fmt"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// Backend type identifiers. These mirror the config.schema.json
// config_store.vault_store.type enum.
const (
	TypeHashiCorp = "hashicorp-vault"
	TypeAWS       = "aws-secrets-manager"
	TypeGCP       = "gcp-secret-manager"
)

// Access modes. read_only resolves existing refs only; read_and_write also
// auto-stores plaintext SecretVars and deletes owned secrets (via the GORM
// vault callbacks in configstore).
const (
	AccessModeReadOnly     = "read_only"
	AccessModeReadAndWrite = "read_and_write"
)

// Defaults.
const (
	DefaultPrefix   = "bifrost"
	DefaultCacheTTL = 5 * time.Minute
)

// Config is the config_store.vault_store section of config.json. Secret-bearing
// fields are SecretVars so "env.VAR" references work; "vault." references
// inside the vault config itself are unresolvable by construction and must not
// be used.
type Config struct {
	Enabled    bool   `json:"enabled"`
	Type       string `json:"type"`
	Prefix     string `json:"prefix,omitempty"`
	AccessMode string `json:"access_mode,omitempty"`
	// SyncInterval, when set to a Go duration string (e.g. "5m"), re-resolves
	// vault-backed provider secrets on a timer so rotated secrets propagate
	// without a restart. Empty or "0" (the default) means manual refresh only
	// (POST /api/vault/refresh).
	SyncInterval string `json:"sync_interval,omitempty"`
	// CacheTTL bounds the registry read cache as a Go duration string.
	// Defaults to "5m". "0" disables caching (every resolve hits the backend).
	CacheTTL string `json:"cache_ttl,omitempty"`

	HashiCorp *HashiCorpConfig `json:"hashicorp,omitempty"`
	AWS       *AWSConfig       `json:"aws,omitempty"`
	GCP       *GCPConfig       `json:"gcp,omitempty"`
}

// HashiCorpConfig configures the HashiCorp Vault KV v2 backend. Either Token or
// RoleID+SecretID (AppRole login) must be provided.
type HashiCorpConfig struct {
	Address   *schemas.SecretVar `json:"address,omitempty"`
	Token     *schemas.SecretVar `json:"token,omitempty"`
	Namespace *schemas.SecretVar `json:"namespace,omitempty"`
	// MountPath is the KV v2 mount (default "secret").
	MountPath *schemas.SecretVar `json:"mount_path,omitempty"`
	// RoleID / SecretID enable AppRole login instead of a static token.
	RoleID   *schemas.SecretVar `json:"role_id,omitempty"`
	SecretID *schemas.SecretVar `json:"secret_id,omitempty"`
}

// AWSConfig reserves the AWS Secrets Manager backend configuration shape
// (already specified in config.schema.json). Not implemented yet.
type AWSConfig struct {
	Region          *schemas.SecretVar `json:"region,omitempty"`
	AccessKeyID     *schemas.SecretVar `json:"access_key_id,omitempty"`
	SecretAccessKey *schemas.SecretVar `json:"secret_access_key,omitempty"`
	SessionToken    *schemas.SecretVar `json:"session_token,omitempty"`
	RoleARN         *schemas.SecretVar `json:"role_arn,omitempty"`
	KMSKeyID        *schemas.SecretVar `json:"kms_key_id,omitempty"`
}

// GCPConfig reserves the GCP Secret Manager backend configuration shape
// (already specified in config.schema.json). Not implemented yet.
type GCPConfig struct {
	ProjectID       *schemas.SecretVar `json:"project_id,omitempty"`
	CredentialsJSON *schemas.SecretVar `json:"credentials_json,omitempty"`
}

// EffectivePrefix returns the configured secret path prefix, defaulting to
// "bifrost" (matching schemas.VaultPrefix's OSS fallback).
func (c *Config) EffectivePrefix() string {
	if c == nil || c.Prefix == "" {
		return DefaultPrefix
	}
	return c.Prefix
}

// EffectiveAccessMode returns the configured access mode, defaulting to read_only.
func (c *Config) EffectiveAccessMode() string {
	if c == nil || c.AccessMode == "" {
		return AccessModeReadOnly
	}
	return c.AccessMode
}

// WriteEnabled reports whether the store/remove hooks should be wired
// (access_mode == read_and_write).
func (c *Config) WriteEnabled() bool {
	return c.EffectiveAccessMode() == AccessModeReadAndWrite
}

// EffectiveCacheTTL returns the parsed cache TTL, defaulting to 5m. Zero
// disables caching.
func (c *Config) EffectiveCacheTTL() time.Duration {
	if c == nil || c.CacheTTL == "" {
		return DefaultCacheTTL
	}
	d, err := time.ParseDuration(c.CacheTTL)
	if err != nil || d < 0 {
		return DefaultCacheTTL
	}
	return d
}

// EffectiveSyncInterval returns the parsed background sync interval. Zero (the
// default) means no background sync — manual refresh only.
func (c *Config) EffectiveSyncInterval() time.Duration {
	if c == nil || c.SyncInterval == "" {
		return 0
	}
	d, err := time.ParseDuration(c.SyncInterval)
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// Validate checks the config for a usable backend definition. Disabled configs
// are always valid.
func (c *Config) Validate() error {
	if c == nil || !c.Enabled {
		return nil
	}
	switch c.EffectiveAccessMode() {
	case AccessModeReadOnly, AccessModeReadAndWrite:
	default:
		return fmt.Errorf("vault: invalid access_mode %q (want %q or %q)", c.AccessMode, AccessModeReadOnly, AccessModeReadAndWrite)
	}
	if c.SyncInterval != "" {
		if _, err := time.ParseDuration(c.SyncInterval); err != nil {
			return fmt.Errorf("vault: invalid sync_interval %q: %w", c.SyncInterval, err)
		}
	}
	if c.CacheTTL != "" {
		if _, err := time.ParseDuration(c.CacheTTL); err != nil {
			return fmt.Errorf("vault: invalid cache_ttl %q: %w", c.CacheTTL, err)
		}
	}
	switch c.Type {
	case TypeHashiCorp:
		if c.HashiCorp == nil {
			return fmt.Errorf("vault: type %q requires a hashicorp config block", c.Type)
		}
		if c.HashiCorp.Address.GetValue() == "" {
			return fmt.Errorf("vault: hashicorp.address is required")
		}
		if c.HashiCorp.Token.GetValue() == "" &&
			(c.HashiCorp.RoleID.GetValue() == "" || c.HashiCorp.SecretID.GetValue() == "") {
			return fmt.Errorf("vault: hashicorp requires either token or role_id + secret_id (AppRole)")
		}
		return nil
	case TypeAWS, TypeGCP:
		return fmt.Errorf("vault: backend type %q is not implemented yet (only %q is supported)", c.Type, TypeHashiCorp)
	default:
		return fmt.Errorf("vault: unknown backend type %q", c.Type)
	}
}
