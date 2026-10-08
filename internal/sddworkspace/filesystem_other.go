//go:build !linux && !darwin

package sddworkspace

import "os"

func fileFilesystemIdentity(*os.File) (string, error) {
	return "", ErrFilesystemBoundaryUnprovable
}
