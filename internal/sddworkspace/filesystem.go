package sddworkspace

import (
	"fmt"
	"os"
	"path/filepath"
)

func rootFilesystemIdentity(root *os.Root) (string, error) {
	file, err := root.Open(".")
	if err != nil {
		return "", fmt.Errorf("open filesystem root: %w", err)
	}
	defer file.Close()
	return fileFilesystemIdentity(file)
}

func verifyRootPathBinding(root *os.Root, expectedFSID string) error {
	opened, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("verify root binding: %w", err)
	}
	openedInfo, statErr := opened.Stat()
	closeErr := opened.Close()
	if statErr != nil {
		return fmt.Errorf("verify root binding: %w", statErr)
	}
	if closeErr != nil {
		return fmt.Errorf("verify root binding: %w", closeErr)
	}
	pathInfo, err := os.Stat(root.Name())
	if err != nil {
		return fmt.Errorf("%w: root path %q is no longer available: %v", ErrSourceDrift, root.Name(), err)
	}
	if !os.SameFile(openedInfo, pathInfo) {
		return fmt.Errorf("%w: root path %q no longer identifies the opened directory", ErrSourceDrift, root.Name())
	}
	current, err := os.OpenRoot(root.Name())
	if err != nil {
		return fmt.Errorf("%w: reopen root path %q: %v", ErrSourceDrift, root.Name(), err)
	}
	defer current.Close()
	currentFSID, err := rootFilesystemIdentity(current)
	if err != nil {
		return fmt.Errorf("%w: verify root filesystem identity: %v", ErrSourceDrift, err)
	}
	if err := ensureSameFilesystem(expectedFSID, currentFSID, "."); err != nil {
		return fmt.Errorf("%w: root binding changed: %v", ErrSourceDrift, err)
	}
	return nil
}

func ensureSameFilesystem(rootID, entryID, relativePath string) error {
	if rootID == "" || entryID == "" {
		return fmt.Errorf("%w: empty filesystem identity at %q", ErrFilesystemBoundaryUnprovable, relativePath)
	}
	if rootID != entryID {
		return fmt.Errorf("%w: %q", ErrFilesystemBoundary, relativePath)
	}
	return nil
}

func isWithinFilesystem(rootPath, candidatePath string) (bool, error) {
	rootPath, err := filepath.Abs(rootPath)
	if err != nil {
		return false, fmt.Errorf("resolve source root: %w", err)
	}
	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return false, fmt.Errorf("resolve source root identity: %w", err)
	}
	candidatePath, err = filepath.Abs(candidatePath)
	if err != nil {
		return false, fmt.Errorf("resolve private parent: %w", err)
	}
	candidatePath, err = filepath.EvalSymlinks(candidatePath)
	if err != nil {
		return false, fmt.Errorf("resolve private parent identity: %w", err)
	}
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		return false, fmt.Errorf("stat source root: %w", err)
	}
	if !rootInfo.IsDir() {
		return false, fmt.Errorf("source root %q is not a directory", rootPath)
	}
	current := filepath.Clean(candidatePath)
	for depth := 0; depth <= maxPathBytes; depth++ {
		info, err := os.Stat(current)
		if err != nil {
			return false, fmt.Errorf("stat private-parent ancestor %q: %w", current, err)
		}
		if os.SameFile(rootInfo, info) {
			return true, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
	return false, fmt.Errorf("could not prove directory ancestry within %d components", maxPathBytes)
}

func verifyPrivateParentOutsideSource(sourcePath string, parentRoot *os.Root, parentFSID string) error {
	if err := verifyRootPathBinding(parentRoot, parentFSID); err != nil {
		return fmt.Errorf("verify private parent binding: %w", err)
	}
	withinSource, err := isWithinFilesystem(sourcePath, parentRoot.Name())
	if err != nil {
		return fmt.Errorf("prove opened private parent ancestry: %w", err)
	}
	if withinSource {
		return fmt.Errorf("private parent must not be inside the source root")
	}
	return nil
}
