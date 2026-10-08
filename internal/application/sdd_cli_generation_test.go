package application

import (
	"errors"
	"testing"
)

func TestGenerateBrainstormQuestionRejectsCodexCLIWithoutReadIsolation(t *testing.T) {
	s, db, vault := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "codex_question_pipeline", Discovery: "Create a simple TODO app"})
	if err != nil {
		t.Fatal(err)
	}
	cli := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0", efforts: []string{"low", "high"}}
	s.external[cli.id] = cli
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: "codex"})
	if err != nil || !listed.Complete {
		t.Fatalf("Codex catalog: %+v %v", listed, err)
	}
	selection := APIModelSelectionInput{Executor: "codex_cli", BackendID: "codex", ModelID: "provider/model-exact", CatalogRevision: listed.ProfileRevision, Source: listed.Source, CheckedAt: listed.CheckedAt, ReasoningEffort: "high", MaxOutputTokens: 256}
	_, err = s.StartBrainstorming(StartBrainstormingInput{PipelineID: pipeline.ID, RequestID: "codex_question_start", PipelineRevision: pipeline.Revision, DiscoveryVersion: 1, Selection: selection})
	if !errors.Is(err, ErrSDDCLIReadIsolationUnavailable) {
		t.Fatalf("unsafe Codex Discovery was admitted: %v", err)
	}
	if len(cli.runs) != 0 || vault.gets != 0 {
		t.Fatalf("Codex CLI executed or loaded API credentials before the isolation gate: runs=%d vault=%d", len(cli.runs), vault.gets)
	}
	var runs int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM pipeline_brainstorm_runs WHERE pipeline_id=?`, pipeline.ID).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("unsafe Codex selection created %d brainstorm runs: %v", runs, err)
	}
}
