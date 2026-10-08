//go:build darwin || linux

package sddworkspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateParentHandleIsRejectedIfPathMovesInsideSource(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	privateParent := filepath.Join(base, "private")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(privateParent, 0o700); err != nil {
		t.Fatal(err)
	}
	parentRoot, err := os.OpenRoot(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	defer parentRoot.Close()
	parentFSID, err := rootFilesystemIdentity(parentRoot)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(source, "moved-private-parent")
	if err := os.Rename(privateParent, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, privateParent); err != nil {
		t.Fatal(err)
	}
	if err := verifyPrivateParentOutsideSource(source, parentRoot, parentFSID); err == nil {
		t.Fatal("opened private parent was allowed after its path moved inside sourceRoot")
	}
}
