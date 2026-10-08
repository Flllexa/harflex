package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	sqlitedriver "modernc.org/sqlite"
)

func TestCatalogGetters(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "catalog.db"))
	ctx := t.Context()
	w := catalog.Workspace{ID: "w", Path: "/root", Profile: "ask", CreatedAt: time.Now().UTC()}
	p := catalog.ProviderProfile{ID: "p", Name: "Provider", ProviderType: "generic", CreatedAt: w.CreatedAt, UpdatedAt: w.CreatedAt}
	r := catalog.SessionRecord{ID: "s", WorkspaceID: w.ID, BackendID: p.ID, BackendRevision: "api:revision", CreatedAt: w.CreatedAt, UpdatedAt: w.CreatedAt}
	if err := s.UpsertWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProviderProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSession(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetWorkspace(ctx, "w"); err != nil || !reflect.DeepEqual(got, w) {
		t.Fatalf("workspace: %+v %v", got, err)
	}
	if got, err := s.GetProviderProfile(ctx, "p"); err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("profile: %+v %v", got, err)
	}
	if got, err := s.GetSession(ctx, "s"); err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("session: %+v %v", got, err)
	}
	if _, err := s.GetWorkspace(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err := s.GetProviderProfile(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestForeignKeysSurviveConnectionRecycling(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, filepath.Join(t.TempDir(), "catalog.db"))
	for _, phase := range []string{"initial", "recycled"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "recycled" {
				store.DB().SetMaxIdleConns(0)
				if err := store.DB().PingContext(ctx); err != nil {
					t.Fatal(err)
				}
			}
			err := store.UpsertSession(ctx, catalog.SessionRecord{ID: phase, WorkspaceID: "missing"})
			var sqliteErr *sqlitedriver.Error
			if !errors.As(err, &sqliteErr) || sqliteErr.Code() != 787 {
				t.Fatalf("missing workspace must produce SQLITE_CONSTRAINT_FOREIGNKEY, got %v", err)
			}
			var mode string
			if err := store.DB().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
				t.Fatal(err)
			}
			if mode != "wal" {
				t.Fatalf("journal mode = %q", mode)
			}
		})
	}
}

func TestWorkspaceRoundTripAndUpdate(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, filepath.Join(t.TempDir(), "catalog.db"))
	created := time.Date(2026, 9, 25, 10, 0, 0, 123, time.UTC)
	want := catalog.Workspace{ID: "workspace-1", Path: "/projects/first", Profile: "default", CreatedAt: created}
	if err := store.UpsertWorkspace(ctx, want); err != nil {
		t.Fatal(err)
	}
	want.Path, want.Profile = "/projects/renamed", "work"
	update := want
	update.CreatedAt = created.Add(time.Hour)
	if err := store.UpsertWorkspace(ctx, update); err != nil {
		t.Fatal(err)
	}
	var got catalog.Workspace
	var timestamp string
	if err := store.DB().QueryRow("SELECT id, path, profile, created_at FROM workspaces WHERE id = ?", want.ID).Scan(&got.ID, &got.Path, &got.Profile, &timestamp); err != nil {
		t.Fatal(err)
	}
	var err error
	got.CreatedAt, err = time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace = %#v, want %#v", got, want)
	}
}

func TestProviderProfilesRoundTripUpdateOrderAndCredentialReferences(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	store := openStore(t, path)
	created := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	fixtureSecret := "fixture-api-key-never-persist"
	want := []catalog.ProviderProfile{
		{ID: "a", Name: "Alpha", Kind: "openai", ProviderType: "generic", BaseURL: "https://example.com", Model: "model-a", CredentialProvider: "keychain", CredentialAccount: "work", CreatedAt: created, UpdatedAt: created},
		{ID: "b", Name: "Beta", Kind: "openai", ProviderType: "generic", BaseURL: "https://example.com", Model: "model-b", CredentialProvider: "keychain", CredentialAccount: "other", CreatedAt: created, UpdatedAt: created},
		{ID: "0", Name: "Later", Kind: "openai", ProviderType: "generic", CreatedAt: created.Add(time.Nanosecond), UpdatedAt: created},
	}
	for _, profile := range []catalog.ProviderProfile{want[2], want[1], want[0]} {
		if err := store.UpsertProviderProfile(ctx, profile); err != nil {
			t.Fatal(err)
		}
	}
	want[0].Name, want[0].Kind, want[0].BaseURL, want[0].Model = "Updated", "compatible", "https://updated.example.com", "model-new"
	want[0].ProviderType = "openrouter"
	want[0].CredentialProvider, want[0].CredentialAccount, want[0].UpdatedAt = "vault", "updated", created.Add(time.Hour)
	update := want[0]
	update.CreatedAt = created.Add(time.Hour)
	if err := store.UpsertProviderProfile(ctx, update); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openStore(t, path)
	got, err := store.ListProviderProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profiles = %#v, want %#v", got, want)
	}
	serialized, err := json.Marshal(got[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(serialized, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 10 || fields["providerType"] != "openrouter" || fields["credentialProvider"] != "vault" || fields["credentialAccount"] != "updated" || strings.Contains(string(serialized), fixtureSecret) {
		t.Fatalf("unexpected serialized profile: %s", serialized)
	}
	var stored string
	if err := store.DB().QueryRow(`SELECT json_object('id',id,'name',name,'kind',kind,'providerType',provider_type,'baseURL',base_url,'model',model,'credentialProvider',credential_provider,'credentialAccount',credential_account,'createdAt',created_at,'updatedAt',updated_at) FROM provider_profiles WHERE id = 'a'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, fixtureSecret) {
		t.Fatal("secret persisted")
	}
	var columns int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM pragma_table_info('provider_profiles')").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 10 {
		t.Fatalf("unexpected profile columns: %d", columns)
	}
}

func TestSessionsRoundTripUpdateAndWorkspaceOrder(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	store := openStore(t, path)
	created := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	for _, workspace := range []catalog.Workspace{{ID: "workspace-1", Path: "/one", CreatedAt: created}, {ID: "workspace-2", Path: "/two", CreatedAt: created}} {
		if err := store.UpsertWorkspace(ctx, workspace); err != nil {
			t.Fatal(err)
		}
	}
	want := []catalog.SessionRecord{
		{ID: "a", WorkspaceID: "workspace-1", BackendID: "backend", Status: "ready", CreatedAt: created, UpdatedAt: created},
		{ID: "b", WorkspaceID: "workspace-1", BackendID: "backend", Status: "ready", CreatedAt: created, UpdatedAt: created},
		{ID: "0", WorkspaceID: "workspace-1", BackendID: "backend", Status: "ready", CreatedAt: created.Add(time.Nanosecond), UpdatedAt: created},
	}
	for _, session := range []catalog.SessionRecord{want[2], want[1], want[0], {ID: "other", WorkspaceID: "workspace-2", CreatedAt: created, UpdatedAt: created}} {
		if err := store.UpsertSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	want[0].Status, want[0].BackendID, want[0].UpdatedAt = "running", "next", created.Add(time.Hour)
	update := want[0]
	update.CreatedAt = created.Add(time.Hour)
	if err := store.UpsertSession(ctx, update); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openStore(t, path)
	got, err := store.ListSessions(ctx, "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sessions = %#v, want %#v", got, want)
	}
	if err := store.UpsertSession(ctx, catalog.SessionRecord{ID: "invalid", WorkspaceID: "missing", CreatedAt: created, UpdatedAt: created}); err == nil {
		t.Fatal("missing workspace must violate foreign key")
	}
}
