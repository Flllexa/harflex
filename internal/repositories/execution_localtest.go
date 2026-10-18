package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrLocalTestSourceDirty refuses a local test on a folder with changes of its own: undoing the test could not tell
// them apart from the test's.
var ErrLocalTestSourceDirty = errors.New("the project folder has changes")

// ErrLocalTestBackupMissing is an undo whose backup is gone (the cache was cleaned): nothing is restored.
var ErrLocalTestBackupMissing = errors.New("the local test backup is missing")

// ErrLocalTestNeedsGit refuses a local test outside Git: without it the folder's own changes cannot be told apart
// from the test's, and undo could not tell what to put back.
var ErrLocalTestNeedsGit = errors.New("a local test needs a Git project")

// LocalTestDirtyError lists what keeps a folder from taking a local test.
type LocalTestDirtyError struct{ Paths []string }

func (e *LocalTestDirtyError) Error() string { return ErrLocalTestSourceDirty.Error() }
func (e *LocalTestDirtyError) Is(target error) bool {
	return target == ErrLocalTestSourceDirty
}

// LocalTestBackupEntry is one path the change touches, as it was before the test: its entry, or absent.
type LocalTestBackupEntry struct {
	Path   string                 `json:"path"`
	Absent bool                   `json:"absent"`
	Entry  ExecutionManifestEntry `json:"entry"`
}

// LocalTestApply is what a local test changed in the folder and what undoing it needs.
type LocalTestApply struct {
	Backup []LocalTestBackupEntry `json:"backup"`
	// Applied is every touched path that exists after the change, as the change left it.
	Applied []ExecutionManifestEntry `json:"applied"`
	// CreatedDirs are the folders the change created, parents first.
	CreatedDirs []string `json:"createdDirs"`
	// Baseline is what Git already listed outside the change before the test (untracked files of a nested
	// repository), which undo leaves alone.
	Baseline []string `json:"baseline"`
}

// LocalTestConflict is what changed in the folder during a test besides the change itself: files edited (the
// change's own or tracked ones) and files created that Git does not ignore.
type LocalTestConflict struct {
	Edited  []string `json:"edited"`
	Created []string `json:"created"`
}

func (c LocalTestConflict) empty() bool { return len(c.Edited) == 0 && len(c.Created) == 0 }

const localTestManifest = "manifest.json"

// ApplyExecutionForLocalTest applies a prepared change to its source folder for the person to try, after backing up
// every path it touches into backupRoot. The folder must be clean apart from ignored files.
func ApplyExecutionForLocalTest(ctx context.Context, prepared ExecutionSnapshot, backupRoot string, maxEvidenceBytes int) (LocalTestApply, error) {
	if !prepared.IsGit {
		return LocalTestApply{}, ErrLocalTestNeedsGit
	}
	dirty, _, err := localTestStatus(ctx, prepared)
	if err != nil {
		return LocalTestApply{}, err
	}
	if len(dirty) > 0 {
		return LocalTestApply{}, &LocalTestDirtyError{Paths: dirty}
	}
	finalManifest, err := ExecutionResultManifest(ctx, prepared.Root, prepared)
	if err != nil {
		return LocalTestApply{}, err
	}
	paths := changedManifestPaths(executionManifestByPath(prepared.Manifest), executionManifestByPath(finalManifest))
	if len(paths) == 0 {
		return LocalTestApply{}, ErrExecutionNoChanges
	}
	if err := os.MkdirAll(filepath.Dir(backupRoot), 0o700); err != nil {
		return LocalTestApply{}, fmt.Errorf("create local test backup parent: %w", err)
	}
	if err := os.Mkdir(backupRoot, 0o700); err != nil {
		return LocalTestApply{}, fmt.Errorf("create local test backup: %w", err)
	}
	result, err := backUpLocalTest(ctx, prepared, backupRoot, paths)
	if err == nil {
		var applied []ExecutionManifestEntry
		if applied, err = ApplyExecutionSnapshot(ctx, prepared, maxEvidenceBytes); err == nil {
			result.Applied = applied
			err = writeLocalTestManifest(backupRoot, result)
		}
	}
	if err != nil {
		_ = os.RemoveAll(backupRoot)
		return LocalTestApply{}, err
	}
	return result, nil
}

