package alerting

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeChannelStore scripts the channel list and records status writebacks.
type fakeChannelStore struct {
	mu       sync.Mutex
	channels []configstoreTables.TableAlertChannel
	statuses []string // "id|status" in write order
}

func (s *fakeChannelStore) GetAlertChannels(_ context.Context) ([]configstoreTables.TableAlertChannel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]configstoreTables.TableAlertChannel, len(s.channels))
	copy(out, s.channels)
	return out, nil
}

func (s *fakeChannelStore) UpdateAlertChannelDeliveryStatus(_ context.Context, id string, _ time.Time, status string, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses = append(s.statuses, id+"|"+status)
	return nil
}

func (s *fakeChannelStore) setChannels(channels ...configstoreTables.TableAlertChannel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels = channels
}

func (s *fakeChannelStore) statusLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.statuses))
	copy(out, s.statuses)
	return out
}

// lockedClock is a race-safe controllable clock shared between the test and
// the worker goroutine.
type lockedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *lockedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *lockedClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestDispatcher returns a started dispatcher with instant sleep and a
// controllable clock, plus the fake store.
func newTestDispatcher(t *testing.T, store *fakeChannelStore) (*Dispatcher, *lockedClock) {
	t.Helper()
	clock := &lockedClock{now: time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)}
	d := NewDispatcher(store, nil)
	d.sleep = func(time.Duration) {}
	d.now = clock.Now
	d.Start(context.Background())
	t.Cleanup(d.Stop)
	return d, clock
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 3*time.Second, 5*time.Millisecond)
}

func TestDispatcher_ZeroChannelsPublishIsNoOp(t *testing.T) {
	store := &fakeChannelStore{}
	d, _ := newTestDispatcher(t, store)

	d.Publish(testEvent())
	time.Sleep(20 * time.Millisecond)
	assert.Empty(t, store.statusLog(), "no channels means no delivery attempts")
	assert.Zero(t, d.Dropped())

	// Nil receiver is safe (default-off wiring passes a nil dispatcher around).
	var nilDispatcher *Dispatcher
	nilDispatcher.Publish(testEvent())
}

func TestDispatcher_DeliversToMatchingChannelsOnly(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	store := &fakeChannelStore{}
	store.setChannels(
		configstoreTables.TableAlertChannel{
			ID: "ch-match", Type: configstoreTables.AlertChannelTypeWebhook, Enabled: true, EndpointURL: srv.URL,
		},
		configstoreTables.TableAlertChannel{
			ID: "ch-filtered", Type: configstoreTables.AlertChannelTypeWebhook, Enabled: true, EndpointURL: srv.URL,
			EventTypes: []string{EventTypeCircuitBreakerOpen}, // does not admit budget.exceeded
		},
		configstoreTables.TableAlertChannel{
			ID: "ch-disabled", Type: configstoreTables.AlertChannelTypeWebhook, Enabled: false, EndpointURL: srv.URL,
		},
	)
	d, _ := newTestDispatcher(t, store)
	require.NoError(t, d.Reload(context.Background()))

	d.Publish(testEvent())
	waitFor(t, func() bool { return len(store.statusLog()) == 1 })
	assert.Equal(t, []string{"ch-match|ok"}, store.statusLog())
	assert.Equal(t, int32(1), hits.Load())
}

func TestDispatcher_DedupSuppressionWindow(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	store := &fakeChannelStore{}
	store.setChannels(configstoreTables.TableAlertChannel{
		ID: "ch", Type: configstoreTables.AlertChannelTypeWebhook, Enabled: true, EndpointURL: srv.URL,
	})
	d, clock := newTestDispatcher(t, store)
	require.NoError(t, d.Reload(context.Background()))

	// Same DedupKey twice inside the window: second is suppressed.
	d.Publish(testEvent())
	d.Publish(testEvent())
	waitFor(t, func() bool { return len(store.statusLog()) == 1 })
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(1), hits.Load(), "repeat inside the dedup window must be suppressed")

	// A different DedupKey is not suppressed.
	other := testEvent()
	other.DedupKey = "budget.exceeded|vk:vk-2"
	d.Publish(other)
	waitFor(t, func() bool { return hits.Load() == 2 })

	// After the window elapses the original key fires again.
	clock.Advance(defaultDedupWindow + time.Second)
	d.Publish(testEvent())
	waitFor(t, func() bool { return hits.Load() == 3 })

	// An empty DedupKey is never suppressed.
	unkeyed := testEvent()
	unkeyed.DedupKey = ""
	d.Publish(unkeyed)
	d.Publish(unkeyed)
	waitFor(t, func() bool { return hits.Load() == 5 })
}

func TestDispatcher_ReloadPicksUpChannelChanges(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	store := &fakeChannelStore{}
	d, _ := newTestDispatcher(t, store)

	// No channels yet: no-op.
	d.Publish(testEvent())
	time.Sleep(20 * time.Millisecond)
	assert.Zero(t, hits.Load())

	// Channel appears; Reload makes it live without a restart.
	store.setChannels(configstoreTables.TableAlertChannel{
		ID: "ch-new", Type: configstoreTables.AlertChannelTypeWebhook, Enabled: true, EndpointURL: srv.URL,
	})
	require.NoError(t, d.Reload(context.Background()))
	d.Publish(testEvent())
	waitFor(t, func() bool { return hits.Load() == 1 })

	// Channel removed; Reload restores the no-op state.
	store.setChannels()
	require.NoError(t, d.Reload(context.Background()))
	d.Publish(testEvent())
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(1), hits.Load())
}

func TestDispatcher_FailedDeliveryWritesFailedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400) // permanent: one attempt
	}))
	t.Cleanup(srv.Close)

	store := &fakeChannelStore{}
	store.setChannels(configstoreTables.TableAlertChannel{
		ID: "ch-bad", Type: configstoreTables.AlertChannelTypeWebhook, Enabled: true, EndpointURL: srv.URL,
	})
	d, _ := newTestDispatcher(t, store)
	require.NoError(t, d.Reload(context.Background()))

	d.Publish(testEvent())
	waitFor(t, func() bool { return len(store.statusLog()) == 1 })
	assert.Equal(t, []string{"ch-bad|failed"}, store.statusLog())
}

func TestDispatcher_NonBlockingPublishWhenQueueFull(t *testing.T) {
	// A destination that blocks until released wedges the worker so the queue
	// fills; publishes past capacity must drop, not block.
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-gate
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(gate) })

	store := &fakeChannelStore{}
	store.setChannels(configstoreTables.TableAlertChannel{
		ID: "ch", Type: configstoreTables.AlertChannelTypeWebhook, Enabled: true, EndpointURL: srv.URL,
	})
	d, _ := newTestDispatcher(t, store)
	require.NoError(t, d.Reload(context.Background()))

	done := make(chan struct{})
	go func() {
		for i := 0; i < dispatcherQueueSize+16; i++ {
			ev := testEvent()
			ev.DedupKey = "" // avoid dedup so every event reaches the queue
			d.Publish(ev)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish blocked with a wedged destination — it must always be non-blocking")
	}
	assert.Greater(t, d.Dropped(), uint64(0))
}
