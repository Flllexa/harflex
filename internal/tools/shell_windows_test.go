package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ignoreShellTestTermination() {}

func TestShellJobConfiguration(t *testing.T) {
	job, err := createShellJob()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(job) })
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		t.Fatal(err)
	}
	if limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatal("job does not own descendant lifetime")
	}
}

func TestShellJobAssignmentFailureReapsChild(t *testing.T) {
	enableShellTestHelper(t)
	job, err := createShellJob()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CloseHandle(job); err != nil {
		t.Fatal(err)
	}
	command := &shellCommand{cmd: exec.Command(os.Args[0], "-test.run=^TestShellHelperProcess$", "--", "wait"), job: job}
	if err := command.start(); err == nil {
		t.Fatal("invalid job assignment accepted")
	}
	if command.cmd.ProcessState == nil || command.cmd.ProcessState.Success() {
		t.Fatal("assignment failure did not kill and reap child")
	}
}

func TestShellWindowsStartsSuspended(t *testing.T) {
	command, err := newShellCommand("exit 0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.close() })
	want := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP)
	if command.cmd.SysProcAttr.CreationFlags&want != want {
		t.Fatalf("missing suspended start flags: %#x", command.cmd.SysProcAttr.CreationFlags)
	}
}

func TestShellWindowsAttachResumeOrder(t *testing.T) {
	for _, failure := range []string{"", "open", "assign", "resume"} {
		t.Run(failure, func(t *testing.T) {
			var calls []string
			failureErr := errors.New("operation failed")
			ops := shellWindowsProcessOps{
				open: func(rights uint32, inherit bool, pid uint32) (windows.Handle, error) {
					calls = append(calls, "open")
					if rights&windows.PROCESS_SUSPEND_RESUME == 0 || inherit || pid != 123 {
						t.Error("unsafe process open")
					}
					if failure == "open" {
						return 0, failureErr
					}
					return 2, nil
				},
				assign: func(job, process windows.Handle) error {
					calls = append(calls, "assign")
					if job != 1 || process != 2 {
						t.Error("wrong ownership handles")
					}
					if failure == "assign" {
						return failureErr
					}
					return nil
				},
				resume: func(process windows.Handle) error {
					calls = append(calls, "resume")
					if process != 2 {
						t.Error("wrong resume handle")
					}
					if failure == "resume" {
						return failureErr
					}
					return nil
				},
				close: func(process windows.Handle) error {
					calls = append(calls, "close")
					if process != 2 {
						t.Error("wrong closed handle")
					}
					return nil
				},
			}
			err := attachAndResumeShell(1, 123, ops)
			want := []string{"open", "assign", "resume", "close"}
			if failure == "open" {
				want = []string{"open"}
			} else if failure == "assign" {
				want = []string{"open", "assign", "close"}
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("unsafe launch order: got %v, want %v", calls, want)
			}
			if failure == "" && err != nil || failure != "" && !errors.Is(err, failureErr) {
				t.Fatalf("lost launch error: %v", err)
			}
		})
	}
}

func TestShellWindowsStartupFailuresNeverRunUserCode(t *testing.T) {
	for _, failure := range []string{"open", "assign", "resume"} {
		t.Run(failure, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "must-not-exist")
			enableShellTestHelper(t)
			job, err := createShellJob()
			if err != nil {
				t.Fatal(err)
			}
			ops := nativeShellWindowsProcessOps()
			failureErr := errors.New("injected startup failure")
			switch failure {
			case "open":
				ops.open = func(uint32, bool, uint32) (windows.Handle, error) { return 0, failureErr }
			case "assign":
				ops.assign = func(windows.Handle, windows.Handle) error { return failureErr }
			case "resume":
				ops.resume = func(windows.Handle) error { return failureErr }
			}
			command := &shellCommand{cmd: exec.Command(os.Args[0], "-test.run=^TestShellHelperProcess$", "--", "touch", marker), job: job, ops: &ops}
			t.Cleanup(func() { _ = command.close() })
			if err := command.start(); !errors.Is(err, failureErr) {
				t.Fatalf("lost failure: %v", err)
			}
			if command.cmd.ProcessState == nil || command.cmd.ProcessState.Success() {
				t.Fatal("startup failure did not reap terminated child")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("user code ran before successful ownership and resume: %v", err)
			}
			var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
			if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err == nil {
				t.Fatal("failed startup leaked job handle")
			}
		})
	}
}
