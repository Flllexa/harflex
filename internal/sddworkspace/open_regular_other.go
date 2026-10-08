//go:build !linux && !darwin

package sddworkspace

import "os"

func openRegularFile(*os.Root, string) (*os.File, error) {
	return nil, ErrFilesystemBoundaryUnprovable
}
