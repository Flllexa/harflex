package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/security"
)

func readOnlyWorkspace(t *testing.T) (*PathGuard, string) {
	t.Helper()
	root := t.TempDir()
	guard, err := NewPathGuard(root)
	if err != nil {
		t.Fatal(err)
	}
	return guard, root
}

func putReadOnlyFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func executeReadOnly(t *testing.T, tool Tool, args string) (map[string]any, map[string]any) {
	t.Helper()
	result, err := tool.Execute(context.Background(), json.RawMessage(args), nil)
	if err != nil {
		t.Fatal(err)
	}
	var content, details map[string]any
	if err := json.Unmarshal(result.Content, &content); err != nil || content == nil {
		t.Fatalf("invalid content: %s (%v)", result.Content, err)
	}
	if err := json.Unmarshal(result.Details, &details); err != nil || details == nil {
		t.Fatalf("invalid details: %s (%v)", result.Details, err)
	}
	return content, details
}

func TestReadListFindGrepSpecs(t *testing.T) {
	g, _ := readOnlyWorkspace(t)
	for _, tc := range []struct {
		tool     Tool
		name     string
		required []string
	}{
		{NewReadTool(g), "read", []string{"path"}},
		{NewListTool(g), "ls", []string{}},
		{NewFindTool(g), "find", []string{}},
		{NewGrepTool(g), "grep", []string{"query"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.tool.Spec()
			if spec.Name != tc.name || spec.Description == "" || tc.tool.Risk() != security.ReadOnly {
				t.Fatalf("invalid tool spec/risk: %+v %s", spec, tc.tool.Risk())
			}
			var schema struct {
				Type                 string
				Properties           map[string]json.RawMessage
				Required             []string
				AdditionalProperties bool
			}
			if err := json.Unmarshal(spec.Schema, &schema); err != nil {
				t.Fatal(err)
			}
			if schema.Type != "object" || schema.AdditionalProperties || len(schema.Properties) == 0 || !reflect.DeepEqual(schema.Required, tc.required) {
				t.Fatalf("invalid schema: %s", spec.Schema)
			}
			for _, key := range schema.Required {
				if _, ok := schema.Properties[key]; !ok {
					t.Fatalf("missing required property %q", key)
				}
			}
		})
	}
}

func TestReadTextRangeAndDefaults(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "four.txt", "first\nsecond\nthird\nfourth\n")
	for _, tc := range []struct {
		args, text    string
		offset, count float64
		truncated     bool
	}{
		{`{"path":"four.txt","offset":2,"limit":2}`, "2: second\n3: third\n", 2, 2, true},
		{`{"path":"four.txt"}`, "1: first\n2: second\n3: third\n4: fourth\n", 1, 4, false},
		{`{"path":"four.txt","offset":3}`, "3: third\n4: fourth\n", 3, 2, false},
		{`{"path":"four.txt","limit":4}`, "1: first\n2: second\n3: third\n4: fourth\n", 1, 4, false},
		{`{"path":"four.txt","offset":8,"limit":1}`, "", 8, 0, false},
	} {
		t.Run(tc.args, func(t *testing.T) {
			content, details := executeReadOnly(t, NewReadTool(g), tc.args)
			if content["text"] != tc.text {
				t.Fatalf("content = %#v, want %q", content, tc.text)
			}
			if details["path"] != "four.txt" || details["offset"] != tc.offset || details["count"] != tc.count || details["truncated"] != tc.truncated {
				t.Fatalf("details = %#v", details)
			}
		})
	}
}

