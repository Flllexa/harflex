package application

import (
	"encoding/base64"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A service started for a project shows up in that project's terminal list, and its output can be drawn again.
func TestListTerminalsShowsAServiceStartedForTheProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no pseudo-terminal on Windows")
	}
	t.Setenv("SHELL", "/bin/sh")
	s, _, _ := setup(t)
	var events []string
	SetEmitter(s, func(name string, _ any) { events = append(events, name) })
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.terminalManager().StartNamed(workspace.Path, "api", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.CloseTerminal(id) })
	if _, err := s.terminalManager().StartNamed(other.Path, "elsewhere", 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTerminal(WriteTerminalInput{ID: id, Data: "echo api-''pronta\n"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListTerminals(ListTerminalsInput{WorkspaceID: workspace.ID})
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].Name != "api" || !list[0].Running {
		t.Fatalf("terminals of the project: %+v %v", list, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		encoded, err := s.RecentTerminalOutput(id)
		data, _ := base64.StdEncoding.DecodeString(encoded)
		if err == nil && strings.Contains(string(data), "api-pronta") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no recent output: %q %v", data, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	found := false
	for _, name := range events {
		found = found || name == TerminalsEvent
	}
	if !found {
		t.Fatalf("no %s event among %v", TerminalsEvent, events)
	}
}
