package application

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func pipelineBrainstormRefForCriteriaTest(t *testing.T, db *sqlite.Store, runID, requestID string) catalog.BrainstormRequest {
	t.Helper()
	brainstorm, err := db.GetBrainstorming(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := db.GetPipeline(t.Context(), brainstorm.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.BrainstormRequest{RunID: runID, RequestID: requestID, PipelineRevision: pipeline.Revision, RunRevision: brainstorm.Revision, DiscoveryVersion: brainstorm.DiscoveryVersion}
}

func approveSynthesisForCriteriaTest(t *testing.T, s *Service, db *sqlite.Store, start StartBrainstormingInput, content catalog.BrainstormSynthesisContent) BrainstormDTO {
	t.Helper()
	brainstorm, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetBrainstorming(t.Context(), brainstorm.ID)
	if err != nil {
		t.Fatal(err)
	}
	question, _, err := db.BeginBrainstormQuestion(t.Context(), catalog.BeginBrainstormAttemptRequest{BrainstormRequest: pipelineBrainstormRefForCriteriaTest(t, db, brainstorm.ID, "criteria_question_001"),
		Kind: "question", Selection: stored.Selection, EstimatedInputTokens: 512})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: pipelineBrainstormRefForCriteriaTest(t, db, brainstorm.ID, "criteria_question_done_01"), AttemptID: question.ID, SessionID: "criteria_question_session", Question: "What is the required behavior?"}); err != nil {
		t.Fatal(err)
	}
	stored, err = db.GetBrainstorming(t.Context(), brainstorm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AnswerBrainstormQuestion(t.Context(), catalog.AnswerBrainstormRequest{BrainstormRequest: pipelineBrainstormRefForCriteriaTest(t, db, brainstorm.ID, "criteria_answer_0001"), QuestionID: stored.CurrentQuestionID, Answer: "Preserve all Discovery requirements"}); err != nil {
		t.Fatal(err)
	}
	stored, err = db.GetBrainstorming(t.Context(), brainstorm.ID)
	if err != nil {
		t.Fatal(err)
	}
	selection := stored.Selection
	selection.MaxOutputTokens = 1024
	synthesis, _, err := db.QuestionsSufficient(t.Context(), catalog.GenerateBrainstormSynthesisRequest{BrainstormRequest: pipelineBrainstormRefForCriteriaTest(t, db, brainstorm.ID, "criteria_synthesis_001"),
		Selection: selection, EstimatedInputTokens: 512})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: pipelineBrainstormRefForCriteriaTest(t, db, brainstorm.ID, "criteria_synthesis_done_01"), AttemptID: synthesis.ID,
		SessionID: "criteria_synthesis_session", Synthesis: &content}); err != nil {
		t.Fatal(err)
	}
	stored, err = db.GetBrainstorming(t.Context(), brainstorm.ID)
	if err != nil {
		t.Fatal(err)
	}
	return mustApproveSynthesisForCriteriaTest(t, s, db, stored)
}

func mustApproveSynthesisForCriteriaTest(t *testing.T, s *Service, db *sqlite.Store, brainstorm catalog.BrainstormRun) BrainstormDTO {
	t.Helper()
	input := pipelineBrainstormRefForCriteriaTest(t, db, brainstorm.ID, "criteria_approve_001")
	approved, err := s.ApproveBrainstormSynthesis(ApproveBrainstormSynthesisInput{Ref: BrainstormRequestInput{RunID: input.RunID, RequestID: input.RequestID,
		PipelineRevision: input.PipelineRevision, RunRevision: input.RunRevision, DiscoveryVersion: input.DiscoveryVersion}, SynthesisVersion: brainstorm.SynthesisVersion})
	if err != nil {
		t.Fatal(err)
	}
	return approved
}

