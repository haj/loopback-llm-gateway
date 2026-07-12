package handlers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

// ---- pruneOnce unit tests (counting mock store) ----

// mockAuditStore embeds the interface so unimplemented methods panic. It counts
// every mutation so the default-off invariant ("no settings row means zero
// store mutations") is provable, and scripts the batched delete results.
type mockAuditStore struct {
	configstore.ConfigStore

	settings    *configstoreTables.TableAuditLogSettings
	settingsErr error

	// scripted (deleted, signatures) results, consumed per call.
	deleteBeforeResults []mockPruneBatch
	trimResults         []mockPruneBatch

	deleteBeforeCalls int
	deleteBeforeArgs  []time.Time
	trimCalls         int
	trimMaxRows       []int64
	createCalls       int
	created           []*configstoreTables.TableAuditLog
	createErr         error
}

type mockPruneBatch struct {
	deleted    int64
	signatures []string
}

func (m *mockAuditStore) GetAuditLogSettings(_ context.Context) (*configstoreTables.TableAuditLogSettings, error) {
	if m.settingsErr != nil {
		return nil, m.settingsErr
	}
	return m.settings, nil
}

func (m *mockAuditStore) DeleteAuditLogsBefore(_ context.Context, cutoff time.Time, _ int) (int64, []string, error) {
	m.deleteBeforeCalls++
	m.deleteBeforeArgs = append(m.deleteBeforeArgs, cutoff)
	if len(m.deleteBeforeResults) == 0 {
		return 0, nil, nil
	}
	batch := m.deleteBeforeResults[0]
	m.deleteBeforeResults = m.deleteBeforeResults[1:]
	return batch.deleted, batch.signatures, nil
}

func (m *mockAuditStore) TrimAuditLogsToCount(_ context.Context, maxRows int64, _ int) (int64, []string, error) {
	m.trimCalls++
	m.trimMaxRows = append(m.trimMaxRows, maxRows)
	if len(m.trimResults) == 0 {
		return 0, nil, nil
	}
	batch := m.trimResults[0]
	m.trimResults = m.trimResults[1:]
	return batch.deleted, batch.signatures, nil
}

func (m *mockAuditStore) CreateAuditLog(_ context.Context, log *configstoreTables.TableAuditLog, _ ...*gorm.DB) error {
	m.createCalls++
	if m.createErr != nil {
		return m.createErr
	}
	m.created = append(m.created, log)
	return nil
}

