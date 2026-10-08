//go:build unix

package application

import (
	"errors"
	"os"
	"syscall"
)

// Unknown, current or reused process IDs remain protected. Only ESRCH proves
// the recorded process no longer exists; permission errors are not that proof.
func pipelineDesignOwnerDead(pid int) bool {
	return pid > 0 && pid != os.Getpid() && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
