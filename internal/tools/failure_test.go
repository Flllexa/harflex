package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
)

// failureOf runs a tool and returns the failure the model can act on, or fails the test when the error is
// anything else (which would end the run).
func failureOf(t *testing.T, tool Tool, args string) (*agentcore.ToolFailure, error) {
	t.Helper()
	_, err := tool.Execute(context.Background(), json.RawMessage(args), nil)
	if err == nil {
		t.Fatalf("%s succeeded, want a failure", args)
	}
	var failure *agentcore.ToolFailure
	if !errors.As(err, &failure) {
		t.Fatalf("%s: %v is not a failure the model can act on, so it would end the run", args, err)
	}
	return failure, err
}

func TestReadOfAFileThatDoesNotExistYetIsAFailureTheModelCanActOn(t *testing.T) {
	guard, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "notes.txt", "x\n")
	failure, err := failureOf(t, NewReadTool(guard), `{"path":"index.html","limit":300,"offset":1}`)
	if failure.Code != "not_found" || failure.Message != `"index.html" does not exist` {
		t.Fatalf("failure = %+v", failure)
	}
	if strings.Contains(failure.Message, root) || strings.Contains(failure.Code, root) {
		t.Fatalf("the message carries the private root: %q", failure.Message)
	}
	// Code that inspects the cause still sees it, and the text for logs is the original one.
	if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "index.html") {
		t.Fatalf("the cause is lost: %v", err)
	}
	// Nested paths are reported relative to the project.
	failure, _ = failureOf(t, NewReadTool(guard), `{"path":"src/deep/missing.go"}`)
	if failure.Code != "not_found" || failure.Message != `"src/deep/missing.go" does not exist` {
		t.Fatalf("nested failure = %+v", failure)
	}
}

func TestListingAndSearchingAMissingFolderAreFailuresToo(t *testing.T) {
	guard, _ := readOnlyWorkspace(t)
	for name, call := range map[string]struct {
		tool Tool
		args string
	}{
		"ls":   {NewListTool(guard), `{"path":"nope"}`},
		"grep": {NewGrepTool(guard), `{"path":"nope","query":"x"}`},
		"find": {NewFindTool(guard), `{"path":"nope","pattern":"*.go"}`},
	} {
		failure, _ := failureOf(t, call.tool, call.args)
		if failure.Code != "not_found" || failure.Message != `"nope" does not exist` {
			t.Errorf("%s: %+v", name, failure)
		}
	}
}

// Reaching outside the project is not a slip the run absorbs: it still ends, and it does so the same way
// whether or not the target exists, so nothing is learned about the rest of the disk.
func TestPathsOutsideTheProjectStillEndTheRunWhetherOrNotTheyExist(t *testing.T) {
	guard, root := readOnlyWorkspace(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "exists.txt"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks are unavailable")
	}
	paths := []string{
		filepath.Join(outside, "exists.txt"),
		filepath.Join(outside, "missing.txt"),
		"../" + filepath.Base(outside) + "/exists.txt",
		"../" + filepath.Base(outside) + "/missing.txt",
		"link/exists.txt",
		"link/missing.txt",
	}
	for _, path := range paths {
		args, _ := json.Marshal(map[string]string{"path": path})
		for name, tool := range map[string]Tool{"read": NewReadTool(guard), "ls": NewListTool(guard)} {
			_, err := tool.Execute(context.Background(), json.RawMessage(args), nil)
			var failure *agentcore.ToolFailure
			if err == nil || errors.As(err, &failure) {
				t.Errorf("%s %s: %v must end the run, not be handed back to the model", name, path, err)
			}
		}
	}
}

