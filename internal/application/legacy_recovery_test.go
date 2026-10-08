package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"sync"
	"testing"
)

type iteratorOnlyStore struct{ Store }

func (iteratorOnlyStore) ListAfterLimit(context.Context, string, int64, int) ([]events.Event, error) {
	return nil, errors.New("paged history forbidden")
}

type budgetWalkStore struct {
	Store
	maxBytes int
}

func (s *budgetWalkStore) WalkAfter(_ context.Context, _ string, _ int64, _ int, maxBytes int, _ func(events.Event) error) error {
	s.maxBytes = maxBytes
	return events.ErrEventBudgetExceeded
}
func TestRecoverAndOpenFailClosedAtIteratorBudget(t *testing.T) {
	s, db, vault, session := sessionSetup(t, &fakeProvider{})
	Shutdown(s)
	store := &budgetWalkStore{Store: db}
	next := NewService(t.Context(), Dependencies{Store: store, Secrets: vault, External: map[string]ExternalBackend{}})
	gets := vault.gets
	if err := next.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := next.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID}); !errors.Is(err, ErrHistoryTooLarge) {
		t.Fatal(err)
	}
	if store.maxBytes != 64<<20 || vault.gets != gets || len(next.sessions) != 0 {
		t.Fatal("over-budget history admitted", store.maxBytes, vault.gets)
	}
}

func TestRecoveryRepairsLegacyAndActiveCallsWithoutInventingOutcomes(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, called := range []bool{false, true} {
			t.Run(map[bool]string{true: "legacy", false: "active"}[legacy]+map[bool]string{true: "_called", false: "_approval"}[called], func(t *testing.T) {
				p := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "original", Name: "write", Arguments: json.RawMessage(`{"path":"unsafe.txt","content":"never"}`)}}
				first, db, vault, session := sessionSetup(t, p)
				result, err := first.Prompt(PromptInput{SessionID: session.ID, Text: "write"})
				if err != nil || result.Approval == nil {
					t.Fatal(result, err)
				}
				Shutdown(first)
				if called {
					db.Append(t.Context(), session.ID, "agent_session", "tool.called", map[string]string{"toolCallId": result.Approval.ToolCallID, "name": "write"})
				}
				if legacy {
					db.Append(t.Context(), session.ID, "agent_session", "run.interrupted", map[string]string{"reason": "app_restart"})
				}
				store := iteratorOnlyStore{db}
				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						s := NewService(t.Context(), Dependencies{Store: store, Secrets: vault, External: map[string]ExternalBackend{}})
						if err := s.Recover(t.Context()); err != nil {
							t.Error(err)
						}
					}()
				}
				wg.Wait()
				items, err := db.ListAfter(t.Context(), session.ID, 0)
				if err != nil {
					t.Fatal(err)
				}
				terminals, closures := 0, 0
				code, typ, text := "not_executed", "tool.skipped", "tool call not executed"
				if called {
					code, typ, text = "outcome_unknown", "tool.failed", "tool outcome unknown after interruption"
				}
				for _, event := range items {
					if event.Type == "run.interrupted" {
						terminals++
					}
					if event.Type == "tool.skipped" || event.Type == "tool.failed" {
						closures++
						var data map[string]string
						json.Unmarshal(event.Data, &data)
						if event.Type != typ || data["errorCode"] != code || data["error"] != text {
							t.Fatal(event.Type, string(event.Data))
						}
					}
				}
				if terminals != 1 || closures != 1 {
					t.Fatal("missing or duplicate recovery evidence", terminals, closures)
				}
				next := &fakeProvider{}
				second := NewService(t.Context(), Dependencies{Store: store, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(c openai.Config) (agentcore.Provider, error) { next.key = c.APIKey; return next, nil }})
				if _, err := second.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID}); err != nil {
					t.Fatal(err)
				}
				if _, err := second.Approve(ApprovalInput{SessionID: session.ID, ApprovalID: result.Approval.ID, Allow: true}); !errors.Is(err, agentcore.ErrApprovalNotFound) {
					t.Fatal(err)
				}
				if _, err := second.Prompt(PromptInput{SessionID: session.ID, Text: "continue"}); err != nil {
					t.Fatal(err)
				}
				if history := conversationOnly(next.request.Messages); len(history) != 4 || history[2].Content != `{"error":"`+code+`"}` {
					t.Fatal(next.request.Messages)
				}
			})
		}
	}
}
