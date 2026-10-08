package sddworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const manifestVersion = 1

const (
	maxPathBytes = 4096
	maxPathDepth = 128
	maxNameBytes = 255
	dirPageSize  = 128
)

var (
	// ErrUnsafePath marks a path that is not safely portable below the root.
	ErrUnsafePath = errors.New("unsafe or non-portable path")
	// ErrSpecialFile marks an unsupported non-regular filesystem node.
	ErrSpecialFile = errors.New("special files are not supported")
	// ErrSymlinkEscape marks an absolute, escaping, or unresolved link target.
	ErrSymlinkEscape = errors.New("symlink may escape the private copy")
	// ErrSymlinkExcludedTarget marks a link that targets a path omitted from the copy.
	ErrSymlinkExcludedTarget = errors.New("symlink target is excluded from the private copy")
	// ErrManifestLimit marks a configured file, byte, or entry limit violation.
	ErrManifestLimit = errors.New("manifest limit exceeded")
	// ErrPatchTooLarge marks an aggregate Code patch that exceeds its persisted size limit.
	ErrPatchTooLarge = errors.New("authoring Code patch exceeds its size limit")
	// ErrPatchSourceDrift marks source changes found while constructing patch evidence.
	ErrPatchSourceDrift = errors.New("authoring Code source changed while constructing patch evidence")
	// ErrPatchResultDrift marks private-root changes found while constructing patch evidence.
	ErrPatchResultDrift = errors.New("authoring Code result changed while constructing patch evidence")
	// ErrSourceDrift marks a source tree that changed during a scan or copy.
	ErrSourceDrift = errors.New("source changed while it was being inspected or copied")
	// ErrPrivatePermissionsUnavailable marks a platform where privacy cannot be proven.
	ErrPrivatePermissionsUnavailable = errors.New("private copy permissions cannot be proven on this platform")
	// ErrPrivateCopyCleanup marks a partial private root that could not be removed.
	ErrPrivateCopyCleanup = errors.New("partial private copy cleanup failed")
	// ErrFilesystemBoundary marks a nested mount or filesystem boundary.
	ErrFilesystemBoundary = errors.New("filesystem boundary inside source root is not supported")
	// ErrFilesystemBoundaryUnprovable marks a platform or kernel that cannot expose a boundary identity.
	ErrFilesystemBoundaryUnprovable = errors.New("filesystem boundary cannot be proven on this platform")
	errSymlinkTargetExcluded        = errors.New("symlink target enters an excluded path")
	emptyContentSHA256              = sha256Hex(nil)
	manifestDomainBytes             = []byte("harflex-sdd-manifest-v1\x00")
)

// EntryType describes the kind of filesystem node represented in a manifest.
type EntryType string

const (
	EntryFile      EntryType = "file"
	EntryDirectory EntryType = "directory"
	EntrySymlink   EntryType = "symlink"
)

// Entry records only Git-relevant mode information: files normalize to
// 100644/100755, directories to 040000, and symlinks to 120000. Private-copy
// permissions therefore do not hide executable-bit changes.
type Entry struct {
	Path   string    `json:"path"`
	Type   EntryType `json:"type"`
	Mode   uint32    `json:"mode"`
	Size   int64     `json:"size"`
	SHA256 string    `json:"sha256"`
	Target string    `json:"target,omitempty"`
}

// Exclusion records a path deliberately omitted from the snapshot. Reasons are
// intentionally narrow and do not claim to identify every secret in a project.
type Exclusion struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Manifest is a deterministic, bounded description of one project tree.
type Manifest struct {
	Version  int         `json:"version"`
	Entries  []Entry     `json:"entries"`
	Excluded []Exclusion `json:"excluded"`
	// FileCount counts included regular files and symlinks, not directories.
	FileCount int `json:"fileCount"`
	// TotalBytes includes file contents and textual symlink targets.
	TotalBytes int64  `json:"totalBytes"`
	Hash       string `json:"hash"`
}

type scanLimits struct {
	maxFiles   int
	maxBytes   int64
	maxEntries int
}

var defaultScanLimits = scanLimits{
	maxFiles:   20_000,
	maxBytes:   2 << 30,
	maxEntries: 40_000,
}

// Scan describes a local project tree without hashing through symlinks and
// applies the product limits of 20,000 files and 2 GiB. It rejects nested
// filesystem boundaries and platforms where that boundary cannot be proven.
func Scan(ctx context.Context, rootPath string) (Manifest, error) {
	return scanWithLimits(ctx, rootPath, defaultScanLimits)
}

