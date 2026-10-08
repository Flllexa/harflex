package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/security"
)

type sessionProvider struct {
	stream func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error)
}

func (*sessionProvider) ID() string { return "test" }
func (*sessionProvider) Capabilities() Capabilities {
	return Capabilities{Streaming: true, ToolCalls: true}
}
func (p *sessionProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
	return p.stream(ctx, req)
}

func streamEvents(items ...StreamEvent) (<-chan StreamEvent, <-chan error) {
	out := make(chan StreamEvent, len(items))
	for _, item := range items {
		out <- item
	}
	close(out)
	failures := make(chan error)
	close(failures)
	return out, failures
}

type sessionTools struct {
	risk    string
	execute func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error)
}

func (*sessionTools) Specs() []ToolSpec {
	return []ToolSpec{{Name: "test", Schema: json.RawMessage(`{"type":"object"}`)}}
}
func (t *sessionTools) Risk(string) string { return t.risk }
func (t *sessionTools) Execute(ctx context.Context, name string, args json.RawMessage, sink ToolUpdateSink) (ToolExecutionResult, error) {
	return t.execute(ctx, name, args, sink)
}

type sessionJournal struct {
	mu           sync.Mutex
	items        []events.Event
	failType     string
	failure      error
	attempts     []string
	checkContext bool
	beforeAppend func(context.Context, string)
}

func (j *sessionJournal) Append(ctx context.Context, id, kind, typ string, data any) (events.Event, error) {
	if j.beforeAppend != nil {
		j.beforeAppend(ctx, typ)
	}
	if j.checkContext && ctx.Err() != nil {
		return events.Event{}, ctx.Err()
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if id != "session-1" || kind != "agent_session" {
		return events.Event{}, errors.New("wrong stream")
	}
	j.attempts = append(j.attempts, typ)
	if typ == j.failType {
		return events.Event{}, j.failure
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return events.Event{}, err
	}
	event := events.Event{StreamID: id, Sequence: int64(len(j.items) + 1), Type: typ, Data: raw}
	j.items = append(j.items, event)
	return event, nil
}
func (j *sessionJournal) ListAfter(context.Context, string, int64) ([]events.Event, error) {
	return nil, nil
}
func (j *sessionJournal) snapshot() []events.Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]events.Event(nil), j.items...)
}
func eventTypes(items []events.Event) []string {
	var types []string
	for _, item := range items {
		types = append(types, item.Type)
	}
	return types
}
func assertTypes(t *testing.T, j *sessionJournal, want ...string) {
	t.Helper()
	if got := eventTypes(j.snapshot()); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}
func eventField(t *testing.T, item events.Event, key string) string {
	t.Helper()
	var data map[string]json.RawMessage
	if err := json.Unmarshal(item.Data, &data); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := json.Unmarshal(data[key], &value); err != nil {
		t.Fatalf("field %s in %s: %v", key, item.Data, err)
	}
	return value
}
func approvalID(t *testing.T, j *sessionJournal) string {
	t.Helper()
	items := j.snapshot()
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Type == "approval.requested" {
			return eventField(t, items[i], "approvalId")
		}
	}
	t.Fatal("no approval requested")
	return ""
}

func TestSessionPromptPersistsOrderedResponse(t *testing.T) {
	j := &sessionJournal{}
	provider := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		if len(req.Messages) != 1 || req.Messages[0].Role != RoleUser || req.Messages[0].Content != "Hello" {
			t.Fatalf("request = %#v", req)
		}
		if req.Model != "" || len(req.Tools) != 1 {
			t.Fatalf("request defaults = %#v", req)
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "Hello "}, StreamEvent{Type: "text_delta", Delta: "world"}, StreamEvent{Type: "usage", Usage: &Usage{InputTokens: 4, OutputTokens: 2}})
	}}
	s := NewSession("session-1", provider, &sessionTools{}, j, security.Ask)
	if err := s.Prompt(context.Background(), "Hello"); err != nil {
		t.Fatal(err)
	}
	assertTypes(t, j, "run.started", "message.user", "assistant.delta", "usage.recorded", "message.assistant", "run.completed")
	items := j.snapshot()
	if got := eventField(t, items[2], "delta"); got != "Hello world" {
		t.Fatalf("delta = %q", got)
	}
	if got := eventField(t, items[4], "content"); got != "Hello world" {
		t.Fatalf("assistant = %q", got)
	}
	if s.Cancel() {
		t.Fatal("idle session was cancelled")
	}
}

func TestSessionCarriesFrozenOutputLimitToEveryProviderTurn(t *testing.T) {
	var calls int
	provider := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		calls++
		if req.MaxOutputTokens != 321 {
			t.Errorf("turn %d output limit = %d", calls, req.MaxOutputTokens)
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "ok"})
	}}
	s, err := RestoreSessionWithLimit("session-1", provider, &sessionTools{}, &sessionJournal{}, security.Ask, nil, 321)
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"one", "two"} {
		if err := s.Prompt(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("provider turns = %d", calls)
	}
}

