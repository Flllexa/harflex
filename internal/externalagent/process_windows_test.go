package externalagent

import (
	"errors"
	"os/exec"
	"reflect"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsOwnsSuspendedProcessAndDescendants(t *testing.T) {
	c, err := ownCommand(exec.Command(helper(t, "wait")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.close() })
	want := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP)
	if c.cmd.SysProcAttr.CreationFlags&want != want {
		t.Fatal("process not created suspended")
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(c.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		t.Fatal(err)
	}
	if limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatal("job does not own descendant lifetime")
	}
}

func TestWindowsResumeRequiresOwnership(t *testing.T) {
	for _, failure := range []string{"", "open", "assign", "resume"} {
		t.Run(failure, func(t *testing.T) {
			var calls []string
			failureErr := errors.New("injected failure")
			step := func(name string) error {
				calls = append(calls, name)
				if failure == name {
					return failureErr
				}
				return nil
			}
			ops := windowsProcessOps{
				open: func(rights uint32, inherit bool, pid uint32) (windows.Handle, error) {
					if rights&windows.PROCESS_SUSPEND_RESUME == 0 || inherit || pid != 123 {
						t.Error("unsafe process open")
					}
					return 2, step("open")
				},
				assign: func(job, process windows.Handle) error {
					if job != 1 || process != 2 {
						t.Error("incorrect ownership")
					}
					return step("assign")
				},
				resume: func(process windows.Handle) error {
					if process != 2 {
						t.Error("incorrect process")
					}
					return step("resume")
				},
				close: func(process windows.Handle) error {
					if process != 2 {
						t.Error("incorrect handle")
					}
					return step("close")
				},
			}
			err := attachAndResume(1, 123, ops)
			want := []string{"open", "assign", "resume", "close"}
			if failure == "open" {
				want = []string{"open"}
			} else if failure == "assign" {
				want = []string{"open", "assign", "close"}
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("unsafe order %v", calls)
			}
			if failure == "" && err != nil || failure != "" && !errors.Is(err, failureErr) {
				t.Fatalf("lost cause %v", err)
			}
		})
	}
}

func TestWindowsAssignmentFailureReapsChild(t *testing.T) {
	c, err := ownCommand(exec.Command(helper(t, "wait")))
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CloseHandle(c.job); err != nil {
		t.Fatal(err)
	}
	if err := c.start(); err == nil {
		t.Fatal("invalid ownership accepted")
	}
	if c.cmd.ProcessState == nil || c.cmd.ProcessState.Success() {
		t.Fatal("failed startup did not reap child")
	}
}
