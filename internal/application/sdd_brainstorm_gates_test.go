package application

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestBrainstormHumanRevisionAndApprovalOffline(t *testing.T) {
	s, db, start, calls, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(requestID string) catalog.BrainstormRequest {
		t.Helper()
		r, e := db.GetBrainstorming(t.Context(), run.ID)
		if e != nil {
			t.Fatal(e)
		}
		return catalog.BrainstormRequest{RunID: r.ID, RequestID: requestID, PipelineRevision: r.PipelineRevision, RunRevision: r.Revision, DiscoveryVersion: r.DiscoveryVersion}
	}
	stored, _ := db.GetBrainstorming(t.Context(), run.ID)
	question, _, err := db.BeginBrainstormQuestion(t.Context(), catalog.BeginBrainstormAttemptRequest{BrainstormRequest: ref("question_gate_0001"), Kind: "question", Selection: stored.Selection, EstimatedInputTokens: 2000})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := db.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: ref("unused_request_00"), AttemptID: question.ID, SessionID: "question_session", Question: "Scope?"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.AnswerBrainstormQuestion(t.Context(), catalog.AnswerBrainstormRequest{BrainstormRequest: ref("answer_gate_00001"), QuestionID: pending.CurrentQuestionID, Answer: "Small"})
	if err != nil {
		t.Fatal(err)
	}
	choice := stored.Selection
	choice.MaxOutputTokens = 1024
	synth, _, err := db.QuestionsSufficient(t.Context(), catalog.GenerateBrainstormSynthesisRequest{BrainstormRequest: ref("synthesis_gate_001"), Selection: choice, EstimatedInputTokens: 2000})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: ref("unused_request_00"), AttemptID: synth.ID, SessionID: "synthesis_session", Synthesis: &catalog.BrainstormSynthesisContent{Scope: "Before", Decisions: []string{"Decision"}}})
	if err != nil {
		t.Fatal(err)
	}
	inputRef := func(requestID string) BrainstormRequestInput {
		r := ref(requestID)
		return BrainstormRequestInput{RunID: r.RunID, RequestID: r.RequestID, PipelineRevision: r.PipelineRevision, RunRevision: r.RunRevision, DiscoveryVersion: r.DiscoveryVersion}
	}
	count := calls.Load()
	s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	revision := RequestBrainstormRevisionInput{Ref: inputRef("revision_gate_001"), SynthesisVersion: 1, Choice: "new_synthesis", Feedback: "Human correction"}
	rejected, err := s.RequestBrainstormRevision(revision)
	if err != nil {
		t.Fatal(err)
	}
	live, _ := db.GetBrainstorming(t.Context(), run.ID)
	prompt, err := prepareSDDBrainstormPrompt(live, "synthesis", choice, "openai")
	if err != nil || !strings.Contains(prompt.Text, "Human correction") {
		t.Fatal("durable feedback missing", err)
	}
	fresh, _, err := db.FinishAndGenerateSynthesis(t.Context(), catalog.GenerateBrainstormSynthesisRequest{BrainstormRequest: ref("synthesis_gate_002"), Selection: choice, EstimatedInputTokens: prompt.EstimatedInputTokens})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: ref("unused_request_00"), AttemptID: fresh.ID, SessionID: "revised_session", Synthesis: &catalog.BrainstormSynthesisContent{Scope: "After", Decisions: []string{"Correction"}}})
	if err != nil {
		t.Fatal(err)
	}
	approval := ApproveBrainstormSynthesisInput{Ref: inputRef("approve_gate_0001"), SynthesisVersion: 2}
	approved, err := s.ApproveBrainstormSynthesis(approval)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Syntheses[0].Status != "rejected" || approved.Syntheses[1].Status != "approved" || approved.HumanActions[1].Actor != "local_user" {
		t.Fatal("history lost")
	}
	replay, err := s.RequestBrainstormRevision(revision)
	if err != nil || !reflect.DeepEqual(rejected, replay) {
		t.Fatal("historical revision replay changed")
	}
	if calls.Load() != count {
		t.Fatal("human action used network")
	}
}

func TestBrainstormHumanSkipAndConfirmOffline(t *testing.T) {
	s, _, start, calls, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	count := calls.Load()
	s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	in := SkipBrainstormQuestionsInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "skip_human_000001", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: run.DiscoveryVersion}, Reason: "Use Discovery"}
	skipped, err := s.SkipBrainstormQuestions(in)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := s.ConfirmDiscoveryAfterSkip(BrainstormRequestInput{RunID: run.ID, RequestID: "confirm_human_001", PipelineRevision: skipped.PipelineRevision, RunRevision: skipped.Revision, DiscoveryVersion: skipped.DiscoveryVersion})
	if err != nil {
		t.Fatal(err)
	}
	if approved.State != "approved" || len(approved.Syntheses) != 0 {
		t.Fatal("invalid approval")
	}
	replay, err := s.SkipBrainstormQuestions(in)
	if err != nil || !reflect.DeepEqual(skipped, replay) {
		t.Fatal("service lost historical receipt")
	}
	if calls.Load() != count {
		t.Fatal("human action called network")
	}
}