func TestSessionRejectsInvalidInputAndDependencies(t *testing.T) {
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		t.Fatal("provider called")
		return nil, nil
	}}
	for _, tc := range []struct {
		name, id, prompt string
		p                Provider
		tools            ToolExecutor
		journalNil       bool
	}{
		{"empty prompt", "session-1", " \t", p, &sessionTools{}, false},
		{"empty id", "", "hello", p, &sessionTools{}, false},
		{"nil provider", "session-1", "hello", nil, &sessionTools{}, false},
		{"typed nil provider", "session-1", "hello", (*sessionProvider)(nil), &sessionTools{}, false},
		{"nil tools", "session-1", "hello", p, nil, false},
		{"nil journal", "session-1", "hello", p, &sessionTools{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := &sessionJournal{}
			var journal Journal = j
			if tc.journalNil {
				journal = nil
			}
			s := NewSession(tc.id, tc.p, tc.tools, journal, security.Ask)
			if err := s.Prompt(context.Background(), tc.prompt); err == nil {
				t.Fatal("expected error")
			}
			if len(j.snapshot()) != 0 {
				t.Fatal("invalid request persisted")
			}
		})
	}
}

func toolEvent(id string) StreamEvent {
	return StreamEvent{Type: "tool_call", ToolCall: &ToolCall{ID: id, Name: "test", Arguments: json.RawMessage(`{"path":"example.txt"}`)}}
}

func TestSessionReadOnlyToolResumesProvider(t *testing.T) {
	j := &sessionJournal{}
	rounds, executions := 0, 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"))
		}
		if rounds != 2 {
			t.Fatal("unexpected provider round")
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != RoleTool || last.ToolCallID != "call-1" || last.Content != `{"ok":true}` {
			t.Fatalf("tool message = %#v", last)
		}
		if prior := req.Messages[len(req.Messages)-2]; prior.Role != RoleAssistant || len(prior.ToolCalls) != 1 || prior.ToolCalls[0].ID != "call-1" {
			t.Fatalf("tool call message = %#v", prior)
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "Done"})
	}}
	tools := &sessionTools{risk: "read_only", execute: func(ctx context.Context, name string, args json.RawMessage, sink ToolUpdateSink) (ToolExecutionResult, error) {
		executions++
		if name != "test" || string(args) != `{"path":"example.txt"}` {
			t.Fatalf("tool call %s %s", name, args)
		}
		sink(ctx, ToolUpdate{Stream: "stdout", Text: "reading"})
		return ToolExecutionResult{Content: json.RawMessage(`{"ok":true}`)}, nil
	}}
	s := NewSession("session-1", p, tools, j, security.Ask)
	if err := s.Prompt(context.Background(), "Read it"); err != nil {
		t.Fatal(err)
	}
	if executions != 1 || rounds != 2 {
		t.Fatalf("executions=%d rounds=%d", executions, rounds)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.updated", "tool.completed", "assistant.delta", "message.assistant", "run.completed")
}

func TestSessionApprovalAllowsOnceAndResumesQueuedCalls(t *testing.T) {
	j := &sessionJournal{}
	rounds, executions := 0, 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"), toolEvent("call-2"))
		}
		var ids []string
		for _, message := range req.Messages {
			if message.Role == RoleTool {
				ids = append(ids, message.ToolCallID)
			}
		}
		if !reflect.DeepEqual(ids, []string{"call-1", "call-2"}) {
			t.Fatalf("tool results=%v", ids)
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "done"})
	}}
	tools := &sessionTools{risk: "write", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		executions++
		return ToolExecutionResult{Content: json.RawMessage(`{}`)}, nil
	}}
	s := NewSession("session-1", p, tools, j, security.Ask)
	if err := s.Prompt(context.Background(), "Write it"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("prompt error=%v", err)
	}
	firstID := approvalID(t, j)
	if executions != 0 || rounds != 1 {
		t.Fatal("work performed before approval")
	}
	if err := s.Prompt(context.Background(), "another prompt"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("pending prompt=%v", err)
	}
	if s.Cancel() {
		t.Fatal("pending approval is not active execution")
	}
	if err := s.Approve(context.Background(), "unknown", true); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("unknown approval=%v", err)
	}
	if err := s.Approve(context.Background(), firstID, true); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("first approval=%v", err)
	}
	secondID := approvalID(t, j)
	if firstID == secondID || executions != 1 || rounds != 1 {
		t.Fatal("queue was not paused at next approval")
	}
	if err := s.Approve(context.Background(), firstID, true); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("repeated approval=%v", err)
	}
	if err := s.Approve(context.Background(), secondID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Approve(context.Background(), secondID, true); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("repeated final approval=%v", err)
	}
	if executions != 2 || rounds != 2 {
		t.Fatalf("executions=%d rounds=%d", executions, rounds)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "approval.requested", "approval.approved", "tool.called", "tool.completed", "approval.requested", "approval.approved", "tool.called", "tool.completed", "assistant.delta", "message.assistant", "run.completed")
}

func TestSessionApprovalDenialFailsRun(t *testing.T) {
	j := &sessionJournal{}
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(toolEvent("call-1"))
	}}
	tools := &sessionTools{risk: "write", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		t.Fatal("denied tool executed")
		return ToolExecutionResult{}, nil
	}}
	s := NewSession("session-1", p, tools, j, security.Ask)
	if err := s.Prompt(context.Background(), "Write it"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatal(err)
	}
	id := approvalID(t, j)
	if err := s.Approve(context.Background(), id, false); err == nil {
		t.Fatal("denial reported success")
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "approval.requested", "approval.denied", "tool.skipped", "run.failed")
	items := j.snapshot()
	if got := eventField(t, items[len(items)-1], "reason"); got != "approval_denied" {
		t.Fatalf("reason=%s", got)
	}
	var runErr *RunError
	if err := s.Approve(context.Background(), id, false); !errors.Is(err, ErrApprovalNotFound) || errors.As(err, &runErr) {
		t.Fatalf("second denial=%v", err)
	}
	if err := s.Approve(context.Background(), id, true); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("denied approval replay=%v", err)
	}
}

