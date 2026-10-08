package sddworkspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// PrivateCopy is a completed, independent copy. The root is returned only
// after the source has been re-read and both trees match the captured baseline.
type PrivateCopy struct {
	Root     string   `json:"root"`
	Baseline Manifest `json:"baseline"`
}

func cleanupPartialPrivateCopy(parent *os.Root, name, displayPath string, cause error) error {
	if name == "" {
		return cause
	}
	if err := parent.RemoveAll(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		cleanupErr := fmt.Errorf("%w: remove partial private copy %q: %v", ErrPrivateCopyCleanup, displayPath, err)
		return errors.Join(cause, cleanupErr)
	}
	return cause
}

func newPrivateStageName() (string, error) {
	var randomBytes [16]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return "", err
	}
	return ".harflex-sdd-copy-" + hex.EncodeToString(randomBytes[:]), nil
}

type copyHooks struct {
	afterFile      func(relativePath string)
	beforeLeafOpen func(Entry, *os.Root) error
}

type createCopyHooks struct {
	beforeStageMkdir func() error
	afterFile        func(relativePath string)
	beforeLeafOpen   func(Entry, *os.Root) error
}

// CreatePrivateCopy copies a project into a new private directory below
// privateParent. It rejects and rechecks a parent found inside sourceRoot before
// staging, follows no symlinks, and returns no usable root until drift checks
// succeed. A concurrent rename by the same UID is outside that atomic guarantee.
// It permits materialization on Linux and on macOS with cgo when owner-only
// permissions, effective write/search access, ACLs, and filesystem boundaries
// can be verified; other platform configurations fail closed. Preflight does not
// reserve disk space or quota, so creation can still fail with ENOSPC or quota
// errors after its safety checks are repeated.
func CreatePrivateCopy(ctx context.Context, sourceRoot, privateParent string) (PrivateCopy, error) {
	return createPrivateCopy(ctx, sourceRoot, privateParent, defaultScanLimits, createCopyHooks{})
}

func createPrivateCopy(ctx context.Context, sourceRoot, privateParent string, limits scanLimits, hooks createCopyHooks) (result PrivateCopy, err error) {
	return createPrivateCopyWithDestination(ctx, sourceRoot, privateParent, "", nil, limits, hooks)
}

// PrivateCopyDestination returns the canonical direct child path planned for a
// private copy. The name is a single path component; existence is checked again
// atomically when CreatePrivateCopyAt creates the directory.
func PrivateCopyDestination(privateParent, name string) (string, error) {
	parent, err := canonicalDirectory(privateParent)
	if err != nil {
		return "", fmt.Errorf("open private parent: %w", err)
	}
	if !validPrivateCopyName(name) {
		return "", errors.New("private copy name must be a safe path component")
	}
	return filepath.Join(parent, name), nil
}

func validPrivateCopyName(name string) bool {
	return name != "" && name != "." && name != ".." && !filepath.IsAbs(name) &&
		filepath.Base(name) == name && !strings.ContainsAny(name, "/\\\x00")
}

// CreatePrivateCopyAt materializes an independent private copy at the exact
// destination selected by the caller. It refuses an existing destination and
// compares the full current source manifest with expected before creating the
// destination root.
func CreatePrivateCopyAt(ctx context.Context, sourceRoot, privateParent, destinationPath string, expected Manifest) (PrivateCopy, error) {
	return createPrivateCopyWithDestination(ctx, sourceRoot, privateParent, destinationPath, &expected, defaultScanLimits, createCopyHooks{})
}

