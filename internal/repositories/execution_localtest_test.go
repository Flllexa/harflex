package repositories

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// localTestProject is a committed project with a prepared copy whose change modifies a file, adds one in a new folder,
// deletes one, changes a mode and moves a symlink.
func localTestProject(t *testing.T) (ExecutionSnapshot, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	source := t.TempDir()
	writeSnapshotFile(t, source, "a.txt", "original\n")
	writeSnapshotFile(t, source, "old.txt", "to delete\n")
	writeSnapshotFile(t, source, "b.txt", "b\n")
	writeSnapshotFile(t, source, "run.sh", "#!/bin/sh\n")
	writeSnapshotFile(t, source, ".gitignore", "node_modules/\n.env\n")
	if err := os.Chmod(filepath.Join(source, "run.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(source, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	gitSnapshot(t, source, "init", "-q", "-b", "main")
	commitSnapshotRepo(t, source)
	prepared, err := PrepareExecutionSnapshotAt(context.Background(), source, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	writeSnapshotFile(t, prepared.Root, "a.txt", "changed by the Coder\n")
	writeSnapshotFile(t, prepared.Root, "dir/deep/new.txt", "new\n")
	if err := os.Remove(filepath.Join(prepared.Root, "old.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(prepared.Root, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(prepared.Root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b.txt", filepath.Join(prepared.Root, "link")); err != nil {
		t.Fatal(err)
	}
	return prepared, filepath.Join(t.TempDir(), "backup")
}

func applyForTest(t *testing.T, prepared ExecutionSnapshot, backup string) LocalTestApply {
	t.Helper()
	applied, err := ApplyExecutionForLocalTest(context.Background(), prepared, backup, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return applied
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLocalTestAppliesAndUndoRestoresTheFolderExactly(t *testing.T) {
	prepared, backup := localTestProject(t)
	source := prepared.SourceRoot
	applied := applyForTest(t, prepared, backup)
	if readText(t, filepath.Join(source, "a.txt")) != "changed by the Coder\n" || readText(t, filepath.Join(source, "dir/deep/new.txt")) != "new\n" {
		t.Fatal("the change was not applied")
	}
	if _, err := os.Lstat(filepath.Join(source, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("the deleted file is still there: %v", err)
	}
	conflict, err := UndoExecutionLocalTest(context.Background(), prepared, backup, applied, false)
	if err != nil || len(conflict.Edited)+len(conflict.Created) != 0 {
		t.Fatalf("undo: %+v %v", conflict, err)
	}
	if readText(t, filepath.Join(source, "a.txt")) != "original\n" || readText(t, filepath.Join(source, "old.txt")) != "to delete\n" {
		t.Fatal("the contents were not restored")
	}
	if info, err := os.Stat(filepath.Join(source, "run.sh")); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("the mode was not restored: %v %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(source, "link")); err != nil || target != "a.txt" {
		t.Fatalf("the symlink was not restored: %q %v", target, err)
	}
	if _, err := os.Lstat(filepath.Join(source, "dir")); !os.IsNotExist(err) {
		t.Fatalf("the folder the change created is still there: %v", err)
	}
	if matches, err := ExecutionSourceStillMatches(context.Background(), prepared); err != nil || !matches {
		t.Fatalf("the folder does not match its state before the test: %v %v", matches, err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("the backup stayed after undo: %v", err)
	}
}

func TestLocalTestRefusesADirtyFolder(t *testing.T) {
	prepared, backup := localTestProject(t)
	writeSnapshotFile(t, prepared.SourceRoot, "node_modules/x/index.js", "ignored\n")
	writeSnapshotFile(t, prepared.SourceRoot, "notes.md", "mine\n")
	_, err := ApplyExecutionForLocalTest(context.Background(), prepared, backup, 1<<20)
	var dirty *LocalTestDirtyError
	if !errors.As(err, &dirty) || !errors.Is(err, ErrLocalTestSourceDirty) || !reflect.DeepEqual(dirty.Paths, []string{"notes.md"}) {
		t.Fatalf("err = %v", err)
	}
	if readText(t, filepath.Join(prepared.SourceRoot, "a.txt")) != "original\n" {
		t.Fatal("a dirty folder was written to")
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("a refused test left a backup: %v", err)
	}
	if err := os.Remove(filepath.Join(prepared.SourceRoot, "notes.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyExecutionForLocalTest(context.Background(), prepared, backup, 1<<20); err != nil {
		t.Fatalf("ignored files alone refused the test: %v", err)
	}
}

func TestUndoStopsOnAnEditedFile(t *testing.T) {
	prepared, backup := localTestProject(t)
	applied := applyForTest(t, prepared, backup)
	writeSnapshotFile(t, prepared.SourceRoot, "a.txt", "edited during the test\n")
	conflict, err := UndoExecutionLocalTest(context.Background(), prepared, backup, applied, false)
	if err != nil || !reflect.DeepEqual(conflict.Edited, []string{"a.txt"}) {
		t.Fatalf("undo over an edit: %+v %v", conflict, err)
	}
	if readText(t, filepath.Join(prepared.SourceRoot, "a.txt")) != "edited during the test\n" {
		t.Fatal("undo threw away the person's edit without asking")
	}
	if conflict, err := UndoExecutionLocalTest(context.Background(), prepared, backup, applied, true); err != nil || len(conflict.Edited) != 0 {
		t.Fatalf("discarding undo: %+v %v", conflict, err)
	}
	if readText(t, filepath.Join(prepared.SourceRoot, "a.txt")) != "original\n" {
		t.Fatal("discarding did not restore the original")
	}
}

func TestUndoReportsFilesTheTestCreated(t *testing.T) {
	prepared, backup := localTestProject(t)
	applied := applyForTest(t, prepared, backup)
	writeSnapshotFile(t, prepared.SourceRoot, "server.log", "listening\n")
	writeSnapshotFile(t, prepared.SourceRoot, "node_modules/pkg/index.js", "dep\n")
	writeSnapshotFile(t, prepared.SourceRoot, "b.txt", "changed by a script\n")
	conflict, err := UndoExecutionLocalTest(context.Background(), prepared, backup, applied, false)
	if err != nil || !reflect.DeepEqual(conflict.Created, []string{"server.log"}) || !reflect.DeepEqual(conflict.Edited, []string{"b.txt"}) {
		t.Fatalf("undo with files the test made: %+v %v", conflict, err)
	}
	if _, err := UndoExecutionLocalTest(context.Background(), prepared, backup, applied, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(prepared.SourceRoot, "server.log")); !os.IsNotExist(err) {
		t.Fatalf("the created file stayed: %v", err)
	}
	if readText(t, filepath.Join(prepared.SourceRoot, "b.txt")) != "b\n" {
		t.Fatal("the tracked file the test changed was not restored")
	}
	if _, err := os.Stat(filepath.Join(prepared.SourceRoot, "node_modules/pkg/index.js")); err != nil {
		t.Fatalf("an ignored dependency was removed: %v", err)
	}
	if matches, err := ExecutionSourceStillMatches(context.Background(), prepared); err != nil || !matches {
		t.Fatalf("the folder is not back: %v %v", matches, err)
	}
}

func TestUndoWithoutItsBackupChangesNothing(t *testing.T) {
	prepared, backup := localTestProject(t)
	applied := applyForTest(t, prepared, backup)
	if err := os.RemoveAll(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := UndoExecutionLocalTest(context.Background(), prepared, backup, applied, true); !errors.Is(err, ErrLocalTestBackupMissing) {
		t.Fatalf("err = %v", err)
	}
	if readText(t, filepath.Join(prepared.SourceRoot, "a.txt")) != "changed by the Coder\n" {
		t.Fatal("an undo without its backup changed the folder")
	}
}

func TestLocalTestUndoRestoresANestedRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := context.Background()
	root, _ := multiRepoWorkspace(t)
	prepared, err := PrepareExecutionSnapshotAt(ctx, root, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	writeSnapshotFile(t, prepared.Root, "domains/contracts/baixa.go", "package contracts\n\nfunc Baixa() bool { return true }\n")
	writeSnapshotFile(t, prepared.Root, "domains/contracts/nested/new.go", "package nested\n")
	backup := filepath.Join(t.TempDir(), "backup")
	applied, err := ApplyExecutionForLocalTest(ctx, prepared, backup, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if readText(t, filepath.Join(root, "domains/contracts/baixa.go")) != "package contracts\n\nfunc Baixa() bool { return true }\n" {
		t.Fatal("the nested change was not applied")
	}
	if conflict, err := UndoExecutionLocalTest(ctx, prepared, backup, applied, false); err != nil || len(conflict.Edited)+len(conflict.Created) != 0 {
		t.Fatalf("undo: %+v %v", conflict, err)
	}
	if readText(t, filepath.Join(root, "domains/contracts/baixa.go")) != "package contracts\n\nfunc Baixa() bool { return false }\n" {
		t.Fatal("the nested file was not restored")
	}
	if _, err := os.Stat(filepath.Join(root, "domains/contracts/nested/new.go")); !os.IsNotExist(err) {
		t.Fatalf("the nested new file stayed: %v", err)
	}
	if matches, err := ExecutionSourceStillMatches(ctx, prepared); err != nil || !matches {
		t.Fatalf("the workspace is not back: %v %v", matches, err)
	}
}