func TestSessionSecurityDeniesUnknownRiskAndUnavailableSandbox(t *testing.T) {
	for _, tc := range []struct {
		name, risk string
		profile    security.Profile
	}{
		{"unknown risk", "WRITE", security.Ask},
		{"sandbox unavailable", "read_only", security.Sandbox},
		{"unknown profile", "read_only", security.Profile("invalid")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := &sessionJournal{}
			rounds := 0
			p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
				rounds++
				if rounds == 1 {
					return streamEvents(toolEvent("call-1"))
				}
				t.Fatal("provider continued after policy denial")
				return nil, nil
			}}
			tools := &sessionTools{risk: tc.risk, execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
				t.Fatal("denied tool executed")
				return ToolExecutionResult{}, nil
			}}
			s := NewSession("session-1", p, tools, j, tc.profile)
			var ended *RunError
			if err := s.Prompt(context.Background(), "Run it"); !errors.As(err, &ended) || ended.Reason != "policy_denied" {
				t.Fatalf("denial=%v", err)
			}
			assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.denied", "run.failed")
			if got := eventField(t, j.snapshot()[3], "reason"); got != "policy_denied" {
				t.Fatalf("reason=%s", got)
			}
		})
	}
}

func awaitSession(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("session did not terminate")
		return nil
	}
}

func TestSessionCancellationAndConcurrentAdmission(t *testing.T) {
	j := &sessionJournal{}
	entered := make(chan struct{})
	var once sync.Once
	p := &sessionProvider{stream: func(ctx context.Context, _ ChatRequest) (<-chan StreamEvent, <-chan error) {
		first := false
		once.Do(func() { first = true; close(entered) })
		if !first {
			return streamEvents()
		}
		return make(chan StreamEvent), nil
	}}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Prompt(ctx, "block") }()
	<-entered
	if err := s.Prompt(ctx, "overlap"); !errors.Is(err, ErrSessionBusy) {
		cancel()
		awaitSession(t, done)
		t.Fatalf("concurrent prompt=%v", err)
	}
	if err := s.Approve(ctx, "anything", true); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("concurrent approval=%v", err)
	}
	if !s.Cancel() {
		t.Fatal("active session did not cancel")
	}
	if s.Cancel() {
		t.Fatal("second cancel was not idempotent")
	}
	if err := awaitSession(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	assertTypes(t, j, "run.started", "message.user", "run.cancelled")
	if s.Cancel() {
		t.Fatal("idle cancellation succeeded")
	}
}

func TestSessionProviderErrorFailsOnceWithoutLeakingErrorText(t *testing.T) {
	failure := errors.New("sensitive provider response")
	j := &sessionJournal{}
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		failures := make(chan error, 2)
		failures <- failure
		failures <- failure
		close(failures)
		return nil, failures
	}}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	var ended *RunError
	if err := s.Prompt(context.Background(), "hello"); !errors.Is(err, failure) || !errors.As(err, &ended) || ended.Reason != "execution_failed" || strings.Contains(err.Error(), failure.Error()) {
		t.Fatalf("error=%v", err)
	}
	assertTypes(t, j, "run.started", "message.user", "run.failed")
	for _, item := range j.snapshot() {
		if strings.Contains(string(item.Data), failure.Error()) {
			t.Fatal("error text was persisted")
		}
	}
}

func TestSessionToolFailurePreservesPriorUpdates(t *testing.T) {
	failure := errors.New("tool failed")
	j := &sessionJournal{}
	rounds := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"))
		}
		t.Fatal("provider continued after tool failure")
		return nil, nil
	}}
	tools := &sessionTools{risk: "read_only", execute: func(ctx context.Context, _ string, _ json.RawMessage, sink ToolUpdateSink) (ToolExecutionResult, error) {
		sink(ctx, ToolUpdate{Stream: "stdout", Text: "partial"})
		return ToolExecutionResult{}, failure
	}}
	s := NewSession("session-1", p, tools, j, security.Ask)
	if err := s.Prompt(context.Background(), "read"); !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.updated", "tool.failed", "run.failed")
	if got := eventField(t, j.snapshot()[5], "error"); got != "tool execution failed" {
		t.Fatalf("persisted error=%q", got)
	}
}

