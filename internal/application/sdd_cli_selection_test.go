package application

import (
	"errors"
	"testing"
	"time"
)

func TestCodexSDDAdmissionFailsClosedWithoutReadIsolation(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "codex_stale_pipeline", Discovery: "Create a local TODO app"})
	if err != nil {
		t.Fatal(err)
	}
	cli := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0", efforts: []string{"low", "high"}}
	s.external[cli.id] = cli
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	selection := APIModelSelectionInput{Executor: "codex_cli", BackendID: "codex", ModelID: "provider/model-exact", CatalogRevision: "stale-revision", Source: listed.Source, CheckedAt: listed.CheckedAt, ReasoningEffort: "high", MaxOutputTokens: 256}
	input := StartBrainstormingInput{PipelineID: pipeline.ID, RequestID: "codex_stale_start", PipelineRevision: pipeline.Revision, DiscoveryVersion: 1, Selection: selection}
	if _, err := s.StartBrainstorming(input); !errors.Is(err, ErrSDDCLIReadIsolationUnavailable) {
		t.Fatalf("Codex SDD was admitted without read isolation: %v", err)
	}
	var runs int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM pipeline_brainstorm_runs WHERE pipeline_id=?`, pipeline.ID).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("stale selection created %d brainstorm runs: %v", runs, err)
	}
	queries := cli.queryCount
	selection.CatalogRevision = listed.ProfileRevision
	selection.CredentialToken = "a-fabricated-credential-token"
	input.RequestID = "codex_credential_start"
	input.Selection = selection
	if _, err := s.StartBrainstorming(input); !errors.Is(err, ErrSDDCLIReadIsolationUnavailable) {
		t.Fatalf("Codex SDD admission did not fail closed: %v", err)
	}
	if cli.queryCount != queries {
		t.Fatalf("credential-bearing CLI selection queried catalog: before=%d after=%d", queries, cli.queryCount)
	}
}

func TestQALoopConfirmsAnOldCodexPickAgainBeforeEachRound(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "TODO", Objective: "Criar TODO"})
	if err != nil {
		t.Fatal(err)
	}
	cli := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0", efforts: []string{"low", "high"}}
	s.external[cli.id] = cli
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	old := APIModelSelectionInput{Executor: "codex_cli", BackendID: "codex", ModelID: "provider/model-exact", CatalogRevision: listed.ProfileRevision, Source: listed.Source, CheckedAt: time.Now().Add(-10 * time.Minute), ReasoningEffort: "high"}
	fresh := s.freshRoleChoice(run.ID, PipelineRoleChoice{BackendID: "codex", Selection: &old})
	if fresh.Selection.ModelID != old.ModelID || fresh.Selection.ReasoningEffort != "high" || fresh.Selection.CatalogRevision != listed.ProfileRevision {
		t.Fatalf("the Codex pick changed: %+v", fresh.Selection)
	}
	if time.Since(fresh.Selection.CheckedAt) > time.Minute {
		t.Fatalf("the Codex pick was not confirmed again: %v", fresh.Selection.CheckedAt)
	}
}
