package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func brainstormChoice() catalog.ModelSelection {
	return catalog.ModelSelection{BackendID: "profile", ModelID: "model", Source: "openai_models", Destination: "https://provider.example/v1", CatalogRevision: "revision", CredentialIdentity: strings.Repeat("a", 64), CheckedAt: time.Now().UTC(), Status: "listed", MaxOutputTokens: 1024, ContextLength: 8192}
}

func TestValidBrainstormSelectionAcceptsCodexCLIWithoutCredential(t *testing.T) {
	selection := catalog.ModelSelection{
		BackendID: "codex", ModelID: "provider/model-exact", Source: "codex_app_server", CatalogRevision: "catalog-revision",
		LocalRevision: "local-revision", ExecutablePath: "/opt/homebrew/bin/codex", ExecutableVersion: "0.157.0",
		WorkspacePath: "/workspace", Status: "listed", MaxOutputTokens: 256, MaxAssistantOutputBytes: 2 * 1024, ContextLength: 8192, CheckedAt: time.Now().UTC(),
	}
	if !validBrainstormSelection(selection) {
		t.Fatal("complete Codex CLI selection without API credential was rejected")
	}
	selection.CredentialIdentity = strings.Repeat("a", 64)
	if validBrainstormSelection(selection) {
		t.Fatal("Codex CLI selection must not carry an API credential identity")
	}
}

func brainstormFixture(t *testing.T) (*Store, string, catalog.BrainstormRun, catalog.StartBrainstormingRequest) {
	t.Helper()
	store, path, pipeline := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), pipeline, "Immutable initial Discovery"); err != nil {
		t.Fatal(err)
	}
	in := catalog.StartBrainstormingRequest{PipelineID: pipeline.ID, RequestID: "start_request_0001", PipelineRevision: 1, DiscoveryVersion: 1, Selection: brainstormChoice()}
	run, err := store.StartBrainstorming(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return store, path, run, in
}

func brainstormRef(t *testing.T, store *Store, runID, requestID string) catalog.BrainstormRequest {
	t.Helper()
	run, err := store.GetBrainstorming(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := store.GetPipeline(t.Context(), run.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.BrainstormRequest{RunID: runID, RequestID: requestID, PipelineRevision: pipeline.Revision, RunRevision: run.Revision, DiscoveryVersion: run.DiscoveryVersion}
}

func brainstormAdmit(t *testing.T, store *Store, runID, kind, requestID string) catalog.BrainstormAttempt {
	t.Helper()
	in := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, runID, requestID), Kind: kind, Selection: brainstormChoice(), EstimatedInputTokens: 1000}
	if kind == "synthesis" {
		command := catalog.GenerateBrainstormSynthesisRequest{BrainstormRequest: in.BrainstormRequest, Selection: in.Selection, EstimatedInputTokens: in.EstimatedInputTokens}
		run, err := store.GetBrainstorming(t.Context(), runID)
		if err != nil {
			t.Fatal(err)
		}
		var attempt catalog.BrainstormAttempt
		if run.State == "ready_for_synthesis" {
			attempt, _, err = store.FinishAndGenerateSynthesis(t.Context(), command)
		} else {
			attempt, _, err = store.QuestionsSufficient(t.Context(), command)
		}
		if err != nil {
			t.Fatal(err)
		}
		return attempt
	}
	attempt, err := store.BeginBrainstormAttempt(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func brainstormAnsweredTurn(t *testing.T, store *Store, runID string) {
	t.Helper()
	attempt := brainstormAdmit(t, store, runID, "question", "seed_question_001")
	run, err := store.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, runID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "seed_session", Question: "Scope?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AnswerBrainstormQuestion(t.Context(), catalog.AnswerBrainstormRequest{BrainstormRequest: brainstormRef(t, store, runID, "seed_answer_0001"), QuestionID: run.CurrentQuestionID, Answer: "Small scope"}); err != nil {
		t.Fatal(err)
	}
}

func TestBrainstormStartExactVersionImmutableAndIdempotent(t *testing.T) {
	store, _, run, in := brainstormFixture(t)
	if run.State != "ready" || run.Revision != 1 || run.DiscoveryVersion != 1 || run.DiscoveryContent != "Immutable initial Discovery" || run.Selection.ModelID != "model" || run.Selection.ContextLength != 8192 {
		t.Fatalf("start: %+v", run)
	}
	again, err := store.StartBrainstorming(t.Context(), in)
	if err != nil || again.ID != run.ID {
		t.Fatalf("duplicate: %+v %v", again, err)
	}
	in.Selection.ModelID = "different"
	if _, err := store.StartBrainstorming(t.Context(), in); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("changed payload: %v", err)
	}
	in.RequestID = "different_start_01"
	if _, err := store.StartBrainstorming(t.Context(), in); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("second run: %v", err)
	}
	got, err := store.GetPipeline(t.Context(), run.PipelineID)
	if err != nil || got.Revision != 2 || got.DiscoveryFrozenVersion != 0 {
		t.Fatalf("pipeline %+v %v", got, err)
	}
}

