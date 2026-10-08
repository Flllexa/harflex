package sddworkspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// MaxPatchEvidenceBytes bounds the canonical JSON persisted for one completed
// Code run, including metadata and base64-encoded file contents.
const MaxPatchEvidenceBytes int64 = 64 << 20

const patchEvidenceVersion = 1

type PatchEvidence struct {
	Version            int           `json:"version"`
	BaselineHash       string        `json:"baselineHash"`
	SourceManifestHash string        `json:"sourceManifestHash"`
	ResultManifestHash string        `json:"resultManifestHash"`
	Changes            []PatchChange `json:"changes"`
}

// PatchChange represents every changed path once and binds complete before
// and after metadata. File bytes are base64 encoded so arbitrary content is
// preserved exactly; symlink targets remain text and are never dereferenced.
type PatchChange struct {
	Path   string          `json:"path"`
	Kinds  []ChangeKind    `json:"kinds"`
	Before *PatchFileEntry `json:"before,omitempty"`
	After  *PatchFileEntry `json:"after,omitempty"`
}

type PatchFileEntry struct {
	Type            EntryType `json:"type"`
	Mode            uint32    `json:"mode"`
	Size            int64     `json:"size"`
	SHA256          string    `json:"sha256"`
	Target          string    `json:"target,omitempty"`
	ContentEncoding string    `json:"contentEncoding,omitempty"`
	ContentBase64   string    `json:"contentBase64,omitempty"`
}

// BuildPatch constructs a canonical, content-complete patch from the source
// baseline and private result. Both roots are scanned before and after reading
// changed content; all file access stays under os.Root and refuses symlinks.
func BuildPatch(ctx context.Context, sourceRoot, resultRoot string, baseline, sourceReadback, result Manifest) (PatchEvidence, []byte, string, error) {
	return buildPatchWithLimit(ctx, sourceRoot, resultRoot, baseline, sourceReadback, result, MaxPatchEvidenceBytes)
}

// BuildPatchWithLimit is the bounded variant used by callers that need to
// apply a smaller product limit while retaining the same no-truncation rules.
func BuildPatchWithLimit(ctx context.Context, sourceRoot, resultRoot string, baseline, sourceReadback, result Manifest, maxBytes int64) (PatchEvidence, []byte, string, error) {
	return buildPatchWithLimit(ctx, sourceRoot, resultRoot, baseline, sourceReadback, result, maxBytes)
}

