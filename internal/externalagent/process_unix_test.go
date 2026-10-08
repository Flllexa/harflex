//go:build darwin || linux

package externalagent

import (
	"errors"
	"os/exec"
	"testing"
)

func TestExitObservationReservesLeaderUntilWait(t *testing.T) {
	cmd := exec.Command(helper(t, "fail"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if err := waitProcessExit(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("observer reaped leader: %v", err)
	}
}