func TestSessionJournalFailureStopsFurtherAppends(t *testing.T) {
	for _, typ := range []string{"run.started", "message.user", "assistant.delta", "usage.recorded", "message.assistant", "tool.called", "tool.updated", "tool.completed", "run.completed", "approval.requested", "approval.approved", "approval.denied"} {
		t.Run(typ, func(t *testing.T) {
			failure := errors.New("journal unavailable")
			j := &sessionJournal{failType: typ, failure: failure}
			rounds, executions := 0, 0
			p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
				rounds++
				if rounds == 1 {
					return streamEvents(StreamEvent{Type: "text_delta", Delta: "working"}, StreamEvent{Type: "usage", Usage: &Usage{InputTokens: 1}}, toolEvent("call-1"))
				}
				return streamEvents(StreamEvent{Type: "text_delta", Delta: "done"})
			}}
			risk := "read_only"
			if strings.HasPrefix(typ, "approval.") {
				risk = "write"
			}
			tools := &sessionTools{risk: risk, execute: func(ctx context.Context, _ string, _ json.RawMessage, sink ToolUpdateSink) (ToolExecutionResult, error) {
				executions++
				sink(ctx, ToolUpdate{Stream: "stdout", Text: "partial"})
				if typ == "tool.updated" && ctx.Err() == nil {
					t.Error("journal failure did not cancel tool context")
				}
				return ToolExecutionResult{Content: json.RawMessage(`{}`)}, ctx.Err()
			}}
			s := NewSession("session-1", p, tools, j, security.Ask)
			err := s.Prompt(context.Background(), "hello")
			if typ == "approval.approved" || typ == "approval.denied" {
				if !errors.Is(err, ErrApprovalRequired) {
					t.Fatalf("prompt=%v", err)
				}
				err = s.Approve(context.Background(), approvalID(t, j), typ == "approval.approved")
			}
			if !errors.Is(err, failure) {
				t.Fatalf("error=%v", err)
			}
			j.mu.Lock()
			attempts := append([]string(nil), j.attempts...)
			j.mu.Unlock()
			if len(attempts) == 0 || attempts[len(attempts)-1] != typ {
				t.Fatalf("appends after failed %s: %v", typ, attempts)
			}
			if strings.HasPrefix(typ, "approval.") && executions != 0 {
				t.Fatal("tool executed without persisted approval")
			}
		})
	}
}

func TestSessionTerminalJournalFailurePreservesBothCauses(t *testing.T) {
	providerFailure := errors.New("provider failure")
	journalFailure := errors.New("journal failure")
	j := &sessionJournal{failType: "run.failed", failure: journalFailure}
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		failures := make(chan error, 1)
		failures <- providerFailure
		close(failures)
		return nil, failures
	}}
	err := NewSession("session-1", p, &sessionTools{}, j, security.Ask).Prompt(context.Background(), "hello")
	if !errors.Is(err, providerFailure) || !errors.Is(err, journalFailure) {
		t.Fatalf("combined error=%v", err)
	}
	if got := j.attempts; !reflect.DeepEqual(got, []string{"run.started", "message.user", "run.failed"}) {
		t.Fatalf("attempts=%v", got)
	}
}

func TestSessionProviderChannelClosurePermutations(t *testing.T) {
	for _, order := range []string{"errors_first", "events_first", "nil_errors", "nil_events"} {
		t.Run(order, func(t *testing.T) {
			producerDone := make(chan struct{})
			p := &sessionProvider{stream: func(ctx context.Context, _ ChatRequest) (<-chan StreamEvent, <-chan error) {
				out := make(chan StreamEvent)
				failures := make(chan error)
				go func() {
					defer close(producerDone)
					send := func() {
						select {
						case out <- StreamEvent{Type: "text_delta", Delta: "exact"}:
						case <-ctx.Done():
						}
					}
					switch order {
					case "errors_first":
						close(failures)
						send()
						close(out)
					case "events_first":
						send()
						close(out)
						close(failures)
					case "nil_errors":
						send()
						close(out)
					case "nil_events":
						close(failures)
					}
				}()
				if order == "nil_errors" {
					return out, nil
				}
				if order == "nil_events" {
					return nil, failures
				}
				return out, failures
			}}
			j := &sessionJournal{}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := NewSession("session-1", p, &sessionTools{}, j, security.Ask).Prompt(ctx, "hello"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-producerDone:
			case <-ctx.Done():
				t.Fatal("producer leaked")
			}
			items := j.snapshot()
			if items[len(items)-1].Type != "run.completed" {
				t.Fatal("missing terminal event")
			}
		})
	}
}

func TestSessionBoundsDeltaAndToolUpdateBatchesWithoutLoss(t *testing.T) {
	text := strings.Repeat("aá🦊", 10000)
	j := &sessionJournal{}
	rounds := 0
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		if rounds == 1 {
			return streamEvents(StreamEvent{Type: "text_delta", Delta: text}, toolEvent("call-1"))
		}
		return streamEvents()
	}}
	tools := &sessionTools{risk: "read_only", execute: func(ctx context.Context, _ string, _ json.RawMessage, sink ToolUpdateSink) (ToolExecutionResult, error) {
		sink(ctx, ToolUpdate{Stream: "stdout", Text: text})
		return ToolExecutionResult{Content: json.RawMessage(`{}`)}, nil
	}}
	if err := NewSession("session-1", p, tools, j, security.Ask).Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	var deltas, updates strings.Builder
	for _, item := range j.snapshot() {
		switch item.Type {
		case "assistant.delta":
			v := eventField(t, item, "delta")
			if len(v) > 16*1024 {
				t.Fatal("unbounded delta")
			}
			deltas.WriteString(v)
		case "tool.updated":
			v := eventField(t, item, "text")
			if len(v) > 16*1024 {
				t.Fatal("unbounded update")
			}
			updates.WriteString(v)
		}
	}
	if deltas.String() != text || updates.String() != text {
		t.Fatal("batching lost data")
	}
}