func buildPatchWithLimit(ctx context.Context, sourceRoot, resultRoot string, baseline, sourceReadback, result Manifest, maxBytes int64) (PatchEvidence, []byte, string, error) {
	var empty PatchEvidence
	if ctx == nil || maxBytes < 1 {
		return empty, nil, "", errors.New("patch context and positive size limit are required")
	}
	if !validPatchManifest(baseline) || !validPatchManifest(sourceReadback) || sourceReadback.Hash != baseline.Hash {
		return empty, nil, "", fmt.Errorf("%w: baseline and source readback differ", ErrPatchSourceDrift)
	}
	if !validPatchManifest(result) {
		return empty, nil, "", fmt.Errorf("%w: invalid result snapshot", ErrPatchResultDrift)
	}

	sourcePath, err := canonicalDirectory(sourceRoot)
	if err != nil {
		return empty, nil, "", fmt.Errorf("open patch source root: %w", err)
	}
	source, err := os.OpenRoot(sourcePath)
	if err != nil {
		return empty, nil, "", fmt.Errorf("open patch source root: %w", err)
	}
	defer source.Close()
	resultPath, err := canonicalDirectory(resultRoot)
	if err != nil {
		return empty, nil, "", fmt.Errorf("open patch result root: %w", err)
	}
	private, err := os.OpenRoot(resultPath)
	if err != nil {
		return empty, nil, "", fmt.Errorf("open patch result root: %w", err)
	}
	defer private.Close()

	currentSource, err := scanRootWithLimits(ctx, source, defaultScanLimits, scanHooks{})
	if err != nil {
		return empty, nil, "", fmt.Errorf("%w: scan origin before patch readback: %v", ErrPatchSourceDrift, err)
	}
	if currentSource.Hash != baseline.Hash {
		return empty, nil, "", fmt.Errorf("%w: origin no longer matches baseline", ErrPatchSourceDrift)
	}
	currentResult, err := scanRootWithLimits(ctx, private, defaultScanLimits, scanHooks{})
	if err != nil {
		return empty, nil, "", fmt.Errorf("%w: scan private result before patch readback: %v", ErrPatchResultDrift, err)
	}
	if currentResult.Hash != result.Hash {
		return empty, nil, "", fmt.Errorf("%w: private root no longer matches result snapshot", ErrPatchResultDrift)
	}

	beforeEntries := patchManifestEntries(baseline)
	afterEntries := patchManifestEntries(result)
	changes := Compare(baseline, result)
	patch := PatchEvidence{
		Version: patchEvidenceVersion, BaselineHash: baseline.Hash,
		SourceManifestHash: sourceReadback.Hash, ResultManifestHash: result.Hash,
		Changes: make([]PatchChange, 0),
	}
	contentLimit := maxBytes - maxBytes/4
	var contentBytes int64
	for index := 0; index < len(changes); {
		if err := ctx.Err(); err != nil {
			return empty, nil, "", fmt.Errorf("build patch cancelled: %w", err)
		}
		end := index + 1
		for end < len(changes) && changes[end].Path == changes[index].Path {
			end++
		}
		path := changes[index].Path
		change := PatchChange{Path: path, Kinds: make([]ChangeKind, 0, end-index)}
		for _, item := range changes[index:end] {
			change.Kinds = append(change.Kinds, item.Kind)
		}
		if entry, ok := beforeEntries[path]; ok {
			node, size, readErr := readPatchEntry(ctx, source, entry, contentLimit-contentBytes)
			if readErr != nil {
				if errors.Is(readErr, ErrPatchTooLarge) {
					return empty, nil, "", readErr
				}
				return empty, nil, "", fmt.Errorf("%w: read before content for %q: %v", ErrPatchSourceDrift, path, readErr)
			}
			contentBytes += size
			change.Before = &node
		}
		if entry, ok := afterEntries[path]; ok {
			node, size, readErr := readPatchEntry(ctx, private, entry, contentLimit-contentBytes)
			if readErr != nil {
				if errors.Is(readErr, ErrPatchTooLarge) {
					return empty, nil, "", readErr
				}
				return empty, nil, "", fmt.Errorf("%w: read after content for %q: %v", ErrPatchResultDrift, path, readErr)
			}
			contentBytes += size
			change.After = &node
		}
		patch.Changes = append(patch.Changes, change)
		index = end
	}

	verifiedSource, err := scanRootWithLimits(ctx, source, defaultScanLimits, scanHooks{})
	if err != nil || verifiedSource.Hash != baseline.Hash {
		if err != nil {
			return empty, nil, "", fmt.Errorf("%w: final origin scan: %v", ErrPatchSourceDrift, err)
		}
		return empty, nil, "", fmt.Errorf("%w: origin changed during patch readback", ErrPatchSourceDrift)
	}
	verifiedResult, err := scanRootWithLimits(ctx, private, defaultScanLimits, scanHooks{})
	if err != nil || verifiedResult.Hash != result.Hash {
		if err != nil {
			return empty, nil, "", fmt.Errorf("%w: final private result scan: %v", ErrPatchResultDrift, err)
		}
		return empty, nil, "", fmt.Errorf("%w: private root changed during patch readback", ErrPatchResultDrift)
	}

	encoded, err := json.Marshal(patch)
	if err != nil {
		return empty, nil, "", fmt.Errorf("encode patch evidence: %w", err)
	}
	if int64(len(encoded)) > maxBytes {
		return empty, nil, "", fmt.Errorf("%w: got %d bytes, limit %d", ErrPatchTooLarge, len(encoded), maxBytes)
	}
	digest := sha256.Sum256(encoded)
	patchHash := hex.EncodeToString(digest[:])
	if err := ValidatePatchEvidence(encoded, patchHash, baseline, sourceReadback.Hash, result); err != nil {
		return empty, nil, "", fmt.Errorf("validate constructed patch evidence: %w", err)
	}
	return patch, encoded, patchHash, nil
}

