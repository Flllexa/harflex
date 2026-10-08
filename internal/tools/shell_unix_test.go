//go:build !windows

package tools

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
)

func ignoreShellTestTermination() { signal.Ignore(syscall.SIGTERM) }

func TestShellExitObservationDoesNotReap(t *testing.T) {
	enableShellTestHelper(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestShellHelperProcess$", "--", "fail")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if err := waitShellExit(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	var exitError *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exitError) || exitError.ExitCode() != 7 {
		t.Fatalf("observer reaped the owned process: %v", err)
	}
}