func TestWrongKindOfPathAndBadArgumentsAreFailures(t *testing.T) {
	guard, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "dir/inner.txt", "x\n")
	putReadOnlyFile(t, root, "file.txt", "x\n")
	read, list := NewReadTool(guard), NewListTool(guard)
	for _, c := range []struct {
		name, args string
		tool       Tool
		code       string
		contains   string
	}{
		{"read a folder", `{"path":"dir"}`, read, "not_a_file", "not a regular file"},
		{"list a file", `{"path":"file.txt"}`, list, "not_a_directory", "not a directory"},
		{"unknown argument", `{"path":"file.txt","bogus":1}`, read, "invalid_arguments", "unknown field"},
		{"not an object", `[1,2]`, read, "invalid_arguments", "JSON object"},
		{"empty path", `{"path":""}`, read, "invalid_arguments", "path is required"},
		{"offset below one", `{"path":"file.txt","offset":0}`, read, "invalid_arguments", "at least 1"},
		{"wrong type", `{"path":5}`, read, "invalid_arguments", "cannot unmarshal"},
	} {
		failure, _ := failureOf(t, c.tool, c.args)
		if failure.Code != c.code || !strings.Contains(failure.Message, c.contains) {
			t.Errorf("%s: %+v", c.name, failure)
		}
		if strings.Contains(failure.Message, root) {
			t.Errorf("%s: the message carries the private root: %q", c.name, failure.Message)
		}
	}
}

func TestReadingAHugeFileWithoutARangeIsAFailure(t *testing.T) {
	guard, root := readOnlyWorkspace(t)
	file, err := os.Create(filepath.Join(root, "huge.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxReadBytes + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	failure, _ := failureOf(t, NewReadTool(guard), `{"path":"huge.log"}`)
	if failure.Code != "range_required" || !strings.Contains(failure.Message, "offset and limit") {
		t.Fatalf("failure = %+v", failure)
	}
}

func TestEditAndWriteMistakesTheModelCanFix(t *testing.T) {
	guard, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "page.html", "<p>one</p>\n<p>one</p>\n")
	edit, write := NewEditTool(guard), NewWriteTool(guard)
	for _, c := range []struct {
		name, args string
		tool       Tool
		code       string
		message    string
	}{
		{"nothing matches", `{"path":"page.html","oldText":"<h1>","newText":"x"}`, edit, "no_match", "oldText was not found in the file"},
		{"two places match", `{"path":"page.html","oldText":"<p>one</p>","newText":"x"}`, edit, "ambiguous_match", "oldText matches 2 times; it must match exactly once"},
		{"file is missing", `{"path":"gone.html","oldText":"a","newText":"b"}`, edit, "not_found", `"gone.html" does not exist`},
		{"edit without oldText", `{"path":"page.html","oldText":"","newText":"b"}`, edit, "invalid_arguments", "the arguments are not valid: path, nonempty oldText and newText are required"},
		{"write without content", `{"path":"page.html"}`, write, "invalid_arguments", "the arguments are not valid: path and content are required"},
	} {
		failure, _ := failureOf(t, c.tool, c.args)
		if failure.Code != c.code || failure.Message != c.message {
			t.Errorf("%s: %+v", c.name, failure)
		}
	}
	// None of them touched the file.
	if body, _ := os.ReadFile(filepath.Join(root, "page.html")); string(body) != "<p>one</p>\n<p>one</p>\n" {
		t.Fatalf("a failed edit changed the file: %q", body)
	}
}

func TestWritingCreatesTheFileTheReadFoundMissing(t *testing.T) {
	guard, root := readOnlyWorkspace(t)
	if _, err := NewReadTool(guard).Execute(context.Background(), json.RawMessage(`{"path":"index.html"}`), nil); err == nil {
		t.Fatal("the file does not exist yet")
	}
	if _, err := NewWriteTool(guard).Execute(context.Background(), json.RawMessage(`{"path":"index.html","content":"<h1>todo</h1>"}`), nil); err != nil {
		t.Fatalf("the model's next step must work: %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "index.html")); string(body) != "<h1>todo</h1>" {
		t.Fatalf("file = %q", body)
	}
}

func TestShellCommandThatFailsKeepsItsOutputAndTheRun(t *testing.T) {
	result, err := NewShellTool(ShellConfig{CWD: t.TempDir()}).Execute(context.Background(), shellTestArgs(t, shellTestCommand(t, "fail"), 0), nil)
	var failure *agentcore.ToolFailure
	if !errors.As(err, &failure) || failure.Code != "exit_status" || failure.Message != "the command exited with status 7" {
		t.Fatalf("a non-zero exit must be a failure the model can act on: %v %+v", err, failure)
	}
	text, details := shellTestResult(t, result)
	if details.ExitCode != 7 || !strings.Contains(text, "before failure") {
		t.Fatalf("the output was lost: %q %+v", text, details)
	}
}

func TestShellCommandThatRunsOutOfItsOwnTimeIsAFailureButTheRunsDeadlineIsNot(t *testing.T) {
	// The command's own limit: the model can choose a shorter job or a longer limit.
	_, err := NewShellTool(ShellConfig{CWD: t.TempDir(), DefaultTimeout: 5 * time.Second}).Execute(context.Background(), shellTestArgs(t, shellTestCommand(t, "wait"), 200), nil)
	var failure *agentcore.ToolFailure
	if !errors.As(err, &failure) || failure.Code != "timeout" || !strings.Contains(failure.Message, "time limit") {
		t.Fatalf("the command's own timeout = %v %+v", err, failure)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the cause must stay inspectable: %v", err)
	}
	// The deadline of the run that called the tool ends the run.
	ctx, done := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer done()
	_, err = NewShellTool(ShellConfig{CWD: t.TempDir(), DefaultTimeout: 5 * time.Second}).Execute(ctx, shellTestArgs(t, shellTestCommand(t, "wait"), 0), nil)
	if err == nil || errors.As(err, &failure) && failure != nil && failure.Code == "timeout" {
		t.Fatalf("the run's own deadline must stay fatal: %v", err)
	}
	// So does cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	_, err = NewShellTool(ShellConfig{CWD: t.TempDir()}).Execute(ctx, shellTestArgs(t, shellTestCommand(t, "wait"), 0), func(_ context.Context, update agentcore.ToolUpdate) {
		if strings.Contains(update.Text, "ready") {
			cancel()
		}
	})
	var again *agentcore.ToolFailure
	if err == nil || errors.As(err, &again) {
		t.Fatalf("cancellation must stay fatal: %v", err)
	}
}

