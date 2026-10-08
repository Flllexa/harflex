package repositories

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitSnapshot(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func committedSnapshotProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("baseline\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "run.sh"), []byte("#!/bin/sh\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("README.md", filepath.Join(root, "README-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	gitSnapshot(t, root, "init")
	gitSnapshot(t, root, "add", "README.md", "run.sh", "README-link")
	gitSnapshot(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "baseline")
	return root
}

func TestPrepareExecutionSnapshotCopiesCleanGitProjectWithoutGitMetadata(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := committedSnapshotProject(t)
	beforeHead := gitSnapshot(t, source, "rev-parse", "HEAD")
	beforeBranch := gitSnapshot(t, source, "branch", "--show-current")

	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	if prepared.Root == source || prepared.SourceRoot != source || prepared.SourceHead != beforeHead || prepared.SourceBranch != beforeBranch || !prepared.IsGit {
		t.Fatalf("execution snapshot identity: %+v", prepared)
	}
	if _, err := os.Lstat(filepath.Join(prepared.Root, ".git")); !os.IsNotExist(err) {
		t.Fatalf("execution copy must not share Git metadata: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(prepared.Root, "README.md")); err != nil || string(got) != "baseline\n" {
		t.Fatalf("copied file: %q %v", got, err)
	}
	if info, err := os.Stat(filepath.Join(prepared.Root, "run.sh")); err != nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("copied executable mode: %v %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(prepared.Root, "README-link")); err != nil || target != "README.md" {
		t.Fatalf("copied symlink: %q %v", target, err)
	}
	if len(prepared.Manifest) != 3 {
		t.Fatalf("manifest entries = %d, want 3: %+v", len(prepared.Manifest), prepared.Manifest)
	}
	if err := os.WriteFile(filepath.Join(prepared.Root, "README.md"), []byte("coder edit\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(source, "README.md")); err != nil || string(got) != "baseline\n" {
		t.Fatalf("Coder write escaped isolated root: %q %v", got, err)
	}
	if gotHead := gitSnapshot(t, source, "rev-parse", "HEAD"); gotHead != beforeHead {
		t.Fatalf("source HEAD changed: %s", gotHead)
	}
}

func TestPrepareExecutionSnapshotRejectsDirtyGitSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := committedSnapshotProject(t)
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("user change\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir()); err != ErrExecutionSourceDirty {
		t.Fatalf("dirty source error = %v, want ErrExecutionSourceDirty", err)
	}
}

func TestPrepareExecutionSnapshotRequiresConfirmationForNonGitCopy(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(source, ".git")
	if err := os.Mkdir(metadata, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "private"), []byte("excluded"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewExecutionSnapshot(context.Background(), source)
	if err != nil || preview.IsGit || preview.FileCount != 1 || preview.TotalBytes != int64(len("package main\n")) || len(preview.ExcludedPaths) != 1 || preview.ExcludedPaths[0] != ".git" {
		t.Fatalf("non-Git copy preview: %+v %v", preview, err)
	}
	if _, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir()); err != ErrExecutionCopyConfirmationRequired {
		t.Fatalf("unconfirmed copy error = %v, want ErrExecutionCopyConfirmationRequired", err)
	}
	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	if prepared.IsGit || prepared.SourceRoot != source || len(prepared.Manifest) != 1 {
		t.Fatalf("non-Git snapshot: %+v", prepared)
	}
}

func TestPrepareExecutionSnapshotRejectsSymlinkEscapingSource(t *testing.T) {
	source := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(source, "external-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := PrepareExecutionSnapshotAt(context.Background(), source, true, t.TempDir()); err != ErrExecutionSnapshotUnsafePath {
		t.Fatalf("external symlink error = %v, want ErrExecutionSnapshotUnsafePath", err)
	}
}

func TestExecutionDiffUsesIsolatedManifestForAddsRemovalsContentAndModes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := committedSnapshotProject(t)
	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	if err := os.WriteFile(filepath.Join(prepared.Root, "README.md"), []byte("coder edit\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(prepared.Root, "run.sh"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(prepared.Root, "README-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared.Root, "new-file.go"), []byte("package sample\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, changed, err := ExecutionDiff(context.Background(), prepared, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+coder edit", "new file mode 100644", "deleted file mode 120000", "old mode 100755", "new mode 100644", "a/new-file.go", "README-link"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("manifest diff missing %q:\n%s", want, diff)
		}
	}
	if len(changed) != 4 {
		t.Fatalf("changed manifest entries=%d, want content, mode, added and removed changes: %+v", len(changed), changed)
	}
	if got, err := os.ReadFile(filepath.Join(source, "README.md")); err != nil || string(got) != "baseline\n" {
		t.Fatalf("diff touched source: %q %v", got, err)
	}
}

func TestExecutionDiffRecordsPermissionChangeWithoutContentChange(t *testing.T) {
	entry := ExecutionManifestEntry{Path: "settings.json", Kind: "file", Hash: "same-hash", Mode: 0o640, Size: 8}
	changed := entry
	changed.Mode = 0o600
	diff, err := formatExecutionDiff(entry.Path, entry, true, []byte("same\n"), changed, true, []byte("same\n"))
	if err != nil || !strings.Contains(diff, "mode changed from 0640 to 0600") || strings.Contains(diff, "@@") {
		t.Fatalf("permission-only diff: %q err=%v", diff, err)
	}
}

func TestExecutionSnapshotDetectsSourceHeadDriftWithIdenticalFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := committedSnapshotProject(t)
	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	gitSnapshot(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "head drift")
	matches, err := ExecutionSourceStillMatches(context.Background(), prepared)
	if err != nil || matches {
		t.Fatalf("source with changed HEAD matches old baseline: matches=%v err=%v", matches, err)
	}
}

