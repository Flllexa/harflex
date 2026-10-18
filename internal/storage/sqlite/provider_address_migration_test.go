package sqlite

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

// A profile saved before 061 may already have moved to another address, and nothing recorded when: the migration
// takes its last edit as that moment, so its older conversations do not follow it there.
func TestProviderAddressMigrationTakesTheLastEditOfOlderProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "older.db")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	edited := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertProviderProfile(t.Context(), catalog.ProviderProfile{ID: "older", Name: "Older", Kind: "openai_compatible", ProviderType: "generic", BaseURL: "http://moved.example/v1", Model: "m", CreatedAt: edited.Add(-time.Hour), UpdatedAt: edited}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `ALTER TABLE provider_profiles DROP COLUMN base_url_changed_at; DELETE FROM schema_migrations WHERE version = 61`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	profile, err := reopened.GetProviderProfile(t.Context(), "older")
	if err != nil || !profile.BaseURLChangedAt.Equal(edited) {
		t.Fatalf("an older profile has no address change recorded: %v %v", profile.BaseURLChangedAt, err)
	}
}

// Following the profile changes the model a conversation uses, after a new catalog check, and never the session,
// backend or folder it is bound to.
func TestSessionModelRebindKeepsWhatTheChoiceIsBoundTo(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	pipeline, err := store.GetPipeline(t.Context(), run.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := catalog.SessionRecord{ID: "rebound-session", WorkspaceID: workspace.ID, BackendID: "profile", Status: "ready", CreatedAt: now, UpdatedAt: now}
	choice := brainstormChoice()
	choice.SessionID, choice.WorkspacePath = session.ID, workspace.Path
	if err := store.CreateSessionWithSnapshots(t.Context(), session, nil, "", nil, "agent_session", &choice); err != nil {
		t.Fatal(err)
	}
	next := choice
	next.ModelID, next.CheckedAt = "another-model", choice.CheckedAt.Add(time.Minute)
	if err := store.RebindSessionModelSelection(t.Context(), session.ID, "api:new", &next); err != nil {
		t.Fatalf("a rebind to another model was refused: %v", err)
	}
	for _, statement := range []string{`UPDATE session_model_selections SET workspace_path='/elsewhere', checked_at='2030-01-01T00:00:00.000000000Z' WHERE session_id=?`,
		`UPDATE session_model_selections SET backend_id='other', checked_at='2030-01-01T00:00:00.000000000Z' WHERE session_id=?`,
		`UPDATE session_model_selections SET model_id='unchecked' WHERE session_id=?`} {
		if _, err := store.DB().ExecContext(t.Context(), statement, session.ID); err == nil || !strings.Contains(err.Error(), "immutable session model selection") {
			t.Fatalf("%s went through: %v", statement, err)
		}
	}
}
