package tools

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/security"
)

type mutationLockEntry struct {
	refs  int
	token chan struct{}
}
type mutationLockManager struct {
	mu      sync.Mutex
	entries map[string]*mutationLockEntry
}

var mutationLocks mutationLockManager

// Locks conservatively serialize every mutation within one workspace in this
// process, including casing, hardlink and symlink aliases. Different workspaces
// remain independent. External processes still require filesystem cooperation;
// the final identity check is not a CAS.
func (m *mutationLockManager) acquire(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.entries == nil {
		m.entries = make(map[string]*mutationLockEntry)
	}
	entry := m.entries[key]
	if entry == nil {
		entry = &mutationLockEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		m.entries[key] = entry
	}
	entry.refs++
	m.mu.Unlock()
	forget := func(held bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if held {
			entry.token <- struct{}{}
		}
		entry.refs--
		if entry.refs == 0 {
			delete(m.entries, key)
		}
	}
	select {
	case <-ctx.Done():
		forget(false)
		return nil, ctx.Err()
	case <-entry.token:
		if err := ctx.Err(); err != nil {
			forget(true)
			return nil, err
		}
		var once sync.Once
		return func() { once.Do(func() { forget(true) }) }, nil
	}
}

func openMutationDirectory(part string, expected os.FileInfo, open func(string) (*os.Root, error)) (*os.Root, error) {
	child, err := open(part)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil {
		child.Close()
		return nil, fmt.Errorf("inspect opened parent %q: %w", part, err)
	}
	if !opened.IsDir() || !os.SameFile(expected, opened) {
		child.Close()
		return nil, fmt.Errorf("parent %q: identity changed while opening", part)
	}
	return child, nil
}

func lockMutation(ctx context.Context, guard *PathGuard, input string, create bool) (func(), error) {
	canonical, err := guard.Resolve(input, create)
	if err != nil {
		return nil, err
	}
	unlock, err := mutationLocks.acquire(ctx, guard.root)
	if err != nil {
		return nil, fmt.Errorf("lock %q: %w", input, err)
	}
	current, err := guard.Resolve(input, create)
	if err != nil {
		unlock()
		return nil, err
	}
	if current != canonical {
		unlock()
		return nil, fmt.Errorf("lock %q: target changed while waiting", input)
	}
	return unlock, nil
}

type mutationTool struct {
	guard *PathGuard
	spec  agentcore.ToolSpec
	run   func(context.Context, *PathGuard, json.RawMessage) (agentcore.ToolExecutionResult, error)
}

func (t mutationTool) Spec() agentcore.ToolSpec {
	spec := t.spec
	spec.Schema = append(json.RawMessage(nil), spec.Schema...)
	return spec
}
func (t mutationTool) Risk() security.Risk { return security.Write }
func (t mutationTool) Execute(ctx context.Context, args json.RawMessage, _ agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("%s: %w", t.spec.Name, err)
	}
	if t.guard == nil {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("%s: path guard is nil", t.spec.Name)
	}
	result, err := t.run(ctx, t.guard, args)
	if err != nil {
		return agentcore.ToolExecutionResult{}, toolError(t.spec.Name, t.guard, err)
	}
	return result, nil
}

// NewWriteTool atomically creates or replaces a file. Internal file symlinks
// retain their link and address the canonical target inside the workspace.
func NewWriteTool(guard *PathGuard) Tool {
	return mutationTool{guard: guard, run: writeFile, spec: agentcore.ToolSpec{
		Name: "write", Description: "Atomically write a file, creating missing parent directories. Internal file symlinks update their target; symlink directories are rejected.",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}
}

func writeFile(ctx context.Context, guard *PathGuard, args json.RawMessage) (agentcore.ToolExecutionResult, error) {
	var params struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	if err := decodeReadOnly(args, &params); err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	if params.Path == "" || params.Content == nil {
		return agentcore.ToolExecutionResult{}, badArguments("path and content are required")
	}
	unlock, err := lockMutation(ctx, guard, params.Path, true)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	defer unlock()
	parent, path, err := mutationParent(ctx, guard, params.Path, true)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	defer parent.Close()
	before, info, err := mutationSource(ctx, parent, filepath.Base(path), true, 0)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	return commitMutation(ctx, parent, path, before, *params.Content, info)
}

// Each parent is opened separately, keeping creation and rename relative to a
// pinned directory handle. Missing directories are private to the user.
func mutationParent(ctx context.Context, guard *PathGuard, input string, create bool) (*os.Root, string, error) {
	for _, part := range strings.Split(filepath.ToSlash(input), "/") {
		if part == ".." || part == "." {
			return nil, "", fmt.Errorf("path %q: dot traversal is not allowed", input)
		}
	}
	canonical, err := guard.Resolve(input, create)
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(guard.root, canonical)
	if err != nil {
		return nil, "", fmt.Errorf("relative path %q: %w", input, err)
	}
	root, err := os.OpenRoot(guard.root)
	if err != nil {
		return nil, "", fmt.Errorf("open workspace: %w", err)
	}
	// Reject symlink parents in the caller's path before canonicalizing the leaf.
	original := input
	if filepath.IsAbs(original) {
		// The workspace itself may have an OS alias, such as /var on macOS.
		// Preserve components below that boundary for explicit symlink checks.
		ancestor := filepath.Dir(input)
		for {
			resolved, resolveErr := filepath.EvalSymlinks(ancestor)
			if resolveErr == nil && resolved == guard.root {
				original, err = filepath.Rel(ancestor, input)
				if err != nil {
					root.Close()
					return nil, "", err
				}
				break
			}
			up := filepath.Dir(ancestor)
			if up == ancestor {
				root.Close()
				return nil, "", classified(errOutsideWorkspace, "path %q is outside workspace root", input)
			}
			ancestor = up
		}
	}
	prefix := ""
	for _, part := range strings.Split(filepath.Dir(original), string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		prefix = filepath.Join(prefix, part)
		info, e := root.Lstat(prefix)
		if e != nil {
			if create && errors.Is(e, os.ErrNotExist) {
				break
			}
			root.Close()
			return nil, "", fmt.Errorf("inspect parent %q: %w", prefix, e)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, "", fmt.Errorf("parent %q: symlink directories are not allowed", prefix)
		}
	}
	parent := root
	for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		if err := ctx.Err(); err != nil {
			parent.Close()
			return nil, "", err
		}
		info, e := parent.Lstat(part)
		if errors.Is(e, os.ErrNotExist) && create {
			if e = parent.Mkdir(part, 0o700); e != nil && !errors.Is(e, os.ErrExist) {
				parent.Close()
				return nil, "", fmt.Errorf("create parent %q: %w", part, e)
			}
			info, e = parent.Lstat(part)
		}
		if e != nil {
			parent.Close()
			return nil, "", fmt.Errorf("inspect parent %q: %w", part, e)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			parent.Close()
			return nil, "", fmt.Errorf("parent %q: not a real directory", part)
		}
		child, e := openMutationDirectory(part, info, parent.OpenRoot)
		parent.Close()
		if e != nil {
			return nil, "", fmt.Errorf("open parent %q: %w", part, e)
		}
		parent = child
	}
	return parent, filepath.ToSlash(rel), nil
}

