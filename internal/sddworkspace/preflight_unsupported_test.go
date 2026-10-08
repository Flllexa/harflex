//go:build android || ios || windows || (darwin && !cgo)

package sddworkspace

import (
	"context"
	"errors"
	"testing"
)

func TestPreflightPrivateCopyFailsClosedWhenPlatformSupportIsUnavailable(t *testing.T) {
	_, err := PreflightPrivateCopy(context.Background(), t.TempDir(), t.TempDir())
	if !errors.Is(err, ErrPrivatePermissionsUnavailable) {
		t.Fatalf("PreflightPrivateCopy() error = %v; want ErrPrivatePermissionsUnavailable", err)
	}
}