func TestShellArgumentsTheModelCanFix(t *testing.T) {
	tool := NewShellTool(ShellConfig{CWD: t.TempDir()})
	for _, c := range []struct{ args, contains string }{
		{`{"command":"   "}`, "command is required"},
		{`{"command":"echo","timeoutMs":0}`, "timeoutMs must be a positive duration"},
		{`{"command":"echo","extra":true}`, "unknown field"},
	} {
		failure, _ := failureOf(t, tool, c.args)
		if failure.Code != "invalid_arguments" || !strings.Contains(failure.Message, c.contains) {
			t.Errorf("%s: %+v", c.args, failure)
		}
	}
}

func TestAMissingWorkingDirectoryOfTheShellIsNotSomethingTheModelCanFix(t *testing.T) {
	tool := NewShellTool(ShellConfig{CWD: filepath.Join(t.TempDir(), "gone")})
	_, err := tool.Execute(context.Background(), shellTestArgs(t, shellTestCommand(t, "fail"), 0), nil)
	var failure *agentcore.ToolFailure
	if err == nil || errors.As(err, &failure) {
		t.Fatalf("a broken setup must still end the run: %v", err)
	}
}

func TestSafeDetailKeepsOneBoundedPrintableLine(t *testing.T) {
	if got := safeDetail("a\n\tb\x00  c"); got != "a b c" {
		t.Fatalf("safeDetail = %q", got)
	}
	long := safeDetail(strings.Repeat("é", 300))
	if !strings.HasSuffix(long, "…") || len(long) > 210 {
		t.Fatalf("long detail = %d bytes", len(long))
	}
}
