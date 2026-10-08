package repositories

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var ErrGitUnavailable = errors.New("git unavailable")
var ErrGitBaselineTooLarge = errors.New("untracked Git baseline exceeds the safety limit")

type Snapshot struct {
	IsRepository bool     `json:"isRepository"`
	Root         string   `json:"root"`
	Branch       string   `json:"branch"`
	Files        []string `json:"files"`
	StagedDiff   string   `json:"stagedDiff"`
	UnstagedDiff string   `json:"unstagedDiff"`
	Truncated    bool     `json:"truncated"`
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	n := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if remaining < n {
			_, _ = b.buffer.Write(data[:remaining])
		} else {
			_, _ = b.buffer.Write(data)
		}
	}
	if n > remaining {
		b.truncated = true
	}
	return n, nil
}

func (b *boundedBuffer) String() string { return b.buffer.String() }
func (b *boundedBuffer) Len() int       { return b.buffer.Len() }

func gitOutput(ctx context.Context, dir string, limit int, args ...string) (string, bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(callCtx, "git", args...)
	cmd.Dir = dir
	cmd.Env = make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat")
	output := &boundedBuffer{limit: limit}
	cmd.Stdout = output
	cmd.Stderr = &boundedBuffer{limit: 4096}
	if err := cmd.Run(); err != nil {
		return "", false, fmt.Errorf("git %s: %w", args[0], err)
	}
	return output.String(), output.truncated, nil
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func parseStatus(raw string) []string {
	parts := strings.Split(raw, "\x00")
	files := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if len(entry) < 4 {
			continue
		}
		files = append(files, entry[:2]+" "+entry[3:])
		if entry[0] == 'R' || entry[1] == 'R' || entry[0] == 'C' || entry[1] == 'C' {
			i++ // porcelain -z puts the source path in the next field.
		}
	}
	return files
}

// StatusPath returns the path portion of a porcelain-v1 status entry.
func StatusPath(entry string) string {
	if len(entry) < 4 {
		return ""
	}
	return entry[3:]
}

func IsUntrackedStatus(entry string) bool { return strings.HasPrefix(entry, "?? ") }

// HashUntrackedPath records the content of one untracked file or directory
// without following symlinks outside the selected repository.
func HashUntrackedPath(ctx context.Context, root, relative string, maxBytes int64) (string, int64, error) {
	if maxBytes < 0 {
		return "", 0, errors.New("invalid untracked Git baseline limit")
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", 0, fmt.Errorf("resolve repository root: %w", err)
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", 0, errors.New("untracked path escaped repository root")
	}
	hasher := sha256.New()
	totalBytes := int64(0)
	entries := 0
	var visit func(string, string) error
	visit = func(path, rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > 10000 {
			return ErrGitBaselineTooLarge
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if totalBytes+int64(len(target)) > maxBytes {
				return ErrGitBaselineTooLarge
			}
			totalBytes += int64(len(target))
			_, _ = fmt.Fprintf(hasher, "link %s %s\n", filepath.ToSlash(rel), target)
		case info.IsDir():
			_, _ = fmt.Fprintf(hasher, "dir %s\n", filepath.ToSlash(rel))
			children, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			for _, child := range children {
				childPath := filepath.Join(path, child.Name())
				childRel := filepath.Join(rel, child.Name())
				if err := visit(childPath, childRel); err != nil {
					return err
				}
			}
		case info.Mode().IsRegular():
			_, _ = fmt.Fprintf(hasher, "file %s %o\n", filepath.ToSlash(rel), info.Mode().Perm())
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			remaining := maxBytes - totalBytes
			copied, copyErr := io.Copy(hasher, io.LimitReader(file, remaining+1))
			closeErr := file.Close()
			totalBytes += copied
			if totalBytes > maxBytes {
				return ErrGitBaselineTooLarge
			}
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return errors.New("unsupported untracked filesystem entry")
		}
		return nil
	}
	if err := visit(filepath.Join(canonicalRoot, clean), clean); err != nil {
		return "", totalBytes, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), totalBytes, nil
}

// DiffForFiles reads only Git changes for the supplied repository-relative paths.
func DiffForFiles(ctx context.Context, path string, files []string, maxBytes int) (string, bool, error) {
	if len(files) == 0 {
		return "", false, nil
	}
	if maxBytes <= 0 || maxBytes >= int(^uint(0)>>1) {
		return "", false, errors.New("invalid Git diff limit")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false, fmt.Errorf("resolve workspace: %w", err)
	}
	root, _, err := gitOutput(ctx, canonical, 4096, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false, err
	}
	if !samePath(strings.TrimSpace(root), canonical) {
		return "", false, errors.New("workspace is not the Git repository root")
	}
	stagedArgs := append([]string{"diff", "--cached", "--no-ext-diff", "--no-textconv", "--no-color", "--"}, files...)
	staged, stagedTruncated, err := gitOutput(ctx, canonical, maxBytes+1, stagedArgs...)
	if err != nil {
		return "", false, err
	}
	unstagedArgs := append([]string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--"}, files...)
	unstaged, unstagedTruncated, err := gitOutput(ctx, canonical, maxBytes+1, unstagedArgs...)
	if err != nil {
		return "", false, err
	}
	diff := staged + unstaged
	if stagedTruncated || unstagedTruncated || len(diff) > maxBytes {
		return "", true, nil
	}
	return diff, false, nil
}

// Inspect reads only the selected directory. A repository rooted above it is
// outside the user's authorized workspace and is not inspected.
func Inspect(ctx context.Context, path string) (Snapshot, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return Snapshot{}, ErrGitUnavailable
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve workspace: %w", err)
	}
	root, _, err := gitOutput(ctx, canonical, 4096, "rev-parse", "--show-toplevel")
	if err != nil {
		if ctx.Err() != nil {
			return Snapshot{}, ctx.Err()
		}
		return Snapshot{IsRepository: false}, nil
	}
	root = strings.TrimSpace(root)
	if !samePath(root, canonical) {
		return Snapshot{IsRepository: false}, nil
	}
	branch, _, err := gitOutput(ctx, canonical, 4096, "branch", "--show-current")
	if err != nil {
		return Snapshot{}, err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "HEAD destacado"
	}
	status, statusTruncated, err := gitOutput(ctx, canonical, 256*1024, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return Snapshot{}, err
	}
	staged, stagedTruncated, err := gitOutput(ctx, canonical, 256*1024, "diff", "--cached", "--no-ext-diff", "--no-textconv", "--no-color", "--", ".")
	if err != nil {
		return Snapshot{}, err
	}
	unstaged, unstagedTruncated, err := gitOutput(ctx, canonical, 256*1024, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--", ".")
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{IsRepository: true, Root: canonical, Branch: branch, Files: parseStatus(status), StagedDiff: staged, UnstagedDiff: unstaged, Truncated: statusTruncated || stagedTruncated || unstagedTruncated}, nil
}
