//go:build (linux && !android) || (darwin && !ios && cgo)

package sddworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPreflightPrivateCopyReturnsManifestWithoutCreatingFiles(t *testing.T) {
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "bin", "app"), []byte("executable"), 0o755)
	writeFile(t, filepath.Join(source, ".env"), []byte("SECRET=never copied"), 0o600)
	writeFile(t, filepath.Join(source, ".git", "config"), []byte("metadata"), 0o600)
	if err := os.Symlink("app", filepath.Join(source, "bin", "app-link")); err != nil {
		t.Fatal(err)
	}

	before, err := Scan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	parentBefore, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}

	manifest, err := PreflightPrivateCopy(context.Background(), source, privateParent)
	if err != nil {
		t.Fatalf("PreflightPrivateCopy() error = %v", err)
	}
	if manifest.Hash == "" || manifest.Hash != before.Hash || manifest.FileCount != 2 || manifest.TotalBytes != int64(len("executable")+len("app")) {
		t.Fatalf("preflight manifest summary = hash %q files %d bytes %d; want source hash %q, 2 files, and included content sizes", manifest.Hash, manifest.FileCount, manifest.TotalBytes, before.Hash)
	}
	if len(manifest.Excluded) != 2 {
		t.Fatalf("preflight exclusions = %#v; want .env and .git", manifest.Excluded)
	}
	entries := make(map[string]Entry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		entries[entry.Path] = entry
	}
	if entries["bin/app"].Mode != 0o100755 || entries["bin/app-link"].Mode != 0o120000 || entries["bin/app-link"].Target != "app" {
		t.Fatalf("preflight omitted source modes or symlink target: %#v", entries)
	}

	after, err := Scan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	parentAfter, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if after.Hash != before.Hash || len(parentBefore) != len(parentAfter) {
		t.Fatalf("preflight changed source or private parent: source hash %q -> %q, parent entries %d -> %d", before.Hash, after.Hash, len(parentBefore), len(parentAfter))
	}
}

func TestPreflightPrivateCopyRejectsPrivateParentInsideSource(t *testing.T) {
	source := t.TempDir()
	privateParent := filepath.Join(source, "private")
	if err := os.Mkdir(privateParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PreflightPrivateCopy(context.Background(), source, privateParent); err == nil {
		t.Fatal("PreflightPrivateCopy() accepted a private parent inside the source")
	}
	entries, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected preflight created content in source: %#v", entries)
	}
}

func TestPreflightAndCreatePrivateCopyRejectParentWithoutWriteSearchAccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("effective root may bypass POSIX mode permissions; an ACL-deny fixture is required for root")
	}
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "app"), []byte("local source"), 0o600)
	if err := os.Chmod(privateParent, 0o500); err != nil {
		t.Fatal(err)
	}

	if _, err := PreflightPrivateCopy(context.Background(), source, privateParent); !errors.Is(err, ErrPrivatePermissionsUnavailable) {
		t.Fatalf("PreflightPrivateCopy() error = %v; want ErrPrivatePermissionsUnavailable for a parent without write+search access", err)
	}
	copy, err := CreatePrivateCopy(context.Background(), source, privateParent)
	if !errors.Is(err, ErrPrivatePermissionsUnavailable) || copy.Root != "" {
		t.Fatalf("CreatePrivateCopy() = %#v, %v; want rejection before staging", copy, err)
	}
	entries, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected parent received staging content: %#v", entries)
	}
}
