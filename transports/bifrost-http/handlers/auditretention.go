// This file contains the audit-log retention worker: a background routine
// (modeled on framework/logstore/cleaner.go — 24h interval plus 15-30m jitter,
// batched deletes, mutex + stop channel) that prunes the append-only audit
// trail per the singleton TableAuditLogSettings row. DEFAULT-OFF: with no
// settings row, or with both retention limits at 0, every run is a pure no-op
// that issues no store mutations.
//
// HMAC verifiability across pruning: audit rows are signed individually —
// each Signature is an HMAC-SHA256 over that row's CanonicalEvent, and there is
// deliberately NO chain linking a row to its predecessor. Deleting any subset
// of rows therefore leaves every surviving row's signature verifiable as-is.
// To keep the deletion itself tamper-evident, every prune that removes rows
// appends one signed "audit_log.prune" marker event anchoring the operation:
// the deleted count, the retention parameters, and a SHA-256 digest computed
// over the deleted rows' signatures (oldest-first). An auditor holding a prior
// export can recompute that digest from the archived rows and confirm exactly
// which records the prune removed — and any unexplained gap that lacks a
// matching signed prune marker is evidence of tampering.
package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const (
	auditRetentionInterval  = 24 * time.Hour
	auditRetentionMinJitter = 15 * time.Minute
	auditRetentionMaxJitter = 30 * time.Minute
	auditRetentionBatchSize = 100
	auditRetentionRunBudget = 30 * time.Minute

	// auditRetentionSystemActor identifies scheduled (non-request) prune runs
	// in the prune-marker event.
	auditRetentionSystemActor = "system:audit-retention"
)

// auditPruneResult summarizes one pruneOnce run.
type auditPruneResult struct {
	// DeletedByAge / DeletedByCount are the rows removed by the age cutoff and
	// the max-rows trim respectively.
	DeletedByAge   int64
	DeletedByCount int64
	// Cutoff is the age cutoff used (zero when age pruning was disabled).
	Cutoff time.Time
	// Digest is the hex SHA-256 over the deleted rows' signatures
	// (oldest-first), empty when nothing was deleted.
	Digest string
	// MarkerID is the ID of the signed audit_log.prune event appended for this
	// run, empty when nothing was deleted.
	MarkerID string
}

// Deleted is the total number of audit rows removed.
func (r auditPruneResult) Deleted() int64 { return r.DeletedByAge + r.DeletedByCount }

// AuditRetentionWorker periodically prunes the audit log per the persisted
// settings row. Start/Stop mirror logstore.LogsCleaner. All work happens off
// the request path.
type AuditRetentionWorker struct {
	store configstore.ConfigStore

	mu   sync.Mutex
	stop chan struct{}
}

// NewAuditRetentionWorker creates a retention worker over the given store.
func NewAuditRetentionWorker(store configstore.ConfigStore) *AuditRetentionWorker {
	return &AuditRetentionWorker{store: store}
}

// Start launches the background pruning routine (immediate run, then every 24h
// plus jitter). Safe to call on an already-started worker.
func (w *AuditRetentionWorker) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stop != nil {
		if logger != nil {
			logger.Debug("audit retention routine already running")
		}
		return
	}
	w.stop = make(chan struct{})
	stopCh := w.stop

	go func() {
		w.runOnceWithBudget()
		timer := time.NewTimer(auditRetentionNextRun())
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				w.runOnceWithBudget()
				timer.Reset(auditRetentionNextRun())
			case <-stopCh:
				if logger != nil {
					logger.Info("audit retention routine stopped")
				}
				return
			}
		}
	}()
	if logger != nil {
		logger.Info("audit retention routine started")
	}
}

// Stop gracefully stops the background routine. Safe to call on an
// already-stopped worker.
func (w *AuditRetentionWorker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stop == nil {
		return
	}
	close(w.stop)
	w.stop = nil
}

// runOnceWithBudget runs one scheduled prune with a bounded context.
func (w *AuditRetentionWorker) runOnceWithBudget() {
	ctx, cancel := context.WithTimeout(context.Background(), auditRetentionRunBudget)
	defer cancel()
	result, err := w.pruneOnce(ctx, auditRetentionSystemActor, "")
	if err != nil {
		if logger != nil {
			logger.Error("audit retention run failed: %v", err)
		}
		return
	}
	if result.Deleted() > 0 && logger != nil {
		logger.Info("audit retention pruned %d audit log row(s) (%d by age, %d by row cap)", result.Deleted(), result.DeletedByAge, result.DeletedByCount)
	}
}

