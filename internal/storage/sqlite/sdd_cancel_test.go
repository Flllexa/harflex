package sqlite

import (
	"errors"
	"github.com/persioflexa/harflex/internal/catalog"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestBrainstormCancelReceiptFailureRollsBackAndLegacyFailsClosed(t *testing.T) {
	db, _, run, _ := brainstormFixture(t)
	a := brainstormAdmit(t, db, run.ID, "question", "attempt_request_01")
	in := catalog.CancelBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, db, run.ID, "cancel_request_01"), AttemptID: a.ID}
	if _, err := db.DB().Exec(`CREATE TRIGGER reject_cancel_receipt BEFORE INSERT ON pipeline_brainstorm_human_receipts BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CancelBrainstormAttempt(t.Context(), in); err == nil {
		t.Fatal("missing receipt accepted")
	}
	current, err := db.GetBrainstorming(t.Context(), run.ID)
	if err != nil || current.State != "running_question" || current.Attempts[0].Status != "running" || current.Revision != in.RunRevision {
		t.Fatalf("partial cancellation %+v %v", current, err)
	}
	hash := brainstormHash(brainstormHumanAction{Ref: in.BrainstormRequest, Kind: "cancel", AttemptID: in.AttemptID})
	if _, err := db.DB().Exec(`INSERT INTO pipeline_brainstorm_requests(run_id,request_id,kind,payload_hash,result_id,created_at) VALUES (?,?,'cancel',?,'old',?)`, run.ID, in.RequestID, hash, formatCatalogTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CancelBrainstormAttempt(t.Context(), in); !errors.Is(err, ErrHistoricalReceiptUnavailable) {
		t.Fatalf("legacy receipt %v", err)
	}
}

func TestBrainstormCancelResumeReceipts(t *testing.T) {
	for _, kind := range []string{"question", "synthesis"} {
		t.Run(kind, func(t *testing.T) {
			db, path, run, _ := brainstormFixture(t)
			if kind == "synthesis" {
				brainstormAnsweredTurn(t, db, run.ID)
				pendingBrainstormQuestion(t, db, run.ID, 2)
			}
			a := brainstormAdmit(t, db, run.ID, kind, "attempt_request_01")
			before, _ := db.GetBrainstorming(t.Context(), run.ID)
			in := catalog.CancelBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, db, run.ID, "cancel_request_01"), AttemptID: a.ID}
			paused, err := db.CancelBrainstormAttempt(t.Context(), in)
			if err != nil || paused.State != "paused" || paused.Attempts[len(paused.Attempts)-1].ErrorCode != "cancelled" {
				t.Fatalf("cancel %+v %v", paused, err)
			}
			if _, err := db.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: in.BrainstormRequest, AttemptID: a.ID, SessionID: "late", Question: "Late?"}); !errors.Is(err, ErrPipelineConflict) {
				t.Fatalf("late: %v", err)
			}
			other, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			resume := brainstormRef(t, other, run.ID, "resume_request_01")
			resumed, err := other.ResumePausedBrainstorm(t.Context(), resume)
			want := "ready"
			if kind == "synthesis" {
				want = "ready_for_synthesis"
			}
			if err != nil || resumed.State != want || resumed.InputBudgetRemaining != before.InputBudgetRemaining || resumed.OutputBudgetRemaining != before.OutputBudgetRemaining || resumed.AttemptCount != before.AttemptCount {
				t.Fatalf("resume %+v %v", resumed, err)
			}
			for _, turn := range resumed.Turns {
				if turn.Status == "waiting_answer" {
					t.Fatal("resume revived pending question")
				}
			}
			again, err := db.CancelBrainstormAttempt(t.Context(), in)
			if err != nil || !reflect.DeepEqual(again, paused) {
				t.Fatalf("cancel receipt changed %v", err)
			}
			again, err = db.ResumePausedBrainstorm(t.Context(), resume)
			if err != nil || !reflect.DeepEqual(again, resumed) {
				t.Fatalf("resume receipt changed %v", err)
			}
			in.AttemptID = "other"
			if _, err := db.CancelBrainstormAttempt(t.Context(), in); !errors.Is(err, ErrPipelineConflict) {
				t.Fatalf("changed payload %v", err)
			}
			resume.RunRevision++
			if _, err := db.ResumePausedBrainstorm(t.Context(), resume); !errors.Is(err, ErrPipelineConflict) {
				t.Fatalf("changed resume %v", err)
			}
		})
	}
}

func TestBrainstormCancelPublicationRace(t *testing.T) {
	for range 10 {
		db, path, run, _ := brainstormFixture(t)
		other, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		a := brainstormAdmit(t, db, run.ID, "question", "attempt_request_01")
		ref := brainstormRef(t, db, run.ID, "cancel_request_01")
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			_, err := db.CancelBrainstormAttempt(t.Context(), catalog.CancelBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: a.ID})
			errs <- err
		}()
		go func() {
			<-start
			_, err := other.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: a.ID, SessionID: "session", Question: "Scope?"})
			errs <- err
		}()
		close(start)
		successes := 0
		for range 2 {
			err := <-errs
			if err == nil {
				successes++
			} else if !errors.Is(err, ErrPipelineConflict) {
				t.Fatal(err)
			}
		}
		got, err := db.GetBrainstorming(t.Context(), run.ID)
		if err != nil || successes != 1 || (got.State == "paused" && len(got.Turns) != 0) || (got.State == "waiting_answer" && len(got.Turns) != 1) {
			t.Fatalf("race %+v %v winners=%d", got, err, successes)
		}
	}
}

func TestBrainstormConcurrentCancelResumeReplay(t *testing.T) {
	db, path, run, _ := brainstormFixture(t)
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	a := brainstormAdmit(t, db, run.ID, "question", "attempt_request_01")
	cancel := catalog.CancelBrainstormAttemptRequest{BrainstormRequest: brainstormRef(t, db, run.ID, "cancel_request_01"), AttemptID: a.ID}
	for _, kind := range []string{"cancel", "resume"} {
		ref := brainstormRef(t, db, run.ID, "resume_request_01")
		start := make(chan struct{})
		results := make(chan catalog.BrainstormRun, 2)
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, store := range []*Store{db, other} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				var got catalog.BrainstormRun
				var err error
				if kind == "cancel" {
					got, err = store.CancelBrainstormAttempt(t.Context(), cancel)
				} else {
					got, err = store.ResumePausedBrainstorm(t.Context(), ref)
				}
				results <- got
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(<-results, <-results) {
			t.Fatal("different receipts")
		}
	}
}

func TestBrainstormResumeRecoveryAndFrozenFences(t *testing.T) {
	for _, mode := range []string{"recovery", "frozen", "stale", "same_key"} {
		t.Run(mode, func(t *testing.T) {
			db, _, run, _ := brainstormFixture(t)
			brainstormAdmit(t, db, run.ID, "question", "attempt_request_01")
			if err := db.InterruptRunningBrainstormAttempts(t.Context()); err != nil {
				t.Fatal(err)
			}
			ref := brainstormRef(t, db, run.ID, "resume_request_01")
			switch mode {
			case "frozen":
				_, err := db.DB().Exec(`UPDATE pipeline_runs SET discovery_frozen_version=1 WHERE id=?`, run.PipelineID)
				if err != nil {
					t.Fatal(err)
				}
			case "stale":
				ref.PipelineRevision--
			case "same_key":
				ref.RequestID = "attempt_request_01"
			}
			got, err := db.ResumePausedBrainstorm(t.Context(), ref)
			if mode == "recovery" {
				if err != nil || got.State != "ready" || got.Attempts[0].Status != "interrupted" {
					t.Fatalf("recovery %+v %v", got, err)
				}
			} else if err == nil {
				t.Fatal("invalid resume accepted")
			}
		})
	}
}
