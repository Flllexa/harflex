package sddworkspace

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildPatchPreservesExactContentsModesAndSymlinkTargets(t *testing.T) {
	source, result := patchRoots(t)
	beforeBinary := []byte{0x00, 0xff, 'a', '\n'}
	afterBinary := []byte{0x00, 'b', 0xfe, '\n'}
	writePatchFile(t, source, "binary.dat", beforeBinary, 0o600)
	writePatchFile(t, source, "removed.txt", []byte("remove me"), 0o600)
	writePatchFile(t, source, "mode.sh", []byte("#!/bin/sh\n"), 0o600)
	writePatchFile(t, source, "old-target.txt", []byte("old target"), 0o600)
	writePatchFile(t, source, "new-target.txt", []byte("new target"), 0o600)
	if err := os.Symlink("old-target.txt", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	writePatchFile(t, result, "binary.dat", afterBinary, 0o600)
	writePatchFile(t, result, "mode.sh", []byte("#!/bin/sh\n"), 0o700)
	writePatchFile(t, result, "old-target.txt", []byte("old target"), 0o600)
	writePatchFile(t, result, "new-target.txt", []byte("new target"), 0o600)
	writePatchFile(t, result, "added.txt", []byte("added exactly"), 0o600)
	if err := os.Symlink("new-target.txt", filepath.Join(result, "link")); err != nil {
		t.Fatal(err)
	}

	baseline := scanPatchRoot(t, source)
	resultManifest := scanPatchRoot(t, result)
	patch, encoded, patchHash, err := BuildPatch(t.Context(), source, result, baseline, baseline, resultManifest)
	if err != nil {
		t.Fatalf("BuildPatch() error = %v", err)
	}
	if patch.BaselineHash != baseline.Hash || patch.SourceManifestHash != baseline.Hash || patch.ResultManifestHash != resultManifest.Hash {
		t.Fatalf("patch snapshot bindings = %+v", patch)
	}
	digest := sha256.Sum256(encoded)
	if patchHash != hex.EncodeToString(digest[:]) {
		t.Fatalf("patch hash = %q; encoded patch SHA-256 = %x", patchHash, digest)
	}

	changes := make(map[string]PatchChange, len(patch.Changes))
	for _, change := range patch.Changes {
		changes[change.Path] = change
	}
	binary := changes["binary.dat"]
	if binary.Before == nil || binary.After == nil || binary.Before.SHA256 != patchSHA256(beforeBinary) || binary.After.SHA256 != patchSHA256(afterBinary) {
		t.Fatalf("binary before/after hashes are not bound to exact bytes: %+v", binary)
	}
	if got, err := base64.StdEncoding.DecodeString(binary.Before.ContentBase64); err != nil || string(got) != string(beforeBinary) {
		t.Fatalf("binary before content = %v, error=%v", got, err)
	}
	if got, err := base64.StdEncoding.DecodeString(binary.After.ContentBase64); err != nil || string(got) != string(afterBinary) {
		t.Fatalf("binary after content = %v, error=%v", got, err)
	}
	if mode := changes["mode.sh"]; mode.Before == nil || mode.After == nil || mode.Before.Mode != 0o100644 || mode.After.Mode != 0o100755 {
		t.Fatalf("mode-only patch lost Git mode: %+v", mode)
	}
	if link := changes["link"]; link.Before == nil || link.After == nil || link.Before.Type != EntrySymlink || link.After.Type != EntrySymlink || link.Before.Target != "old-target.txt" || link.After.Target != "new-target.txt" {
		t.Fatalf("symlink patch lost textual targets or followed the link: %+v", link)
	}
	if added := changes["added.txt"]; added.Before != nil || added.After == nil || added.After.ContentBase64 != base64.StdEncoding.EncodeToString([]byte("added exactly")) {
		t.Fatalf("created file patch = %+v", added)
	}
	if removed := changes["removed.txt"]; removed.Before == nil || removed.After != nil || removed.Before.ContentBase64 != base64.StdEncoding.EncodeToString([]byte("remove me")) {
		t.Fatalf("removed file patch = %+v", removed)
	}
}

func TestBuildPatchRejectsSourceAndResultDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, source, result string)
		want   error
	}{
		{name: "origin", mutate: func(t *testing.T, source, _ string) {
			writePatchFile(t, source, "file", []byte("drifted source"), 0o600)
		}, want: ErrPatchSourceDrift},
		{name: "private result", mutate: func(t *testing.T, _, result string) {
			writePatchFile(t, result, "file", []byte("drifted result"), 0o600)
		}, want: ErrPatchResultDrift},
		{name: "escaping symlink", mutate: func(t *testing.T, _, result string) {
			if err := os.Symlink("../../outside", filepath.Join(result, "escape")); err != nil {
				t.Fatal(err)
			}
		}, want: ErrPatchResultDrift},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, result := patchRoots(t)
			writePatchFile(t, source, "file", []byte("baseline"), 0o600)
			writePatchFile(t, result, "file", []byte("result"), 0o600)
			baseline, resultManifest := scanPatchRoot(t, source), scanPatchRoot(t, result)
			test.mutate(t, source, result)
			_, encoded, patchHash, err := BuildPatch(t.Context(), source, result, baseline, baseline, resultManifest)
			if !errors.Is(err, test.want) {
				t.Fatalf("BuildPatch() error = %v; want %v", err, test.want)
			}
			if len(encoded) != 0 || patchHash != "" {
				t.Fatalf("drift returned truncated or reviewable patch data: bytes=%d hash=%q", len(encoded), patchHash)
			}
		})
	}
}

func TestBuildPatchRejectsAggregateLimitWithoutTruncation(t *testing.T) {
	source, result := patchRoots(t)
	writePatchFile(t, source, "file", []byte("before contents"), 0o600)
	writePatchFile(t, result, "file", []byte("after contents"), 0o600)
	baseline, resultManifest := scanPatchRoot(t, source), scanPatchRoot(t, result)
	patch, encoded, patchHash, err := buildPatchWithLimit(t.Context(), source, result, baseline, baseline, resultManifest, 64)
	if !errors.Is(err, ErrPatchTooLarge) {
		t.Fatalf("buildPatchWithLimit() error = %v; want ErrPatchTooLarge", err)
	}
	if patch.Version != 0 || len(encoded) != 0 || patchHash != "" {
		t.Fatalf("oversized patch was returned partially: patch=%+v bytes=%d hash=%q", patch, len(encoded), patchHash)
	}
	patch, encoded, patchHash, err = buildPatchWithLimit(t.Context(), source, result, baseline, baseline, resultManifest, 4)
	if !errors.Is(err, ErrPatchTooLarge) || patch.Version != 0 || len(encoded) != 0 || patchHash != "" {
		t.Fatalf("raw content limit was not fail-closed: patch=%+v bytes=%d hash=%q error=%v", patch, len(encoded), patchHash, err)
	}
}

func patchRoots(t *testing.T) (string, string) {
	t.Helper()
	return t.TempDir(), t.TempDir()
}

func writePatchFile(t *testing.T, root, name string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), content, mode); err != nil {
		t.Fatal(err)
	}
}

func scanPatchRoot(t *testing.T, root string) Manifest {
	t.Helper()
	manifest, err := Scan(t.Context(), root)
	if err != nil {
		t.Fatalf("Scan(%q): %v", root, err)
	}
	return manifest
}

func patchSHA256(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
