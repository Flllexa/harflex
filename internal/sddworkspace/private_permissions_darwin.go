//go:build darwin && !ios && cgo

package sddworkspace

/*
#include <errno.h>
#include <sys/types.h>
#include <sys/acl.h>

static acl_t harflex_acl_get_fd(int fd, int *error_number) {
	errno = 0;
	acl_t acl = acl_get_fd_np(fd, ACL_TYPE_EXTENDED);
	*error_number = errno;
	return acl;
}

static int harflex_acl_valid(acl_t acl, int *error_number) {
	errno = 0;
	int result = acl_valid(acl);
	*error_number = errno;
	return result;
}

static int harflex_acl_get_entry(acl_t acl, int entry_id, acl_entry_t *entry, int *error_number) {
	errno = 0;
	int result = acl_get_entry(acl, entry_id, entry);
	*error_number = errno;
	return result;
}

static int harflex_acl_get_tag_type(acl_entry_t entry, acl_tag_t *tag, int *error_number) {
	errno = 0;
	int result = acl_get_tag_type(entry, tag);
	*error_number = errno;
	return result;
}

static int harflex_acl_free(acl_t acl, int *error_number) {
	errno = 0;
	int result = acl_free((void *)acl);
	*error_number = errno;
	return result;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

func requirePrivateCopyPlatformSupport() error { return nil }

func verifyPrivateParent(directory *os.File) error {
	if err := verifyDarwinDirectoryPolicy(directory, "private parent", true); err != nil {
		return err
	}
	return verifyPrivateParentCreateAccess(directory)
}

func verifyPrivateParentCreateAccess(directory *os.File) error {
	if directory == nil {
		return fmt.Errorf("%w: private parent descriptor is nil", ErrPrivatePermissionsUnavailable)
	}
	connection, err := directory.SyscallConn()
	if err != nil {
		return privatePermissionError("access private-parent descriptor", err)
	}
	var accessErr error
	if err := connection.Control(func(fd uintptr) {
		accessErr = unix.Faccessat(int(fd), ".", unix.W_OK|unix.X_OK, unix.AT_EACCESS)
	}); err != nil {
		return privatePermissionError("check effective private-parent permissions", err)
	}
	runtime.KeepAlive(directory)
	if accessErr != nil {
		return privatePermissionError("check effective private-parent permissions", accessErr)
	}
	return nil
}

func verifyPrivatePathAncestors(parentPath string) error {
	directory, err := os.Open(parentPath)
	if err != nil {
		return privatePermissionError("open private-parent ancestor", err)
	}
	defer directory.Close()
	return verifyDarwinDirectoryAncestry(directory)
}

func verifyPrivateRootAncestors(parentRoot *os.Root) error {
	directory, err := parentRoot.Open(".")
	if err != nil {
		return privatePermissionError("open private-parent handle", err)
	}
	defer directory.Close()
	return verifyDarwinDirectoryAncestry(directory)
}

func verifyDarwinDirectoryAncestry(directory *os.File) error {
	current := directory
	closeCurrent := false
	defer func() {
		if closeCurrent {
			_ = current.Close()
		}
	}()

	for depth := 0; depth <= maxPathBytes; depth++ {
		description := fmt.Sprintf("private-parent ancestor %d", depth)
		if err := verifyDarwinDirectoryPolicy(current, description, depth == 0); err != nil {
			return err
		}
		currentInfo, err := current.Stat()
		if err != nil {
			return privatePermissionError("stat private-parent ancestor", err)
		}
		connection, err := current.SyscallConn()
		if err != nil {
			return privatePermissionError("access private-parent ancestor descriptor", err)
		}
		parentFD := -1
		var openErr error
		if err := connection.Control(func(fd uintptr) {
			parentFD, openErr = unix.Openat(int(fd), "..", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		}); err != nil {
			return privatePermissionError("open private-parent ancestor descriptor", err)
		}
		if openErr != nil {
			return privatePermissionError("open private-parent ancestor", openErr)
		}
		parent := os.NewFile(uintptr(parentFD), "private-parent/..")
		if parent == nil {
			_ = unix.Close(parentFD)
			return fmt.Errorf("%w: invalid private-parent ancestor descriptor", ErrPrivatePermissionsUnavailable)
		}
		parentInfo, err := parent.Stat()
		if err != nil {
			_ = parent.Close()
			return privatePermissionError("stat private-parent ancestor descriptor", err)
		}
		if os.SameFile(currentInfo, parentInfo) {
			_ = parent.Close()
			return nil
		}
		if closeCurrent {
			if err := current.Close(); err != nil {
				_ = parent.Close()
				return privatePermissionError("close private-parent ancestor descriptor", err)
			}
		}
		current = parent
		closeCurrent = true
	}
	return fmt.Errorf("%w: private-parent ancestry exceeds %d components", ErrPrivatePermissionsUnavailable, maxPathBytes)
}

func verifyDarwinDirectoryPolicy(directory *os.File, description string, requireCurrentOwner bool) error {
	info, err := directory.Stat()
	if err != nil {
		return privatePermissionError("stat "+description, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrPrivatePermissionsUnavailable, description)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: %s owner identity has unsupported type %T", ErrPrivatePermissionsUnavailable, description, info.Sys())
	}
	sticky := info.Mode()&os.ModeSticky != 0
	if info.Mode().Perm()&0o022 != 0 && !sticky {
		return fmt.Errorf("%w: %s mode=%04o permits other-user replacement", ErrPrivatePermissionsUnavailable, description, info.Mode().Perm())
	}
	if sticky && stat.Uid != uint32(os.Geteuid()) && stat.Uid != 0 {
		return fmt.Errorf("%w: sticky %s is owned by untrusted UID %d", ErrPrivatePermissionsUnavailable, description, stat.Uid)
	}
	if requireCurrentOwner && !sticky && stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: %s owner UID %d differs from effective UID %d", ErrPrivatePermissionsUnavailable, description, stat.Uid, os.Geteuid())
	}
	if err := verifyDarwinExtendedACL(directory); err != nil {
		return fmt.Errorf("verify %s ACL: %w", description, err)
	}
	return nil
}

func securePrivateRoot(directory *os.File) error {
	if err := directory.Chmod(0o700); err != nil {
		return privatePermissionError("chmod private staging root", err)
	}
	info, err := directory.Stat()
	if err != nil {
		return privatePermissionError("stat private staging root", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%w: staging mode=%04o is not owner-only 0700", ErrPrivatePermissionsUnavailable, info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: staging owner identity has unsupported type %T", ErrPrivatePermissionsUnavailable, info.Sys())
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: staging owner UID %d differs from effective UID %d", ErrPrivatePermissionsUnavailable, stat.Uid, os.Geteuid())
	}
	if err := verifyDarwinExtendedACL(directory); err != nil {
		return fmt.Errorf("verify private staging ACL: %w", err)
	}
	return nil
}

func verifyDarwinExtendedACL(file *os.File) (result error) {
	if file == nil {
		return fmt.Errorf("%w: ACL descriptor is nil", ErrPrivatePermissionsUnavailable)
	}
	connection, err := file.SyscallConn()
	if err != nil {
		return privatePermissionError("access ACL descriptor", err)
	}
	var aclErr error
	if err := connection.Control(func(fd uintptr) {
		aclErr = verifyDarwinExtendedACLFD(int(fd))
	}); err != nil {
		return privatePermissionError("inspect ACL descriptor", err)
	}
	runtime.KeepAlive(file)
	return aclErr
}

func verifyDarwinExtendedACLFD(fd int) (result error) {
	var descriptorStat unix.Stat_t
	if err := unix.Fstat(fd, &descriptorStat); err != nil {
		return privatePermissionError("stat ACL descriptor", err)
	}
	if descriptorStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("%w: ACL descriptor is not a directory", ErrPrivatePermissionsUnavailable)
	}
	var errorNumber C.int
	acl := C.harflex_acl_get_fd(C.int(fd), &errorNumber)
	if acl == nil {
		// ACL_TYPE_EXTENDED is absent on otherwise valid filesystem objects as
		// ENOENT; unsupported ACL retrieval uses a different errno (for example,
		// EOPNOTSUPP) and remains fail-closed.
		if errorNumber == C.ENOENT {
			return nil
		}
		return darwinACLError("acl_get_fd_np", errorNumber)
	}
	defer func() {
		var freeError C.int
		if C.harflex_acl_free(acl, &freeError) != 0 {
			result = errors.Join(result, darwinACLError("acl_free", freeError))
		}
	}()
	var validationErrorNumber C.int
	// acl_get_fd_np returned an independent copy; acl_valid may reorder only this copy.
	validationStatus := C.harflex_acl_valid(acl, &validationErrorNumber)
	if err := darwinACLValidationResult(int(validationStatus), int(validationErrorNumber)); err != nil {
		return err
	}

	entryID := C.int(C.ACL_FIRST_ENTRY)
	classifiedEntries := 0
	for count := 0; count < 1024; count++ {
		var entry C.acl_entry_t
		errorNumber = 0
		status := C.harflex_acl_get_entry(acl, entryID, &entry, &errorNumber)
		finished, entryErr := darwinACLIterationResult(int(status), int(errorNumber), entryID == C.ACL_NEXT_ENTRY, classifiedEntries > 0, true)
		if entryErr != nil {
			return entryErr
		}
		if finished {
			return nil
		}
		if entry == nil {
			return fmt.Errorf("%w: acl_get_entry returned an unclassifiable result", ErrPrivatePermissionsUnavailable)
		}
		var tag C.acl_tag_t
		errorNumber = 0
		if C.harflex_acl_get_tag_type(entry, &tag, &errorNumber) != 0 {
			return darwinACLError("acl_get_tag_type", errorNumber)
		}
		switch tag {
		case C.ACL_EXTENDED_ALLOW:
			return fmt.Errorf("%w: directory ACL grants access through an allow ACE", ErrPrivatePermissionsUnavailable)
		case C.ACL_EXTENDED_DENY:
			// Deny entries can only reduce the effective permissions.
		default:
			return fmt.Errorf("%w: directory ACL contains an unclassifiable tag %d", ErrPrivatePermissionsUnavailable, int(tag))
		}
		classifiedEntries++
		entryID = C.int(C.ACL_NEXT_ENTRY)
	}
	return fmt.Errorf("%w: ACL has more entries than can be safely classified", ErrPrivatePermissionsUnavailable)
}

func darwinACLIterationResult(status, errorNumber int, isNextEntry, hasClassifiedACE, aclValidated bool) (bool, error) {
	if status == -1 {
		if errorNumber == int(C.EINVAL) && isNextEntry && hasClassifiedACE && aclValidated {
			return true, nil
		}
		return false, darwinACLError("acl_get_entry", C.int(errorNumber))
	}
	if status != 0 {
		return false, fmt.Errorf("%w: acl_get_entry returned an unclassifiable result", ErrPrivatePermissionsUnavailable)
	}
	return false, nil
}

func darwinACLValidationResult(status, errorNumber int) error {
	if status == 0 {
		return nil
	}
	return darwinACLError("acl_valid", C.int(errorNumber))
}

func darwinACLError(operation string, errorNumber C.int) error {
	if errorNumber == 0 {
		return fmt.Errorf("%w: %s failed without errno", ErrPrivatePermissionsUnavailable, operation)
	}
	return fmt.Errorf("%w: %s: %v", ErrPrivatePermissionsUnavailable, operation, syscall.Errno(errorNumber))
}

func privatePermissionError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrPrivatePermissionsUnavailable, operation, err)
}
