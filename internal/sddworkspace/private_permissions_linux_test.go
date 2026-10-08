//go:build linux && !android

package sddworkspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCreatePrivateCopyRejectsWritablePrivateParentBeforeStaging(t *testing.T) {
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "file.txt"), []byte("source"), 0o600)
	if err := os.Chmod(privateParent, 0o777); err != nil {
		t.Fatal(err)
	}
	copy, err := CreatePrivateCopy(context.Background(), source, privateParent)
	if copy.Root != "" {
		_ = os.RemoveAll(copy.Root)
	}
	if !errors.Is(err, ErrPrivatePermissionsUnavailable) || copy.Root != "" {
		t.Fatalf("CreatePrivateCopy() = %#v, %v; want parent-permissions rejection", copy, err)
	}
	contents, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("unsafe private parent received staging content: %#v", contents)
	}
}

func TestPrivateParentAncestryAllowsStickyTemporaryDirectory(t *testing.T) {
	info, err := os.Stat(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSticky == 0 {
		t.Skip("temporary directory is not sticky")
	}
	if err := verifyPrivatePathAncestors(os.TempDir()); err != nil {
		t.Fatalf("sticky temporary ancestor was rejected: %v", err)
	}
}

func TestStickyDirectoryOwnedByForeignUIDIsRejected(t *testing.T) {
	info := syntheticDirectoryInfo{
		mode: os.ModeDir | os.ModeSticky | 0o777,
		stat: &syscall.Stat_t{Uid: uint32(os.Geteuid()) + 1},
	}
	if err := verifyDirectoryMutationPolicy(info, "foreign sticky parent"); !errors.Is(err, ErrPrivatePermissionsUnavailable) {
		t.Fatalf("verifyDirectoryMutationPolicy() error = %v, want ErrPrivatePermissionsUnavailable", err)
	}
}

type syntheticDirectoryInfo struct {
	mode os.FileMode
	stat *syscall.Stat_t
}

func (info syntheticDirectoryInfo) Name() string       { return "synthetic" }
func (info syntheticDirectoryInfo) Size() int64        { return 0 }
func (info syntheticDirectoryInfo) Mode() os.FileMode  { return info.mode }
func (info syntheticDirectoryInfo) ModTime() time.Time { return time.Time{} }
func (info syntheticDirectoryInfo) IsDir() bool        { return info.mode.IsDir() }
func (info syntheticDirectoryInfo) Sys() any           { return info.stat }

var _ fs.FileInfo = syntheticDirectoryInfo{}

func TestLinuxCleanupPartialPrivateCopyReportsFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can traverse a mode-000 directory")
	}
	parentRootPath := t.TempDir()
	parentRoot, err := os.OpenRoot(parentRootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer parentRoot.Close()
	if err := parentRoot.Mkdir("stage", 0o700); err != nil {
		t.Fatal(err)
	}
	stage, err := parentRoot.OpenRoot("stage")
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.WriteFile("partial", []byte("partial"), 0o600); err != nil {
		stage.Close()
		t.Fatal(err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := parentRoot.Chmod("stage", 0); err != nil {
		t.Fatal(err)
	}
	cleanupErr := cleanupPartialPrivateCopy(parentRoot, "stage", filepath.Join(parentRootPath, "stage"), errors.New("copy failed"))
	if !errors.Is(cleanupErr, ErrPrivateCopyCleanup) {
		t.Fatalf("cleanup error = %v, want ErrPrivateCopyCleanup", cleanupErr)
	}
	if err := parentRoot.Chmod("stage", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := parentRoot.RemoveAll("stage"); err != nil {
		t.Fatal(err)
	}
}

func TestCreatePrivateCopyRechecksParentAfterSourceScanBeforeStaging(t *testing.T) {
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "file.txt"), []byte("source"), 0o600)
	movedParent := filepath.Join(source, "moved-private-parent")
	copy, err := createPrivateCopy(context.Background(), source, privateParent, defaultScanLimits, createCopyHooks{
		beforeStageMkdir: func() error {
			if err := os.Rename(privateParent, movedParent); err != nil {
				return err
			}
			return os.Symlink(movedParent, privateParent)
		},
	})
	if err == nil || copy.Root != "" {
		t.Fatalf("createPrivateCopy() = %#v, %v; want containment rejection", copy, err)
	}
	if !strings.Contains(err.Error(), "private parent must not be inside the source root") {
		t.Fatalf("createPrivateCopy() error = %v, want immediate ancestry rejection", err)
	}
	contents, readErr := os.ReadDir(movedParent)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(contents) != 0 {
		t.Fatalf("source received transient staging content: %#v", contents)
	}
}
