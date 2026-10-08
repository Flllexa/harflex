package sqlite

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/migrations"
)

func synthesisCommand(t *testing.T, store *Store, runID string) catalog.GenerateBrainstormSynthesisRequest {
	t.Helper()
	return catalog.GenerateBrainstormSynthesisRequest{BrainstormRequest: brainstormRef(t, store, runID, "sufficient_request_1"), Selection: brainstormChoice(), EstimatedInputTokens: 2000}
}

func pendingBrainstormQuestion(t *testing.T, store *Store, runID string, number int) catalog.BrainstormRun {
	t.Helper()
	a := brainstormAdmit(t, store, runID, "question", fmt.Sprintf("pending_question_%02d", number))
	run, err := store.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, runID, "unused_request_00"), AttemptID: a.ID, SessionID: fmt.Sprintf("session_%d", number), Question: "Next scope?"})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestQuestionsSufficientAtomicSkipAndReplay(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	brainstormAnsweredTurn(t, store, run.ID)
	pending := pendingBrainstormQuestion(t, store, run.ID, 2)
	in := synthesisCommand(t, store, run.ID)
	a, admitted, err := store.QuestionsSufficient(t.Context(), in)
	if err != nil || !admitted {
		t.Fatalf("admission: %v %v", admitted, err)
	}
	again, admitted, err := store.QuestionsSufficient(t.Context(), in)
	if err != nil || admitted || again.ID != a.ID {
		t.Fatalf("replay: %+v %v %v", again, admitted, err)
	}
	got, err := store.GetBrainstorming(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "running_synthesis" || got.CurrentQuestionID != "" || got.AttemptCount != 3 || got.Revision != pending.Revision+1 || got.Turns[1].Status != "skipped" || got.Turns[1].Answer != "" || got.Turns[0].Status != "answered" {
		t.Fatalf("atomic result: %+v", got)
	}
	if a.Kind != "synthesis" || a.ReservedOutputTokens != 1024 || a.Selection.MaxOutputTokens != 1024 || a.SynthesisVersion != 1 {
		t.Fatalf("snapshot: %+v", a)
	}
	in.EstimatedInputTokens++
	if _, admitted, err := store.QuestionsSufficient(t.Context(), in); !errors.Is(err, ErrPipelineConflict) || admitted {
		t.Fatalf("changed request: %v %v", admitted, err)
	}
}

func TestQuestionsSufficientConcurrentStores(t *testing.T) {
	store, path, run, _ := brainstormFixture(t)
	brainstormAnsweredTurn(t, store, run.ID)
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	in := synthesisCommand(t, store, run.ID)
	type result struct {
		attempt  catalog.BrainstormAttempt
		admitted bool
		err      error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, db := range []*Store{store, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, admitted, err := db.QuestionsSufficient(t.Context(), in)
			results <- result{a, admitted, err}
		}()
	}
	wg.Wait()
	close(results)
	count, attemptID := 0, ""
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.admitted {
			count++
		}
		if attemptID != "" && attemptID != r.attempt.ID {
			t.Fatal("multiple attempts")
		}
		attemptID = r.attempt.ID
	}
	if count != 1 {
		t.Fatalf("admitted %d", count)
	}
	in = synthesisCommand(t, store, run.ID)
	in.RequestID = "different_request_1"
	if _, admitted, err := store.QuestionsSufficient(t.Context(), in); err == nil || admitted {
		t.Fatalf("second command: %v %v", admitted, err)
	}
}

