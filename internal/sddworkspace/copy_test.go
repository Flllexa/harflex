package sddworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCreatePrivateCopyMakesPrivatePhysicalCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows ACL gate is covered separately")
	}
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "src", "main.go"), []byte("package main\n"), 0o755)
	writeFile(t, filepath.Join(source, "empty", ".keep"), nil, 0o644)
	if err := os.Remove(filepath.Join(source, "empty", ".keep")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("src/main.go", filepath.Join(source, "entrypoint")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	copy, err := createPrivateCopyForTest(context.Background(), source, privateParent, copyHooks{})
	if err != nil {
		t.Fatalf("CreatePrivateCopy() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(copy.Root) })
	if copy.Root == "" || copy.Baseline.Hash == "" {
		t.Fatalf("copy must return a usable root and baseline: %#v", copy)
	}
	canonicalParent, err := canonicalDirectory(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(copy.Root) != canonicalParent {
		t.Fatalf("copy root %q is not a child of private parent %q", copy.Root, canonicalParent)
	}
	rootInfo, err := os.Stat(copy.Root)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("copy root is not private: mode %o", rootInfo.Mode().Perm())
	}

	sourceFile := filepath.Join(source, "src", "main.go")
	copyFile := filepath.Join(copy.Root, "src", "main.go")
	sourceInfo, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	copyInfo, err := os.Stat(copyFile)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sourceInfo, copyInfo) {
		t.Fatal("private copy must not hard-link to source")
	}
	got, err := os.ReadFile(copyFile)
	if err != nil || string(got) != "package main\n" {
		t.Fatalf("copied bytes = %q, error = %v", got, err)
	}
	linkInfo, err := os.Lstat(filepath.Join(copy.Root, "entrypoint"))
	if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was not preserved as a link: info=%v error=%v", linkInfo, err)
	}
	if target, err := os.Readlink(filepath.Join(copy.Root, "entrypoint")); err != nil || target != "src/main.go" {
		t.Fatalf("symlink target = %q, error = %v", target, err)
	}
	if copy.Baseline.FileCount != 2 {
		t.Fatalf("baseline file count = %d, want 2 (file and symlink)", copy.Baseline.FileCount)
	}
	if emptyInfo, err := os.Stat(filepath.Join(copy.Root, "empty")); err != nil || !emptyInfo.IsDir() {
		t.Fatalf("empty source directory was not preserved: info=%v error=%v", emptyInfo, err)
	}
	if copyInfo.Mode().Perm()&0o077 != 0 || copyInfo.Mode().Perm()&0o100 == 0 {
		t.Fatalf("private executable mode = %o, want owner execute and no group/other access", copyInfo.Mode().Perm())
	}
	if err := os.WriteFile(copyFile, []byte("changed only in copy"), 0o700); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(sourceFile)
	if err != nil || string(original) != "package main\n" {
		t.Fatalf("source changed through private copy: bytes=%q error=%v", original, err)
	}
}

func TestCreatePrivateCopyAbortsAndCleansUpWhenSourceDriftsDuringCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows ACL gate is covered separately")
	}
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "a.txt"), []byte("A"), 0o600)
	writeFile(t, filepath.Join(source, "z.txt"), []byte("Z"), 0o600)
	drifted := false
	hooks := copyHooks{afterFile: func(relativePath string) {
		if relativePath == "a.txt" && !drifted {
			drifted = true
			if writeErr := os.WriteFile(filepath.Join(source, "z.txt"), []byte("Y"), 0o600); writeErr != nil {
				t.Fatalf("simulate source drift: %v", writeErr)
			}
		}
	}}
	copy, err := createPrivateCopyForTest(context.Background(), source, privateParent, hooks)
	if !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("createPrivateCopy() error = %v, want ErrSourceDrift", err)
	}
	if copy.Root != "" {
		t.Fatalf("drifted copy returned usable root %q", copy.Root)
	}
	leftovers, readErr := os.ReadDir(privateParent)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(leftovers) != 0 {
		t.Fatalf("staging root was not cleaned after drift: %#v", leftovers)
	}
}

func TestCleanupPartialPrivateCopySurfacesFailureAndOriginalCause(t *testing.T) {
	parentRoot, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parentRoot.Close()
	originalErr := errors.New("copy failed")
	err = cleanupPartialPrivateCopy(parentRoot, "../outside", filepath.Join(parentRoot.Name(), "outside"), originalErr)
	if !errors.Is(err, ErrPrivateCopyCleanup) {
		t.Fatalf("cleanup error = %v, want ErrPrivateCopyCleanup", err)
	}
	if !errors.Is(err, originalErr) {
		t.Fatalf("cleanup error = %v, want original copy failure to remain discoverable", err)
	}
}

