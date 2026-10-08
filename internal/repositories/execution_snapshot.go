package repositories

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"
)

const (
	maxExecutionSnapshotFiles = 20_000
	maxExecutionSnapshotBytes = int64(2 << 30)
)

var (
	ErrExecutionSourceDirty              = errors.New("execution source is dirty")
	ErrExecutionCopyConfirmationRequired = errors.New("execution copy confirmation required")
	ErrExecutionSnapshotUnsafePath       = errors.New("execution snapshot contains an unsafe path")
	ErrExecutionSnapshotTooLarge         = errors.New("execution snapshot exceeds safety limits")
)

type ExecutionManifestEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Hash   string `json:"hash"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	Target string `json:"target,omitempty"`
}

type ExecutionSnapshot struct {
	Root          string                   `json:"root"`
	SourceRoot    string                   `json:"sourceRoot"`
	SourceGitRoot string                   `json:"sourceGitRoot,omitempty"`
	SourceHead    string                   `json:"sourceHead,omitempty"`
	SourceBranch  string                   `json:"sourceBranch,omitempty"`
	IsGit         bool                     `json:"isGit"`
	Manifest      []ExecutionManifestEntry `json:"manifest"`
	ExcludedPaths []string                 `json:"excludedPaths,omitempty"`
}

type ExecutionSnapshotPreview struct {
	SourceRoot    string   `json:"sourceRoot"`
	SourceGitRoot string   `json:"sourceGitRoot,omitempty"`
	SourceHead    string   `json:"sourceHead,omitempty"`
	SourceBranch  string   `json:"sourceBranch,omitempty"`
	IsGit         bool     `json:"isGit"`
	FileCount     int      `json:"fileCount"`
	TotalBytes    int64    `json:"totalBytes"`
	ExcludedPaths []string `json:"excludedPaths,omitempty"`
	UnsafePaths   []string `json:"unsafePaths,omitempty"`
}

// PreviewExecutionSnapshot reads file metadata to present the exact source
// perimeter before a non-Git folder is copied for Code.
func PreviewExecutionSnapshot(ctx context.Context, source string) (ExecutionSnapshotPreview, error) {
	if ctx == nil || strings.TrimSpace(source) == "" {
		return ExecutionSnapshotPreview{}, errors.New("execution preview requires context and source")
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		return ExecutionSnapshotPreview{}, fmt.Errorf("resolve execution source: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return ExecutionSnapshotPreview{}, ErrExecutionSnapshotUnsafePath
	}
	preview := ExecutionSnapshotPreview{SourceRoot: canonical}
	snapshot := ExecutionSnapshot{SourceRoot: canonical}
	paths, err := executionSourcePaths(ctx, &snapshot)
	if err != nil {
		return ExecutionSnapshotPreview{}, err
	}
	preview.IsGit, preview.SourceGitRoot, preview.SourceHead, preview.SourceBranch = snapshot.IsGit, snapshot.SourceGitRoot, snapshot.SourceHead, snapshot.SourceBranch
	preview.ExcludedPaths = append([]string(nil), snapshot.ExcludedPaths...)
	preview.FileCount = len(paths)
	for _, slashPath := range paths {
		if err := ctx.Err(); err != nil {
			return ExecutionSnapshotPreview{}, err
		}
		path := filepath.Join(canonical, filepath.FromSlash(slashPath))
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return ExecutionSnapshotPreview{}, fmt.Errorf("inspect execution preview file: %w", err)
		}
		switch {
		case entryInfo.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil || !symlinkStaysWithin(canonical, path, target) {
				preview.UnsafePaths = append(preview.UnsafePaths, filepath.ToSlash(filepath.FromSlash(slashPath)))
				continue
			}
			preview.TotalBytes += int64(len(target))
		case entryInfo.Mode().IsRegular():
			preview.TotalBytes += entryInfo.Size()
		default:
			preview.UnsafePaths = append(preview.UnsafePaths, filepath.ToSlash(filepath.FromSlash(slashPath)))
		}
		if preview.TotalBytes > maxExecutionSnapshotBytes || preview.FileCount > maxExecutionSnapshotFiles {
			return ExecutionSnapshotPreview{}, ErrExecutionSnapshotTooLarge
		}
	}
	sort.Strings(preview.ExcludedPaths)
	sort.Strings(preview.UnsafePaths)
	return preview, nil
}

// PrepareExecutionSnapshot creates a private, independent copy of a clean Git
// workspace. Non-Git folders require explicit confirmation before copying.
func PrepareExecutionSnapshot(ctx context.Context, source string, confirmNonGitCopy bool) (ExecutionSnapshot, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ExecutionSnapshot{}, fmt.Errorf("resolve private execution cache: %w", err)
	}
	return PrepareExecutionSnapshotAt(ctx, source, confirmNonGitCopy, filepath.Join(cache, "Harflex", "sdd-executions"))
}

// PrepareExecutionSnapshotAt is the injectable form used by the application
// and tests to place snapshots under a private, app-owned cache root.
func PrepareExecutionSnapshotAt(ctx context.Context, source string, confirmNonGitCopy bool, cacheRoot string) (ExecutionSnapshot, error) {
	if ctx == nil || strings.TrimSpace(source) == "" || strings.TrimSpace(cacheRoot) == "" {
		return ExecutionSnapshot{}, errors.New("execution snapshot requires context, source and cache root")
	}
	if err := ctx.Err(); err != nil {
		return ExecutionSnapshot{}, err
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		return ExecutionSnapshot{}, fmt.Errorf("resolve execution source: %w", err)
	}
	sourceInfo, err := os.Stat(canonical)
	if err != nil || !sourceInfo.IsDir() {
		return ExecutionSnapshot{}, ErrExecutionSnapshotUnsafePath
	}
	canonicalCache, err := filepath.Abs(cacheRoot)
	if err != nil {
		return ExecutionSnapshot{}, fmt.Errorf("resolve execution cache: %w", err)
	}
	if samePath(canonical, canonicalCache) || pathWithin(canonical, canonicalCache) || pathWithin(canonicalCache, canonical) {
		return ExecutionSnapshot{}, ErrExecutionSnapshotUnsafePath
	}

	preview := ExecutionSnapshot{SourceRoot: canonical}
	paths, err := executionSourcePaths(ctx, &preview)
	if err != nil {
		return ExecutionSnapshot{}, err
	}
	if !preview.IsGit && !confirmNonGitCopy {
		return ExecutionSnapshot{}, ErrExecutionCopyConfirmationRequired
	}
	if len(paths) > maxExecutionSnapshotFiles {
		return ExecutionSnapshot{}, ErrExecutionSnapshotTooLarge
	}
	if err := os.MkdirAll(canonicalCache, 0o700); err != nil {
		return ExecutionSnapshot{}, fmt.Errorf("create private execution cache: %w", err)
	}
	if err := os.Chmod(canonicalCache, 0o700); err != nil {
		return ExecutionSnapshot{}, fmt.Errorf("protect private execution cache: %w", err)
	}
	root, err := os.MkdirTemp(canonicalCache, "sdd-code-")
	if err != nil {
		return ExecutionSnapshot{}, fmt.Errorf("create isolated execution root: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return ExecutionSnapshot{}, fmt.Errorf("protect isolated execution root: %w", err)
	}
	prepared := preview
	prepared.Root = root
	prepared.Manifest = make([]ExecutionManifestEntry, 0, len(paths))
	var total int64
	for _, relative := range paths {
		if err := ctx.Err(); err != nil {
			_ = os.RemoveAll(root)
			return ExecutionSnapshot{}, err
		}
		entry, size, copyErr := copyExecutionPath(canonical, root, relative, maxExecutionSnapshotBytes-total)
		if copyErr != nil {
			_ = os.RemoveAll(root)
			return ExecutionSnapshot{}, copyErr
		}
		total += size
		if total > maxExecutionSnapshotBytes {
			_ = os.RemoveAll(root)
			return ExecutionSnapshot{}, ErrExecutionSnapshotTooLarge
		}
		prepared.Manifest = append(prepared.Manifest, entry)
	}
	sort.Slice(prepared.Manifest, func(i, j int) bool { return prepared.Manifest[i].Path < prepared.Manifest[j].Path })
	return prepared, nil
}

func executionSourcePaths(ctx context.Context, snapshot *ExecutionSnapshot) ([]string, error) {
	inspected, err := Inspect(ctx, snapshot.SourceRoot)
	if err != nil {
		if errors.Is(err, ErrGitUnavailable) {
			return walkExecutionPaths(snapshot.SourceRoot, snapshot)
		}
		return nil, err
	}
	if !inspected.IsRepository {
		return walkExecutionPaths(snapshot.SourceRoot, snapshot)
	}
	snapshot.IsGit = true
	snapshot.SourceGitRoot = inspected.Root
	if inspected.Truncated || len(inspected.Files) > 0 || inspected.StagedDiff != "" || inspected.UnstagedDiff != "" {
		return nil, ErrExecutionSourceDirty
	}
	head, _, err := gitOutput(ctx, inspected.Root, 256, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, ErrExecutionSourceDirty
	}
	snapshot.SourceHead = strings.TrimSpace(head)
	branch, _, err := gitOutput(ctx, inspected.Root, 4096, "branch", "--show-current")
	if err != nil {
		return nil, err
	}
	snapshot.SourceBranch = strings.TrimSpace(branch)
	if snapshot.SourceBranch == "" {
		snapshot.SourceBranch = "HEAD destacado"
	}
	excluded, err := gitExcludedPaths(ctx, inspected.Root)
	if err != nil {
		return nil, err
	}
	snapshot.ExcludedPaths = append(snapshot.ExcludedPaths, excluded...)
	tracked, truncated, err := gitOutput(ctx, inspected.Root, 8*1024*1024, "ls-files", "-z", "--stage", "--cached")
	if err != nil || truncated {
		return nil, ErrExecutionSnapshotTooLarge
	}
	paths := make([]string, 0)
	for _, raw := range strings.Split(tracked, "\x00") {
		if raw == "" {
			continue
		}
		tab := strings.IndexByte(raw, '\t')
		if tab <= 0 || tab == len(raw)-1 {
			return nil, ErrExecutionSnapshotUnsafePath
		}
		metadata, name := raw[:tab], raw[tab+1:]
		mode := strings.Fields(metadata)
		if len(mode) != 3 {
			return nil, ErrExecutionSnapshotUnsafePath
		}
		if mode[0] == "160000" {
			return nil, ErrExecutionSnapshotUnsafePath
		}
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths, nil
}

func gitExcludedPaths(ctx context.Context, root string) ([]string, error) {
	ignored, truncated, err := gitOutput(ctx, root, 8*1024*1024, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil || truncated {
		return nil, ErrExecutionSnapshotTooLarge
	}
	paths := []string{".git"}
	for _, path := range strings.Split(ignored, "\x00") {
		path = strings.TrimSuffix(path, "/")
		if path != "" && safeExecutionRelativePath(filepath.FromSlash(path)) {
			paths = append(paths, filepath.ToSlash(path))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func walkExecutionPaths(root string, snapshot *ExecutionSnapshot) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return ErrExecutionSnapshotUnsafePath
		}
		if entry.Name() == ".git" {
			snapshot.ExcludedPaths = append(snapshot.ExcludedPaths, filepath.ToSlash(relative))
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !safeExecutionRelativePath(relative) {
			return ErrExecutionSnapshotUnsafePath
		}
		if entry.IsDir() {
			return nil
		}
		paths = append(paths, filepath.ToSlash(relative))
		if len(paths) > maxExecutionSnapshotFiles {
			return ErrExecutionSnapshotTooLarge
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func copyExecutionPath(sourceRoot, destinationRoot, slashPath string, maxBytes int64) (ExecutionManifestEntry, int64, error) {
	if maxBytes < 0 || !safeExecutionRelativePath(filepath.FromSlash(slashPath)) {
		return ExecutionManifestEntry{}, 0, ErrExecutionSnapshotUnsafePath
	}
	relative := filepath.FromSlash(slashPath)
	source := filepath.Join(sourceRoot, relative)
	destination := filepath.Join(destinationRoot, relative)
	if !pathWithin(sourceRoot, source) || !pathWithin(destinationRoot, destination) {
		return ExecutionManifestEntry{}, 0, ErrExecutionSnapshotUnsafePath
	}
	info, err := os.Lstat(source)
	if err != nil {
		return ExecutionManifestEntry{}, 0, fmt.Errorf("inspect execution source entry: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return ExecutionManifestEntry{}, 0, fmt.Errorf("create execution copy parent: %w", err)
	}
	entry := ExecutionManifestEntry{Path: filepath.ToSlash(relative), Mode: uint32(info.Mode().Perm())}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(source)
		if err != nil || !symlinkStaysWithin(sourceRoot, source, target) {
			return ExecutionManifestEntry{}, 0, ErrExecutionSnapshotUnsafePath
		}
		if err := os.Symlink(target, destination); err != nil {
			return ExecutionManifestEntry{}, 0, fmt.Errorf("copy execution symlink: %w", err)
		}
		digest := sha256.Sum256([]byte(target))
		entry.Kind, entry.Hash, entry.Size, entry.Target = "symlink", hex.EncodeToString(digest[:]), int64(len(target)), target
		if entry.Size > maxBytes {
			return ExecutionManifestEntry{}, 0, ErrExecutionSnapshotTooLarge
		}
		return entry, entry.Size, nil
	case info.Mode().IsRegular():
		input, err := os.Open(source)
		if err != nil {
			return ExecutionManifestEntry{}, 0, fmt.Errorf("open execution source file: %w", err)
		}
		opened, statErr := input.Stat()
		current, lstatErr := os.Lstat(source)
		if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || !os.SameFile(info, current) {
			_ = input.Close()
			return ExecutionManifestEntry{}, 0, ErrExecutionSnapshotUnsafePath
		}
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return ExecutionManifestEntry{}, 0, fmt.Errorf("create execution copy file: %w", err)
		}
		hasher := sha256.New()
		copied, copyErr := io.Copy(io.MultiWriter(output, hasher), io.LimitReader(input, maxBytes+1))
		closeInputErr := input.Close()
		closeOutputErr := output.Close()
		if copied > maxBytes {
			return ExecutionManifestEntry{}, copied, ErrExecutionSnapshotTooLarge
		}
		if err := errors.Join(copyErr, closeInputErr, closeOutputErr); err != nil {
			return ExecutionManifestEntry{}, copied, fmt.Errorf("copy execution source file: %w", err)
		}
		entry.Kind, entry.Hash, entry.Size = "file", hex.EncodeToString(hasher.Sum(nil)), copied
		return entry, copied, nil
	default:
		return ExecutionManifestEntry{}, 0, ErrExecutionSnapshotUnsafePath
	}
}

func safeExecutionRelativePath(path string) bool {
	clean := filepath.Clean(path)
	return path != "" && !filepath.IsAbs(path) && clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(os.PathSeparator)) && clean != ".git" && !strings.HasPrefix(clean, ".git"+string(os.PathSeparator))
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func symlinkStaysWithin(root, link, target string) bool {
	if filepath.IsAbs(target) {
		return false
	}
	lexical := filepath.Clean(filepath.Join(filepath.Dir(link), target))
	if !pathWithin(root, lexical) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err == nil {
		return pathWithin(root, resolved)
	}
	return errors.Is(err, os.ErrNotExist)
}

// ExecutionHead returns the full commit OID and repository root for a clean
// registered workspace. It does not create a worktree or execute project code.
func ExecutionHead(ctx context.Context, root string) (string, string, error) {
	inspected, err := Inspect(ctx, root)
	if err != nil {
		return "", "", err
	}
	if !inspected.IsRepository {
		return "", "", ErrGitUnavailable
	}
	if inspected.Truncated || len(inspected.Files) > 0 || inspected.StagedDiff != "" || inspected.UnstagedDiff != "" {
		return "", "", ErrExecutionSourceDirty
	}
	head, _, err := gitOutput(ctx, inspected.Root, 256, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", "", ErrExecutionSourceDirty
	}
	return strings.TrimSpace(head), inspected.Root, nil
}

// ExecutionGitIdentity returns the source repository identity even after the
// explicit patch-application step has made the worktree dirty.
func ExecutionGitIdentity(ctx context.Context, root string) (head, gitRoot, branch string, err error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve Git execution source: %w", err)
	}
	gitRoot, _, err = gitOutput(ctx, canonical, 4096, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", "", ErrGitUnavailable
	}
	gitRoot = strings.TrimSpace(gitRoot)
	if !samePath(gitRoot, canonical) {
		return "", "", "", ErrGitUnavailable
	}
	head, _, err = gitOutput(ctx, canonical, 256, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", "", "", ErrExecutionSourceDirty
	}
	branch, _, err = gitOutput(ctx, canonical, 4096, "branch", "--show-current")
	if err != nil {
		return "", "", "", err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "HEAD destacado"
	}
	return strings.TrimSpace(head), gitRoot, branch, nil
}

// ExecutionManifest captures regular files and symlink targets under root
// without following symlink directories. A named Git metadata entry is never
// included in an execution root.
func ExecutionManifest(ctx context.Context, root string) ([]ExecutionManifestEntry, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve execution root: %w", err)
	}
	paths, err := walkExecutionPaths(canonical, &ExecutionSnapshot{SourceRoot: canonical})
	if err != nil {
		return nil, err
	}
	manifest := make([]ExecutionManifestEntry, 0, len(paths))
	total := int64(0)
	for _, relative := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry, size, err := executionManifestPath(canonical, relative, maxExecutionSnapshotBytes-total)
		if err != nil {
			return nil, err
		}
		total += size
		manifest = append(manifest, entry)
	}
	return manifest, nil
}

func executionManifestPath(root, slashPath string, maxBytes int64) (ExecutionManifestEntry, int64, error) {
	if maxBytes < 0 || !safeExecutionRelativePath(filepath.FromSlash(slashPath)) {
		return ExecutionManifestEntry{}, 0, ErrExecutionSnapshotUnsafePath
	}
	path := filepath.Join(root, filepath.FromSlash(slashPath))
	info, err := os.Lstat(path)
	if err != nil {
		return ExecutionManifestEntry{}, 0, err
	}
	entry := ExecutionManifestEntry{Path: filepath.ToSlash(filepath.FromSlash(slashPath)), Mode: uint32(info.Mode().Perm())}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil || !symlinkStaysWithin(root, path, target) {
			return ExecutionManifestEntry{}, 0, ErrExecutionSnapshotUnsafePath
		}
		digest := sha256.Sum256([]byte(target))
		entry.Kind, entry.Hash, entry.Size, entry.Target = "symlink", hex.EncodeToString(digest[:]), int64(len(target)), target
		if entry.Size > maxBytes {
			return ExecutionManifestEntry{}, entry.Size, ErrExecutionSnapshotTooLarge
		}
		return entry, entry.Size, nil
	}
	if !info.Mode().IsRegular() {
		return ExecutionManifestEntry{}, 0, ErrExecutionSnapshotUnsafePath
	}
	file, err := os.Open(path)
	if err != nil {
		return ExecutionManifestEntry{}, 0, err
	}
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || !os.SameFile(info, current) {
		_ = file.Close()
		return ExecutionManifestEntry{}, 0, ErrExecutionSnapshotUnsafePath
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(hasher, io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if size > maxBytes {
		return ExecutionManifestEntry{}, size, ErrExecutionSnapshotTooLarge
	}
	if err := errors.Join(copyErr, closeErr); err != nil {
		return ExecutionManifestEntry{}, size, err
	}
	entry.Kind, entry.Hash, entry.Size = "file", hex.EncodeToString(hasher.Sum(nil)), size
	return entry, size, nil
}

// ExecutionSourceStillMatches checks source commit/root and the exact file
// manifest captured before Code. It fails closed on added, removed or changed
// paths and on Git branch/HEAD changes.
func ExecutionSourceStillMatches(ctx context.Context, prepared ExecutionSnapshot) (bool, error) {
	if prepared.SourceRoot == "" || prepared.Root == "" {
		return false, ErrExecutionSnapshotUnsafePath
	}
	if prepared.IsGit {
		inspected, err := Inspect(ctx, prepared.SourceRoot)
		if err != nil {
			return false, err
		}
		branch := strings.TrimSpace(inspected.Branch)
		if branch == "" {
			branch = "HEAD destacado"
		}
		if branch != prepared.SourceBranch {
			return false, nil
		}
		head, gitRoot, err := ExecutionHead(ctx, prepared.SourceRoot)
		if err != nil {
			return false, err
		}
		if head != prepared.SourceHead || !samePath(gitRoot, prepared.SourceGitRoot) {
			return false, nil
		}
		paths, err := executionSourcePaths(ctx, &ExecutionSnapshot{SourceRoot: prepared.SourceRoot})
		if err != nil {
			return false, err
		}
		current := ExecutionSnapshot{SourceRoot: prepared.SourceRoot}
		for _, relative := range paths {
			entry, _, err := executionManifestPath(prepared.SourceRoot, relative, maxExecutionSnapshotBytes)
			if err != nil {
				return false, err
			}
			current.Manifest = append(current.Manifest, entry)
		}
		return manifestsEqual(prepared.Manifest, current.Manifest), nil
	}
	current, err := ExecutionManifest(ctx, prepared.SourceRoot)
	if err != nil {
		return false, err
	}
	return manifestsEqual(prepared.Manifest, current), nil
}

// ExecutionSnapshotApplied confirms that the original workspace now reflects
// the isolated result while the recorded HEAD and branch still match.
func ExecutionSnapshotApplied(ctx context.Context, prepared ExecutionSnapshot) (bool, error) {
	if prepared.SourceRoot == "" || prepared.Root == "" {
		return false, ErrExecutionSnapshotUnsafePath
	}
	finalManifest, err := ExecutionManifest(ctx, prepared.Root)
	if err != nil {
		return false, err
	}
	if !prepared.IsGit {
		current, err := ExecutionManifest(ctx, prepared.SourceRoot)
		if err != nil {
			return false, err
		}
		return manifestsEqual(finalManifest, current), nil
	}
	head, gitRoot, branch, err := ExecutionGitIdentity(ctx, prepared.SourceRoot)
	if err != nil || head != prepared.SourceHead || branch != prepared.SourceBranch || !samePath(gitRoot, prepared.SourceGitRoot) {
		return false, nil
	}
	before := executionManifestByPath(prepared.Manifest)
	after := executionManifestByPath(finalManifest)
	for path, expected := range after {
		current, _, err := executionManifestPath(prepared.SourceRoot, path, maxExecutionSnapshotBytes)
		if err != nil || current != expected {
			return false, nil
		}
	}
	for path := range before {
		if _, remains := after[path]; remains {
			continue
		}
		if _, err := os.Lstat(filepath.Join(prepared.SourceRoot, filepath.FromSlash(path))); !errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
	}
	changed := changedManifestPaths(before, after)
	return verifyAppliedGitStatus(ctx, prepared, before, after, changed) == nil, nil
}

// ExecutionDiff builds attributable evidence from the source snapshot and
// isolated execution root. It includes additions, removals, content, modes,
// and symlink targets; it never consults Git diff drivers or filters.
func ExecutionDiff(ctx context.Context, prepared ExecutionSnapshot, maxBytes int) (string, []ExecutionManifestEntry, error) {
	if maxBytes <= 0 {
		return "", nil, errors.New("invalid execution evidence limit")
	}
	sourceMatches, err := ExecutionSourceStillMatches(ctx, prepared)
	if err != nil {
		if errors.Is(err, ErrExecutionSourceDirty) {
			return "", nil, ErrExecutionSourceDrift
		}
		return "", nil, err
	}
	if !sourceMatches {
		return "", nil, ErrExecutionSourceDrift
	}
	current, err := ExecutionManifest(ctx, prepared.Root)
	if err != nil {
		return "", nil, err
	}
	before := make(map[string]ExecutionManifestEntry, len(prepared.Manifest))
	after := make(map[string]ExecutionManifestEntry, len(current))
	for _, entry := range prepared.Manifest {
		before[entry.Path] = entry
	}
	for _, entry := range current {
		after[entry.Path] = entry
	}
	paths := make([]string, 0, len(before)+len(after))
	seen := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for path := range after {
		if _, exists := seen[path]; !exists {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	changed := make([]ExecutionManifestEntry, 0)
	var output strings.Builder
	for _, path := range paths {
		oldEntry, hadOld := before[path]
		newEntry, hasNew := after[path]
		if hadOld && hasNew && oldEntry == newEntry {
			continue
		}
		var oldContent, newContent []byte
		if hadOld {
			oldContent, err = readExecutionContent(prepared.SourceRoot, oldEntry, maxBytes-output.Len())
			if err != nil {
				return "", nil, err
			}
		}
		if hasNew {
			newContent, err = readExecutionContent(prepared.Root, newEntry, maxBytes-output.Len())
			if err != nil {
				return "", nil, err
			}
		}
		diff, err := formatExecutionDiff(path, oldEntry, hadOld, oldContent, newEntry, hasNew, newContent)
		if err != nil {
			return "", nil, err
		}
		if len(diff) > maxBytes-output.Len() {
			return "", nil, ErrExecutionEvidenceTooLarge
		}
		output.WriteString(diff)
		if hasNew {
			changed = append(changed, newEntry)
		} else {
			changed = append(changed, oldEntry)
		}
	}
	return output.String(), changed, nil
}

var ErrExecutionSourceDrift = errors.New("registered project changed during isolated Code execution")
var ErrExecutionEvidenceTooLarge = errors.New("isolated Code evidence exceeds the safety limit")

func readExecutionContent(root string, entry ExecutionManifestEntry, maxBytes int) ([]byte, error) {
	if maxBytes < 0 || entry.Size > int64(maxBytes) {
		return nil, ErrExecutionEvidenceTooLarge
	}
	if entry.Kind == "symlink" {
		return []byte(entry.Target), nil
	}
	if entry.Kind != "file" {
		return nil, ErrExecutionSnapshotUnsafePath
	}
	path := filepath.Join(root, filepath.FromSlash(entry.Path))
	if !pathWithin(root, path) {
		return nil, ErrExecutionSnapshotUnsafePath
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrExecutionSnapshotUnsafePath
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || !os.SameFile(info, current) {
		_ = file.Close()
		return nil, ErrExecutionSnapshotUnsafePath
	}
	content, readErr := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	closeErr := file.Close()
	if len(content) > maxBytes {
		return nil, ErrExecutionEvidenceTooLarge
	}
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	return content, nil
}

func formatExecutionDiff(path string, oldEntry ExecutionManifestEntry, hadOld bool, oldContent []byte, newEntry ExecutionManifestEntry, hasNew bool, newContent []byte) (string, error) {
	var output strings.Builder
	output.WriteString("diff --git a/")
	output.WriteString(path)
	output.WriteString(" b/")
	output.WriteString(path)
	output.WriteByte('\n')
	mode := func(entry ExecutionManifestEntry) string {
		if entry.Kind == "symlink" {
			return "120000"
		}
		if entry.Mode&0o111 != 0 {
			return "100755"
		}
		return "100644"
	}
	if !hadOld {
		output.WriteString("new file mode " + mode(newEntry) + "\n")
	} else if !hasNew {
		output.WriteString("deleted file mode " + mode(oldEntry) + "\n")
	} else if mode(oldEntry) != mode(newEntry) {
		output.WriteString("old mode " + mode(oldEntry) + "\nnew mode " + mode(newEntry) + "\n")
	} else if oldEntry.Mode != newEntry.Mode {
		_, _ = fmt.Fprintf(&output, "mode changed from %04o to %04o\n", oldEntry.Mode, newEntry.Mode)
	}
	if hadOld && hasNew && oldEntry.Hash == newEntry.Hash && oldEntry.Kind == newEntry.Kind {
		return output.String(), nil
	}
	if bytesContainBinary(oldContent) || bytesContainBinary(newContent) {
		from, to := "/dev/null", "/dev/null"
		if hadOld {
			from = "a/" + path
		}
		if hasNew {
			to = "b/" + path
		}
		output.WriteString("Binary files " + from + " and " + to + " differ\n")
		return output.String(), nil
	}
	from, to := "/dev/null", "/dev/null"
	if hadOld {
		from = "a/" + path
	}
	if hasNew {
		to = "b/" + path
	}
	textDiff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: diffLines(string(oldContent)), B: diffLines(string(newContent)), FromFile: from, ToFile: to, Context: 3,
	})
	if err != nil {
		return "", fmt.Errorf("format isolated Code diff: %w", err)
	}
	output.WriteString(textDiff)
	return output.String(), nil
}

func diffLines(value string) []string {
	if value == "" {
		return nil
	}
	lines := strings.SplitAfter(value, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func bytesContainBinary(value []byte) bool {
	return !utf8.Valid(value) || strings.IndexByte(string(value), 0) >= 0
}

func manifestsEqual(left, right []ExecutionManifestEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
