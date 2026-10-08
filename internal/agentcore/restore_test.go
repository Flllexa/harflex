package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/security"
	"strings"
	"testing"
)

func TestRestoreSessionOwnsHistoryAndDoesNotReplay(t *testing.T) {
	history := []Message{{Role: RoleUser, Content: "before"}, {Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "public-call", Name: "test", Arguments: json.RawMessage(`{"ok":true}`)}}}, {Role: RoleTool, ToolCallID: "public-call", Content: `{"answer":1}`}}
	calls := 0
	p := &sessionProvider{stream: func(_ context.Context, r ChatRequest) (<-chan StreamEvent, <-chan error) {
		calls++
		if len(r.Messages) != 4 || r.Messages[0].Content != "before" || string(r.Messages[1].ToolCalls[0].Arguments) != `{"ok":true}` {
			t.Fatal(r)
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "done"})
	}}
	s, err := RestoreSession("session-1", p, &sessionTools{}, &sessionJournal{}, security.Ask, history)
	if err != nil || calls != 0 {
		t.Fatal(err, calls)
	}
	history[0].Content = "changed"
	history[1].ToolCalls[0].Arguments[0] = 'X'
	if err := s.Approve(t.Context(), "old-approval", true); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatal(err)
	}
	if err := s.Prompt(t.Context(), "new"); err != nil {
		t.Fatal(err)
	}
}
func TestRestoreRejectsBrokenHistory(t *testing.T) {
	call := ToolCall{ID: "call", Name: "test", Arguments: json.RawMessage(`{}`)}
	for _, history := range [][]Message{
		{{Role: RoleTool, ToolCallID: "orphan", Content: `{}`}},
		{{Role: RoleAssistant, ToolCalls: []ToolCall{call}}},
		{{Role: RoleAssistant, ToolCalls: []ToolCall{call}}, {Role: RoleTool, ToolCallID: "wrong", Content: `{}`}},
		{{Role: RoleUser, Content: strings.Repeat("x", events.MaxDataBytes+1)}},
		{{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "bad", Name: "test", Arguments: json.RawMessage(`invalid`)}}}},
		{{Role: "unknown", Content: "bad"}},
	} {
		if _, err := RestoreSession("restored", nil, nil, nil, security.Ask, history); !errors.Is(err, ErrInvalidHistory) {
			t.Fatal(history, err)
		}
	}
}
