package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func TestReopenRestoresHistoryWithoutExecuting(t *testing.T) {
	p := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "read-call", Name: "ls", Arguments: json.RawMessage(`{"path":"."}`)}}
	first, db, vault, session := sessionSetup(t, p)
	if result, err := first.Prompt(PromptInput{session.ID, "first " + profileInput().APIKey}); err != nil || result.Status != RunCompleted {
		t.Fatal(result, err)
	}
	Shutdown(first)
	next := &fakeProvider{}
	second := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(c openai.Config) (agentcore.Provider, error) { next.key = c.APIKey; return next, nil }})
	if err := second.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	openedEvents := 0
	SetEmitter(second, func(string, any) { openedEvents++ })
	listed, err := second.ListSessions(ListSessionsInput{WorkspaceID: session.WorkspaceID})
	if err != nil || len(listed) != 1 || listed[0].Status != "completed" || !listed[0].Resumable {
		t.Fatal(listed, err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := second.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
			if err != nil || got.ID != session.ID {
				t.Error(got, err)
			}
		}()
	}
	wg.Wait()
	if len(next.request.Messages) != 0 || len(second.sessions) != 1 || openedEvents != 0 {
		t.Fatal("open executed or created duplicate runners")
	}
	if result, err := second.Prompt(PromptInput{session.ID, "next"}); err != nil || result.Status != RunCompleted {
		t.Fatal(result, err)
	}
	history := conversationOnly(next.request.Messages)
	if len(history) != 5 || history[0].Content != "first [REDACTED]" || history[1].Role != agentcore.RoleAssistant || history[2].Role != agentcore.RoleTool || history[1].ToolCalls[0].ID != history[2].ToolCallID || history[3].Content != "hello" || history[4].Content != "next" {
		t.Fatalf("history: %+v", history)
	}
}

type terminalCatalogFailure struct{ Store }

func (s terminalCatalogFailure) UpsertSession(ctx context.Context, r catalog.SessionRecord) error {
	if r.Status == "completed" {
		return errors.New("private-canary")
	}
	return s.Store.UpsertSession(ctx, r)
}

func TestOpenReconcilesStatusAfterDurableTerminalCatalogFailure(t *testing.T) {
	s, db, vault, session := sessionSetup(t, &fakeProvider{})
	s.journals[session.ID].store = terminalCatalogFailure{db}
	if _, err := s.Prompt(PromptInput{session.ID, "first"}); err == nil {
		t.Fatal("catalog failure reported success")
	}
	if _, err := s.Prompt(PromptInput{session.ID, "again"}); err == nil {
		t.Fatal("faulted runner accepted input")
	}
	Shutdown(s)
	next := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}})
	got, err := next.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
	if err != nil || got.Status != "completed" {
		t.Fatal(got, err)
	}
}

type reopenExternal struct{ requests []externalagent.Request }

func (a *reopenExternal) ID() string { return "opencode" }
func (a *reopenExternal) Detect() externalagent.Detection {
	return externalagent.Detection{Available: true}
}
func (a *reopenExternal) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Resumable: true}
}
func (a *reopenExternal) Run(_ context.Context, r externalagent.Request) (<-chan externalagent.Event, <-chan error) {
	a.requests = append(a.requests, r)
	es := make(chan externalagent.Event, 1)
	es <- externalagent.Event{Type: "text", Text: "response", SessionID: "ses_reopen"}
	close(es)
	errs := make(chan error)
	close(errs)
	return es, errs
}
func TestOpenExternalRestoresSessionIDAndDoesNotReadKeyring(t *testing.T) {
	base, db, vault := setup(t)
	workspace, err := base.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &reopenExternal{}
	first := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{"opencode": a}})
	session, err := first.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := first.Prompt(PromptInput{session.ID, "hello"}); err != nil || result.Status != RunCompleted {
		t.Fatal(result, err)
	}
	Shutdown(first)
	b := &reopenExternal{}
	second := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{"opencode": b}})
	if _, err := second.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	if len(b.requests) != 0 {
		t.Fatal("open ran CLI")
	}
	if result, err := second.Prompt(PromptInput{session.ID, "next"}); err != nil || result.Status != RunCompleted {
		t.Fatal(result, err)
	}
	if len(b.requests) != 1 || b.requests[0].SessionID != "ses_reopen" || vault.gets != 0 {
		t.Fatal(b.requests, vault.gets)
	}
}

func TestOpenChangedProfileRemainsReadableButCannotContinue(t *testing.T) {
	first, db, vault, session := sessionSetup(t, &fakeProvider{})
	Shutdown(first)
	profile, err := db.GetProviderProfile(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	profile.Model = "different-model"
	if err := db.UpsertProviderProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	next := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}})
	if dto, err := next.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID}); err != nil || dto.Resumable {
		t.Fatal(dto, err)
	}
	if _, err := next.Prompt(PromptInput{SessionID: session.ID, Text: "next"}); !errors.Is(err, ErrBackendChanged) {
		t.Fatal(err)
	}
}