// VerifyPrivateCopyAt confirms an already materialized stable copy without
// changing it. It is used to reconcile a durable preparing receipt after an
// uncertain completion write; it never creates or repairs a destination.
func VerifyPrivateCopyAt(ctx context.Context, sourceRoot, privateParent, destinationPath string, expected Manifest) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	source, err := canonicalDirectory(sourceRoot)
	if err != nil {
		return fmt.Errorf("open source root: %w", err)
	}
	parent, err := canonicalDirectory(privateParent)
	if err != nil {
		return fmt.Errorf("open private parent: %w", err)
	}
	copyName, err := privateCopyNameForDestination(parent, destinationPath)
	if err != nil {
		return err
	}
	if err := requirePrivateCopyPlatformSupport(); err != nil {
		return err
	}
	currentSource, err := PreflightPrivateCopy(ctx, source, parent)
	if err != nil {
		return fmt.Errorf("verify confirmed copy source: %w", err)
	}
	if !equalManifest(currentSource, expected) {
		return fmt.Errorf("%w: source manifest differs from the confirmed snapshot", ErrSourceDrift)
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return fmt.Errorf("open private parent handle: %w", err)
	}
	defer parentRoot.Close()
	parentFSID, err := rootFilesystemIdentity(parentRoot)
	if err != nil {
		return fmt.Errorf("establish private parent filesystem identity: %w", err)
	}
	if err := verifyReadOnlyPrivateParent(source, parentRoot, parentFSID); err != nil {
		return err
	}
	pathInfo, err := parentRoot.Lstat(copyName)
	if err != nil {
		return fmt.Errorf("inspect private copy root: %w", err)
	}
	if !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 || pathInfo.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: private copy root is not an owner-only directory", ErrPrivatePermissionsUnavailable)
	}
	destination, err := parentRoot.OpenRoot(copyName)
	if err != nil {
		return fmt.Errorf("open private copy root: %w", err)
	}
	defer destination.Close()
	destinationDirectory, err := destination.Open(".")
	if err != nil {
		return fmt.Errorf("open private copy directory handle: %w", err)
	}
	destinationInfo, statErr := destinationDirectory.Stat()
	closeErr := destinationDirectory.Close()
	if statErr != nil {
		return fmt.Errorf("inspect private copy directory: %w", statErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close private copy directory handle: %w", closeErr)
	}
	if !os.SameFile(pathInfo, destinationInfo) {
		return fmt.Errorf("%w: private copy path no longer identifies the registered directory", ErrSourceDrift)
	}
	directory, err := destination.Open(".")
	if err != nil {
		return fmt.Errorf("verify private copy permissions: %w", err)
	}
	permissionErr := verifyPrivateParent(directory)
	permissionCloseErr := directory.Close()
	if permissionErr != nil {
		return fmt.Errorf("verify private copy permissions: %w", permissionErr)
	}
	if permissionCloseErr != nil {
		return fmt.Errorf("close private copy permission handle: %w", permissionCloseErr)
	}
	destinationFSID, err := rootFilesystemIdentity(destination)
	if err != nil {
		return fmt.Errorf("establish private copy filesystem identity: %w", err)
	}
	if err := ensureSameFilesystem(parentFSID, destinationFSID, copyName); err != nil {
		return err
	}
	if err := verifyRootPathBinding(destination, destinationFSID); err != nil {
		return err
	}
	privateManifest, err := scanRootWithLimits(ctx, destination, defaultScanLimits, scanHooks{})
	if err != nil {
		return fmt.Errorf("scan existing private copy: %w", err)
	}
	if len(privateManifest.Excluded) != 0 || !equalEntries(expected.Entries, privateManifest.Entries) ||
		privateManifest.FileCount != expected.FileCount || privateManifest.TotalBytes != expected.TotalBytes || manifestHash(expected) != expected.Hash {
		return fmt.Errorf("%w: existing private copy differs from the confirmed snapshot", ErrSourceDrift)
	}
	currentSource, err = PreflightPrivateCopy(ctx, source, parent)
	if err != nil {
		return fmt.Errorf("recheck confirmed copy source: %w", err)
	}
	if !equalManifest(currentSource, expected) {
		return fmt.Errorf("%w: source manifest changed during copy reconciliation", ErrSourceDrift)
	}
	if err := verifyRootPathBinding(destination, destinationFSID); err != nil {
		return err
	}
	return nil
}

