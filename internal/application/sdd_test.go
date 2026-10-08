package application

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestPipelinePersistsArtifactsAndAuditedBypassAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sdd.db")
	db, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(t.Context(), Dependencies{Store: db, External: map[string]ExternalBackend{}})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar faturas", Objective: "Adicionar exportação em CSV"})
	if err != nil || run.CurrentStage != "discovery" {
		t.Fatalf("create pipeline: %+v %v", run, err)
	}
	if _, err := s.AdvancePipeline(run.ID); !errors.Is(err, sdd.ErrEvidenceRequired) {
		t.Fatalf("advanced without artifact: %v", err)
	}
	if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: "discovery", Content: "Objetivo e limites revisados"}); err != nil {
		t.Fatal(err)
	}
	run, err = s.AdvancePipeline(run.ID)
	if err != nil || run.CurrentStage != "spec" || run.StageStatus["discovery"] != "completed" {
		t.Fatalf("advance discovery: %+v %v", run, err)
	}
	run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID, Reason: "SPEC já aprovada fora do app"})
	if err != nil || run.CurrentStage != "plan" || run.StageStatus["spec"] != "skipped" {
		t.Fatalf("skip spec: %+v %v", run, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s = NewService(t.Context(), Dependencies{Store: db, External: map[string]ExternalBackend{}})
	loaded, err := s.GetPipeline(run.ID)
	if err != nil || loaded.CurrentStage != "plan" || loaded.StageStatus["spec"] != "skipped" || loaded.Artifacts["discovery"].Content != "Objetivo e limites revisados" {
		t.Fatalf("reopened pipeline: %+v %v", loaded, err)
	}
	activity, err := db.ListAfter(t.Context(), run.ID, 0)
	if err != nil || len(activity) != 4 || activity[0].Type != "pipeline.created" || activity[3].Type != "pipeline.stage.skipped" {
		t.Fatalf("pipeline journal: %+v %v", activity, err)
	}
}