func skipSpecForCriteriaTest(t *testing.T, s *Service, pipelineID, reason string) {
	t.Helper()
	stage, err := s.GetAuthoringStage(GetAuthoringStageInput{PipelineID: pipelineID, Stage: "spec"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SkipAuthoringStage(AuthoringStageDecisionInput{Ref: AuthoringStageRefInput{PipelineID: pipelineID, Stage: "spec", RequestID: "criteria_skip_spec_01",
		PipelineRevision: stage.PipelineRevision, StageRevision: stage.Revision, DiscoveryVersion: stage.DiscoveryVersion, ArtifactVersion: stage.ArtifactVersion}, Reason: reason})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPipelineEvaluationCriteriaUsesFullDiscoveryApprovedSynthesisAndBypass(t *testing.T) {
	s, db, start, _, _ := brainstormingServiceFixture(t)
	synthesisContent := catalog.BrainstormSynthesisContent{Scope: "Build a single-page task list", Decisions: []string{"Persist every task in localStorage", "Allow completing and reopening tasks"}, OpenQuestions: []string{"Should completed tasks be hidden by default?"}}
	brainstorm := approveSynthesisForCriteriaTest(t, s, db, start, synthesisContent)
	skipSpecForCriteriaTest(t, s, start.PipelineID, "Use the full approved Discovery and synthesis as criteria")
	run, err := s.loadPipeline(start.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	criteria, err := s.pipelineEvaluationCriteria(run)
	if err != nil {
		t.Fatal(err)
	}
	encodedSynthesis, err := json.Marshal(synthesisContent)
	if err != nil {
		t.Fatal(err)
	}
	if criteria.SourceStage != sdd.Discovery || criteria.SourceVersion != 1 || criteria.SourceContent != "Immutable Discovery" ||
		criteria.SourceDigest != digestEvaluationCriteriaContent("Immutable Discovery") || criteria.DiscoveryVersion != 1 ||
		criteria.SynthesisRunID != brainstorm.ID || criteria.SynthesisVersion != 1 || criteria.SynthesisContent != string(encodedSynthesis) ||
		criteria.SynthesisDigest != digestEvaluationCriteriaContent(string(encodedSynthesis)) || criteria.Digest == "" ||
		len(criteria.Bypasses) != 1 || criteria.Bypasses[0].Stage != "spec" || criteria.Bypasses[0].Reason != "Use the full approved Discovery and synthesis as criteria" {
		t.Fatalf("evaluation criteria lost the approved sources or bypass: %+v", criteria)
	}
	text := evaluationCriteriaText(criteria)
	if text == criteria.SourceContent || !containsAll(text, []string{"Immutable Discovery", "single-page task list", "Persist every task in localStorage", "reopening tasks"}) {
		t.Fatalf("Evaluator criteria omitted full Discovery or approved synthesis: %s", text)
	}
}

func TestPipelineEvaluationCriteriaRecordsBrainstormBypassWhenNoSynthesisExists(t *testing.T) {
	s, _, start, _, _ := brainstormingServiceFixture(t)
	brainstorm, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	skipped, err := s.SkipBrainstormQuestions(SkipBrainstormQuestionsInput{Ref: BrainstormRequestInput{RunID: brainstorm.ID, RequestID: "criteria_brainstorm_skip_01",
		PipelineRevision: brainstorm.PipelineRevision, RunRevision: brainstorm.Revision, DiscoveryVersion: brainstorm.DiscoveryVersion}, Reason: "The full Discovery already contains acceptance criteria"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmDiscoveryAfterSkip(BrainstormRequestInput{RunID: skipped.ID, RequestID: "criteria_brainstorm_confirm_1", PipelineRevision: skipped.PipelineRevision, RunRevision: skipped.Revision, DiscoveryVersion: skipped.DiscoveryVersion}); err != nil {
		t.Fatal(err)
	}
	skipSpecForCriteriaTest(t, s, start.PipelineID, "Use Discovery without a detailed SPEC")
	run, err := s.loadPipeline(start.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	criteria, err := s.pipelineEvaluationCriteria(run)
	if err != nil {
		t.Fatal(err)
	}
	if criteria.SourceStage != sdd.Discovery || criteria.SourceContent != "Immutable Discovery" || criteria.SynthesisRunID != brainstorm.ID ||
		criteria.SynthesisVersion != 0 || criteria.SynthesisContent != "" || len(criteria.Bypasses) != 2 ||
		criteria.Bypasses[0].Stage != "spec" || criteria.Bypasses[0].Reason != "Use Discovery without a detailed SPEC" ||
		criteria.Bypasses[1].Stage != "brainstorming" || criteria.Bypasses[1].Reason != "The full Discovery already contains acceptance criteria" {
		t.Fatalf("evaluation criteria lost the two explicit bypasses: %+v", criteria)
	}
	if text := evaluationCriteriaText(criteria); text != "Immutable Discovery" {
		t.Fatalf("an unapproved or skipped synthesis altered acceptance criteria: %s", text)
	}
}

func containsAll(text string, values []string) bool {
	for _, value := range values {
		if !strings.Contains(text, value) {
			return false
		}
	}
	return true
}
