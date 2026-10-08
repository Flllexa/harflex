//go:build linux

package sddworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScanRejectsCaseFoldCollisionsOnCaseSensitiveFilesystems(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Key"), []byte("one"), 0o600)
	writeFile(t, filepath.Join(root, "Key"), []byte("two"), 0o600)
	_, err := Scan(context.Background(), root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Scan() error = %v, want ErrUnsafePath for Unicode case-fold collision", err)
	}
}

func TestScanRejectsUnicodeNormalizationCollisionsOnLinux(t *testing.T) {
	root := t.TempDir()
	composed := filepath.Join(root, "\u00e9")
	decomposed := filepath.Join(root, "e\u0301")
	writeFile(t, composed, []byte("one"), 0o600)
	writeFile(t, decomposed, []byte("two"), 0o600)
	composedInfo, err := os.Stat(composed)
	if err != nil {
		t.Fatal(err)
	}
	decomposedInfo, err := os.Stat(decomposed)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(composedInfo, decomposedInfo) {
		t.Skip("filesystem normalizes the two names before creating distinct entries")
	}
	_, err = Scan(context.Background(), root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Scan() error = %v, want ErrUnsafePath for NFC/NFD path collision", err)
	}
}

func TestScanRejectsUnicodeNormalizationAliasToSymlinkChainOnLinux(t *testing.T) {
	root := t.TempDir()
	composedDirectory := filepath.Join(root, "\u00e9")
	writeFile(t, filepath.Join(composedDirectory, "target"), []byte("safe"), 0o600)
	if err := os.Symlink("target", filepath.Join(composedDirectory, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join("e\u0301", "link"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	_, err := Scan(context.Background(), root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Scan() error = %v, want ErrUnsafePath for NFD alias to symlink chain", err)
	}
}
