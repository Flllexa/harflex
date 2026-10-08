package application

import (
	"errors"
	"testing"
)

func TestCodexCLIProfessionalSDDIsUnavailableWithoutReadIsolation(t *testing.T) {
	s, db, vault := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "codex_full_flow_pipeline", Discovery: "Create a TODO app with localStorage"})
	if err != nil {
		t.Fatal(err)
	}
	cli := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0", efforts: []string{"low", "high"}}
	s.external[cli.id] = cli
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: "codex"})
	if err != nil || !listed.Complete || len(listed.Models) == 0 {
		t.Fatalf("Codex catalog: %+v %v", listed, err)
	}
	selection := APIModelSelectionInput{Executor: "codex_cli", BackendID: "codex", ModelID: listed.Models[0].ID, CatalogRevision: listed.ProfileRevision, Source: listed.Source, CheckedAt: listed.CheckedAt, ReasoningEffort: "high", MaxOutputTokens: 256}
	_, err = s.StartBrainstorming(StartBrainstormingInput{PipelineID: pipeline.ID, RequestID: "codex_full_flow_start", PipelineRevision: pipeline.Revision, DiscoveryVersion: 1, Selection: selection})
	if !errors.Is(err, ErrSDDCLIReadIsolationUnavailable) {
		t.Fatalf("Professional SDD ran Codex outside its read boundary: %v", err)
	}
	if len(cli.runs) != 0 || vault.gets != 0 {
		t.Fatalf("unsafe Codex flow executed or read API credentials: runs=%d vault=%d", len(cli.runs), vault.gets)
	}
	var runs int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM pipeline_brainstorm_runs WHERE pipeline_id=?`, pipeline.ID).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("unsafe Codex flow created %d brainstorm runs: %v", runs, err)
	}
}
