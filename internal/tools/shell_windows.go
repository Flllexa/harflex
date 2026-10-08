package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const shellToolName = "powershell"

type shellCommand struct {
	cmd       *exec.Cmd
	job       windows.Handle
	ops       *shellWindowsProcessOps
	closeOnce sync.Once
	closeErr  error
}

func createShellJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create shell job: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("configure shell job: %w", err)
	}
	return job, nil
}

func newShellCommand(command string) (*shellCommand, error) {
	job, err := createShellJob()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP}
	return &shellCommand{cmd: cmd, job: job}, nil
}

func (c *shellCommand) start() error {
	if c.cmd.SysProcAttr == nil {
		c.cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP
	if err := c.cmd.Start(); err != nil {
		return errors.Join(err, c.close())
	}
	ops := nativeShellWindowsProcessOps()
	if c.ops != nil {
		ops = *c.ops
	}
	// No user code can execute until the suspended process is owned by the job.
	if err := attachAndResumeShell(c.job, uint32(c.cmd.Process.Pid), ops); err != nil {
		terminateErr := windows.TerminateJobObject(c.job, 1)
		killErr := c.cmd.Process.Kill()
		waitErr := c.cmd.Wait()
		return errors.Join(err, terminateErr, killErr, waitErr, c.close())
	}
	return nil
}

type shellWindowsProcessOps struct {
	open   func(uint32, bool, uint32) (windows.Handle, error)
	assign func(windows.Handle, windows.Handle) error
	resume func(windows.Handle) error
	close  func(windows.Handle) error
}

func nativeShellWindowsProcessOps() shellWindowsProcessOps {
	return shellWindowsProcessOps{open: windows.OpenProcess, assign: windows.AssignProcessToJobObject, resume: resumeShellProcess, close: windows.CloseHandle}
}

func attachAndResumeShell(job windows.Handle, pid uint32, ops shellWindowsProcessOps) (err error) {
	rights := uint32(windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE | windows.PROCESS_SUSPEND_RESUME)
	process, err := ops.open(rights, false, pid)
	if err != nil {
		return fmt.Errorf("open suspended shell process: %w", err)
	}
	defer func() { err = errors.Join(err, ops.close(process)) }()
	if err := ops.assign(job, process); err != nil {
		return fmt.Errorf("assign suspended shell to job: %w", err)
	}
	if err := ops.resume(process); err != nil {
		return fmt.Errorf("resume owned shell process: %w", err)
	}
	return nil
}

var shellNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// os/exec closes the primary thread handle. NtResumeProcess resumes the owned
// suspended process through its process handle without racing a thread snapshot.
func resumeShellProcess(process windows.Handle) error {
	if err := shellNtResumeProcess.Find(); err != nil {
		return fmt.Errorf("resolve process resume: %w", err)
	}
	status, _, _ := shellNtResumeProcess.Call(uintptr(process))
	if int32(status) < 0 {
		return windows.NTStatus(status)
	}
	return nil
}

func (c *shellCommand) wait(ctx context.Context) error {
	exited := make(chan error, 1)
	go func() { exited <- c.cmd.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-ctx.Done():
		terminateErr := windows.TerminateJobObject(c.job, 1)
		if terminateErr != nil {
			_ = c.cmd.Process.Kill()
		}
		return errors.Join(terminateErr, <-exited)
	}
}

func (c *shellCommand) close() error {
	c.closeOnce.Do(func() { c.closeErr = windows.CloseHandle(c.job) })
	return c.closeErr
}
