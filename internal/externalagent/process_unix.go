//go:build darwin || linux

package externalagent

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

type ownedCommand struct{ cmd *exec.Cmd }

func ownCommand(cmd *exec.Cmd) (*ownedCommand, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &ownedCommand{cmd: cmd}, nil
}
func (c *ownedCommand) start() error { return c.cmd.Start() }
func (c *ownedCommand) close() error { return nil }
func (c *ownedCommand) wait(ctx context.Context) error {
	// Ownership covers this process group. A descendant that deliberately calls
	// setsid escapes it; this adapter does not claim containment of that process.
	// Exit observation leaves the group leader unreaped, reserving its PGID until
	// this owner has finished signaling descendants. No signals follow Wait.
	exited := make(chan error, 1)
	go func() { exited <- waitProcessExit(c.cmd.Process.Pid) }()
	var observeErr, stopErr error
	graceful := false
	select {
	case observeErr = <-exited:
	case <-ctx.Done():
		graceful = true
	}
	pid := -c.cmd.Process.Pid
	if graceful {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			stopErr = err
		}
		timer := time.NewTimer(150 * time.Millisecond)
		<-timer.C
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && !(runtime.GOOS == "darwin" && !graceful && errors.Is(err, syscall.EPERM)) {
		stopErr = errors.Join(stopErr, err)
	}
	if graceful {
		observeErr = <-exited
	}
	return errors.Join(observeErr, stopErr, c.cmd.Wait())
}
