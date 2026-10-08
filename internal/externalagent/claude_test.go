package externalagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/persioflexa/harflex/internal/modelcatalog"
)

func runClaudeHelper(mode string) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("2.1.286 (Claude Code)")
		return
	}
	if os.Getenv("HARFLEX_OTHER_API_KEY") != "" || os.Getenv("DISABLE_AUTOUPDATER") != "1" {
		fmt.Fprintln(os.Stderr, "unsafe Claude Code invocation")
		syscall.Exit(9)
	}
	switch mode {
	case "claude-auth-in", "claude-auth-out", "claude-auth-garbage":
		if len(os.Args) != 3 || os.Args[1] != "auth" || os.Args[2] != "status" {
			syscall.Exit(9)
		}
		switch mode {
		case "claude-auth-in":
			fmt.Println(`{"loggedIn": true, "authMethod": "claude.ai"}`)
		case "claude-auth-out":
			fmt.Println(`{"loggedIn": false, "authMethod": "none"}`)
			syscall.Exit(1)
		default:
			fmt.Println("not json")
		}
	case "claude-document", "claude-document-tools", "claude-document-logout":
		runClaudeDocumentHelper(mode)
	case "claude-run":
		input, _ := io.ReadAll(os.Stdin)
		args, _ := json.Marshal(map[string]any{"args": os.Args[1:], "stdin": string(input)})
		fmt.Println(string(args))
		fmt.Println(`{"type":"system","subtype":"init","session_id":"0b6f3c3e-1111-4c1f-9e0a-000000000001","tools":[]}`)
		fmt.Println(`{"type":"assistant","session_id":"0b6f3c3e-1111-4c1f-9e0a-000000000001","message":{"id":"msg_1","role":"assistant","content":[{"type":"thinking","thinking":"secret plan"}]}}`)
		fmt.Println(`{"type":"assistant","session_id":"0b6f3c3e-1111-4c1f-9e0a-000000000001","message":{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"Primeira parte"}]}}`)
		fmt.Println(`{"type":"assistant","session_id":"0b6f3c3e-1111-4c1f-9e0a-000000000001","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"Edit","input":{}}]}}`)
		fmt.Println(`{"type":"user","session_id":"0b6f3c3e-1111-4c1f-9e0a-000000000001","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"ok"}]}}`)
		fmt.Println(`{"type":"assistant","session_id":"0b6f3c3e-1111-4c1f-9e0a-000000000001","message":{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"Segunda parte"}]}}`)
		fmt.Println(`{"type":"result","subtype":"success","is_error":false,"result":"Segunda parte","session_id":"0b6f3c3e-1111-4c1f-9e0a-000000000001"}`)
	}
}

