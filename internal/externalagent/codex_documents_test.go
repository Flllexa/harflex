package externalagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCodexDocumentGeneratorContractIsAvailable(t *testing.T) {
	if _, exists := reflect.TypeOf(NewCodex("")).MethodByName("GenerateDocument"); !exists {
		t.Fatal("Codex adapter must expose the isolated document generator")
	}
}

func codexDocumentRequestFixture(t *testing.T) DocumentRequest {
	t.Helper()
	return DocumentRequest{Prompt: "Discovery supplied by Harflex", SystemPrompt: "Return JSON documents only.", CWD: t.TempDir(), Model: "gpt-6-sol", ReasoningEffort: "low", ExpectedExecutablePath: codexDocumentExecutable, ExpectedExecutableVersion: codexDocumentVersion, OutputSchema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`), MaxAssistantOutputBytes: 4096}
}

func TestCodexDocumentInvocationUsesEmptyScratchAndPrivateSchema(t *testing.T) {
	r := codexDocumentRequestFixture(t)
	r.SystemPrompt = "Return JSON. Preserve these literal characters: \" \n $() `x`."
	privateRoot := filepath.Join(t.TempDir(), "private")
	inv, err := prepareCodexDocumentInvocation(r, codexDocumentExecutable, []string{"HOME=" + privateRoot, "PATH=/usr/bin:/bin"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(inv.scratch) })
	if inv.path != "/usr/bin/sandbox-exec" || inv.scratch == r.CWD || strings.Contains(strings.Join(inv.args, " "), r.CWD) {
		t.Fatal("document generation inherited the project workspace")
	}
	joined := strings.Join(inv.args, " ")
	for _, required := range []string{"--ignore-user-config", "--ignore-rules", "--ephemeral", "--strict-config", "--sandbox read-only", "code_mode_host", "skills.include_instructions=false", "agents.enabled=false", "project_doc_max_bytes=0", "AGENTS.md", "AGENTS.override.md"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing isolation control %q", required)
		}
	}
	if strings.Contains(inv.args[1], "auth.json") || strings.Contains(joined, "--add-dir") || strings.Contains(joined, "--worktree") {
		t.Fatal("document isolation changes authentication or grants workspace access")
	}
	for _, name := range []string{"output-schema.json", "model-catalog.json"} {
		info, err := os.Stat(filepath.Join(inv.scratch, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private %s mode not retained", name)
		}
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	raw, _ := os.ReadFile(filepath.Join(inv.scratch, "model-catalog.json"))
	if json.Unmarshal(raw, &catalog) != nil || len(catalog.Models) != 1 {
		t.Fatal("document tool catalog unavailable")
	}
	m := catalog.Models[0]
	if m["slug"] != r.Model || m["tool_mode"] != "code_mode_only" || m["apply_patch_tool_type"] != nil || m["node_repl_disabled"] != true || m["supports_search_tool"] != false {
		t.Fatal("selected model was not constrained to the inactive tool host")
	}
	for i, arg := range inv.args {
		if arg == "-c" && i+1 < len(inv.args) && strings.HasPrefix(inv.args[i+1], "developer_instructions=") {
			got, err := strconv.Unquote(strings.TrimPrefix(inv.args[i+1], "developer_instructions="))
			if err != nil || got != r.SystemPrompt {
				t.Fatal("document instructions were altered or interpreted")
			}
		}
	}
}

func TestCodexDocumentBootstrapProfileCoversInstructionSymlink(t *testing.T) {
	privateRoot := t.TempDir()
	actual := filepath.Join(t.TempDir(), "instructions.md")
	if err := os.WriteFile(actual, []byte("synthetic ambient instruction"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, filepath.Join(privateRoot, "AGENTS.md")); err != nil {
		t.Skip(err)
	}
	profile, err := codexDocumentBootstrapProfile([]string{"HOME=/unused", "CODEX_HOME=" + privateRoot})
	if err != nil || !strings.Contains(profile, strconv.Quote(actual)) || !strings.Contains(profile, strconv.Quote(filepath.Join(privateRoot, "AGENTS.md"))) {
		t.Fatal("ambient instruction symlink escaped the bootstrap denial")
	}
}

func TestCodexDocumentRequestRejectsMissingFrozenSelection(t *testing.T) {
	for _, change := range []func(*DocumentRequest){
		func(r *DocumentRequest) { r.ExpectedExecutablePath = "" },
		func(r *DocumentRequest) { r.ExpectedExecutableVersion = "" },
		func(r *DocumentRequest) { r.Model = "--arbitrary-option" },
		func(r *DocumentRequest) { r.ReasoningEffort = "low\"\nunsafe" },
		func(r *DocumentRequest) { r.OutputSchema = json.RawMessage(`[]`) },
		func(r *DocumentRequest) { r.MaxAssistantOutputBytes = 0 },
	} {
		r := codexDocumentRequestFixture(t)
		change(&r)
		if validateCodexDocumentRequest(r) == nil {
			t.Fatal("invalid document contract was admitted")
		}
	}
}

func TestCodexDocumentProtocolUsesTerminalTextAndUsage(t *testing.T) {
	s := codexDocumentState{limit: 4096}
	var progress []Event
	for _, raw := range []string{
		`{"type":"thread.started","thread_id":"doc-synthetic"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"commentary","type":"agent_message","text":"Preparing documents"}}`,
		`{"type":"item.completed","item":{"id":"final","type":"agent_message","text":"{\"ok\":true}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":7,"output_tokens":4}}`,
	} {
		if err := s.consume(json.RawMessage(raw), func(e Event) { progress = append(progress, e) }); err != nil {
			t.Fatal(err)
		}
	}
	if !s.completed || s.result.Text != `{"ok":true}` || !s.result.UsageKnown || s.result.Usage.InputTokens != 7 || s.result.Usage.OutputTokens != 4 || s.result.ThreadID != "doc-synthetic" {
		t.Fatalf("terminal document evidence missing: %+v", s.result)
	}
	for _, event := range progress {
		if event.Type != "text_delta" || event.Raw != nil {
			t.Fatal("callback leaked raw protocol or tool event")
		}
	}
}

func TestCodexDocumentProtocolRejectsToolsAndInvalidTerminals(t *testing.T) {
	for _, raw := range []string{
		`{"type":"item.started","item":{"type":"command_execution"}}`,
		`{"type":"item.completed","item":{"type":"file_change"}}`,
		`{"type":"item.completed","item":{"type":"mcp_tool_call"}}`,
		`{"type":"item.completed","item":{"type":"web_search"}}`,
		`{"type":"turn.failed","error":{"message":"private diagnostic"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":-1,"output_tokens":1}}`,
		`{"type":"item.completed","item":{"type":"error","message":"private diagnostic"}}`,
		`{"type":"item.completed","item":{"type":"error","message":"Code Mode is unavailable because code-mode host is disabled. Code mode will fail closed; enable ` + "`features.code_mode_host` and install `codex-code-mode-host`." + `"}}`,
	} {
		s := codexDocumentState{limit: 4096}
		_ = s.consume(json.RawMessage(`{"type":"thread.started","thread_id":"doc-synthetic"}`), nil)
		_ = s.consume(json.RawMessage(`{"type":"turn.started"}`), nil)
		if !errors.Is(s.consume(json.RawMessage(raw), nil), ErrCodexDocumentProtocol) {
			t.Fatalf("unsafe protocol event was accepted: %s", raw)
		}
	}
}

func TestCodexDocumentProtocolCapsCumulativeAssistantOutput(t *testing.T) {
	s := codexDocumentState{limit: 5}
	_ = s.consume(json.RawMessage(`{"type":"thread.started","thread_id":"doc-synthetic"}`), nil)
	_ = s.consume(json.RawMessage(`{"type":"turn.started"}`), nil)
	if err := s.consume(json.RawMessage(`{"type":"item.completed","item":{"type":"agent_message","text":"abc"}}`), nil); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.consume(json.RawMessage(`{"type":"item.completed","item":{"type":"agent_message","text":"def"}}`), nil), ErrOutputLimitExceeded) {
		t.Fatal("assistant output cap did not cover separate messages")
	}
}