// pruneDigest recomputes the digest the worker anchors in the marker: SHA-256
// over each deleted signature followed by a newline, oldest-first.
func pruneDigest(signatures ...string) string {
	h := sha256.New()
	for _, sig := range signatures {
		h.Write([]byte(sig))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestAuditRetention_NoSettingsRowIsStrictNoOp(t *testing.T) {
	SetLogger(&mockLogger{})
	store := &mockAuditStore{settingsErr: configstore.ErrNotFound}
	w := NewAuditRetentionWorker(store)

	result, err := w.pruneOnce(context.Background(), "tester", "127.0.0.1")
	require.NoError(t, err)
	assert.Equal(t, int64(0), result.Deleted())
	assert.Zero(t, store.deleteBeforeCalls, "unconfigured feature must issue no deletes")
	assert.Zero(t, store.trimCalls)
	assert.Zero(t, store.createCalls, "no prune marker may be written when nothing ran")
}

func TestAuditRetention_ZeroLimitsAreNoOp(t *testing.T) {
	SetLogger(&mockLogger{})
	store := &mockAuditStore{settings: &configstoreTables.TableAuditLogSettings{
		ID: configstoreTables.AuditLogSettingsID,
	}}
	w := NewAuditRetentionWorker(store)

	result, err := w.pruneOnce(context.Background(), "tester", "")
	require.NoError(t, err)
	assert.Equal(t, int64(0), result.Deleted())
	assert.Zero(t, store.deleteBeforeCalls)
	assert.Zero(t, store.trimCalls)
	assert.Zero(t, store.createCalls)
}

func TestAuditRetention_PrunesByAgeAndRowsWithSignedMarker(t *testing.T) {
	SetLogger(&mockLogger{})
	store := &mockAuditStore{
		settings: &configstoreTables.TableAuditLogSettings{
			ID:                  configstoreTables.AuditLogSettingsID,
			RetentionMaxAgeDays: 30,
			RetentionMaxRows:    500,
		},
		deleteBeforeResults: []mockPruneBatch{
			{deleted: 100, signatures: []string{"sig-a", "sig-b"}}, // full batch: loop continues
			{deleted: 2, signatures: []string{"sig-c"}},            // short batch: loop ends
		},
		trimResults: []mockPruneBatch{
			{deleted: 3, signatures: []string{"sig-d"}},
		},
	}
	w := NewAuditRetentionWorker(store)

	before := time.Now().UTC().AddDate(0, 0, -30)
	result, err := w.pruneOnce(context.Background(), "admin@example.com", "10.0.0.1")
	after := time.Now().UTC().AddDate(0, 0, -30)
	require.NoError(t, err)

	assert.Equal(t, int64(102), result.DeletedByAge)
	assert.Equal(t, int64(3), result.DeletedByCount)
	assert.Equal(t, 2, store.deleteBeforeCalls, "full batches must loop until a short batch")
	assert.Equal(t, 1, store.trimCalls, "short trim batch must end the loop")
	assert.Equal(t, []int64{500}, store.trimMaxRows)

	// Cutoff is now minus RetentionMaxAgeDays, and stable across both delete calls.
	for _, cutoff := range store.deleteBeforeArgs {
		assert.False(t, cutoff.Before(before) || cutoff.After(after), "cutoff must be now-30d")
	}

	// One signed marker anchors the whole prune.
	require.Equal(t, 1, store.createCalls)
	marker := store.created[0]
	assert.Equal(t, AuditActionAuditLogPrune, marker.Action)
	assert.Equal(t, configstoreTables.AuditOutcomeSuccess, marker.Outcome)
	assert.Equal(t, "admin@example.com", marker.Actor)
	assert.Equal(t, "10.0.0.1", marker.IP)
	assert.Equal(t, marker.ID, result.MarkerID)
	assert.True(t, marker.VerifySignature(auditHMACKey()), "the prune marker must be signed like any audit event")

	expectedDigest := pruneDigest("sig-a", "sig-b", "sig-c", "sig-d")
	assert.Equal(t, expectedDigest, result.Digest)
	assert.Contains(t, marker.Target, "deleted=105")
	assert.Contains(t, marker.Target, "deleted_by_age=102")
	assert.Contains(t, marker.Target, "deleted_by_rows=3")
	assert.Contains(t, marker.Target, "max_age_days=30")
	assert.Contains(t, marker.Target, "max_rows=500")
	assert.Contains(t, marker.Target, "deleted_signatures_digest=sha256:"+expectedDigest)
}

func TestAuditRetention_NothingDeletedWritesNoMarker(t *testing.T) {
	SetLogger(&mockLogger{})
	store := &mockAuditStore{
		settings: &configstoreTables.TableAuditLogSettings{
			ID:                  configstoreTables.AuditLogSettingsID,
			RetentionMaxAgeDays: 30,
			RetentionMaxRows:    500,
		},
	}
	w := NewAuditRetentionWorker(store)

	result, err := w.pruneOnce(context.Background(), "tester", "")
	require.NoError(t, err)
	assert.Equal(t, int64(0), result.Deleted())
	assert.Empty(t, result.Digest)
	assert.Empty(t, result.MarkerID)
	assert.Zero(t, store.createCalls)
}

func TestAuditRetention_ErrorsPropagate(t *testing.T) {
	SetLogger(&mockLogger{})

	// Settings load failure (other than not-found) is an error, not a no-op.
	store := &mockAuditStore{settingsErr: errors.New("db down")}
	_, err := NewAuditRetentionWorker(store).pruneOnce(context.Background(), "t", "")
	assert.ErrorContains(t, err, "db down")

	// A prune that deleted rows but could not append its marker must surface
	// that loudly — the deletion happened without its tamper-evidence anchor.
	store = &mockAuditStore{
		settings: &configstoreTables.TableAuditLogSettings{
			ID:                  configstoreTables.AuditLogSettingsID,
			RetentionMaxAgeDays: 1,
		},
		deleteBeforeResults: []mockPruneBatch{{deleted: 5, signatures: []string{"s"}}},
		createErr:           errors.New("insert failed"),
	}
	_, err = NewAuditRetentionWorker(store).pruneOnce(context.Background(), "t", "")
	assert.ErrorContains(t, err, "prune marker")
}

func TestAuditRetention_StartStopIdempotent(t *testing.T) {
	SetLogger(&mockLogger{})
	store := &mockAuditStore{settingsErr: configstore.ErrNotFound}
	w := NewAuditRetentionWorker(store)

	w.Start()
	w.Start() // second start is a no-op, not a second goroutine
	w.Stop()
	w.Stop() // second stop must not panic
}

// ---- handler endpoint tests (real SQLite configstore) ----

// newAuditTestStore builds a real SQLite-backed configstore (running the full
// migration chain, including add_audit_log_settings_table) in a temp dir.
func newAuditTestStore(t *testing.T) configstore.ConfigStore {
	t.Helper()
	SetLogger(&mockLogger{})
	store, err := configstore.NewConfigStore(context.Background(), &configstore.Config{
		Enabled: true,
		Type:    configstore.ConfigStoreTypeSQLite,
		Config:  &configstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "audit.db")},
	}, &mockLogger{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	return store
}

// seedHandlerAuditLogs writes n signed rows through the store, oldest first.
func seedHandlerAuditLogs(t *testing.T, store configstore.ConfigStore, n int) []configstoreTables.TableAuditLog {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	logs := make([]configstoreTables.TableAuditLog, 0, n)
	for i := 0; i < n; i++ {
		entry := configstoreTables.TableAuditLog{
			ID:        fmt.Sprintf("row-%04d", i),
			Action:    "virtual_key.create",
			Outcome:   configstoreTables.AuditOutcomeSuccess,
			Actor:     "seeder",
			Target:    fmt.Sprintf("vk-%d", i),
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		}
		entry.Sign(auditHMACKey())
		require.NoError(t, store.CreateAuditLog(context.Background(), &entry))
		logs = append(logs, entry)
	}
	return logs
}

// auditRequestCtx builds an initialized fasthttp request context for direct
// handler invocation. Init is required (matching newHandlerCtx): handlers pass
// the RequestCtx into store calls as a context.Context, and Done() panics on a
// bare RequestCtx.
func auditRequestCtx(method, uri string, body []byte) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	var req fasthttp.Request
	req.Header.SetMethod(method)
	req.SetRequestURI(uri)
	if body != nil {
		req.SetBody(body)
	}
	ctx.Init(&req, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, nil)
	return ctx
}

func TestAuditSettingsEndpoint_DefaultsWhenUnconfigured(t *testing.T) {
	store := newAuditTestStore(t)
	h, err := NewAuditLogsHandler(store)
	require.NoError(t, err)

	ctx := auditRequestCtx("GET", "/api/governance/audit-logs/settings", nil)
	h.getSettings(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode())

	var resp struct {
		Settings configstoreTables.TableAuditLogSettings `json:"settings"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	assert.Equal(t, configstoreTables.AuditLogSettingsID, resp.Settings.ID)
	assert.False(t, resp.Settings.ExportEnabled, "unconfigured feature must present as default-off")
	assert.Zero(t, resp.Settings.RetentionMaxAgeDays)
}

func TestAuditSettingsEndpoint_ValidationRejects(t *testing.T) {
	store := newAuditTestStore(t)
	h, err := NewAuditLogsHandler(store)
	require.NoError(t, err)

	cases := []struct {
		name string
		body string
	}{
		{"malformed json", `{not json`},
		{"negative age", `{"retention_max_age_days": -1}`},
		{"negative rows", `{"retention_max_rows": -1}`},
		{"unknown export type", `{"export_type": "carrier-pigeon"}`},
		{"enabled without type", `{"export_enabled": true}`},
		{"file export without path", `{"export_enabled": true, "export_type": "file"}`},
		{"bad syslog network", `{"syslog_network": "sctp"}`},
		{"remote syslog without address", `{"export_enabled": true, "export_type": "syslog", "syslog_network": "udp"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := auditRequestCtx("PUT", "/api/governance/audit-logs/settings", []byte(tc.body))
			h.updateSettings(ctx)
			assert.Equal(t, 400, ctx.Response.StatusCode())
		})
	}

	// Nothing may have been persisted by the rejected updates.
	_, err = store.GetAuditLogSettings(context.Background())
	assert.ErrorIs(t, err, configstore.ErrNotFound)
}

func TestAuditSettingsEndpoint_UpdatePersistsAndAudits(t *testing.T) {
	store := newAuditTestStore(t)
	h, err := NewAuditLogsHandler(store)
	require.NoError(t, err)
	t.Cleanup(ShutdownAuditExport)

	exportPath := filepath.Join(t.TempDir(), "audit-export.jsonl")
	body := fmt.Sprintf(
		`{"retention_max_age_days": 90, "retention_max_rows": 10000, "export_enabled": true, "export_type": "file", "export_file_path": %q}`,
		exportPath,
	)
	ctx := auditRequestCtx("PUT", "/api/governance/audit-logs/settings", []byte(body))
	h.updateSettings(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode(), "body: %s", ctx.Response.Body())

	saved, err := store.GetAuditLogSettings(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 90, saved.RetentionMaxAgeDays)
	assert.Equal(t, int64(10000), saved.RetentionMaxRows)
	assert.True(t, saved.ExportEnabled)
	assert.Equal(t, exportPath, saved.ExportFilePath)

	// The mutation itself is recorded in the trail.
	logs, _, err := store.GetAuditLogs(context.Background(), configstore.AuditLogsQueryParams{Action: AuditActionAuditSettingsUpdate})
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.True(t, logs[0].VerifySignature(auditHMACKey()))

	// A partial follow-up PUT must leave unmentioned fields unchanged.
	ctx = auditRequestCtx("PUT", "/api/governance/audit-logs/settings", []byte(`{"export_enabled": false}`))
	h.updateSettings(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode())
	saved, err = store.GetAuditLogSettings(context.Background())
	require.NoError(t, err)
	assert.False(t, saved.ExportEnabled)
	assert.Equal(t, 90, saved.RetentionMaxAgeDays, "fields absent from the PUT must be preserved")
}

func TestAuditExportEndpoint_StreamsNDJSON(t *testing.T) {
	store := newAuditTestStore(t)
	h, err := NewAuditLogsHandler(store)
	require.NoError(t, err)
	seeded := seedHandlerAuditLogs(t, store, 5)

	ctx := auditRequestCtx("GET", "/api/governance/audit-logs/export", nil)
	h.exportAuditLogs(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode())
	assert.Equal(t, "application/x-ndjson", string(ctx.Response.Header.ContentType()))
	assert.Contains(t, string(ctx.Response.Header.Peek("Content-Disposition")), "audit-logs.ndjson")

	scanner := bufio.NewScanner(bytes.NewReader(ctx.Response.Body()))
	var rows []configstoreTables.TableAuditLog
	for scanner.Scan() {
		var row configstoreTables.TableAuditLog
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &row))
		rows = append(rows, row)
	}
	require.Len(t, rows, 5, "one NDJSON line per row")
	for i, row := range rows {
		assert.Equal(t, seeded[i].ID, row.ID, "export must be keyset-ordered oldest-first")
		assert.True(t, row.VerifySignature(auditHMACKey()), "exported rows must remain verifiable offline")
	}

	// Filters narrow the export; a bad timestamp is rejected.
	ctx = auditRequestCtx("GET", "/api/governance/audit-logs/export?since=2026-01-01T00:03:00Z", nil)
	h.exportAuditLogs(ctx)
	lines := strings.Count(strings.TrimSpace(string(ctx.Response.Body())), "\n") + 1
	assert.Equal(t, 2, lines)

	ctx = auditRequestCtx("GET", "/api/governance/audit-logs/export?since=yesterday", nil)
	h.exportAuditLogs(ctx)
	assert.Equal(t, 400, ctx.Response.StatusCode())
}