func TestSessionCancellationFlushesAcceptedDeltaBeforeTerminal(t *testing.T) {
	accepted := make(chan struct{})
	producerDone := make(chan struct{})
	p := &sessionProvider{stream: func(ctx context.Context, _ ChatRequest) (<-chan StreamEvent, <-chan error) {
		out := make(chan StreamEvent)
		go func() {
			defer close(producerDone)
			defer close(out)
			for _, item := range []StreamEvent{{Type: "text_delta", Delta: "partial"}, {Type: "marker"}} {
				select {
				case out <- item:
				case <-ctx.Done():
					return
				}
			}
			close(accepted)
			<-ctx.Done()
		}()
		return out, nil
	}}
	j := &sessionJournal{checkContext: true}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Prompt(ctx, "hello") }()
	select {
	case <-accepted:
	case <-ctx.Done():
		t.Fatal("provider did not send")
	}
	if !s.Cancel() {
		t.Fatal("cancel failed")
	}
	if err := awaitSession(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	select {
	case <-producerDone:
	case <-ctx.Done():
		t.Fatal("producer leaked")
	}
	assertTypes(t, j, "run.started", "message.user", "assistant.delta", "run.cancelled")
	if got := eventField(t, j.snapshot()[2], "delta"); got != "partial" {
		t.Fatalf("delta=%s", got)
	}
}

func TestSessionDeltaFlushesWhileProviderRemainsOpen(t *testing.T) {
	persisted := make(chan struct{})
	out := make(chan StreamEvent, 1)
	out <- StreamEvent{Type: "text_delta", Delta: "visible"}
	j := &sessionJournal{beforeAppend: func(_ context.Context, typ string) {
		if typ == "assistant.delta" {
			close(persisted)
		}
	}}
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) { return out, nil }}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Prompt(ctx, "hello") }()
	select {
	case <-persisted:
	case <-ctx.Done():
		t.Fatal("delta stayed buffered until stream closure")
	}
	close(out)
	if err := awaitSession(t, done); err != nil {
		t.Fatal(err)
	}
	assertTypes(t, j, "run.started", "message.user", "assistant.delta", "message.assistant", "run.completed")
}

func TestSessionToolUpdatesAreSerializedAndUseEffectiveContext(t *testing.T) {
	type key struct{}
	j := &sessionJournal{beforeAppend: func(ctx context.Context, typ string) {
		if typ == "tool.updated" && ctx.Value(key{}) != "effective" {
			t.Error("callback context lost")
		}
	}}
	rounds := 0
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"))
		}
		return streamEvents()
	}}
	tools := &sessionTools{risk: "read_only", execute: func(ctx context.Context, _ string, _ json.RawMessage, sink ToolUpdateSink) (ToolExecutionResult, error) {
		var workers sync.WaitGroup
		for range 16 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				sink(context.WithValue(ctx, key{}, "effective"), ToolUpdate{Stream: "stdout", Text: "x"})
			}()
		}
		workers.Wait()
		return ToolExecutionResult{Content: json.RawMessage(`{}`)}, nil
	}}
	if err := NewSession("session-1", p, tools, j, security.Ask).Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	updates := 0
	for _, item := range j.snapshot() {
		if item.Type == "tool.updated" {
			updates++
		}
	}
	if updates != 16 {
		t.Fatalf("updates=%d", updates)
	}
}

func TestSessionTerminalPersistenceClosesCancellationGate(t *testing.T) {
	terminalEntered, release := make(chan struct{}), make(chan struct{})
	j := &sessionJournal{beforeAppend: func(ctx context.Context, typ string) {
		if typ == "run.completed" {
			close(terminalEntered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}}
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) { return streamEvents() }}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	done := make(chan error, 1)
	go func() { done <- s.Prompt(context.Background(), "hello") }()
	<-terminalEntered
	if s.Cancel() {
		t.Error("cancel accepted after terminal transition")
	}
	if err := s.Prompt(context.Background(), "overlap"); !errors.Is(err, ErrSessionBusy) {
		t.Errorf("overlap=%v", err)
	}
	close(release)
	if err := awaitSession(t, done); err != nil {
		t.Fatal(err)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "run.completed")
}

func TestSessionCancelledToolDoesNotAttemptLateUpdates(t *testing.T) {
	entered := make(chan struct{})
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(toolEvent("call-1"))
	}}
	tools := &sessionTools{risk: "read_only", execute: func(ctx context.Context, _ string, _ json.RawMessage, sink ToolUpdateSink) (ToolExecutionResult, error) {
		close(entered)
		<-ctx.Done()
		sink(ctx, ToolUpdate{Stream: "stdout", Text: "late"})
		return ToolExecutionResult{}, ctx.Err()
	}}
	j := &sessionJournal{checkContext: true}
	s := NewSession("session-1", p, tools, j, security.Ask)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Prompt(ctx, "hello") }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("tool did not execute")
	}
	if !s.Cancel() {
		t.Fatal("cancel failed")
	}
	if err := awaitSession(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.failed", "run.cancelled")
	if got := eventField(t, j.snapshot()[4], "error"); got != "tool execution cancelled" {
		t.Fatalf("persisted error=%q", got)
	}
}

func TestSessionConcurrentApprovalExecutesAtMostOnce(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var executions int
	rounds := 0
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"))
		}
		return streamEvents()
	}}
	tools := &sessionTools{risk: "write", execute: func(ctx context.Context, _ string, _ json.RawMessage, _ ToolUpdateSink) (ToolExecutionResult, error) {
		executions++
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ToolExecutionResult{}, ctx.Err()
		}
		return ToolExecutionResult{Content: json.RawMessage(`{}`)}, nil
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, tools, j, security.Ask)
	if err := s.Prompt(context.Background(), "hello"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatal(err)
	}
	id := approvalID(t, j)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Approve(ctx, id, true) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("approval did not execute")
	}
	if err := s.Approve(ctx, id, true); !errors.Is(err, ErrSessionBusy) {
		t.Errorf("concurrent approval=%v", err)
	}
	close(release)
	if err := awaitSession(t, done); err != nil {
		t.Fatal(err)
	}
	if executions != 1 {
		t.Fatalf("executions=%d", executions)
	}
}

