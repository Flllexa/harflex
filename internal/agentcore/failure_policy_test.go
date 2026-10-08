package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/security"
)

func assertClosedToolHistory(t *testing.T, messages []Message) {
	t.Helper()
	var pending []ToolCall
	for _, message := range messages {
		if message.Role == RoleTool {
			if len(pending) == 0 || pending[0].ID != message.ToolCallID {
				t.Fatalf("unexpected tool response: %#v pending=%#v", message, pending)
			}
			pending = pending[1:]
			continue
		}
		if len(pending) != 0 {
			t.Fatalf("unanswered calls before %s: %#v", message.Role, pending)
		}
		pending = message.ToolCalls
	}
	if len(pending) != 0 {
		t.Fatalf("unanswered calls: %#v", pending)
	}
}

func TestSessionPolicyDenialStopsAndKeepsInProcessContinuity(t *testing.T) {
	rounds := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		assertClosedToolHistory(t, req.Messages)
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"), toolEvent("call-2"))
		}
		return streamEvents()
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, &sessionTools{risk: "invalid"}, j, security.Ask)
	err := s.Prompt(context.Background(), "deny")
	var ended *RunError
	if !errors.As(err, &ended) || ended.Reason != "policy_denied" || rounds != 1 {
		t.Fatalf("error=%v rounds=%d", err, rounds)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.denied", "tool.skipped", "run.failed")
	if s.messages[2].Content != `{"error":"policy_denied"}` || s.messages[3].Content != `{"error":"skipped_after_failure"}` {
		t.Fatalf("tool responses=%#v", s.messages[2:])
	}
	if err := s.Prompt(context.Background(), "continue"); err != nil || rounds != 2 {
		t.Fatalf("next prompt=%v rounds=%d", err, rounds)
	}
}

func TestSessionToolFailurePreservesSafePartialResultAndCause(t *testing.T) {
	failure := errors.New("api-key-supersecret")
	content := json.RawMessage(`{"stdout":"partial output","stderr":"command failed"}`)
	details := json.RawMessage(`{"diff":"-old\n+new","exitCode":1}`)
	wantContent, wantDetails := string(content), string(details)
	rounds, executions := 0, 0
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		return streamEvents(toolEvent("call-1"), toolEvent("call-2"))
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		executions++
		return ToolExecutionResult{Content: content, Details: details}, failure
	}}
	j := &sessionJournal{beforeAppend: func(_ context.Context, typ string) {
		if typ == "tool.failed" {
			content[0], details[0] = '!', '!'
		}
	}}
	s := NewSession("session-1", p, tools, j, security.Ask)
	err := s.Prompt(context.Background(), "execute")
	var ended *RunError
	if !errors.Is(err, failure) || !errors.As(err, &ended) || ended.Reason != "tool_failed" || rounds != 1 || executions != 1 {
		t.Fatalf("error=%v rounds=%d executions=%d", err, rounds, executions)
	}
	if strings.Contains(err.Error(), failure.Error()) {
		t.Fatal("returned error leaked executor diagnostics")
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.failed", "tool.skipped", "run.failed")
	for _, event := range j.snapshot() {
		if strings.Contains(string(event.Data), failure.Error()) {
			t.Fatal("journal leaked executor diagnostics")
		}
		if event.Type == "tool.failed" {
			var payload struct {
				Content, Details            json.RawMessage
				ErrorCode, ToolCallID, Name string
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if string(payload.Content) != wantContent || string(payload.Details) != wantDetails || payload.ErrorCode != "tool_failed" || payload.ToolCallID != "call-1" || payload.Name != "test" {
				t.Fatalf("partial result lost: %s", event.Data)
			}
		}
	}
	assertClosedToolHistory(t, s.messages)
	if s.messages[2].Content != `{"error":"tool_failed"}` || s.messages[3].Content != `{"error":"skipped_after_failure"}` {
		t.Fatalf("responses=%#v", s.messages[2:])
	}
}

func TestSessionRepeatedCallIDClosesEachTurnOccurrence(t *testing.T) {
	rounds := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		assertClosedToolHistory(t, req.Messages)
		if rounds <= 2 {
			return streamEvents(toolEvent("call-1"))
		}
		return streamEvents()
	}}
	executions := 0
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		executions++
		return ToolExecutionResult{Content: json.RawMessage(`{}`)}, nil
	}}
	j := &sessionJournal{}
	// The second occurrence awaits approval after the first occurrence succeeded.
	j.beforeAppend = func(_ context.Context, typ string) {
		if typ == "tool.completed" {
			tools.risk = "write"
		}
	}
	s := NewSession("session-1", p, tools, j, security.Ask)
	if err := s.Prompt(context.Background(), "two turns"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("approval=%v", err)
	}
	if err := s.Approve(context.Background(), approvalID(t, j), false); !errors.Is(err, errApprovalDenied) {
		t.Fatalf("denial=%v", err)
	}
	assertClosedToolHistory(t, s.messages)
	if len(s.messages) != 5 || s.messages[4].Content != `{"error":"approval_denied"}` {
		t.Fatalf("messages=%#v", s.messages)
	}
	if err := s.Prompt(context.Background(), "next prompt"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionRepeatedCallIDAcrossPromptsHasOneResponsePerOccurrence(t *testing.T) {
	rounds := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		assertClosedToolHistory(t, req.Messages)
		if rounds <= 2 {
			return streamEvents(toolEvent("call-1"))
		}
		return streamEvents()
	}}
	s := NewSession("session-1", p, &sessionTools{risk: "invalid"}, &sessionJournal{}, security.Ask)
	for range 2 {
		if err := s.Prompt(context.Background(), "deny"); err == nil {
			t.Fatal("denial completed")
		}
		assertClosedToolHistory(t, s.messages)
	}
	if err := s.Prompt(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	responses := 0
	for _, message := range s.messages {
		if message.Role == RoleTool {
			responses++
		}
	}
	if responses != 2 {
		t.Fatalf("responses=%d", responses)
	}
}

