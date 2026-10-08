package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/security"
)

const maxReadBytes = 10 * 1024 * 1024
const maxResults = 200

type readOnlyTool struct {
	spec  agentcore.ToolSpec
	guard *PathGuard
	run   func(context.Context, *PathGuard, json.RawMessage) (agentcore.ToolExecutionResult, error)
}

func (t readOnlyTool) Spec() agentcore.ToolSpec {
	spec := t.spec
	spec.Schema = append(json.RawMessage(nil), spec.Schema...)
	return spec
}
func (t readOnlyTool) Risk() security.Risk { return security.ReadOnly }
func (t readOnlyTool) Execute(ctx context.Context, args json.RawMessage, _ agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
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

// NewReadTool reads numbered UTF-8 lines or base64-encoded supported images.
func NewReadTool(guard *PathGuard) Tool {
	return readOnlyTool{guard: guard, run: readFile, spec: agentcore.ToolSpec{
		Name: "read", Description: "Read UTF-8 text with 1-based line ranges, or PNG, JPEG, GIF and WebP images. Reads include at most 10 MiB of source data; large text files require a range.",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"offset":{"type":"integer","minimum":1,"default":1},"limit":{"type":"integer","minimum":1}},"required":["path"],"additionalProperties":false}`),
	}}
}

func decodeReadOnly(args json.RawMessage, target any) error {
	if trimmed := bytes.TrimSpace(args); len(trimmed) == 0 || trimmed[0] != '{' {
		return badArguments("parameters must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return badArguments("decode parameters: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return badArguments("decode trailing parameters: %v", err)
		}
		return badArguments("parameters must contain exactly one JSON value")
	}
	return nil
}

func readOnlyResult(content, details any) (agentcore.ToolExecutionResult, error) {
	encodedContent, err := json.Marshal(content)
	if err != nil {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("encode content: %w", err)
	}
	encodedDetails, err := json.Marshal(details)
	if err != nil {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("encode details: %w", err)
	}
	return agentcore.ToolExecutionResult{Content: encodedContent, Details: encodedDetails}, nil
}

func textResult(text string, details any) (agentcore.ToolExecutionResult, error) {
	return readOnlyResult(map[string]string{"text": text}, details)
}

// os.Root confines filesystem access to the opened workspace root.
func readOnlyRoot(guard *PathGuard, input string) (*os.Root, string, error) {
	canonical, err := guard.Resolve(input, false)
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(guard.root, canonical)
	if err != nil {
		return nil, "", fmt.Errorf("make path %q relative: %w", input, err)
	}
	root, err := os.OpenRoot(guard.root)
	if err != nil {
		return nil, "", fmt.Errorf("open workspace: %w", err)
	}
	return root, filepath.ToSlash(rel), nil
}

func openReadOnlyDescriptor(root *os.Root, path string, directory bool) (*os.File, os.FileInfo, error) {
	file, err := openReadOnlyPlatform(root, filepath.FromSlash(path))
	if err != nil {
		return nil, nil, fmt.Errorf("open %q: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("stat descriptor %q: %w", path, err)
	}
	if directory && !info.IsDir() {
		file.Close()
		return nil, nil, classified(errNotDirectory, "open %q: not a directory", path)
	}
	if !directory && !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, classified(errNotRegularFile, "open %q: not a regular file", path)
	}
	return file, info, nil
}

func supportedImage(data []byte) string {
	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mime
	}
	return ""
}

type readOnlyContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r readOnlyContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func readFile(ctx context.Context, guard *PathGuard, args json.RawMessage) (agentcore.ToolExecutionResult, error) {
	var params struct {
		Path   string `json:"path"`
		Offset *int   `json:"offset"`
		Limit  *int   `json:"limit"`
	}
	if err := decodeReadOnly(args, &params); err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	if params.Path == "" {
		return agentcore.ToolExecutionResult{}, badArguments("path is required")
	}
	offset := 1
	if params.Offset != nil {
		offset = *params.Offset
	}
	if offset < 1 || (params.Limit != nil && *params.Limit < 1) {
		return agentcore.ToolExecutionResult{}, badArguments("offset and limit must be at least 1")
	}
	root, path, err := readOnlyRoot(guard, params.Path)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	defer root.Close()
	file, info, err := openReadOnlyDescriptor(root, path, false)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	defer file.Close()
	if info.Size() > maxReadBytes && params.Offset == nil && params.Limit == nil {
		return agentcore.ToolExecutionResult{}, classified(errRangeRequired, "read %q: files larger than 10 MiB require a line range", path)
	}
	reader := bufio.NewReaderSize(readOnlyContextReader{ctx: ctx, reader: file}, 64*1024)
	prefix, err := reader.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("inspect %q: %w", path, err)
	}
	if mime := supportedImage(prefix); mime != "" {
		data, err := io.ReadAll(io.LimitReader(reader, maxReadBytes+1))
		if err != nil {
			return agentcore.ToolExecutionResult{}, fmt.Errorf("read image %q: %w", path, err)
		}
		if len(data) > maxReadBytes {
			return agentcore.ToolExecutionResult{}, fmt.Errorf("read image %q: exceeds 10 MiB", path)
		}
		if err := ctx.Err(); err != nil {
			return agentcore.ToolExecutionResult{}, err
		}
		return readOnlyResult(map[string]string{"mime": mime, "data": base64.StdEncoding.EncodeToString(data)}, map[string]any{"path": path, "bytes": len(data), "truncated": false})
	}
	if bytes.IndexByte(prefix, 0) >= 0 {
		return agentcore.ToolExecutionResult{}, classified(errBinaryFile, "read %q: unsupported binary file", path)
	}
	var output strings.Builder
	count, lineNumber := 0, 0
	sourceBytes := 0
	truncated := false
	for {
		if err := ctx.Err(); err != nil {
			return agentcore.ToolExecutionResult{}, fmt.Errorf("read %q: %w", path, err)
		}
		if params.Limit != nil && count == *params.Limit {
			_, err := reader.Peek(1)
			if err != nil && !errors.Is(err, io.EOF) {
				return agentcore.ToolExecutionResult{}, fmt.Errorf("read %q: %w", path, err)
			}
			truncated = err == nil
			break
		}
		line, err := boundedReadLine(ctx, reader)
		if err != nil && !errors.Is(err, io.EOF) {
			return agentcore.ToolExecutionResult{}, fmt.Errorf("read %q: %w", path, err)
		}
		if len(line) == 0 && errors.Is(err, io.EOF) {
			break
		}
		lineNumber++
		if !utf8.Valid(line) || bytes.IndexByte(line, 0) >= 0 {
			return agentcore.ToolExecutionResult{}, classified(errBinaryFile, "read %q: unsupported binary file", path)
		}
		if lineNumber >= offset {
			if sourceBytes+len(line) > maxReadBytes {
				return agentcore.ToolExecutionResult{}, fmt.Errorf("read %q: selected data exceeds 10 MiB; use a smaller line range", path)
			}
			sourceBytes += len(line)
			text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
			formatted := fmt.Sprintf("%d: %s\n", lineNumber, text)
			output.WriteString(formatted)
			count++
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return textResult(output.String(), map[string]any{"path": path, "offset": offset, "count": count, "truncated": truncated})
}

func boundedReadLine(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > maxReadBytes {
			return nil, fmt.Errorf("line exceeds 10 MiB")
		}
		line = append(line, part...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}
