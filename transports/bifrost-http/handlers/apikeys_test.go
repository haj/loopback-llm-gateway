// Hermetic tests for the admin API keys handler (apikeys.go): CRUD, rotate and
// revoke against a real SQLite config store in t.TempDir() (the
// governance_test.go bootstrap pattern). No network, no Docker.
package handlers

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/valyala/fasthttp"
)

// newAPIKeysTestStore creates a real SQLite-backed config store in a temp dir.
func newAPIKeysTestStore(t *testing.T) configstore.ConfigStore {
	t.Helper()
	SetLogger(&mockLogger{})
	store, err := configstore.NewConfigStore(context.Background(), &configstore.Config{
		Enabled: true,
		Type:    configstore.ConfigStoreTypeSQLite,
		Config:  &configstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "apikeys.db")},
	}, &mockLogger{})
	if err != nil {
		t.Fatalf("failed to create config store: %v", err)
	}
	return store
}

func newAPIKeysTestHandler(t *testing.T) (*APIKeysHandler, configstore.ConfigStore) {
	t.Helper()
	store := newAPIKeysTestStore(t)
	h, err := NewAPIKeysHandler(store)
	if err != nil {
		t.Fatalf("failed to create api keys handler: %v", err)
	}
	return h, store
}

// newHandlerCtx builds a request ctx wired to a (fake) server so it is usable
// as a context.Context by store calls (bare RequestCtx panics in Done()).
func newHandlerCtx(t *testing.T) *fasthttp.RequestCtx {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	var req fasthttp.Request
	ctx.Init(&req, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, nil)
	return ctx
}

// postJSON builds a request ctx with a JSON body.
func postJSON(t *testing.T, body any) *fasthttp.RequestCtx {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("failed to marshal body: %v", err)
	}
	ctx := newHandlerCtx(t)
	ctx.Request.SetBody(raw)
	return ctx
}

func decodeJSONResponse(t *testing.T, ctx *fasthttp.RequestCtx) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatalf("failed to decode response %q: %v", string(ctx.Response.Body()), err)
	}
	return out
}

