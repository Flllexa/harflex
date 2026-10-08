package sqlite

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func completedSynthesis(t *testing.T, store *Store, runID string) {
	t.Helper()
	brainstormAnsweredTurn(t, store, runID)
	a := brainstormAdmit(t, store, runID, "synthesis", "synthesis_gate_0001")
	_, err := store.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, runID, "unused_request_00"), AttemptID: a.ID, SessionID: "synthesis_session", Synthesis: &catalog.BrainstormSynthesisContent{Scope: "Scope", Decisions: []string{"Decision"}, OpenQuestions: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBrainstormHumanGateRefusalsRollback(t *testing.T) {
	for _, scenario := range []string{"stale_run", "stale_pipeline", "stale_discovery", "stale_synthesis", "active_stage", "receipt_failure", "event_failure"} {
		t.Run(scenario, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			completedSynthesis(t, store, run.ID)
			in := catalog.ApproveBrainstormSynthesisRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "approve_failure_01"), SynthesisVersion: 1}
			switch scenario {
			case "stale_run":
				in.RunRevision--
			case "stale_pipeline":
				in.PipelineRevision--
			case "stale_discovery":
				in.DiscoveryVersion++
			case "stale_synthesis":
				in.SynthesisVersion++
			case "active_stage":
				_, err := store.DB().Exec(`UPDATE pipeline_runs SET stage_status=json_set(stage_status,'$.discovery','active') WHERE id=?`, run.PipelineID)
				if err != nil {
					t.Fatal(err)
				}
			case "receipt_failure":
				_, err := store.DB().Exec(`CREATE TRIGGER fail_human_receipt BEFORE INSERT ON pipeline_brainstorm_human_receipts BEGIN SELECT RAISE(ABORT,'injected'); END`)
				if err != nil {
					t.Fatal(err)
				}
			case "event_failure":
				_, err := store.DB().Exec(`CREATE TRIGGER fail_gate_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'injected'); END`)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := store.GetBrainstorming(t.Context(), run.ID)
			pBefore, _ := store.GetPipeline(t.Context(), run.PipelineID)
			if _, err := store.ApproveBrainstormSynthesis(t.Context(), in); err == nil {
				t.Fatal("invalid approval accepted")
			}
			after, _ := store.GetBrainstorming(t.Context(), run.ID)
			pAfter, _ := store.GetPipeline(t.Context(), run.PipelineID)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(pBefore, pAfter) {
				t.Fatal("failed action changed aggregate")
			}
		})
	}
}

