package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/security"
)

func TestShellHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	switch args[1] {
	case "stream":
		fmt.Fprintln(os.Stdout, "first")
		time.Sleep(80 * time.Millisecond)
		fmt.Fprintln(os.Stdout, "second")
		fmt.Fprintln(os.Stderr, "warning")
	case "fail":
		fmt.Fprintln(os.Stdout, "before failure")
		os.Exit(7)
	case "wait":
		fmt.Fprintln(os.Stdout, "ready")
		time.Sleep(20 * time.Second)
	case "utf8":
		for _, output := range []*os.File{os.Stdout, os.Stderr} {
			_, _ = output.Write([]byte{'a', 0xc3})
			time.Sleep(80 * time.Millisecond)
			_, _ = output.Write([]byte{0xa1, 'b', 0xff, 0xc3})
		}
	case "utf8cut":
		_, _ = os.Stdout.Write([]byte{'a', 0xc3})
		time.Sleep(80 * time.Millisecond)
		_, _ = os.Stdout.Write([]byte{0xa1, 'b'})
	case "flood":
		var wg sync.WaitGroup
		for _, output := range []*os.File{os.Stdout, os.Stderr} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 128; i++ {
					fmt.Fprint(output, strings.Repeat("x", 8192))
				}
			}()
		}
		wg.Wait()
	case "touch":
		if err := os.WriteFile(args[2], []byte("spawned"), 0600); err != nil {
			os.Exit(3)
		}
	case "tree", "treeexit":
		child := exec.Command(os.Args[0], "-test.run=^TestShellHelperProcess$", "--", "heartbeat", args[2])
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(4)
		}
		if args[1] == "treeexit" {
			deadline := time.Now().Add(2 * time.Second)
			for {
				if _, err := os.Stat(args[2]); err == nil {
					break
				}
				if time.Now().After(deadline) {
					os.Exit(8)
				}
				time.Sleep(10 * time.Millisecond)
			}
		} else {
			_ = child.Wait()
		}
	case "heartbeat":
		ignoreShellTestTermination()
		for {
			if err := os.WriteFile(args[2], []byte(time.Now().String()), 0600); err != nil {
				os.Exit(5)
			}
			fmt.Fprintln(os.Stdout, "heartbeat")
			time.Sleep(20 * time.Millisecond)
		}
	default:
		os.Exit(6)
	}
	os.Exit(0)
}

