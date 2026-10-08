//go:build darwin

package sddworkspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIsWithinFilesystemRecognizesCaseInsensitiveAncestor(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "Project")
	parent := filepath.Join(base, "PROJECT", "cache")
	if err := os.MkdirAll(filepath.Join(source, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	aliasedInfo, err := os.Stat(parent)
	if err != nil {
		t.Skipf("temporary volume is case-sensitive: %v", err)
	}
	actualInfo, err := os.Stat(filepath.Join(source, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(aliasedInfo, actualInfo) {
		t.Skip("temporary volume does not alias path casing")
	}
	within, err := isWithinFilesystem(source, parent)
	if err != nil {
		t.Fatalf("isWithinFilesystem() error = %v", err)
	}
	if !within {
		t.Fatal("case-folded private parent was not recognized as inside the source")
	}
}

func TestCreatePrivateCopyRefusesCaseFoldedParentInsideSource(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "Project")
	actualParent := filepath.Join(source, "cache")
	aliasedParent := filepath.Join(base, "PROJECT", "cache")
	if err := os.MkdirAll(actualParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(aliasedParent); err != nil {
		t.Skipf("temporary volume is case-sensitive: %v", err)
	}
	copy, err := CreatePrivateCopy(context.Background(), source, aliasedParent)
	if err == nil || copy.Root != "" {
		t.Fatalf("CreatePrivateCopy() = %#v, %v; want rejection before staging", copy, err)
	}
	contents, err := os.ReadDir(actualParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("case-folded in-source destination created staging: %#v", contents)
	}
}