func TestBrainstormQuestionAdmissionIdempotencyAndConcurrency(t *testing.T) {
	store, path, run, _ := brainstormFixture(t)
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	in := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "question_request_1"), Kind: "question", Selection: brainstormChoice(), EstimatedInputTokens: 1000}
	var wg sync.WaitGroup
	results := make(chan catalog.BrainstormAttempt, 2)
	errs := make(chan error, 2)
	admissions := make(chan bool, 2)
	for _, db := range []*Store{store, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attempt, admitted, err := db.BeginBrainstormQuestion(t.Context(), in)
			results <- attempt
			errs <- err
			admissions <- admitted
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	close(admissions)
	admittedCount := 0
	for admitted := range admissions {
		if admitted {
			admittedCount++
		}
	}
	if admittedCount != 1 {
		t.Fatalf("provider-admissible question attempts: %d", admittedCount)
	}
	if _, admitted, err := store.BeginBrainstormQuestion(t.Context(), in); err != nil || admitted {
		t.Fatalf("question replay: %v %v", admitted, err)
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first string
	for attempt := range results {
		if first != "" && first != attempt.ID {
			t.Fatal("duplicate attempts")
		}
		first = attempt.ID
		if attempt.Selection.ModelID != "model" || attempt.Selection.MaxOutputTokens != 256 || attempt.ReservedOutputTokens != 256 || attempt.Selection.ContextLength != 8192 {
			t.Fatalf("snapshot %+v", attempt)
		}
	}
	in.EstimatedInputTokens++
	if _, err := store.BeginBrainstormAttempt(t.Context(), in); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("changed payload: %v", err)
	}
	in.BrainstormRequest = brainstormRef(t, store, run.ID, "question_request_2")
	if _, err := store.BeginBrainstormAttempt(t.Context(), in); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("parallel admission: %v", err)
	}
	got, _ := store.GetBrainstorming(t.Context(), run.ID)
	if got.State != "running_question" || got.AttemptCount != 1 || got.OutputBudgetRemaining != 4608-256 || got.InputBudgetRemaining != 262144-1000 {
		t.Fatalf("reservation %+v", got)
	}
}

