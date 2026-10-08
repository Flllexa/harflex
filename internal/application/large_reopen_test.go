package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/security"
	"strings"
	"testing"
)

type maximumResultTool struct{ payload, details json.RawMessage }

func (maximumResultTool) Specs() []agentcore.ToolSpec {
	return []agentcore.ToolSpec{{Name: "large", Schema: json.RawMessage(`{"type":"object"}`)}}
}
func (maximumResultTool) Risk(string) string { return "read_only" }
func (t maximumResultTool) Execute(context.Context, string, json.RawMessage, agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	return agentcore.ToolExecutionResult{Content: t.payload, Details: t.details}, nil
}

func TestMaximumToolFieldsPersistAndReopenWithinHistoryBudget(t *testing.T) {
	p := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "large-call", Name: "large", Arguments: json.RawMessage(`{}`)}}
	first, db, vault, session := sessionSetup(t, p)
	payload := json.RawMessage(`"` + strings.Repeat("x", (16<<20)-2) + `"`)
	runner := agentcore.NewSession(session.ID, p, maximumResultTool{payload, payload}, first.journals[session.ID], security.Ask)
	if err := runner.Prompt(t.Context(), "large result"); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("maximum-sized journal payload exceeded persistence deadline", err)
		}
		t.Fatal(err)
	}
	p.toolCall = &agentcore.ToolCall{ID: "second-large", Name: "large", Arguments: json.RawMessage(`{}`)}
	if err := runner.Prompt(t.Context(), "second result cannot fit"); !errors.Is(err, events.ErrStreamBudgetExceeded) {
		t.Fatal("second result exceeded durable budget", err)
	}
	var total int64
	var completed int
	if err := db.DB().QueryRow(`SELECT SUM(length(data)),SUM(CASE WHEN type='tool.completed' THEN 1 ELSE 0 END) FROM events WHERE stream_id=?`, session.ID).Scan(&total, &completed); err != nil || total > events.MaxStreamDataBytes || completed != 1 {
		t.Fatal(total, completed, err)
	}
	if err := runner.Prompt(t.Context(), "sealed retry"); !errors.Is(err, events.ErrStreamBudgetExceeded) {
		t.Fatal(err)
	}
	Shutdown(first)
	next := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return &fakeProvider{}, nil }})
	if err := next.Recover(t.Context()); err != nil {
		t.Fatal("writable event could not recover", err)
	}
	if _, err := next.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID}); err != nil {
		t.Fatal("writable event could not reopen", err)
	}
}

func TestEscapedToolContentWithinEventBudgetReopens(t *testing.T) {
	p := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "escaped", Name: "large", Arguments: json.RawMessage(`{}`)}}
	first, db, vault, session := sessionSetup(t, p)
	payload := json.RawMessage(`"` + strings.Repeat("<", 4<<20) + `"`)
	runner := agentcore.NewSession(session.ID, p, maximumResultTool{payload: payload}, first.journals[session.ID], security.Ask)
	if err := runner.Prompt(t.Context(), "escaped result"); err != nil {
		t.Fatal(err)
	}
	Shutdown(first)
	next := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return &fakeProvider{}, nil }})
	if _, err := next.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID}); err != nil {
		t.Fatal("serialized escaping made a valid result unreadable", err)
	}
}
