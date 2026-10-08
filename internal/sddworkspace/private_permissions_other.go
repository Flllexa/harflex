//go:build android || ios || (!linux && !darwin)

package sddworkspace

import (
	"os"
)

func requirePrivateCopyPlatformSupport() error { return ErrPrivatePermissionsUnavailable }

func verifyPrivatePathAncestors(string) error { return ErrPrivatePermissionsUnavailable }

func verifyPrivateRootAncestors(*os.Root) error { return ErrPrivatePermissionsUnavailable }

func verifyPrivateParent(*os.File) error { return ErrPrivatePermissionsUnavailable }

func securePrivateRoot(*os.File) error { return ErrPrivatePermissionsUnavailable }