func TestSessionCancellationClosesRemainingCallsBeforeNextPrompt(t *testing.T) {
	rounds, executions := 0, 0
	var s *Session
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		assertClosedToolHistory(t, req.Messages)
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"), toolEvent("call-2"))
		}
		return streamEvents()
	}}
	tools := &sessionTools{risk: "read_only", execute: func(ctx context.Context, _ string, _ json.RawMessage, _ ToolUpdateSink) (ToolExecutionResult, error) {
		executions++
		if !s.Cancel() {
			t.Fatal("cancel failed")
		}
		return ToolExecutionResult{Content: json.RawMessage(`{"stdout":"partial"}`)}, ctx.Err()
	}}
	j := &sessionJournal{}
	s = NewSession("session-1", p, tools, j, security.Ask)
	err := s.Prompt(context.Background(), "cancel")
	var ended *RunError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &ended) || ended.Reason != "cancelled" || executions != 1 || rounds != 1 {
		t.Fatalf("err=%v executions=%d rounds=%d", err, executions, rounds)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.failed", "tool.skipped", "run.cancelled")
	for _, message := range s.messages[2:] {
		if message.Content != `{"error":"cancelled"}` {
			t.Fatalf("response=%#v", message)
		}
	}
	if err := s.Prompt(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionToolFailureJournalFaultSealsAndPreservesCause(t *testing.T) {
	toolFailure, journalFailure := errors.New("api-key-supersecret"), errors.New("journal unavailable")
	rounds := 0
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		return streamEvents(toolEvent("call-1"), toolEvent("call-2"))
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		return ToolExecutionResult{}, toolFailure
	}}
	j := &sessionJournal{failType: "tool.failed", failure: journalFailure}
	s := NewSession("session-1", p, tools, j, security.Ask)
	err := s.Prompt(context.Background(), "run")
	if !errors.Is(err, toolFailure) || !errors.Is(err, journalFailure) || strings.Contains(err.Error(), toolFailure.Error()) {
		t.Fatalf("error=%v", err)
	}
	attempts := len(j.attempts)
	if err := s.Prompt(context.Background(), "retry"); !errors.Is(err, journalFailure) {
		t.Fatalf("retry=%v", err)
	}
	if rounds != 1 || len(j.attempts) != attempts {
		t.Fatal("journal fault did not seal session")
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called")
}

func TestSessionInvalidUTF8ToolEvidenceIsDiscarded(t *testing.T) {
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(toolEvent("call-1"))
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		return ToolExecutionResult{Content: json.RawMessage{'"', 0xff, '"'}, Details: json.RawMessage(`{"exitCode":1}`)}, errors.New("failure")
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, tools, j, security.Ask)
	if err := s.Prompt(context.Background(), "invalid"); err == nil {
		t.Fatal("invalid result completed")
	}
	var payload ToolExecutionResult
	if err := json.Unmarshal(j.snapshot()[4].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload.Content) != "null" || string(payload.Details) != `{"exitCode":1}` {
		t.Fatalf("unsafe evidence=%s / %s", payload.Content, payload.Details)
	}
}