// createTestAPIKey drives the real create handler and returns the plaintext
// secret and the created key's ID.
func createTestAPIKey(t *testing.T, h *APIKeysHandler, name string, scopes []permissionInput) (string, string) {
	t.Helper()
	ctx := postJSON(t, createAPIKeyRequest{Name: name, Scopes: scopes})
	h.createAPIKey(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("create failed with status %d: %s", ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}
	resp := decodeJSONResponse(t, ctx)
	plaintext, _ := resp["key"].(string)
	keyObj, _ := resp["api_key"].(map[string]any)
	id, _ := keyObj["id"].(string)
	if plaintext == "" || id == "" {
		t.Fatalf("create response missing key or id: %v", resp)
	}
	return plaintext, id
}

var hexHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestAPIKeysCreate_ReturnsPlaintextOnceAndStoresOnlyHash(t *testing.T) {
	h, store := newAPIKeysTestHandler(t)

	plaintext, id := createTestAPIKey(t, h, "ci-bot", []permissionInput{
		{Resource: "ModelProvider", Operation: "Create"},
		{Resource: "GuardrailsConfig", Operation: "*"},
	})

	if !strings.HasPrefix(plaintext, configstoreTables.AdminAPIKeyPrefix) {
		t.Fatalf("expected plaintext to start with %q, got %q", configstoreTables.AdminAPIKeyPrefix, plaintext)
	}

	// The persisted row must contain only the SHA-256 hash of the plaintext.
	rdb, ok := store.(*configstore.RDBConfigStore)
	if !ok {
		t.Fatalf("expected RDBConfigStore, got %T", store)
	}
	var raw struct {
		ValueHash string
		KeyPrefix string
	}
	if err := rdb.DB().Raw("SELECT value_hash, key_prefix FROM governance_admin_api_keys WHERE id = ?", id).Scan(&raw).Error; err != nil {
		t.Fatalf("failed to read raw row: %v", err)
	}
	if raw.ValueHash == plaintext {
		t.Fatal("plaintext secret was persisted verbatim")
	}
	if !hexHashRe.MatchString(raw.ValueHash) {
		t.Fatalf("value_hash is not a 64-char hex sha-256: %q", raw.ValueHash)
	}
	if raw.ValueHash != encrypt.HashSHA256(plaintext) {
		t.Fatal("value_hash does not match SHA-256 of the plaintext")
	}
	if raw.KeyPrefix != plaintext[:adminAPIKeyDisplayPrefixLen] {
		t.Fatalf("key_prefix %q does not match plaintext prefix %q", raw.KeyPrefix, plaintext[:adminAPIKeyDisplayPrefixLen])
	}

	// List and get must never leak the plaintext or the hash.
	listCtx := newHandlerCtx(t)
	h.getAPIKeys(listCtx)
	listBody := string(listCtx.Response.Body())
	if strings.Contains(listBody, plaintext) || strings.Contains(listBody, raw.ValueHash) {
		t.Fatal("list response leaks the secret or its hash")
	}
	if !strings.Contains(listBody, plaintext[:adminAPIKeyDisplayPrefixLen]) {
		t.Fatal("list response should include the display prefix")
	}

	getCtx := newHandlerCtx(t)
	getCtx.SetUserValue("id", id)
	h.getAPIKey(getCtx)
	getBody := string(getCtx.Response.Body())
	if strings.Contains(getBody, plaintext) || strings.Contains(getBody, raw.ValueHash) {
		t.Fatal("get response leaks the secret or its hash")
	}
	// Scopes round-trip.
	if !strings.Contains(getBody, "GuardrailsConfig") || !strings.Contains(getBody, "ModelProvider") {
		t.Fatalf("get response missing scopes: %s", getBody)
	}
}

func TestAPIKeysCreate_Validation(t *testing.T) {
	h, _ := newAPIKeysTestHandler(t)

	// Missing name.
	ctx := postJSON(t, createAPIKeyRequest{Name: "  "})
	h.createAPIKey(ctx)
	if ctx.Response.StatusCode() != 400 {
		t.Fatalf("expected 400 for empty name, got %d", ctx.Response.StatusCode())
	}

	// Invalid scope operation (must be RBAC vocabulary).
	ctx = postJSON(t, createAPIKeyRequest{
		Name:   "bad-scope",
		Scopes: []permissionInput{{Resource: "ModelProvider", Operation: "Frobnicate"}},
	})
	h.createAPIKey(ctx)
	if ctx.Response.StatusCode() != 400 {
		t.Fatalf("expected 400 for invalid scope operation, got %d", ctx.Response.StatusCode())
	}
	if !strings.Contains(string(ctx.Response.Body()), "Frobnicate") {
		t.Fatalf("expected error to name the bad operation, got %s", string(ctx.Response.Body()))
	}

	// Empty scope resource.
	ctx = postJSON(t, createAPIKeyRequest{
		Name:   "bad-scope-2",
		Scopes: []permissionInput{{Resource: " ", Operation: "Read"}},
	})
	h.createAPIKey(ctx)
	if ctx.Response.StatusCode() != 400 {
		t.Fatalf("expected 400 for empty scope resource, got %d", ctx.Response.StatusCode())
	}
}

func TestAPIKeysUpdate_NameAndScopes(t *testing.T) {
	h, store := newAPIKeysTestHandler(t)
	_, id := createTestAPIKey(t, h, "before", []permissionInput{{Resource: "Teams", Operation: "Read"}})

	newName := "after"
	scopes := []permissionInput{{Resource: "Customers", Operation: "Update"}}
	ctx := postJSON(t, updateAPIKeyRequest{Name: &newName, Scopes: &scopes})
	ctx.SetUserValue("id", id)
	h.updateAPIKey(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("update failed: %d %s", ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}

	updated, err := store.GetAPIKey(context.Background(), id)
	if err != nil {
		t.Fatalf("failed to reload key: %v", err)
	}
	if updated.Name != "after" {
		t.Fatalf("expected name %q, got %q", "after", updated.Name)
	}
	if len(updated.Scopes) != 1 || updated.Scopes[0].Resource != "Customers" || updated.Scopes[0].Operation != "Update" {
		t.Fatalf("scopes were not replaced: %+v", updated.Scopes)
	}

	// Updating never returns or changes the secret.
	if strings.Contains(string(ctx.Response.Body()), configstoreTables.AdminAPIKeyPrefix+"") && strings.Contains(string(ctx.Response.Body()), "\"key\"") {
		t.Fatal("update response must not include a key field")
	}
}

func TestAPIKeysRotate_InvalidatesOldSecretAndRecordsLineage(t *testing.T) {
	h, store := newAPIKeysTestHandler(t)
	oldPlain, oldID := createTestAPIKey(t, h, "rotate-me", []permissionInput{{Resource: "*", Operation: "*"}})

	ctx := newHandlerCtx(t)
	ctx.SetUserValue("id", oldID)
	h.rotateAPIKey(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("rotate failed: %d %s", ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}
	resp := decodeJSONResponse(t, ctx)
	newPlain, _ := resp["key"].(string)
	newKeyObj, _ := resp["api_key"].(map[string]any)
	newID, _ := newKeyObj["id"].(string)
	if newPlain == "" || newID == "" || newID == oldID {
		t.Fatalf("rotate response invalid: %v", resp)
	}
	if newPlain == oldPlain {
		t.Fatal("rotate returned the same secret")
	}

	// New secret resolves to an active key with the old key's scopes and lineage.
	bg := context.Background()
	rotated, err := store.GetAPIKeyByValueHash(bg, encrypt.HashSHA256(newPlain))
	if err != nil {
		t.Fatalf("new secret does not resolve: %v", err)
	}
	if rotated.Status != configstoreTables.AdminAPIKeyStatusActive {
		t.Fatalf("expected rotated key active, got %q", rotated.Status)
	}
	if rotated.RotatedFromID == nil || *rotated.RotatedFromID != oldID {
		t.Fatalf("expected rotated_from_id %q, got %v", oldID, rotated.RotatedFromID)
	}
	if len(rotated.Scopes) != 1 || rotated.Scopes[0].Resource != "*" {
		t.Fatalf("scopes were not carried over: %+v", rotated.Scopes)
	}

	// Old secret still resolves to its row, but the row is revoked (the
	// middleware rejects revoked keys).
	old, err := store.GetAPIKeyByValueHash(bg, encrypt.HashSHA256(oldPlain))
	if err != nil {
		t.Fatalf("old key row should remain for lineage: %v", err)
	}
	if old.Status != configstoreTables.AdminAPIKeyStatusRevoked {
		t.Fatalf("expected old key revoked after rotate, got %q", old.Status)
	}
}

func TestAPIKeysRevokeAndDelete(t *testing.T) {
	h, store := newAPIKeysTestHandler(t)
	_, id := createTestAPIKey(t, h, "kill-me", []permissionInput{{Resource: "Teams", Operation: "Read"}})

	// Revoke flips status (soft).
	ctx := newHandlerCtx(t)
	ctx.SetUserValue("id", id)
	h.revokeAPIKey(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("revoke failed: %d %s", ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}
	bg := context.Background()
	revoked, err := store.GetAPIKey(bg, id)
	if err != nil {
		t.Fatalf("failed to reload key: %v", err)
	}
	if revoked.Status != configstoreTables.AdminAPIKeyStatusRevoked {
		t.Fatalf("expected status revoked, got %q", revoked.Status)
	}

	// Delete removes the key and cascades its scopes.
	ctx = newHandlerCtx(t)
	ctx.SetUserValue("id", id)
	h.deleteAPIKey(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("delete failed: %d %s", ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}
	if _, err := store.GetAPIKey(bg, id); err == nil {
		t.Fatal("expected key to be gone after delete")
	}
	rdb := store.(*configstore.RDBConfigStore)
	var scopeCount int64
	if err := rdb.DB().Raw("SELECT COUNT(*) FROM governance_admin_api_key_scopes WHERE api_key_id = ?", id).Scan(&scopeCount).Error; err != nil {
		t.Fatalf("failed to count scopes: %v", err)
	}
	if scopeCount != 0 {
		t.Fatalf("expected scopes to cascade on delete, %d remain", scopeCount)
	}

	// Mutations on a missing key 404.
	ctx = newHandlerCtx(t)
	ctx.SetUserValue("id", id)
	h.revokeAPIKey(ctx)
	if ctx.Response.StatusCode() != 404 {
		t.Fatalf("expected 404 revoking a deleted key, got %d", ctx.Response.StatusCode())
	}
}

func TestAPIKeysMutations_RecordAuditEvents(t *testing.T) {
	h, store := newAPIKeysTestHandler(t)
	_, id := createTestAPIKey(t, h, "audited", nil)

	ctx := newHandlerCtx(t)
	ctx.SetUserValue("id", id)
	h.revokeAPIKey(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("revoke failed: %d", ctx.Response.StatusCode())
	}

	logs, _, err := store.GetAuditLogs(context.Background(), configstore.AuditLogsQueryParams{Target: id})
	if err != nil {
		t.Fatalf("failed to read audit logs: %v", err)
	}
	actions := map[string]bool{}
	for _, l := range logs {
		actions[l.Action] = true
	}
	if !actions[AuditActionAPIKeyCreate] || !actions[AuditActionAPIKeyRevoke] {
		t.Fatalf("expected create+revoke audit events, got %v", actions)
	}
}
