// This file contains the async audit-log export pipeline and the audit sink
// fan-out seam.
//
// Fan-out seam: every successfully persisted audit event (see appendAuditLog in
// audit.go) is offered to a small named registry of AuditSinks. The registry is
// nil-safe and default-empty — with no sinks registered the audit write path is
// byte-for-byte unchanged. The export manager below registers itself under
// auditExportSinkName; future consumers (e.g. the alert-channels publisher)
// plug in with SetAuditSink under their own name without touching recordAudit.
//
// Export pipeline: an auditExportManager tails the sink seam into an
// AuditExportDestination (JSONL file and syslog today; S3/GCS drop in later by
// implementing the same interface). It replicates the bounded-queue /
// batch-on-size-or-timeout / bounded-backoff-retry / drop-with-counter producer
// pattern from plugins/loopbackkafka/producer.go so a stuck destination can
// never apply backpressure to the governance mutation path.
//
// Exported JSONL rows carry the Signature column, so archives remain
// independently verifiable offline with the HMAC key (auditHMACKey).
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// ---- audit sink fan-out seam ----

// AuditSink receives a copy of every audit event that was successfully
// persisted by appendAuditLog. Enqueue MUST be non-blocking (buffer-and-drop):
// it runs on the governance mutation path.
type AuditSink interface {
	Enqueue(entry configstoreTables.TableAuditLog)
}

var (
	auditSinksMu       sync.Mutex
	auditSinksByName   = map[string]AuditSink{}
	auditSinksSnapshot atomic.Pointer[[]AuditSink]
)

// SetAuditSink installs (or replaces) the audit sink registered under name; a
// nil sink removes the registration. The read path (auditFanOut) works off an
// atomic snapshot, so swaps are safe under concurrent audit writes. Callers own
// the lifecycle of any sink they replace (e.g. closing a superseded export
// manager).
func SetAuditSink(name string, sink AuditSink) {
	auditSinksMu.Lock()
	defer auditSinksMu.Unlock()
	if sink == nil {
		delete(auditSinksByName, name)
	} else {
		auditSinksByName[name] = sink
	}
	if len(auditSinksByName) == 0 {
		auditSinksSnapshot.Store(nil)
		return
	}
	snapshot := make([]AuditSink, 0, len(auditSinksByName))
	for _, s := range auditSinksByName {
		snapshot = append(snapshot, s)
	}
	auditSinksSnapshot.Store(&snapshot)
}

// auditFanOut offers a persisted audit event to every registered sink. Nil-safe
// and free when no sinks are registered (the default-off state).
func auditFanOut(entry *configstoreTables.TableAuditLog) {
	sinks := auditSinksSnapshot.Load()
	if sinks == nil || entry == nil {
		return
	}
	for _, sink := range *sinks {
		sink.Enqueue(*entry)
	}
}

// ---- export destination interface ----

// AuditExportDestination is the pluggable write target for exported audit
// events. Write receives an ordered batch; implementations must be safe for a
// single writer goroutine. The interface is deliberately batch-shaped so
// object-store destinations (S3/GCS multipart appends) drop in without
// reshaping the manager.
type AuditExportDestination interface {
	// Name identifies the destination in logs ("file", "syslog", ...).
	Name() string
	Write(ctx context.Context, events []configstoreTables.TableAuditLog) error
	Close() error
}

// ---- JSONL file destination ----

// jsonlFileDestination appends one JSON object per line to a local file. Rows
// include the signature field so the archive is independently verifiable.
type jsonlFileDestination struct {
	path string
	mu   sync.Mutex
	file *os.File
}

// newJSONLFileDestination opens (creating parent directories as needed) the
// append-only JSONL export file.
func newJSONLFileDestination(path string) (*jsonlFileDestination, error) {
	if path == "" {
		return nil, fmt.Errorf("audit export file path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create audit export directory: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open audit export file: %w", err)
	}
	return &jsonlFileDestination{path: path, file: f}, nil
}

func (d *jsonlFileDestination) Name() string { return "file" }

func (d *jsonlFileDestination) Write(_ context.Context, events []configstoreTables.TableAuditLog) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file == nil {
		return fmt.Errorf("audit export file %s is closed", d.path)
	}
	for i := range events {
		line, err := json.Marshal(&events[i])
		if err != nil {
			return fmt.Errorf("failed to marshal audit event %s: %w", events[i].ID, err)
		}
		if _, err := d.file.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func (d *jsonlFileDestination) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file == nil {
		return nil
	}
	err := d.file.Close()
	d.file = nil
	return err
}