func TestReadLargeFileRequiresBoundedRange(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "large.txt", "first\nsecond\n"+strings.Repeat("x\n", 6*1024*1024))
	tool := NewReadTool(g)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"large.txt"}`), nil); err == nil || !strings.Contains(err.Error(), "10 MiB") {
		t.Fatalf("large file error = %v", err)
	}
	content, details := executeReadOnly(t, tool, `{"path":"large.txt","offset":2,"limit":1}`)
	if content["text"] != "2: second\n" || details["count"] != float64(1) || details["truncated"] != true {
		t.Fatalf("result = %#v %#v", content, details)
	}
}

func TestReadImagesAndUnknownBinary(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	for _, tc := range []struct{ name, bytes, mime string }{
		{"png", "\x89PNG\r\n\x1a\n\x00", "image/png"},
		{"jpeg", "\xff\xd8\xff\xe0\x00", "image/jpeg"},
		{"gif", "GIF89a\x01\x00\x01\x00", "image/gif"},
		{"webp", "RIFF\x10\x00\x00\x00WEBPVP8 \x00", "image/webp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putReadOnlyFile(t, root, tc.name, tc.bytes)
			content, details := executeReadOnly(t, NewReadTool(g), fmt.Sprintf(`{"path":%q}`, tc.name))
			if content["mime"] != tc.mime || content["data"] != base64.StdEncoding.EncodeToString([]byte(tc.bytes)) || details["path"] != tc.name {
				t.Fatalf("image = %#v %#v", content, details)
			}
		})
	}
	for _, bytes := range []string{"hello\x00world", "\xff\xfe\xfa"} {
		putReadOnlyFile(t, root, "unknown", bytes)
		if _, err := NewReadTool(g).Execute(context.Background(), json.RawMessage(`{"path":"unknown"}`), nil); err == nil || !strings.Contains(err.Error(), "binary") {
			t.Fatalf("binary error = %v", err)
		}
	}
}

func TestReadRejectsInvalidInputs(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "valid", "hello\n")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{}`, `null`, `{"path":""}`, `{"path":"."}`, `{"path":"missing"}`, `{"path":"valid","offset":0}`, `{"path":"valid","offset":-1}`, `{"path":"valid","limit":0}`, `{"path":"valid","limit":-1}`, `{"path":"valid","unknown":true}`, `{"path":"valid"}{}`, `{"path":"valid"} true`, fmt.Sprintf(`{"path":%q}`, outside)} {
		t.Run(args, func(t *testing.T) {
			if _, err := NewReadTool(g).Execute(context.Background(), json.RawMessage(args), nil); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestListOrderTypesSizesAndTruncation(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	for _, name := range []string{"zdir", "Adir", ".git"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	putReadOnlyFile(t, root, "z.txt", "three")
	putReadOnlyFile(t, root, "B.txt", "two")
	putReadOnlyFile(t, root, "a.txt", "one")
	_, details := executeReadOnly(t, NewListTool(g), `{}`)
	entries := details["entries"].([]any)
	wantNames := []string{".git", "Adir", "zdir", "a.txt", "B.txt", "z.txt"}
	for i, raw := range entries {
		entry := raw.(map[string]any)
		kind := "file"
		if i < 3 {
			kind = "directory"
		}
		if entry["name"] != wantNames[i] || entry["type"] != kind {
			t.Fatalf("entry = %#v", entry)
		}
		if _, ok := entry["size"].(float64); !ok {
			t.Fatalf("size missing: %#v", entry)
		}
		if entry["name"] == "z.txt" && entry["size"] != float64(5) {
			t.Fatalf("incorrect size: %#v", entry)
		}
	}
	if len(entries) != len(wantNames) || details["path"] != "." || details["count"] != float64(6) || details["truncated"] != false {
		t.Fatalf("details = %#v", details)
	}
	_, details = executeReadOnly(t, NewListTool(g), `{"limit":2}`)
	if details["count"] != float64(2) || details["truncated"] != true {
		t.Fatalf("limited = %#v", details)
	}
	_, details = executeReadOnly(t, NewListTool(g), `{"path":".git"}`)
	if details["count"] != float64(0) {
		t.Fatalf(".git listing = %#v", details)
	}
}

func TestListCaseTieBreak(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "a", "lower")
	putReadOnlyFile(t, root, "A", "upper")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Skip("filesystem is case insensitive")
	}
	_, details := executeReadOnly(t, NewListTool(g), `{}`)
	got := details["entries"].([]any)
	if got[0].(map[string]any)["name"] != "A" || got[1].(map[string]any)["name"] != "a" {
		t.Fatalf("order = %#v", got)
	}
}

func TestListFindGrepValidateLimitsPathsAndJSON(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "file", "needle\n")
	outside := t.TempDir()
	for _, tool := range []Tool{NewListTool(g), NewFindTool(g), NewGrepTool(g)} {
		t.Run(tool.Spec().Name, func(t *testing.T) {
			base := map[string]any{}
			if tool.Spec().Name == "grep" {
				base["query"] = "needle"
			}
			for _, invalid := range []map[string]any{{"limit": 0}, {"limit": -1}, {"limit": 201}, {"path": outside}, {"path": "missing"}, {"unknown": true}} {
				args := make(map[string]any)
				for k, v := range base {
					args[k] = v
				}
				for k, v := range invalid {
					args[k] = v
				}
				encoded, _ := json.Marshal(args)
				if _, err := tool.Execute(context.Background(), encoded, nil); err == nil {
					t.Fatalf("accepted %s", encoded)
				}
			}
			encoded, _ := json.Marshal(base)
			if _, err := tool.Execute(context.Background(), append(encoded, []byte(` {}`)...), nil); err == nil {
				t.Fatal("accepted trailing JSON")
			}
		})
	}
	if _, err := NewListTool(g).Execute(context.Background(), json.RawMessage(`{"path":"file"}`), nil); err == nil {
		t.Fatal("ls accepted file")
	}
	for _, args := range []string{`{}`, `{"query":""}`} {
		if _, err := NewGrepTool(g).Execute(context.Background(), json.RawMessage(args), nil); err == nil {
			t.Fatal("grep accepted empty query")
		}
	}
}

func TestFindPatternsSkipsAndRelativePaths(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	for _, name := range []string{"z.go", "src/b.go", "src/a.txt", ".git/config", "node_modules/pkg/file", "src/node_modules/pkg/file"} {
		putReadOnlyFile(t, root, name, "content")
	}
	for _, tc := range []struct {
		args  string
		paths []string
	}{
		{`{}`, []string{"src", "src/a.txt", "src/b.go", "z.go"}},
		{`{"pattern":"*.go"}`, []string{"src/b.go", "z.go"}},
		{`{"pattern":"src/*.go"}`, []string{"src/b.go"}},
		{`{"path":"src","pattern":"*.go"}`, []string{"src/b.go"}},
		{`{"pattern":"not-found*"}`, []string{}},
	} {
		t.Run(tc.args, func(t *testing.T) {
			_, details := executeReadOnly(t, NewFindTool(g), tc.args)
			got := details["paths"].([]any)
			want := make([]any, len(tc.paths))
			for i, v := range tc.paths {
				want[i] = v
			}
			if !reflect.DeepEqual(got, want) || details["count"] != float64(len(want)) || details["truncated"] != false {
				t.Fatalf("details = %#v, want %v", details, want)
			}
		})
	}
	_, details := executeReadOnly(t, NewFindTool(g), `{"limit":1}`)
	if details["count"] != float64(1) || details["truncated"] != true {
		t.Fatalf("limited = %#v", details)
	}
	if _, err := NewFindTool(g).Execute(context.Background(), json.RawMessage(`{"pattern":"["}`), nil); err == nil {
		t.Fatal("invalid glob accepted")
	}
}

func TestFindDoesNotFollowSymlinkDirectories(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "real/file", "content")
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, details := executeReadOnly(t, NewFindTool(g), `{}`)
	for _, raw := range details["paths"].([]any) {
		if strings.HasPrefix(raw.(string), "link/") {
			t.Fatal("followed symlink directory")
		}
	}
}