func createPrivateCopyWithDestination(ctx context.Context, sourceRoot, privateParent, destinationPath string, expected *Manifest, limits scanLimits, hooks createCopyHooks) (result PrivateCopy, err error) {
	if ctx == nil {
		return result, errors.New("context is required")
	}
	source, err := canonicalDirectory(sourceRoot)
	if err != nil {
		return result, fmt.Errorf("open source root: %w", err)
	}
	parent, err := canonicalDirectory(privateParent)
	if err != nil {
		return result, fmt.Errorf("open private parent: %w", err)
	}
	copyName := ""
	if destinationPath != "" {
		copyName, err = privateCopyNameForDestination(parent, destinationPath)
		if err != nil {
			return result, err
		}
	}
	parentWithinSource, err := isWithinFilesystem(source, parent)
	if err != nil {
		return result, fmt.Errorf("prove private parent ancestry: %w", err)
	}
	if parentWithinSource {
		return result, errors.New("private parent must not be inside the source root")
	}
	if err := requirePrivateCopyPlatformSupport(); err != nil {
		return result, err
	}
	privateParentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return result, fmt.Errorf("open private parent handle: %w", err)
	}
	defer privateParentRoot.Close()
	privateParentFSID, err := rootFilesystemIdentity(privateParentRoot)
	if err != nil {
		return result, fmt.Errorf("establish private parent filesystem identity: %w", err)
	}
	if err := verifyPrivateParentOutsideSource(source, privateParentRoot, privateParentFSID); err != nil {
		return result, err
	}
	if err := verifyPrivateRootAncestors(privateParentRoot); err != nil {
		return result, err
	}
	privateParentDirectory, err := privateParentRoot.Open(".")
	if err != nil {
		return result, fmt.Errorf("open private parent directory handle: %w", err)
	}
	parentPermissionErr := verifyPrivateParent(privateParentDirectory)
	parentCloseErr := privateParentDirectory.Close()
	if parentPermissionErr != nil {
		return result, fmt.Errorf("verify private parent permissions: %w", parentPermissionErr)
	}
	if parentCloseErr != nil {
		return result, fmt.Errorf("close private parent directory handle: %w", parentCloseErr)
	}
	sourceHandle, err := os.OpenRoot(source)
	if err != nil {
		return result, fmt.Errorf("open source root: %w", err)
	}
	defer sourceHandle.Close()
	snapshot, err := scanRootSnapshot(ctx, sourceHandle, limits, scanHooks{})
	if err != nil {
		return result, fmt.Errorf("scan copy baseline: %w", err)
	}
	baseline := snapshot.manifest
	if expected != nil && !equalManifest(baseline, *expected) {
		return result, fmt.Errorf("%w: source manifest differs from the confirmed snapshot", ErrSourceDrift)
	}
	sourceFSID, err := rootFilesystemIdentity(sourceHandle)
	if err != nil {
		return result, fmt.Errorf("establish source filesystem boundary: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("copy cancelled: %w", err)
	}

	stageName := ""
	staging := ""
	var destination *os.Root
	keep := false
	defer func() {
		if destination != nil {
			_ = destination.Close()
		}
		if !keep {
			err = cleanupPartialPrivateCopy(privateParentRoot, stageName, staging, err)
		}
	}()
	for attempt := 0; attempt < 8; attempt++ {
		candidateName := copyName
		if candidateName == "" {
			candidateName, err = newPrivateStageName()
			if err != nil {
				return result, fmt.Errorf("generate private staging name: %w", err)
			}
		}
		if hooks.beforeStageMkdir != nil {
			if err := hooks.beforeStageMkdir(); err != nil {
				return result, fmt.Errorf("before-stage-mkdir test hook: %w", err)
			}
		}
		if err := verifyPrivateRootAncestors(privateParentRoot); err != nil {
			return result, err
		}
		if err := verifyPrivateParentOutsideSource(source, privateParentRoot, privateParentFSID); err != nil {
			return result, fmt.Errorf("recheck private parent immediately before staging: %w", err)
		}
		err = privateParentRoot.Mkdir(candidateName, 0o700)
		if errors.Is(err, fs.ErrExist) {
			if copyName == "" {
				continue
			}
			return result, fmt.Errorf("private copy destination already exists: %q", filepath.Join(parent, candidateName))
		}
		if err != nil {
			return result, fmt.Errorf("create private staging root: %w", err)
		}
		stageName = candidateName
		staging = filepath.Join(parent, stageName)
		break
	}
	if stageName == "" {
		return result, errors.New("unable to allocate an exclusive private staging root")
	}

	destination, err = privateParentRoot.OpenRoot(stageName)
	if err != nil {
		return result, fmt.Errorf("open private staging root: %w", err)
	}
	stagingDirectory, err := destination.Open(".")
	if err != nil {
		return result, fmt.Errorf("open private staging directory handle: %w", err)
	}
	stageInfo, stageStatErr := stagingDirectory.Stat()
	parentStageInfo, parentStatErr := privateParentRoot.Lstat(stageName)
	if stageStatErr != nil || parentStatErr != nil || !parentStageInfo.IsDir() || !os.SameFile(stageInfo, parentStageInfo) {
		stagingDirectory.Close()
		if stageStatErr != nil {
			return result, fmt.Errorf("inspect private staging root: %w", stageStatErr)
		}
		if parentStatErr != nil {
			return result, fmt.Errorf("verify private staging name: %w", parentStatErr)
		}
		return result, fmt.Errorf("%w: private staging name no longer identifies its created directory", ErrPrivatePermissionsUnavailable)
	}
	if err := securePrivateRoot(stagingDirectory); err != nil {
		stagingDirectory.Close()
		return result, fmt.Errorf("secure private staging root: %w", err)
	}
	stageCloseErr := stagingDirectory.Close()
	if stageCloseErr != nil {
		return result, fmt.Errorf("close private staging directory handle: %w", stageCloseErr)
	}
	destinationFSID, err := rootFilesystemIdentity(destination)
	if err != nil {
		return result, fmt.Errorf("establish private staging filesystem identity: %w", err)
	}
	if err := ensureSameFilesystem(privateParentFSID, destinationFSID, stageName); err != nil {
		return result, fmt.Errorf("verify private staging filesystem: %w", err)
	}
	if err := verifyRootPathBinding(destination, destinationFSID); err != nil {
		return result, fmt.Errorf("verify private staging root binding: %w", err)
	}
	if err := materializeSnapshot(ctx, sourceHandle, destination, staging, baseline, snapshot.infos, limits, sourceFSID, copyHooks{
		afterFile: hooks.afterFile, beforeLeafOpen: hooks.beforeLeafOpen,
	}); err != nil {
		return result, err
	}
	if err := destination.Close(); err != nil {
		destination = nil
		return result, fmt.Errorf("close private staging root: %w", err)
	}
	destination = nil

	keep = true
	return PrivateCopy{Root: staging, Baseline: baseline}, nil
}

