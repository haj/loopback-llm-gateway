// This file contains the async alert Dispatcher: a bounded queue fronting a
// single worker goroutine that fans each event out to the enabled channels,
// with per-(channel, DedupKey) suppression so repeating conditions (every
// rejected request during a budget breach, breaker flapping) produce one alert
// per window instead of a storm.
//
// Producers (recordAudit, the governance violation bridge, the circuit-breaker
// bridge) call Publish, which is select/default non-blocking and a cheap
// atomic no-op while zero channels are configured — the DEFAULT-OFF state.
package alerting

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const (
	dispatcherQueueSize   = 256
	defaultDedupWindow    = 5 * time.Minute
	deliveryStatusOK      = "ok"
	deliveryStatusFailed  = "failed"
	dedupCleanupThreshold = 4096
)

// ChannelStore is the narrow configstore surface the dispatcher needs: the
// enabled-channel list on Reload and best-effort delivery-status writeback.
// *configstore.RDBConfigStore satisfies it; tests substitute a fake.
type ChannelStore interface {
	GetAlertChannels(ctx context.Context) ([]configstoreTables.TableAlertChannel, error)
	UpdateAlertChannelDeliveryStatus(ctx context.Context, id string, attemptAt time.Time, status string, lastError string) error
}

// Dispatcher owns alert delivery. Construct with NewDispatcher, call Start,
// Reload after every channel mutation, and Stop on shutdown.
type Dispatcher struct {
	store  ChannelStore
	logger schemas.Logger
	client *http.Client

	queue     chan Event
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	dropped   atomic.Uint64

	// channels is the cached enabled-channel snapshot; channelCount mirrors
	// its length so Publish's zero-channel fast path is one atomic load.
	channels     atomic.Pointer[[]configstoreTables.TableAlertChannel]
	channelCount atomic.Int32

	// lastSent tracks the most recent dispatch per channelID+"|"+DedupKey.
	dedupMu     sync.Mutex
	lastSent    map[string]time.Time
	dedupWindow time.Duration

	// now and sleep are injectable for hermetic dedup/backoff tests.
	now   func() time.Time
	sleep func(time.Duration)
}

// NewDispatcher creates a stopped dispatcher over the given store. logger may
// be nil.
func NewDispatcher(store ChannelStore, logger schemas.Logger) *Dispatcher {
	return &Dispatcher{
		store:       store,
		logger:      logger,
		client:      &http.Client{Timeout: senderTimeout},
		queue:       make(chan Event, dispatcherQueueSize),
		done:        make(chan struct{}),
		lastSent:    map[string]time.Time{},
		dedupWindow: defaultDedupWindow,
		now:         time.Now,
		sleep:       time.Sleep,
	}
}

// Start launches the worker goroutine and loads the initial channel snapshot.
func (d *Dispatcher) Start(ctx context.Context) {
	if err := d.Reload(ctx); err != nil && d.logger != nil {
		d.logger.Warn("failed to load alert channels: %v", err)
	}
	d.wg.Add(1)
	go d.run()
}

// Stop terminates the worker. Queued events are abandoned — alert delivery is
// best-effort and must never delay shutdown. Safe to call more than once.
func (d *Dispatcher) Stop() {
	d.closeOnce.Do(func() {
		close(d.done)
		d.wg.Wait()
	})
}

// Reload refreshes the cached enabled-channel snapshot. The handler calls it
// after every channel mutation so changes apply without a restart.
func (d *Dispatcher) Reload(ctx context.Context) error {
	if d.store == nil {
		return nil
	}
	all, err := d.store.GetAlertChannels(ctx)
	if err != nil {
		return err
	}
	enabled := make([]configstoreTables.TableAlertChannel, 0, len(all))
	for _, ch := range all {
		if ch.Enabled {
			enabled = append(enabled, ch)
		}
	}
	d.channels.Store(&enabled)
	d.channelCount.Store(int32(len(enabled)))
	return nil
}

// Dropped reports events discarded because the queue was full.
func (d *Dispatcher) Dropped() uint64 { return d.dropped.Load() }

// Publish offers an event for delivery. Non-blocking by contract: with zero
// channels it is a single atomic load, and with a full queue the event is
// dropped and counted — producers sit on request paths and must never stall.
func (d *Dispatcher) Publish(event Event) {
	if d == nil || d.channelCount.Load() == 0 {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = d.now()
	}
	select {
	case d.queue <- event:
	default:
		n := d.dropped.Add(1)
		if d.logger != nil && (n == 1 || n%100 == 0) {
			d.logger.Warn("alert queue full, dropped %d event(s)", n)
		}
	}
}

// run is the single worker goroutine.
func (d *Dispatcher) run() {
	defer d.wg.Done()
	for {
		select {
		case event := <-d.queue:
			d.dispatch(event)
		case <-d.done:
			return
		}
	}
}

// dispatch fans one event out to every enabled channel whose filter admits it
// and whose dedup window has elapsed.
func (d *Dispatcher) dispatch(event Event) {
	snapshot := d.channels.Load()
	if snapshot == nil {
		return
	}
	for i := range *snapshot {
		channel := &(*snapshot)[i]
		if !channel.WantsEvent(event.Type) {
			continue
		}
		if d.suppressed(channel.ID, event.DedupKey) {
			continue
		}
		attemptAt := d.now()
		status, err := deliver(d.client, channel, event, d.sleep)
		if err != nil {
			// Log the channel ID and status only — never the endpoint URL or
			// secret-bearing payload.
			if d.logger != nil {
				d.logger.Warn("alert delivery to channel %s failed (HTTP %d): %v", channel.ID, status, err)
			}
			d.writeStatus(channel.ID, attemptAt, deliveryStatusFailed, err.Error())
			continue
		}
		d.writeStatus(channel.ID, attemptAt, deliveryStatusOK, "")
	}
}

// suppressed records-and-checks the dedup window for one (channel, DedupKey)
// pair. Recording happens at dispatch time — before the delivery outcome is
// known — so a failing destination cannot amplify a storm through retries.
func (d *Dispatcher) suppressed(channelID, dedupKey string) bool {
	if dedupKey == "" {
		return false
	}
	key := channelID + "|" + dedupKey
	now := d.now()

	d.dedupMu.Lock()
	defer d.dedupMu.Unlock()
	if last, ok := d.lastSent[key]; ok && now.Sub(last) < d.dedupWindow {
		return true
	}
	// Opportunistic cleanup keeps the map bounded across long uptimes with
	// high-cardinality dedup keys.
	if len(d.lastSent) >= dedupCleanupThreshold {
		for k, ts := range d.lastSent {
			if now.Sub(ts) >= d.dedupWindow {
				delete(d.lastSent, k)
			}
		}
	}
	d.lastSent[key] = now
	return false
}

// writeStatus persists the last delivery attempt on the channel row,
// best-effort: a bookkeeping failure only logs.
func (d *Dispatcher) writeStatus(channelID string, attemptAt time.Time, status, lastError string) {
	if d.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.store.UpdateAlertChannelDeliveryStatus(ctx, channelID, attemptAt, status, lastError); err != nil && d.logger != nil {
		d.logger.Warn("failed to record alert delivery status for channel %s: %v", channelID, err)
	}
}
