package sqlite

import (
	"errors"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func TestBrainstormClientIntentReadback(t *testing.T) {
	store, _, pipeline := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), pipeline, "Discovery"); err != nil {
		t.Fatal(err)
	}
	in := catalog.StartBrainstormingRequest{PipelineID: pipeline.ID, RequestID: "intent_start_0001", PipelineRevision: 1, DiscoveryVersion: 1, Selection: brainstormChoice(), ClientIntentHash: strings.Repeat("a", 64)}
	run, err := store.StartBrainstorming(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	read, err := store.GetBrainstormingByStartRequest(t.Context(), pipeline.ID, in.RequestID)
	if err != nil || read.ID != run.ID || read.StartClientIntentHash != in.ClientIntentHash {
		t.Fatalf("start readback: %+v %v", read, err)
	}
	byPipeline, err := store.GetBrainstormingByPipeline(t.Context(), pipeline.ID, 1)
	if err != nil || byPipeline.ID != run.ID {
		t.Fatalf("pipeline readback: %+v %v", byPipeline, err)
	}
	command := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "intent_question_01"), Kind: "question", Selection: brainstormChoice(), EstimatedInputTokens: 2000, ClientIntentHash: strings.Repeat("b", 64)}
	attempt, admitted, err := store.BeginBrainstormQuestion(t.Context(), command)
	if err != nil || !admitted {
		t.Fatalf("admission: %v %v", admitted, err)
	}
	receipt, err := store.GetBrainstormCommand(t.Context(), run.ID, command.RequestID)
	if err != nil || receipt.ClientIntentHash != command.ClientIntentHash || receipt.Action != "question" || receipt.AttemptID != attempt.ID {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	command.ClientIntentHash = strings.Repeat("c", 64)
	if _, admitted, err := store.BeginBrainstormQuestion(t.Context(), command); !errors.Is(err, ErrPipelineConflict) || admitted {
		t.Fatalf("changed intent accepted: %v %v", admitted, err)
	}
	if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_runs SET start_client_intent_hash='changed' WHERE id=?`, run.ID); err == nil {
		t.Fatal("start intent mutable")
	}
	if _, err := store.DB().Exec(`UPDATE pipeline_brainstorm_requests SET client_intent_hash='changed' WHERE run_id=? AND request_id=?`, run.ID, command.RequestID); err == nil {
		t.Fatal("command intent mutable")
	}
}

func TestBrainstormAnswerRejectsMalformedQuestionIDBeforeHashing(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	pending := pendingBrainstormQuestion(t, store, run.ID, 1)
	for _, questionID := range []string{"\xff", strings.Repeat("x", 129), "question\nline", ""} {
		_, err := store.AnswerBrainstormQuestion(t.Context(), catalog.AnswerBrainstormRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "invalid_answer_0001"), QuestionID: questionID, Answer: "Confirmed answer"})
		if !errors.Is(err, sdd.ErrInvalidTransition) {
			t.Fatalf("malformed question ID error=%v", err)
		}
	}
	after, err := store.GetBrainstorming(t.Context(), run.ID)
	if err != nil || after.Revision != pending.Revision || after.State != "waiting_answer" {
		t.Fatalf("invalid answer changed state: %+v %v", after, err)
	}
}

func TestBrainstormIntentMigrationPreservesLegacyPreparedHashes(t *testing.T) {
	store, path, run, start := brainstormFixture(t)
	in := catalog.BeginBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, store, run.ID, "legacy_question_001"), Kind: "question", Selection: brainstormChoice(), EstimatedInputTokens: 2000}
	a, admitted, err := store.BeginBrainstormQuestion(t.Context(), in)
	if err != nil || !admitted {
		t.Fatalf("seed: %v %v", admitted, err)
	}
	// Restore the v27 schema shape, then let Open apply the real migration.
	for _, statement := range []string{
		`DROP TRIGGER brainstorm_immutable_start_intent`,
		`DROP TRIGGER brainstorm_immutable_request`,
		`ALTER TABLE pipeline_brainstorm_runs DROP COLUMN start_client_intent_hash`,
		`ALTER TABLE pipeline_brainstorm_requests DROP COLUMN client_intent_hash`,
		`DELETE FROM schema_migrations WHERE version=28`,
	} {
		if _, err := store.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	stored, err := upgraded.GetBrainstorming(t.Context(), run.ID)
	if err != nil || stored.StartPayloadHash != run.StartPayloadHash || stored.StartClientIntentHash != "" || len(stored.Attempts) != 1 || stored.Attempts[0].PayloadHash != a.PayloadHash {
		t.Fatalf("migration changed evidence: %+v %v", stored, err)
	}
	if replay, err := upgraded.StartBrainstorming(t.Context(), start); err != nil || replay.ID != run.ID {
		t.Fatalf("legacy start replay: %+v %v", replay, err)
	}
	if replay, admitted, err := upgraded.BeginBrainstormQuestion(t.Context(), in); err != nil || admitted || replay.ID != a.ID {
		t.Fatalf("legacy attempt replay: %+v %v %v", replay, admitted, err)
	}
}
