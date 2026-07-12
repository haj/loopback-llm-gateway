package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var auditExportTestKey = []byte("audit-export-test-key")

// testAuditEvent returns a signed audit event with a deterministic ID.
func testAuditEvent(i int) configstoreTables.TableAuditLog {
	entry := configstoreTables.TableAuditLog{
		ID:        fmt.Sprintf("evt-%04d", i),
		Action:    "virtual_key.create",
		Outcome:   configstoreTables.AuditOutcomeSuccess,
		Actor:     "tester",
		Target:    fmt.Sprintf("vk-%04d", i),
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second),
	}
	entry.Sign(auditExportTestKey)
	return entry
}

// fakeDestination records every batch it receives. gate (optional) blocks
// Write until released, letting tests wedge the flush goroutine to force
// queue-full drops.
type fakeDestination struct {
	mu      sync.Mutex
	batches [][]configstoreTables.TableAuditLog
	gate    chan struct{}
	failN   int // fail the first N writes
	closed  bool
}

func (d *fakeDestination) Name() string { return "fake" }

func (d *fakeDestination) Write(_ context.Context, events []configstoreTables.TableAuditLog) error {
	if d.gate != nil {
		<-d.gate
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failN > 0 {
		d.failN--
		return fmt.Errorf("injected write failure")
	}
	batch := make([]configstoreTables.TableAuditLog, len(events))
	copy(batch, events)
	d.batches = append(d.batches, batch)
	return nil
}

func (d *fakeDestination) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

func (d *fakeDestination) allEvents() []configstoreTables.TableAuditLog {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []configstoreTables.TableAuditLog
	for _, b := range d.batches {
		out = append(out, b...)
	}
	return out
}

func (d *fakeDestination) batchCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.batches)
}

func TestAuditExportManager_FlushOnBatchSize(t *testing.T) {
	dest := &fakeDestination{}
	m := newAuditExportManager(dest, 16, 4, time.Hour) // timeout far away: size must trigger
	defer m.Close()

	for i := 0; i < 4; i++ {
		m.Enqueue(testAuditEvent(i))
	}
	require.Eventually(t, func() bool { return len(dest.allEvents()) == 4 }, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, 1, dest.batchCount(), "a full batch must flush as one write")
	assert.Equal(t, uint64(0), m.Dropped())
}

func TestAuditExportManager_FlushOnTimeout(t *testing.T) {
	dest := &fakeDestination{}
	m := newAuditExportManager(dest, 16, 100, 50*time.Millisecond) // size unreachable: timeout must trigger
	defer m.Close()

	m.Enqueue(testAuditEvent(0))
	m.Enqueue(testAuditEvent(1))
	require.Eventually(t, func() bool { return len(dest.allEvents()) == 2 }, 2*time.Second, 10*time.Millisecond)
}

func TestAuditExportManager_DropsWhenQueueFull(t *testing.T) {
	gate := make(chan struct{})
	dest := &fakeDestination{gate: gate}
	// Queue of 2, batch of 1: the first event wedges the flush goroutine in
	// Write, the next two fill the queue, everything after that must drop.
	m := newAuditExportManager(dest, 2, 1, time.Hour)

	// At most 3 events can be absorbed (1 wedged in Write + 2 queued); the
	// rest must drop immediately rather than block the caller.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			m.Enqueue(testAuditEvent(i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Enqueue blocked with a wedged destination — it must always be non-blocking")
	}
	assert.Greater(t, m.Dropped(), uint64(0), "overflow with a wedged destination must drop, not block")

	close(gate) // un-wedge and let Close drain
	require.NoError(t, m.Close())
	assert.True(t, dest.closed)
}

func TestAuditExportManager_DrainsOnClose(t *testing.T) {
	dest := &fakeDestination{}
	m := newAuditExportManager(dest, 64, 100, time.Hour) // neither size nor timeout will fire

	for i := 0; i < 7; i++ {
		m.Enqueue(testAuditEvent(i))
	}
	require.NoError(t, m.Close())
	assert.Len(t, dest.allEvents(), 7, "Close must drain the queue and flush the final partial batch")
	assert.True(t, dest.closed, "Close must close the destination")
	require.NoError(t, m.Close(), "Close must be idempotent")
}