func TestSessionJournalFailureDoesNotClaimDurableFailedOutcome(t *testing.T) {
	for _, failType := range []string{"tool.skipped", "run.failed"} {
		t.Run(failType, func(t *testing.T) {
			toolFailure, journalFailure := errors.New("api-key-supersecret"), errors.New("journal unavailable")
			p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
				return streamEvents(toolEvent("call-1"), toolEvent("call-2"))
			}}
			tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
				return ToolExecutionResult{}, toolFailure
			}}
			j := &sessionJournal{failType: failType, failure: journalFailure}
			s := NewSession("session-1", p, tools, j, security.Ask)
			err := s.Prompt(context.Background(), "fail")
			var ended *RunError
			if errors.As(err, &ended) || !errors.Is(err, journalFailure) || !errors.Is(err, toolFailure) || strings.Contains(err.Error(), toolFailure.Error()) {
				t.Fatalf("unconfirmed terminal outcome: %v", err)
			}
		})
	}
}

func TestSessionProviderAndJournalFailuresKeepDiagnosticsPrivate(t *testing.T) {
	for _, failType := range []string{"run.failed", "tool.skipped", "message.assistant"} {
		t.Run(failType, func(t *testing.T) {
			providerFailure := errors.New("provider-secret-api-key")
			journalFailure := errors.New("journal-secret-api-key")
			p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
				items := make(chan StreamEvent, 1)
				items <- toolEvent("call-1")
				close(items)
				failures := make(chan error, 1)
				failures <- providerFailure
				close(failures)
				return items, failures
			}}
			j := &sessionJournal{failType: failType, failure: journalFailure}
			s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
			check := func(err error) {
				var ended *RunError
				var persistenceFailure *journalError
				if err == nil || strings.Contains(err.Error(), providerFailure.Error()) || strings.Contains(err.Error(), journalFailure.Error()) {
					t.Fatalf("diagnostics leaked: %v", err)
				}
				if !errors.Is(err, providerFailure) || !errors.Is(err, journalFailure) || !errors.As(err, &persistenceFailure) || errors.As(err, &ended) {
					t.Fatalf("causes or durable status incorrect: %v", err)
				}
			}
			check(s.Prompt(context.Background(), "fail"))
			attempts := len(j.attempts)
			check(s.Prompt(context.Background(), "retry"))
			if len(j.attempts) != attempts {
				t.Fatal("journal fault did not seal session")
			}
			for _, event := range j.snapshot() {
				if event.Type == "run.failed" || event.Type == "run.completed" {
					t.Fatalf("unconfirmed terminal event: %s", event.Type)
				}
			}
		})
	}
}
