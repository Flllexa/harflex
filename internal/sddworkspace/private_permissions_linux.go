//go:build linux && !android

package sddworkspace

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

func requirePrivateCopyPlatformSupport() error { return nil }

func verifyPrivateParent(directory *os.File) error {
	info, err := directory.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: private parent is not a directory", ErrPrivatePermissionsUnavailable)
	}
	if err := verifyDirectoryMutationPolicy(info, "private parent"); err != nil {
		return err
	}
	if info.Mode()&os.ModeSticky != 0 {
		return verifyPrivateParentCreateAccess(directory)
	}
	if err := verifyCurrentOwner(info); err != nil {
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
		return fmt.Errorf("%w: access private parent descriptor: %v", ErrPrivatePermissionsUnavailable, err)
	}
	var accessErr error
	if err := connection.Control(func(fd uintptr) {
		accessErr = unix.Faccessat2(int(fd), ".", unix.W_OK|unix.X_OK, unix.AT_EACCESS)
	}); err != nil {
		return fmt.Errorf("%w: check effective private-parent permissions: %v", ErrPrivatePermissionsUnavailable, err)
	}
	runtime.KeepAlive(directory)
	if accessErr != nil {
		return fmt.Errorf("%w: check effective private-parent permissions: %v", ErrPrivatePermissionsUnavailable, accessErr)
	}
	return nil
}

func verifyPrivatePathAncestors(parentPath string) error {
	current := filepath.Clean(parentPath)
	for depth := 0; depth <= maxPathBytes; depth++ {
		info, err := os.Stat(current)
		if err != nil {
			return fmt.Errorf("%w: stat private-parent ancestor %q: %v", ErrPrivatePermissionsUnavailable, current, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: private-parent ancestor %q is not a directory", ErrPrivatePermissionsUnavailable, current)
		}
		if err := verifyDirectoryMutationPolicy(info, current); err != nil {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
	return fmt.Errorf("%w: private-parent ancestry exceeds %d components", ErrPrivatePermissionsUnavailable, maxPathBytes)
}

func verifyPrivateRootAncestors(parentRoot *os.Root) error {
	return verifyPrivatePathAncestors(parentRoot.Name())
}

func verifyDirectoryMutationPolicy(info os.FileInfo, description string) error {
	sticky := info.Mode()&os.ModeSticky != 0
	if info.Mode().Perm()&0o022 != 0 && !sticky {
		return fmt.Errorf("%w: %s mode=%04o permits other-user replacement", ErrPrivatePermissionsUnavailable, description, info.Mode().Perm())
	}
	if sticky {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("%w: %s owner identity has unsupported type %T", ErrPrivatePermissionsUnavailable, description, info.Sys())
		}
		if stat.Uid != uint32(os.Geteuid()) && stat.Uid != 0 {
			return fmt.Errorf("%w: sticky %s is owned by untrusted UID %d", ErrPrivatePermissionsUnavailable, description, stat.Uid)
		}
	}
	return nil
}

func securePrivateRoot(directory *os.File) error {
	if err := directory.Chmod(0o700); err != nil {
		return err
	}
	info, err := directory.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: staging mode=%04o is not owner-only", ErrPrivatePermissionsUnavailable, info.Mode().Perm())
	}
	return verifyCurrentOwner(info)
}

func verifyCurrentOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: filesystem owner identity has unsupported type %T", ErrPrivatePermissionsUnavailable, info.Sys())
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: directory owner UID %d differs from effective UID %d", ErrPrivatePermissionsUnavailable, stat.Uid, os.Geteuid())
	}
	return nil
}