func TestQuestionsSufficientRefusalPreservesPending(t *testing.T) {
	for _, scenario := range []string{"zero_ready", "zero_waiting", "budget", "stale_run", "stale_pipeline", "stale_discovery", "wrong_action", "insert_failure"} {
		t.Run(scenario, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			if scenario != "zero_ready" && scenario != "zero_waiting" {
				brainstormAnsweredTurn(t, store, run.ID)
			}
			if scenario != "zero_ready" {
				pendingBrainstormQuestion(t, store, run.ID, 2)
			}
			if scenario == "budget" {
				if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_runs SET output_budget_remaining=0 WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "insert_failure" {
				if _, err := store.DB().Exec(`CREATE TRIGGER fail_synthesis_insert BEFORE INSERT ON pipeline_brainstorm_attempts BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := store.GetBrainstorming(t.Context(), run.ID)
			pipelineBefore, _ := store.GetPipeline(t.Context(), run.PipelineID)
			in := synthesisCommand(t, store, run.ID)
			switch scenario {
			case "stale_run":
				in.RunRevision--
			case "stale_pipeline":
				in.PipelineRevision--
			case "stale_discovery":
				in.DiscoveryVersion++
			}
			var admitted bool
			var err error
			if scenario == "wrong_action" {
				_, admitted, err = store.FinishAndGenerateSynthesis(t.Context(), in)
			} else {
				_, admitted, err = store.QuestionsSufficient(t.Context(), in)
			}
			if err == nil || admitted {
				t.Fatalf("refusal: %v %v", admitted, err)
			}
			after, _ := store.GetBrainstorming(t.Context(), run.ID)
			pipelineAfter, _ := store.GetPipeline(t.Context(), run.PipelineID)
			if after.Revision != before.Revision || after.AttemptCount != before.AttemptCount || after.State != before.State || after.CurrentQuestionID != before.CurrentQuestionID || after.InputBudgetRemaining != before.InputBudgetRemaining || after.OutputBudgetRemaining != before.OutputBudgetRemaining || pipelineAfter.Revision != pipelineBefore.Revision {
				t.Fatal("refusal changed state")
			}
			if len(after.Turns) > 0 && after.Turns[len(after.Turns)-1].Status != "waiting_answer" {
				t.Fatal("refusal skipped pending question")
			}
		})
	}
}

func TestGenericAttemptCannotAdmitSynthesis(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	brainstormAnsweredTurn(t, store, run.ID)
	in := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "generic_synthesis_1"), Kind: "synthesis", Selection: brainstormChoice(), EstimatedInputTokens: 2000}
	if _, err := store.BeginBrainstormAttempt(t.Context(), in); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("generic synthesis admitted: %v", err)
	}
}

func TestSynthesisCommandReplayAfterCompletionAndRestart(t *testing.T) {
	store, path, run, _ := brainstormFixture(t)
	brainstormAnsweredTurn(t, store, run.ID)
	in := synthesisCommand(t, store, run.ID)
	a, _, err := store.QuestionsSufficient(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: a.ID, SessionID: "synthesis_session", Synthesis: &catalog.BrainstormSynthesisContent{Scope: "Bounded", Decisions: []string{"Human confirmed"}, OpenQuestions: []string{}}}); err != nil {
		t.Fatal(err)
	}
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	again, admitted, err := other.QuestionsSufficient(t.Context(), in)
	if err != nil || admitted || again.ID != a.ID || again.Status != "completed" {
		t.Fatalf("restart replay: %+v %v %v", again, admitted, err)
	}
}

func TestSynthesisCommandMigrationPreservesRequestLedger(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	brainstormAnsweredTurn(t, store, run.ID)
	// Reapply the table rebuild against populated legacy-compatible rows.
	for _, column := range []string{"result_run_revision", "result_pipeline_revision", "actor", "result_snapshot"} {
		if _, err := store.DB().Exec(`ALTER TABLE pipeline_brainstorm_requests DROP COLUMN ` + column); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().Exec(`ALTER TABLE pipeline_brainstorm_requests DROP COLUMN client_intent_hash`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := store.DB().QueryRow(`SELECT group_concat(run_id || request_id || kind || payload_hash || result_id || created_at) FROM pipeline_brainstorm_requests`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	body, err := migrations.FS.ReadFile("027_brainstorm_synthesis_commands.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(string(body)); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := store.DB().QueryRow(`SELECT group_concat(run_id || request_id || kind || payload_hash || result_id || created_at) FROM pipeline_brainstorm_requests`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("request ledger changed during migration")
	}
	if _, err := store.DB().Exec(`ALTER TABLE pipeline_brainstorm_requests ADD COLUMN client_intent_hash TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	// Rebuild 027 intentionally drops all later columns; restore 030's additive
	// receipt schema before exercising the current command implementation.
	for _, column := range []string{"result_run_revision INTEGER NOT NULL DEFAULT 0", "result_pipeline_revision INTEGER NOT NULL DEFAULT 0", "actor TEXT NOT NULL DEFAULT ''", "result_snapshot BLOB"} {
		if _, err := store.DB().Exec(`ALTER TABLE pipeline_brainstorm_requests ADD COLUMN ` + column); err != nil {
			t.Fatal(err)
		}
	}
	if _, admitted, err := store.QuestionsSufficient(t.Context(), synthesisCommand(t, store, run.ID)); err != nil || !admitted {
		t.Fatalf("migrated command: %v %v", admitted, err)
	}
}

func TestFinishAndGenerateSynthesisExplicitGate(t *testing.T) {
	for _, scenario := range []string{"five_answers", "new_synthesis", "ready", "zero_answers"} {
		t.Run(scenario, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			if scenario != "zero_answers" {
				brainstormAnsweredTurn(t, store, run.ID)
			}
			if scenario == "five_answers" {
				for number := 2; number <= 5; number++ {
					pending := pendingBrainstormQuestion(t, store, run.ID, number)
					if _, err := store.AnswerBrainstormQuestion(t.Context(), catalog.AnswerBrainstormRequest{BrainstormRequest: brainstormRef(t, store, run.ID, fmt.Sprintf("answer_request_%02d", number)), QuestionID: pending.CurrentQuestionID, Answer: "Exact answer"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Task 4 will create this state through its separate human rejection gate.
			if scenario == "new_synthesis" || scenario == "zero_answers" {
				if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_runs SET state='ready_for_synthesis',synthesis_version=1 WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			in := synthesisCommand(t, store, run.ID)
			a, admitted, err := store.FinishAndGenerateSynthesis(t.Context(), in)
			if scenario == "ready" || scenario == "zero_answers" {
				if !errors.Is(err, sdd.ErrInvalidTransition) || admitted {
					t.Fatalf("invalid gate: %v %v", admitted, err)
				}
				return
			}
			if err != nil || !admitted {
				t.Fatalf("admission: %v %v", admitted, err)
			}
			if scenario == "new_synthesis" && a.SynthesisVersion != 2 {
				t.Fatalf("version: %d", a.SynthesisVersion)
			}
			if _, admitted, err := store.FinishAndGenerateSynthesis(t.Context(), in); err != nil || admitted {
				t.Fatalf("replay: %v %v", admitted, err)
			}
			if _, admitted, err := store.QuestionsSufficient(t.Context(), in); !errors.Is(err, ErrPipelineConflict) || admitted {
				t.Fatalf("action mismatch: %v %v", admitted, err)
			}
		})
	}
}