func TestBrainstormFiveQuestionsSynthesisAndExactAnswer(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	for number := 1; number <= 5; number++ {
		attempt := brainstormAdmit(t, store, run.ID, "question", fmt.Sprintf("question_request_%d", number))
		ref := brainstormRef(t, store, run.ID, "unused_request_00")
		in := catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: attempt.ID, SessionID: fmt.Sprintf("session_%d", number), Question: "Qual é o escopo?"}
		if _, err := store.CompleteBrainstormAttempt(t.Context(), in); err != nil {
			t.Fatal(err)
		}
		run, _ = store.GetBrainstorming(t.Context(), run.ID)
		if run.State != "waiting_answer" || run.QuestionCount != number {
			t.Fatalf("question %d %+v", number, run)
		}
		answer := catalog.AnswerBrainstormRequest{BrainstormRequest: brainstormRef(t, store, run.ID, fmt.Sprintf("answer_request_%02d", number)), QuestionID: run.CurrentQuestionID, Answer: "Escopo limitado"}
		stale := answer
		stale.RunRevision--
		if _, err := store.AnswerBrainstormQuestion(t.Context(), stale); !errors.Is(err, ErrPipelineConflict) {
			t.Fatalf("stale answer: %v", err)
		}
		wrong := answer
		wrong.QuestionID = "wrong"
		if _, err := store.AnswerBrainstormQuestion(t.Context(), wrong); !errors.Is(err, ErrPipelineConflict) {
			t.Fatalf("wrong question: %v", err)
		}
		if _, err := store.AnswerBrainstormQuestion(t.Context(), answer); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AnswerBrainstormQuestion(t.Context(), answer); err != nil {
			t.Fatalf("repeated answer: %v", err)
		}
		answer.Answer = "changed"
		if _, err := store.AnswerBrainstormQuestion(t.Context(), answer); !errors.Is(err, ErrPipelineConflict) {
			t.Fatalf("changed answer: %v", err)
		}
	}
	run, _ = store.GetBrainstorming(t.Context(), run.ID)
	if run.State != "ready_for_synthesis" || len(run.Turns) != 5 {
		t.Fatalf("fifth question %+v", run)
	}
	in := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "sixth_question_01"), Kind: "question", Selection: brainstormChoice(), EstimatedInputTokens: 1000}
	if _, err := store.BeginBrainstormAttempt(t.Context(), in); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("sixth: %v", err)
	}
	attempt := brainstormAdmit(t, store, run.ID, "synthesis", "synthesis_request1")
	if attempt.ReservedOutputTokens != 1024 || attempt.DiscoveryVersion != 1 || attempt.SynthesisVersion != 1 {
		t.Fatalf("synthesis cap %+v", attempt)
	}
	complete := catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "synthesis_session", Synthesis: &catalog.BrainstormSynthesisContent{Scope: "Small scope", Decisions: []string{"Explicit decisions"}, OpenQuestions: []string{"Remaining question"}}}
	if _, err := store.CompleteBrainstormAttempt(t.Context(), complete); err != nil {
		t.Fatal(err)
	}
	run, _ = store.GetBrainstorming(t.Context(), run.ID)
	pipeline, _ := store.GetPipeline(t.Context(), run.PipelineID)
	if run.State != "waiting_user" || run.SynthesisVersion != 1 || len(run.Syntheses) != 1 || run.Syntheses[0].DiscoveryVersion != 1 || pipeline.Current != sdd.Discovery || pipeline.Status[sdd.Discovery] != sdd.WaitingUser {
		t.Fatalf("synthesis: %+v %+v", run, pipeline)
	}
}

