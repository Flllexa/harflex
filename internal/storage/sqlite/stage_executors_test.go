package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func stageExecutorStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, id := range []string{"workspace-1", "workspace-2"} {
		if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: id, Path: t.TempDir(), Profile: "ask", CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestStageExecutorsAreKeptPerProjectAndPhase(t *testing.T) {
	store := stageExecutorStore(t)
	at := time.Date(2026, 10, 2, 9, 0, 0, 123456789, time.UTC)
	for _, value := range []catalog.StageExecutor{
		{WorkspaceID: "workspace-1", Stage: sdd.PRs, BackendID: "api", ModelID: "pr-model", UpdatedAt: at},
		{WorkspaceID: "workspace-1", Stage: sdd.Discovery, BackendID: "codex", ModelID: "gpt-5-codex", UpdatedAt: at},
		{WorkspaceID: "workspace-1", Stage: sdd.Code, BackendID: "api", UpdatedAt: at},
		{WorkspaceID: "workspace-2", Stage: sdd.Spec, BackendID: "other", UpdatedAt: at},
	} {
		if err := store.SaveWorkspaceStageExecutor(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := store.ListWorkspaceStageExecutors(t.Context(), "workspace-1")
	if err != nil || len(listed) != 3 || listed[0].Stage != sdd.Discovery || listed[1].Stage != sdd.Code || listed[2].Stage != sdd.PRs {
		t.Fatalf("phases are not listed in pipeline order: %+v %v", listed, err)
	}
	if listed[0].BackendID != "codex" || listed[0].ModelID != "gpt-5-codex" || !listed[0].UpdatedAt.Equal(at) || listed[1].ModelID != "" {
		t.Fatalf("what was saved did not come back: %+v", listed)
	}
	if got, err := store.GetWorkspaceStageExecutor(t.Context(), "workspace-2", sdd.Spec); err != nil || got.BackendID != "other" {
		t.Fatalf("another project's phase: %+v %v", got, err)
	}
	if _, err := store.GetWorkspaceStageExecutor(t.Context(), "workspace-2", sdd.Plan); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a phase that chose nothing must read as absent so the default applies: %v", err)
	}
	// Saving again replaces the phase instead of adding a row.
	if err := store.SaveWorkspaceStageExecutor(t.Context(), catalog.StageExecutor{WorkspaceID: "workspace-1", Stage: sdd.Code, BackendID: "codex", ModelID: "gpt-5", UpdatedAt: at.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if listed, err = store.ListWorkspaceStageExecutors(t.Context(), "workspace-1"); err != nil || len(listed) != 3 || listed[1].BackendID != "codex" || listed[1].ModelID != "gpt-5" {
		t.Fatalf("saving again did not replace: %+v %v", listed, err)
	}
	if err := store.DeleteWorkspaceStageExecutor(t.Context(), "workspace-1", sdd.Code); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteWorkspaceStageExecutor(t.Context(), "workspace-1", sdd.Code); err != nil {
		t.Fatalf("deleting a phase that had none: %v", err)
	}
	if listed, err = store.ListWorkspaceStageExecutors(t.Context(), "workspace-1"); err != nil || len(listed) != 2 {
		t.Fatalf("delete left the phase chosen: %+v %v", listed, err)
	}
}

func TestStageExecutorTableRefusesWhatIsNotAPhaseOrAProject(t *testing.T) {
	store := stageExecutorStore(t)
	at := time.Now().UTC()
	if err := store.SaveWorkspaceStageExecutor(t.Context(), catalog.StageExecutor{WorkspaceID: "workspace-1", Stage: "deploy", BackendID: "api", UpdatedAt: at}); err == nil {
		t.Fatal("an unknown phase was stored")
	}
	if err := store.SaveWorkspaceStageExecutor(t.Context(), catalog.StageExecutor{WorkspaceID: "workspace-1", Stage: sdd.Spec, BackendID: "", UpdatedAt: at}); err == nil {
		t.Fatal("a phase with no executor was stored")
	}
	if err := store.SaveWorkspaceStageExecutor(t.Context(), catalog.StageExecutor{WorkspaceID: "workspace-gone", Stage: sdd.Spec, BackendID: "api", UpdatedAt: at}); err == nil {
		t.Fatal("a phase of a project that does not exist was stored")
	}
}

// A catalog from before the table existed gets an empty one: every phase keeps following the default of Settings.
func TestOpenCreatesTheStageExecutorTableEmpty(t *testing.T) {
	store := stageExecutorStore(t)
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_stage_executors`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("the phase executors of an upgraded catalog: %d rows, %v", count, err)
	}
	var applied int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE version=50`).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("migration 50 recorded %d times: %v", applied, err)
	}
}
