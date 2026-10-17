package agentcore

import (
	"context"
	"encoding/json"
	"github.com/persioflexa/harflex/internal/security"
	"strings"
	"testing"
)

func TestSerializedToolResultBudgetFailsTheCallBeforeCompletedAppend(t *testing.T) {
	rounds := 0
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		if rounds == 1 {
			return streamEvents(toolEvent("call"))
		}
		return streamEvents()
	}}
	// HTML escaping expands this valid, below-field-limit JSON to over 34 MiB.
	payload := json.RawMessage(`"` + strings.Repeat("<", 6<<20) + `"`)
	executor := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		return ToolExecutionResult{Content: payload}, nil
	}}
	journal := &sessionJournal{}
	session := NewSession("session-1", p, executor, journal, security.Ask)
	err := session.Prompt(t.Context(), "large result")
	if err != nil || rounds != 2 {
		t.Fatalf("an oversized result ended the run: %v rounds=%d", err, rounds)
	}
	for _, event := range journal.snapshot() {
		if event.Type == "tool.completed" || len(event.Data) > 34<<20 {
			t.Fatal("oversized result reached journal", event.Type, len(event.Data))
		}
	}
	assertTypes(t, journal, "run.started", "message.user", "message.assistant", "tool.called", "tool.failed", "message.assistant", "run.completed")
}