func TestSessionHistorySurvivesProviderMutationAndApprovalContextEnds(t *testing.T) {
	rounds := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		switch rounds {
		case 1:
			return streamEvents(toolEvent("call-1"))
		case 2:
			req.Messages[0].Content = "mutated"
			req.Messages[1].ToolCalls[0].Arguments[0] = '!'
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "done"})
		case 3:
			if req.Messages[0].Content != "original" || !json.Valid(req.Messages[1].ToolCalls[0].Arguments) {
				t.Error("provider mutated private history")
			}
			return streamEvents()
		default:
			t.Fatal("unexpected round")
			return nil, nil
		}
	}}
	tools := &sessionTools{risk: "write", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		return ToolExecutionResult{Content: json.RawMessage(`{}`)}, nil
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, tools, j, security.Ask)
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Prompt(ctx, "original"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatal(err)
	}
	cancel()
	if err := s.Approve(context.Background(), approvalID(t, j), true); err != nil {
		t.Fatal(err)
	}
	if err := s.Prompt(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionJournalFaultPreventsAmbiguousRetry(t *testing.T) {
	failure := errors.New("durability uncertain")
	j := &sessionJournal{failType: "run.completed", failure: failure}
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) { return streamEvents() }}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	if err := s.Prompt(context.Background(), "first"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	before := len(j.attempts)
	if err := s.Prompt(context.Background(), "retry"); !errors.Is(err, failure) {
		t.Fatalf("retry=%v", err)
	}
	if len(j.attempts) != before {
		t.Fatal("faulted session attempted more persistence")
	}
}

func TestSessionToolCompletedPreservesJSONDetailsAndOwnsBuffers(t *testing.T) {
	content := json.RawMessage(`{"ok":true}`)
	details := json.RawMessage(`{"diff":"-old\n+new","exitCode":0,"truncated":false}`)
	wantContent, wantDetails := string(content), string(details)
	rounds := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"))
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != RoleTool || last.Content != wantContent {
			t.Errorf("provider result=%#v", last)
		}
		return streamEvents()
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		return ToolExecutionResult{Content: content, Details: details}, nil
	}}
	j := &sessionJournal{beforeAppend: func(_ context.Context, typ string) {
		if typ == "tool.completed" {
			content[0] = '!'
			details[0] = '!'
		}
	}}
	if err := NewSession("session-1", p, tools, j, security.Ask).Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	for _, item := range j.snapshot() {
		if item.Type != "tool.completed" {
			continue
		}
		var payload struct {
			ToolCallID string          `json:"toolCallId"`
			Name       string          `json:"name"`
			Content    json.RawMessage `json:"content"`
			Details    json.RawMessage `json:"details"`
		}
		if err := json.Unmarshal(item.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.ToolCallID != "call-1" || payload.Name != "test" || string(payload.Content) != wantContent || string(payload.Details) != wantDetails {
			t.Fatalf("tool.completed=%s", item.Data)
		}
		return
	}
	t.Fatal("missing tool.completed")
}

func TestSessionValidatesToolResultJSONAndSupportsLargeReads(t *testing.T) {
	for _, tc := range []struct {
		name             string
		content, details json.RawMessage
		valid            bool
	}{
		{"invalid content", json.RawMessage(`secret-invalid`), nil, false},
		{"invalid details", json.RawMessage(`{}`), json.RawMessage(`secret-invalid`), false},
		{"large content", json.RawMessage(`"` + strings.Repeat("a", 14*1024*1024) + `"`), json.RawMessage(`{"truncated":false}`), true},
		{"oversized content", json.RawMessage(`"` + strings.Repeat("a", 16*1024*1024) + `"`), nil, false},
		{"oversized details", json.RawMessage(`{}`), json.RawMessage(`"` + strings.Repeat("a", 16*1024*1024) + `"`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rounds := 0
			p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
				rounds++
				if rounds == 1 {
					return streamEvents(toolEvent("call-1"))
				}
				if !tc.valid {
					t.Fatal("provider continued after invalid result")
				}
				want := string(tc.content)
				if req.Messages[len(req.Messages)-1].Content != want {
					t.Error("tool result not returned to the model")
				}
				return streamEvents()
			}}
			tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
				return ToolExecutionResult{Content: tc.content, Details: tc.details}, nil
			}}
			j := &sessionJournal{}
			err := NewSession("session-1", p, tools, j, security.Ask).Prompt(context.Background(), "hello")
			if tc.valid {
				if err != nil || rounds != 2 {
					t.Fatalf("valid result: error=%v rounds=%d", err, rounds)
				}
				return
			}
			var ended *RunError
			if !errors.As(err, &ended) || ended.Reason != "invalid_tool_result" || rounds != 1 {
				t.Fatalf("invalid result: error=%v rounds=%d", err, rounds)
			}
			for _, item := range j.snapshot() {
				if strings.Contains(string(item.Data), "secret-invalid") {
					t.Fatal("unsafe result persisted")
				}
			}
			assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.failed", "run.failed")
		})
	}
}