func TestBrainstormInvalidationAndRestartPreserveHistory(t *testing.T) {
	store, path, run, _ := brainstormFixture(t)
	attempt := brainstormAdmit(t, store, run.ID, "question", "question_request1")
	old := brainstormRef(t, store, run.ID, "unused_request_00")
	if err := store.ReviseAuthoringDiscovery(t.Context(), run.PipelineID, old.PipelineRevision, 1, "New Discovery", "new", "objective"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetBrainstorming(t.Context(), run.ID)
	if got.State != "invalidated" || got.DiscoveryContent != run.DiscoveryContent || got.Attempts[0].Status != "stale" {
		t.Fatalf("invalidate %+v", got)
	}
	if _, err := store.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: old, AttemptID: attempt.ID, SessionID: "late", Question: "Too late?"}); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("stale publication %v", err)
	}
	pipeline, _ := store.GetPipeline(t.Context(), run.PipelineID)
	newRun, err := store.StartBrainstorming(t.Context(), catalog.StartBrainstormingRequest{PipelineID: run.PipelineID, RequestID: "new_start_request", PipelineRevision: pipeline.Revision, DiscoveryVersion: 2, Selection: brainstormChoice()})
	if err != nil {
		t.Fatal(err)
	}
	brainstormAdmit(t, store, newRun.ID, "question", "new_question_req1")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.InterruptRunningBrainstormAttempts(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, _ = reopened.GetBrainstorming(t.Context(), newRun.ID)
	if got.State != "paused" || got.Attempts[0].Status != "interrupted" || got.DiscoveryContent != "New Discovery" {
		t.Fatalf("restart %+v", got)
	}
	before := got.Revision
	if err := reopened.InterruptRunningBrainstormAttempts(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, _ = reopened.GetBrainstorming(t.Context(), newRun.ID)
	if got.Revision != before {
		t.Fatal("non-idempotent recovery")
	}
}

func TestBrainstormValidationAndRollback(t *testing.T) {
	for _, question := range []string{"two? questions?", "line\nbreak?", strings.Repeat("界", 280) + "?", "missing mark", ""} {
		t.Run(fmt.Sprintf("question_%d", len(question)), func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			attempt := brainstormAdmit(t, store, run.ID, "question", "question_request1")
			in := catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "session", Question: question}
			if _, err := store.CompleteBrainstormAttempt(t.Context(), in); !errors.Is(err, sdd.ErrInvalidTransition) {
				t.Fatalf("invalid output %v", err)
			}
			got, _ := store.GetBrainstorming(t.Context(), run.ID)
			if got.QuestionCount != 0 || got.State != "running_question" {
				t.Fatalf("invalid output mutated %+v", got)
			}
		})
	}
	store, _, run, _ := brainstormFixture(t)
	if _, err := store.DB().Exec(`CREATE TRIGGER reject_brainstorm_event BEFORE INSERT ON events WHEN NEW.type = 'pipeline.brainstorm.attempt_started' BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	in := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "question_request1"), Kind: "question", Selection: brainstormChoice(), EstimatedInputTokens: 1000}
	if _, err := store.BeginBrainstormAttempt(t.Context(), in); err == nil {
		t.Fatal("late event failure accepted")
	}
	got, _ := store.GetBrainstorming(t.Context(), run.ID)
	if got.Revision != 1 || got.AttemptCount != 0 || got.OutputBudgetRemaining != 4608 {
		t.Fatalf("partial transaction %+v", got)
	}
}

func TestBrainstormAttemptSnapshotCannotBeRewritten(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	attempt := brainstormAdmit(t, store, run.ID, "question", "question_request1")
	if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_attempts SET selection_snapshot='{}' WHERE id=?`, attempt.ID); err == nil {
		t.Fatal("immutable attempt snapshot was rewritten")
	}
}