func privateCopyNameForDestination(canonicalParent, destinationPath string) (string, error) {
	if destinationPath == "" {
		return "", errors.New("private copy destination is required")
	}
	destination, err := filepath.Abs(destinationPath)
	if err != nil {
		return "", fmt.Errorf("resolve private copy destination: %w", err)
	}
	destination = filepath.Clean(destination)
	destinationParent, err := canonicalDirectory(filepath.Dir(destination))
	if err != nil {
		return "", fmt.Errorf("resolve private copy destination parent: %w", err)
	}
	if destinationParent != canonicalParent {
		return "", errors.New("private copy destination must be a direct child of private parent")
	}
	name := filepath.Base(destination)
	if !validPrivateCopyName(name) {
		return "", errors.New("private copy destination name must be a safe path component")
	}
	return name, nil
}

func equalManifest(left, right Manifest) bool {
	return left.Version == right.Version && left.FileCount == right.FileCount && left.TotalBytes == right.TotalBytes &&
		left.Hash == right.Hash && slices.Equal(left.Entries, right.Entries) && slices.Equal(left.Excluded, right.Excluded)
}

func materializeSnapshot(ctx context.Context, source, destination *os.Root, destinationPath string, baseline Manifest, baselineInfos map[string]os.FileInfo, limits scanLimits, sourceFSID string, hooks copyHooks) error {
	destinationFSID, err := rootFilesystemIdentity(destination)
	if err != nil {
		return fmt.Errorf("establish private copy filesystem boundary: %w", err)
	}
	if err := verifyRootPathBinding(destination, destinationFSID); err != nil {
		return err
	}
	entriesByPath := make(map[string]Entry, len(baseline.Entries))
	for _, entry := range baseline.Entries {
		entriesByPath[entry.Path] = entry
	}
	for _, entry := range baseline.Entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("copy cancelled: %w", err)
		}
		if entry.Type != EntryDirectory {
			continue
		}
		if err := destination.MkdirAll(filepath.FromSlash(entry.Path), 0o700); err != nil {
			return fmt.Errorf("create destination directory %q: %w", entry.Path, err)
		}
	}
	for _, entry := range baseline.Entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("copy cancelled: %w", err)
		}
		switch entry.Type {
		case EntryDirectory:
			continue
		case EntryFile:
			parent, closeParent, err := openSourceParent(source, entry.Path, entriesByPath, baselineInfos, sourceFSID)
			if err != nil {
				return err
			}
			copyErr := copyRegularFile(ctx, parent, destination, entry, baselineInfos[entry.Path], sourceFSID, hooks.beforeLeafOpen)
			closeParent()
			if copyErr != nil {
				return copyErr
			}
			if hooks.afterFile != nil {
				hooks.afterFile(entry.Path)
			}
		case EntrySymlink:
			parent, closeParent, err := openSourceParent(source, entry.Path, entriesByPath, baselineInfos, sourceFSID)
			if err != nil {
				return err
			}
			copyErr := copySymlink(parent, destination, entry, baselineInfos[entry.Path])
			closeParent()
			if copyErr != nil {
				return copyErr
			}
		default:
			return fmt.Errorf("unsupported manifest entry type %q at %q", entry.Type, entry.Path)
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("copy cancelled: %w", err)
	}
	currentSource, err := scanRootWithLimits(ctx, source, limits, scanHooks{})
	if err != nil {
		return fmt.Errorf("verify source after copy: %w", err)
	}
	if currentSource.Hash != baseline.Hash {
		return fmt.Errorf("%w: source manifest changed", ErrSourceDrift)
	}
	privateManifest, err := scanRootWithLimits(ctx, destination, limits, scanHooks{})
	if err != nil {
		return fmt.Errorf("verify private copy: %w", err)
	}
	if !equalEntries(baseline.Entries, privateManifest.Entries) || baseline.FileCount != privateManifest.FileCount || baseline.TotalBytes != privateManifest.TotalBytes {
		return fmt.Errorf("%w: private copy differs from the captured baseline", ErrSourceDrift)
	}
	if err := verifyRootPathBinding(destination, destinationFSID); err != nil {
		return err
	}
	if err := verifyRootPathBinding(source, sourceFSID); err != nil {
		return err
	}
	if destination.Name() != destinationPath {
		return fmt.Errorf("%w: private copy root path changed", ErrSourceDrift)
	}
	return nil
}