func backUpLocalTest(ctx context.Context, prepared ExecutionSnapshot, backupRoot string, paths []string) (LocalTestApply, error) {
	var result LocalTestApply
	files := filepath.Join(backupRoot, "files")
	created := map[string]bool{}
	for _, path := range paths {
		if !safeExecutionRelativePath(filepath.FromSlash(path)) {
			return LocalTestApply{}, ErrExecutionSnapshotUnsafePath
		}
		location := prepared.locate(path)
		if _, err := os.Lstat(location.abs()); errors.Is(err, os.ErrNotExist) {
			result.Backup = append(result.Backup, LocalTestBackupEntry{Path: path, Absent: true})
			// The folders the change will create for it, so undo can take them away again.
			for dir := filepath.Dir(location.abs()); pathWithin(location.root, dir) && dir != location.root; dir = filepath.Dir(dir) {
				if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
					break
				}
				created[dir] = true
			}
			continue
		} else if err != nil {
			return LocalTestApply{}, err
		}
		entry, _, err := copyExecutionPath(location.root, location.inner, files, path, maxExecutionSnapshotBytes)
		if err != nil {
			return LocalTestApply{}, err
		}
		entry.Path = path
		result.Backup = append(result.Backup, LocalTestBackupEntry{Path: path, Entry: entry})
	}
	for dir := range created {
		result.CreatedDirs = append(result.CreatedDirs, dir)
	}
	// Parents first: a shorter path is never below a longer one.
	sort.Slice(result.CreatedDirs, func(i, j int) bool { return len(result.CreatedDirs[i]) < len(result.CreatedDirs[j]) })
	if prepared.IsGit {
		_, baseline, err := localTestStatus(ctx, prepared)
		if err != nil {
			return LocalTestApply{}, err
		}
		result.Baseline = baseline
	}
	return result, nil
}

func writeLocalTestManifest(backupRoot string, result LocalTestApply) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(backupRoot, localTestManifest), encoded, 0o600)
}

// localTestStatus lists what Git reports in the project: dirty is what keeps a test out (anything in the project's
// own repository, a tracked change in a nested one); tolerated is the rest (untracked files of nested repositories,
// which the copy also leaves alone). Paths are the project's.
func localTestStatus(ctx context.Context, prepared ExecutionSnapshot) (dirty, tolerated []string, err error) {
	entries, err := localTestStatusEntries(ctx, prepared)
	if err != nil {
		return nil, nil, err
	}
	for _, item := range entries {
		if item.repository >= 0 && item.untracked {
			tolerated = append(tolerated, item.path)
		} else {
			dirty = append(dirty, item.path)
		}
	}
	sort.Strings(dirty)
	sort.Strings(tolerated)
	return dirty, tolerated, nil
}

type localTestStatusEntry struct {
	path       string
	untracked  bool
	repository int
}

