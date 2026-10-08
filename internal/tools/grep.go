package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
)

const maxGrepLineBytes = 1024 * 1024

// NewGrepTool searches UTF-8 files for literal, case-sensitive text.
func NewGrepTool(guard *PathGuard) Tool {
	return readOnlyTool{guard: guard, run: grepFiles, spec: agentcore.ToolSpec{
		Name: "grep", Description: "Search UTF-8 files for a literal case-sensitive query. Skips binaries, images, .git and node_modules; lines are limited to 1 MiB and results to 200 matches.",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","default":""},"query":{"type":"string","minLength":1},"limit":{"type":"integer","minimum":1,"maximum":200,"default":200}},"required":["query"],"additionalProperties":false}`),
	}}
}

type grepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func grepMatchLess(a, b grepMatch) bool {
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	return a.Line < b.Line
}

func grepFiles(ctx context.Context, guard *PathGuard, args json.RawMessage) (agentcore.ToolExecutionResult, error) {
	var params struct {
		Path  string `json:"path"`
		Query string `json:"query"`
		Limit *int   `json:"limit"`
	}
	if err := decodeReadOnly(args, &params); err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	if params.Query == "" || !utf8.ValidString(params.Query) {
		return agentcore.ToolExecutionResult{}, badArguments("query must be non-empty UTF-8 text")
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
	matches := make([]grepMatch, 0, limit+1)
	total := 0
	err = walkReadOnly(ctx, root, rel, func(name string, entry fs.DirEntry) error {
		if !entry.Type().IsRegular() {
			return nil
		}
		found, count, err := grepFile(ctx, root, name, params.Query, limit)
		if err != nil {
			return err
		}
		total += count
		for _, candidate := range found {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("grep %q: %w", name, err)
			}
			index := sort.Search(len(matches), func(i int) bool { return !grepMatchLess(matches[i], candidate) })
			if index < limit {
				matches = append(matches, grepMatch{})
				copy(matches[index+1:], matches[index:])
				matches[index] = candidate
				if len(matches) > limit {
					matches = matches[:limit]
				}
			}
		}
		return nil
	})
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	var text strings.Builder
	for _, match := range matches {
		fmt.Fprintf(&text, "%s:%d: %s\n", match.Path, match.Line, match.Text)
	}
	return textResult(text.String(), map[string]any{"path": rel, "count": len(matches), "truncated": total > limit, "matches": matches})
}

func grepFile(ctx context.Context, root *os.Root, path, query string, limit int) ([]grepMatch, int, error) {
	file, _, err := openReadOnlyDescriptor(root, path, false)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	reader := bufio.NewReader(readOnlyContextReader{ctx: ctx, reader: file})
	prefix, err := reader.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, fmt.Errorf("inspect %q: %w", path, err)
	}
	if supportedImage(prefix) != "" || bytes.IndexByte(prefix, 0) >= 0 {
		return nil, 0, nil
	}
	scanner := bufio.NewScanner(reader)
	// Leave room for a full-size line followed by a CRLF delimiter.
	scanner.Buffer(make([]byte, 64*1024), maxGrepLineBytes+2)
	matches := make([]grepMatch, 0, limit)
	line, total := 0, 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, 0, fmt.Errorf("scan %q: %w", path, err)
		}
		line++
		data := scanner.Bytes()
		if len(data) > maxGrepLineBytes {
			return nil, 0, fmt.Errorf("scan %q: line %d exceeds 1 MiB", path, line)
		}
		// Delay publishing matches until the entire file passes text detection.
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return nil, 0, nil
		}
		if bytes.Contains(data, []byte(query)) {
			total++
			if len(matches) < limit {
				matches = append(matches, grepMatch{Path: path, Line: line, Text: scanner.Text()})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("scan %q (maximum line content 1 MiB): %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, fmt.Errorf("scan %q: %w", path, err)
	}
	return matches, total, nil
}