func TestBrainstormAdmissionGuardsAndBudgets(t *testing.T) {
	for _, test := range []struct{ name, sql string }{
		{"attempts", `UPDATE pipeline_brainstorm_runs SET attempt_count=8`},
		{"input", `UPDATE pipeline_brainstorm_runs SET input_budget_remaining=999`},
		{"output", `UPDATE pipeline_brainstorm_runs SET output_budget_remaining=255`},
		{"duration", `UPDATE pipeline_brainstorm_runs SET active_duration=511000000000`},
		{"frozen", `UPDATE pipeline_runs SET discovery_frozen_version=1`},
		{"legacy", `UPDATE pipeline_runs SET kind='legacy'`},
		{"not Discovery", `UPDATE pipeline_runs SET current_stage='spec'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			if _, err := store.DB().Exec(test.sql); err != nil {
				t.Fatal(err)
			}
			in := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "question_request1"), Kind: "question", Selection: brainstormChoice(), EstimatedInputTokens: 1000}
			if _, err := store.BeginBrainstormAttempt(t.Context(), in); err == nil {
				t.Fatal("guard admitted generation")
			}
			got, err := store.GetBrainstorming(t.Context(), run.ID)
			if err != nil || len(got.Attempts) != 0 {
				t.Fatalf("guard persisted attempt %+v %v", got, err)
			}
		})
	}
}

func TestBrainstormFailedAttemptSanitizesErrorsAndDoesNotRefund(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	attempt := brainstormAdmit(t, store, run.ID, "question", "question_request1")
	in := catalog.FailBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, ErrorCode: "raw provider secret"}
	if _, err := store.FailBrainstormAttempt(t.Context(), in); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("raw provider error %v", err)
	}
	in.ErrorCode = "provider_failed"
	if _, err := store.FailBrainstormAttempt(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailBrainstormAttempt(t.Context(), in); err != nil {
		t.Fatalf("duplicate failure %v", err)
	}
	got, err := store.GetBrainstorming(t.Context(), run.ID)
	if err != nil || got.State != "paused" || got.Attempts[0].ErrorCode != "provider_failed" || got.Attempts[0].Usage != nil || got.OutputBudgetRemaining != 4608-256 {
		t.Fatalf("failure %+v %v", got, err)
	}
}

func TestBrainstormStartRefusesInvalidIdentityVersionAndPipeline(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*catalog.StartBrainstormingRequest)
		sql    string
	}{
		{name: "request key", mutate: func(in *catalog.StartBrainstormingRequest) { in.RequestID = "short" }},
		{name: "revision", mutate: func(in *catalog.StartBrainstormingRequest) { in.PipelineRevision = 2 }},
		{name: "version", mutate: func(in *catalog.StartBrainstormingRequest) { in.DiscoveryVersion = 2 }},
		{name: "choice", mutate: func(in *catalog.StartBrainstormingRequest) { in.Selection.CatalogRevision = "" }},
		{name: "negative context", mutate: func(in *catalog.StartBrainstormingRequest) { in.Selection.ContextLength = -1 }},
		{name: "fingerprint missing", mutate: func(in *catalog.StartBrainstormingRequest) { in.Selection.CredentialIdentity = "" }},
		{name: "raw credential", mutate: func(in *catalog.StartBrainstormingRequest) { in.Selection.CredentialIdentity = "raw-private-value" }},
		{name: "status missing", mutate: func(in *catalog.StartBrainstormingRequest) { in.Selection.Status = "" }},
		{name: "manual", mutate: func(in *catalog.StartBrainstormingRequest) { in.Selection.Status = "unverified_manual" }},
		{name: "generic", mutate: func(in *catalog.StartBrainstormingRequest) { in.Selection.Source = "generic_models" }},
		{name: "unconfirmed unfiltered", mutate: func(in *catalog.StartBrainstormingRequest) {
			in.Selection.Status = "listed_unfiltered"
			in.Selection.Source = "openrouter_general_unfiltered"
		}},
		{name: "destination credentials", mutate: func(in *catalog.StartBrainstormingRequest) {
			in.Selection.Destination = "https://user:private@example.test"
		}},
		{name: "frozen", sql: `UPDATE pipeline_runs SET discovery_frozen_version=1`},
		{name: "legacy", sql: `UPDATE pipeline_runs SET kind='legacy'`},
		{name: "stage", sql: `UPDATE pipeline_runs SET current_stage='spec'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, pipeline := authoringFixture(t)
			if err := store.CreateAuthoringPipeline(t.Context(), pipeline, "Discovery"); err != nil {
				t.Fatal(err)
			}
			in := catalog.StartBrainstormingRequest{PipelineID: pipeline.ID, RequestID: "start_request_0001", PipelineRevision: 1, DiscoveryVersion: 1, Selection: brainstormChoice()}
			if test.mutate != nil {
				test.mutate(&in)
			}
			if test.sql != "" {
				if _, err := store.DB().Exec(test.sql); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.StartBrainstorming(t.Context(), in); err == nil {
				t.Fatal("invalid start admitted")
			}
			var runs, attempts int
			if err := store.DB().QueryRow(`SELECT COUNT(*) FROM pipeline_brainstorm_runs`).Scan(&runs); err != nil {
				t.Fatal(err)
			}
			if err := store.DB().QueryRow(`SELECT COUNT(*) FROM pipeline_brainstorm_attempts`).Scan(&attempts); err != nil || runs != 0 || attempts != 0 {
				t.Fatalf("writes %d %d %v", runs, attempts, err)
			}
		})
	}
}

func TestBrainstormGenerationAttemptLimitsIncludeFailures(t *testing.T) {
	for _, test := range []struct {
		kind  string
		limit int
	}{{"question", 8}, {"synthesis", 3}} {
		t.Run(test.kind, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			initialAttempts := 0
			if test.kind == "synthesis" {
				brainstormAnsweredTurn(t, store, run.ID)
				initialAttempts = 1
			}
			for number := 0; number < test.limit; number++ {
				attempt := brainstormAdmit(t, store, run.ID, test.kind, fmt.Sprintf("attempt_request_%02d", number))
				if _, err := store.FailBrainstormAttempt(t.Context(), catalog.FailBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, ErrorCode: "provider_failed"}); err != nil {
					t.Fatal(err)
				}
				// Arrange a future explicit resume; its human API belongs to Task 4.
				if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_runs SET state='ready' WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			in := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "last_request_0001"), Kind: test.kind, Selection: brainstormChoice(), EstimatedInputTokens: 1000}
			var err error
			if test.kind == "synthesis" {
				_, _, err = store.QuestionsSufficient(t.Context(), catalog.GenerateBrainstormSynthesisRequest{BrainstormRequest: in.BrainstormRequest, Selection: in.Selection, EstimatedInputTokens: in.EstimatedInputTokens})
			} else {
				_, err = store.BeginBrainstormAttempt(t.Context(), in)
			}
			if !errors.Is(err, sdd.ErrInvalidTransition) {
				t.Fatalf("limit admitted %v", err)
			}
			got, err := store.GetBrainstorming(t.Context(), run.ID)
			if err != nil || got.AttemptCount != test.limit+initialAttempts {
				t.Fatalf("counter %+v %v", got, err)
			}
		})
	}
}

