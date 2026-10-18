package terminal

import (
	"encoding/base64"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTerminalRunsAShellInTheFolderAndEnds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no pseudo-terminal on Windows")
	}
	t.Setenv("SHELL", "/bin/sh")
	var mu sync.Mutex
	var output strings.Builder
	exited := make(chan Output, 1)
	manager := NewManager(func(out Output) {
		if out.Exited {
			exited <- out
			return
		}
		data, _ := base64.StdEncoding.DecodeString(out.Data)
		mu.Lock()
		output.Write(data)
		mu.Unlock()
	})
	dir := t.TempDir()
	id, err := manager.Start(dir, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Resize(id, 120, 30); err != nil {
		t.Fatal(err)
	}
	if err := manager.Write(id, "pwd; echo harflex-$((40+2))\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		text := output.String()
		mu.Unlock()
		if strings.Contains(text, "harflex-42") {
			if !strings.Contains(text, dir[len(dir)-12:]) {
				t.Fatalf("the shell must start in the project folder: %q", text)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no output from the shell: %q", text)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := manager.Write(id, "exit 3\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-exited:
		if out.ID != id || out.Code != 3 {
			t.Fatalf("exit = %+v", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the shell did not report its end")
	}
	if err := manager.Write(id, "x"); err != ErrNotFound {
		t.Fatalf("a finished terminal must be gone: %v", err)
	}
}

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A service the AI starts for a local test is a named terminal: it is listed, and what it printed can be read back.
func TestNamedTerminalKeepsItsRecentOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no pseudo-terminal on Windows")
	}
	t.Setenv("SHELL", "/bin/sh")
	changes := 0
	var mu sync.Mutex
	manager := NewManager(nil)
	manager.OnChange(func() { mu.Lock(); changes++; mu.Unlock() })
	dir := t.TempDir()
	id, err := manager.StartNamed(dir, "web", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Write(id, "printf 'pro''nto\\n'\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the service output", func() bool { recent, _ := manager.Recent(id); return strings.Contains(string(recent), "pronto") })
	list := manager.List()
	if len(list) != 1 || list[0].ID != id || list[0].Name != "web" || list[0].Dir != dir || !list[0].Running {
		t.Fatalf("list: %+v", list)
	}
	if err := manager.Write(id, "exit\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the service to end", func() bool { list := manager.List(); return len(list) == 1 && !list[0].Running })
	if recent, err := manager.Recent(id); err != nil || !strings.Contains(string(recent), "pronto") {
		t.Fatalf("the output of an ended service is gone: %q %v", recent, err)
	}
	manager.Forget(id)
	if len(manager.List()) != 0 {
		t.Fatal("a forgotten terminal is still listed")
	}
	mu.Lock()
	defer mu.Unlock()
	if changes < 2 {
		t.Fatalf("changes = %d, want the start and the end", changes)
	}
}

func TestRecentOutputIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no pseudo-terminal on Windows")
	}
	t.Setenv("SHELL", "/bin/sh")
	manager := NewManager(nil)
	id, err := manager.StartNamed(t.TempDir(), "noisy", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(id) })
	if err := manager.Write(id, "yes harflex | head -c 300000; echo; echo fi''m\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the end of the output", func() bool { recent, _ := manager.Recent(id); return strings.Contains(string(recent), "fim") })
	if recent, _ := manager.Recent(id); len(recent) > 64*1024 {
		t.Fatalf("recent output kept %d bytes", len(recent))
	}
}