func TestCopyStopsWhenParentChangesToExcludedSSHDirectoryBeforeLeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACL behavior is not runtime-tested in this slice")
	}
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "a.txt"), []byte("A"), 0o600)
	writeFile(t, filepath.Join(source, "parent", "file.txt"), []byte("original"), 0o600)
	writeFile(t, filepath.Join(source, ".ssh", "file.txt"), []byte("secret!!"), 0o600)
	parent := filepath.Join(source, "parent")
	moved := filepath.Join(source, "moved")
	leafOpenCount := 0
	hooks := copyHooks{
		afterFile: func(relativePath string) {
			if relativePath != "a.txt" {
				return
			}
			if err := os.Rename(parent, moved); err != nil {
				t.Fatalf("move source parent: %v", err)
			}
			if err := os.Symlink(".ssh", parent); err != nil {
				t.Fatalf("replace source parent with symlink: %v", err)
			}
		},
		beforeLeafOpen: func(entry Entry, _ *os.Root) error {
			if entry.Path == "parent/file.txt" {
				leafOpenCount++
			}
			return nil
		},
	}
	copy, err := createPrivateCopyForTest(context.Background(), source, privateParent, hooks)
	if !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("copy error = %v, want ErrSourceDrift before opening .ssh/file.txt", err)
	}
	if leafOpenCount != 0 {
		t.Fatalf("leaf-open hook called %d times after parent escaped to .ssh", leafOpenCount)
	}
	if copy.Root != "" {
		t.Fatalf("drifted copy returned usable root %q", copy.Root)
	}
	leftovers, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("staging root was not cleaned after parent drift: %#v", leftovers)
	}
}

func TestCopyStopsWhenParentChangesToDifferentDirectoryBeforeLeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACL behavior is not runtime-tested in this slice")
	}
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "a.txt"), []byte("A"), 0o600)
	parent := filepath.Join(source, "parent")
	writeFile(t, filepath.Join(parent, "file.txt"), []byte("original"), 0o600)
	moved := filepath.Join(source, "moved")
	leafOpenCount := 0
	hooks := copyHooks{
		afterFile: func(relativePath string) {
			if relativePath != "a.txt" {
				return
			}
			if err := os.Rename(parent, moved); err != nil {
				t.Fatalf("move source parent: %v", err)
			}
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatalf("replace source parent: %v", err)
			}
			writeFile(t, filepath.Join(parent, "file.txt"), []byte("secret!!"), 0o600)
		},
		beforeLeafOpen: func(entry Entry, _ *os.Root) error {
			if entry.Path == "parent/file.txt" {
				leafOpenCount++
			}
			return nil
		},
	}
	copy, err := createPrivateCopyForTest(context.Background(), source, privateParent, hooks)
	if !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("copy error = %v, want ErrSourceDrift before reading replaced parent", err)
	}
	if leafOpenCount != 0 {
		t.Fatalf("leaf-open hook called %d times after parent was replaced", leafOpenCount)
	}
	if copy.Root != "" {
		t.Fatalf("drifted copy returned usable root %q", copy.Root)
	}
	leftovers, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("staging root was not cleaned after parent replacement: %#v", leftovers)
	}
}

func createPrivateCopyForTest(ctx context.Context, source, parent string, hooks copyHooks) (PrivateCopy, error) {
	if runtime.GOOS == "linux" && hooks.afterFile == nil {
		return CreatePrivateCopy(ctx, source, parent)
	}
	sourcePath, err := canonicalDirectory(source)
	if err != nil {
		return PrivateCopy{}, err
	}
	parentPath, err := canonicalDirectory(parent)
	if err != nil {
		return PrivateCopy{}, err
	}
	sourceRoot, err := os.OpenRoot(sourcePath)
	if err != nil {
		return PrivateCopy{}, err
	}
	defer sourceRoot.Close()
	snapshot, err := scanRootSnapshot(ctx, sourceRoot, defaultScanLimits, scanHooks{})
	if err != nil {
		return PrivateCopy{}, err
	}
	baseline := snapshot.manifest
	sourceFSID, err := rootFilesystemIdentity(sourceRoot)
	if err != nil {
		return PrivateCopy{}, err
	}
	staging, err := os.MkdirTemp(parentPath, ".harflex-sdd-test-copy-")
	if err != nil {
		return PrivateCopy{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := os.Chmod(staging, 0o700); err != nil {
		return PrivateCopy{}, err
	}
	destination, err := os.OpenRoot(staging)
	if err != nil {
		return PrivateCopy{}, err
	}
	defer destination.Close()
	if err := materializeSnapshot(ctx, sourceRoot, destination, staging, baseline, snapshot.infos, defaultScanLimits, sourceFSID, hooks); err != nil {
		return PrivateCopy{}, err
	}
	keep = true
	return PrivateCopy{Root: staging, Baseline: baseline}, nil
}

func TestCreatePrivateCopyRefusesDestinationInsideSource(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "file.txt"), []byte("source"), 0o600)
	privateParent := filepath.Join(source, "private")
	if err := os.Mkdir(privateParent, 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := Scan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if copy, err := CreatePrivateCopy(context.Background(), source, privateParent); err == nil || copy.Root != "" {
		t.Fatalf("CreatePrivateCopy() = %#v, %v; want rejection without usable root", copy, err)
	}
	after, err := Scan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if before.Hash != after.Hash {
		t.Fatalf("rejected copy modified source: before=%s after=%s", before.Hash, after.Hash)
	}
	contents, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("rejected copy created items under source: %#v", contents)
	}
}
