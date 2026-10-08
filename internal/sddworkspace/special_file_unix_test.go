//go:build darwin || linux

package sddworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestScanRejectsNamedPipeWithoutOpeningIt(t *testing.T) {
	root := t.TempDir()
	pipePath := filepath.Join(root, "input.pipe")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Scan(context.Background(), root)
	if !errors.Is(err, ErrSpecialFile) {
		t.Fatalf("Scan() error = %v, want ErrSpecialFile", err)
	}
}

func TestOpenRegularFileDoesNotBlockOrAcceptNamedPipe(t *testing.T) {
	rootPath := t.TempDir()
	pipePath := filepath.Join(rootPath, "input.pipe")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	done := make(chan error, 1)
	go func() {
		file, err := openRegularFile(root, "input.pipe")
		if file != nil {
			_ = file.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSpecialFile) {
			t.Fatalf("openRegularFile() error = %v, want ErrSpecialFile", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("opening a raced-in named pipe blocked")
	}
}

func TestOpenRegularFileDoesNotFollowFinalSymlink(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "safe.txt"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("safe.txt", filepath.Join(rootPath, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := openRegularFile(root, "alias")
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("openRegularFile followed a final symlink")
	}
}
