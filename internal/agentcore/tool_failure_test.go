package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/security"
)

func TestSessionToolFailureTheModelCanActOnDoesNotEndTheRun(t *testing.T) {
	rounds, executions := 0, 0
	var secondRound []Message
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		assertClosedToolHistory(t, req.Messages)
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"))
		}
		secondRound = req.Messages
		return streamEvents()
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		executions++
		// Wrapped the way the tools wrap everything, with a path the person must not see in the journal.
		return ToolExecutionResult{}, fmt.Errorf("read: open /private/secret/root/index.html: %w", &ToolFailure{Code: "not_found", Message: "index.html does not exist"})
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, tools, j, security.Ask)
	if err := s.Prompt(context.Background(), "create the page"); err != nil {
		t.Fatalf("a missing file must not end the run: %v", err)
	}
	if rounds != 2 || executions != 1 {
		t.Fatalf("rounds=%d executions=%d: the model must get another turn to adjust", rounds, executions)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.failed", "message.assistant", "run.completed")
	var failed struct {
		ToolCallID, Name, ErrorCode, Error string
		Recoverable                        bool
		Content                            json.RawMessage
	}
	for _, event := range j.snapshot() {
		if strings.Contains(string(event.Data), "/private/secret/root") {
			t.Fatalf("the journal kept a path that came from the underlying error: %s", event.Data)
		}
		if event.Type == "tool.failed" {
			if err := json.Unmarshal(event.Data, &failed); err != nil {
				t.Fatal(err)
			}
		}
	}
	if failed.ToolCallID != "call-1" || failed.Name != "test" || failed.ErrorCode != "not_found" || failed.Error != "index.html does not exist" || !failed.Recoverable {
		t.Fatalf("failed event = %+v", failed)
	}
	// The model reads the failure as the tool's result.
	want := `{"error":"not_found","message":"index.html does not exist"}`
	var tool *Message
	for index := range secondRound {
		if secondRound[index].Role == RoleTool {
			tool = &secondRound[index]
		}
	}
	if tool == nil || tool.ToolCallID != "call-1" || tool.Content != want {
		t.Fatalf("model history = %+v, want %s", secondRound, want)
	}
	assertClosedToolHistory(t, s.messages)
}

func TestSessionRecoverableFailureKeepsTheRestOfTheTurnAndTheCommandOutput(t *testing.T) {
	rounds := 0
	var responses []string
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		assertClosedToolHistory(t, req.Messages)
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"), toolEvent("call-2"))
		}
		for _, message := range req.Messages {
			if message.Role == RoleTool {
				responses = append(responses, message.Content)
			}
		}
		return streamEvents()
	}}
	executions := 0
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		executions++
		if executions == 1 {
			return ToolExecutionResult{Content: json.RawMessage(`{"text":"FAIL: TestX"}`), Details: json.RawMessage(`{"exitCode":1}`)}, &ToolFailure{Code: "exit_status", Message: "the command exited with status 1"}
		}
		return ToolExecutionResult{Content: json.RawMessage(`{"text":"ok"}`)}, nil
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, tools, j, security.Ask)
	if err := s.Prompt(context.Background(), "run the tests twice"); err != nil {
		t.Fatalf("run ended: %v", err)
	}
	if executions != 2 {
		t.Fatalf("a failed call must not skip the next one of the same turn: executions=%d", executions)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.failed", "tool.called", "tool.completed", "message.assistant", "run.completed")
	wantFirst := `{"error":"exit_status","message":"the command exited with status 1","result":{"text":"FAIL: TestX"}}`
	if !reflect.DeepEqual(responses, []string{wantFirst, `{"text":"ok"}`}) {
		t.Fatalf("responses = %v", responses)
	}
	for _, event := range j.snapshot() {
		if event.Type != "tool.failed" {
			continue
		}
		var payload struct {
			Content, Details json.RawMessage
			Recoverable      bool
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil || !payload.Recoverable || string(payload.Content) != `{"text":"FAIL: TestX"}` || string(payload.Details) != `{"exitCode":1}` {
			t.Fatalf("the output of the failed command must stay in the journal: %s (%v)", event.Data, err)
		}
	}
}

func TestSessionFailureMixedWithAnotherErrorStillEndsTheRun(t *testing.T) {
	cases := map[string]error{
		"joined with an unexpected error": errors.Join(&ToolFailure{Code: "not_found", Message: "index.html does not exist"}, errors.New("api-key-supersecret")),
		"code is not an identifier":       &ToolFailure{Code: "Not Found!", Message: "index.html does not exist"},
		"message is empty":                &ToolFailure{Code: "not_found"},
		"message has a control character": &ToolFailure{Code: "not_found", Message: "line one\nline two"},
		"message is too long":             &ToolFailure{Code: "not_found", Message: strings.Repeat("x", maxToolFailureMessageBytes+1)},
	}
	for name, failure := range cases {
		t.Run(name, func(t *testing.T) {
			rounds := 0
			p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
				rounds++
				return streamEvents(toolEvent("call-1"), toolEvent("call-2"))
			}}
			tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
				return ToolExecutionResult{}, failure
			}}
			j := &sessionJournal{}
			s := NewSession("session-1", p, tools, j, security.Ask)
			err := s.Prompt(context.Background(), "execute")
			var ended *RunError
			if !errors.As(err, &ended) || ended.Reason != "tool_failed" || rounds != 1 {
				t.Fatalf("error=%v rounds=%d: anything but a clean ToolFailure must still end the run", err, rounds)
			}
			assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.failed", "tool.skipped", "run.failed")
			for _, event := range j.snapshot() {
				if strings.Contains(string(event.Data), "api-key-supersecret") || strings.Contains(string(event.Data), `"recoverable"`) {
					t.Fatalf("the generic path leaked or claimed recovery: %s", event.Data)
				}
			}
			if s.messages[2].Content != `{"error":"tool_failed"}` {
				t.Fatalf("response = %s", s.messages[2].Content)
			}
		})
	}
}

