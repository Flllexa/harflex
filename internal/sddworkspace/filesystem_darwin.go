//go:build darwin

package sddworkspace

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

func fileFilesystemIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("read device identity: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ErrFilesystemBoundaryUnprovable
	}
	return "dev:" + strconv.FormatUint(uint64(stat.Dev), 10), nil
}