// pruneOnce performs a single retention pass: load settings (no-op without a
// row or with both limits at 0 — the default-off invariant), delete by age
// cutoff, trim to the row cap oldest-first (both in batches of 100), then
// append one signed audit_log.prune marker anchoring what was deleted. actor/ip
// attribute the marker (the manual prune endpoint passes the caller; the
// scheduler passes auditRetentionSystemActor).
func (w *AuditRetentionWorker) pruneOnce(ctx context.Context, actor, ip string) (auditPruneResult, error) {
	var result auditPruneResult
	if w == nil || w.store == nil {
		return result, nil
	}
	settings, err := w.store.GetAuditLogSettings(ctx)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return result, nil // feature unconfigured: strict no-op
		}
		return result, err
	}
	if settings == nil || (settings.RetentionMaxAgeDays <= 0 && settings.RetentionMaxRows <= 0) {
		return result, nil
	}

	digest := sha256.New()

	if settings.RetentionMaxAgeDays > 0 {
		result.Cutoff = time.Now().UTC().AddDate(0, 0, -settings.RetentionMaxAgeDays)
		deleted, err := w.pruneLoop(ctx, digest, func() (int64, []string, error) {
			return w.store.DeleteAuditLogsBefore(ctx, result.Cutoff, auditRetentionBatchSize)
		})
		result.DeletedByAge = deleted
		if err != nil {
			return result, err
		}
	}

	if settings.RetentionMaxRows > 0 {
		deleted, err := w.pruneLoop(ctx, digest, func() (int64, []string, error) {
			return w.store.TrimAuditLogsToCount(ctx, settings.RetentionMaxRows, auditRetentionBatchSize)
		})
		result.DeletedByCount = deleted
		if err != nil {
			return result, err
		}
	}

	if result.Deleted() == 0 {
		return result, nil
	}
	result.Digest = hex.EncodeToString(digest.Sum(nil))

	// Anchor the deletion: one signed marker event per prune that removed rows.
	// Written through appendAuditLog so it is signed, persisted, and fanned out
	// to export sinks like any other audit event.
	marker := &configstoreTables.TableAuditLog{
		ID:      uuid.NewString(),
		Action:  AuditActionAuditLogPrune,
		Outcome: configstoreTables.AuditOutcomeSuccess,
		Actor:   actor,
		IP:      ip,
		Target: fmt.Sprintf(
			"deleted=%d deleted_by_age=%d deleted_by_rows=%d max_age_days=%d max_rows=%d cutoff=%s deleted_signatures_digest=sha256:%s",
			result.Deleted(), result.DeletedByAge, result.DeletedByCount,
			settings.RetentionMaxAgeDays, settings.RetentionMaxRows,
			formatPruneCutoff(result.Cutoff), result.Digest,
		),
		Timestamp: time.Now(),
	}
	if err := appendAuditLog(ctx, w.store, marker); err != nil {
		return result, fmt.Errorf("pruned %d audit log row(s) but failed to append prune marker: %w", result.Deleted(), err)
	}
	result.MarkerID = marker.ID
	return result, nil
}

// pruneLoop repeatedly invokes a batched delete until it reports no more rows,
// folding each deleted row's signature into the running digest (oldest-first,
// one signature per line — the same order an auditor recomputes from an
// archived export).
func (w *AuditRetentionWorker) pruneLoop(ctx context.Context, digest hash.Hash, deleteBatch func() (int64, []string, error)) (int64, error) {
	var total int64
	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		deleted, signatures, err := deleteBatch()
		if err != nil {
			return total, err
		}
		if deleted == 0 {
			return total, nil
		}
		total += deleted
		for _, sig := range signatures {
			digest.Write([]byte(sig))
			digest.Write([]byte("\n"))
		}
		if deleted < auditRetentionBatchSize {
			return total, nil
		}
	}
}

// formatPruneCutoff renders the cutoff for the marker Target ("-" when age
// pruning was disabled).
func formatPruneCutoff(cutoff time.Time) string {
	if cutoff.IsZero() {
		return "-"
	}
	return cutoff.UTC().Format(time.RFC3339)
}

// auditRetentionNextRun returns 24 hours plus a random 15-30 minute jitter.
func auditRetentionNextRun() time.Duration {
	jitter := auditRetentionMinJitter + time.Duration(rand.Int63n(int64(auditRetentionMaxJitter-auditRetentionMinJitter)))
	return auditRetentionInterval + jitter
}