func TestSessionCancelledToolIsNeverTreatedAsRecoverable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(toolEvent("call-1"))
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		cancel()
		return ToolExecutionResult{}, &ToolFailure{Code: "timeout", Message: "the command ran out of time"}
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, tools, j, security.Ask)
	err := s.Prompt(ctx, "execute")
	var ended *RunError
	if !errors.As(err, &ended) || ended.Reason != "cancelled" {
		t.Fatalf("error=%v: a cancelled run is cancelled, whatever the tool said", err)
	}
	for _, event := range j.snapshot() {
		if event.Type == "tool.failed" && (eventField(t, event, "errorCode") != "cancelled" || strings.Contains(string(event.Data), `"recoverable"`)) {
			t.Fatalf("tool.failed = %s", event.Data)
		}
	}
}

func TestToolFailureResponseBuildsOneShapeFromTheJournaledParts(t *testing.T) {
	cases := []struct {
		name    string
		content json.RawMessage
		want    string
	}{
		{"no result", nil, `{"error":"not_found","message":"x does not exist"}`},
		{"null result", json.RawMessage(`null`), `{"error":"not_found","message":"x does not exist"}`},
		{"blank result", json.RawMessage("  "), `{"error":"not_found","message":"x does not exist"}`},
		{"invalid result is dropped", json.RawMessage(`{broken`), `{"error":"not_found","message":"x does not exist"}`},
		{"partial result is kept", json.RawMessage(` {"text":"partial"} `), `{"error":"not_found","message":"x does not exist","result":{"text":"partial"}}`},
	}
	for _, c := range cases {
		if got := ToolFailureResponse("not_found", "x does not exist", c.content); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestSoleToolFailureSeesThroughWrappingButNotThroughJoins(t *testing.T) {
	failure := &ToolFailure{Code: "not_found", Message: "x does not exist"}
	if got, ok := soleToolFailure(fmt.Errorf("read: %w", fmt.Errorf("again: %w", failure))); !ok || got != failure {
		t.Fatalf("wrapped failure not found: %v %v", got, ok)
	}
	if _, ok := soleToolFailure(errors.Join(failure, errors.New("other"))); ok {
		t.Fatal("a joined failure has a second cause and must not qualify")
	}
	if _, ok := soleToolFailure(errors.New("plain")); ok {
		t.Fatal("a plain error is not a tool failure")
	}
	if _, ok := soleToolFailure(nil); ok {
		t.Fatal("nil is not a tool failure")
	}
}
