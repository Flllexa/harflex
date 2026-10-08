package externalagent

import (
	"context"
	"errors"
	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/events"
	"strings"
	"testing"
)

type continuityAdapter struct {
	requests  []Request
	resumable bool
	beforeRun func()
}

func (a *continuityAdapter) ID() string        { return "opencode" }
func (a *continuityAdapter) Detect() Detection { return Detection{Available: true} }
func (a *continuityAdapter) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Resumable: a.resumable}
}
func (a *continuityAdapter) Run(_ context.Context, r Request) (<-chan Event, <-chan error) {
	if a.beforeRun != nil {
		a.beforeRun()
	}
	a.requests = append(a.requests, r)
	es := make(chan Event, 1)
	es <- normalize([]byte(`{"type":"text","text":"answer","sessionID":"ses_123"}`))
	close(es)
	errs := make(chan error)
	close(errs)
	return es, errs
}
func TestSessionPersistsPromptAndResumesCapturedID(t *testing.T) {
	a := &continuityAdapter{resumable: true}
	r := &recording{}
	a.beforeRun = func() {
		n := len(r.types)
		if n < 2 || r.types[n-2] != "external.run.started" || r.types[n-1] != "message.user" {
			t.Fatal("process started before durable admission", r.types)
		}
	}
	s := NewSession("session", a, r, Request{CWD: t.TempDir()})
	if err := s.Prompt(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	if r.types[0] != "external.run.started" || r.types[1] != "message.user" {
		t.Fatal(r.types)
	}
	if err := s.Prompt(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
	if len(a.requests) != 2 || a.requests[1].SessionID != "ses_123" {
		t.Fatal(a.requests)
	}
	args, _ := NewOpenCode("").(*cliAdapter).build(a.requests[1])
	if !strings.Contains(strings.Join(args, " "), "--session ses_123") {
		t.Fatal(args)
	}
}

func TestAbortBeforePromptFencesExternalProcess(t *testing.T) {
	a := &continuityAdapter{resumable: true}
	r := &recording{}
	s := NewSession("session", a, r, Request{CWD: t.TempDir()})
	if err := s.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Prompt(t.Context(), "late work"); !errors.Is(err, context.Canceled) {
		t.Fatalf("late prompt accepted: %v", err)
	}
	if len(a.requests) != 0 || len(r.types) != 1 || r.types[0] != "external.run.cancelled" {
		t.Fatalf("late process or missing terminal: %v %v", a.requests, r.types)
	}
}

func TestRejectedAbortAfterCompletionPreservesExternalSession(t *testing.T) {
	a := &continuityAdapter{resumable: true}
	r := &recording{}
	s := NewSession("session", a, r, Request{CWD: t.TempDir()})
	if err := s.Prompt(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	if err := s.Abort(t.Context()); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("completed run was cancelled: %v", err)
	}
	if err := s.Prompt(t.Context(), "second"); err != nil {
		t.Fatalf("rejected abort sealed completed session: %v", err)
	}
	if len(a.requests) != 2 {
		t.Fatalf("unexpected process count: %d", len(a.requests))
	}
}

type failingAdmissionRecorder struct {
	recording
	promptErr, terminalErr error
}

func (r *failingAdmissionRecorder) Append(ctx context.Context, id, kind, typ string, data any) (events.Event, error) {
	if typ == "message.user" {
		return events.Event{}, r.promptErr
	}
	if typ == "external.run.failed" {
		return events.Event{}, r.terminalErr
	}
	return r.recording.Append(ctx, id, kind, typ, data)
}
func TestAdmissionAndTerminalFailureSealWithSafeJoinedCauses(t *testing.T) {
	a := &continuityAdapter{resumable: true}
	r := &failingAdmissionRecorder{promptErr: errors.New("private-prompt-error"), terminalErr: errors.New("private-terminal-error")}
	s := NewSession("session", a, r, Request{CWD: t.TempDir()})
	err := s.Prompt(t.Context(), "private prompt")
	if !errors.Is(err, r.promptErr) || !errors.Is(err, r.terminalErr) || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	if len(r.types) != 1 || r.types[0] != "external.run.started" || len(a.requests) != 0 {
		t.Fatal(r.types, a.requests)
	}
	if next := s.Prompt(t.Context(), "again"); !errors.Is(next, r.terminalErr) || strings.Contains(next.Error(), "private") || len(r.types) != 1 {
		t.Fatal(next)
	}
}

func TestPromptAdmissionFailureClosesStartedRunBeforeAnyProcess(t *testing.T) {
	a := &continuityAdapter{resumable: true}
	r := &recording{failAt: "message.user"}
	s := NewSession("session", a, r, Request{CWD: t.TempDir()})
	if err := s.Prompt(t.Context(), "private prompt"); err == nil || strings.Contains(err.Error(), "recorder failed") {
		t.Fatal("unsafe or missing error", err)
	}
	if len(r.types) != 2 || r.types[0] != "external.run.started" || r.types[1] != "external.run.failed" || len(a.requests) != 0 {
		t.Fatal(r.types, a.requests)
	}
	n := len(r.types)
	if err := s.Prompt(t.Context(), "again"); err == nil || len(r.types) != n {
		t.Fatal("faulted admission continued", err)
	}
}
func TestSessionNonResumableRefusesSecondPrompt(t *testing.T) {
	a := &continuityAdapter{}
	r := &recording{}
	s := NewSession("session", a, r, Request{CWD: t.TempDir()})
	if err := s.Prompt(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	n := len(r.types)
	if err := s.Prompt(t.Context(), "second"); !errors.Is(err, ErrNotResumable) {
		t.Fatal(err)
	}
	if len(r.types) != n || len(a.requests) != 1 {
		t.Fatal("refused prompt started work")
	}
}
func TestNormalizeSessionIDIsConservative(t *testing.T) {
	for _, raw := range []string{`{"sessionID":"ses_1"}`, `{"session_id":"ses_1"}`, `{"session":{"id":"ses_1"}}`} {
		e := normalize([]byte(raw))
		if e.SessionID != "ses_1" || string(e.Raw) != raw {
			t.Fatal(e)
		}
	}
	for _, raw := range []string{`{"sessionID":123}`, `{"session_id":"../private/path"}`, `{"session":{"id":true}}`, `{"sessionID":"` + strings.Repeat("a", 257) + `"}`} {
		if e := normalize([]byte(raw)); e.SessionID != "" {
			t.Fatal(e)
		}
	}
}
