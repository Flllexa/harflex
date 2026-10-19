package repositories

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRestoreExecutionSnapshotRebuildsAVanishedCopyFromItsPatch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := committedSnapshotProject(t)
	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared.Root, "README.md"), []byte("coder edit\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(prepared.Root, "run.sh"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(prepared.Root, "pkg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared.Root, "pkg", "new-file.go"), []byte("package sample\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want, _, err := ExecutionDiff(context.Background(), prepared, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// The copy vanishes, as when the system clears a cache to free disk.
	if err := os.RemoveAll(prepared.Root); err != nil {
		t.Fatal(err)
	}
	modes := map[string]os.FileMode{"README.md": 0o640, "run.sh": 0o640, "pkg/new-file.go": 0o600}
	if err := RestoreExecutionSnapshot(context.Background(), prepared, want, modes); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(prepared.Root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("restored root: %v %v", info, err)
	}
	got, _, err := ExecutionDiff(context.Background(), prepared, 1<<20)
	if err != nil || got != want {
		t.Fatalf("the restored copy differs from the saved change (%v):\n%s\n---\n%s", err, got, want)
	}
	if body, _ := os.ReadFile(filepath.Join(source, "README.md")); string(body) != "baseline\n" {
		t.Fatalf("restoring touched the project: %q", body)
	}
}

func TestRestoreExecutionSnapshotRefusesAProjectThatMovedOn(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := committedSnapshotProject(t)
	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(prepared.Root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("someone else's edit\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := RestoreExecutionSnapshot(context.Background(), prepared, "", nil); !errors.Is(err, ErrExecutionSourceDrift) {
		t.Fatalf("restore over a changed project: %v", err)
	}
	if _, err := os.Lstat(prepared.Root); !os.IsNotExist(err) {
		t.Fatalf("a refused restore left a copy behind: %v", err)
	}
}

func TestRestoreExecutionSnapshotLeavesSymlinkChangesUnrestored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := committedSnapshotProject(t)
	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(prepared.Root, "README-link")); err != nil {
		t.Fatal(err)
	}
	patch, _, err := ExecutionDiff(context.Background(), prepared, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(prepared.Root); err != nil {
		t.Fatal(err)
	}
	if err := RestoreExecutionSnapshot(context.Background(), prepared, patch, nil); !errors.Is(err, ErrExecutionSourceDrift) {
		t.Fatalf("restore of a symlink change: %v", err)
	}
	if _, err := os.Lstat(prepared.Root); !os.IsNotExist(err) {
		t.Fatalf("a refused restore left a copy behind: %v", err)
	}
}
