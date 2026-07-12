package configstore

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupAlertChannelTestStore extends the shared in-memory SQLite store with
// the alert_channels table.
func setupAlertChannelTestStore(t *testing.T) *RDBConfigStore {
	s := setupRDBTestStore(t)
	require.NoError(t, s.DB().AutoMigrate(&tables.TableAlertChannel{}), "failed to migrate alert channel table")
	return s
}

func TestAlertChannel_CRUDRoundTrip(t *testing.T) {
	s := setupAlertChannelTestStore(t)
	ctx := context.Background()

	channel := &tables.TableAlertChannel{
		ID:          "ch-1",
		Name:        "ops-slack",
		Type:        tables.AlertChannelTypeSlack,
		Enabled:     true,
		EndpointURL: "https://hooks.slack.com/services/T000/B000/XXX",
		Secret:      "super-secret",
		EventTypes:  []string{"budget.exceeded", "circuit_breaker.open"},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, s.CreateAlertChannel(ctx, channel))

	// Read back: EventTypesJSON deserializes and the secret round-trips.
	got, err := s.GetAlertChannel(ctx, "ch-1")
	require.NoError(t, err)
	assert.Equal(t, "ops-slack", got.Name)
	assert.Equal(t, []string{"budget.exceeded", "circuit_breaker.open"}, got.EventTypes)
	assert.Equal(t, "super-secret", got.Secret)
	assert.True(t, got.WantsEvent("budget.exceeded"))
	assert.False(t, got.WantsEvent("audit.mutation"))

	// Empty filter admits everything.
	got.EventTypes = nil
	require.NoError(t, s.UpdateAlertChannel(ctx, got))
	got, err = s.GetAlertChannel(ctx, "ch-1")
	require.NoError(t, err)
	assert.Empty(t, got.EventTypes)
	assert.True(t, got.WantsEvent("audit.mutation"))

	// List, then delete.
	channels, err := s.GetAlertChannels(ctx)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.NoError(t, s.DeleteAlertChannel(ctx, "ch-1"))
	_, err = s.GetAlertChannel(ctx, "ch-1")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, s.DeleteAlertChannel(ctx, "ch-1"), ErrNotFound)
}

func TestAlertChannel_RejectsUnknownType(t *testing.T) {
	s := setupAlertChannelTestStore(t)
	err := s.CreateAlertChannel(context.Background(), &tables.TableAlertChannel{
		ID: "ch-bad", Name: "x", Type: "carrier-pigeon",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	assert.Error(t, err)
}

func TestAlertChannel_DeliveryStatusWritebackDoesNotTouchDefinition(t *testing.T) {
	s := setupAlertChannelTestStore(t)
	ctx := context.Background()

	channel := &tables.TableAlertChannel{
		ID: "ch-1", Name: "hook", Type: tables.AlertChannelTypeWebhook,
		Enabled: true, EndpointURL: "https://example.com/hook", Secret: "sig-key",
		EventTypes: []string{"budget.exceeded"},
		CreatedAt:  time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, s.CreateAlertChannel(ctx, channel))

	attemptAt := time.Now()
	require.NoError(t, s.UpdateAlertChannelDeliveryStatus(ctx, "ch-1", attemptAt, "failed", "HTTP 500"))

	got, err := s.GetAlertChannel(ctx, "ch-1")
	require.NoError(t, err)
	assert.Equal(t, "failed", got.LastStatus)
	assert.Equal(t, "HTTP 500", got.LastError)
	require.NotNil(t, got.LastAttemptAt)
	// The definition fields survive the columns-only writeback untouched.
	assert.Equal(t, "sig-key", got.Secret)
	assert.Equal(t, []string{"budget.exceeded"}, got.EventTypes)

	assert.ErrorIs(t, s.UpdateAlertChannelDeliveryStatus(ctx, "missing", attemptAt, "ok", ""), ErrNotFound)
}
