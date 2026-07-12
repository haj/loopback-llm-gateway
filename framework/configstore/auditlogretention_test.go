package configstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupAuditLogTestStore extends the shared in-memory SQLite store with the
// audit log and audit log settings tables.
func setupAuditLogTestStore(t *testing.T) *RDBConfigStore {
	s := setupRDBTestStore(t)
	require.NoError(t, s.DB().AutoMigrate(
		&tables.TableAuditLog{},
		&tables.TableAuditLogSettings{},
	), "failed to migrate audit log tables")
	return s
}

var auditTestHMACKey = []byte("audit-retention-test-key")

// seedAuditLogs writes n signed audit rows with strictly increasing timestamps
// (one minute apart, oldest first, starting at base) and returns them in
// insertion order. IDs are zero-padded so lexicographic and chronological order
// agree.
func seedAuditLogs(t *testing.T, s *RDBConfigStore, n int, base time.Time) []tables.TableAuditLog {
	t.Helper()
	ctx := context.Background()
	logs := make([]tables.TableAuditLog, 0, n)
	for i := 0; i < n; i++ {
		entry := tables.TableAuditLog{
			ID:        fmt.Sprintf("audit-%04d", i),
			Action:    "virtual_key.create",
			Outcome:   tables.AuditOutcomeSuccess,
			Actor:     "tester",
			Target:    fmt.Sprintf("vk-%04d", i),
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		}
		entry.Sign(auditTestHMACKey)
		require.NoError(t, s.CreateAuditLog(ctx, &entry))
		logs = append(logs, entry)
	}
	return logs
}

