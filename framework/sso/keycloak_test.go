package sso

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
)

func newTestStore(t *testing.T) configstore.ConfigStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "sso_test.db")
	logger := bifrost.NewDefaultLogger(schemas.LogLevelError)
	store, err := configstore.NewConfigStore(context.Background(), &configstore.Config{
		Enabled: true,
		Type:    configstore.ConfigStoreTypeSQLite,
		Config:  &configstore.SQLiteConfig{Path: dbPath},
	}, logger)
	if err != nil {
		t.Fatalf("create config store: %v", err)
	}
	if store == nil {
		t.Fatal("nil config store")
	}
	return store
}

// mockKeycloakAdmin serves the token endpoint and the admin users/groups
// endpoints used by SyncEngine.
func mockKeycloakAdmin(t *testing.T, users []kcUser, groups []kcGroup) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/realms/test/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "admin-token", "expires_in": 60})
	})
	mux.HandleFunc("/admin/realms/test/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Serve all users on first page, empty on subsequent pages.
		if r.URL.Query().Get("first") != "0" {
			_ = json.NewEncoder(w).Encode([]kcUser{})
			return
		}
		_ = json.NewEncoder(w).Encode(users)
	})
	mux.HandleFunc("/admin/realms/test/groups", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(groups)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func keycloakSyncConfig(serverURL string) *SCIMConfig {
	cfg, _ := json.Marshal(map[string]any{
		"serverUrl":    serverURL,
		"realm":        "test",
		"clientId":     "loopback",
		"clientSecret": "secret",
	})
	return &SCIMConfig{Enabled: true, Provider: ProviderKeycloak, Config: cfg}
}

func TestSyncEngineProvisionsUsersAndGroups(t *testing.T) {
	store := newTestStore(t)
	users := []kcUser{
		{ID: "kc-1", Username: "alice", Email: "alice@example.com", FirstName: "Alice", LastName: "A", Enabled: true},
		{ID: "kc-2", Username: "bob", Email: "bob@example.com", Enabled: false},
	}
	groups := []kcGroup{{ID: "g-1", Name: "engineering", Path: "/engineering"}}
	srv := mockKeycloakAdmin(t, users, groups)

	engine, err := NewSyncEngine(keycloakSyncConfig(srv.URL), store, srv.Client())
	if err != nil {
		t.Fatalf("NewSyncEngine: %v", err)
	}
	res, err := engine.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected sync errors: %v", res.Errors)
	}
	if res.UsersSynced != 1 || res.UsersDeactivated != 1 {
		t.Fatalf("counts: synced=%d deactivated=%d", res.UsersSynced, res.UsersDeactivated)
	}
	if res.GroupsSynced != 1 {
		t.Fatalf("groups synced=%d", res.GroupsSynced)
	}

	// SCIM mirror rows exist and link to managed users.
	ctx := context.Background()
	scimAlice, err := store.GetSCIMUserByExternalID(ctx, ProviderKeycloak, "kc-1")
	if err != nil {
		t.Fatalf("get scim alice: %v", err)
	}
	if !scimAlice.Active || scimAlice.ManagedUserID == nil {
		t.Fatalf("alice scim row bad: %+v", scimAlice)
	}
	managed, err := store.GetUser(ctx, *scimAlice.ManagedUserID)
	if err != nil {
		t.Fatalf("get managed alice: %v", err)
	}
	if !strings.EqualFold(managed.Email, "alice@example.com") {
		t.Fatalf("managed alice email: %q", managed.Email)
	}

	scimBob, err := store.GetSCIMUserByExternalID(ctx, ProviderKeycloak, "kc-2")
	if err != nil {
		t.Fatalf("get scim bob: %v", err)
	}
	if scimBob.Active {
		t.Fatal("bob should be deactivated (Enabled=false in IdP)")
	}
}

func TestSyncEngineIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	users := []kcUser{{ID: "kc-1", Username: "alice", Email: "alice@example.com", Enabled: true}}
	srv := mockKeycloakAdmin(t, users, nil)
	engine, _ := NewSyncEngine(keycloakSyncConfig(srv.URL), store, srv.Client())

	if _, err := engine.Sync(context.Background()); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if _, err := engine.Sync(context.Background()); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	// A second sync must not create a duplicate managed user.
	all, total, err := store.GetUsers(context.Background(), configstore.UsersQueryParams{Limit: 100})
	if err != nil {
		t.Fatalf("get users: %v", err)
	}
	if total != 1 || len(all) != 1 {
		t.Fatalf("expected exactly 1 managed user after two syncs, got %d", total)
	}
}

func TestProvisionFromIdentityJITCreatesAndReuses(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	id := &Identity{
		Provider:    ProviderKeycloak,
		Subject:     "kc-jit",
		Email:       "carol@example.com",
		UserName:    "carol",
		DisplayName: "Carol",
	}
	first, err := ProvisionFromIdentity(ctx, store, id, true, AttributeMappings{})
	if err != nil {
		t.Fatalf("first provision: %v", err)
	}
	second, err := ProvisionFromIdentity(ctx, store, id, true, AttributeMappings{})
	if err != nil {
		t.Fatalf("second provision: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("JIT login created a new managed user instead of reusing: %s != %s", first.ID, second.ID)
	}
}

func TestNewSyncEngineDisabledReturnsNil(t *testing.T) {
	engine, err := NewSyncEngine(&SCIMConfig{Enabled: false}, newTestStore(t), nil)
	if err != nil {
		t.Fatalf("disabled should not error: %v", err)
	}
	if engine != nil {
		t.Fatal("disabled config must yield a nil engine (default-OFF)")
	}
}
