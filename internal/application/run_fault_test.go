package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

// flakyStore fails one write of the given event type, as SQLite does when its single connection is held past a
// write's deadline.
type flakyStore struct {
	*sqlite.Store
	mu       sync.Mutex
	failType string
	failed   bool
}

func (f *flakyStore) Append(ctx context.Context, streamID, kind, eventType string, data any) (events.Event, error) {
	f.mu.Lock()
	fail := eventType == f.failType && !f.failed
	if fail {
		f.failed = true
	}
	f.mu.Unlock()
	if fail {
		return events.Event{}, errors.New("database is locked (SQLITE_BUSY) while writing sk-test-secret-value")
	}
	return f.Store.Append(ctx, streamID, kind, eventType, data)
}

// A run whose journal write fails mid-way is closed: the session leaves "running", the journal says the run was
// interrupted, the person gets a reason instead of "operation failed", the cause is in the diagnostic log (without
// secrets), and the next message works.
func TestARunThatLosesItsJournalIsClosedAndSaysWhy(t *testing.T) {
	_, db, vault := setup(t)
	store := &flakyStore{Store: db, failType: "message.assistant"}
	logs := t.TempDir()
	provider := &fakeProvider{}
	s := NewService(t.Context(), Dependencies{Store: store, Secrets: vault, External: map[string]ExternalBackend{}, DiagnosticLogDir: logs,
		ProviderFactory: func(c openai.Config) (agentcore.Provider, error) { provider.key = c.APIKey; return provider, nil }})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "primeiro"})
	if err != nil || result.Status != RunFailed || result.Reason != "journal_unavailable" {
		t.Fatalf("a run that lost its journal: %+v %v", result, err)
	}
	record, err := db.GetSession(t.Context(), session.ID)
	if err != nil || record.Status == "running" {
		t.Fatalf("the session stayed running: %+v %v", record, err)
	}
	history, err := db.ListAfter(t.Context(), session.ID, 0)
	if err != nil || history[len(history)-1].Type != "run.interrupted" {
		t.Fatalf("the run was not closed in the journal: %v", err)
	}
	log, err := os.ReadFile(filepath.Join(logs, "harflex.log"))
	if err != nil || !strings.Contains(string(log), session.ID) || !strings.Contains(string(log), "SQLITE_BUSY") || strings.Contains(string(log), "sk-test-secret-value") {
		t.Fatalf("diagnostic log: %q %v", log, err)
	}
	if next, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "de novo"}); err != nil || next.Status != RunCompleted {
		t.Fatalf("the next message after the closed run: %+v %v", next, err)
	}
}

// The QA loop says what stopped a role: the cause's code, not only "operation failed".
func TestRoleFailureTextNamesTheCause(t *testing.T) {
	if got := roleFailureText(safe("prompt", agentcore.ErrJournalUnavailable)); !strings.Contains(got, "session_journal_unavailable") {
		t.Fatalf("journal failure: %q", got)
	}
	if got := roleFailureText(safe("prompt", agentcore.ErrSessionBusy)); !strings.Contains(got, "session_busy") {
		t.Fatalf("busy session: %q", got)
	}
	if got := roleFailureText(errors.New("failed: execution_failed")); got != "failed: execution_failed" {
		t.Fatalf("a run that ended failed: %q", got)
	}
}