func TestAuditLogSettings_SingletonSemantics(t *testing.T) {
	s := setupAuditLogTestStore(t)
	ctx := context.Background()

	// Unconfigured feature reports ErrNotFound (the default-off state).
	_, err := s.GetAuditLogSettings(ctx)
	require.ErrorIs(t, err, ErrNotFound)

	first := &tables.TableAuditLogSettings{
		RetentionMaxAgeDays: 30,
		RetentionMaxRows:    1000,
		ExportEnabled:       true,
		ExportType:          tables.AuditExportTypeFile,
		ExportFilePath:      "/tmp/audit.jsonl",
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	require.NoError(t, s.UpdateAuditLogSettings(ctx, first))

	got, err := s.GetAuditLogSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, tables.AuditLogSettingsID, got.ID, "primary key must be pinned to the singleton ID")
	assert.Equal(t, 30, got.RetentionMaxAgeDays)
	assert.Equal(t, int64(1000), got.RetentionMaxRows)

	// A second update — even with a divergent ID — must overwrite the same row,
	// never create a sibling.
	second := &tables.TableAuditLogSettings{
		ID:                  "some-other-id",
		RetentionMaxAgeDays: 7,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	require.NoError(t, s.UpdateAuditLogSettings(ctx, second))

	got, err = s.GetAuditLogSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, tables.AuditLogSettingsID, got.ID)
	assert.Equal(t, 7, got.RetentionMaxAgeDays)

	var count int64
	require.NoError(t, s.DB().Model(&tables.TableAuditLogSettings{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "settings table must hold exactly one row")

	// BeforeSave clamps negative retention values to 0 (unlimited).
	negative := &tables.TableAuditLogSettings{
		RetentionMaxAgeDays: -5,
		RetentionMaxRows:    -10,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	require.NoError(t, s.UpdateAuditLogSettings(ctx, negative))
	got, err = s.GetAuditLogSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, got.RetentionMaxAgeDays)
	assert.Equal(t, int64(0), got.RetentionMaxRows)

	// BeforeSave rejects unknown export destination enums.
	invalid := &tables.TableAuditLogSettings{ExportType: "carrier-pigeon"}
	assert.Error(t, s.UpdateAuditLogSettings(ctx, invalid))
	assert.Error(t, s.UpdateAuditLogSettings(ctx, &tables.TableAuditLogSettings{SyslogNetwork: "sctp"}))

	// Nil settings are rejected, not persisted.
	assert.Error(t, s.UpdateAuditLogSettings(ctx, nil))
}

func TestDeleteAuditLogsBefore_CutoffAndBatching(t *testing.T) {
	s := setupAuditLogTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seeded := seedAuditLogs(t, s, 10, base)

	// Cutoff lands after the first 6 rows (timestamp < base+5m30s).
	cutoff := base.Add(5*time.Minute + 30*time.Second)

	// batchSize 4 must delete at most 4 rows per call, oldest-first.
	deleted, sigs, err := s.DeleteAuditLogsBefore(ctx, cutoff, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(4), deleted)
	require.Len(t, sigs, 4)
	for i, sig := range sigs {
		assert.Equal(t, seeded[i].Signature, sig, "signatures must come back oldest-first")
	}

	deleted, sigs, err = s.DeleteAuditLogsBefore(ctx, cutoff, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)
	assert.Equal(t, []string{seeded[4].Signature, seeded[5].Signature}, sigs)

	// Nothing older than the cutoff remains.
	deleted, sigs, err = s.DeleteAuditLogsBefore(ctx, cutoff, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
	assert.Empty(t, sigs)

	remaining, total, err := s.GetAuditLogs(ctx, AuditLogsQueryParams{})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	for _, row := range remaining {
		assert.False(t, row.Timestamp.Before(cutoff), "row %s older than cutoff survived", row.ID)
	}
}

func TestTrimAuditLogsToCount_OldestFirst(t *testing.T) {
	s := setupAuditLogTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seeded := seedAuditLogs(t, s, 10, base)

	// maxRows <= 0 means unlimited: strict no-op.
	deleted, sigs, err := s.TrimAuditLogsToCount(ctx, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
	assert.Empty(t, sigs)

	// Under the cap: no-op.
	deleted, _, err = s.TrimAuditLogsToCount(ctx, 100, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)

	// Trim to 4: three batched calls (4+2, then 0) removing exactly the 6
	// oldest rows.
	deleted, sigs, err = s.TrimAuditLogsToCount(ctx, 4, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(4), deleted)
	assert.Equal(t, []string{seeded[0].Signature, seeded[1].Signature, seeded[2].Signature, seeded[3].Signature}, sigs)

	deleted, _, err = s.TrimAuditLogsToCount(ctx, 4, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	deleted, _, err = s.TrimAuditLogsToCount(ctx, 4, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)

	// The 4 newest rows — the ones a fresh prune-marker event would be among —
	// must be the survivors.
	remaining, total, err := s.GetAuditLogs(ctx, AuditLogsQueryParams{})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	survivorIDs := make(map[string]bool, len(remaining))
	for _, row := range remaining {
		survivorIDs[row.ID] = true
	}
	for _, expected := range seeded[6:] {
		assert.True(t, survivorIDs[expected.ID], "newest row %s must survive the trim", expected.ID)
	}
}

func TestGetAuditLogsSince_KeysetIteration(t *testing.T) {
	s := setupAuditLogTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedAuditLogs(t, s, 7, base)

	// Two extra rows sharing one timestamp exercise the (timestamp, id)
	// tie-break.
	tied := base.Add(90 * time.Minute)
	for _, id := range []string{"tie-a", "tie-b"} {
		entry := tables.TableAuditLog{
			ID:        id,
			Action:    "team.update",
			Outcome:   tables.AuditOutcomeSuccess,
			Actor:     "tester",
			Target:    "team-1",
			Timestamp: tied,
		}
		entry.Sign(auditTestHMACKey)
		require.NoError(t, s.CreateAuditLog(ctx, &entry))
	}

	// Walk the full table in pages of 3 and assert strict keyset order with no
	// duplicates and no gaps.
	var (
		afterTS time.Time
		afterID string
		seen    []tables.TableAuditLog
	)
	for {
		page, err := s.GetAuditLogsSince(ctx, AuditLogsQueryParams{}, afterTS, afterID, 3)
		require.NoError(t, err)
		seen = append(seen, page...)
		if len(page) < 3 {
			break
		}
		last := page[len(page)-1]
		afterTS, afterID = last.Timestamp, last.ID
	}
	require.Len(t, seen, 9)
	for i := 1; i < len(seen); i++ {
		prev, cur := seen[i-1], seen[i]
		ordered := cur.Timestamp.After(prev.Timestamp) ||
			(cur.Timestamp.Equal(prev.Timestamp) && cur.ID > prev.ID)
		assert.True(t, ordered, "rows %s and %s out of keyset order", prev.ID, cur.ID)
	}

	// Filters compose with the cursor: only the tied team.update rows match.
	page, err := s.GetAuditLogsSince(ctx, AuditLogsQueryParams{Action: "team.update"}, time.Time{}, "", 10)
	require.NoError(t, err)
	require.Len(t, page, 2)
	assert.Equal(t, "tie-a", page[0].ID)
	assert.Equal(t, "tie-b", page[1].ID)
}

func TestGetAuditLogs_SinceUntilFilters(t *testing.T) {
	s := setupAuditLogTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seeded := seedAuditLogs(t, s, 6, base)

	since := base.Add(2 * time.Minute)
	until := base.Add(4 * time.Minute)

	logs, total, err := s.GetAuditLogs(ctx, AuditLogsQueryParams{Since: &since})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total, "since is inclusive")

	logs, total, err = s.GetAuditLogs(ctx, AuditLogsQueryParams{Until: &until})
	require.NoError(t, err)
	assert.Equal(t, int64(5), total, "until is inclusive")

	logs, total, err = s.GetAuditLogs(ctx, AuditLogsQueryParams{Since: &since, Until: &until})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	for _, row := range logs {
		assert.False(t, row.Timestamp.Before(since))
		assert.False(t, row.Timestamp.After(until))
	}
	_ = seeded
}

// TestSignatures_SurviveSubsetDeletion proves the prune-safety property the
// retention design rests on: signatures are per-row HMACs with no chain
// linking, so deleting any subset leaves every surviving row independently
// verifiable.
func TestSignatures_SurviveSubsetDeletion(t *testing.T) {
	s := setupAuditLogTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedAuditLogs(t, s, 8, base)

	cutoff := base.Add(3*time.Minute + 30*time.Second)
	deleted, _, err := s.DeleteAuditLogsBefore(ctx, cutoff, 100)
	require.NoError(t, err)
	require.Equal(t, int64(4), deleted)

	remaining, _, err := s.GetAuditLogs(ctx, AuditLogsQueryParams{})
	require.NoError(t, err)
	require.Len(t, remaining, 4)
	for _, row := range remaining {
		assert.True(t, row.VerifySignature(auditTestHMACKey),
			"surviving row %s must still verify after subset deletion", row.ID)
	}
}