func TestBrainstormAnswerAndSynthesisSizeBounds(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	attempt := brainstormAdmit(t, store, run.ID, "question", "question_request1")
	if _, err := store.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "session", Question: strings.Repeat("界", 279) + "?"}); err != nil {
		t.Fatal(err)
	}
	run, _ = store.GetBrainstorming(t.Context(), run.ID)
	answer := catalog.AnswerBrainstormRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "answer_request_01"), QuestionID: run.CurrentQuestionID, Answer: strings.Repeat("a", 16*1024+1)}
	if _, err := store.AnswerBrainstormQuestion(t.Context(), answer); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("large answer %v", err)
	}
	answer.Answer = strings.Repeat("a", 16*1024)
	if _, err := store.AnswerBrainstormQuestion(t.Context(), answer); err != nil {
		t.Fatal(err)
	}
	attempt = brainstormAdmit(t, store, run.ID, "synthesis", "synthesis_req_001")
	complete := catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "synthesis_session"}
	for _, content := range []*catalog.BrainstormSynthesisContent{{Scope: "scope"}, {Decisions: []string{"decisions"}}, {Scope: strings.Repeat("a", 64*1024), Decisions: []string{"decisions"}}, {Scope: "scope", Decisions: []string{" "}}, {Scope: "scope", Decisions: []string{"decision"}, OpenQuestions: []string{" "}}} {
		complete.Synthesis = content
		if _, err := store.CompleteBrainstormAttempt(t.Context(), complete); !errors.Is(err, sdd.ErrInvalidTransition) {
			t.Fatalf("invalid synthesis %v", err)
		}
	}
	complete.Synthesis = &catalog.BrainstormSynthesisContent{Scope: "scope", Decisions: []string{"decisions"}}
	if _, err := store.CompleteBrainstormAttempt(t.Context(), complete); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteBrainstormAttempt(t.Context(), complete); err != nil {
		t.Fatalf("duplicate completion %v", err)
	}
	run, _ = store.GetBrainstorming(t.Context(), run.ID)
	if run.Attempts[0].Usage != nil || run.Attempts[1].Usage != nil || len(run.Syntheses) != 1 || run.OutputBudgetRemaining != 4608-256-1024 {
		t.Fatalf("usage absent %+v", run)
	}
	before, _ := store.GetPipeline(t.Context(), run.PipelineID)
	if err := store.ReviseAuthoringDiscovery(t.Context(), run.PipelineID, before.Revision, 1, "Revised", "title", "objective"); err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetPipeline(t.Context(), run.PipelineID)
	run, _ = store.GetBrainstorming(t.Context(), run.ID)
	if run.State != "invalidated" || len(run.Turns) != 1 || run.Turns[0].Answer != answer.Answer || len(run.Syntheses) != 1 || run.Syntheses[0].Content.Scope != "scope" || after.Status[sdd.Discovery] != sdd.Active {
		t.Fatalf("lost immutable history %+v %+v", run, after)
	}
}

