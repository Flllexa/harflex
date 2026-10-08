//go:build android || ios || windows || (!linux && !darwin)

package sddworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreatePrivateCopyFailsClosedWithoutACLProof(t *testing.T) {
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "file.txt"), []byte("source"), 0o600)
	copy, err := CreatePrivateCopy(context.Background(), source, privateParent)
	if copy.Root != "" {
		_ = os.RemoveAll(copy.Root)
	}
	if !errors.Is(err, ErrPrivatePermissionsUnavailable) || copy.Root != "" {
		t.Fatalf("CreatePrivateCopy() = %#v, %v; want fail-closed ACL error", copy, err)
	}
	contents, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("unsupported platform copy created content: %#v", contents)
	}
}
