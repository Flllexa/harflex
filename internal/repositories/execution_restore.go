package repositories

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/procattr"
)

// RestoreExecutionSnapshot rebuilds a private copy that vanished, for instance when the system cleared a cache to
// free disk: the project's files as they were when the copy was taken, each checked against the manifest, then the
// change the copy held, with the modes it recorded. The caller compares the rebuilt copy with the saved change.
func RestoreExecutionSnapshot(ctx context.Context, snapshot ExecutionSnapshot, patch string, modes map[string]os.FileMode) (err error) {
	if snapshot.Root == "" || snapshot.SourceRoot == "" || !filepath.IsAbs(snapshot.Root) {
		return ErrExecutionSnapshotUnsafePath
	}
	if _, err := os.Lstat(snapshot.Root); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if canonical, err := filepath.EvalSymlinks(snapshot.SourceRoot); err != nil || canonical != snapshot.SourceRoot {
		return ErrExecutionSourceDrift
	}
	if err := os.MkdirAll(filepath.Dir(snapshot.Root), 0o700); err != nil {
		return fmt.Errorf("create execution copy parent: %w", err)
	}
	if err := os.Mkdir(snapshot.Root, 0o700); err != nil {
		return fmt.Errorf("create execution copy: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(snapshot.Root)
		}
	}()
	if err := os.Chmod(snapshot.Root, 0o700); err != nil {
		return err
	}
	for _, want := range snapshot.Manifest {
		if err := ctx.Err(); err != nil {
			return err
		}
		location := snapshot.locate(want.Path)
		got, _, copyErr := copyExecutionPath(location.root, location.inner, snapshot.Root, want.Path, maxExecutionSnapshotBytes)
		if errors.Is(copyErr, os.ErrNotExist) || (copyErr == nil && (got.Kind != want.Kind || got.Hash != want.Hash || got.Mode != want.Mode)) {
			return ErrExecutionSourceDrift
		}
		if copyErr != nil {
			return copyErr
		}
	}
	if strings.Contains(patch, " mode 120000\n") {
		// Links are written in a form git does not apply; such a change is not rebuilt.
		return ErrExecutionSourceDrift
	}
	if strings.TrimSpace(patch) != "" {
		if err := applyExecutionPatch(ctx, snapshot.Root, patch); err != nil {
			return err
		}
	}
	for path, mode := range modes {
		target := filepath.Join(snapshot.Root, filepath.FromSlash(path))
		if !safeExecutionRelativePath(filepath.FromSlash(path)) || !pathWithin(snapshot.Root, target) {
			return ErrExecutionSnapshotUnsafePath
		}
		if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink == 0 {
			if err := os.Chmod(target, mode.Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyExecutionPatch applies the change with git as a plain patch tool: the copy has no repository of its own, and
// none above it is looked up.
func applyExecutionPatch(ctx context.Context, root, patch string) error {
	file, err := os.CreateTemp("", "harflex-restore-*.patch")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(patch); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(callCtx, "git", "apply", "--whitespace=nowarn", file.Name())
	procattr.Hide(cmd)
	cmd.Dir = root
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(root))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: apply the saved change: %s", ErrExecutionSourceDrift, strings.TrimSpace(string(output)))
	}
	return nil
}
