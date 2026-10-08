//go:build linux

package sddworkspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxFilesystemIdentityUsesStatxMountID(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "file"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	rootID, err := rootFilesystemIdentity(root)
	if errors.Is(err, ErrFilesystemBoundaryUnprovable) {
		t.Skipf("kernel does not expose statx mount IDs: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rootID, "mnt:") {
		t.Fatalf("root filesystem identity = %q, want statx mount ID", rootID)
	}
	file, err := root.Open("file")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fileID, err := fileFilesystemIdentity(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureSameFilesystem(rootID, fileID, "file"); err != nil {
		t.Fatalf("same-mount file rejected: %v", err)
	}
	if err := ensureSameFilesystem(rootID, "mnt:different", "nested-mount"); !errors.Is(err, ErrFilesystemBoundary) {
		t.Fatalf("different mount accepted: %v", err)
	}
}
