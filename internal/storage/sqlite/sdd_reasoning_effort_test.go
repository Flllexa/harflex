package sqlite

import (
	"reflect"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestBrainstormSelectionRequiresExactAdvertisedEffortSnapshot(t *testing.T) {
	choice := brainstormChoice()
	choice.ReasoningEffort = "high"
	if validBrainstormSelection(choice) {
		t.Fatal("accepted effort without advertised capability")
	}
	choice.SupportedReasoningEfforts = []string{"low", "high"}
	if !validBrainstormSelection(choice) {
		t.Fatal("rejected exact advertised effort")
	}
	choice.ReasoningEffort = "HIGH"
	if validBrainstormSelection(choice) {
		t.Fatal("effort matching was not exact")
	}
	choice.ReasoningEffort = ""
	choice.SupportedReasoningEfforts = nil
	if !validBrainstormSelection(choice) {
		t.Fatal("Automatic without levels rejected")
	}
}

func TestReasoningCapabilityMigrationAndImmutableAttempt(t *testing.T) {
	store, path, run, _ := brainstormFixture(t)
	pipeline, err := store.GetPipeline(t.Context(), run.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := catalog.SessionRecord{ID: "legacy-auto-session", WorkspaceID: workspace.ID, BackendID: "profile", Status: "ready", CreatedAt: now, UpdatedAt: now}
	choice := brainstormChoice()
	choice.SessionID, choice.WorkspacePath = session.ID, workspace.Path
	if err := store.CreateSessionWithSnapshots(t.Context(), session, nil, "", nil, "agent_session", &choice); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSessionModelSelection(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DROP TRIGGER session_model_selection_immutable`, `ALTER TABLE session_model_selections DROP COLUMN supported_reasoning_efforts`, `DELETE FROM schema_migrations WHERE version=33`} {
		if _, err := store.DB().ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	after, err := upgraded.GetSessionModelSelection(t.Context(), session.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed legacy Automatic", before, after, err)
	}
	in := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, upgraded, run.ID, "effort_attempt_001"), Kind: "question", Selection: brainstormChoice(), EstimatedInputTokens: 1000}
	in.Selection.ReasoningEffort, in.Selection.SupportedReasoningEfforts = "high", []string{"low", "high"}
	attempt, admitted, err := upgraded.BeginBrainstormQuestion(t.Context(), in)
	if err != nil || !admitted {
		t.Fatal(attempt, admitted, err)
	}
	in.Selection.ReasoningEffort = "low"
	in.Selection.SupportedReasoningEfforts[1] = "medium"
	if _, admitted, err := upgraded.BeginBrainstormQuestion(t.Context(), in); err == nil || admitted {
		t.Fatal("changed snapshot replay admitted")
	}
	read, err := upgraded.GetBrainstorming(t.Context(), run.ID)
	if err != nil || len(read.Attempts) != 1 || read.Attempts[0].Selection.ReasoningEffort != "high" || !reflect.DeepEqual(read.Attempts[0].Selection.SupportedReasoningEfforts, []string{"low", "high"}) {
		t.Fatal("attempt snapshot mutated", read, err)
	}
}