func TestSessionProviderErrorDrainsAlreadyProducedEvents(t *testing.T) {
	failure := errors.New("provider interrupted")
	for range 100 {
		out := make(chan StreamEvent, 4)
		out <- StreamEvent{Type: "text_delta", Delta: "accepted "}
		out <- StreamEvent{Type: "text_delta", Delta: "text"}
		out <- StreamEvent{Type: "usage", Usage: &Usage{InputTokens: 3, OutputTokens: 2}}
		out <- toolEvent("call-1")
		close(out)
		failures := make(chan error, 1)
		failures <- failure
		close(failures)
		p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) { return out, failures }}
		j := &sessionJournal{}
		tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
			t.Fatal("tool executed after provider failed")
			return ToolExecutionResult{}, nil
		}}
		if err := NewSession("session-1", p, tools, j, security.Ask).Prompt(context.Background(), "hello"); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		assertTypes(t, j, "run.started", "message.user", "assistant.delta", "usage.recorded", "message.assistant", "tool.skipped", "run.failed")
		items := j.snapshot()
		if eventField(t, items[2], "delta") != "accepted text" {
			t.Fatal("lost accepted delta")
		}
		var message Message
		if err := json.Unmarshal(items[4].Data, &message); err != nil {
			t.Fatal(err)
		}
		if len(message.ToolCalls) != 1 || message.ToolCalls[0].ID != "call-1" {
			t.Fatal("lost accepted tool call")
		}
	}
}

func TestSessionProviderErrorDrainStillHonorsCancellation(t *testing.T) {
	failure := errors.New("provider failed but left events open")
	failures := make(chan error, 1)
	failures <- failure
	close(failures)
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return make(chan StreamEvent), failures
	}}
	j := &sessionJournal{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := NewSession("session-1", p, &sessionTools{}, j, security.Ask).Prompt(ctx, "hello")
	if !errors.Is(err, failure) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error=%v", err)
	}
	assertTypes(t, j, "run.started", "message.user", "run.cancelled")
}

func TestSessionAcceptedPersistenceSurvivesCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	type valueKey struct{}
	j := &sessionJournal{checkContext: true, beforeAppend: func(ctx context.Context, typ string) {
		if typ == "assistant.delta" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
			if ctx.Value(valueKey{}) != "retained" {
				t.Error("persistence lost context value")
			}
		}
	}}
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "accepted"})
	}}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), valueKey{}, "retained"), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Prompt(ctx, "hello") }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("append did not start")
	}
	if !s.Cancel() {
		t.Fatal("cancel failed")
	}
	close(release)
	if err := awaitSession(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	items := j.snapshot()
	if len(items) < 4 || items[2].Type != "assistant.delta" || items[len(items)-1].Type != "run.cancelled" {
		t.Fatalf("events=%v", eventTypes(items))
	}
	s.mu.Lock()
	fault := s.fault
	s.mu.Unlock()
	if fault != nil {
		t.Fatalf("cancellation sealed session: %v", fault)
	}
}

func TestSessionPersistenceTimeoutSealsWithoutClaimingTerminal(t *testing.T) {
	var deadlineDuration time.Duration
	j := &sessionJournal{checkContext: true, beforeAppend: func(ctx context.Context, typ string) {
		if typ == "assistant.delta" {
			deadline, ok := ctx.Deadline()
			if ok {
				deadlineDuration = time.Until(deadline)
			}
			<-ctx.Done()
		}
	}}
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "accepted"})
	}}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	err := s.Prompt(ctx, "hello")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout=%v", err)
	}
	if deadlineDuration <= 0 || deadlineDuration > 2100*time.Millisecond {
		t.Fatalf("persistence deadline=%v, want bounded 2s", deadlineDuration)
	}
	assertTypes(t, j, "run.started", "message.user")
	if err := s.Prompt(context.Background(), "retry"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sealed retry=%v", err)
	}
}

func TestSessionCancellationDuringToolAdmissionPreventsExecution(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	j := &sessionJournal{checkContext: true, beforeAppend: func(ctx context.Context, typ string) {
		if typ == "tool.called" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}}
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(toolEvent("call-1"))
	}}
	executed := false
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		executed = true
		return ToolExecutionResult{Content: json.RawMessage(`{}`)}, nil
	}}
	s := NewSession("session-1", p, tools, j, security.Ask)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Prompt(ctx, "hello") }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("tool admission did not start")
	}
	if !s.Cancel() {
		t.Fatal("cancel failed")
	}
	close(release)
	if err := awaitSession(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if executed {
		t.Fatal("tool executed after cancellation during admission")
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.skipped", "run.cancelled")
}

func TestSessionDrainsMultipleUnbufferedErrorsAndIgnoresNil(t *testing.T) {
	firstErr, secondErr := errors.New("first provider error"), errors.New("second provider error")
	producerDone := make(chan struct{})
	p := &sessionProvider{stream: func(ctx context.Context, _ ChatRequest) (<-chan StreamEvent, <-chan error) {
		out := make(chan StreamEvent)
		failures := make(chan error)
		go func() {
			defer close(producerDone)
			defer close(out)
			defer close(failures)
			for i, err := range []error{nil, firstErr, secondErr} {
				select {
				case failures <- err:
				case <-ctx.Done():
					return
				}
				if i == 1 {
					select {
					case out <- StreamEvent{Type: "text_delta", Delta: "between errors"}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
		return out, failures
	}}
	j := &sessionJournal{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := NewSession("session-1", p, &sessionTools{}, j, security.Ask).Prompt(ctx, "hello")
	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("provider producer leaked")
	}
	if !errors.Is(err, firstErr) || errors.Is(err, secondErr) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error=%v", err)
	}
	assertTypes(t, j, "run.started", "message.user", "assistant.delta", "message.assistant", "run.failed")
	if eventField(t, j.snapshot()[2], "delta") != "between errors" {
		t.Fatal("intermediate event lost")
	}
}

func TestSessionIgnoresNilProviderErrorWithoutFailingRun(t *testing.T) {
	out := make(chan StreamEvent)
	close(out)
	failures := make(chan error, 1)
	failures <- nil
	close(failures)
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) { return out, failures }}
	j := &sessionJournal{}
	if err := NewSession("session-1", p, &sessionTools{}, j, security.Ask).Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "run.completed")
}

func TestSessionRecordsToolEffectWhenCancelledAfterExecution(t *testing.T) {
	var s *Session
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(toolEvent("call-1"))
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		if !s.Cancel() {
			t.Error("cancel failed")
		}
		return ToolExecutionResult{Content: json.RawMessage(`{"written":true}`)}, nil
	}}
	j := &sessionJournal{}
	s = NewSession("session-1", p, tools, j, security.Ask)
	err := s.Prompt(context.Background(), "write")
	var runErr *RunError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &runErr) || runErr.Reason != "cancelled" {
		t.Fatalf("error=%v", err)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.called", "tool.completed", "run.cancelled")
}