func mutationSource(ctx context.Context, parent *os.Root, name string, allowMissing bool, limit int64) (string, os.FileInfo, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("inspect %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, classified(errNotRegularFile, "read %q: not a regular file", name)
	}
	file, opened, err := openReadOnlyDescriptor(parent, name, false)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	if !os.SameFile(info, opened) {
		return "", nil, fmt.Errorf("read %q: file changed while opening", name)
	}
	var reader io.Reader = readOnlyContextReader{ctx: ctx, reader: file}
	if limit > 0 {
		if opened.Size() > limit {
			return "", nil, fmt.Errorf("read %q: exceeds 10 MiB", name)
		}
		reader = io.LimitReader(reader, limit+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", nil, fmt.Errorf("read %q: %w", name, err)
	}
	if limit > 0 && int64(len(data)) > limit {
		return "", nil, fmt.Errorf("read %q: exceeds 10 MiB", name)
	}
	return string(data), opened, nil
}

func commitMutation(ctx context.Context, parent *os.Root, path, before, after string, info os.FileInfo) (agentcore.ToolExecutionResult, error) {
	diff, err := unifiedDiff(path, before, after)
	if err != nil {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("diff %q: %w", path, err)
	}
	mode := os.FileMode(0o600)
	if info != nil {
		mode = info.Mode().Perm()
	}
	if err := atomicReplace(ctx, parent, filepath.Base(path), after, mode, info, parent.Rename); err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	return textResult(fmt.Sprintf("Wrote %d bytes to %s", len(after), path), map[string]any{"path": path, "created": info == nil, "bytes": len(after), "diff": diff})
}

// rename is supplied by the caller so commit failures can be tested without
// privileged-user-dependent permission tricks. It never removes the old file.
func atomicReplace(ctx context.Context, parent *os.Root, name, content string, mode os.FileMode, expected os.FileInfo, rename func(string, string) error) (err error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write %q: %w", name, err)
	}
	temp := ".harflex-" + rand.Text()
	file, err := parent.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", name, err)
	}
	defer func() {
		file.Close()
		if e := parent.Remove(temp); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("clean temporary file for %q: %w", name, e))
		}
	}()
	for offset := 0; offset < len(content); {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("write %q: %w", name, err)
		}
		end := min(offset+64*1024, len(content))
		n, e := io.WriteString(file, content[offset:end])
		if e != nil {
			return fmt.Errorf("write %q: %w", name, e)
		}
		if n != end-offset {
			return fmt.Errorf("write %q: %w", name, io.ErrShortWrite)
		}
		offset = end
	}
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("set mode for %q: %w", name, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %q: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %q: %w", name, err)
	}
	current, e := parent.Lstat(name)
	if expected == nil {
		if !errors.Is(e, os.ErrNotExist) {
			return fmt.Errorf("commit %q: destination appeared or cannot be inspected", name)
		}
	} else {
		if e != nil {
			return fmt.Errorf("revalidate %q: %w", name, e)
		}
		if !current.Mode().IsRegular() || !os.SameFile(expected, current) || expected.Size() != current.Size() || !expected.ModTime().Equal(current.ModTime()) || expected.Mode() != current.Mode() {
			return fmt.Errorf("commit %q: destination changed", name)
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("commit %q: %w", name, err)
	}
	if err := rename(temp, name); err != nil {
		return fmt.Errorf("replace %q: %w", name, err)
	}
	return nil
}
