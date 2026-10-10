//go:build windows

package updater

import (
	"context"
	"os"
	"os/exec"
)

// StagingDir is where the installer is downloaded.
func StagingDir() (string, error) { return os.MkdirTemp("", "harflex-update-") }

// Apply starts the downloaded installer; it takes over once this process has exited, and the person finishes it there.
func Apply(_ context.Context, installer string) error {
	helper := exec.Command(installer)
	if err := helper.Start(); err != nil {
		return err
	}
	return helper.Process.Release()
}

// CanApply: the installer does the work, and asks for its own permission.
func CanApply() error { return nil }
