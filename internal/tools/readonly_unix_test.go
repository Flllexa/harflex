//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A rescue endpoint releases a regressed blocking open before failing the test.
func expectPromptFIFOError(t *testing.T, fifo string, run func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a") {
			t.Fatalf("FIFO error = %v, want descriptor type rejection", err)
		}
	case <-time.After(time.Second):
		fd, err := syscall.Open(fifo, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatalf("open rescue FIFO endpoint: %v", err)
		}
		// Release a reader blocked after Open as well as one blocked inside Open.
		if err := syscall.Close(fd); err != nil {
			t.Errorf("close rescue endpoint: %v", err)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("FIFO operation did not finish after releasing its blocked open")
		}
		t.Fatal("FIFO operation blocked without a writer")
	}
}

func TestReadListGrepRejectFIFODescriptors(t *testing.T) {
	g, workspace := readOnlyWorkspace(t)
	fifo := filepath.Join(workspace, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []Tool{NewReadTool(g), NewListTool(g)} {
		t.Run(tool.Spec().Name, func(t *testing.T) {
			expectPromptFIFOError(t, fifo, func() error {
				_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"pipe"}`), nil)
				return err
			})
		})
	}
	t.Run("grep descriptor", func(t *testing.T) {
		root, err := os.OpenRoot(g.root)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		expectPromptFIFOError(t, fifo, func() error {
			_, _, err := grepFile(context.Background(), root, "pipe", "needle", 1)
			return err
		})
	})
	t.Run("grep skips FIFO during walk", func(t *testing.T) {
		_, details := executeReadOnly(t, NewGrepTool(g), `{"query":"needle"}`)
		if details["count"] != float64(0) {
			t.Fatalf("FIFO grep = %#v", details)
		}
	})
}
