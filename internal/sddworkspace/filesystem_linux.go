//go:build linux

package sddworkspace

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func fileFilesystemIdentity(file *os.File) (string, error) {
	connection, err := file.SyscallConn()
	if err != nil {
		return "", fmt.Errorf("access filesystem identity: %w", err)
	}
	var identity string
	var statErr error
	if err := connection.Control(func(fd uintptr) {
		var stat unix.Statx_t
		statErr = unix.Statx(int(fd), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &stat)
		if statErr == nil && stat.Mask&unix.STATX_MNT_ID != 0 {
			identity = fmt.Sprintf("mnt:%d", stat.Mnt_id)
		}
	}); err != nil {
		return "", fmt.Errorf("read mount identity: %w", err)
	}
	if statErr != nil {
		return "", fmt.Errorf("read mount identity: %w", statErr)
	}
	if identity == "" {
		return "", ErrFilesystemBoundaryUnprovable
	}
	return identity, nil
}
