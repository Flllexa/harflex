package application

import (
	"errors"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestCodexCannotBeComposedForAnyProfessionalSDDMode(t *testing.T) {
	s, _, _ := setup(t)
	cli := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0"}
	s.external[cli.id] = cli
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storedWorkspace, err := s.store.GetWorkspace(t.Context(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"sdd_readonly", "sdd_code", "evaluation"} {
		record := catalog.SessionRecord{ID: "blocked-" + mode, WorkspaceID: workspace.ID, BackendID: "codex", Mode: mode, Status: "ready", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if _, _, err := s.makeRunnerAt(&record, storedWorkspace, restoredHistory{}, false, nil, "", nil, ""); !errors.Is(err, ErrSDDCLIReadIsolationUnavailable) {
			t.Fatalf("mode %s composed Codex despite missing read confinement: %v", mode, err)
		}
	}
	if cli.detectCount != 0 || cli.queryCount != 0 || len(cli.runs) != 0 {
		t.Fatalf("SDD composition probed or ran Codex before the isolation gate: detects=%d queries=%d runs=%d", cli.detectCount, cli.queryCount, len(cli.runs))
	}
}

func TestAuthoringStageCompositionRejectsPersistedCodexSelectionBeforePipelineLookup(t *testing.T) {
	s, db, _ := setup(t)
	cli := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0"}
	s.external[cli.id] = cli
	selection := catalog.ModelSelection{BackendID: "codex", Source: "codex_app_server", ModelID: "gpt-6-sol"}
	_, err := s.createAuthoringReadOnlySession(catalog.AuthoringStageRequest{PipelineID: "missing-pipeline"}, catalog.AuthoringStageAttempt{Selection: selection})
	if !errors.Is(err, ErrSDDCLIReadIsolationUnavailable) {
		t.Fatalf("persisted Codex stage selection escaped the composition gate: %v", err)
	}
	if cli.detectCount != 0 || cli.queryCount != 0 || len(cli.runs) != 0 {
		t.Fatalf("blocked stage composition probed or ran Codex: detects=%d queries=%d runs=%d", cli.detectCount, cli.queryCount, len(cli.runs))
	}
	var sessions int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("blocked stage composition created %d sessions: %v", sessions, err)
	}
}
