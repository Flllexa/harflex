package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
)

// NewEditTool edits exactly one occurrence in an existing UTF-8 text file.
func NewEditTool(guard *PathGuard) Tool {
	return mutationTool{guard: guard, run: editFile, spec: agentcore.ToolSpec{
		Name: "edit", Description: "Atomically replace exactly one occurrence in a UTF-8 text file of at most 10 MiB. Internal file symlinks update their target; symlink directories are rejected.",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"oldText":{"type":"string","minLength":1},"newText":{"type":"string"}},"required":["path","oldText","newText"],"additionalProperties":false}`),
	}}
}

func editFile(ctx context.Context, guard *PathGuard, args json.RawMessage) (agentcore.ToolExecutionResult, error) {
	var params struct {
		Path    string  `json:"path"`
		OldText string  `json:"oldText"`
		NewText *string `json:"newText"`
	}
	if err := decodeReadOnly(args, &params); err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	if params.Path == "" || params.OldText == "" || params.NewText == nil {
		return agentcore.ToolExecutionResult{}, badArguments("path, nonempty oldText and newText are required")
	}
	unlock, err := lockMutation(ctx, guard, params.Path, false)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	defer unlock()
	parent, path, err := mutationParent(ctx, guard, params.Path, false)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	defer parent.Close()
	before, info, err := mutationSource(ctx, parent, filepath.Base(path), false, maxReadBytes)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	if !utf8.ValidString(before) || strings.IndexByte(before, 0) >= 0 {
		return agentcore.ToolExecutionResult{}, classified(errBinaryFile, "edit %q: unsupported binary file", path)
	}
	if count := strings.Count(before, params.OldText); count != 1 {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("edit %q: %w", path, &editMatchError{count: count})
	}
	return commitMutation(ctx, parent, path, before, strings.Replace(before, params.OldText, *params.NewText, 1), info)
}
