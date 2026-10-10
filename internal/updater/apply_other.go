//go:build !darwin && !linux && !windows

package updater

import "context"

// StagingDir has no place to download to: this platform cannot update itself.
func StagingDir() (string, error) { return "", ErrNotInstalled }

// Apply cannot replace the app on this platform.
func Apply(context.Context, string) error { return ErrNotInstalled }

// CanApply says this platform cannot update itself.
func CanApply() error { return ErrNotInstalled }
