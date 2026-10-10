//go:build linux

package updater

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// StagingDir is where the installer is downloaded: beside the AppImage, so that replacing it is a rename on one volume.
func StagingDir() (string, error) {
	image := os.Getenv("APPIMAGE")
	if image == "" {
		return "", ErrNotInstalled // .deb and .rpm installs belong to the package manager
	}
	return os.MkdirTemp(filepath.Dir(image), ".harflex-update-")
}

// Apply replaces the running AppImage with the downloaded one and starts it once this process has exited.
func Apply(_ context.Context, installer string) error {
	image := os.Getenv("APPIMAGE")
	if image == "" {
		return ErrNotInstalled
	}
	if err := os.Chmod(installer, 0o755); err != nil {
		return err
	}
	if err := os.Rename(installer, image); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Dir(installer))
	helper := exec.Command("/bin/sh", "-c", "sleep 1; exec \"$0\"", image)
	if err := helper.Start(); err != nil {
		return err
	}
	return helper.Process.Release()
}

// CanApply says whether this copy is an AppImage in a folder the user can write to.
func CanApply() error {
	image := os.Getenv("APPIMAGE")
	if image == "" {
		return ErrNotInstalled
	}
	if syscall.Access(filepath.Dir(image), 2) != nil { // W_OK
		return ErrNotInstalled
	}
	return nil
}