// ---- async export manager ----

// Export manager defaults. The queue is deliberately generous relative to audit
// volume (one event per governance mutation) so drops only occur when the
// destination is stuck, and small batches keep the tail near-live.
const (
	auditExportQueueSize    = 1024
	auditExportBatchSize    = 32
	auditExportBatchTimeout = 2 * time.Second
	auditExportMaxRetries   = 3
	auditExportRetryBackoff = 250 * time.Millisecond

	// auditExportSinkName is the fan-out registry slot owned by the export
	// pipeline. Other features must register under their own name.
	auditExportSinkName = "export"
)

// auditExportManager is the transport layer of the export pipeline: a bounded
// queue fronting a single batching/retrying flush goroutine, mirroring
// plugins/loopbackkafka/producer.go. Enqueue is non-blocking; when the queue is
// full the event is dropped and counted so the mutation path never stalls.
type auditExportManager struct {
	dest         AuditExportDestination
	queue        chan configstoreTables.TableAuditLog
	batchSize    int
	batchTimeout time.Duration
	maxRetries   int
	retryBackoff time.Duration

	wg        sync.WaitGroup
	done      chan struct{}
	closeOnce sync.Once
	dropped   atomic.Uint64
}

// newAuditExportManager starts the background flush goroutine and returns the
// manager. Zero/negative tuning values fall back to the package defaults.
func newAuditExportManager(dest AuditExportDestination, queueSize, batchSize int, batchTimeout time.Duration) *auditExportManager {
	if queueSize <= 0 {
		queueSize = auditExportQueueSize
	}
	if batchSize <= 0 {
		batchSize = auditExportBatchSize
	}
	if batchTimeout <= 0 {
		batchTimeout = auditExportBatchTimeout
	}
	m := &auditExportManager{
		dest:         dest,
		queue:        make(chan configstoreTables.TableAuditLog, queueSize),
		batchSize:    batchSize,
		batchTimeout: batchTimeout,
		maxRetries:   auditExportMaxRetries,
		retryBackoff: auditExportRetryBackoff,
		done:         make(chan struct{}),
	}
	m.wg.Add(1)
	go m.run()
	return m
}

// Enqueue offers an event to the bounded queue without blocking. When the
// queue is full (the destination cannot keep up) the event is dropped and the
// dropped counter incremented — export must never apply backpressure to the
// governance mutation path.
func (m *auditExportManager) Enqueue(entry configstoreTables.TableAuditLog) {
	select {
	case m.queue <- entry:
	default:
		n := m.dropped.Add(1)
		// Log on the first drop and then every 100th to avoid log floods.
		if logger != nil && (n == 1 || n%100 == 0) {
			logger.Warn("audit export queue full, dropped %d event(s) (destination %s)", n, m.dest.Name())
		}
	}
}

// Dropped reports the number of events dropped due to a full queue or an
// exhausted retry budget.
func (m *auditExportManager) Dropped() uint64 { return m.dropped.Load() }

// run is the single batching goroutine. It flushes a batch when it reaches
// batchSize or when batchTimeout elapses with a non-empty batch.
func (m *auditExportManager) run() {
	defer m.wg.Done()
	batch := make([]configstoreTables.TableAuditLog, 0, m.batchSize)
	timer := time.NewTimer(m.batchTimeout)
	defer timer.Stop()

	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(m.batchTimeout)
	}

	for {
		select {
		case entry := <-m.queue:
			batch = append(batch, entry)
			if len(batch) >= m.batchSize {
				m.flush(batch)
				batch = batch[:0]
				resetTimer()
			}
		case <-timer.C:
			if len(batch) > 0 {
				m.flush(batch)
				batch = batch[:0]
			}
			timer.Reset(m.batchTimeout)
		case <-m.done:
			// Drain whatever is queued, then flush the final partial batch.
			for {
				select {
				case entry := <-m.queue:
					batch = append(batch, entry)
					if len(batch) >= m.batchSize {
						m.flush(batch)
						batch = batch[:0]
					}
				default:
					if len(batch) > 0 {
						m.flush(batch)
					}
					return
				}
			}
		}
	}
}

