package configstore

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupAPIKeyTestStore extends the shared in-memory SQLite store with the admin
// API key tables.
func setupAPIKeyTestStore(t *testing.T) *RDBConfigStore {
	s := setupRDBTestStore(t)
	require.NoError(t, s.DB().AutoMigrate(
		&tables.TableAdminAPIKey{},
		&tables.TableAdminAPIKeyScope{},
	), "failed to migrate admin api key tables")
	return s
}

func TestAdminAPIKey_RoundTripAndHashAtRest(t *testing.T) {
	s := setupAPIKeyTestStore(t)
	ctx := context.Background()

	const plaintext = "lbk_test-secret-value-0123456789abcdef"
	expiry := time.Now().Add(24 * time.Hour)
	key := &tables.TableAdminAPIKey{
		ID:        "ak-1",
		Name:      "automation",
		KeyPrefix: plaintext[:10],
		Value:     plaintext,
		Status:    tables.AdminAPIKeyStatusActive,
		ExpiresAt: &expiry,
		CreatedBy: "local-admin",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, s.CreateAPIKey(ctx, key))
	require.NoError(t, s.ReplaceAPIKeyScopes(ctx, key.ID, []tables.TableAdminAPIKeyScope{
		{ID: "sc-1", APIKeyID: key.ID, Resource: "ModelProvider", Operation: tables.RbacOperationCreate, CreatedAt: time.Now()},
		{ID: "sc-2", APIKeyID: key.ID, Resource: tables.RbacWildcard, Operation: tables.RbacOperationRead, CreatedAt: time.Now()},
	}))

	// Hash-at-rest: the persisted row must hold only the SHA-256 of the
	// plaintext (64 hex chars), never the plaintext itself.
	var raw struct{ ValueHash string }
	require.NoError(t, s.DB().Raw("SELECT value_hash FROM governance_admin_api_keys WHERE id = ?", key.ID).Scan(&raw).Error)
	assert.NotEqual(t, plaintext, raw.ValueHash, "plaintext must never be persisted")
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{64}$`), raw.ValueHash)
	assert.Equal(t, encrypt.HashSHA256(plaintext), raw.ValueHash)

	// Lookup by hash is the middleware path; scopes are preloaded.
	byHash, err := s.GetAPIKeyByValueHash(ctx, encrypt.HashSHA256(plaintext))
	require.NoError(t, err)
	assert.Equal(t, key.ID, byHash.ID)
	assert.Len(t, byHash.Scopes, 2)

	// Replace scopes is a full swap.
	require.NoError(t, s.ReplaceAPIKeyScopes(ctx, key.ID, []tables.TableAdminAPIKeyScope{
		{ID: "sc-3", APIKeyID: key.ID, Resource: "Teams", Operation: tables.RbacOperationUpdate, CreatedAt: time.Now()},
	}))
	reloaded, err := s.GetAPIKey(ctx, key.ID)
	require.NoError(t, err)
	require.Len(t, reloaded.Scopes, 1)
	assert.Equal(t, "Teams", reloaded.Scopes[0].Resource)

	// Unknown hash → ErrNotFound (the middleware's fall-through signal).
	_, err = s.GetAPIKeyByValueHash(ctx, encrypt.HashSHA256("lbk_nope"))
	assert.ErrorIs(t, err, ErrNotFound)

	// TouchAPIKeyLastUsed stamps last_used_at.
	usedAt := time.Now()
	require.NoError(t, s.TouchAPIKeyLastUsed(ctx, key.ID, usedAt))
	touched, err := s.GetAPIKey(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, touched.LastUsedAt)

	// Delete removes key and scopes.
	require.NoError(t, s.DeleteAPIKey(ctx, key.ID))
	_, err = s.GetAPIKey(ctx, key.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	var scopeCount int64
	require.NoError(t, s.DB().Raw("SELECT COUNT(*) FROM governance_admin_api_key_scopes WHERE api_key_id = ?", key.ID).Scan(&scopeCount).Error)
	assert.Zero(t, scopeCount)
}

func TestAdminAPIKeyScope_ValidationUsesRbacVocabulary(t *testing.T) {
	s := setupAPIKeyTestStore(t)
	ctx := context.Background()

	key := &tables.TableAdminAPIKey{
		ID:        "ak-2",
		Name:      "validator",
		Value:     "lbk_another-secret",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, s.CreateAPIKey(ctx, key))

	// Non-RBAC operations are rejected by the scope's BeforeSave hook.
	err := s.ReplaceAPIKeyScopes(ctx, key.ID, []tables.TableAdminAPIKeyScope{
		{ID: "sc-bad", APIKeyID: key.ID, Resource: "Teams", Operation: "Frobnicate", CreatedAt: time.Now()},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Frobnicate")

	// A key without a value hash is rejected.
	err = s.CreateAPIKey(ctx, &tables.TableAdminAPIKey{ID: "ak-3", Name: "no-secret", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	require.Error(t, err)
}

func TestGrantMatches_SharedWildcardSemantics(t *testing.T) {
	perm := tables.TablePermission{Resource: tables.RbacWildcard, Operation: "Read"}
	scope := tables.TableAdminAPIKeyScope{Resource: tables.RbacWildcard, Operation: "Read"}

	for _, tc := range []struct {
		resource, operation string
		want                bool
	}{
		{"ModelProvider", "Read", true},
		{"anything", "Read", true},
		{"", "Read", true}, // wildcard resource covers unmapped ("") resources
		{"ModelProvider", "Create", false},
	} {
		assert.Equal(t, tc.want, perm.Matches(tc.resource, tc.operation), "permission %v", tc)
		assert.Equal(t, tc.want, scope.Matches(tc.resource, tc.operation), "scope %v", tc)
	}

	// Non-wildcard grants never cover an unmapped ("") resource — the
	// middleware's deny-by-default depends on this.
	narrow := tables.TableAdminAPIKeyScope{Resource: "ModelProvider", Operation: tables.RbacWildcard}
	assert.False(t, narrow.Matches("", "Read"))
	// Case-insensitive resource comparison, exact wildcard.
	assert.True(t, narrow.Matches("modelprovider", "Delete"))
}
