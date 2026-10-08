//go:build linux || darwin

package sddworkspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openRegularFile(root *os.Root, relativePath string) (*os.File, error) {
	nativePath := filepath.FromSlash(relativePath)
	parentPath := filepath.Dir(nativePath)
	baseName := filepath.Base(nativePath)
	parentRoot := root
	closeParentRoot := false
	if parentPath != "." {
		openedRoot, err := root.OpenRoot(parentPath)
		if err != nil {
			return nil, err
		}
		parentRoot = openedRoot
		closeParentRoot = true
	}
	if closeParentRoot {
		defer parentRoot.Close()
	}
	directory, err := parentRoot.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	connection, err := directory.SyscallConn()
	if err != nil {
		return nil, err
	}
	fd := -1
	var openErr error
	if err := connection.Control(func(directoryFD uintptr) {
		fd, openErr = unix.Openat(int(directoryFD), baseName, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	}); err != nil {
		return nil, err
	}
	if openErr != nil {
		return nil, openErr
	}
	file := os.NewFile(uintptr(fd), relativePath)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("openat returned an invalid file descriptor")
	}
	return verifyRegularFile(file, relativePath)
}

func verifyRegularFile(file *os.File, relativePath string) (*os.File, error) {
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("%w: %q is not a regular file", ErrSpecialFile, relativePath)
	}
	return file, nil
}