// flush writes a batch with bounded exponential backoff. A write that still
// fails after maxRetries is dropped (counted) rather than retried forever.
func (m *auditExportManager) flush(batch []configstoreTables.TableAuditLog) {
	if len(batch) == 0 {
		return
	}
	// Copy: the caller reuses the backing array after flush returns.
	events := make([]configstoreTables.TableAuditLog, len(batch))
	copy(events, batch)

	backoff := m.retryBackoff
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := m.dest.Write(ctx, events)
		cancel()
		if err == nil {
			return
		}
		if attempt == m.maxRetries {
			n := m.dropped.Add(uint64(len(events)))
			if logger != nil {
				logger.Error("audit export dropping %d event(s) after %d attempts to destination %s: %v (total dropped %d)", len(events), attempt+1, m.dest.Name(), err, n)
			}
			return
		}
		if logger != nil {
			logger.Warn("audit export write attempt %d to destination %s failed: %v; retrying in %s", attempt+1, m.dest.Name(), err, backoff)
		}
		m.sleep(backoff)
		backoff *= 2
	}
}

// sleep waits for d, returning early if the manager is closing.
func (m *auditExportManager) sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-m.done:
	}
}

// Close stops the flush goroutine (after a final drain/flush) and closes the
// destination. Safe to call more than once.
func (m *auditExportManager) Close() error {
	m.closeOnce.Do(func() {
		close(m.done)
		m.wg.Wait()
	})
	if m.dest != nil {
		return m.dest.Close()
	}
	return nil
}

// ---- settings-driven lifecycle ----

var (
	auditExportCurrentMu sync.Mutex
	auditExportCurrent   *auditExportManager
)

// buildAuditExportDestination constructs the destination described by the
// settings row. Returns an error for unusable configurations so callers can
// reject a PUT before persisting it.
func buildAuditExportDestination(settings *configstoreTables.TableAuditLogSettings) (AuditExportDestination, error) {
	switch settings.ExportType {
	case configstoreTables.AuditExportTypeFile:
		return newJSONLFileDestination(settings.ExportFilePath)
	case configstoreTables.AuditExportTypeSyslog:
		return newSyslogDestination(settings.SyslogNetwork, settings.SyslogAddress, settings.SyslogTag)
	default:
		return nil, fmt.Errorf("unsupported audit export type %q", settings.ExportType)
	}
}

// applyAuditExportSettings installs, replaces, or removes the "export" audit
// sink to match the settings row. A nil settings row or ExportEnabled=false
// removes the sink (the default-off state). The superseded manager is closed
// after being unregistered so its final batch flushes.
func applyAuditExportSettings(settings *configstoreTables.TableAuditLogSettings) error {
	var next *auditExportManager
	if settings != nil && settings.ExportEnabled {
		dest, err := buildAuditExportDestination(settings)
		if err != nil {
			return err
		}
		next = newAuditExportManager(dest, 0, 0, 0)
	}

	auditExportCurrentMu.Lock()
	previous := auditExportCurrent
	auditExportCurrent = next
	if next != nil {
		SetAuditSink(auditExportSinkName, next)
	} else {
		SetAuditSink(auditExportSinkName, nil)
	}
	auditExportCurrentMu.Unlock()

	if previous != nil {
		if err := previous.Close(); err != nil && logger != nil {
			logger.Warn("failed to close previous audit export destination: %v", err)
		}
	}
	if next != nil && logger != nil {
		logger.Info("audit export enabled (destination %s)", next.dest.Name())
	}
	return nil
}

// ShutdownAuditExport unregisters and closes the live export manager (if any),
// flushing its final batch. Called on server shutdown.
func ShutdownAuditExport() {
	if err := applyAuditExportSettings(nil); err != nil && logger != nil {
		logger.Warn("failed to shut down audit export: %v", err)
	}
}

// LoadAuditExportSettings reads the persisted settings row and applies its
// export section. Best-effort startup hook: a missing row (default-off) is not
// an error, and failures only log — export can never block server startup.
func LoadAuditExportSettings(ctx context.Context, store configstore.ConfigStore) {
	if store == nil {
		return
	}
	settings, err := store.GetAuditLogSettings(ctx)
	if err != nil {
		if !errors.Is(err, configstore.ErrNotFound) && logger != nil {
			logger.Warn("failed to load audit log settings: %v", err)
		}
		return
	}
	if err := applyAuditExportSettings(settings); err != nil && logger != nil {
		logger.Warn("failed to apply audit export settings: %v", err)
	}
}
