//go:build darwin && !ios && !cgo

package sddworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinWithoutCGOFailsClosedBeforeStaging(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	privateParent := filepath.Join(base, "private")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(privateParent, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "file.txt"), []byte("source"), 0o600)

	copy, err := CreatePrivateCopy(context.Background(), source, privateParent)
	if copy.Root != "" {
		_ = os.RemoveAll(copy.Root)
	}
	if !errors.Is(err, ErrPrivatePermissionsUnavailable) || copy.Root != "" {
		t.Fatalf("CreatePrivateCopy() = %#v, %v; want fail-closed result without cgo", copy, err)
	}
	contents, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("CGO-disabled Darwin created staging content: %#v", contents)
	}
}