func TestFindRejectsInitialSymlinkDirectory(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "real/sub/file", "content")
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, input := range []string{"link", "link/sub", filepath.Join(g.root, "link")} {
		args, err := json.Marshal(map[string]string{"path": input})
		if err != nil {
			t.Fatal(err)
		}
		_, err = NewFindTool(g).Execute(context.Background(), args, nil)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("find path %q: error = %v, want explicit symlink rejection", input, err)
		}
	}
}

func TestGrepLiteralOrderSkipsAndDetails(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	for name, data := range map[string]string{
		"b.txt": "needle.*\nNeedle.*\nneedle\n", "a/x.txt": "no\nneedle.* olá\n",
		".git/config": "needle.*", "node_modules/pkg/file": "needle.*", "binary": "needle.*\x00",
		"image": "GIF89a\x00needle.*", "invalid-utf8": "needle.*\xff",
	} {
		putReadOnlyFile(t, root, name, data)
	}
	content, details := executeReadOnly(t, NewGrepTool(g), `{"query":"needle.*"}`)
	want := []any{map[string]any{"path": "a/x.txt", "line": float64(2), "text": "needle.* olá"}, map[string]any{"path": "b.txt", "line": float64(1), "text": "needle.*"}}
	if !reflect.DeepEqual(details["matches"], want) || details["count"] != float64(2) || details["truncated"] != false {
		t.Fatalf("details = %#v", details)
	}
	if content["text"] != "a/x.txt:2: needle.* olá\nb.txt:1: needle.*\n" {
		t.Fatalf("content = %#v", content)
	}
	_, details = executeReadOnly(t, NewGrepTool(g), `{"query":"needle.*","limit":1}`)
	if details["count"] != float64(1) || details["truncated"] != true {
		t.Fatalf("limited = %#v", details)
	}
	_, details = executeReadOnly(t, NewGrepTool(g), `{"query":"needle.*","path":"b.txt"}`)
	if details["count"] != float64(1) || details["truncated"] != false {
		t.Fatalf("file grep = %#v", details)
	}
}

