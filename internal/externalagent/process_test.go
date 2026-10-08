package externalagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/events"
)

func TestDefaultCLIExecutablePrefersRealHomebrewBinaryOnMac(t *testing.T) {
	if got := executableCandidates("codex", "darwin", "", "/Users/test"); !reflect.DeepEqual(got, []string{"/opt/homebrew/bin/codex", "/Users/test/.local/bin/codex", "codex"}) {
		t.Fatalf("macOS Codex candidates: %v", got)
	}
	if got := executableCandidates("codex", "windows", "", `C:\Users\test`); !reflect.DeepEqual(got, []string{"codex"}) {
		t.Fatalf("Windows Codex candidates: %v", got)
	}
	if got := executableCandidates("opencode", "darwin", "", "/Users/test"); !reflect.DeepEqual(got, []string{"/opt/homebrew/bin/opencode", "/Users/test/.local/bin/opencode", "opencode"}) {
		t.Fatalf("macOS OpenCode candidates: %v", got)
	}
	if got := executableCandidates("codex", "darwin", "/custom/codex", "/Users/test"); !reflect.DeepEqual(got, []string{"/custom/codex"}) {
		t.Fatalf("explicit Codex path overridden: %v", got)
	}
}

func TestMain(m *testing.M) {
	if name := filepath.Base(os.Args[0]); strings.HasPrefix(name, "harflex-helper-") {
		mode := strings.TrimSuffix(strings.TrimPrefix(name, "harflex-helper-"), ".exe")
		if len(os.Args) > 1 && os.Args[1] == "--harflex-child" {
			mode = "marker"
		}
		if (mode == "echo" || mode == "env-dump") && len(os.Args) == 2 && os.Args[1] == "--version" && os.Getenv("OPENCODE_CONFIG_DIR") != "" {
			fmt.Println("1.14.27")
			syscall.Exit(0)
		}
		marker, _ := os.ReadFile(filepath.Join(filepath.Dir(os.Args[0]), "marker-path"))
		switch mode {
		case "codex-catalog-paged", "codex-catalog-hang", "codex-catalog-request", "codex-catalog-large", "codex-catalog-context-missing", "codex-catalog-no-account":
			runCodexCatalogHelper(mode)
		case "opencode-catalog-lines", "opencode-catalog-invalid", "opencode-catalog-hang", "opencode-catalog-many", "opencode-catalog-overflow", "opencode-catalog-probe-fail":
			runOpenCodeCatalogHelper(mode)
		case "claude-auth-in", "claude-auth-out", "claude-auth-garbage", "claude-run", "claude-document", "claude-document-tools", "claude-document-logout":
			runClaudeHelper(mode)
		case "codex-protocol", "opencode-protocol":
			if mode == "opencode-protocol" && len(os.Args) == 2 && os.Args[1] == "--version" {
				fmt.Println("1.14.27")
				break
			}
			name := "codex-exec"
			if mode == "opencode-protocol" {
				name = "opencode-run"
			}
			data, err := protocolFixtures.ReadFile("testdata/" + name + ".jsonl")
			if err != nil {
				syscall.Exit(8)
			}
			os.Stdout.Write(data)
			args, _ := json.Marshal(map[string]any{"args": os.Args[1:]})
			fmt.Println(string(args))
		case "echo":
			input, _ := io.ReadAll(os.Stdin)
			cwd, _ := os.Getwd()
			b, _ := json.Marshal(map[string]any{"args": os.Args[1:], "stdin": string(input), "cwd": cwd})
			fmt.Println(string(b))
		case "output":
			fmt.Print("{\"type\":\"delta\",\"text\":\"hello\"}\n {\"unknown\": 1} \nplain\n")
			os.Stdout.Write([]byte{'b', 0xff, 'd', '\n'})
			fmt.Print("final")
		case "fail":
			fmt.Println(`{"type":"delta","text":"before"}`)
			fmt.Fprintln(os.Stderr, "harmless diagnostic; private prompt; "+os.Getenv("TEST_API_KEY"))
			syscall.Exit(7)
		case "large":
			fmt.Print(strings.Repeat("x", 1024*1024+1))
		case "stderr":
			fmt.Fprint(os.Stderr, strings.Repeat("x", 100000))
			syscall.Exit(9)
		case "stream":
			for {
				fmt.Println(`{"type":"delta","text":"x"}`)
			}
		case "wait":
			time.Sleep(time.Minute)
		case "version":
			fmt.Print("helper 1.2.3")
		case "version-large":
			fmt.Print(strings.Repeat("v", 9000))
		case "crlf":
			fmt.Print(" {\"unknown\": true}\r\n")
		case "environment":
			if os.Getenv("TEST_API_KEY") != "" || os.Getenv("TEST_PROMPT") != "" {
				syscall.Exit(8)
			}
			fmt.Print("helper clean")
		case "env-dump":
			data, _ := json.Marshal(map[string]string{"secret": os.Getenv("HARFLEX_OTHER_API_KEY"), "unlisted": os.Getenv("HARFLEX_OTHER_PLAIN"), "path": os.Getenv("PATH"), "home": os.Getenv("HOME"), "userprofile": os.Getenv("USERPROFILE")})
			fmt.Println(string(data))
			fmt.Fprintln(os.Stderr, string(data))
			if len(os.Args) > 1 && os.Args[1] != "--version" && os.Getenv("OPENCODE_CONFIG_DIR") == "" {
				syscall.Exit(7)
			}
		case "tree", "orphan":
			child := exec.Command(os.Args[0], "--harflex-child")
			child.Env = os.Environ()
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr
			if err := child.Start(); err != nil {
				syscall.Exit(8)
			}
			fmt.Printf("{\"type\":\"child\",\"text\":\"%d\"}\n", child.Process.Pid)
			if mode == "tree" {
				time.Sleep(time.Minute)
			}
		case "marker":
			time.Sleep(600 * time.Millisecond)
			os.WriteFile(string(marker), []byte("escaped"), 0600)
			time.Sleep(time.Minute)
		case "burst":
			for range 1000 {
				fmt.Printf("{\"type\":\"delta\",\"text\":\"%s\"}\n", strings.Repeat("x", 100))
			}
			os.WriteFile(string(marker), []byte("done"), 0600)
		}
		// Exit the helper directly; race-runtime shutdown delays would change
		// descendant timing. The parent test process still runs full race checks.
		syscall.Exit(0)
	}
	os.Exit(m.Run())
}