func TestClaudeCodeRunsPrintModeThroughStdinAndJoinsMessageBlocks(t *testing.T) {
	t.Setenv("HARFLEX_OTHER_API_KEY", "secret-canary")
	adapter := NewClaudeCode(helper(t, "claude-run"))
	events, errs := adapter.Run(t.Context(), Request{Prompt: "--olá", CWD: t.TempDir(), Model: "sonnet", ReasoningEffort: "high", SystemPrompt: "Seja breve.", SessionID: "0b6f3c3e-1111-4c1f-9e0a-000000000000"})
	var collected []Event
	for event := range events {
		collected = append(collected, event)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	var invocation struct {
		Args  []string `json:"args"`
		Stdin string   `json:"stdin"`
	}
	if len(collected) == 0 || json.Unmarshal(collected[0].Raw, &invocation) != nil {
		t.Fatalf("missing invocation: %+v", collected)
	}
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--model", "sonnet", "--effort", "high", "--append-system-prompt", "Seja breve.", "--resume", "0b6f3c3e-1111-4c1f-9e0a-000000000000"}
	if !slices.Equal(invocation.Args, want) || invocation.Stdin != "--olá" {
		t.Fatalf("invocation: %+v", invocation)
	}
	var messages []Event
	for _, event := range collected {
		if event.Type == "assistant.message" {
			messages = append(messages, event)
		}
	}
	if len(messages) != 2 || messages[0].Text != "Primeira parte" || messages[1].Text != "Primeira parte\n\nSegunda parte" ||
		messages[1].MessageID != "msg_1" || messages[1].Mode != "replace" || messages[1].SessionID != "0b6f3c3e-1111-4c1f-9e0a-000000000001" {
		t.Fatalf("messages: %+v", messages)
	}
	for _, event := range collected {
		if strings.Contains(event.Text, "secret plan") {
			t.Fatal("thinking must never become assistant text")
		}
	}
	if last := collected[len(collected)-1]; last.SessionID != "0b6f3c3e-1111-4c1f-9e0a-000000000001" {
		t.Fatalf("result must keep the session for resume: %+v", last)
	}
}

func TestClaudeCodeRejectsUnknownEffortAndCodexPolicies(t *testing.T) {
	adapter := NewClaudeCode(helper(t, "claude-run"))
	for _, request := range []Request{
		{Prompt: "x", CWD: t.TempDir(), ReasoningEffort: "ultra"},
		{Prompt: "x", CWD: t.TempDir(), Sandbox: "read-only"},
		{Prompt: "x", CWD: t.TempDir(), Policy: "sdd_code"},
	} {
		events, errs := adapter.Run(t.Context(), request)
		for range events {
		}
		if err := <-errs; err == nil {
			t.Fatalf("request must be refused: %+v", request)
		}
	}
}

func TestClaudeCodeCatalogOffersAliasesOnlyWhenLoggedIn(t *testing.T) {
	t.Setenv("HARFLEX_OTHER_API_KEY", "secret-canary")
	query := func(mode string) modelcatalog.Result {
		cataloger := NewClaudeCode(helper(t, mode)).(interface {
			QueryModels(context.Context, string) (modelcatalog.Result, error)
		})
		result, err := cataloger.QueryModels(t.Context(), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := query("claude-auth-in")
	if result.Status != modelcatalog.StatusComplete || !result.Complete || result.Source != "claude_cli" || result.BackendID != "claude" || len(result.Models) != len(claudeCatalogModels) ||
		result.Models[0].ID != "fable" || !slices.Equal(result.Models[0].SupportedReasoningEfforts, claudeReasoningEfforts) {
		t.Fatalf("logged in: %+v", result)
	}
	if result := query("claude-auth-out"); result.Status != modelcatalog.StatusFailed || result.ErrorCode != "catalog_not_logged_in" || len(result.Models) != 0 {
		t.Fatalf("logged out: %+v", result)
	}
	if result := query("claude-auth-garbage"); result.Status != modelcatalog.StatusFailed || result.ErrorCode != "catalog_unavailable" {
		t.Fatalf("garbage: %+v", result)
	}
}

func TestClaudeDesktopCandidatesPreferTheNewestVersion(t *testing.T) {
	home := t.TempDir()
	for _, version := range []string{"2.1.99", "2.1.286", "2.1.255"} {
		dir := home + "/Library/Application Support/Claude/claude-code/" + version + "/abc/claude.app/Contents/MacOS"
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir+"/claude", []byte("x"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	found := claudeDesktopCandidates(home)
	if len(found) != 3 || claudeDesktopVersion(found[0]) != "2.1.286" || claudeDesktopVersion(found[2]) != "2.1.99" {
		t.Fatalf("candidates: %v", found)
	}
}

func runClaudeDocumentHelper(mode string) {
	args := os.Args[1:]
	flag := func(name string) (string, bool) {
		for i, arg := range args {
			if arg == name && i+1 < len(args) {
				return args[i+1], true
			}
		}
		return "", false
	}
	for _, name := range []string{"--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence", "--verbose"} {
		if !slices.Contains(args, name) {
			syscall.Exit(9)
		}
	}
	tools, toolsOK := flag("--tools")
	sources, sourcesOK := flag("--setting-sources")
	format, _ := flag("--output-format")
	model, _ := flag("--model")
	effort, _ := flag("--effort")
	schema, _ := flag("--json-schema")
	systemPath, _ := flag("--system-prompt-file")
	system, err := os.ReadFile(systemPath)
	cwd, _ := os.Getwd()
	if args[0] != "-p" || !toolsOK || tools != "" || !sourcesOK || sources != "" || format != "stream-json" || model != "sonnet" || effort != "high" ||
		!json.Valid([]byte(schema)) || err != nil || string(system) != "Escreva a SPEC.\n\n"+claudeDocumentToolInstructions || filepath.Dir(systemPath) != cwd {
		syscall.Exit(9)
	}
	// The sandbox must keep the personal instructions unread.
	if _, err := os.ReadFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "CLAUDE.md")); err == nil {
		syscall.Exit(10)
	}
	if prompt, _ := io.ReadAll(os.Stdin); string(prompt) != "Pedido do usuário" {
		syscall.Exit(11)
	}
	session := "5b2c1d1e-2222-4c1f-9e0a-000000000002"
	toolList := `["StructuredOutput"]`
	if mode == "claude-document-tools" {
		toolList = `["StructuredOutput","Bash"]`
	}
	fmt.Printf(`{"type":"system","subtype":"init","session_id":%q,"tools":%s,"mcp_servers":[]}`+"\n", session, toolList)
	if mode == "claude-document-logout" {
		fmt.Printf(`{"type":"assistant","session_id":%q,"error":"authentication_failed","message":{"id":"m0","content":[{"type":"text","text":"Not logged in"}]}}`+"\n", session)
		fmt.Printf(`{"type":"result","subtype":"success","is_error":true,"result":"Not logged in","session_id":%q}`+"\n", session)
		syscall.Exit(1)
	}
	// A refused attempt at a tool the run does not have, as models do with the Harflex tool names.
	fmt.Printf(`{"type":"assistant","session_id":%q,"message":{"id":"m0","content":[{"type":"tool_use","id":"t0","name":"write","input":{}}]}}`+"\n", session)
	fmt.Printf(`{"type":"user","session_id":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t0","is_error":true,"content":"No such tool available: write"}]}}`+"\n", session)
	fmt.Printf(`{"type":"assistant","session_id":%q,"message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"StructuredOutput","input":{}}]}}`+"\n", session)
	fmt.Printf(`{"type":"user","session_id":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`+"\n", session)
	fmt.Printf(`{"type":"result","subtype":"success","is_error":false,"result":"","structured_output":{"reply": "pronto", "toolCalls": []},"session_id":%q,"usage":{"input_tokens":10,"cache_read_input_tokens":5,"output_tokens":7}}`+"\n", session)
}

func claudeDocumentFixture(t *testing.T, mode string) (*cliAdapter, DocumentRequest) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the isolated Claude Code document contract runs under macOS sandbox-exec")
	}
	config := t.TempDir()
	if err := os.WriteFile(filepath.Join(config, "CLAUDE.md"), []byte("regra pessoal"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	path := helper(t, mode)
	adapter := NewClaudeCode(path).(*cliAdapter)
	detected := adapter.Detect()
	if !detected.Available || !adapter.DocumentContractSupported(detected) {
		t.Fatalf("contract unavailable: %+v", detected)
	}
	return adapter, DocumentRequest{Prompt: "Pedido do usuário", SystemPrompt: "Escreva a SPEC.", Model: "sonnet", ReasoningEffort: "high",
		ExpectedExecutablePath: detected.Path, ExpectedExecutableVersion: detected.Version, OutputSchema: json.RawMessage(`{"type":"object"}`), MaxAssistantOutputBytes: 4096}
}

func TestClaudeCodeWritesADocumentWithNoToolsSettingsOrPersonalInstructions(t *testing.T) {
	adapter, request := claudeDocumentFixture(t, "claude-document")
	var progress []Event
	result, err := adapter.GenerateDocument(t.Context(), request, func(event Event) { progress = append(progress, event) })
	if err != nil || result.Text != `{"reply":"pronto","toolCalls":[]}` || result.ThreadID != "5b2c1d1e-2222-4c1f-9e0a-000000000002" ||
		!result.UsageKnown || result.Usage.InputTokens != 15 || result.Usage.OutputTokens != 7 {
		t.Fatalf("document: %+v %v", result, err)
	}
	if len(progress) != 1 || progress[0].Type != "text_delta" || progress[0].Text != result.Text {
		t.Fatalf("progress: %+v", progress)
	}
	moved := request
	moved.ExpectedExecutableVersion = "9.9.9 (Claude Code)"
	if _, err := adapter.GenerateDocument(t.Context(), moved, nil); !errors.Is(err, ErrClaudeDocumentContractUnavailable) {
		t.Fatalf("an updated CLI must not run a selection made for another version: %v", err)
	}
}

func TestClaudeCodeDocumentFailsClosedOnExtraToolsAndExplainsMissingLogin(t *testing.T) {
	adapter, request := claudeDocumentFixture(t, "claude-document-tools")
	if _, err := adapter.GenerateDocument(t.Context(), request, nil); !errors.Is(err, ErrClaudeDocumentProtocol) {
		t.Fatalf("a run that saw a tool of its own must fail: %v", err)
	}
	adapter, request = claudeDocumentFixture(t, "claude-document-logout")
	if _, err := adapter.GenerateDocument(t.Context(), request, nil); !errors.Is(err, ErrClaudeNotLoggedIn) {
		t.Fatalf("logged out: %v", err)
	}
}

func TestClaudeVersionAcceptsOnlyTheCLIBanner(t *testing.T) {
	for input, want := range map[string]string{"2.1.286 (Claude Code)\n": "2.1.286 (Claude Code)", "codex-cli 0.157.0": "", "2.1.286 (Claude Code) extra": "", "": ""} {
		if got := claudeVersion(input); got != want {
			t.Fatalf("%q: got %q want %q", input, got, want)
		}
	}
}

func TestClaudeEnvironmentKeepsTheUserForTheKeychainLogin(t *testing.T) {
	env := buildEnvironment("claude", []string{"USER=pessoa", "HOME=/home/pessoa", "ANTHROPIC_API_KEY=secret"}, "darwin")
	if !slices.Contains(env, "USER=pessoa") || slices.Contains(env, "ANTHROPIC_API_KEY=secret") || !slices.Contains(env, "DISABLE_AUTOUPDATER=1") {
		t.Fatalf("claude env: %v", env)
	}
	if slices.Contains(buildEnvironment("codex", []string{"USER=pessoa"}, "darwin"), "USER=pessoa") {
		t.Fatal("other CLIs keep their environment unchanged")
	}
}

func TestCodexGetsInstructionsAndHarflexToolsOnEveryTurn(t *testing.T) {
	adapter := NewCodex("").(*cliAdapter)
	for _, session := range []string{"", "thread_1"} {
		args, input := adapter.build(Request{Prompt: "pergunta", CWD: "/tmp", SystemPrompt: "Você está no \"Harflex\".", SessionID: session, HarflexMCPURL: "http://127.0.0.1:9/mcp", HarflexMCPToken: "secret"})
		joined := strings.Join(args, " ")
		if input != "pergunta" || !strings.Contains(joined, `developer_instructions="Você está no \"Harflex\"."`) || !strings.Contains(joined, `mcp_servers.harflex.url="http://127.0.0.1:9/mcp"`) || !strings.Contains(joined, `mcp_servers.harflex.bearer_token_env_var="HARFLEX_MCP_TOKEN"`) || !strings.Contains(joined, `mcp_servers.harflex.default_tools_approval_mode="approve"`) || strings.Contains(joined, "secret") {
			t.Fatalf("session %q: %v / %q", session, args, input)
		}
		if session != "" && !strings.HasSuffix(joined, "resume thread_1 -") {
			t.Fatalf("resume stays last: %v", args)
		}
	}
	if args, plain := adapter.build(Request{Prompt: "só isso", CWD: "/tmp"}); plain != "só isso" || strings.Contains(strings.Join(args, " "), "developer_instructions") || strings.Contains(strings.Join(args, " "), "mcp_servers") {
		t.Fatalf("without instructions nothing is added: %v %q", args, plain)
	}
}

func TestClaudeGetsTheHarflexMCPServerWithoutTheTokenInItsArguments(t *testing.T) {
	args, _ := NewClaudeCode("").(*cliAdapter).build(Request{Prompt: "oi", CWD: "/tmp", HarflexMCPURL: "http://127.0.0.1:9/mcp", HarflexMCPToken: "secret"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, `"url":"http://127.0.0.1:9/mcp"`) || !strings.Contains(joined, "Bearer ${HARFLEX_MCP_TOKEN}") || !strings.Contains(joined, "--allowedTools mcp__harflex") || strings.Contains(joined, "secret") || strings.Contains(joined, "--strict-mcp-config") {
		t.Fatalf("claude args: %v", args)
	}
}