func TestSessionKeepsValidHistoryAfterFailedRun(t *testing.T) {
	rounds := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"), toolEvent("call-2"))
		}
		var roles []Role
		for _, message := range req.Messages {
			roles = append(roles, message.Role)
		}
		if !reflect.DeepEqual(roles, []Role{RoleUser, RoleAssistant, RoleTool, RoleTool, RoleUser}) {
			t.Fatalf("roles=%v", roles)
		}
		for i, message := range req.Messages[2:4] {
			want := []string{`{"error":"approval_denied"}`, `{"error":"skipped_after_failure"}`}[i]
			if message.Content != want {
				t.Fatalf("skipped result=%#v", message)
			}
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "ok"})
	}}
	tools := &sessionTools{risk: "write", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		t.Fatal("denied tool executed")
		return ToolExecutionResult{}, nil
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, tools, j, security.Ask)
	if err := s.Prompt(context.Background(), "write both"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatal(err)
	}
	var runErr *RunError
	if err := s.Approve(context.Background(), approvalID(t, j), false); !errors.As(err, &runErr) || runErr.Reason != "approval_denied" {
		t.Fatalf("denial=%v", err)
	}
	if err := s.Prompt(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionAnswersUnknownToolWithoutApproval(t *testing.T) {
	rounds := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		if rounds == 1 {
			return streamEvents(StreamEvent{Type: "tool_call", ToolCall: &ToolCall{ID: "call-1", Name: "rm_rf", Arguments: json.RawMessage(`{}`)}})
		}
		t.Fatal("provider continued after unknown tool")
		return nil, nil
	}}
	tools := &sessionTools{risk: "destructive", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		t.Fatal("unknown tool executed")
		return ToolExecutionResult{}, nil
	}}
	j := &sessionJournal{}
	var ended *RunError
	if err := NewSession("session-1", p, tools, j, security.Ask).Prompt(context.Background(), "hello"); !errors.As(err, &ended) || ended.Reason != "unknown_tool" {
		t.Fatalf("unknown tool=%v", err)
	}
	assertTypes(t, j, "run.started", "message.user", "message.assistant", "tool.denied", "run.failed")
	if got := eventField(t, j.snapshot()[3], "reason"); got != "unknown_tool" {
		t.Fatalf("reason=%s", got)
	}
}

func TestSessionBoundsProviderTurnsPerRun(t *testing.T) {
	rounds := 0
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		rounds++
		return streamEvents(toolEvent(fmt.Sprintf("call-%d", rounds)))
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		return ToolExecutionResult{Content: json.RawMessage(`{}`)}, nil
	}}
	j := &sessionJournal{}
	err := NewSession("session-1", p, tools, j, security.Ask).Prompt(context.Background(), "loop")
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Reason != "turn_limit" || rounds != maxTurns {
		t.Fatalf("error=%v rounds=%d", err, rounds)
	}
	items := j.snapshot()
	if last := items[len(items)-1]; last.Type != "run.failed" || eventField(t, last, "reason") != "turn_limit" {
		t.Fatalf("terminal=%s %s", last.Type, last.Data)
	}
}

func TestSessionApprovalRequiredCarriesStoredCall(t *testing.T) {
	p := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(toolEvent("call-1"))
	}}
	j := &sessionJournal{}
	err := NewSession("session-1", p, &sessionTools{risk: "write"}, j, security.Ask).Prompt(context.Background(), "write")
	var required *ApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v", err)
	}
	want := ApprovalRequest{ID: approvalID(t, j), ToolCallID: "call-1", Name: "test", Risk: "write", Arguments: json.RawMessage(`{"path":"example.txt"}`)}
	if !reflect.DeepEqual(required.Approval, want) {
		t.Fatalf("approval=%#v", required.Approval)
	}
	var persisted ApprovalRequest
	if err := json.Unmarshal(j.snapshot()[3].Data, &persisted); err != nil || !reflect.DeepEqual(persisted, want) {
		t.Fatalf("persisted=%#v err=%v", persisted, err)
	}
}