// ScanNoFollow scans an already-canonical source root and rejects a root path
// that is itself a symlink or traverses a symlinked parent. Entries below the
// root are still read through os.Root and recorded with Lstat semantics.
func ScanNoFollow(ctx context.Context, rootPath string) (Manifest, error) {
	var empty Manifest
	if ctx == nil {
		return empty, errors.New("context is required")
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return empty, fmt.Errorf("resolve source root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if err := verifyNoFollowRootPath(absolute, nil); err != nil {
		return empty, err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return empty, fmt.Errorf("open source root: %w", err)
	}
	defer root.Close()
	opened, err := root.Open(".")
	if err != nil {
		return empty, fmt.Errorf("open source root identity: %w", err)
	}
	openedInfo, statErr := opened.Stat()
	closeErr := opened.Close()
	if statErr != nil {
		return empty, fmt.Errorf("inspect source root identity: %w", statErr)
	}
	if closeErr != nil {
		return empty, fmt.Errorf("close source root identity: %w", closeErr)
	}
	if err := verifyNoFollowRootPath(absolute, openedInfo); err != nil {
		return empty, err
	}
	snapshot, err := scanRootSnapshot(ctx, root, defaultScanLimits, scanHooks{
		verifyRootBinding: func(openedRoot *os.Root) error {
			return verifyNoFollowRootPath(openedRoot.Name(), openedInfo)
		},
	})
	if err != nil {
		return empty, err
	}
	return snapshot.manifest, nil
}

func verifyNoFollowRootPath(rootPath string, opened os.FileInfo) error {
	info, err := os.Lstat(rootPath)
	if err != nil {
		return fmt.Errorf("inspect source root without following links: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: source root is not a real directory", ErrUnsafePath)
	}
	resolved, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return fmt.Errorf("resolve source root identity: %w", err)
	}
	if filepath.Clean(resolved) != filepath.Clean(rootPath) {
		return fmt.Errorf("%w: source root traverses a symlinked path", ErrUnsafePath)
	}
	if opened != nil && !os.SameFile(info, opened) {
		return fmt.Errorf("%w: source root was replaced during readback", ErrSourceDrift)
	}
	return nil
}

func scanWithLimits(ctx context.Context, rootPath string, limits scanLimits) (Manifest, error) {
	return scanWithLimitsAndHooks(ctx, rootPath, limits, scanHooks{})
}

type scanHooks struct {
	hashFile          func(context.Context, *os.Root, string, string, os.FileInfo, string) (Entry, error)
	afterWalk         func(*os.Root) error
	verifyRootBinding func(*os.Root) error
}

type scanSnapshot struct {
	manifest Manifest
	infos    map[string]os.FileInfo
}

