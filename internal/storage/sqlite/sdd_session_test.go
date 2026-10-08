package sqlite

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestBrainstormSessionAtomicLink(t *testing.T) {
	for _, mutation := range []string{"valid", "pipeline", "run", "selection", "credential", "discovery", "attempt", "workspace", "mode", "rollback"} {
		t.Run(mutation, func(t *testing.T) {
			db, _, run, _ := brainstormFixture(t)
			attempt := brainstormAdmit(t, db, run.ID, "question", "session_attempt_001")
			ref := brainstormRef(t, db, run.ID, attempt.RequestID)
			pipeline, _ := db.GetPipeline(t.Context(), run.PipelineID)
			workspace, _ := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
			now := time.Now().UTC()
			record := catalog.SessionRecord{ID: "linked-session", WorkspaceID: workspace.ID, BackendID: attempt.Selection.BackendID, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
			switch mutation {
			case "pipeline":
				ref.PipelineRevision--
			case "run":
				ref.RunRevision--
			case "selection":
				attempt.Selection.ModelID = "tampered"
			case "credential":
				attempt.Selection.CredentialIdentity = "tampered"
			case "discovery":
				ref.DiscoveryVersion++
			case "attempt":
				attempt.ID = "other"
			case "workspace":
				record.WorkspaceID = "other"
			case "mode":
				record.Mode = "evaluation"
			case "rollback":
				if _, err := db.db.Exec(`CREATE TRIGGER fail_session_selection BEFORE INSERT ON session_model_selections BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err := db.CreateBrainstormSession(t.Context(), ref, attempt, record)
			stored, readErr := db.GetBrainstorming(t.Context(), run.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mutation != "valid" {
				if err == nil || stored.Attempts[0].SessionID != "" {
					t.Fatalf("invalid link committed: %v %+v", err, stored.Attempts[0])
				}
				if _, err := db.GetSession(t.Context(), record.ID); err == nil {
					t.Fatal("orphan session persisted")
				}
				return
			}
			if err != nil || stored.Attempts[0].SessionID != record.ID {
				t.Fatalf("link: %v %+v", err, stored.Attempts[0])
			}
			selection, err := db.GetSessionModelSelection(t.Context(), record.ID)
			if err != nil || selection.ModelID != attempt.Selection.ModelID || selection.MaxOutputTokens != 256 || selection.WorkspacePath != workspace.Path {
				t.Fatalf("selection: %+v %v", selection, err)
			}
			if _, err := db.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: attempt.ID, SessionID: "unlinked-session", Question: "Scope?"}); !errors.Is(err, ErrPipelineConflict) {
				t.Fatalf("completion replaced session link: %v", err)
			}
			record.ID = "second-session"
			if err := db.CreateBrainstormSession(t.Context(), ref, attempt, record); !errors.Is(err, ErrPipelineConflict) {
				t.Fatalf("relink = %v", err)
			}
		})
	}
}

func TestBrainstormSessionConcurrentLinkHasOneWinner(t *testing.T) {
	db, path, run, _ := brainstormFixture(t)
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	attempt := brainstormAdmit(t, db, run.ID, "question", "session_attempt_001")
	ref := brainstormRef(t, db, run.ID, attempt.RequestID)
	pipeline, _ := db.GetPipeline(t.Context(), run.PipelineID)
	now := time.Now().UTC()
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, store := range []*Store{db, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			record := catalog.SessionRecord{ID: []string{"first", "second"}[i], WorkspaceID: pipeline.WorkspaceID, BackendID: attempt.Selection.BackendID, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
			results <- store.CreateBrainstormSession(t.Context(), ref, attempt, record)
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrPipelineConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d", winners)
	}
	records, err := db.ListSessions(t.Context(), pipeline.WorkspaceID)
	if err != nil || len(records) != 1 {
		t.Fatalf("sessions = %+v %v", records, err)
	}
}