func TestBrainstormSchemaSupportsFutureHumanGatesWithoutContentRewrite(t *testing.T) {
	for _, status := range []string{"approved", "rejected"} {
		t.Run(status, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			brainstormAnsweredTurn(t, store, run.ID)
			attempt := brainstormAdmit(t, store, run.ID, "synthesis", "synthesis_req_001")
			if _, err := store.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "session", Synthesis: &catalog.BrainstormSynthesisContent{Scope: "scope", Decisions: []string{"decision"}, OpenQuestions: []string{"question"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_syntheses SET status=? WHERE run_id=?`, status, run.ID); err != nil {
				t.Fatalf("human gate status %v", err)
			}
			if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_syntheses SET content='{}' WHERE run_id=?`, run.ID); err == nil {
				t.Fatal("gate rewrote synthesis")
			}
			if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_syntheses SET status='completed' WHERE run_id=?`, run.ID); err == nil {
				t.Fatal("terminal synthesis status undone")
			}
			if _, err := store.DB().Exec(`INSERT INTO pipeline_brainstorm_requests (run_id,request_id,kind,payload_hash,result_id,created_at) VALUES (?,?,'cancel','hash','result',?)`, run.ID, "cancel_request_01", formatCatalogTime(time.Now().UTC())); err != nil {
				t.Fatal(err)
			}
		})
	}
	store, _, run, _ := brainstormFixture(t)
	attempt := brainstormAdmit(t, store, run.ID, "question", "question_request1")
	if _, err := store.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "session", Question: "Question?"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_turns SET status='skipped' WHERE run_id=?`, run.ID); err != nil {
		t.Fatalf("skip turn %v", err)
	}
	if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_turns SET answer='overwrite' WHERE run_id=?`, run.ID); err == nil {
		t.Fatal("skipped turn rewritten")
	}
}

func TestBrainstormFailureAcceptsBoundedProviderBudgetCodes(t *testing.T) {
	for _, code := range []string{"budget_overrun", "output_overflow"} {
		t.Run(code, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			attempt := brainstormAdmit(t, store, run.ID, "question", "question_request1")
			if _, err := store.FailBrainstormAttempt(t.Context(), catalog.FailBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, ErrorCode: code}); err != nil {
				t.Fatalf("bounded code rejected %v", err)
			}
		})
	}
}

func TestBrainstormSynthesisRequiresAnAnsweredTurn(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	in := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "synthesis_req_001"), Kind: "synthesis", Selection: brainstormChoice(), EstimatedInputTokens: 1000}
	if _, err := store.BeginBrainstormAttempt(t.Context(), in); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("zero-answer synthesis %v", err)
	}
}

func TestBrainstormUsageOverrunPausesWithoutPublishing(t *testing.T) {
	for _, kind := range []string{"question", "synthesis"} {
		for _, dimension := range []string{"input", "output"} {
			t.Run(kind+"_"+dimension, func(t *testing.T) {
				store, _, run, _ := brainstormFixture(t)
				if kind == "synthesis" {
					brainstormAnsweredTurn(t, store, run.ID)
				}
				attempt := brainstormAdmit(t, store, run.ID, kind, "overrun_request_01")
				before, _ := store.GetBrainstorming(t.Context(), run.ID)
				usage := &catalog.BrainstormUsage{InputTokens: attempt.ReservedInputTokens, OutputTokens: attempt.ReservedOutputTokens}
				if dimension == "input" {
					usage.InputTokens++
				} else {
					usage.OutputTokens++
				}
				in := catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "overrun_session", Usage: usage}
				if kind == "question" {
					in.Question = "Question?"
				} else {
					in.Synthesis = &catalog.BrainstormSynthesisContent{Scope: "scope", Decisions: []string{"decision"}}
				}
				if _, err := store.CompleteBrainstormAttempt(t.Context(), in); err == nil {
					t.Fatal("usage overrun accepted")
				}
				if _, err := store.CompleteBrainstormAttempt(t.Context(), in); err == nil {
					t.Fatal("overrun retry accepted")
				}
				got, err := store.GetBrainstorming(t.Context(), run.ID)
				last := got.Attempts[len(got.Attempts)-1]
				wantTurns := 0
				if kind == "synthesis" {
					wantTurns = 1
				}
				if err != nil || got.State != "paused" || last.Status != "failed" || last.ErrorCode != "budget_overrun" || last.Usage == nil || len(got.Turns) != wantTurns || len(got.Syntheses) != 0 {
					t.Fatalf("overrun %+v %v", got, err)
				}
				wantInput, wantOutput := before.InputBudgetRemaining, before.OutputBudgetRemaining
				if dimension == "input" {
					wantInput--
				} else {
					wantOutput--
				}
				if got.InputBudgetRemaining != wantInput || got.OutputBudgetRemaining != wantOutput {
					t.Fatalf("overrun charged more than once: input=%d output=%d", got.InputBudgetRemaining, got.OutputBudgetRemaining)
				}
			})
		}
	}
}

func TestBrainstormInvalidOutputCannotHideUsageOverrun(t *testing.T) {
	for _, kind := range []string{"question", "synthesis"} {
		t.Run(kind, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			if kind == "synthesis" {
				brainstormAnsweredTurn(t, store, run.ID)
			}
			attempt := brainstormAdmit(t, store, run.ID, kind, "overrun_request_01")
			before, err := store.GetBrainstorming(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			usage := &catalog.BrainstormUsage{InputTokens: attempt.ReservedInputTokens, OutputTokens: attempt.ReservedOutputTokens}
			in := catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "overrun_session", Usage: usage}
			if kind == "question" {
				in.Question = "Invalid question without a question mark"
				usage.OutputTokens++
			} else {
				in.Synthesis = &catalog.BrainstormSynthesisContent{Scope: "Missing decisions"}
				usage.InputTokens++
			}
			for range 2 {
				if _, err := store.CompleteBrainstormAttempt(t.Context(), in); !errors.Is(err, sdd.ErrBrainstormBudgetExceeded) {
					t.Fatalf("invalid output concealed overrun: %v", err)
				}
			}
			got, err := store.GetBrainstorming(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			last := got.Attempts[len(got.Attempts)-1]
			if got.State != "paused" || got.Revision != before.Revision+1 || last.Status != "failed" || last.ErrorCode != "budget_overrun" || last.Usage == nil || len(got.Turns) != len(before.Turns) || len(got.Syntheses) != 0 {
				t.Fatalf("overrun not durably paused without publication: %+v", got)
			}
			wantInput, wantOutput := before.InputBudgetRemaining, before.OutputBudgetRemaining
			if kind == "question" {
				wantOutput--
			} else {
				wantInput--
			}
			if got.InputBudgetRemaining != wantInput || got.OutputBudgetRemaining != wantOutput {
				t.Fatalf("overrun retry changed charge: input=%d output=%d", got.InputBudgetRemaining, got.OutputBudgetRemaining)
			}
		})
	}
}

func TestBrainstormRecoveryRollsBackAggregateAndAttemptTogether(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	brainstormAdmit(t, store, run.ID, "question", "question_request1")
	if _, err := store.DB().Exec(`CREATE TRIGGER reject_recovery_event BEFORE INSERT ON events WHEN NEW.type='pipeline.brainstorm.interrupted' BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.InterruptRunningBrainstormAttempts(t.Context()); err == nil {
		t.Fatal("recovery late failure accepted")
	}
	got, err := store.GetBrainstorming(t.Context(), run.ID)
	if err != nil || got.State != "running_question" || got.Attempts[0].Status != "running" || got.ActiveDuration != 0 {
		t.Fatalf("partial recovery %+v %v", got, err)
	}
}

func TestBrainstormTimeoutRefusesLatePublication(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	attempt := brainstormAdmit(t, store, run.ID, "question", "question_request1")
	// Simulate an old process without sleeping or changing the production clock.
	if _, err := store.DB().Exec(`DROP TRIGGER brainstorm_immutable_attempt`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_attempts SET created_at=? WHERE id=?`, formatCatalogTime(time.Now().UTC().Add(-91*time.Second)), attempt.ID); err != nil {
		t.Fatal(err)
	}
	in := catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "unused_request_00"), AttemptID: attempt.ID, SessionID: "late_session", Question: "Late?"}
	if _, err := store.CompleteBrainstormAttempt(t.Context(), in); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("late publication %v", err)
	}
	if _, err := store.FailBrainstormAttempt(t.Context(), catalog.FailBrainstormAttemptRequest{BrainstormRequest: in.BrainstormRequest, AttemptID: attempt.ID, ErrorCode: "timeout"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetBrainstorming(t.Context(), run.ID)
	if err != nil || len(got.Turns) != 0 || got.State != "paused" || got.ActiveDuration != 90*time.Second {
		t.Fatalf("timeout %+v %v", got, err)
	}
}