func scanWithLimitsAndHooks(ctx context.Context, rootPath string, limits scanLimits, hooks scanHooks) (Manifest, error) {
	var empty Manifest
	if ctx == nil {
		return empty, errors.New("context is required")
	}
	if limits.maxFiles <= 0 || limits.maxBytes < 0 || limits.maxEntries <= 0 {
		return empty, errors.New("scan limits must have positive counts and non-negative bytes")
	}
	rootPath, err := canonicalDirectory(rootPath)
	if err != nil {
		return empty, fmt.Errorf("open source root: %w", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return empty, fmt.Errorf("open source root: %w", err)
	}
	defer root.Close()
	return scanRootWithLimits(ctx, root, limits, hooks)
}

func scanRootWithLimits(ctx context.Context, root *os.Root, limits scanLimits, hooks scanHooks) (Manifest, error) {
	snapshot, err := scanRootSnapshot(ctx, root, limits, hooks)
	if err != nil {
		return Manifest{}, err
	}
	return snapshot.manifest, nil
}

func scanRootSnapshot(ctx context.Context, root *os.Root, limits scanLimits, hooks scanHooks) (scanSnapshot, error) {
	var empty scanSnapshot
	if ctx == nil {
		return empty, errors.New("context is required")
	}
	if limits.maxFiles <= 0 || limits.maxBytes < 0 || limits.maxEntries <= 0 {
		return empty, errors.New("scan limits must have positive counts and non-negative bytes")
	}
	rootFSID, err := rootFilesystemIdentity(root)
	if err != nil {
		return empty, fmt.Errorf("establish source filesystem boundary: %w", err)
	}
	if err := verifyScanRootBinding(root, rootFSID, hooks); err != nil {
		return empty, err
	}

	state := scanState{ctx: ctx, limits: limits, hooks: hooks, rootFSID: rootFSID, infos: make(map[string]os.FileInfo), members: make(map[string][]string)}
	if err := state.walk(root, ""); err != nil {
		return empty, err
	}
	if hooks.afterWalk != nil {
		if err := hooks.afterWalk(root); err != nil {
			return empty, fmt.Errorf("after-walk test hook: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, fmt.Errorf("scan cancelled: %w", err)
	}
	if err := validateSymlinkTargets(ctx, state.manifest.Entries, state.manifest.Excluded); err != nil {
		return empty, err
	}
	if err := state.verifySnapshot(root); err != nil {
		return empty, err
	}
	sort.Slice(state.manifest.Entries, func(i, j int) bool {
		return state.manifest.Entries[i].Path < state.manifest.Entries[j].Path
	})
	sort.Slice(state.manifest.Excluded, func(i, j int) bool {
		if state.manifest.Excluded[i].Path != state.manifest.Excluded[j].Path {
			return state.manifest.Excluded[i].Path < state.manifest.Excluded[j].Path
		}
		return state.manifest.Excluded[i].Reason < state.manifest.Excluded[j].Reason
	})
	state.manifest.Version = manifestVersion
	state.manifest.Hash = manifestHash(state.manifest)
	if err := ctx.Err(); err != nil {
		return empty, fmt.Errorf("scan cancelled: %w", err)
	}
	if err := verifyScanRootBinding(root, rootFSID, hooks); err != nil {
		return empty, err
	}
	return scanSnapshot{manifest: state.manifest, infos: state.infos}, nil
}

func verifyScanRootBinding(root *os.Root, rootFSID string, hooks scanHooks) error {
	if hooks.verifyRootBinding != nil {
		return hooks.verifyRootBinding(root)
	}
	return verifyRootPathBinding(root, rootFSID)
}

type scanState struct {
	ctx      context.Context
	limits   scanLimits
	hooks    scanHooks
	rootFSID string
	visited  int
	manifest Manifest
	infos    map[string]os.FileInfo
	members  map[string][]string
}

func (state *scanState) walk(dir *os.Root, relativeDir string) error {
	if err := state.ctx.Err(); err != nil {
		return fmt.Errorf("scan cancelled: %w", err)
	}
	listing, err := dir.Open(".")
	if err != nil {
		return fmt.Errorf("read directory %q: %w", relativeDir, err)
	}
	defer listing.Close()
	state.members[relativeDir] = nil
	for {
		entries, readErr := listing.ReadDir(dirPageSize)
		for _, item := range entries {
			if err := state.ctx.Err(); err != nil {
				return fmt.Errorf("scan cancelled: %w", err)
			}
			name := item.Name()
			state.members[relativeDir] = append(state.members[relativeDir], name)
			if err := validatePathComponent(name); err != nil {
				return fmt.Errorf("%w: %q", err, name)
			}
			rel := name
			if relativeDir != "" {
				rel = path.Join(relativeDir, name)
			}
			if err := validateRelativePath(rel); err != nil {
				return fmt.Errorf("%w: %q", err, rel)
			}
			if state.visited >= state.limits.maxEntries {
				return fmt.Errorf("%w: visited entries exceed %d", ErrManifestLimit, state.limits.maxEntries)
			}
			state.visited++
			info, err := dir.Lstat(name)
			if err != nil {
				return fmt.Errorf("inspect %q: %w", rel, err)
			}
			if reason, excluded := exclusionReason(name); excluded {
				state.manifest.Excluded = append(state.manifest.Excluded, Exclusion{Path: rel, Reason: reason})
				continue
			}

			switch mode := info.Mode(); {
			case mode.IsDir():
				state.infos[rel] = info
				if err := state.addEntry(Entry{Path: rel, Type: EntryDirectory, Mode: 0o040000, Size: 0, SHA256: emptyContentSHA256}, false); err != nil {
					return err
				}
				child, err := dir.OpenRoot(name)
				if err != nil {
					return fmt.Errorf("open directory %q: %w", rel, err)
				}
				opened, openErr := child.Open(".")
				if openErr != nil {
					child.Close()
					return fmt.Errorf("verify directory %q: %w", rel, openErr)
				}
				actual, statErr := opened.Stat()
				opened.Close()
				if statErr != nil || !os.SameFile(info, actual) {
					child.Close()
					if statErr != nil {
						return fmt.Errorf("verify directory %q: %w", rel, statErr)
					}
					return fmt.Errorf("%w: directory %q was replaced", ErrSourceDrift, rel)
				}
				childFSID, err := rootFilesystemIdentity(child)
				if err != nil {
					child.Close()
					return fmt.Errorf("establish filesystem boundary at %q: %w", rel, err)
				}
				if err := ensureSameFilesystem(state.rootFSID, childFSID, rel); err != nil {
					child.Close()
					return err
				}
				walkErr := state.walk(child, rel)
				closeErr := child.Close()
				if walkErr != nil {
					return walkErr
				}
				if closeErr != nil {
					return fmt.Errorf("close directory %q: %w", rel, closeErr)
				}
				currentInfo, err := dir.Lstat(name)
				if err != nil || !currentInfo.IsDir() || !os.SameFile(info, currentInfo) {
					if err != nil {
						return fmt.Errorf("verify directory %q after traversal: %w", rel, err)
					}
					return fmt.Errorf("%w: directory %q changed after traversal", ErrSourceDrift, rel)
				}
				currentRoot, err := dir.OpenRoot(name)
				if err != nil {
					return fmt.Errorf("verify directory %q after traversal: %w", rel, err)
				}
				openedCurrent, openErr := currentRoot.Open(".")
				if openErr != nil {
					currentRoot.Close()
					return fmt.Errorf("verify directory %q after traversal: %w", rel, openErr)
				}
				openedInfo, statErr := openedCurrent.Stat()
				openedCurrent.Close()
				if statErr != nil || !os.SameFile(currentInfo, openedInfo) {
					currentRoot.Close()
					if statErr != nil {
						return fmt.Errorf("verify directory %q after traversal: %w", rel, statErr)
					}
					return fmt.Errorf("%w: directory %q was replaced after traversal", ErrSourceDrift, rel)
				}
				currentFSID, idErr := rootFilesystemIdentity(currentRoot)
				currentCloseErr := currentRoot.Close()
				if idErr != nil {
					return fmt.Errorf("verify filesystem boundary at %q after traversal: %w", rel, idErr)
				}
				if currentCloseErr != nil {
					return fmt.Errorf("close verified directory %q: %w", rel, currentCloseErr)
				}
				if err := ensureSameFilesystem(state.rootFSID, currentFSID, rel); err != nil {
					return fmt.Errorf("directory changed filesystem after traversal: %w", err)
				}
			case mode.IsRegular():
				if err := state.checkFileBudget(info.Size()); err != nil {
					return fmt.Errorf("%w at %q", err, rel)
				}
				hashFile := state.hooks.hashFile
				if hashFile == nil {
					hashFile = func(ctx context.Context, parent *os.Root, name, relativePath string, before os.FileInfo, rootFSID string) (Entry, error) {
						return hashRegularFile(ctx, parent, name, relativePath, before, rootFSID)
					}
				}
				entry, err := hashFile(state.ctx, dir, name, rel, info, state.rootFSID)
				if err != nil {
					return err
				}
				state.infos[rel] = info
				if err := state.addEntry(entry, true); err != nil {
					return err
				}
			case mode&os.ModeSymlink != 0:
				target, err := dir.Readlink(name)
				if err != nil {
					return fmt.Errorf("read symlink %q: %w", rel, err)
				}
				after, err := dir.Lstat(name)
				if err != nil || !os.SameFile(info, after) || after.Mode()&os.ModeSymlink == 0 {
					if err != nil {
						return fmt.Errorf("verify symlink %q: %w", rel, err)
					}
					return fmt.Errorf("%w: symlink %q was replaced", ErrSourceDrift, rel)
				}
				state.infos[rel] = info
				entry := Entry{Path: rel, Type: EntrySymlink, Mode: 0o120000, Size: int64(len(target)), SHA256: sha256Hex([]byte(target)), Target: target}
				if err := state.addEntry(entry, true); err != nil {
					return err
				}
			default:
				return fmt.Errorf("%w: %q has mode %s", ErrSpecialFile, rel, mode.Type())
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read directory %q: %w", relativeDir, readErr)
		}
	}
	sort.Strings(state.members[relativeDir])
	return nil
}

func (state *scanState) addEntry(entry Entry, countAsFile bool) error {
	if countAsFile {
		if err := state.checkFileBudget(entry.Size); err != nil {
			return err
		}
		state.manifest.FileCount++
		state.manifest.TotalBytes += entry.Size
	}
	state.manifest.Entries = append(state.manifest.Entries, entry)
	return nil
}

func (state *scanState) checkFileBudget(size int64) error {
	if state.manifest.FileCount >= state.limits.maxFiles {
		return fmt.Errorf("%w: files exceed %d", ErrManifestLimit, state.limits.maxFiles)
	}
	if size < 0 || size > state.limits.maxBytes-state.manifest.TotalBytes {
		return fmt.Errorf("%w: bytes exceed %d", ErrManifestLimit, state.limits.maxBytes)
	}
	return nil
}

func (state *scanState) verifySnapshot(root *os.Root) error {
	entriesByPath := make(map[string]Entry, len(state.manifest.Entries))
	for _, entry := range state.manifest.Entries {
		entriesByPath[entry.Path] = entry
	}
	if err := state.verifyDirectoryMembership(root, entriesByPath); err != nil {
		return err
	}
	for _, entry := range state.manifest.Entries {
		if err := state.ctx.Err(); err != nil {
			return fmt.Errorf("snapshot verification cancelled: %w", err)
		}
		before, ok := state.infos[entry.Path]
		if !ok {
			return fmt.Errorf("%w: missing captured identity for %q", ErrSourceDrift, entry.Path)
		}
		name := filepath.FromSlash(entry.Path)
		current, err := root.Lstat(name)
		if err != nil {
			return fmt.Errorf("verify snapshot path %q: %w", entry.Path, err)
		}
		if !os.SameFile(before, current) {
			return fmt.Errorf("%w: path %q was replaced after it was read", ErrSourceDrift, entry.Path)
		}
		switch entry.Type {
		case EntryDirectory:
			if !current.IsDir() {
				return fmt.Errorf("%w: directory %q changed type", ErrSourceDrift, entry.Path)
			}
			directory, err := root.OpenRoot(name)
			if err != nil {
				return fmt.Errorf("verify directory %q: %w", entry.Path, err)
			}
			opened, openErr := directory.Open(".")
			if openErr != nil {
				directory.Close()
				return fmt.Errorf("verify directory %q: %w", entry.Path, openErr)
			}
			openedInfo, statErr := opened.Stat()
			opened.Close()
			if statErr != nil || !os.SameFile(current, openedInfo) {
				directory.Close()
				if statErr != nil {
					return fmt.Errorf("verify directory %q: %w", entry.Path, statErr)
				}
				return fmt.Errorf("%w: directory %q changed during verification", ErrSourceDrift, entry.Path)
			}
			filesystemID, idErr := rootFilesystemIdentity(directory)
			closeErr := directory.Close()
			if idErr != nil {
				return fmt.Errorf("verify filesystem boundary at %q: %w", entry.Path, idErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close verified directory %q: %w", entry.Path, closeErr)
			}
			if err := ensureSameFilesystem(state.rootFSID, filesystemID, entry.Path); err != nil {
				return err
			}
		case EntryFile:
			if !current.Mode().IsRegular() || current.Size() != entry.Size || fileGitMode(current) != entry.Mode {
				return fmt.Errorf("%w: file %q changed metadata after it was read", ErrSourceDrift, entry.Path)
			}
			parent, closeParent, err := openSourceParent(root, entry.Path, entriesByPath, state.infos, state.rootFSID)
			if err != nil {
				return err
			}
			leafName := filepath.Base(name)
			parentInfo, err := parent.Lstat(leafName)
			if err != nil {
				closeParent()
				return fmt.Errorf("verify file %q parent handle: %w", entry.Path, err)
			}
			if !os.SameFile(before, parentInfo) {
				closeParent()
				return fmt.Errorf("%w: file %q parent handle changed", ErrSourceDrift, entry.Path)
			}
			actual, hashErr := hashRegularFile(state.ctx, parent, leafName, entry.Path, parentInfo, state.rootFSID)
			closeParent()
			if hashErr != nil {
				return hashErr
			}
			if actual.Size != entry.Size || actual.Mode != entry.Mode || actual.SHA256 != entry.SHA256 {
				return fmt.Errorf("%w: file %q content changed after hashing", ErrSourceDrift, entry.Path)
			}
		case EntrySymlink:
			if current.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("%w: symlink %q changed type", ErrSourceDrift, entry.Path)
			}
			target, err := root.Readlink(name)
			if err != nil {
				return fmt.Errorf("verify symlink %q: %w", entry.Path, err)
			}
			if target != entry.Target {
				return fmt.Errorf("%w: symlink %q target changed", ErrSourceDrift, entry.Path)
			}
		}
	}
	return state.verifyDirectoryMembership(root, entriesByPath)
}

func (state *scanState) verifyDirectoryMembership(root *os.Root, entriesByPath map[string]Entry) error {
	directories := make([]string, 1, len(state.manifest.Entries)+1)
	for _, entry := range state.manifest.Entries {
		if entry.Type == EntryDirectory {
			directories = append(directories, entry.Path)
		}
	}
	for _, directoryPath := range directories {
		if err := state.ctx.Err(); err != nil {
			return fmt.Errorf("directory membership verification cancelled: %w", err)
		}
		expected, ok := state.members[directoryPath]
		if !ok {
			return fmt.Errorf("%w: no captured membership for directory %q", ErrSourceDrift, directoryPath)
		}
		directory, closeDirectory, err := openSourceDirectory(root, directoryPath, entriesByPath, state.infos, state.rootFSID)
		if err != nil {
			return err
		}
		actual, readErr := readDirectoryNames(state.ctx, directory, len(expected))
		closeDirectory()
		if readErr != nil {
			return readErr
		}
		if len(actual) != len(expected) {
			return fmt.Errorf("%w: directory %q membership changed", ErrSourceDrift, directoryPath)
		}
		for index := range expected {
			if actual[index] != expected[index] {
				return fmt.Errorf("%w: directory %q membership changed", ErrSourceDrift, directoryPath)
			}
		}
	}
	return nil
}

func readDirectoryNames(ctx context.Context, directory *os.Root, expectedCount int) ([]string, error) {
	listing, err := directory.Open(".")
	if err != nil {
		return nil, fmt.Errorf("verify directory membership: %w", err)
	}
	defer listing.Close()
	names := make([]string, 0, expectedCount)
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("directory membership verification cancelled: %w", err)
		}
		page, readErr := listing.ReadDir(dirPageSize)
		for _, item := range page {
			names = append(names, item.Name())
			if len(names) > expectedCount {
				return nil, fmt.Errorf("%w: directory gained entries", ErrSourceDrift)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("verify directory membership: %w", readErr)
		}
	}
	sort.Strings(names)
	return names, nil
}

func hashRegularFile(ctx context.Context, parent *os.Root, name, relativePath string, before os.FileInfo, rootFSID string) (Entry, error) {
	if before.Size() < 0 {
		return Entry{}, fmt.Errorf("invalid negative size for %q", relativePath)
	}
	file, err := openRegularFile(parent, name)
	if err != nil {
		return Entry{}, fmt.Errorf("open %q: %w", relativePath, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return Entry{}, fmt.Errorf("stat opened file %q: %w", relativePath, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(before, openedInfo) || openedInfo.Size() != before.Size() || relevantMode(openedInfo) != relevantMode(before) {
		return Entry{}, fmt.Errorf("%w: file %q was replaced before reading", ErrSourceDrift, relativePath)
	}
	fileFSID, err := fileFilesystemIdentity(file)
	if err != nil {
		return Entry{}, fmt.Errorf("establish file filesystem boundary at %q: %w", relativePath, err)
	}
	if err := ensureSameFilesystem(rootFSID, fileFSID, relativePath); err != nil {
		return Entry{}, err
	}

	digest := sha256.New()
	var size int64
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return Entry{}, fmt.Errorf("hash cancelled for %q: %w", relativePath, err)
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			size += int64(read)
			if size > before.Size() {
				return Entry{}, fmt.Errorf("%w: file %q grew while being read", ErrSourceDrift, relativePath)
			}
			_, _ = digest.Write(buffer[:read])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Entry{}, fmt.Errorf("read %q: %w", relativePath, readErr)
		}
	}
	if size != before.Size() {
		return Entry{}, fmt.Errorf("%w: file %q changed size while being read", ErrSourceDrift, relativePath)
	}
	afterFD, err := file.Stat()
	if err != nil {
		return Entry{}, fmt.Errorf("verify opened file %q: %w", relativePath, err)
	}
	afterPath, err := parent.Lstat(name)
	if err != nil || !afterPath.Mode().IsRegular() || !os.SameFile(before, afterFD) || !os.SameFile(before, afterPath) || afterPath.Size() != before.Size() || relevantMode(afterPath) != relevantMode(before) {
		if err != nil {
			return Entry{}, fmt.Errorf("verify %q: %w", relativePath, err)
		}
		return Entry{}, fmt.Errorf("%w: file %q changed while being read", ErrSourceDrift, relativePath)
	}
	currentPath, err := openRegularFile(parent, name)
	if err != nil {
		return Entry{}, fmt.Errorf("verify file %q after reading: %w", relativePath, err)
	}
	currentInfo, statErr := currentPath.Stat()
	currentFSID, identityErr := fileFilesystemIdentity(currentPath)
	currentPath.Close()
	if statErr != nil {
		return Entry{}, fmt.Errorf("verify file %q after reading: %w", relativePath, statErr)
	}
	if identityErr != nil {
		return Entry{}, fmt.Errorf("verify filesystem boundary at %q after reading: %w", relativePath, identityErr)
	}
	if !os.SameFile(before, currentInfo) {
		return Entry{}, fmt.Errorf("%w: file %q changed after reading", ErrSourceDrift, relativePath)
	}
	if err := ensureSameFilesystem(rootFSID, currentFSID, relativePath); err != nil {
		return Entry{}, err
	}

	return Entry{
		Path:   relativePath,
		Type:   EntryFile,
		Mode:   fileGitMode(before),
		Size:   size,
		SHA256: hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func canonicalDirectory(rootPath string) (string, error) {
	if strings.TrimSpace(rootPath) == "" {
		return "", errors.New("source root is required")
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("source root %q is not a directory", rootPath)
	}
	return resolved, nil
}

func exclusionReason(name string) (string, bool) {
	switch {
	case strings.EqualFold(name, ".git"):
		return "git metadata", true
	case strings.EqualFold(name, ".ssh"):
		return "SSH credentials directory", true
	case strings.EqualFold(name, ".env"):
		return "environment credentials file", true
	case strings.EqualFold(name, "credentials.json"):
		return "credentials file", true
	}
	if len(name) >= len(".env.") && strings.EqualFold(name[:len(".env.")], ".env.") && !strings.EqualFold(name, ".env.example") {
		return "environment credentials file", true
	}
	switch {
	case strings.EqualFold(name, "id_rsa"), strings.EqualFold(name, "id_dsa"),
		strings.EqualFold(name, "id_ecdsa"), strings.EqualFold(name, "id_ed25519"),
		strings.EqualFold(name, "id_ecdsa_sk"), strings.EqualFold(name, "id_ed25519_sk"):
		return "common SSH private key filename", true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pem", ".key", ".p12", ".pfx":
		return "private key or certificate file", true
	}
	return "", false
}

func validatePathComponent(component string) error {
	if component == "" || component == "." || component == ".." || !utf8.ValidString(component) {
		return ErrUnsafePath
	}
	if len(component) > maxNameBytes {
		return ErrUnsafePath
	}
	if strings.ContainsAny(component, "/\\:\x00") || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
		return ErrUnsafePath
	}
	base := strings.ToUpper(strings.TrimSuffix(component, filepath.Ext(component)))
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
		(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		return ErrUnsafePath
	}
	return nil
}

func validateRelativePath(relative string) error {
	if relative == "" || len(relative) > maxPathBytes || strings.Count(relative, "/") >= maxPathDepth || path.IsAbs(relative) || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return ErrUnsafePath
	}
	for _, component := range strings.Split(relative, "/") {
		if err := validatePathComponent(component); err != nil {
			return err
		}
	}
	return nil
}

func relevantMode(info os.FileInfo) uint32 {
	if info.Mode().IsRegular() {
		return fileGitMode(info)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0o120000
	}
	if info.IsDir() {
		return 0o040000
	}
	return uint32(info.Mode().Perm())
}

func fileGitMode(info os.FileInfo) uint32 {
	if info.Mode().Perm()&0o111 != 0 {
		return 0o100755
	}
	return 0o100644
}

func validateSymlinkTargets(ctx context.Context, entries []Entry, exclusions []Exclusion) error {
	entriesByFold := make(map[string]Entry, len(entries))
	excludedByFold := make(map[string]struct{}, len(exclusions))
	for _, exclusion := range exclusions {
		excludedByFold[foldedPathKey(exclusion.Path)] = struct{}{}
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("validate symlinks cancelled: %w", err)
		}
		key := foldedPathKey(entry.Path)
		if existing, ok := entriesByFold[key]; ok && existing.Path != entry.Path {
			return fmt.Errorf("%w: paths %q and %q collide under case folding", ErrUnsafePath, existing.Path, entry.Path)
		}
		entriesByFold[key] = entry
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("validate symlinks cancelled: %w", err)
		}
		if entry.Type != EntrySymlink {
			continue
		}
		if err := validateSymlinkTargetText(entry.Path, entry.Target); err != nil {
			return err
		}
		if err := proveSymlinkStaysWithin(entry.Path, entry.Target, entriesByFold, excludedByFold); err != nil {
			if errors.Is(err, errSymlinkTargetExcluded) {
				return fmt.Errorf("%w: %q", ErrSymlinkExcludedTarget, entry.Path)
			}
			return fmt.Errorf("%w: %q -> %q", err, entry.Path, entry.Target)
		}
	}
	return nil
}

func validateSymlinkTargetText(linkPath, target string) error {
	if target == "" || len(target) > maxPathBytes || strings.Count(target, "/") >= maxPathDepth || !utf8.ValidString(target) || strings.ContainsAny(target, "\\:\x00") || path.IsAbs(target) || filepath.IsAbs(target) || filepath.VolumeName(target) != "" {
		return fmt.Errorf("%w: %q has an absolute or invalid target", ErrSymlinkEscape, linkPath)
	}
	for _, component := range strings.Split(target, "/") {
		if component == "" || component == "." || component == ".." {
			continue
		}
		if err := validatePathComponent(component); err != nil {
			return fmt.Errorf("%w: %q has an invalid target", ErrSymlinkEscape, linkPath)
		}
	}
	return nil
}

func proveSymlinkStaysWithin(linkPath, target string, entriesByFold map[string]Entry, excludedByFold map[string]struct{}) error {
	parent := path.Dir(linkPath)
	stack := []string{}
	if parent != "." {
		stack = strings.Split(parent, "/")
	}
	parts := strings.Split(target, "/")
	expansions := 0
	for index := 0; index < len(parts); index++ {
		component := parts[index]
		if component == "" || component == "." {
			continue
		}
		if component == ".." {
			if len(stack) == 0 {
				return ErrSymlinkEscape
			}
			stack = stack[:len(stack)-1]
			continue
		}
		stack = append(stack, component)
		candidate := strings.Join(stack, "/")
		if _, excluded := excludedByFold[foldedPathKey(candidate)]; excluded {
			return errSymlinkTargetExcluded
		}
		matched, ok := entriesByFold[foldedPathKey(candidate)]
		if !ok {
			continue
		}
		if matched.Path != candidate {
			return ErrUnsafePath
		}
		if matched.Type != EntrySymlink {
			continue
		}
		chainedTarget := matched.Target
		expansions++
		if expansions > 40 || validateSymlinkTargetText(candidate, chainedTarget) != nil {
			return ErrSymlinkEscape
		}
		// Resolve a link component from its containing directory, then continue
		// with the original suffix. A bounded expansion rejects cycles safely.
		stack = stack[:len(stack)-1]
		remaining := append([]string(nil), parts[index+1:]...)
		parts = append(strings.Split(chainedTarget, "/"), remaining...)
		index = -1
	}
	return nil
}

func foldedPathKey(value string) string {
	decomposed := norm.NFD.String(value)
	return norm.NFD.String(cases.Fold().String(decomposed))
}

func manifestHash(manifest Manifest) string {
	digest := sha256.New()
	_, _ = digest.Write(manifestDomainBytes)
	writeUint64(digest, uint64(manifest.Version))
	writeUint64(digest, uint64(manifest.FileCount))
	writeUint64(digest, uint64(manifest.TotalBytes))
	for _, entry := range manifest.Entries {
		writeString(digest, entry.Path)
		writeString(digest, string(entry.Type))
		writeUint64(digest, uint64(entry.Mode))
		writeUint64(digest, uint64(entry.Size))
		writeString(digest, entry.SHA256)
		writeString(digest, entry.Target)
	}
	writeUint64(digest, uint64(len(manifest.Excluded)))
	for _, exclusion := range manifest.Excluded {
		writeString(digest, exclusion.Path)
		writeString(digest, exclusion.Reason)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// HashManifest returns the canonical digest for a complete manifest snapshot.
// Callers that accept persisted snapshots must also validate the entries
// against their own source policy before treating the hash as authoritative.
func HashManifest(manifest Manifest) string {
	return manifestHash(manifest)
}

func writeString(dst hash.Hash, value string) {
	writeUint64(dst, uint64(len(value)))
	_, _ = io.WriteString(dst, value)
}

func writeUint64(dst hash.Hash, value uint64) {
	var bytes [8]byte
	binary.BigEndian.PutUint64(bytes[:], value)
	_, _ = dst.Write(bytes[:])
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
