//go:build windows

package updater

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

// The installer asks for administrator rights; only the shell can show Windows' permission prompt for it, so it
// is opened the way a double click opens it.
func TestTheInstallerOpensThroughTheShellSoWindowsCanAskForPermission(t *testing.T) {
	var verb, file string
	var show int32
	shellExecute = func(_ windows.Handle, v, f, _, _ *uint16, s int32) error {
		verb, file, show = windows.UTF16PtrToString(v), windows.UTF16PtrToString(f), s
		return nil
	}
	t.Cleanup(func() { shellExecute = windows.ShellExecute })
	if err := Apply(context.Background(), `C:\Temp\harflex-update-1\Harflex-0.2.34-windows-amd64-setup.exe`); err != nil {
		t.Fatal(err)
	}
	if verb != "open" || file != `C:\Temp\harflex-update-1\Harflex-0.2.34-windows-amd64-setup.exe` || show != windows.SW_SHOWNORMAL {
		t.Fatalf("opened %q with %q (show %d)", file, verb, show)
	}
	shellExecute = func(windows.Handle, *uint16, *uint16, *uint16, *uint16, int32) error { return windows.ERROR_CANCELLED }
	if err := Apply(context.Background(), `C:\Temp\setup.exe`); !errors.Is(err, ErrCancelled) {
		t.Fatalf("declined permission: %v", err)
	}
}
