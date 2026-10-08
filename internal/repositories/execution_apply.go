package repositories

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	ErrExecutionApplyConflict = errors.New("execution patch conflicts with the registered workspace")
	ErrExecutionNoChanges     = errors.New("isolated Code execution produced no changes")
)

type stagedExecutionChange struct {
	path      string
	oldEntry  ExecutionManifestEntry
	hadOld    bool
	newEntry  ExecutionManifestEntry
	hasNew    bool
	stageDir  string
	stagePath string
	applied   bool
}

// ApplyExecutionSnapshot applies an already-reviewed isolated patch to the
// original workspace. It preflights changed paths, stages same-volume
// replacements, and rolls back changed paths on an apply or readback failure.
func ApplyExecutionSnapshot(ctx context.Context, prepared ExecutionSnapshot, maxEvidenceBytes int) ([]ExecutionManifestEntry, error) {
	finalManifest, err := ExecutionManifest(ctx, prepared.Root)
	if err != nil {
		return nil, err
	}
	if applied, err := ExecutionSnapshotApplied(ctx, prepared); err == nil && applied {
		return finalManifest, nil
	}
	diff, _, err := ExecutionDiff(ctx, prepared, maxEvidenceBytes)
	if err != nil {
		return nil, err
	}
	if diff == "" {
		return nil, ErrExecutionNoChanges
	}
	before := executionManifestByPath(prepared.Manifest)
	after := executionManifestByPath(finalManifest)
	paths := changedManifestPaths(before, after)
	if len(paths) == 0 {
		return nil, ErrExecutionNoChanges
	}
	changes := make([]stagedExecutionChange, 0, len(paths))
	for _, path := range paths {
		if !safeExecutionRelativePath(filepath.FromSlash(path)) {
			return nil, ErrExecutionSnapshotUnsafePath
		}
		oldEntry, hadOld := before[path]
		newEntry, hasNew := after[path]
		if hadOld {
			current, _, err := executionManifestPath(prepared.SourceRoot, path, maxExecutionSnapshotBytes)
			if err != nil || current != oldEntry {
				return nil, ErrExecutionApplyConflict
			}
		} else if _, err := os.Lstat(filepath.Join(prepared.SourceRoot, filepath.FromSlash(path))); !errors.Is(err, os.ErrNotExist) {
			return nil, ErrExecutionApplyConflict
		}
		changes = append(changes, stagedExecutionChange{path: path, oldEntry: oldEntry, hadOld: hadOld, newEntry: newEntry, hasNew: hasNew})
	}
	if matches, err := ExecutionSourceStillMatches(ctx, prepared); err != nil || !matches {
		return nil, ErrExecutionApplyConflict
	}

	backupRoot, err := os.MkdirTemp(filepath.Dir(prepared.Root), "sdd-apply-backup-")
	if err != nil {
		return nil, fmt.Errorf("prepare patch rollback: %w", err)
	}
	if err := os.Chmod(backupRoot, 0o700); err != nil {
		_ = os.RemoveAll(backupRoot)
		return nil, fmt.Errorf("protect patch rollback: %w", err)
	}
	defer os.RemoveAll(backupRoot)
	createdDirs := make([]string, 0)
	cleanupStaging := func() {
		for _, change := range changes {
			if change.stageDir != "" {
				_ = os.RemoveAll(change.stageDir)
			}
		}
	}
	removeEmptyDirs := func() {
		for i := len(createdDirs) - 1; i >= 0; i-- {
			if err := os.Remove(createdDirs[i]); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrExist) {
				continue
			}
		}
	}
	rollback := func() error {
		var rollbackErr error
		for i := len(changes) - 1; i >= 0; i-- {
			change := changes[i]
			if !change.applied {
				continue
			}
			destination := filepath.Join(prepared.SourceRoot, filepath.FromSlash(change.path))
			if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, err)
				continue
			}
			if change.hadOld {
				if _, err := ensureExecutionParents(prepared.SourceRoot, change.path, &createdDirs); err != nil {
					rollbackErr = errors.Join(rollbackErr, err)
					continue
				}
				if _, _, err := copyExecutionPath(backupRoot, prepared.SourceRoot, change.path, maxExecutionSnapshotBytes); err != nil {
					rollbackErr = errors.Join(rollbackErr, err)
				}
			}
		}
		removeEmptyDirs()
		return rollbackErr
	}
	fail := func(cause error) ([]ExecutionManifestEntry, error) {
		rollbackErr := rollback()
		cleanupStaging()
		if rollbackErr != nil {
			return nil, errors.Join(cause, fmt.Errorf("rollback incomplete patch application: %w", rollbackErr))
		}
		return nil, cause
	}

	var backupBytes, stagedBytes int64
	for i := range changes {
		change := &changes[i]
		if change.hadOld {
			_, size, err := copyExecutionPath(prepared.SourceRoot, backupRoot, change.path, maxExecutionSnapshotBytes-backupBytes)
			if err != nil {
				cleanupStaging()
				removeEmptyDirs()
				return nil, fmt.Errorf("prepare patch rollback: %w", err)
			}
			backupBytes += size
		}
		if !change.hasNew {
			continue
		}
		if _, err := ensureExecutionParents(prepared.SourceRoot, change.path, &createdDirs); err != nil {
			cleanupStaging()
			removeEmptyDirs()
			return nil, err
		}
		destination := filepath.Join(prepared.SourceRoot, filepath.FromSlash(change.path))
		stageDir, err := os.MkdirTemp(filepath.Dir(destination), ".harflex-stage-")
		if err != nil {
			cleanupStaging()
			removeEmptyDirs()
			return nil, fmt.Errorf("stage patch file: %w", err)
		}
		change.stageDir = stageDir
		_, size, err := copyExecutionPath(prepared.Root, stageDir, change.path, maxExecutionSnapshotBytes-stagedBytes)
		if err != nil {
			cleanupStaging()
			removeEmptyDirs()
			return nil, fmt.Errorf("stage isolated patch file: %w", err)
		}
		stagedBytes += size
		change.stagePath = filepath.Join(stageDir, filepath.FromSlash(change.path))
	}

	for i := range changes {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		change := &changes[i]
		destination := filepath.Join(prepared.SourceRoot, filepath.FromSlash(change.path))
		if change.hadOld {
			current, _, err := executionManifestPath(prepared.SourceRoot, change.path, maxExecutionSnapshotBytes)
			if err != nil || current != change.oldEntry {
				return fail(ErrExecutionApplyConflict)
			}
		} else if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
			return fail(ErrExecutionApplyConflict)
		}
		if change.hasNew {
			if err := os.Rename(change.stagePath, destination); err != nil {
				return fail(fmt.Errorf("apply patch file %q: %w", change.path, err))
			}
		} else if err := os.Remove(destination); err != nil {
			return fail(fmt.Errorf("remove patch file %q: %w", change.path, err))
		}
		change.applied = true
	}
	cleanupStaging()

	for _, change := range changes {
		path := filepath.Join(prepared.SourceRoot, filepath.FromSlash(change.path))
		if change.hasNew {
			current, _, err := executionManifestPath(prepared.SourceRoot, change.path, maxExecutionSnapshotBytes)
			if err != nil || current != change.newEntry {
				return fail(ErrExecutionApplyConflict)
			}
		} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return fail(ErrExecutionApplyConflict)
		}
	}
	if prepared.IsGit {
		if err := verifyAppliedGitStatus(ctx, prepared, before, after, paths); err != nil {
			return fail(err)
		}
	} else {
		readback, err := ExecutionManifest(ctx, prepared.SourceRoot)
		if err != nil || !manifestsEqual(finalManifest, readback) {
			return fail(ErrExecutionApplyConflict)
		}
	}
	return finalManifest, nil
}