func TestReopenInterruptedApprovalIsClosedAndNeverExecutable(t *testing.T) {
	p := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "write-call", Name: "write", Arguments: json.RawMessage(`{"path":"result.txt","content":"unsafe"}`)}}
	first, db, vault, session := sessionSetup(t, p)
	result, err := first.Prompt(PromptInput{session.ID, "write"})
	if err != nil || result.Approval == nil {
		t.Fatal(result, err)
	}
	if record, err := db.GetSession(t.Context(), session.ID); err != nil || record.Status != "awaiting_approval" {
		t.Fatal(record, err)
	}
	Shutdown(first)
	next := &fakeProvider{}
	second := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(c openai.Config) (agentcore.Provider, error) { next.key = c.APIKey; return next, nil }})
	var emitted []string
	SetEmitter(second, func(_ string, payload any) {
		event := payload.(EventDTO)
		emitted = append(emitted, event.Type)
		items, err := db.ListAfter(t.Context(), session.ID, event.Sequence-1)
		if err != nil || len(items) == 0 || items[0].ID != event.ID || items[len(items)-1].Type != "run.interrupted" {
			t.Error("recovery emitted before atomic commit", err)
		}
	})
	opened, err := second.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
	if err != nil || opened.Status != "paused" {
		t.Fatal(opened, err)
	}
	if len(next.request.Messages) != 0 || len(emitted) != 2 || emitted[0] != "tool.skipped" || emitted[1] != "run.interrupted" {
		t.Fatal("open executed unexpected work", emitted)
	}
	workspace, err := db.GetWorkspace(t.Context(), session.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "result.txt")); !os.IsNotExist(err) {
		t.Fatal("recovery executed a tool", err)
	}
	SetEmitter(second, nil)
	if _, err := second.Approve(ApprovalInput{session.ID, result.Approval.ID, true}); !errors.Is(err, agentcore.ErrApprovalNotFound) {
		t.Fatal(err)
	}
	if result, err := second.Prompt(PromptInput{session.ID, "new run"}); err != nil || result.Status != RunCompleted {
		t.Fatal(result, err)
	}
	if history := conversationOnly(next.request.Messages); len(history) != 4 || history[2].Content != `{"error":"not_executed"}` || history[2].ToolCallID != history[1].ToolCalls[0].ID {
		t.Fatalf("history: %+v", next.request.Messages)
	}
}

func TestListSessionsFiltersWorkspaceAndOrdersByLatestActivity(t *testing.T) {
	s, db, _ := setup(t)
	first, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, record := range []catalog.SessionRecord{
		{ID: "a", WorkspaceID: first.ID, BackendID: "missing", Status: "ready", CreatedAt: now, UpdatedAt: now},
		{ID: "c", WorkspaceID: first.ID, BackendID: "missing", Status: "paused", CreatedAt: now, UpdatedAt: now.Add(time.Second)},
		{ID: "b", WorkspaceID: first.ID, BackendID: "missing", Status: "failed", CreatedAt: now, UpdatedAt: now.Add(time.Second)},
		{ID: "d", WorkspaceID: second.ID, BackendID: "missing", Status: "ready", CreatedAt: now, UpdatedAt: now.Add(time.Hour)},
	} {
		if err := db.UpsertSession(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListSessions(ListSessionsInput{WorkspaceID: first.ID})
	if err != nil || len(got) != 3 || got[0].ID != "b" || got[1].ID != "c" || got[2].ID != "a" {
		t.Fatal(got, err)
	}
	if _, err := s.ListSessions(ListSessionsInput{WorkspaceID: "unknown"}); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatal(err)
	}
	if _, err := s.ListSessions(ListSessionsInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}

func TestReopenRejectsInvalidCatalogAndHistory(t *testing.T) {
	for _, mode := range []string{"unknown", "wrong_workspace", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			first, db, vault, session := sessionSetup(t, &fakeProvider{})
			Shutdown(first)
			second := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}})
			input := OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID}
			want := ErrSessionNotFound
			switch mode {
			case "unknown":
				input.SessionID = "unknown"
			case "wrong_workspace":
				input.WorkspaceID = "other"
			case "corrupt":
				db.Append(context.Background(), session.ID, "agent_session", "message.assistant", map[string]string{"role": "user", "content": "invalid"})
				want = ErrSessionCorrupt
			}
			if _, err := second.OpenSession(input); !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if len(second.sessions) != 0 {
				t.Fatal("invalid runner retained")
			}
		})
	}
}