func localTestStatusEntries(ctx context.Context, prepared ExecutionSnapshot) ([]localTestStatusEntry, error) {
	var entries []localTestStatusEntry
	read := func(root, prefix string, repository int) error {
		status, truncated, err := gitOutput(ctx, root, 8*1024*1024, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
		if err != nil || truncated {
			return ErrExecutionApplyConflict
		}
		for _, item := range parseStatus(status) {
			path := StatusPath(item)
			if prefix != "" {
				path = prefix + "/" + path
			}
			entries = append(entries, localTestStatusEntry{path: path, untracked: IsUntrackedStatus(item), repository: repository})
		}
		return nil
	}
	if err := read(prepared.SourceRoot, "", -1); err != nil {
		return nil, err
	}
	for index, repository := range prepared.Repositories {
		if err := read(repository.SourceRoot, repository.Path, index); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

// LocalTestConflicts tells what changed in the folder during the test besides the change itself.
func LocalTestConflicts(ctx context.Context, prepared ExecutionSnapshot, applied LocalTestApply) (LocalTestConflict, error) {
	var conflict LocalTestConflict
	after := executionManifestByPath(applied.Applied)
	touched := map[string]bool{}
	for _, item := range applied.Backup {
		touched[item.Path] = true
		current, _, err := prepared.sourceEntry(item.Path, maxExecutionSnapshotBytes)
		if localTestRestored(item, current, err) {
			// Already as it was before the test (an undo that stopped half way): nothing of the person's is here.
			continue
		}
		want, exists := after[item.Path]
		// Edited: gone although the change left it, there although the change removed it, or different.
		if err != nil {
			if exists || !errors.Is(err, os.ErrNotExist) {
				conflict.Edited = append(conflict.Edited, item.Path)
			}
		} else if !exists || current != want {
			conflict.Edited = append(conflict.Edited, item.Path)
		}
	}
	if prepared.IsGit {
		entries, err := localTestStatusEntries(ctx, prepared)
		if err != nil {
			return LocalTestConflict{}, err
		}
		baseline := map[string]bool{}
		for _, path := range applied.Baseline {
			baseline[path] = true
		}
		for _, item := range entries {
			if touched[item.path] || baseline[item.path] {
				continue
			}
			if item.untracked {
				conflict.Created = append(conflict.Created, item.path)
			} else {
				conflict.Edited = append(conflict.Edited, item.path)
			}
		}
	}
	sort.Strings(conflict.Edited)
	sort.Strings(conflict.Created)
	return conflict, nil
}

// UndoExecutionLocalTest puts back every path the change touched, as it was before the test. With changes made during
// the test it stops and reports them, unless discard is set: then they go too (tracked files back to their commit,
// created files removed). Ignored files the test created stay. Without its backup (a cleaned cache), the originals
// come from the commit the folder was on when the test began, which it matched exactly.
func UndoExecutionLocalTest(ctx context.Context, prepared ExecutionSnapshot, backupRoot string, applied LocalTestApply, discard bool) (LocalTestConflict, error) {
	_, statErr := os.Stat(filepath.Join(backupRoot, localTestManifest))
	fromBackup := statErr == nil
	if !fromBackup && !prepared.IsGit {
		return LocalTestConflict{}, ErrLocalTestBackupMissing
	}
	conflict, err := LocalTestConflicts(ctx, prepared, applied)
	if err != nil {
		return LocalTestConflict{}, err
	}
	if !conflict.empty() && !discard {
		return conflict, nil
	}
	// Nothing is removed or written through a folder that became a link to somewhere else.
	for _, item := range applied.Backup {
		if err := localTestPathSafe(prepared, item.Path); err != nil {
			return LocalTestConflict{}, err
		}
	}
	if discard {
		for _, path := range append(append([]string(nil), conflict.Created...), conflict.Edited...) {
			if err := localTestPathSafe(prepared, path); err != nil {
				return LocalTestConflict{}, err
			}
		}
	}
	files := filepath.Join(backupRoot, "files")
	var createdDirs []string
	for _, item := range applied.Backup {
		location := prepared.locate(item.Path)
		if err := os.RemoveAll(location.abs()); err != nil {
			return LocalTestConflict{}, err
		}
		if item.Absent {
			continue
		}
		if _, err := ensureExecutionParents(location.root, location.inner, &createdDirs); err != nil {
			return LocalTestConflict{}, err
		}
		if !fromBackup {
			if err := restoreCommittedPath(ctx, location.root, localTestHead(prepared, location), location.inner); err != nil {
				return LocalTestConflict{}, err
			}
			continue
		}
		if _, _, err := copyExecutionPath(files, item.Path, location.root, location.inner, maxExecutionSnapshotBytes); err != nil {
			return LocalTestConflict{}, err
		}
		if item.Entry.Kind == "file" {
			if err := os.Chmod(location.abs(), os.FileMode(item.Entry.Mode)); err != nil {
				return LocalTestConflict{}, err
			}
		}
	}
	if discard {
		touched := map[string]bool{}
		for _, item := range applied.Backup {
			touched[item.Path] = true
		}
		for _, path := range conflict.Created {
			if err := os.RemoveAll(prepared.locate(path).abs()); err != nil {
				return LocalTestConflict{}, err
			}
		}
		for _, path := range conflict.Edited {
			if touched[path] {
				continue
			}
			location := prepared.locate(path)
			if err := restoreTrackedPath(ctx, location.root, location.inner); err != nil {
				return LocalTestConflict{}, err
			}
		}
	}
	for index := len(applied.CreatedDirs) - 1; index >= 0; index-- {
		_ = os.Remove(applied.CreatedDirs[index])
	}
	// The check is on what the change touched: a branch the person made during the test is theirs to keep.
	for _, item := range applied.Backup {
		current, _, err := prepared.sourceEntry(item.Path, maxExecutionSnapshotBytes)
		if !localTestRestored(item, current, err) {
			return LocalTestConflict{}, ErrExecutionApplyConflict
		}
	}
	return LocalTestConflict{}, os.RemoveAll(backupRoot)
}

// localTestRestored says whether a touched path is as it was before the test.
func localTestRestored(item LocalTestBackupEntry, current ExecutionManifestEntry, err error) bool {
	if item.Absent {
		return errors.Is(err, os.ErrNotExist)
	}
	return err == nil && current == item.Entry
}

// localTestPathSafe refuses a path whose folders inside its repository are not plain folders (a link put there
// during the test would lead outside the project).
func localTestPathSafe(prepared ExecutionSnapshot, path string) error {
	location := prepared.locate(path)
	if !safeExecutionRelativePath(filepath.FromSlash(location.inner)) {
		return ErrExecutionSnapshotUnsafePath
	}
	current := location.root
	parts := strings.Split(filepath.Dir(filepath.FromSlash(location.inner)), string(os.PathSeparator))
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrExecutionSnapshotUnsafePath
		}
	}
	return nil
}

// localTestHead is the commit the repository that owns a path was on when the test began.
func localTestHead(prepared ExecutionSnapshot, location executionLocation) string {
	if location.repository >= 0 {
		return prepared.Repositories[location.repository].Head
	}
	return prepared.SourceHead
}

func restoreCommittedPath(ctx context.Context, root, head, path string) error {
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(callCtx, "git", "restore", "--source="+head, "--worktree", "--", path)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("restore %s from its commit: %w: %s", path, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func restoreTrackedPath(ctx context.Context, root, path string) error {
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(callCtx, "git", "restore", "--source=HEAD", "--staged", "--worktree", "--", path)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("restore %s: %w: %s", path, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// IsIgnored says whether Git ignores a path of the project, asking the repository that owns it (the deepest folder
// with a .git between the path and the project root). Tracked files are never ignored; outside Git nothing is.
func IsIgnored(ctx context.Context, root, slashPath string) (bool, error) {
	relative := filepath.FromSlash(slashPath)
	if !safeExecutionRelativePath(relative) {
		return false, ErrExecutionSnapshotUnsafePath
	}
	full := filepath.Join(root, relative)
	repository := ""
	for dir := filepath.Dir(full); pathWithin(root, dir); dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			repository = dir
			break
		}
		if dir == root {
			break
		}
	}
	if repository == "" {
		return false, nil
	}
	inner, err := filepath.Rel(repository, full)
	if err != nil {
		return false, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	command := exec.CommandContext(callCtx, "git", "check-ignore", "-q", "--", filepath.ToSlash(inner))
	command.Dir = repository
	err = command.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	default:
		return false, fmt.Errorf("check ignored path: %w", err)
	}
}