func executionManifestByPath(manifest []ExecutionManifestEntry) map[string]ExecutionManifestEntry {
	byPath := make(map[string]ExecutionManifestEntry, len(manifest))
	for _, entry := range manifest {
		byPath[entry.Path] = entry
	}
	return byPath
}

func changedManifestPaths(before, after map[string]ExecutionManifestEntry) []string {
	seen := make(map[string]struct{}, len(before)+len(after))
	paths := make([]string, 0, len(before)+len(after))
	for path, oldEntry := range before {
		seen[path] = struct{}{}
		if newEntry, ok := after[path]; !ok || oldEntry != newEntry {
			paths = append(paths, path)
		}
	}
	for path := range after {
		if _, exists := seen[path]; !exists {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func ensureExecutionParents(root, slashPath string, created *[]string) (string, error) {
	relative := filepath.FromSlash(slashPath)
	if !safeExecutionRelativePath(relative) {
		return "", ErrExecutionSnapshotUnsafePath
	}
	directory := filepath.Dir(relative)
	if directory == "." {
		return filepath.Join(root, relative), nil
	}
	current := root
	for _, part := range strings.Split(directory, string(os.PathSeparator)) {
		if part == "" || part == "." || part == ".." {
			return "", ErrExecutionSnapshotUnsafePath
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", ErrExecutionSnapshotUnsafePath
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := os.Mkdir(current, 0o700); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return "", err
			}
			info, statErr := os.Lstat(current)
			if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", ErrExecutionSnapshotUnsafePath
			}
			continue
		}
		*created = append(*created, current)
	}
	return filepath.Join(root, relative), nil
}

func verifyAppliedGitStatus(ctx context.Context, prepared ExecutionSnapshot, before, after map[string]ExecutionManifestEntry, changed []string) error {
	head, gitRoot, branch, err := ExecutionGitIdentity(ctx, prepared.SourceRoot)
	if err != nil || head != prepared.SourceHead || branch != prepared.SourceBranch || !samePath(gitRoot, prepared.SourceGitRoot) {
		return ErrExecutionApplyConflict
	}
	allowed := make(map[string]struct{}, len(changed))
	for _, path := range changed {
		allowed[path] = struct{}{}
	}
	status, truncated, err := gitOutput(ctx, prepared.SourceRoot, 8*1024*1024, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil || truncated {
		return ErrExecutionApplyConflict
	}
	found := make(map[string]struct{})
	for _, item := range parseStatus(status) {
		path := StatusPath(item)
		if _, ok := allowed[path]; !ok {
			return ErrExecutionApplyConflict
		}
		found[path] = struct{}{}
	}
	for _, path := range changed {
		oldEntry, hadOld := before[path]
		newEntry, hasNew := after[path]
		if hadOld && (!hasNew || oldEntry != newEntry) {
			if _, ok := found[path]; !ok {
				return ErrExecutionApplyConflict
			}
		}
	}
	return nil
}
