package configstore

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupJWTAuthTestStore extends the shared in-memory SQLite store with the
// JWT auth config table.
func setupJWTAuthTestStore(t *testing.T) *RDBConfigStore {
	s := setupRDBTestStore(t)
	require.NoError(t, s.DB().AutoMigrate(&tables.TableJWTAuthConfig{}), "failed to migrate jwt auth config table")
	return s
}

func TestJWTAuthConfig_CRUDRoundTrip(t *testing.T) {
	s := setupJWTAuthTestStore(t)
	ctx := context.Background()

	config := &tables.TableJWTAuthConfig{
		ID:      "jwt-1",
		Name:    "corp-idp",
		Enabled: true,
		Issuer:  "https://idp.example.com",
		JWKSURL: "https://idp.example.com/.well-known/jwks.json",
		Audience: "gateway",
		ClaimMappings: []tables.JWTAuthClaimMapping{
			{Claim: "tenant", Value: "acme", VirtualKeyID: "vk-acme"},
			{Claim: "groups", Value: "*", VirtualKeyID: "vk-any"},
		},
		DefaultVirtualKeyID: "vk-default",
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	require.NoError(t, s.CreateJWTAuthConfig(ctx, config))

	got, err := s.GetJWTAuthConfig(ctx, "jwt-1")
	require.NoError(t, err)
	assert.Equal(t, "https://idp.example.com", got.Issuer)
	require.Len(t, got.ClaimMappings, 2, "claim mappings must round-trip through the JSON column")
	assert.Equal(t, "vk-acme", got.ClaimMappings[0].VirtualKeyID)
	assert.Equal(t, "*", got.ClaimMappings[1].Value)

	// One config per issuer: a second row with the same issuer is rejected.
	dup := &tables.TableJWTAuthConfig{
		ID: "jwt-2", Issuer: "https://idp.example.com", JWKSURL: "https://x/jwks",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	assert.Error(t, s.CreateJWTAuthConfig(ctx, dup))

	got.ClaimMappings = nil
	got.Enabled = false
	require.NoError(t, s.UpdateJWTAuthConfig(ctx, got))
	got, err = s.GetJWTAuthConfig(ctx, "jwt-1")
	require.NoError(t, err)
	assert.Empty(t, got.ClaimMappings)
	assert.False(t, got.Enabled)

	configs, err := s.GetJWTAuthConfigs(ctx)
	require.NoError(t, err)
	require.Len(t, configs, 1)

	require.NoError(t, s.DeleteJWTAuthConfig(ctx, "jwt-1"))
	_, err = s.GetJWTAuthConfig(ctx, "jwt-1")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, s.DeleteJWTAuthConfig(ctx, "jwt-1"), ErrNotFound)
}

func TestJWTAuthConfig_Validation(t *testing.T) {
	s := setupJWTAuthTestStore(t)
	ctx := context.Background()
	now := time.Now()

	// Missing issuer / jwks_url rejected.
	assert.Error(t, s.CreateJWTAuthConfig(ctx, &tables.TableJWTAuthConfig{
		ID: "x", JWKSURL: "https://x/jwks", CreatedAt: now, UpdatedAt: now,
	}))
	assert.Error(t, s.CreateJWTAuthConfig(ctx, &tables.TableJWTAuthConfig{
		ID: "x", Issuer: "https://x", CreatedAt: now, UpdatedAt: now,
	}))

	// Mapping rules must name a claim and a virtual key.
	assert.Error(t, s.CreateJWTAuthConfig(ctx, &tables.TableJWTAuthConfig{
		ID: "x", Issuer: "https://x", JWKSURL: "https://x/jwks",
		ClaimMappings: []tables.JWTAuthClaimMapping{{Claim: "", Value: "v", VirtualKeyID: "vk"}},
		CreatedAt:     now, UpdatedAt: now,
	}))
	assert.Error(t, s.CreateJWTAuthConfig(ctx, &tables.TableJWTAuthConfig{
		ID: "x", Issuer: "https://x", JWKSURL: "https://x/jwks",
		ClaimMappings: []tables.JWTAuthClaimMapping{{Claim: "tenant", Value: "v", VirtualKeyID: ""}},
		CreatedAt:     now, UpdatedAt: now,
	}))
}
