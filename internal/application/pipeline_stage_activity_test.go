package application

import (
	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"testing"
)

func TestPipelineStageInspectionUsesRealSessionsWithoutStartingInference(t *testing.T) {
	s, db, pipeline := pipelineDesignServiceFixture(t)
	providers := installDesignAnswers(t, s, db, pipeline.ID)
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetPipelineStageActivity(pipeline.ID, "spec")
	if err != nil || before.SessionID != "" || len(*providers) != 0 {
		t.Fatalf("pending inspection inferred: %+v %v", before, err)
	}
	if _, err = s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_stage_inspection_01"), Target: "all", Message: "Preparar documentos"}); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"spec", "plan"} {
		activity, err := s.GetPipelineStageActivity(pipeline.ID, stage)
		if err != nil || activity.SessionID == "" || activity.ModelID != "selected-model" || activity.Status != "completed" {
			t.Fatalf("%s inspection=%+v %v", stage, activity, err)
		}
	}
	sessions, err := s.ListSessions(ListSessionsInput{WorkspaceID: pipeline.WorkspaceID})
	if err != nil || len(sessions) != 2 {
		t.Fatalf("sessions=%+v %v", sessions, err)
	}
	for _, session := range sessions {
		if session.Purpose != "preparation" || session.Title == "" || session.Resumable {
			t.Fatalf("internal preparation exposed as a user chat: %+v", session)
		}
	}
	after, err := s.GetPipeline(pipeline.ID)
	if err != nil || after.CurrentStage != "discovery" || len(*providers) != 2 {
		t.Fatal("stage inspection changed phase or invoked model")
	}
	if _, err := s.GetPipelineStageActivity(pipeline.ID, "unsafe"); err == nil {
		t.Fatal("unknown stage was accepted")
	}
}

func TestPipelineStageInspectionReportsRejectedDocumentResponseAsFailed(t *testing.T) {
	s, _, pipeline := pipelineDesignServiceFixture(t)
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		return &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: `{"document":"invalid","reply":42}`}}}, nil
	}
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_rejected_output_01"), Target: "all", Message: "Preparar documentos"}); err != nil {
		t.Fatal(err)
	}
	activity, err := s.GetPipelineStageActivity(pipeline.ID, "spec")
	if err != nil || activity.Status != "failed" || activity.AttemptStatus != "failed" || activity.ErrorCode != "pipeline_design_invalid_response" {
		t.Fatalf("rejected model response was shown as success: %+v %v", activity, err)
	}
}

func TestPipelineStageInspectionDoesNotReportAnUnstartedCodeRunAsBusy(t *testing.T) {
	s, _, pipeline, _, _ := codexHostCodeFixture(t, "ask")
	activity, err := s.GetPipelineStageActivity(pipeline.ID, "code")
	if err != nil || activity.SessionID != "" || activity.Status != "ready" || activity.Phase != "" {
		t.Fatalf("unstarted Code was shown running: %+v %v", activity, err)
	}
}

func TestPipelineStageInspectionDoesNotReuseCompletedCodeAfterRevisionRequest(t *testing.T) {
	s, pipeline := pipelineCodeReviewFixture(t)
	pipeline = decidePipelineStageReviewForTest(t, s, pipeline, "code", "request_revision", "Adicionar suporte a teclado.")
	activity, err := s.GetPipelineStageActivity(pipeline.ID, "code")
	if err != nil || activity.SessionID != "" || activity.Status != "ready" {
		t.Fatalf("revised Code reused past execution: %+v %v", activity, err)
	}
}
