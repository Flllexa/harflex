//go:build windows

package procattr

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHideKeepsTheFlagsACommandAlreadyHas(t *testing.T) {
	cmd := exec.Command("git", "status")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	Hide(cmd)
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 || cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("flags: %#x hide=%v", cmd.SysProcAttr.CreationFlags, cmd.SysProcAttr.HideWindow)
	}
}
