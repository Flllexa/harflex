package sddworkspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestScanBuildsDeterministicManifestAndSkipsExcludedContents(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644)
	writeFile(t, filepath.Join(root, ".git", "config"), []byte("git metadata"), 0o600)
	writeFile(t, filepath.Join(root, ".env"), []byte("unreadable credential"), 0)
	writeFile(t, filepath.Join(root, ".env.example"), []byte("SAFE=example\n"), 0o600)

	first, err := Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	second, err := Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("second Scan() error = %v", err)
	}
	if first.Hash == "" || first.Hash != second.Hash {
		t.Fatalf("manifest hash must be stable and non-empty: first=%q second=%q", first.Hash, second.Hash)
	}
	if first.FileCount != 2 || first.TotalBytes != int64(len("package main\n")+len("SAFE=example\n")) {
		t.Fatalf("unexpected included content summary: files=%d bytes=%d", first.FileCount, first.TotalBytes)
	}
	if len(first.Excluded) != 2 {
		t.Fatalf("expected .git and .env exclusions, got %#v", first.Excluded)
	}
	if got := entryPaths(first.Entries); len(got) != 3 || got[0] != ".env.example" || got[1] != "src" || got[2] != "src/main.go" {
		t.Fatalf("unexpected manifest entry paths: %#v", got)
	}
}

func TestScanCapturesExecutableBitAndModeOnlyChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX executable permission bits")
	}
	root := t.TempDir()
	script := filepath.Join(root, "script.sh")
	writeFile(t, script, []byte("#!/bin/sh\necho ok\n"), 0o755)
	before, err := Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Entries[0].Mode != 0o100755 {
		t.Fatalf("executable manifest mode = %#o, want 100755", before.Entries[0].Mode)
	}
	if err := os.Chmod(script, 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Entries[0].SHA256 != after.Entries[0].SHA256 {
		t.Fatal("chmod changed content hash")
	}
	if got := Compare(before, after); len(got) != 1 || got[0] != (Change{Path: "script.sh", Kind: ChangeMode}) {
		t.Fatalf("mode-only comparison = %#v", got)
	}
}

func writeFile(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
}

func entryPaths(entries []Entry) []string {
	paths := make([]string, len(entries))
	for index, entry := range entries {
		paths[index] = entry.Path
	}
	return paths
}
