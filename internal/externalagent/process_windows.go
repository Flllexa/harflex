package externalagent

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ownedCommand struct {
	cmd       *exec.Cmd
	job       windows.Handle
	closeOnce sync.Once
	closeErr  error
}

func ownCommand(cmd *exec.Cmd) (*ownedCommand, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP}
	return &ownedCommand{cmd: cmd, job: job}, nil
}
func (c *ownedCommand) start() error {
	if err := c.cmd.Start(); err != nil {
		return errors.Join(err, c.close())
	}
	// Assign ownership before any user code can create descendants.
	err := attachAndResume(c.job, uint32(c.cmd.Process.Pid), nativeProcessOps())
	if err != nil {
		return errors.Join(err, windows.TerminateJobObject(c.job, 1), c.cmd.Process.Kill(), c.cmd.Wait(), c.close())
	}
	return nil
}

type windowsProcessOps struct {
	open   func(uint32, bool, uint32) (windows.Handle, error)
	assign func(windows.Handle, windows.Handle) error
	resume func(windows.Handle) error
	close  func(windows.Handle) error
}

func nativeProcessOps() windowsProcessOps {
	return windowsProcessOps{windows.OpenProcess, windows.AssignProcessToJobObject, resumeProcess, windows.CloseHandle}
}
func attachAndResume(job windows.Handle, pid uint32, ops windowsProcessOps) (err error) {
	p, err := ops.open(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, pid)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, ops.close(p)) }()
	if err = ops.assign(job, p); err != nil {
		return err
	}
	return ops.resume(p)
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func resumeProcess(process windows.Handle) error {
	if err := ntResumeProcess.Find(); err != nil {
		return err
	}
	status, _, _ := ntResumeProcess.Call(uintptr(process))
	if int32(status) < 0 {
		return windows.NTStatus(status)
	}
	return nil
}
func (c *ownedCommand) wait(ctx context.Context) error {
	exited := make(chan error, 1)
	go func() { exited <- c.cmd.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-ctx.Done():
		err := windows.TerminateJobObject(c.job, 1)
		if err != nil {
			_ = c.cmd.Process.Kill()
		}
		return errors.Join(err, <-exited)
	}
}
func (c *ownedCommand) close() error {
	c.closeOnce.Do(func() { c.closeErr = windows.CloseHandle(c.job) })
	return c.closeErr
}
