package application

import (
	"context"
	"errors"
	"github.com/persioflexa/harflex/internal/events"
	"testing"
)

func TestRecoveryIsolatesOversizedLegacyHistoryAndContinues(t *testing.T) {
	s, db, vault := setup(t)
	large := persistedSession(t, s, db, "a-large", "run.started")
	healthy := persistedSession(t, s, db, "z-healthy", "run.started")
	// Metadata alone reveals the legacy oversize; no payload must be decoded.
	if _, err := db.DB().Exec(`UPDATE events SET data=zeroblob(?) WHERE stream_id=?`, (64<<20)+1, large.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal("oversized history blocked startup", err)
	}
	got, err := db.GetSession(t.Context(), large.ID)
	if err != nil || got.Status != "history_too_large" {
		t.Fatal(got, err)
	}
	other, err := db.GetSession(t.Context(), healthy.ID)
	if err != nil || other.Status != "paused" {
		t.Fatal(other, err)
	}
	list, err := s.ListSessions(ListSessionsInput{WorkspaceID: large.WorkspaceID})
	if err != nil || len(list) != 1 || list[0].Resumable || list[0].Status != "history_too_large" {
		t.Fatal(list, err)
	}
	if _, err := s.OpenSession(OpenSessionInput{SessionID: large.ID, WorkspaceID: large.WorkspaceID}); err == nil || ErrorCode(err) != "history_too_large" {
		t.Fatal(err)
	}
	if vault.gets != 0 || len(s.sessions) != 0 {
		t.Fatal("oversize history executed dependencies")
	}
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := db.GetSession(t.Context(), large.ID)
	if !after.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatal("quarantine is not idempotent")
	}
}

type readFailureStore struct {
	Store
	failure error
}

func (s readFailureStore) WalkAfter(context.Context, string, int64, int, int, func(events.Event) error) error {
	return s.failure
}
func TestGenuineHistoryReadFailureStillFailsStartup(t *testing.T) {
	s, db, _ := setup(t)
	record := persistedSession(t, s, db, "session", "run.started")
	s.store = readFailureStore{Store: db, failure: errors.New("private storage failure")}
	if err := s.Recover(t.Context()); err == nil {
		t.Fatal("storage failure ignored")
	}
	got, _ := db.GetSession(t.Context(), record.ID)
	if got.Status == "history_too_large" {
		t.Fatal("storage error misclassified")
	}
}

func TestCurrentSessionSealsWhenStreamCannotAdmitResult(t *testing.T) {
	s, db, _, session := sessionSetup(t, &fakeProvider{})
	// Leave enough space for run admission but not for the normal user message.
	_, err := db.Append(t.Context(), session.ID, "agent_session", "legacy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec(`UPDATE events SET data=zeroblob(?) WHERE stream_id=?`, (64<<20)-2, session.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "cannot fit"}); !errors.Is(err, events.ErrStreamBudgetExceeded) || result.Status == RunCompleted {
		t.Fatal(result, err)
	}
	var before int
	db.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE stream_id=?`, session.ID).Scan(&before)
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "must remain sealed"}); !errors.Is(err, events.ErrStreamBudgetExceeded) {
		t.Fatal("sealed session resumed")
	}
	var after int
	var total int64
	db.DB().QueryRow(`SELECT COUNT(*),SUM(length(data)) FROM events WHERE stream_id=?`, session.ID).Scan(&after, &total)
	if before != after || total > 64<<20 {
		t.Fatal(before, after, total)
	}
}