func TestGitExecutionPreviewListsIgnoredFilesAndExcludesThemFromCopy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := committedSnapshotProject(t)
	if err := os.WriteFile(filepath.Join(source, ".gitignore"), []byte(".env\nignored-dir/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitSnapshot(t, source, "add", ".gitignore")
	gitSnapshot(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "ignore local files")
	if err := os.WriteFile(filepath.Join(source, ".env"), []byte("credential=do-not-copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "ignored-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "ignored-dir", "cache.db"), []byte("local cache"), 0o600); err != nil {
		t.Fatal(err)
	}

	preview, err := PreviewExecutionSnapshot(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	excluded := strings.Join(preview.ExcludedPaths, "\n")
	for _, path := range []string{".git", ".env", "ignored-dir"} {
		if !strings.Contains(excluded, path) {
			t.Fatalf("preview omitted excluded path %q: %+v", path, preview)
		}
	}
	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	if _, err := os.Lstat(filepath.Join(prepared.Root, ".env")); !os.IsNotExist(err) {
		t.Fatalf("ignored credentials entered the Code copy: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(prepared.Root, "ignored-dir")); !os.IsNotExist(err) {
		t.Fatalf("ignored local data entered the Code copy: %v", err)
	}
}

func TestApplyExecutionSnapshotAppliesOnlyAfterSourceReadback(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := committedSnapshotProject(t)
	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	if err := os.WriteFile(filepath.Join(prepared.Root, "README.md"), []byte("approved code\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(prepared.Root, "run.sh"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(prepared.Root, "README-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared.Root, "new-file.go"), []byte("package sample\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(source, "README.md")); err != nil || string(got) != "baseline\n" {
		t.Fatalf("source changed before Apply: %q %v", got, err)
	}
	readback, err := ApplyExecutionSnapshot(context.Background(), prepared, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(readback) != 3 {
		t.Fatalf("apply readback manifest=%+v", readback)
	}
	if got, err := os.ReadFile(filepath.Join(source, "README.md")); err != nil || string(got) != "approved code\n" {
		t.Fatalf("source content not applied: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(source, "new-file.go")); err != nil {
		t.Fatalf("new source file not applied: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(source, "README-link")); !os.IsNotExist(err) {
		t.Fatalf("deleted symlink still exists: %v", err)
	}
	if info, err := os.Stat(filepath.Join(source, "run.sh")); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode not applied: %v %v", info, err)
	}
	if readbackAgain, err := ApplyExecutionSnapshot(context.Background(), prepared, 1<<20); err != nil || !manifestsEqual(readback, readbackAgain) {
		t.Fatalf("patch application is not idempotent: manifest=%+v err=%v", readbackAgain, err)
	}
}

func TestApplyExecutionSnapshotRefusesDriftWithoutOverwritingIt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := committedSnapshotProject(t)
	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	if err := os.WriteFile(filepath.Join(prepared.Root, "README.md"), []byte("approved code\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("concurrent user edit\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyExecutionSnapshot(context.Background(), prepared, 1<<20); !errors.Is(err, ErrExecutionSourceDrift) {
		t.Fatalf("source drift apply error = %v, want ErrExecutionSourceDrift", err)
	}
	if got, err := os.ReadFile(filepath.Join(source, "README.md")); err != nil || string(got) != "concurrent user edit\n" {
		t.Fatalf("apply overwrote the concurrent source edit: %q %v", got, err)
	}
}