func TestAuditPruneEndpoint_DeletesAndAnchors(t *testing.T) {
	store := newAuditTestStore(t)
	h, err := NewAuditLogsHandler(store)
	require.NoError(t, err)
	seedHandlerAuditLogs(t, store, 10)

	// Cap the trail at 4 rows; the seeded timestamps are all in the past so
	// age-based pruning stays out of the way (0 = disabled).
	require.NoError(t, store.UpdateAuditLogSettings(context.Background(), &configstoreTables.TableAuditLogSettings{
		RetentionMaxRows: 4,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}))

	ctx := auditRequestCtx("POST", "/api/governance/audit-logs/prune", nil)
	h.pruneAuditLogs(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode(), "body: %s", ctx.Response.Body())

	var resp struct {
		Deleted       int64  `json:"deleted"`
		DeletedByRows int64  `json:"deleted_by_rows"`
		MarkerID      string `json:"prune_marker_id"`
		Digest        string `json:"signature_digest"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	assert.Equal(t, int64(6), resp.Deleted)
	assert.Equal(t, int64(6), resp.DeletedByRows)
	assert.NotEmpty(t, resp.MarkerID)
	assert.NotEmpty(t, resp.Digest)

	// 4 survivors + 1 signed prune marker; every remaining row still verifies.
	logs, total, err := store.GetAuditLogs(context.Background(), configstore.AuditLogsQueryParams{})
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	var marker *configstoreTables.TableAuditLog
	for i := range logs {
		assert.True(t, logs[i].VerifySignature(auditHMACKey()), "row %s must verify after prune", logs[i].ID)
		if logs[i].Action == AuditActionAuditLogPrune {
			marker = &logs[i]
		}
	}
	require.NotNil(t, marker, "the prune must be anchored by a signed marker event")
	assert.Equal(t, resp.MarkerID, marker.ID)
	assert.Contains(t, marker.Target, "deleted_signatures_digest=sha256:"+resp.Digest)
}

func TestAuditVerifyEndpoint_CountsTamperedRows(t *testing.T) {
	store := newAuditTestStore(t)
	h, err := NewAuditLogsHandler(store)
	require.NoError(t, err)
	seeded := seedHandlerAuditLogs(t, store, 4)

	// Tamper with one row behind the store's back: rewrite its actor without
	// re-signing, exactly what an attacker with raw DB access would do.
	sqlStore, ok := store.(*configstore.RDBConfigStore)
	require.True(t, ok)
	require.NoError(t, sqlStore.DB().Model(&configstoreTables.TableAuditLog{}).
		Where("id = ?", seeded[2].ID).
		Update("actor", "attacker").Error)

	ctx := auditRequestCtx("GET", "/api/governance/audit-logs/verify", nil)
	h.verifyAuditLogs(ctx)
	require.Equal(t, 200, ctx.Response.StatusCode())

	var resp struct {
		Checked    int      `json:"checked"`
		Valid      int      `json:"valid"`
		Invalid    int      `json:"invalid"`
		InvalidIDs []string `json:"invalid_ids"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	assert.Equal(t, 4, resp.Checked)
	assert.Equal(t, 3, resp.Valid)
	assert.Equal(t, 1, resp.Invalid)
	assert.Equal(t, []string{seeded[2].ID}, resp.InvalidIDs)

	// Limit validation.
	ctx = auditRequestCtx("GET", "/api/governance/audit-logs/verify?limit=0", nil)
	h.verifyAuditLogs(ctx)
	assert.Equal(t, 400, ctx.Response.StatusCode())
}
