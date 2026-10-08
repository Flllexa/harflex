package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestSessionModelSelectionIsAtomicImmutableAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-selection.db")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Nanosecond)
	workspace := catalog.Workspace{ID: "workspace", Path: t.TempDir(), Profile: "ask", CreatedAt: now}
	if err := store.UpsertWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	session := catalog.SessionRecord{ID: "session", WorkspaceID: workspace.ID, BackendID: "codex", Status: "ready", CreatedAt: now, UpdatedAt: now}
	selection := catalog.ModelSelection{SessionID: session.ID, BackendID: session.BackendID, ModelID: "runtime-model", ReasoningEffort: "high", CatalogRevision: "catalog-revision", LocalRevision: "local-revision", Source: "codex_app_server", ExecutablePath: "/opt/homebrew/bin/codex", ExecutableVersion: "0.157.0", WorkspacePath: workspace.Path, CheckedAt: now}
	if err := store.CreateSessionWithSnapshots(t.Context(), session, nil, "", nil, "external_session", &selection); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSessionModelSelection(t.Context(), session.ID)
	if err != nil || !reflect.DeepEqual(got, selection) {
		t.Fatalf("selection roundtrip: %+v %v", got, err)
	}
	if err := store.UpsertSession(t.Context(), catalog.SessionRecord{ID: session.ID, WorkspaceID: workspace.ID, BackendID: "codex", Status: "completed", CreatedAt: now, UpdatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	got, err = store.GetSessionModelSelection(t.Context(), session.ID)
	if err != nil || !reflect.DeepEqual(got, selection) {
		t.Fatalf("selection after restart/status update: %+v %v", got, err)
	}
	bad := session
	bad.ID = "rolled-back"
	if err := store.CreateSessionWithSnapshots(t.Context(), bad, nil, "", nil, "external_session", &selection); err == nil {
		t.Fatal("mismatched selection inserted")
	}
	if _, err := store.GetSession(t.Context(), bad.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("session survived failed selection transaction: %v", err)
	}
	if _, err := store.GetSessionModelSelection(t.Context(), bad.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("selection survived failed transaction: %v", err)
	}
	selection.ModelID = "changed-model"
	if err := store.CreateSessionWithSnapshots(t.Context(), session, nil, "", nil, "external_session", &selection); err == nil {
		t.Fatal("immutable selection overwritten")
	}
	got, err = store.GetSessionModelSelection(t.Context(), session.ID)
	if err != nil || got.ModelID != "runtime-model" {
		t.Fatalf("immutable selection changed: %+v %v", got, err)
	}
}

func TestAPIModelSelectionRoundTripWithoutCLIFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-selection.db")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	workspace := catalog.Workspace{ID: "workspace", Path: t.TempDir(), Profile: "ask", CreatedAt: now}
	if err := store.UpsertWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	session := catalog.SessionRecord{ID: "sdd-attempt", WorkspaceID: workspace.ID, BackendID: "api", Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	selection := catalog.ModelSelection{SessionID: session.ID, BackendID: "api", ModelID: "chosen", CatalogRevision: "api:revision", Source: "openrouter_general_unfiltered", Destination: "https://example.test", CredentialIdentity: "vault:account:fingerprint", Status: "listed_unfiltered", ConfirmUnfiltered: true, MaxOutputTokens: 321, ContextLength: 8192, WorkspacePath: workspace.Path, CheckedAt: now}
	if err := store.CreateSessionWithSnapshots(t.Context(), session, nil, "", nil, "agent_session", &selection); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	got, err := store.GetSessionModelSelection(t.Context(), session.ID)
	if err != nil || !reflect.DeepEqual(got, selection) {
		t.Fatalf("API selection after restart = %+v, %v", got, err)
	}
}

func TestCodexSDDModelSelectionRoundTripWithoutAPIProfileOrCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-sdd-selection.db")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	workspace := catalog.Workspace{ID: "workspace", Path: t.TempDir(), Profile: "ask", CreatedAt: now}
	if err := store.UpsertWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	session := catalog.SessionRecord{ID: "codex-sdd-session", WorkspaceID: workspace.ID, BackendID: "codex", Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	selection := catalog.ModelSelection{SessionID: session.ID, BackendID: "codex", ModelID: "provider/model-exact", ReasoningEffort: "high", CatalogRevision: "catalog-revision", LocalRevision: "local-revision", Source: "codex_app_server", ExecutablePath: "/opt/homebrew/bin/codex", ExecutableVersion: "0.157.0", WorkspacePath: workspace.Path, CheckedAt: now, Status: "listed", MaxOutputTokens: 256, MaxAssistantOutputBytes: 2 * 1024}
	if err := store.CreateSessionWithSnapshots(t.Context(), session, nil, "", nil, "external_session", &selection); err != nil {
		t.Fatalf("Codex SDD selection without API credential was rejected: %v", err)
	}
	got, err := store.GetSessionModelSelection(t.Context(), session.ID)
	want := selection
	want.MaxAssistantOutputBytes = 0 // The phase-scoped cap is durably carried by the SDD attempt snapshot.
	if err != nil || !reflect.DeepEqual(got, want) || got.CredentialIdentity != "" {
		t.Fatalf("Codex SDD selection roundtrip: %+v %v", got, err)
	}
}
