//go:build darwin || linux

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

const shellToolName = "bash"

type shellCommand struct {
	cmd      *exec.Cmd
	stopOnce sync.Once
	stopErr  error
}

func newShellCommand(command string) (*shellCommand, error) {
	shell := os.Getenv("SHELL")
	info, err := os.Stat(shell)
	if !filepath.IsAbs(shell) || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-lc", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &shellCommand{cmd: cmd}, nil
}

func (c *shellCommand) start() error { return c.cmd.Start() }

func (c *shellCommand) wait(ctx context.Context) error {
	// Observe exit without reaping: our zombie leader reserves its PID/PGID until
	// all group signals have finished. Only this owner can then call Wait.
	exited := make(chan error, 1)
	go func() { exited <- waitShellExit(c.cmd.Process.Pid) }()
	var observeErr error
	select {
	case observeErr = <-exited:
		c.stop(false)
	case <-ctx.Done():
		c.stop(true)
		observeErr = <-exited
	}
	if observeErr != nil {
		observeErr = fmt.Errorf("observe shell exit: %w", observeErr)
	}
	return errors.Join(observeErr, c.stopErr, c.cmd.Wait())
}

func (c *shellCommand) stop(graceful bool) {
	c.stopOnce.Do(func() {
		pid := -c.cmd.Process.Pid
		if graceful {
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
				c.stopErr = err
			}
			timer := time.NewTimer(150 * time.Millisecond)
			<-timer.C
		}
		// Darwin rejects signals to a group containing only its zombie leader.
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && !(runtime.GOOS == "darwin" && !graceful && errors.Is(err, syscall.EPERM)) {
			c.stopErr = errors.Join(c.stopErr, err)
		}
	})
}

// No signal is allowed after Wait has reaped the group leader.
func (c *shellCommand) close() error { return nil }
