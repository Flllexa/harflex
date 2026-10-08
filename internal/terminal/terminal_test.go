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