func codexDocumentShellInvocation(t *testing.T, lines []string, suffix string) codexDocumentInvocation {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a local shell fixture")
	}
	var body strings.Builder
	for _, line := range lines {
		body.WriteString("printf '%s\\n' '" + strings.ReplaceAll(line, "'", "'\\''") + "'\n")
	}
	body.WriteString(suffix)
	return codexDocumentInvocation{path: "/bin/sh", args: []string{"-c", body.String()}, scratch: t.TempDir(), env: []string{"PATH=/usr/bin:/bin"}}
}

func TestCodexDocumentProcessRejectsToolAndJoins(t *testing.T) {
	r := codexDocumentRequestFixture(t)
	inv := codexDocumentShellInvocation(t, []string{`{"type":"thread.started","thread_id":"doc-synthetic"}`, `{"type":"turn.started"}`, `{"type":"item.started","item":{"type":"command_execution"}}`}, "sleep 30\n")
	started := time.Now()
	_, err := runCodexDocumentInvocation(t.Context(), inv, r, nil)
	if !errors.Is(err, ErrCodexDocumentProtocol) || time.Since(started) > 2*time.Second {
		t.Fatalf("tool rejection did not join process: elapsed=%s err=%v", time.Since(started), err)
	}
}

func TestCodexDocumentProcessCancellationJoins(t *testing.T) {
	r := codexDocumentRequestFixture(t)
	inv := codexDocumentShellInvocation(t, []string{`{"type":"thread.started","thread_id":"doc-synthetic"}`, `{"type":"turn.started"}`}, "sleep 30\n")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runCodexDocumentInvocation(ctx, inv, r, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatalf("cancellation did not join process: elapsed=%s err=%v", time.Since(started), err)
	}
}

