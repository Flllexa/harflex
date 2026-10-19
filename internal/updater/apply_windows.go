//go:build windows

package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// StagingDir is where the installer is downloaded.
func StagingDir() (string, error) { return os.MkdirTemp("", "harflex-update-") }

var shellExecute = windows.ShellExecute

// Apply opens the downloaded installer the way a double click does. It asks for administrator rights, and only the
// shell shows Windows' permission prompt for that; starting it directly fails with "requires elevation". The
// installer takes over once this process has exited, and the person finishes it there.
func Apply(_ context.Context, installer string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(installer)
	if err != nil {
		return err
	}
	dir, err := windows.UTF16PtrFromString(filepath.Dir(installer))
	if err != nil {
		return err
	}
	if err := shellExecute(0, verb, file, nil, dir, windows.SW_SHOWNORMAL); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return ErrCancelled
		}
		return err
	}
	return nil
}

// CanApply: the installer does the work, and asks for its own permission.
func CanApply() error { return nil }
