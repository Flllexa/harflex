//go:build darwin || linux

package sddworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScanDetectsSymlinkDriftBeforeOpeningExcludedCredential(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mode-000 credential fixture is readable by root")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".env"), []byte("credential"), 0o600)
	if err := os.Chmod(filepath.Join(root, ".env"), 0); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	writeFile(t, target, []byte("safe"), 0o600)
	if err := os.Symlink("target", filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := scanWithLimitsAndHooks(context.Background(), root, defaultScanLimits, scanHooks{
		afterWalk: func(*os.Root) error {
			if removeErr := os.Remove(target); removeErr != nil {
				return removeErr
			}
			return os.Symlink(".env", target)
		},
	})
	if !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("scan error = %v, want ErrSourceDrift without opening .env", err)
	}
}

func TestScanRejectsNewExcludedSymlinkTargetAfterWalkWithoutOpeningIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mode-000 credential fixture is readable by root")
	}
	root := t.TempDir()
	if err := os.Symlink(".env", filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := scanWithLimitsAndHooks(context.Background(), root, defaultScanLimits, scanHooks{
		afterWalk: func(*os.Root) error {
			if err := os.WriteFile(filepath.Join(root, ".env"), []byte("credential"), 0o600); err != nil {
				return err
			}
			return os.Chmod(filepath.Join(root, ".env"), 0)
		},
	})
	if !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("scan error = %v, want ErrSourceDrift for new excluded target", err)
	}
}