func TestShellCallbackPanicIsContained(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	result, err := NewShellTool(ShellConfig{CWD: t.TempDir()}).Execute(ctx, shellTestArgs(t, shellTestCommand(t, "wait"), 0), func(context.Context, agentcore.ToolUpdate) { panic("private-command-value") })
	if !errors.Is(err, ErrShellCallback) || strings.Contains(err.Error(), "private-command-value") {
		t.Fatalf("unsafe callback error: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("callback panic did not cancel the child promptly")
	}
	text, _ := shellTestResult(t, result)
	if !strings.Contains(text, "ready") {
		t.Fatalf("lost output before panic: %q", text)
	}
}

func TestShellCallbackCooperatesWithCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	tool := NewShellTool(ShellConfig{CWD: t.TempDir()})
	args := shellTestArgs(t, shellTestCommand(t, "flood"), 0)
	go func() {
		_, err := tool.Execute(ctx, args, func(effective context.Context, _ agentcore.ToolUpdate) {
			once.Do(func() { close(entered) })
			<-effective.Done()
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("callback not entered")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("bad cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cooperative callback blocked return")
	}
}

func TestShellCallbackReceivesEffectiveContext(t *testing.T) {
	for _, kind := range []string{"default timeout", "timeout override", "parent cancellation"} {
		t.Run(kind, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			config := ShellConfig{CWD: t.TempDir(), DefaultTimeout: 200 * time.Millisecond}
			timeout := 0
			if kind == "timeout override" {
				config.DefaultTimeout = 5 * time.Second
				timeout = 200
			}
			var sinkErr error
			started := time.Now()
			result, err := NewShellTool(config).Execute(parent, shellTestArgs(t, shellTestCommand(t, "wait"), timeout), func(effective context.Context, _ agentcore.ToolUpdate) {
				if kind == "parent cancellation" {
					cancel()
				}
				select {
				case <-effective.Done():
					sinkErr = effective.Err()
				case <-time.After(time.Second):
					t.Error("effective tool deadline was not propagated to the sink")
				}
			})
			if time.Since(started) > 800*time.Millisecond {
				t.Error("cooperative sink blocked past the effective deadline")
			}
			_, details := shellTestResult(t, result)
			if kind == "parent cancellation" {
				if !errors.Is(err, context.Canceled) || !errors.Is(sinkErr, context.Canceled) || !details.Cancelled {
					t.Fatalf("parent cancellation not shared: %v %v %+v", err, sinkErr, details)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(sinkErr, context.DeadlineExceeded) || !details.TimedOut {
				t.Fatalf("tool deadline not shared: %v %v %+v", err, sinkErr, details)
			}
		})
	}
}

func TestShellUTF8IncrementalDecoding(t *testing.T) {
	var stdout, stderr string
	result, err := NewShellTool(ShellConfig{CWD: t.TempDir()}).Execute(context.Background(), shellTestArgs(t, shellTestCommand(t, "utf8"), 0), func(_ context.Context, update agentcore.ToolUpdate) {
		if !utf8.ValidString(update.Text) {
			t.Errorf("invalid UTF-8 update: %q", update.Text)
		}
		if update.Stream == "stdout" {
			stdout += update.Text
		} else if update.Stream == "stderr" {
			stderr += update.Text
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "aáb��"
	if stdout != want || stderr != want {
		t.Fatalf("decoded streams: stdout=%q stderr=%q; want %q", stdout, stderr, want)
	}
	text, details := shellTestResult(t, result)
	if text != want+"\n[stderr]\n"+want || !utf8.ValidString(text) {
		t.Fatalf("bad decoded content %q", text)
	}
	if details.StdoutBytes != 6 || details.StderrBytes != 6 {
		t.Fatalf("lost raw counters: %+v", details)
	}
}

func TestShellUTF8BudgetDoesNotSplitRune(t *testing.T) {
	var output string
	result, err := NewShellTool(ShellConfig{CWD: t.TempDir(), MaxOutputBytes: 2}).Execute(context.Background(), shellTestArgs(t, shellTestCommand(t, "utf8cut"), 0), func(_ context.Context, update agentcore.ToolUpdate) {
		if !utf8.ValidString(update.Text) {
			t.Errorf("invalid update %q", update.Text)
		}
		if update.Stream == "stdout" {
			output += update.Text
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	text, details := shellTestResult(t, result)
	if output != "a" || text != "a"+shellTruncation || !details.Truncated || details.StdoutBytes != 4 {
		t.Fatalf("rune budget: output=%q text=%q details=%+v", output, text, details)
	}
}

func shellTestCommand(t *testing.T, mode string, args ...string) string {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parts := append([]string{binary, "-test.run=^TestShellHelperProcess$", "--", mode}, args...)
	for i, s := range parts {
		if runtime.GOOS == "windows" {
			parts[i] = "'" + strings.ReplaceAll(s, "'", "''") + "'"
		} else {
			parts[i] = "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
		}
	}
	command := strings.Join(parts, " ")
	// Commands do not inherit the test environment, so the helper opts in inline.
	if runtime.GOOS == "windows" {
		return "$env:GO_WANT_HELPER_PROCESS='1'; $env:GORACE='atexit_sleep_ms=0'; & " + command + "; exit $LASTEXITCODE"
	}
	return "GO_WANT_HELPER_PROCESS=1 GORACE=atexit_sleep_ms=0 " + command
}

func TestShellDoesNotInheritHarnessEnvironment(t *testing.T) {
	t.Setenv("HARFLEX_TEST_SECRET", "secret-canary-321")
	command := `printf '%s|%s' "${HARFLEX_TEST_SECRET:-absent}" "${PATH:+path}"`
	if runtime.GOOS == "windows" {
		command = `Write-Output ("{0}|{1}" -f $(if ($env:HARFLEX_TEST_SECRET) { $env:HARFLEX_TEST_SECRET } else { 'absent' }), $(if ($env:PATH) { 'path' }))`
	}
	result, err := NewShellTool(ShellConfig{CWD: t.TempDir()}).Execute(context.Background(), shellTestArgs(t, command, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := shellTestResult(t, result); strings.TrimSpace(text) != "absent|path" {
		t.Fatalf("output=%q", text)
	}
}

func TestShellEnvironmentKeepsOnlySystemVariables(t *testing.T) {
	source := []string{"PATH=/bin", "OPENAI_API_KEY=secret", "HOME=/home/a", "HOME=/home/b", "malformed", "Path=C:\\bin", "AWS_SESSION_TOKEN=secret"}
	if got := shellEnvironment(source, "linux"); strings.Join(got, ",") != "PATH=/bin,HOME=/home/b" {
		t.Fatalf("linux=%v", got)
	}
	if got := shellEnvironment(source, "windows"); strings.Join(got, ",") != "Path=C:\\bin,HOME=/home/b" {
		t.Fatalf("windows=%v", got)
	}
}

func TestShellExplicitEnvironmentReplacesInheritance(t *testing.T) {
	t.Setenv("HARFLEX_ENV_OVERRIDE", "parent")
	t.Setenv("XDG_STATE_HOME", "harflex-parent-canary")
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"empty", []string{}, "absent|absent"},
		{"override", []string{"HARFLEX_ENV_OVERRIDE=child"}, "child|absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := `printf '%s|%s' "${HARFLEX_ENV_OVERRIDE:-absent}" "${XDG_STATE_HOME:-absent}"`
			if runtime.GOOS == "windows" {
				command = `Write-Output ("{0}|{1}" -f $(if ($env:HARFLEX_ENV_OVERRIDE) { $env:HARFLEX_ENV_OVERRIDE } else { 'absent' }), $(if ($env:XDG_STATE_HOME) { $env:XDG_STATE_HOME } else { 'absent' }))`
			}
			result, err := NewShellTool(ShellConfig{CWD: t.TempDir(), Env: tc.env}).Execute(t.Context(), shellTestArgs(t, command, 0), nil)
			if err != nil {
				t.Fatal(err)
			}
			if output, _ := shellTestResult(t, result); strings.TrimSpace(output) != tc.want {
				t.Fatalf("output=%q want=%q", output, tc.want)
			}
		})
	}
}

// enableShellTestHelper lets helper processes started directly by a test, not
// through the shell tool, find their mode in the inherited environment.
func enableShellTestHelper(t *testing.T) {
	t.Helper()
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
}

func shellTestArgs(t *testing.T, command string, timeout int) json.RawMessage {
	t.Helper()
	params := map[string]any{"command": command}
	if timeout != 0 {
		params["timeoutMs"] = timeout
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type shellTestDetails struct {
	ExitCode      int    `json:"exitCode"`
	TimedOut      bool   `json:"timedOut"`
	Cancelled     bool   `json:"cancelled"`
	Truncated     bool   `json:"truncated"`
	DurationMs    int64  `json:"durationMs"`
	StdoutBytes   int64  `json:"stdoutBytes"`
	StderrBytes   int64  `json:"stderrBytes"`
	TimeoutSource string `json:"timeoutSource"`
}

func shellTestResult(t *testing.T, result agentcore.ToolExecutionResult) (string, shellTestDetails) {
	t.Helper()
	var content struct {
		Text string `json:"text"`
	}
	var details shellTestDetails
	if err := json.Unmarshal(result.Content, &content); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result.Details, &details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Details), "command") {
		t.Fatal("details leaked command")
	}
	return content.Text, details
}

func TestShellStreamsAndSpec(t *testing.T) {
	tool := NewShellTool(ShellConfig{CWD: t.TempDir()})
	name := "bash"
	if runtime.GOOS == "windows" {
		name = "powershell"
	}
	if tool.Spec().Name != name || tool.Risk() != security.Shell {
		t.Fatalf("unexpected spec/risk: %+v %v", tool.Spec(), tool.Risk())
	}
	var updates []agentcore.ToolUpdate
	result, err := tool.Execute(context.Background(), shellTestArgs(t, shellTestCommand(t, "stream"), 0), func(_ context.Context, update agentcore.ToolUpdate) { updates = append(updates, update) })
	if err != nil {
		t.Fatal(err)
	}
	text, details := shellTestResult(t, result)
	if !strings.Contains(text, "first\nsecond\n") || !strings.Contains(text, "[stderr]") || !strings.Contains(text, "warning") {
		t.Fatalf("missing output: %q", text)
	}
	var stdout, stderr string
	stdoutChunks := 0
	for _, update := range updates {
		switch update.Stream {
		case "stdout":
			stdout += update.Text
			stdoutChunks++
		case "stderr":
			stderr += update.Text
		default:
			t.Fatalf("unexpected stream %q", update.Stream)
		}
	}
	if stdout != "first\nsecond\n" || stderr != "warning\n" {
		t.Fatalf("updates missing output: %#v", updates)
	}
	if stdoutChunks < 2 {
		t.Fatalf("stdout was not streamed in separate chunks: %#v", updates)
	}
	if details.ExitCode != 0 || details.StdoutBytes != 13 || details.StderrBytes != 8 || details.DurationMs < 50 {
		t.Fatalf("bad details: %+v", details)
	}
}

func TestShellExitErrorPreservesOutput(t *testing.T) {
	result, err := NewShellTool(ShellConfig{CWD: t.TempDir()}).Execute(context.Background(), shellTestArgs(t, shellTestCommand(t, "fail"), 0), nil)
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("missing exit error: %v", err)
	}
	text, details := shellTestResult(t, result)
	if details.ExitCode != 7 || !strings.Contains(text, "before failure") {
		t.Fatalf("lost failure details: %q %+v", text, details)
	}
}

func TestShellTimeoutAndCancellation(t *testing.T) {
	for _, kind := range []string{"default", "override", "cancel", "parent deadline"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			config := ShellConfig{CWD: t.TempDir(), DefaultTimeout: 200 * time.Millisecond}
			override := 0
			if kind == "override" {
				config.DefaultTimeout = 5 * time.Second
				override = 200
			}
			if kind == "parent deadline" {
				var done context.CancelFunc
				ctx, done = context.WithTimeout(ctx, 200*time.Millisecond)
				defer done()
				config.DefaultTimeout = 5 * time.Second
			}
			started := time.Now()
			result, err := NewShellTool(config).Execute(ctx, shellTestArgs(t, shellTestCommand(t, "wait"), override), func(_ context.Context, update agentcore.ToolUpdate) {
				if kind == "cancel" && strings.Contains(update.Text, "ready") {
					cancel()
				}
			})
			if time.Since(started) > 2*time.Second {
				t.Fatal("cancellation exceeded bound")
			}
			_, details := shellTestResult(t, result)
			if kind == "cancel" {
				if !errors.Is(err, context.Canceled) || !details.Cancelled || details.TimedOut {
					t.Fatalf("bad cancellation: %v %+v", err, details)
				}
			} else {
				if !errors.Is(err, context.DeadlineExceeded) || !details.TimedOut || details.Cancelled {
					t.Fatalf("bad timeout: %v %+v", err, details)
				}
				wantSource := "tool"
				if kind == "parent deadline" {
					wantSource = "parent"
				}
				if details.TimeoutSource != wantSource {
					t.Fatalf("timeout source: got %q, want %q", details.TimeoutSource, wantSource)
				}
			}
		})
	}
}

func TestShellOutputBudgetDrainsBothStreams(t *testing.T) {
	const budget = 1024
	var streamed, truncations int
	result, err := NewShellTool(ShellConfig{CWD: t.TempDir(), MaxOutputBytes: budget, DefaultTimeout: 5 * time.Second}).Execute(context.Background(), shellTestArgs(t, shellTestCommand(t, "flood"), 0), func(_ context.Context, update agentcore.ToolUpdate) {
		if update.Stream == "system" {
			truncations++
			return
		}
		streamed += len(update.Text)
	})
	if err != nil {
		t.Fatal(err)
	}
	text, details := shellTestResult(t, result)
	if !details.Truncated || !strings.Contains(text, "truncated") || len(text) > budget+100 {
		t.Fatalf("unbounded result: len=%d %+v", len(text), details)
	}
	if details.StdoutBytes != 1024*1024 || details.StderrBytes != 1024*1024 {
		t.Fatalf("incorrect drained counts: %+v", details)
	}
	if streamed != budget || truncations != 1 {
		t.Fatalf("stream budget: %d bytes, %d truncations", streamed, truncations)
	}
}

func TestShellRejectsParametersBeforeSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawn-marker")
	command := shellTestCommand(t, "touch", marker)
	valid := string(shellTestArgs(t, command, 0))
	for _, args := range []string{"", `null`, `[]`, `{}`, `{"command":""}`, `{"command":"  "}`, `{"command":42}`, `{"command":"test","timeoutMs":null}`, strings.TrimSuffix(valid, "}") + `,"unknown":true}`, valid + ` {}`, strings.TrimSuffix(valid, "}") + `,"timeoutMs":0}`, strings.TrimSuffix(valid, "}") + `,"timeoutMs":-1}`, strings.TrimSuffix(valid, "}") + `,"timeoutMs":9223372036854775807}`} {
		_, err := NewShellTool(ShellConfig{CWD: t.TempDir()}).Execute(context.Background(), json.RawMessage(args), nil)
		if err == nil {
			t.Errorf("accepted invalid arguments: %s", args)
		}
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid parameters spawned process: %v", err)
	}
}

func TestShellInvalidWorkingDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{"", filepath.Join(t.TempDir(), "missing"), file} {
		_, err := NewShellTool(ShellConfig{CWD: cwd}).Execute(context.Background(), shellTestArgs(t, shellTestCommand(t, "stream"), 0), nil)
		if err == nil {
			t.Errorf("accepted CWD %q", cwd)
		}
	}
}

func TestShellExitRetiresBackgroundDescendant(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "heartbeat")
	result, err := NewShellTool(ShellConfig{CWD: t.TempDir(), DefaultTimeout: 3 * time.Second}).Execute(context.Background(), shellTestArgs(t, shellTestCommand(t, "treeexit", marker), 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, details := shellTestResult(t, result)
	if details.ExitCode != 0 || details.TimedOut {
		t.Fatalf("unexpected result: %+v", details)
	}
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("background descendant survived normal shell exit")
	}
}

func TestShellCancellationTerminatesDescendants(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "heartbeat")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := NewShellTool(ShellConfig{CWD: t.TempDir(), DefaultTimeout: 3 * time.Second}).Execute(ctx, shellTestArgs(t, shellTestCommand(t, "tree", marker), 0), func(_ context.Context, update agentcore.ToolUpdate) {
		if strings.Contains(update.Text, "heartbeat") {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	_, details := shellTestResult(t, result)
	if !details.Cancelled {
		t.Fatalf("not cancelled: %+v", details)
	}
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("descendant survived cancellation")
	}
}