func TestBrainstormHumanGateConcurrentReplay(t *testing.T) {
	store, path, run, _ := brainstormFixture(t)
	completedSynthesis(t, store, run.ID)
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	in := catalog.ApproveBrainstormSynthesisRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "approve_parallel_1"), SynthesisVersion: 1}
	var wg sync.WaitGroup
	results := make(chan catalog.BrainstormRun, 2)
	errs := make(chan error, 2)
	for _, db := range []*Store{store, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := db.ApproveBrainstormSynthesis(t.Context(), in)
			results <- got
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *catalog.BrainstormRun
	for got := range results {
		if first == nil {
			first = &got
		} else if !reflect.DeepEqual(*first, got) {
			t.Fatal("different replay results")
		}
	}
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM pipeline_brainstorm_human_receipts WHERE run_id=?`, run.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipt count %d %v", count, err)
	}
}

func TestBrainstormSkipRejectsAnswersAndRunningAttempt(t *testing.T) {
	for _, state := range []string{"answered", "running"} {
		t.Run(state, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			if state == "answered" {
				brainstormAnsweredTurn(t, store, run.ID)
			} else {
				brainstormAdmit(t, store, run.ID, "question", "running_question_1")
			}
			in := catalog.SkipBrainstormQuestionsRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "skip_invalid_0001"), Reason: "Reason"}
			if _, err := store.SkipBrainstormQuestions(t.Context(), in); err == nil {
				t.Fatal("unsafe skip accepted")
			}
		})
	}
}

func TestBrainstormRevisionFiveQuestionsLimit(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	completedSynthesis(t, store, run.ID)
	if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_runs SET question_count=5 WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	in := catalog.RequestBrainstormRevisionRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "revision_limit_001"), SynthesisVersion: 1, Choice: "more_questions", Feedback: "Scope"}
	if _, err := store.RequestBrainstormRevision(t.Context(), in); err == nil {
		t.Fatal("sixth question allowed")
	}
	in.Choice = "new_synthesis"
	if _, err := store.RequestBrainstormRevision(t.Context(), in); err != nil {
		t.Fatal(err)
	}
}

func TestBrainstormAnswerAndEnoughReceiptsKeepResultRevisions(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	pending := pendingBrainstormQuestion(t, store, run.ID, 1)
	answer := catalog.AnswerBrainstormRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "answer_receipt_001"), QuestionID: pending.CurrentQuestionID, Answer: "Confirmed"}
	answered, err := store.AnswerBrainstormQuestion(t.Context(), answer)
	if err != nil {
		t.Fatal(err)
	}
	command := synthesisCommand(t, store, run.ID)
	attempt, _, err := store.QuestionsSufficient(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.GetBrainstormCommand(t.Context(), run.ID, command.RequestID)
	if err != nil || receipt.ResultRunRevision != command.RunRevision+1 || receipt.ResultPipelineRevision != command.PipelineRevision+1 {
		t.Fatalf("enough revision receipt: %+v %v", receipt, err)
	}
	_, err = store.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "synthesis_session", Synthesis: &catalog.BrainstormSynthesisContent{Scope: "Scope", Decisions: []string{"Decision"}}})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.AnswerBrainstormQuestion(t.Context(), answer)
	if err != nil || !reflect.DeepEqual(answered, replay) {
		t.Fatal("answer replay lost exact result")
	}
	again, err := store.GetBrainstormCommand(t.Context(), run.ID, command.RequestID)
	if err != nil || !reflect.DeepEqual(receipt, again) {
		t.Fatal("enough receipt changed")
	}
	answerReceipt, err := store.GetBrainstormCommand(t.Context(), run.ID, answer.RequestID)
	if err != nil || answerReceipt.ResultRunRevision != answer.RunRevision+1 || answerReceipt.ResultPipelineRevision != answer.PipelineRevision+1 {
		t.Fatal("answer receipt missing revisions")
	}
}

func TestBrainstormLegacyReceiptUnavailableIsReadOnly(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	pending := pendingBrainstormQuestion(t, store, run.ID, 1)
	answer := catalog.AnswerBrainstormRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "legacy_answer_001"), QuestionID: pending.CurrentQuestionID, Answer: "Confirmed"}
	if _, err := store.AnswerBrainstormQuestion(t.Context(), answer); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`DROP TRIGGER brainstorm_immutable_request; UPDATE pipeline_brainstorm_requests SET result_snapshot=NULL,result_run_revision=0,result_pipeline_revision=0 WHERE kind='answer'`); err != nil {
		t.Fatal(err)
	}
	before, _ := store.GetBrainstorming(t.Context(), run.ID)
	if _, err := store.AnswerBrainstormQuestion(t.Context(), answer); !errors.Is(err, ErrHistoricalReceiptUnavailable) {
		t.Fatalf("legacy replay: %v", err)
	}
	if _, err := store.GetBrainstormCommand(t.Context(), run.ID, answer.RequestID); !errors.Is(err, ErrHistoricalReceiptUnavailable) {
		t.Fatalf("legacy receipt: %v", err)
	}
	after, _ := store.GetBrainstorming(t.Context(), run.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("legacy replay mutated state")
	}
}

func TestApproveBrainstormHumanGate(t *testing.T) {
	store, path, run, _ := brainstormFixture(t)
	completedSynthesis(t, store, run.ID)
	restarted, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	stillWaiting, err := restarted.GetBrainstorming(t.Context(), run.ID)
	_ = restarted.Close()
	if err != nil || stillWaiting.State != "waiting_user" || stillWaiting.Syntheses[0].Status != "completed" {
		t.Fatal("restart approved model output")
	}
	before, _ := store.GetPipeline(t.Context(), run.PipelineID)
	if before.Current != sdd.Discovery || before.DiscoveryFrozenVersion != 0 {
		t.Fatal("model crossed human gate")
	}
	in := catalog.ApproveBrainstormSynthesisRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "approve_request_01"), SynthesisVersion: 1}
	got, err := store.ApproveBrainstormSynthesis(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := store.GetPipeline(t.Context(), run.PipelineID)
	if got.State != "approved" || p.Current != sdd.Spec || p.Status[sdd.Discovery] != sdd.Completed || p.Status[sdd.Spec] != sdd.Active || p.DiscoveryFrozenVersion != 1 {
		t.Fatalf("gate: %+v %+v", got, p)
	}
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	replay, err := other.ApproveBrainstormSynthesis(t.Context(), in)
	if err != nil || !reflect.DeepEqual(replay, got) {
		t.Fatalf("restart replay: %+v %v", replay, err)
	}
	in.SynthesisVersion++
	if _, err := store.ApproveBrainstormSynthesis(t.Context(), in); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("changed replay: %v", err)
	}
}

func TestBrainstormSkipRequiresSeparateConfirmation(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	pendingBrainstormQuestion(t, store, run.ID, 1)
	skip := catalog.SkipBrainstormQuestionsRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "bypass_request_001"), Reason: "Enough initial context"}
	got, err := store.SkipBrainstormQuestions(t.Context(), skip)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := store.GetPipeline(t.Context(), run.PipelineID)
	if got.State != "skipped_waiting_confirmation" || len(got.Syntheses) != 0 || got.Turns[0].Status != "skipped" || p.Current != sdd.Discovery || p.Status[sdd.Discovery] != sdd.WaitingUser || p.DiscoveryFrozenVersion != 0 {
		t.Fatal("skip crossed gate or fabricated synthesis")
	}
	_, err = store.ApproveBrainstormSynthesis(t.Context(), catalog.ApproveBrainstormSynthesisRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "approve_skipped_01"), SynthesisVersion: 1})
	if err == nil {
		t.Fatal("approved fake synthesis")
	}
	confirm := brainstormRef(t, store, run.ID, "confirm_request_01")
	approved, err := store.ConfirmDiscoveryAfterSkip(t.Context(), confirm)
	if err != nil {
		t.Fatal(err)
	}
	if approved.State != "approved" || len(approved.Syntheses) != 0 {
		t.Fatal("confirmation failed")
	}
	confirmedReplay, err := store.ConfirmDiscoveryAfterSkip(t.Context(), confirm)
	if err != nil || !reflect.DeepEqual(approved, confirmedReplay) {
		t.Fatal("confirm replay changed")
	}
	changed := confirm
	changed.RunRevision++
	if _, err := store.ConfirmDiscoveryAfterSkip(t.Context(), changed); !errors.Is(err, ErrPipelineConflict) {
		t.Fatal("confirm changed payload accepted")
	}
	replay, err := store.SkipBrainstormQuestions(t.Context(), skip)
	if err != nil || !reflect.DeepEqual(got, replay) {
		t.Fatal("skip replay lost original result")
	}
}

func TestBrainstormRevisionRejectsInspectedVersion(t *testing.T) {
	for _, choice := range []string{"more_questions", "new_synthesis"} {
		t.Run(choice, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			completedSynthesis(t, store, run.ID)
			in := catalog.RequestBrainstormRevisionRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "revision_request_1"), SynthesisVersion: 1, Choice: choice, Feedback: "Clarify scope"}
			got, err := store.RequestBrainstormRevision(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			want := "ready"
			if choice == "new_synthesis" {
				want = "ready_for_synthesis"
			}
			if got.State != want || got.Syntheses[0].Status != "rejected" {
				t.Fatalf("revision: %+v", got)
			}
			p, _ := store.GetPipeline(t.Context(), run.PipelineID)
			if p.Status[sdd.Discovery] != sdd.Active {
				t.Fatal("Discovery not active")
			}
			if _, err := store.ApproveBrainstormSynthesis(t.Context(), catalog.ApproveBrainstormSynthesisRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "approve_rejected_1"), SynthesisVersion: 1}); err == nil {
				t.Fatal("approved rejected synthesis")
			}
			replay, err := store.RequestBrainstormRevision(t.Context(), in)
			if err != nil || !reflect.DeepEqual(got, replay) {
				t.Fatal("revision replay")
			}
		})
	}
}
