package application

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func commandEvents(t *testing.T, s *Service) (func() []ChatCommandEventDTO, func(string) ChatCommandEventDTO) {
	t.Helper()
	var mu sync.Mutex
	var got []ChatCommandEventDTO
	SetEmitter(s, func(name string, payload any) {
		if name == "harflex:command" {
			mu.Lock()
			got = append(got, payload.(ChatCommandEventDTO))
			mu.Unlock()
		}
	})
	all := func() []ChatCommandEventDTO {
		mu.Lock()
		defer mu.Unlock()
		return append([]ChatCommandEventDTO(nil), got...)
	}
	end := func(runID string) ChatCommandEventDTO {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			for _, event := range all() {
				if event.ID == runID && event.Done {
					return event
				}
			}
		}
		t.Fatalf("the command never ended: %+v", all())
		return ChatCommandEventDTO{}
	}
	return all, end
}

func TestChatCommandRunsInTheProjectStreamsAndReportsItsEnd(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	all, end := commandEvents(t, s)
	runID, err := s.RunChatCommand(RunChatCommandInput{SessionID: session.ID, Command: "pwd; echo saída; echo aviso 1>&2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	done := end(runID)
	if done.ExitCode != 3 || done.Cancelled || !strings.Contains(done.Output, "saída") || !strings.Contains(done.Output, "aviso") || done.SessionID != session.ID {
		t.Fatalf("end: %+v", done)
	}
	workspace, _ := s.store.GetWorkspace(s.ctx, session.WorkspaceID)
	if !strings.Contains(done.Output, workspace.Path) && !strings.Contains(done.Output, strings.TrimPrefix(workspace.Path, "/private")) {
		t.Fatalf("it did not run in the project folder: %q (project %s)", done.Output, workspace.Path)
	}
	streamed := ""
	for _, event := range all() {
		if event.ID == runID && !event.Done {
			streamed += event.Text
		}
	}
	if !strings.Contains(streamed, "saída") {
		t.Fatalf("the output did not stream: %q", streamed)
	}
}

func TestChatCommandCanBeCancelledAndRejectsBadInput(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	_, end := commandEvents(t, s)
	runID, err := s.RunChatCommand(RunChatCommandInput{SessionID: session.ID, Command: "echo começou; sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := s.CancelChatCommand(runID); err != nil {
		t.Fatal(err)
	}
	if done := end(runID); !done.Cancelled || done.DurationMs > 10000 {
		t.Fatalf("cancelled: %+v", done)
	}
	if err := s.CancelChatCommand(runID); !errors.Is(err, ErrChatCommandNotFound) {
		t.Fatalf("a finished command can be cancelled again: %v", err)
	}
	for _, in := range []RunChatCommandInput{{SessionID: session.ID, Command: "  "}, {SessionID: "", Command: "ls"}, {SessionID: session.ID, Command: strings.Repeat("a", maxChatCommandBytes+1)}} {
		if _, err := s.RunChatCommand(in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	if _, err := s.RunChatCommand(RunChatCommandInput{SessionID: "nope", Command: "ls"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
}
