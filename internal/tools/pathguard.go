package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PathGuard validates paths at resolution time. Callers must separately protect
// filesystem operations against concurrent symlink changes.
type PathGuard struct {
	root string
}

func NewPathGuard(root string) (*PathGuard, error) {
	if root == "" {
		return nil, fmt.Errorf("create path guard: root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root %q: %w", root, err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("canonicalize root %q: %w", root, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("stat root %q: %w", canonical, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("create path guard: root %q is not a directory", canonical)
	}
	return &PathGuard{root: canonical}, nil
}

func (g *PathGuard) Resolve(input string, allowMissingLeaf bool) (string, error) {
	path := input
	if !filepath.IsAbs(path) {
		path = filepath.Join(g.root, path)
	}
	path = filepath.Clean(path)
	canonical, err := canonicalPath(path, allowMissingLeaf)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", input, err)
	}
	rel, err := filepath.Rel(g.root, canonical)
	if err != nil {
		return "", fmt.Errorf("check path %q against root %q: %w", canonical, g.root, err)
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", classified(errOutsideWorkspace, "path %q is outside workspace root %q", canonical, g.root)
	}
	return canonical, nil
}

func canonicalPath(path string, allowMissingLeaf bool) (string, error) {
	ancestor := path
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !allowMissingLeaf || !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect path %q: %w", ancestor, err)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("find existing ancestor of %q: %w", path, err)
		}
		ancestor = parent
	}
	// Lstat stops on dangling symlinks so EvalSymlinks rejects them instead of
	// treating their names as new directories.
	canonical, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", fmt.Errorf("canonicalize path %q: %w", ancestor, err)
	}
	if ancestor == path {
		return canonical, nil
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("stat ancestor %q: %w", canonical, err)
	}
	if !info.IsDir() {
		return "", classified(errNotDirectory, "ancestor %q is not a directory", canonical)
	}
	suffix, err := filepath.Rel(ancestor, path)
	if err != nil {
		return "", fmt.Errorf("resolve suffix of %q: %w", path, err)
	}
	return filepath.Join(canonical, suffix), nil
}
