package application

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

const planAnswerForTest = `{"document":"# Plan\n\n1. Criar a lista de contratos.\n2. Filtrar por mês.","reply":"O plano está pronto."}`

func designDigests(design PipelineDesignDTO, stages ...string) map[string]string {
	digests := map[string]string{}
	for _, stage := range stages {
		digests[stage] = design.Documents[stage].ContentDigest
	}
	return digests
}

// preparedBrainstormForTest prepares SPEC and Brainstorm through the AI and leaves the answers that follow scripted.
func preparedBrainstormForTest(t *testing.T, more ...string) (*Service, PipelineDTO, PipelineDesignDTO, *[]*brainstormTestProvider) {
	t.Helper()
	s, _, pipeline := pipelineDesignServiceFixture(t)
	providers := scriptDesignAnswers(t, s, append([]string{specAnswerForTest, brainstormAnswerForTest()}, more...)...)
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	design, err = s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_for_approval_01"), Target: "all", Message: "Prepare a SPEC e o Brainstorm."})
	if err != nil || design.Documents["brainstorm"].Content == "" {
		t.Fatalf("preparation = %+v, %v", design.Documents["brainstorm"], err)
	}
	return s, pipeline, design, providers
}

// waitForPipelineDesign waits until no preparation is running and returns the design as it settled.
func waitForPipelineDesign(t *testing.T, s *Service, pipelineID string) PipelineDesignDTO {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		design, err := s.OpenPipelineDesign(pipelineID)
		if err != nil {
			t.Fatal(err)
		}
		if design.ActiveAttemptID == "" && design.State != "running" {
			return design
		}
		if time.Now().After(deadline) {
			t.Fatalf("preparation did not settle: %+v", design.Attempts)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestApproveBrainstormPublishesAndStartsPlan(t *testing.T) {
	s, pipeline, design, providers := preparedBrainstormForTest(t, planAnswerForTest)
	run, err := s.ApprovePipelineBrainstorm(ApprovePipelineDesignInput{Ref: designRefForTest(design, "approve_brainstorm_01"), Digests: designDigests(design, "discovery", "spec", "brainstorm")})
	if err != nil {
		t.Fatal(err)
	}
	if run.CurrentStage != "plan" || run.StageStatus["discovery"] != "completed" || run.StageStatus["spec"] != "completed" || run.StageStatus["brainstorm"] != "completed" || run.StageStatus["plan"] != "active" {
		t.Fatalf("approval moved to %q with %v", run.CurrentStage, run.StageStatus)
	}
	if run.Artifacts["brainstorm"].Content != design.Documents["brainstorm"].Content || run.Artifacts["spec"].Content != design.Documents["spec"].Content {
		t.Fatal("approval must publish the exact SPEC and Brainstorm")
	}
	settled := waitForPipelineDesign(t, s, pipeline.ID)
	if !strings.Contains(settled.Documents["plan"].Content, "Filtrar por mês") || len(*providers) != 3 {
		t.Fatalf("Plan was not prepared after approval: %+v (calls %d)", settled.Attempts, len(*providers))
	}
	planPrompt := lastUserPrompt((*providers)[2])
	if !strings.Contains(planPrompt, "Prepare o Plan a partir da SPEC e do Brainstorm aprovados.") || !strings.Contains(planPrompt, "Abrir contratos") || strings.Contains(planPrompt, "<!doctype") {
		t.Fatalf("Plan must see the approved flow and screens, without their HTML: %.600s", planPrompt)
	}
	approved, err := s.ApprovePipelineDesign(ApprovePipelineDesignInput{Ref: designRefForTest(settled, "approve_plan_after_brainstorm_01"), Digests: designDigests(settled, "discovery", "spec", "brainstorm", "plan")})
	if err != nil || approved.CurrentStage != "code" || approved.StageStatus["brainstorm"] != "completed" || approved.Artifacts["plan"].Content != settled.Documents["plan"].Content {
		t.Fatalf("Plan approval = %q %v, %v", approved.CurrentStage, approved.StageStatus, err)
	}
}

func TestApproveBrainstormRefusesStaleOrRunning(t *testing.T) {
	s, pipeline, design, _ := preparedBrainstormForTest(t)
	digests := designDigests(design, "discovery", "spec", "brainstorm")
	wrong := designDigests(design, "discovery", "spec", "brainstorm")
	wrong["brainstorm"] = strings.Repeat("0", 64)
	if _, err := s.ApprovePipelineBrainstorm(ApprovePipelineDesignInput{Ref: designRefForTest(design, "approve_brainstorm_wrong_01"), Digests: wrong}); err == nil {
		t.Fatal("a digest that is not the current Brainstorm was approved")
	}
	if _, err := s.ApprovePipelineBrainstorm(ApprovePipelineDesignInput{Ref: designRefForTest(design, "approve_brainstorm_partial_01"), Digests: designDigests(design, "spec", "brainstorm")}); err == nil {
		t.Fatal("an approval without Discovery was accepted")
	}
	edited, err := s.EditPipelineDesignDocument(EditPipelineDesignDocumentInput{Ref: designRefForTest(design, "edit_spec_after_brainstorm_01"), Stage: "spec", Content: "# SPEC revisada\n\nTambém exportar CSV."})
	if err != nil || !edited.Documents["brainstorm"].Stale {
		t.Fatalf("editing SPEC must make Brainstorm stale: %+v %v", edited.Documents["brainstorm"], err)
	}
	if _, err := s.ApprovePipelineBrainstorm(ApprovePipelineDesignInput{Ref: designRefForTest(edited, "approve_brainstorm_stale_01"), Digests: designDigests(edited, "discovery", "spec", "brainstorm")}); err == nil {
		t.Fatal("a stale Brainstorm was approved")
	}
	selection := catalog.ModelSelection{BackendID: "design-api", ModelID: "selected-model", Source: "test", CatalogRevision: "catalog-v1", WorkspacePath: "/project", CheckedAt: time.Now().UTC(), MaxOutputTokens: brainstormOutputTokens}
	request := catalog.PipelineDesignAttemptRequest{Ref: designRefForTest(edited, "running_brainstorm_01").request(), Message: "Ajustar", Target: "brainstorm", Selections: map[sdd.Stage]catalog.ModelSelection{sdd.Brainstorm: selection}}
	request.IntentHash, _ = request.Hash()
	running, _, admitted, err := s.store.BeginPipelineDesignAttempt(t.Context(), request)
	if err != nil || !admitted {
		t.Fatalf("running attempt not admitted: %v", err)
	}
	if _, err := s.ApprovePipelineBrainstorm(ApprovePipelineDesignInput{Ref: designRefForTest(pipelineDesignDTO(running), "approve_brainstorm_running_01"), Digests: digests}); err == nil {
		t.Fatal("Brainstorm was approved while a preparation was running")
	}
	current, err := s.GetPipeline(pipeline.ID)
	if err != nil || current.CurrentStage != "discovery" {
		t.Fatalf("refused approvals must leave the pipeline where it was: %q %v", current.CurrentStage, err)
	}
}

func TestApprovePlanNeedsBrainstormDigest(t *testing.T) {
	s, pipeline, design, _ := preparedBrainstormForTest(t, planAnswerForTest)
	if _, err := s.ApprovePipelineDesign(ApprovePipelineDesignInput{Ref: designRefForTest(design, "approve_plan_too_early_01"), Digests: designDigests(design, "discovery", "spec", "brainstorm", "plan")}); err == nil {
		t.Fatal("the Plan gate opened before the Brainstorm was approved")
	}
	if _, err := s.ApprovePipelineBrainstorm(ApprovePipelineDesignInput{Ref: designRefForTest(design, "approve_brainstorm_02"), Digests: designDigests(design, "discovery", "spec", "brainstorm")}); err != nil {
		t.Fatal(err)
	}
	settled := waitForPipelineDesign(t, s, pipeline.ID)
	if _, err := s.ApprovePipelineDesign(ApprovePipelineDesignInput{Ref: designRefForTest(settled, "approve_plan_three_01"), Digests: designDigests(settled, "discovery", "spec", "plan")}); err == nil {
		t.Fatal("Plan approval without the Brainstorm digest was accepted")
	}
	if approved, err := s.ApprovePipelineDesign(ApprovePipelineDesignInput{Ref: designRefForTest(settled, "approve_plan_four_01"), Digests: designDigests(settled, "discovery", "spec", "brainstorm", "plan")}); err != nil || approved.CurrentStage != "code" {
		t.Fatalf("Plan approval with four digests = %+v, %v", approved.CurrentStage, err)
	}
}

func TestApprovePlanOfWorkPreparedBeforeBrainstorm(t *testing.T) {
	s, db, pipeline := pipelineDesignServiceFixture(t)
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []struct{ stage, content string }{{"spec", "# SPEC\n\nTarefas em localStorage."}, {"plan", "# Plan\n\n1. Criar o HTML."}} {
		if design, err = s.EditPipelineDesignDocument(EditPipelineDesignDocumentInput{Ref: designRefForTest(design, "edit_old_"+edit.stage+"_0001"), Stage: edit.stage, Content: edit.content}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='plan', stage_status=CAST(json_set(CAST(stage_status AS TEXT),'$.discovery','completed','$.spec','completed','$.brainstorm','skipped','$.plan','active') AS BLOB) WHERE id=?`, pipeline.ID); err != nil {
		t.Fatal(err)
	}
	approved, err := s.ApprovePipelineDesign(ApprovePipelineDesignInput{Ref: designRefForTest(design, "approve_old_plan_01"), Digests: designDigests(design, "discovery", "spec", "plan")})
	if err != nil || approved.CurrentStage != "code" || approved.StageStatus["brainstorm"] != "skipped" {
		t.Fatalf("work prepared before Brainstorm must still approve its Plan: %q %v, %v", approved.CurrentStage, approved.StageStatus, err)
	}
}

func TestContinuationInheritsBrainstorm(t *testing.T) {
	s, pipeline, design, _ := preparedBrainstormForTest(t, planAnswerForTest)
	if _, err := s.ApprovePipelineBrainstorm(ApprovePipelineDesignInput{Ref: designRefForTest(design, "approve_brainstorm_03"), Digests: designDigests(design, "discovery", "spec", "brainstorm")}); err != nil {
		t.Fatal(err)
	}
	settled := waitForPipelineDesign(t, s, pipeline.ID)
	approved, err := s.ApprovePipelineDesign(ApprovePipelineDesignInput{Ref: designRefForTest(settled, "approve_plan_continuation_01"), Digests: designDigests(settled, "discovery", "spec", "brainstorm", "plan")})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.DeriveAuthoringPipeline(DeriveAuthoringPipelineInput{ParentPipelineID: pipeline.ID, RequestID: "derive_with_brainstorm_01", ExpectedRevision: approved.Revision, Discovery: settled.Documents["discovery"].Content})
	if err != nil {
		t.Fatal(err)
	}
	derived, err := s.OpenPipelineDesign(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"spec", "brainstorm", "plan"} {
		before, after := settled.Documents[stage], derived.Documents[stage]
		if after.Content != before.Content || after.SourceDigest != before.SourceDigest || after.Stale {
			t.Fatalf("continuation lost %s: before=%+v after=%+v", stage, before, after)
		}
	}
}

var _ = errors.Is
