package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/persioflexa/harflex/internal/agentcore"
)

// NewListTool lists immediate directory entries, including hidden entries.
func NewListTool(guard *PathGuard) Tool {
	return readOnlyTool{guard: guard, run: listDirectory, spec: agentcore.ToolSpec{
		Name: "ls", Description: "List directory entries with types and sizes; directories sort first, then names case-insensitively. Defaults to workspace root and at most 200 entries.",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","default":""},"limit":{"type":"integer","minimum":1,"maximum":200,"default":200}},"required":[],"additionalProperties":false}`),
	}}
}

// NewFindTool finds workspace paths without traversing ignored or symlink directories.
func NewFindTool(guard *PathGuard) Tool {
	return readOnlyTool{guard: guard, run: findPaths, spec: agentcore.ToolSpec{
		Name: "find", Description: "Find sorted workspace-relative paths by optional basename or relative-path glob. Skips .git and node_modules; returns at most 200 paths.",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","default":""},"pattern":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":200,"default":200}},"required":[],"additionalProperties":false}`),
	}}
}

func readOnlyLimit(value *int) (int, error) {
	if value == nil {
		return maxResults, nil
	}
	if *value < 1 || *value > maxResults {
		return 0, badArguments("limit must be between 1 and %d", maxResults)
	}
	return *value, nil
}

type directoryEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

func directoryEntryLess(a, b directoryEntry) bool {
	if (a.Type == "directory") != (b.Type == "directory") {
		return a.Type == "directory"
	}
	left, right := strings.ToLower(a.Name), strings.ToLower(b.Name)
	if left != right {
		return left < right
	}
	return a.Name < b.Name
}

func listDirectory(ctx context.Context, guard *PathGuard, args json.RawMessage) (agentcore.ToolExecutionResult, error) {
	var params struct {
		Path  string `json:"path"`
		Limit *int   `json:"limit"`
	}
	if err := decodeReadOnly(args, &params); err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	limit, err := readOnlyLimit(params.Limit)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	root, rel, err := readOnlyRoot(guard, params.Path)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	defer root.Close()
	directory, _, err := openReadOnlyDescriptor(root, rel, true)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	defer directory.Close()
	entries := make([]directoryEntry, 0, limit+1)
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return agentcore.ToolExecutionResult{}, fmt.Errorf("list %q: %w", rel, err)
		}
		batch, err := directory.ReadDir(64)
		if err != nil && !errors.Is(err, io.EOF) {
			return agentcore.ToolExecutionResult{}, fmt.Errorf("list directory %q: %w", rel, err)
		}
		for _, entry := range batch {
			if err := ctx.Err(); err != nil {
				return agentcore.ToolExecutionResult{}, fmt.Errorf("list %q: %w", rel, err)
			}
			info, err := entry.Info()
			if err != nil {
				return agentcore.ToolExecutionResult{}, fmt.Errorf("inspect entry %q: %w", path.Join(rel, entry.Name()), err)
			}
			kind := "file"
			switch {
			case info.IsDir():
				kind = "directory"
			case info.Mode()&os.ModeSymlink != 0:
				kind = "symlink"
			case !info.Mode().IsRegular():
				kind = "other"
			}
			candidate := directoryEntry{Name: entry.Name(), Type: kind, Size: info.Size()}
			index := sort.Search(len(entries), func(i int) bool { return !directoryEntryLess(entries[i], candidate) })
			if index < limit {
				entries = append(entries, directoryEntry{})
				copy(entries[index+1:], entries[index:])
				entries[index] = candidate
				if len(entries) > limit {
					entries = entries[:limit]
				}
			}
			total++
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	var text strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&text, "%s\t%s\t%d\n", entry.Name, entry.Type, entry.Size)
	}
	return textResult(text.String(), map[string]any{"path": rel, "count": len(entries), "truncated": total > limit, "entries": entries})
}

func walkReadOnly(ctx context.Context, root *os.Root, start string, visit func(string, fs.DirEntry) error) error {
	return fs.WalkDir(root.FS(), start, func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("walk %q: %w", name, err)
		}
		if walkErr != nil {
			return fmt.Errorf("walk %q: %w", name, walkErr)
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules") {
			return fs.SkipDir
		}
		return visit(name, entry)
	})
}

func findRoot(ctx context.Context, guard *PathGuard, input string) (*os.Root, string, error) {
	lexical := filepath.Clean(input)
	if filepath.IsAbs(lexical) {
		var err error
		lexical, err = filepath.Rel(guard.root, lexical)
		if err != nil {
			return nil, "", fmt.Errorf("make find path %q relative: %w", input, err)
		}
	}
	if filepath.IsAbs(lexical) || lexical == ".." || strings.HasPrefix(lexical, ".."+string(filepath.Separator)) {
		return nil, "", classified(errOutsideWorkspace, "find path %q is outside workspace root", input)
	}
	root, err := os.OpenRoot(guard.root)
	if err != nil {
		return nil, "", fmt.Errorf("open find workspace: %w", err)
	}
	// Check lexical components before PathGuard canonicalization would erase links.
	prefix := "."
	for _, component := range strings.Split(lexical, string(filepath.Separator)) {
		if err := ctx.Err(); err != nil {
			root.Close()
			return nil, "", fmt.Errorf("inspect find path %q: %w", input, err)
		}
		prefix = filepath.Join(prefix, component)
		info, err := root.Lstat(prefix)
		if err != nil {
			root.Close()
			return nil, "", fmt.Errorf("inspect find path %q: %w", input, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, "", fmt.Errorf("find path %q: symlink paths are not followed", input)
		}
	}
	if _, err := guard.Resolve(input, false); err != nil {
		root.Close()
		return nil, "", err
	}
	// Static Lstat rejects existing links; retain the lexical path for WalkDir.
	return root, filepath.ToSlash(lexical), nil
}

func findPaths(ctx context.Context, guard *PathGuard, args json.RawMessage) (agentcore.ToolExecutionResult, error) {
	var params struct {
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
		Limit   *int   `json:"limit"`
	}
	if err := decodeReadOnly(args, &params); err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	limit, err := readOnlyLimit(params.Limit)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	if _, err := path.Match(params.Pattern, ""); err != nil {
		return agentcore.ToolExecutionResult{}, badArguments("invalid find pattern: %v", err)
	}
	root, rel, err := findRoot(ctx, guard, params.Path)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	defer root.Close()
	paths := make([]string, 0, limit+1)
	total := 0
	err = walkReadOnly(ctx, root, rel, func(name string, entry fs.DirEntry) error {
		if name == rel && entry.IsDir() {
			return nil
		}
		if params.Pattern != "" {
			baseMatch, _ := path.Match(params.Pattern, entry.Name())
			relMatch, _ := path.Match(params.Pattern, name)
			if !baseMatch && !relMatch {
				return nil
			}
		}
		index := sort.SearchStrings(paths, name)
		if index < limit {
			paths = append(paths, "")
			copy(paths[index+1:], paths[index:])
			paths[index] = name
			if len(paths) > limit {
				paths = paths[:limit]
			}
		}
		total++
		return nil
	})
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	text := strings.Join(paths, "\n")
	if len(paths) > 0 {
		text += "\n"
	}
	return textResult(text, map[string]any{"path": rel, "count": len(paths), "truncated": total > limit, "paths": paths})
}