func TestAdaptersUseExplicitEnvironment(t *testing.T) {
	t.Setenv("HARFLEX_OTHER_API_KEY", "canary-other-account")
	t.Setenv("HARFLEX_OTHER_PLAIN", "unlisted-value")
	for _, factory := range []func(string) Adapter{NewCodex, NewOpenCode} {
		a := factory(helper(t, "env-dump"))
		es, err := collect(t, context.Background(), a, Request{CWD: t.TempDir(), Prompt: "go"})
		if len(es) != 1 || err == nil {
			t.Fatalf("unexpected run %v %v", es, err)
		}
		var got map[string]string
		if err := json.Unmarshal(es[0].Raw, &got); err != nil {
			t.Fatal(err)
		}
		if got["secret"] != "" || got["unlisted"] != "" || strings.Contains(err.Error(), "canary-other-account") {
			t.Fatal("run inherited unrelated environment")
		}
		if got["path"] != os.Getenv("PATH") || got["home"] != os.Getenv("HOME") || got["userprofile"] != os.Getenv("USERPROFILE") {
			t.Fatal("missing required environment")
		}
		d := a.Detect()
		if !d.Available || strings.Contains(d.Version, "canary-other-account") || strings.Contains(d.Version, "unlisted-value") {
			t.Fatalf("unsafe detection environment: %+v", d)
		}
	}
}