func openSourceParent(source *os.Root, entryPath string, entries map[string]Entry, baselineInfos map[string]os.FileInfo, rootFSID string) (*os.Root, func(), error) {
	parentPath := path.Dir(entryPath)
	if parentPath == "." {
		return source, func() {}, nil
	}
	current := source
	owned := false
	closeCurrent := func() {
		if owned {
			_ = current.Close()
		}
	}
	currentPath := ""
	for _, component := range strings.Split(parentPath, "/") {
		if currentPath == "" {
			currentPath = component
		} else {
			currentPath = path.Join(currentPath, component)
		}
		expected, ok := entries[currentPath]
		if !ok || expected.Type != EntryDirectory {
			closeCurrent()
			return nil, func() {}, fmt.Errorf("%w: parent directory %q is not in the captured source", ErrSourceDrift, currentPath)
		}
		baselineInfo, ok := baselineInfos[currentPath]
		if !ok || !baselineInfo.IsDir() {
			closeCurrent()
			return nil, func() {}, fmt.Errorf("%w: missing captured identity for source parent %q", ErrSourceDrift, currentPath)
		}
		before, err := current.Lstat(component)
		if err != nil {
			closeCurrent()
			return nil, func() {}, fmt.Errorf("inspect source parent %q: %w", currentPath, err)
		}
		if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || !os.SameFile(baselineInfo, before) {
			closeCurrent()
			return nil, func() {}, fmt.Errorf("%w: source parent %q changed type", ErrSourceDrift, currentPath)
		}
		child, err := current.OpenRoot(component)
		if err != nil {
			closeCurrent()
			return nil, func() {}, fmt.Errorf("open source parent %q: %w", currentPath, err)
		}
		opened, openErr := child.Open(".")
		if openErr != nil {
			child.Close()
			closeCurrent()
			return nil, func() {}, fmt.Errorf("verify source parent %q: %w", currentPath, openErr)
		}
		openedInfo, statErr := opened.Stat()
		opened.Close()
		if statErr != nil || !os.SameFile(before, openedInfo) || !os.SameFile(baselineInfo, openedInfo) {
			child.Close()
			closeCurrent()
			if statErr != nil {
				return nil, func() {}, fmt.Errorf("verify source parent %q: %w", currentPath, statErr)
			}
			return nil, func() {}, fmt.Errorf("%w: source parent %q was replaced", ErrSourceDrift, currentPath)
		}
		filesystemID, idErr := rootFilesystemIdentity(child)
		if idErr != nil {
			child.Close()
			closeCurrent()
			return nil, func() {}, fmt.Errorf("verify source parent boundary %q: %w", currentPath, idErr)
		}
		if err := ensureSameFilesystem(rootFSID, filesystemID, currentPath); err != nil {
			child.Close()
			closeCurrent()
			return nil, func() {}, err
		}
		closeCurrent()
		current = child
		owned = true
	}
	return current, closeCurrent, nil
}