func TestAuditExportManager_RetriesThenRecovers(t *testing.T) {
	dest := &fakeDestination{failN: 2}
	m := newAuditExportManager(dest, 16, 1, time.Hour)
	m.retryBackoff = time.Millisecond // keep the test fast
	defer m.Close()

	m.Enqueue(testAuditEvent(0))
	require.Eventually(t, func() bool { return len(dest.allEvents()) == 1 }, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, uint64(0), m.Dropped(), "a write that succeeds within the retry budget must not count as dropped")
}

func TestAuditExportManager_DropsAfterRetryBudget(t *testing.T) {
	dest := &fakeDestination{failN: 100} // more failures than the retry budget
	m := newAuditExportManager(dest, 16, 1, time.Hour)
	m.retryBackoff = time.Millisecond
	defer m.Close()

	m.Enqueue(testAuditEvent(0))
	require.Eventually(t, func() bool { return m.Dropped() == 1 }, 2*time.Second, 10*time.Millisecond)
	assert.Empty(t, dest.allEvents())
}

func TestJSONLFileDestination_RoundTripVerifiable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "audit.jsonl")
	dest, err := newJSONLFileDestination(path)
	require.NoError(t, err, "parent directories must be created")

	batch := []configstoreTables.TableAuditLog{testAuditEvent(0), testAuditEvent(1)}
	require.NoError(t, dest.Write(context.Background(), batch))
	require.NoError(t, dest.Write(context.Background(), []configstoreTables.TableAuditLog{testAuditEvent(2)}))
	require.NoError(t, dest.Close())

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var rows []configstoreTables.TableAuditLog
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row configstoreTables.TableAuditLog
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &row))
		rows = append(rows, row)
	}
	require.NoError(t, scanner.Err())

	require.Len(t, rows, 3, "one JSONL line per event")
	for i, row := range rows {
		assert.Equal(t, fmt.Sprintf("evt-%04d", i), row.ID)
		assert.NotEmpty(t, row.Signature, "exported rows must carry the signature column")
		assert.True(t, row.VerifySignature(auditExportTestKey),
			"archived row %s must be independently verifiable offline", row.ID)
	}

	// A closed destination reports an error instead of silently dropping.
	assert.Error(t, dest.Write(context.Background(), batch))
	assert.NoError(t, dest.Close(), "Close must be idempotent")
}

func TestJSONLFileDestination_RequiresPath(t *testing.T) {
	_, err := newJSONLFileDestination("")
	assert.Error(t, err)
}

func TestAuditSinkRegistry_FanOut(t *testing.T) {
	received := make(chan configstoreTables.TableAuditLog, 4)
	sink := auditSinkFunc(func(entry configstoreTables.TableAuditLog) { received <- entry })

	SetAuditSink("test-sink", sink)
	defer SetAuditSink("test-sink", nil)

	entry := testAuditEvent(0)
	auditFanOut(&entry)
	select {
	case got := <-received:
		assert.Equal(t, entry.ID, got.ID)
	case <-time.After(time.Second):
		t.Fatal("registered sink did not receive the fanned-out event")
	}

	// Removing the sink restores the default-off state: fan-out is a no-op.
	SetAuditSink("test-sink", nil)
	auditFanOut(&entry)
	select {
	case <-received:
		t.Fatal("unregistered sink must not receive events")
	case <-time.After(50 * time.Millisecond):
	}

	// Nil-safety.
	auditFanOut(nil)
}

// auditSinkFunc adapts a func to the AuditSink interface.
type auditSinkFunc func(entry configstoreTables.TableAuditLog)

func (f auditSinkFunc) Enqueue(entry configstoreTables.TableAuditLog) { f(entry) }