func TestRedactionNormalizesBeforeLongestMatch(t *testing.T) {
	for _, input := range []string{"sk-123456", "s\x00k-123456"} {
		if got := sanitizeDiagnostic(input, Request{Prompt: "sk", Model: "sk-123456"}, false); got != "[REDACTED]" {
			t.Fatalf("leaked overlapping candidate: %q", got)
		}
	}
	if got := sanitizeDiagnostic("secret", Request{Prompt: "s\x00ecret"}, false); got != "[REDACTED]" {
		t.Fatalf("candidate not normalized: %q", got)
	}
	for _, input := range []string{"sk-123456", "s\x00k-123456", "sk-123\n456"} {
		if got := sanitizeDiagnostic(input, Request{Prompt: "sk"}, false, "sk-123456", "sk-123456", ""); got != "[REDACTED]" {
			t.Fatalf("explicit sensitive candidate leaked: %q", got)
		}
	}
	if got := sanitizeDiagnostic("secret", Request{Prompt: "[RED"}, false, "secret"); got != "[REDACTED]" {
		t.Fatalf("replacement was processed recursively: %q", got)
	}
}

func TestRedactionUnionsEverySensitiveOverlap(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		candidates       []string
	}{
		{"crossing", "prefixsk-123456", "[REDACTED]", []string{"prefixsk", "sk-123456"}},
		{"prefix", "sk-123456 tail", "[REDACTED] tail", []string{"sk", "sk-123456"}},
		{"suffix", "head sk-123456", "head [REDACTED]", []string{"123456", "sk-123456"}},
		{"same offset", "abcde", "[REDACTED]", []string{"abc", "abcde"}},
		{"duplicates", "secret", "[REDACTED]", []string{"secret", "secret", ""}},
		{"self overlapping", "aaaaa", "[REDACTED]", []string{"aaa"}},
		{"controls", "pre\x00fixsk-123\n456", "[REDACTED]", []string{"prefix\tsk", "sk-123456"}},
		{"utf8", "before çãõsecret after", "before [REDACTED] after", []string{"çãõs", "õsecret"}},
		{"no match", "harmless diagnostic", "harmless diagnostic", []string{"secret", "token"}},
		{"no complete match", "secret-pa", "secret-pa", []string{"secret-password"}},
		{"empty", "harmless diagnostic", "harmless diagnostic", []string{"", "\x00"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeDiagnostic(tc.text, Request{}, false, tc.candidates...); got != tc.want {
				t.Fatalf("redaction=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestRedactionProtectsTruncatedSensitiveSuffix(t *testing.T) {
	if got := sanitizeDiagnostic("safe secret-pa", Request{}, true, "secret-password"); got != "safe [REDACTED]" {
		t.Fatalf("truncated candidate leaked: %q", got)
	}
}

func TestRedactionTruncatedUTF8SensitiveSuffix(t *testing.T) {
	for _, suffix := range []string{"ç", "€", "😀"} {
		for cut := 1; cut < len(suffix); cut++ {
			t.Run(fmt.Sprintf("%U/%d", []rune(suffix)[0], cut), func(t *testing.T) {
				candidate := "conteúdo confidencial " + suffix
				text := "safe conteúdo confidencial " + suffix[:cut]
				got := sanitizeDiagnostic(text, Request{}, true, candidate)
				if got != "safe [REDACTED]" || !utf8.ValidString(got) {
					t.Fatalf("truncated sensitive UTF-8 leaked: %q", got)
				}
			})
		}
	}
}

func TestRedactionKeepsCompleteUTF8AndNormalizesInteriorInvalidity(t *testing.T) {
	for _, tc := range []struct {
		text, candidate, want string
		truncated             bool
	}{
		{"public ç", "secret", "public ç", true},
		{"public €", "secret", "public €", true},
		{"public 😀", "secret", "public 😀", true},
		{"safe classified pass", "classified password", "safe [REDACTED]", true},
		{"public \xff ordinary", "secret", "public � ordinary", true},
		{"public \xc3", "secret", "public �", false},
		{"public \xff", "secret", "public �", true},
	} {
		got := sanitizeDiagnostic(tc.text, Request{}, tc.truncated, tc.candidate)
		if got != tc.want || !utf8.ValidString(got) {
			t.Fatalf("redaction=%q want=%q", got, tc.want)
		}
	}
}

func TestBuildEnvironmentAllowlistAndDeduplication(t *testing.T) {
	for _, tc := range []struct {
		name, adapter, goos string
		source, want        []string
	}{
		{"unix codex", "codex", "linux", []string{"PATH=/first", "HOME=/home/test", "PATH=/last", "CODEX_HOME=/config/codex", "HARFLEX_OTHER_API_KEY=blocked", "HARFLEX_OTHER_PLAIN=blocked", "OPENAI_API_KEY=blocked", "OPENCODE_CONFIG=blocked", "NO_COLOR=0", "broken"}, []string{"PATH=/last", "HOME=/home/test", "CODEX_HOME=/config/codex", "NO_COLOR=1"}},
		{"opencode config", "opencode", "darwin", []string{"HOME=/home/test", "CODEX_HOME=/config/codex", "XDG_CONFIG_HOME=/config", "XDG_STATE_HOME=/state", "TMPDIR="}, []string{"HOME=/home/test", "XDG_CONFIG_HOME=/config", "XDG_STATE_HOME=/state", "TMPDIR=", "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_LSP_DOWNLOAD=1", "OPENCODE_DISABLE_MODELS_FETCH=1", "NO_COLOR=1"}},
		{"windows case", "codex", "windows", []string{"Path=C:\\first", "PATH=C:\\last", "UserProfile=C:\\user", "USERPROFILE=C:\\other", "systemroot=C:\\Windows", "COMSPEC=C:\\Windows\\cmd.exe", "codex_home=C:\\codex", "secret=blocked", "No_Color=0"}, []string{"PATH=C:\\last", "USERPROFILE=C:\\other", "systemroot=C:\\Windows", "COMSPEC=C:\\Windows\\cmd.exe", "codex_home=C:\\codex", "NO_COLOR=1"}},
		{"empty", "codex", "linux", nil, []string{"NO_COLOR=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildEnvironment(tc.adapter, tc.source, tc.goos); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("environment=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestSlowConsumerPreservesBufferedOutputAfterExit(t *testing.T) {
	marker := t.TempDir() + "/done"
	t.Setenv("HARFLEX_EXTERNAL_MARKER", marker)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	es, errs := NewCodex(helper(t, "burst")).Run(ctx, Request{CWD: t.TempDir(), Prompt: "go"})
	count := 0
	paused := false
	for range es {
		count++
		if !paused {
			if _, err := os.Stat(marker); err == nil {
				paused = true
				time.Sleep(400 * time.Millisecond)
			} else {
				time.Sleep(time.Millisecond)
			}
		}
	}
	var runErr error
	for err := range errs {
		runErr = errors.Join(runErr, err)
	}
	if count != 1000 || runErr != nil {
		t.Fatalf("lost buffered output: %d events, error %v", count, runErr)
	}
}

func TestRawPreservesCarriageReturn(t *testing.T) {
	es, err := collect(t, context.Background(), NewCodex(helper(t, "crlf")), Request{CWD: t.TempDir(), Prompt: "go"})
	if err != nil || len(es) != 1 || string(es[0].Raw) != " {\"unknown\": true}\r" {
		t.Fatalf("raw changed: %+v %v", es, err)
	}
}
func TestPromptCannotBecomeCLIFlag(t *testing.T) {
	es, err := collect(t, context.Background(), NewOpenCode(helper(t, "echo")), Request{CWD: t.TempDir(), Prompt: "--dangerously-skip-permissions"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Args []string }
	json.Unmarshal(es[0].Raw, &got)
	if len(got.Args) < 2 || got.Args[len(got.Args)-2] != "--" {
		t.Fatalf("prompt can become CLI flag: %v", got.Args)
	}
}

func TestOptionValuesCannotBecomeCLIFlags(t *testing.T) {
	a := NewOpenCode(helper(t, "echo"))
	cwd := t.TempDir()
	for _, req := range []Request{{CWD: cwd, Prompt: "go", Model: "--dangerously-skip-permissions"}, {CWD: cwd, Prompt: "go", SessionID: "--share"}} {
		es, err := collect(t, context.Background(), a, req)
		if err == nil || len(es) != 0 {
			t.Fatalf("option flag accepted: %+v", req)
		}
	}
}
func TestDetectionRemovesSensitiveEnvironment(t *testing.T) {
	t.Setenv("TEST_API_KEY", "test-secret")
	t.Setenv("TEST_PROMPT", "private prompt")
	if !NewCodex(helper(t, "environment")).Detect().Available {
		t.Fatal("detection inherited sensitive environment")
	}
}
func TestDescendantsCannotOutliveRun(t *testing.T) {
	for _, mode := range []string{"tree", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			marker := t.TempDir() + "/child-marker"
			t.Setenv("HARFLEX_EXTERNAL_MARKER", marker)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			es, errs := NewCodex(helper(t, mode)).Run(ctx, Request{CWD: t.TempDir(), Prompt: "go"})
			select {
			case <-es:
			case <-time.After(3 * time.Second):
				t.Fatal("no child readiness")
			}
			if mode == "tree" {
				cancel()
			}
			for range es {
			}
			for range errs {
			}
			time.Sleep(800 * time.Millisecond)
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("descendant escaped: %v", err)
			}
		})
	}
}

func helper(t *testing.T, mode string) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "harflex-helper-"+mode+".exe")
	if err := os.Link(path, target); err != nil {
		in, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "marker-path"), []byte(os.Getenv("HARFLEX_EXTERNAL_MARKER")), 0600); err != nil {
		t.Fatal(err)
	}
	return target
}
func collect(t *testing.T, ctx context.Context, a Adapter, r Request) ([]Event, error) {
	t.Helper()
	es, errs := a.Run(ctx, r)
	var out []Event
	for e := range es {
		out = append(out, e)
	}
	var err error
	for e := range errs {
		err = errors.Join(err, e)
	}
	return out, err
}

func TestAdapterCommands(t *testing.T) {
	for _, id := range []string{"codex", "opencode"} {
		t.Run(id, func(t *testing.T) {
			path := helper(t, "echo")
			cwd := t.TempDir()
			req := Request{Prompt: "private prompt", CWD: cwd, Model: "test-model"}
			var a Adapter = NewCodex(path)
			want := []string{"exec", "--json", "--color", "never", "--sandbox", "workspace-write", "-C", cwd, "--skip-git-repo-check", "--model", "test-model", "-"}
			stdin := req.Prompt
			if id == "opencode" {
				a = NewOpenCode(path)
				req.SessionID = "session-1"
				want = []string{"--pure", "run", "--format", "json", "--dir", cwd, "--model", "test-model", "--session", "session-1", req.Prompt}
				stdin = ""
			}
			es, err := collect(t, context.Background(), a, req)
			if err != nil || len(es) != 1 {
				t.Fatalf("events=%v err=%v", es, err)
			}
			var got struct {
				Args       []string
				Stdin, CWD string
			}
			if err := json.Unmarshal(es[0].Raw, &got); err != nil {
				t.Fatal(err)
			}
			actualDir, err := os.Stat(got.CWD)
			if err != nil {
				t.Fatal(err)
			}
			requestedDir, err := os.Stat(cwd)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Args, want) || got.Stdin != stdin || !os.SameFile(actualDir, requestedDir) {
				t.Fatalf("command mismatch: %+v", got)
			}
			caps := a.Capabilities()
			if a.ID() != id || !caps.Streaming || caps.ToolCalls || caps.Images || caps.Reasoning || !caps.Resumable {
				t.Fatalf("capabilities=%+v", caps)
			}
		})
	}
}

func TestCodexCommandArgumentsKeepSandboxAndCWDBeforeResume(t *testing.T) {
	cwd := t.TempDir()
	build := NewCodex("").(*cliAdapter).build

	firstRun, input := build(Request{CWD: cwd, Prompt: "go", Model: "runtime-model", ReasoningEffort: "high"})
	wantFirstRun := []string{"exec", "--json", "--color", "never", "--sandbox", "workspace-write", "-C", cwd, "--skip-git-repo-check", "--model", "runtime-model", "-c", "model_reasoning_effort=high", "-"}
	if !reflect.DeepEqual(firstRun, wantFirstRun) || input != "go" {
		t.Fatalf("Codex first-run args=%v input=%q want=%v", firstRun, input, wantFirstRun)
	}

	resume, input := build(Request{CWD: cwd, Prompt: "continue", Model: "runtime-model", ReasoningEffort: "high", SessionID: "thread_synthetic"})
	wantResume := []string{"exec", "--json", "--color", "never", "--sandbox", "workspace-write", "-C", cwd, "--skip-git-repo-check", "--model", "runtime-model", "-c", "model_reasoning_effort=high", "resume", "thread_synthetic", "-"}
	if !reflect.DeepEqual(resume, wantResume) || input != "continue" {
		t.Fatalf("Codex resume args=%v input=%q want=%v", resume, input, wantResume)
	}
}

func TestCodexReasoningEffortIsExplicitAndOpenCodeNeverInventsVariant(t *testing.T) {
	cwd := t.TempDir()
	request := Request{CWD: cwd, Prompt: "go", Model: "runtime-model", ReasoningEffort: "high"}
	events, err := collect(t, t.Context(), NewCodex(helper(t, "echo")), request)
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	var command struct{ Args []string }
	if err := json.Unmarshal(events[0].Raw, &command); err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "--json", "--color", "never", "--sandbox", "workspace-write", "-C", cwd, "--skip-git-repo-check", "--model", "runtime-model", "-c", "model_reasoning_effort=high", "-"}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("Codex effort args=%v want=%v", command.Args, want)
	}
	events, err = collect(t, t.Context(), NewOpenCode(helper(t, "echo")), request)
	if err == nil || len(events) != 0 {
		t.Fatalf("unverified OpenCode variant admitted: %d %v", len(events), err)
	}
}
func TestNormalizationAndFinalLine(t *testing.T) {
	a := NewCodex(helper(t, "output")).(*cliAdapter)
	a.newNormalizer = nil // Generic helpers retain the root-level text fallback.
	es, err := collect(t, context.Background(), a, Request{CWD: t.TempDir(), Prompt: "go"})
	if err != nil || len(es) != 5 {
		t.Fatalf("events=%v err=%v", es, err)
	}
	if es[0].Type != "delta" || es[0].Text != "hello" || es[1].Type != "external.raw" || string(es[1].Raw) != " {\"unknown\": 1} " || es[2].Type != "external.stdout" || es[4].Text != "final" || !utf8.ValidString(es[3].Text) {
		t.Fatalf("events=%+v", es)
	}
	raw := []byte(`{"type":"delta","text":"safe"}`)
	event := normalize(raw)
	raw[2] = 'X'
	if string(event.Raw) != `{"type":"delta","text":"safe"}` {
		t.Fatal("raw aliases input")
	}
	for _, raw := range []string{`{"type":"x"}`, `{"type":12,"text":"y"}`, `{"text":"y"}`, `[1]`, `null`} {
		if e := normalize([]byte(raw)); e.Type != "external.raw" {
			t.Fatalf("unexpected normalization %+v", e)
		}
	}
}
func TestExitErrorAndSanitization(t *testing.T) {
	a := NewCodex(helper(t, "fail"))
	t.Setenv("TEST_API_KEY", "secret-value-123")
	es, err := collect(t, context.Background(), a, Request{CWD: t.TempDir(), Prompt: "private prompt"})
	var exit *exec.ExitError
	if len(es) != 1 || !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("events=%v err=%v", es, err)
	}
	if strings.Contains(err.Error(), "private prompt") || strings.Contains(err.Error(), "secret-value-123") || !strings.Contains(err.Error(), "harmless diagnostic") {
		t.Fatalf("unsafe error %v", err)
	}
}
func TestOutputLimits(t *testing.T) {
	for _, mode := range []string{"large", "stderr"} {
		t.Run(mode, func(t *testing.T) {
			a := NewCodex(helper(t, mode))
			_, err := collect(t, context.Background(), a, Request{CWD: t.TempDir(), Prompt: "go"})
			if err == nil || len(err.Error()) > 66000 {
				t.Fatalf("invalid limit error: %v", err)
			}
		})
	}
}

