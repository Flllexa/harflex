package sddworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// PreflightPrivateCopy inspects a source tree and verifies that privateParent
// can contain a future private copy. It performs no filesystem mutations and
// does not reserve available disk space or quota.
func PreflightPrivateCopy(ctx context.Context, sourceRoot, privateParent string) (Manifest, error) {
	var empty Manifest
	if ctx == nil {
		return empty, errors.New("context is required")
	}
	source, err := canonicalDirectory(sourceRoot)
	if err != nil {
		return empty, fmt.Errorf("open source root: %w", err)
	}
	parent, err := canonicalDirectory(privateParent)
	if err != nil {
		return empty, fmt.Errorf("open private parent: %w", err)
	}
	parentWithinSource, err := isWithinFilesystem(source, parent)
	if err != nil {
		return empty, fmt.Errorf("prove private parent ancestry: %w", err)
	}
	if parentWithinSource {
		return empty, errors.New("private parent must not be inside the source root")
	}
	if err := requirePrivateCopyPlatformSupport(); err != nil {
		return empty, err
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return empty, fmt.Errorf("open private parent handle: %w", err)
	}
	defer parentRoot.Close()
	parentFSID, err := rootFilesystemIdentity(parentRoot)
	if err != nil {
		return empty, fmt.Errorf("establish private parent filesystem identity: %w", err)
	}
	if err := verifyReadOnlyPrivateParent(source, parentRoot, parentFSID); err != nil {
		return empty, err
	}
	manifest, err := Scan(ctx, source)
	if err != nil {
		return empty, fmt.Errorf("scan private-copy source: %w", err)
	}
	if err := verifyReadOnlyPrivateParent(source, parentRoot, parentFSID); err != nil {
		return empty, fmt.Errorf("recheck private parent after source scan: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return empty, fmt.Errorf("preflight cancelled: %w", err)
	}
	return manifest, nil
}

func verifyReadOnlyPrivateParent(source string, parentRoot *os.Root, parentFSID string) error {
	if err := verifyPrivateParentOutsideSource(source, parentRoot, parentFSID); err != nil {
		return fmt.Errorf("verify private parent containment: %w", err)
	}
	if err := verifyPrivateRootAncestors(parentRoot); err != nil {
		return fmt.Errorf("verify private-parent ancestors: %w", err)
	}
	directory, err := parentRoot.Open(".")
	if err != nil {
		return fmt.Errorf("open private parent directory handle: %w", err)
	}
	permissionErr := verifyPrivateParent(directory)
	closeErr := directory.Close()
	if permissionErr != nil {
		return fmt.Errorf("verify private parent permissions: %w", permissionErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close private parent directory handle: %w", closeErr)
	}
	return nil
}
