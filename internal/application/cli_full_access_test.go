package application

import (
	"context"
	"sync"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/externalagent"
)

// requestRecorder is a resumable CLI that records what each run asked for and binds a conversation id, as Claude Code does.
type requestRecorder struct {
	mu       sync.Mutex
	requests []externalagent.Request
}

func (*requestRecorder) ID() string { return "claude" }
func (*requestRecorder) Detect() externalagent.Detection {
	return externalagent.Detection{Available: true, Path: "/private/claude"}
}
func (*requestRecorder) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, Resumable: true}
}
func (r *requestRecorder) Run(_ context.Context, request externalagent.Request) (<-chan externalagent.Event, <-chan error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	r.mu.Unlock()
	events, errs := make(chan externalagent.Event, 1), make(chan error)
	events <- externalagent.Event{Type: "assistant.message", Text: "ok", SessionID: "0b6f3c3e-1111-4c1f-9e0a-000000000000", MessageID: "m", Mode: "replace"}
	close(events)
	close(errs)
	return events, errs
}

func TestCLIChatFollowsTheProjectProfileFromOneMessageToTheNext(t *testing.T) {
	s, _, _ := setup(t)
	recorder := &requestRecorder{}
	s.external["claude"] = recorder
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "claude", Reason: "Conversa"})
	if err != nil {
		t.Fatal(err)
	}
	prompt := func(text string) {
		t.Helper()
		if result, err := s.Prompt(PromptInput{SessionID: chat.ID, Text: text}); err != nil || result.Status != RunCompleted {
			t.Fatalf("prompt %q: %+v %v", text, result, err)
		}
	}
	prompt("primeira")
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "full_access", ConfirmFullAccess: true}); err != nil {
		t.Fatal(err)
	}
	prompt("segunda")
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "ask"}); err != nil {
		t.Fatal(err)
	}
	prompt("terceira")
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.requests) != 3 {
		t.Fatalf("runs: %d", len(recorder.requests))
	}
	for i, want := range []bool{false, true, false} {
		if recorder.requests[i].FullAccess != want {
			t.Fatalf("run %d: FullAccess = %v, want %v", i+1, recorder.requests[i].FullAccess, want)
		}
	}
}

func TestCLIChatOpenedOnAFullAccessProjectStartsWithFullAccess(t *testing.T) {
	s, _, _ := setup(t)
	recorder := &requestRecorder{}
	s.external["claude"] = recorder
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "full_access", ConfirmFullAccess: true}); err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "claude", Reason: "Conversa"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: chat.ID, Text: "oi"}); err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.requests) != 1 || !recorder.requests[0].FullAccess {
		t.Fatalf("requests: %+v", recorder.requests)
	}
}