func TestRedactionCannotExpandDiagnosticsWithoutBound(t *testing.T) {
	_, err := collect(t, context.Background(), NewCodex(helper(t, "stderr")), Request{CWD: t.TempDir(), Prompt: "go", Model: "x"})
	if err == nil || len(err.Error()) > 66000 {
		t.Fatalf("unbounded diagnostic: %d bytes", len(err.Error()))
	}
}
func TestCancellationWithoutConsumer(t *testing.T) {
	a := NewCodex(helper(t, "stream"))
	ctx, cancel := context.WithCancel(context.Background())
	es, errs := a.Run(ctx, Request{CWD: t.TempDir(), Prompt: "go"})
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not join")
	}
	for range es {
	}
	if _, ok := <-errs; ok {
		t.Fatal("error channel not closed")
	}
}
func TestValidationAndPreCancellation(t *testing.T) {
	a := NewCodex(helper(t, "echo"))
	cwd := t.TempDir()
	for _, req := range []Request{{Prompt: "go", CWD: "relative"}, {Prompt: "go", CWD: cwd + "/absent"}, {CWD: cwd}, {Prompt: "go", CWD: cwd, SessionID: "../invalid"}} {
		es, err := collect(t, context.Background(), a, req)
		if err == nil || len(es) != 0 {
			t.Fatalf("accepted invalid request %+v", req)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	es, err := collect(t, ctx, a, Request{CWD: cwd, Prompt: "go"})
	if len(es) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("events=%v err=%v", es, err)
	}
	_, err = collect(t, context.Background(), NewCodex(cwd), Request{CWD: cwd, Prompt: "go"})
	if err == nil {
		t.Fatal("directory accepted as executable")
	}
}
func TestDetection(t *testing.T) {
	for _, mode := range []string{"version", "version-large", "wait", "fail"} {
		t.Run(mode, func(t *testing.T) {
			a := NewCodex(helper(t, mode))
			started := time.Now()
			got := a.Detect()
			if got.Available != (mode == "version") || time.Since(started) > 3*time.Second {
				t.Fatalf("detection=%+v", got)
			}
			if mode == "version" && (got.Version != "helper 1.2.3" || got.Path == "") {
				t.Fatalf("detection=%+v", got)
			}
		})
	}
	if NewOpenCode("/definitely/absent").Detect().Available {
		t.Fatal("missing binary detected")
	}
}

type recording struct {
	mu     sync.Mutex
	types  []string
	data   []any
	failAt string
}

func (r *recording) Append(_ context.Context, stream, kind, typ string, data any) (events.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if stream != "session" || kind != "external_session" {
		return events.Event{}, errors.New("wrong stream")
	}
	if typ == r.failAt {
		return events.Event{}, errors.New("recorder failed")
	}
	r.types = append(r.types, typ)
	r.data = append(r.data, data)
	return events.Event{}, nil
}
func TestSessionLifecycle(t *testing.T) {
	for _, mode := range []string{"output", "fail"} {
		t.Run(mode, func(t *testing.T) {
			a := NewCodex(helper(t, mode))
			r := &recording{}
			s := NewSession("session", a, r, Request{CWD: t.TempDir()})
			err := s.Prompt(context.Background(), "private prompt")
			end := "external.run.completed"
			if mode == "fail" {
				end = "external.run.failed"
				if err == nil {
					t.Fatal("lost run error")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if r.types[0] != "external.run.started" || r.types[1] != "message.user" || r.types[len(r.types)-1] != end || s.Cancel() {
				t.Fatalf("types=%v", r.types)
			}
			started, _ := json.Marshal(r.data[0])
			if strings.Contains(string(started), "private prompt") {
				t.Fatal("started leaked prompt")
			}
			for _, typ := range r.types[2 : len(r.types)-1] {
				if typ != "external.event" {
					t.Fatal(typ)
				}
			}
		})
	}
}

func TestSessionWorkingDirectoryFailureDoesNotAdmitRun(t *testing.T) {
	r := &recording{}
	s := NewSession("session", NewCodex(helper(t, "echo")), r, Request{})
	failure := errors.New("working directory unavailable")
	s.getwd = func() (string, error) { return "", failure }
	if err := s.Prompt(context.Background(), "go"); !errors.Is(err, failure) {
		t.Fatalf("lost preflight cause: %v", err)
	}
	if len(r.types) != 0 || s.Cancel() {
		t.Fatalf("failed preflight admitted run: %v", r.types)
	}
}

func TestSessionInvalidRequestDoesNotAdmitRun(t *testing.T) {
	for _, request := range []Request{{CWD: "relative"}, {CWD: t.TempDir(), SessionID: "../invalid"}} {
		r := &recording{}
		s := NewSession("session", NewCodex(helper(t, "echo")), r, request)
		if err := s.Prompt(context.Background(), "go"); err == nil {
			t.Fatal("invalid request accepted")
		}
		if len(r.types) != 0 || s.Cancel() {
			t.Fatalf("failed preflight admitted run: %v", r.types)
		}
	}
}
func TestSessionBusyCancelAndRecorderFailure(t *testing.T) {
	a := NewCodex(helper(t, "stream"))
	r := &recording{}
	s := NewSession("session", a, r, Request{CWD: t.TempDir()})
	if s.Cancel() {
		t.Fatal("idle cancel")
	}
	done := make(chan error, 1)
	go func() { done <- s.Prompt(context.Background(), "go") }()
	deadline := time.After(3 * time.Second)
	for {
		r.mu.Lock()
		ready := len(r.types) > 1
		r.mu.Unlock()
		if ready {
			break
		}
		select {
		case <-deadline:
			t.Fatal("not started")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := s.Prompt(context.Background(), "again"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("busy=%v", err)
	}
	if !s.Cancel() || s.Cancel() {
		t.Fatal("cancel not idempotent")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("session did not join")
	}
	if r.types[len(r.types)-1] != "external.run.cancelled" {
		t.Fatalf("types=%v", r.types)
	}
	r = &recording{failAt: "external.event"}
	s = NewSession("session", a, r, Request{CWD: t.TempDir()})
	if err := s.Prompt(context.Background(), "go"); err == nil || !strings.Contains(err.Error(), "journal unavailable") {
		t.Fatalf("recorder=%v", err)
	}
	if !reflect.DeepEqual(r.types, []string{"external.run.started", "message.user"}) {
		t.Fatalf("continued publication %v", r.types)
	}
}