func openSourceDirectory(source *os.Root, directoryPath string, entries map[string]Entry, baselineInfos map[string]os.FileInfo, rootFSID string) (*os.Root, func(), error) {
	if directoryPath == "" {
		return source, func() {}, nil
	}
	parent, closeParent, err := openSourceParent(source, directoryPath, entries, baselineInfos, rootFSID)
	if err != nil {
		return nil, func() {}, err
	}
	name := path.Base(directoryPath)
	baselineInfo, ok := baselineInfos[directoryPath]
	if !ok || !baselineInfo.IsDir() {
		closeParent()
		return nil, func() {}, fmt.Errorf("%w: missing captured identity for directory %q", ErrSourceDrift, directoryPath)
	}
	before, err := parent.Lstat(name)
	if err != nil {
		closeParent()
		return nil, func() {}, fmt.Errorf("inspect source directory %q: %w", directoryPath, err)
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || !os.SameFile(baselineInfo, before) {
		closeParent()
		return nil, func() {}, fmt.Errorf("%w: source directory %q changed before opening", ErrSourceDrift, directoryPath)
	}
	directory, err := parent.OpenRoot(name)
	if err != nil {
		closeParent()
		return nil, func() {}, fmt.Errorf("open source directory %q: %w", directoryPath, err)
	}
	opened, err := directory.Open(".")
	if err != nil {
		directory.Close()
		closeParent()
		return nil, func() {}, fmt.Errorf("verify source directory %q: %w", directoryPath, err)
	}
	openedInfo, statErr := opened.Stat()
	opened.Close()
	if statErr != nil || !os.SameFile(baselineInfo, openedInfo) {
		directory.Close()
		closeParent()
		if statErr != nil {
			return nil, func() {}, fmt.Errorf("verify source directory %q: %w", directoryPath, statErr)
		}
		return nil, func() {}, fmt.Errorf("%w: source directory %q was replaced", ErrSourceDrift, directoryPath)
	}
	filesystemID, err := rootFilesystemIdentity(directory)
	if err != nil {
		directory.Close()
		closeParent()
		return nil, func() {}, fmt.Errorf("verify source directory boundary %q: %w", directoryPath, err)
	}
	if err := ensureSameFilesystem(rootFSID, filesystemID, directoryPath); err != nil {
		directory.Close()
		closeParent()
		return nil, func() {}, err
	}
	closeParent()
	return directory, func() { _ = directory.Close() }, nil
}

func copyRegularFile(ctx context.Context, source, destination *os.Root, entry Entry, baselineInfo os.FileInfo, rootFSID string, beforeOpen func(Entry, *os.Root) error) error {
	name := filepath.Base(filepath.FromSlash(entry.Path))
	destinationName := filepath.FromSlash(entry.Path)
	before, err := source.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect source file %q: %w", entry.Path, err)
	}
	if !baselineInfo.Mode().IsRegular() || !before.Mode().IsRegular() || !os.SameFile(baselineInfo, before) || before.Size() != entry.Size || fileGitMode(before) != entry.Mode {
		return fmt.Errorf("%w: file %q changed before copy", ErrSourceDrift, entry.Path)
	}
	if beforeOpen != nil {
		if err := beforeOpen(entry, source); err != nil {
			return err
		}
	}
	input, err := openRegularFile(source, name)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", entry.Path, err)
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil {
		return fmt.Errorf("stat source file %q: %w", entry.Path, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(baselineInfo, opened) {
		return fmt.Errorf("%w: file %q was replaced before copy", ErrSourceDrift, entry.Path)
	}
	fileFSID, err := fileFilesystemIdentity(input)
	if err != nil {
		return fmt.Errorf("establish source file filesystem boundary at %q: %w", entry.Path, err)
	}
	if err := ensureSameFilesystem(rootFSID, fileFSID, entry.Path); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if entry.Mode&0o111 != 0 {
		mode = 0o700
	}
	output, err := destination.OpenFile(destinationName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create private file %q: %w", entry.Path, err)
	}
	digest := sha256.New()
	written, copyErr := copyContext(ctx, io.MultiWriter(output, digest), input, entry.Size)
	closeErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("copy %q: %w", entry.Path, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close private file %q: %w", entry.Path, closeErr)
	}
	if written != entry.Size || hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
		return fmt.Errorf("%w: copied bytes for %q differ from baseline", ErrSourceDrift, entry.Path)
	}
	afterFD, err := input.Stat()
	if err != nil {
		return fmt.Errorf("verify source file %q: %w", entry.Path, err)
	}
	afterPath, err := source.Lstat(name)
	if err != nil || !os.SameFile(before, afterFD) || !os.SameFile(before, afterPath) || afterPath.Size() != entry.Size || fileGitMode(afterPath) != entry.Mode {
		if err != nil {
			return fmt.Errorf("verify source file %q: %w", entry.Path, err)
		}
		return fmt.Errorf("%w: file %q changed during copy", ErrSourceDrift, entry.Path)
	}
	current, err := openRegularFile(source, name)
	if err != nil {
		return fmt.Errorf("verify source file %q after copy: %w", entry.Path, err)
	}
	currentInfo, statErr := current.Stat()
	currentFSID, identityErr := fileFilesystemIdentity(current)
	current.Close()
	if statErr != nil {
		return fmt.Errorf("verify source file %q after copy: %w", entry.Path, statErr)
	}
	if identityErr != nil {
		return fmt.Errorf("verify source file filesystem boundary at %q: %w", entry.Path, identityErr)
	}
	if !os.SameFile(before, currentInfo) {
		return fmt.Errorf("%w: file %q changed after copy", ErrSourceDrift, entry.Path)
	}
	if err := ensureSameFilesystem(rootFSID, currentFSID, entry.Path); err != nil {
		return err
	}
	return nil
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader, expectedSize int64) (int64, error) {
	buffer := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written += int64(read)
			if written > expectedSize {
				return written, fmt.Errorf("%w: file grew while copying", ErrSourceDrift)
			}
			count, writeErr := destination.Write(buffer[:read])
			if writeErr != nil {
				return written, writeErr
			}
			if count != read {
				return written, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func copySymlink(source, destination *os.Root, entry Entry, baselineInfo os.FileInfo) error {
	name := filepath.Base(filepath.FromSlash(entry.Path))
	destinationName := filepath.FromSlash(entry.Path)
	before, err := source.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect source symlink %q: %w", entry.Path, err)
	}
	if before.Mode()&os.ModeSymlink == 0 || baselineInfo.Mode()&os.ModeSymlink == 0 || !os.SameFile(before, baselineInfo) {
		return fmt.Errorf("%w: symlink %q changed type before copy", ErrSourceDrift, entry.Path)
	}
	target, err := source.Readlink(name)
	if err != nil {
		return fmt.Errorf("read source symlink %q: %w", entry.Path, err)
	}
	after, err := source.Lstat(name)
	if err != nil || !os.SameFile(before, after) || !os.SameFile(baselineInfo, after) || after.Mode()&os.ModeSymlink == 0 || target != entry.Target {
		if err != nil {
			return fmt.Errorf("verify source symlink %q: %w", entry.Path, err)
		}
		return fmt.Errorf("%w: symlink %q changed before copy", ErrSourceDrift, entry.Path)
	}
	if err := validateSymlinkTargetText(entry.Path, target); err != nil {
		return err
	}
	if err := destination.Symlink(target, destinationName); err != nil {
		return fmt.Errorf("create private symlink %q: %w", entry.Path, err)
	}
	return nil
}

func equalEntries(left, right []Entry) bool {
	return slices.Equal(left, right)
}