// ValidatePatchEvidence confirms canonical serialization, content integrity,
// and exact correspondence to both bound manifests before persistence/readback.
func ValidatePatchEvidence(encoded []byte, patchHash string, baseline Manifest, sourceManifestHash string, result Manifest) error {
	if int64(len(encoded)) > MaxPatchEvidenceBytes || len(encoded) == 0 {
		return ErrPatchTooLarge
	}
	digest := sha256.Sum256(encoded)
	if patchHash != hex.EncodeToString(digest[:]) {
		return errors.New("patch evidence hash mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var patch PatchEvidence
	if err := decoder.Decode(&patch); err != nil {
		return fmt.Errorf("decode patch evidence: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("patch evidence contains trailing data")
	}
	canonical, err := json.Marshal(patch)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return errors.New("patch evidence is not canonically encoded")
	}
	if patch.Version != patchEvidenceVersion || !validPatchManifest(baseline) || !validPatchManifest(result) ||
		patch.BaselineHash != baseline.Hash || patch.SourceManifestHash != sourceManifestHash ||
		patch.SourceManifestHash != baseline.Hash || patch.ResultManifestHash != result.Hash {
		return errors.New("patch evidence snapshot binding mismatch")
	}
	expected := Compare(baseline, result)
	changeGroups := make([]PatchChange, 0)
	for index := 0; index < len(expected); {
		end := index + 1
		for end < len(expected) && expected[end].Path == expected[index].Path {
			end++
		}
		group := PatchChange{Path: expected[index].Path, Kinds: make([]ChangeKind, 0, end-index)}
		for _, item := range expected[index:end] {
			group.Kinds = append(group.Kinds, item.Kind)
		}
		changeGroups = append(changeGroups, group)
		index = end
	}
	if len(changeGroups) != len(patch.Changes) {
		return errors.New("patch evidence change set differs from manifests")
	}
	beforeEntries, afterEntries := patchManifestEntries(baseline), patchManifestEntries(result)
	for index, want := range changeGroups {
		got := patch.Changes[index]
		if got.Path != want.Path || !slices.Equal(got.Kinds, want.Kinds) || !validRelativePatchPath(got.Path) {
			return fmt.Errorf("patch evidence change %d differs from manifests", index)
		}
		if entry, ok := beforeEntries[got.Path]; ok {
			if got.Before == nil || !validPatchNode(*got.Before, entry) {
				return fmt.Errorf("patch evidence before entry %q is invalid", got.Path)
			}
		} else if got.Before != nil {
			return fmt.Errorf("patch evidence has unexpected before entry %q", got.Path)
		}
		if entry, ok := afterEntries[got.Path]; ok {
			if got.After == nil || !validPatchNode(*got.After, entry) {
				return fmt.Errorf("patch evidence after entry %q is invalid", got.Path)
			}
		} else if got.After != nil {
			return fmt.Errorf("patch evidence has unexpected after entry %q", got.Path)
		}
	}
	return nil
}

func validPatchManifest(manifest Manifest) bool {
	return manifest.Version == manifestVersion && manifest.Hash != "" && manifest.Hash == manifestHash(manifest)
}

func patchManifestEntries(manifest Manifest) map[string]Entry {
	entries := make(map[string]Entry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		entries[entry.Path] = entry
	}
	return entries
}

func validRelativePatchPath(value string) bool { return validateRelativePath(value) == nil }

func readPatchEntry(ctx context.Context, root *os.Root, expected Entry, contentRemaining int64) (PatchFileEntry, int64, error) {
	var empty PatchFileEntry
	if err := ctx.Err(); err != nil {
		return empty, 0, err
	}
	if err := validateRelativePath(expected.Path); err != nil {
		return empty, 0, err
	}
	path := filepath.FromSlash(expected.Path)
	before, err := root.Lstat(path)
	if err != nil {
		return empty, 0, err
	}
	if !patchInfoMatchesEntry(before, expected) {
		return empty, 0, ErrSourceDrift
	}
	node := PatchFileEntry{Type: expected.Type, Mode: expected.Mode, Size: expected.Size, SHA256: expected.SHA256, Target: expected.Target}
	switch expected.Type {
	case EntryFile:
		if expected.Size < 0 || expected.Size > contentRemaining {
			return empty, 0, ErrPatchTooLarge
		}
		file, err := openRegularFile(root, expected.Path)
		if err != nil {
			return empty, 0, err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(before, opened) || !patchInfoMatchesEntry(opened, expected) {
			if err != nil {
				return empty, 0, err
			}
			return empty, 0, ErrSourceDrift
		}
		content, err := io.ReadAll(io.LimitReader(file, expected.Size+1))
		if err != nil {
			return empty, 0, err
		}
		if int64(len(content)) != expected.Size || sha256Hex(content) != expected.SHA256 {
			return empty, 0, ErrSourceDrift
		}
		afterFD, err := file.Stat()
		if err != nil {
			return empty, 0, err
		}
		afterPath, err := root.Lstat(path)
		if err != nil || !os.SameFile(before, afterFD) || !os.SameFile(before, afterPath) || !patchInfoMatchesEntry(afterPath, expected) {
			if err != nil {
				return empty, 0, err
			}
			return empty, 0, ErrSourceDrift
		}
		node.ContentEncoding = "base64"
		node.ContentBase64 = base64.StdEncoding.EncodeToString(content)
		return node, int64(len(content)), nil
	case EntrySymlink:
		target, err := root.Readlink(path)
		if err != nil {
			return empty, 0, err
		}
		after, err := root.Lstat(path)
		if err != nil || !os.SameFile(before, after) || target != expected.Target || !patchInfoMatchesEntry(after, expected) {
			if err != nil {
				return empty, 0, err
			}
			return empty, 0, ErrSourceDrift
		}
		return node, 0, nil
	case EntryDirectory:
		return node, 0, nil
	default:
		return empty, 0, ErrSpecialFile
	}
}

func patchInfoMatchesEntry(info os.FileInfo, expected Entry) bool {
	switch expected.Type {
	case EntryFile:
		return info.Mode().IsRegular() && info.Size() == expected.Size && fileGitMode(info) == expected.Mode
	case EntryDirectory:
		return info.IsDir() && expected.Mode == 0o040000
	case EntrySymlink:
		return info.Mode()&os.ModeSymlink != 0 && expected.Mode == 0o120000
	default:
		return false
	}
}

func validPatchNode(node PatchFileEntry, expected Entry) bool {
	if node.Type != expected.Type || node.Mode != expected.Mode || node.Size != expected.Size || node.SHA256 != expected.SHA256 || node.Target != expected.Target {
		return false
	}
	switch expected.Type {
	case EntryFile:
		if node.ContentEncoding != "base64" {
			return false
		}
		content, err := base64.StdEncoding.DecodeString(node.ContentBase64)
		return err == nil && int64(len(content)) == expected.Size && sha256Hex(content) == expected.SHA256
	case EntrySymlink:
		return node.ContentEncoding == "" && node.ContentBase64 == "" && sha256Hex([]byte(expected.Target)) == expected.SHA256
	case EntryDirectory:
		return node.ContentEncoding == "" && node.ContentBase64 == "" && expected.Size == 0 && expected.SHA256 == emptyContentSHA256
	default:
		return false
	}
}