func TestGrepLongLineReturnsContextualError(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "long.txt", strings.Repeat("x", 1024*1024+1)+"needle\n")
	_, err := NewGrepTool(g).Execute(context.Background(), json.RawMessage(`{"query":"needle"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "long.txt") || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("long line error = %v", err)
	}
}

func TestGrepExactLineSizeBoundary(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	for _, ending := range []string{"", "\n", "\r\n"} {
		t.Run(fmt.Sprintf("ending-%q", ending), func(t *testing.T) {
			line := "needle" + strings.Repeat("x", 1024*1024-len("needle"))
			putReadOnlyFile(t, root, "boundary.txt", line+ending)
			_, details := executeReadOnly(t, NewGrepTool(g), `{"query":"needle"}`)
			if details["count"] != float64(1) || details["matches"].([]any)[0].(map[string]any)["text"] != line {
				t.Fatalf("exact line boundary not preserved: count=%v", details["count"])
			}
		})
	}
}

func TestGrepOneByteOverLineSizeBoundary(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	for _, ending := range []string{"", "\n", "\r\n"} {
		t.Run(fmt.Sprintf("ending-%q", ending), func(t *testing.T) {
			line := "needle" + strings.Repeat("x", 1024*1024+1-len("needle"))
			putReadOnlyFile(t, root, "over.txt", line+ending)
			_, err := NewGrepTool(g).Execute(context.Background(), json.RawMessage(`{"query":"needle"}`), nil)
			if err == nil || !strings.Contains(err.Error(), "scan") || !strings.Contains(err.Error(), "over.txt") || !strings.Contains(err.Error(), "1 MiB") {
				t.Fatalf("one byte over boundary: error = %v", err)
			}
		})
	}
}

func TestListFindGrepDefaultMaximum(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	for i := range 202 {
		putReadOnlyFile(t, root, fmt.Sprintf("%03d.txt", i), "needle\n")
	}
	for _, tc := range []struct {
		tool Tool
		args string
	}{{NewListTool(g), `{}`}, {NewFindTool(g), `{}`}, {NewGrepTool(g), `{"query":"needle"}`}} {
		t.Run(tc.tool.Spec().Name, func(t *testing.T) {
			_, details := executeReadOnly(t, tc.tool, tc.args)
			if details["count"] != float64(200) || details["truncated"] != true {
				t.Fatalf("default limit = %#v", details)
			}
		})
	}
}

// Cancel on a known context check, making cancellation during traversal deterministic.
type readOnlyCancelContext struct {
	context.Context
	cancel        context.CancelFunc
	checks, after int
}

func (c *readOnlyCancelContext) Err() error {
	c.checks++
	if c.checks == c.after {
		c.cancel()
	}
	return c.Context.Err()
}

func TestReadListFindGrepCancellation(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	for i := range 10 {
		putReadOnlyFile(t, root, fmt.Sprintf("%02d.txt", i), strings.Repeat("needle\n", 100))
	}
	for _, tc := range []struct {
		tool Tool
		args string
	}{{NewReadTool(g), `{"path":"00.txt"}`}, {NewListTool(g), `{}`}, {NewFindTool(g), `{}`}, {NewGrepTool(g), `{"query":"needle"}`}} {
		for _, after := range []int{1, 7} {
			t.Run(fmt.Sprintf("%s/check-%d", tc.tool.Spec().Name, after), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				controlled := &readOnlyCancelContext{Context: ctx, cancel: cancel, after: after}
				if _, err := tc.tool.Execute(controlled, json.RawMessage(tc.args), nil); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation error = %v (checks %d)", err, controlled.checks)
				}
			})
		}
	}
}

func TestReadListFindGrepRejectExternalSymlink(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	outside := t.TempDir()
	putReadOnlyFile(t, outside, "secret", "secret")
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, tc := range []struct {
		tool Tool
		args string
	}{{NewReadTool(g), `{"path":"outside/secret"}`}, {NewListTool(g), `{"path":"outside"}`}, {NewFindTool(g), `{"path":"outside"}`}, {NewGrepTool(g), `{"path":"outside","query":"secret"}`}} {
		if _, err := tc.tool.Execute(context.Background(), json.RawMessage(tc.args), nil); err == nil {
			t.Fatalf("%s escaped root", tc.tool.Spec().Name)
		}
	}
}

func TestReadWholeFileAtSizeBoundary(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	data := strings.Repeat("x", 10*1024*1024)
	putReadOnlyFile(t, root, "boundary.txt", data)
	content, details := executeReadOnly(t, NewReadTool(g), `{"path":"boundary.txt"}`)
	if content["text"] != "1: "+data+"\n" || details["count"] != float64(1) || details["truncated"] != false {
		t.Fatalf("whole boundary file was not returned: details=%#v", details)
	}
}

func TestReadSmallRangeBeforeOversizedLine(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "range.txt", "first\n"+strings.Repeat("x", 11*1024*1024))
	content, details := executeReadOnly(t, NewReadTool(g), `{"path":"range.txt","limit":1}`)
	if content["text"] != "1: first\n" || details["truncated"] != true {
		t.Fatalf("bounded read = %#v %#v", content, details)
	}
}

func TestFindGrepWalkErrorsAreNotSilent(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "private/file", "needle")
	private := filepath.Join(root, "private")
	if err := os.Chmod(private, 0); err != nil {
		t.Skipf("permission changes unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(private, 0o700); err != nil {
			t.Errorf("restore permission: %v", err)
		}
	})
	if _, err := os.ReadDir(private); err == nil {
		t.Skip("filesystem does not enforce directory permissions for this user")
	}
	for _, tc := range []struct {
		tool Tool
		args string
	}{{NewFindTool(g), `{}`}, {NewGrepTool(g), `{"query":"needle"}`}} {
		t.Run(tc.tool.Spec().Name, func(t *testing.T) {
			_, err := tc.tool.Execute(context.Background(), json.RawMessage(tc.args), nil)
			if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "private") || !strings.Contains(err.Error(), "walk") {
				t.Fatalf("walk error = %v", err)
			}
		})
	}
}

func TestFindGrepGlobalPathOrder(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	for _, name := range []string{"a/z.txt", "a.txt", "z.txt"} {
		putReadOnlyFile(t, root, name, "needle\n")
	}
	_, details := executeReadOnly(t, NewFindTool(g), `{"pattern":"*.txt","limit":1}`)
	if !reflect.DeepEqual(details["paths"], []any{"a.txt"}) || details["truncated"] != true {
		t.Fatalf("find order = %#v", details)
	}
	_, details = executeReadOnly(t, NewGrepTool(g), `{"query":"needle","limit":1}`)
	if details["matches"].([]any)[0].(map[string]any)["path"] != "a.txt" || details["truncated"] != true {
		t.Fatalf("grep order = %#v", details)
	}
}

func TestGrepSkipsLateBinaryAndExternalSymlinkFiles(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "binary", "needle\n"+strings.Repeat("text\n", 200)+"\x00")
	outside := t.TempDir()
	putReadOnlyFile(t, outside, "secret", "needle")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, details := executeReadOnly(t, NewGrepTool(g), `{"query":"needle"}`)
	if details["count"] != float64(0) {
		t.Fatalf("grep published binary or symlink content: %#v", details)
	}
}