func TestCodexDocumentProcessOmitsPrivateDiagnostics(t *testing.T) {
	r := codexDocumentRequestFixture(t)
	r.Prompt = "private supplied document"
	inv := codexDocumentShellInvocation(t, nil, "printf '%s' 'private supplied document provider credential diagnostic' >&2\nexit 9\n")
	_, err := runCodexDocumentInvocation(t.Context(), inv, r, nil)
	if err == nil || strings.Contains(fmt.Sprint(err), "private supplied") || strings.Contains(fmt.Sprint(err), "credential diagnostic") {
		t.Fatal("private process diagnostics escaped the document adapter")
	}
}

func TestCodexDocumentVersionRejectsUnprovenContracts(t *testing.T) {
	for _, output := range []string{"codex-cli 0.158.0", "codex-cli 0.157.0\ncodex-cli 0.157.0", "helper 0.157.0", ""} {
		if isolatedCodexVersion(output) == codexDocumentVersion {
			t.Fatalf("unproven version was accepted: %q", output)
		}
	}
	if isolatedCodexVersion("WARNING: synthetic warning\ncodex-cli 0.157.0\n") != codexDocumentVersion {
		t.Fatal("non-version diagnostic changed the frozen version")
	}
}

// Opt-in: runs the real 0.157.0 CLI against a loopback-only synthetic provider.
// No authentication values, config values, model payloads or stdout are printed.
func TestLiveCodexDocumentSyntheticContract(t *testing.T) {
	if os.Getenv("HARFLEX_LIVE_CODEX_DOCUMENT_CONTRACT") != "1" || runtime.GOOS != "darwin" {
		t.Skip("set HARFLEX_LIVE_CODEX_DOCUMENT_CONTRACT=1 for the local fake-provider contract")
	}
	env := buildEnvironment("codex", os.Environ(), runtime.GOOS)
	if err := verifyCodexDocumentVersion(t.Context(), codexDocumentExecutable, env, codexDocumentVersion); err != nil {
		t.Fatal("the exact document CLI contract is not installed")
	}
	for _, model := range []string{"gpt-6-sol", "gpt-5.4"} {
		t.Run(model, func(t *testing.T) {
			r := codexDocumentRequestFixture(t)
			r.Model = model
			inv, err := prepareCodexDocumentInvocation(r, codexDocumentExecutable, env)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(inv.scratch) })
			const marker = "HARFLEX_AMBIENT_CANARY_MUST_NOT_REACH_MODEL"
			if err := os.WriteFile(filepath.Join(inv.scratch, "AGENTS.md"), []byte(marker), 0600); err != nil {
				t.Fatal(err)
			}
			skillRoot := filepath.Join(inv.scratch, ".agents", "skills", "ambient-canary")
			if err := os.MkdirAll(skillRoot, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("---\nname: ambient-canary\ndescription: "+marker+"\n---\n"+marker), 0600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			requests := 0
			var wireErr error
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodGet {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"models":[]}`)
					return
				}
				var body struct {
					Model string            `json:"model"`
					Tools []json.RawMessage `json:"tools"`
					Input []struct {
						Role, Type string
						Content    []struct {
							Type, Text string
						} `json:"content"`
					} `json:"input"`
				}
				payload, readErr := io.ReadAll(io.LimitReader(request.Body, 2*maxLine))
				mu.Lock()
				requests++
				decodeErr := json.Unmarshal(payload, &body)
				if readErr != nil || decodeErr != nil || request.Header.Get("Authorization") != "" || body.Model != model || len(body.Tools) != 0 {
					var toolNames []string
					for _, tool := range body.Tools {
						var name struct{ Name, Type string }
						_ = json.Unmarshal(tool, &name)
						toolNames = append(toolNames, name.Type+":"+name.Name)
					}
					wireErr = fmt.Errorf("synthetic wire failed: readError=%t decodeError=%t authenticationPresent=%t modelMatches=%t tools=%v", readErr != nil, decodeErr != nil, request.Header.Get("Authorization") != "", body.Model == model, toolNames)
				}
				if strings.Contains(string(payload), marker) || strings.Contains(string(payload), "# AGENTS.md instructions") || strings.Contains(string(payload), "Available skills") || strings.Contains(string(payload), "MEMORY_SUMMARY") {
					wireErr = errors.New("ambient data entered the synthetic document request")
				}
				userMessages := 0
				for _, input := range body.Input {
					if input.Role == "user" {
						userMessages++
						if len(input.Content) != 1 || input.Content[0].Text != r.Prompt {
							wireErr = errors.New("request includes another user data source")
						}
					}
				}
				if userMessages != 1 {
					wireErr = errors.New("missing unique supplied Discovery")
				}
				mu.Unlock()
				item := map[string]any{"id": "msg_synthetic", "type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": `{"ok":true}`}}}
				wireEvents := []any{map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_synthetic", "status": "in_progress", "output": []any{}}}, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}, map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_synthetic", "status": "completed", "output": []any{item}, "usage": map[string]int{"input_tokens": 7, "output_tokens": 3, "total_tokens": 10}}}}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range wireEvents {
					data, _ := json.Marshal(event)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				}
			}))
			t.Cleanup(server.Close)
			options := []string{"model_provider=\"harflex_document_probe\"", "model_providers.harflex_document_probe.name=\"Harflex synthetic probe\"", "model_providers.harflex_document_probe.base_url=" + strconv.Quote(server.URL), "model_providers.harflex_document_probe.wire_api=\"responses\"", "model_providers.harflex_document_probe.requires_openai_auth=false"}
			inv.args = inv.args[:len(inv.args)-1]
			for _, option := range options {
				inv.args = append(inv.args, "-c", option)
			}
			inv.args = append(inv.args, "-")
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			result, err := runCodexDocumentInvocation(ctx, inv, r, nil)
			if err != nil || result.Text != `{"ok":true}` || !result.UsageKnown || result.Usage.InputTokens != 7 || result.Usage.OutputTokens != 3 {
				t.Fatalf("synthetic document terminal failed: usageKnown=%t err=%v", result.UsageKnown, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if requests != 1 || wireErr != nil {
				t.Fatalf("synthetic contract requests=%d err=%v", requests, wireErr)
			}
		})
	}
}

func TestLiveCodexDocumentRejectsSyntheticToolCall(t *testing.T) {
	if os.Getenv("HARFLEX_LIVE_CODEX_DOCUMENT_CONTRACT") != "1" || runtime.GOOS != "darwin" {
		t.Skip("set HARFLEX_LIVE_CODEX_DOCUMENT_CONTRACT=1 for the local fake-provider contract")
	}
	r := codexDocumentRequestFixture(t)
	inv, err := prepareCodexDocumentInvocation(r, codexDocumentExecutable, buildEnvironment("codex", os.Environ(), runtime.GOOS))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(inv.scratch) })
	const marker = "HARFLEX_OUTSIDE_SCRATCH_CANARY_CONTENT"
	canary := filepath.Join(t.TempDir(), "outside-scratch.txt")
	if err := os.WriteFile(canary, []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requests := 0
	unsafeOutput := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"models":[]}`)
			return
		}
		payload, _ := io.ReadAll(io.LimitReader(request.Body, 2*maxLine))
		mu.Lock()
		requests++
		if strings.Contains(string(payload), marker) || request.Header.Get("Authorization") != "" {
			unsafeOutput = true
		}
		mu.Unlock()
		arguments, _ := json.Marshal(map[string]string{"cmd": "cat " + strconv.Quote(canary)})
		item := map[string]any{"id": "fc_synthetic", "type": "function_call", "name": "exec_command", "call_id": "call_synthetic", "arguments": string(arguments)}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_synthetic", "status": "in_progress", "output": []any{}}}, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}, map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_synthetic", "status": "completed", "output": []any{item}, "usage": map[string]int{"input_tokens": 7, "output_tokens": 3, "total_tokens": 10}}}} {
			data, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}))
	t.Cleanup(server.Close)
	inv.args = inv.args[:len(inv.args)-1]
	for _, option := range []string{"model_provider=\"harflex_document_probe\"", "model_providers.harflex_document_probe.name=\"Harflex synthetic probe\"", "model_providers.harflex_document_probe.base_url=" + strconv.Quote(server.URL), "model_providers.harflex_document_probe.wire_api=\"responses\"", "model_providers.harflex_document_probe.requires_openai_auth=false"} {
		inv.args = append(inv.args, "-c", option)
	}
	inv.args = append(inv.args, "-")
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	started := time.Now()
	result, err := runCodexDocumentInvocation(ctx, inv, r, nil)
	mu.Lock()
	defer mu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) || requests == 0 || unsafeOutput || result.Text != "" || time.Since(started) > 6*time.Second {
		t.Fatalf("synthetic invisible tool call was not bounded: requests=%d unsafe=%t err=%v", requests, unsafeOutput, err)
	}
}
