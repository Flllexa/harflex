package externalagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
)

type DocumentRequest struct {
	Prompt, SystemPrompt, CWD, Model, ReasoningEffort string
	ExpectedExecutablePath, ExpectedExecutableVersion string
	OutputSchema                                      json.RawMessage
	MaxAssistantOutputBytes                           int
}

type DocumentResult struct {
	Text       string
	Usage      agentcore.Usage
	UsageKnown bool
	ThreadID   string
}

type DocumentGenerator interface {
	GenerateDocument(context.Context, DocumentRequest, func(Event)) (DocumentResult, error)
}

var ErrCodexDocumentContractUnavailable = errors.New("isolated Codex document contract unavailable")
var ErrCodexDocumentProtocol = errors.New("unexpected Codex document protocol event")

const codexDocumentVersion = "codex-cli 0.157.0"
const codexDocumentExecutable = "/opt/homebrew/bin/codex"

// The exact 0.157.0 model contract keeps every selected model in Code Mode,
// disables its host, and removes patch, image and experimental capabilities.
// The catalog controls the local tool router; it does not grant model access.
const codexDocumentModelTemplate = `{"slug":"selected_model","display_name":"Harflex document author","description":"Text-only document generation","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low","description":"Low"},{"effort":"medium","description":"Medium"},{"effort":"high","description":"High"},{"effort":"xhigh","description":"Extra high"},{"effort":"max","description":"Maximum"},{"effort":"ultra","description":"Ultra"}],"shell_type":"unified_exec","visibility":"list","supported_in_api":true,"priority":2,"additional_speed_tiers":[],"service_tiers":[],"default_service_tier":null,"availability_nux":null,"upgrade":null,"model_messages":null,"include_skills_usage_instructions":false,"include_plugin_usage_instructions":false,"include_apps_usage_instructions":false,"default_reasoning_summary":"none","support_verbosity":false,"default_verbosity":"low","apply_patch_tool_type":null,"web_search_tool_type":"text_and_image","truncation_policy":{"mode":"tokens","limit":10000},"supports_image_detail_original":false,"context_window":272000,"max_context_window":872000,"comp_hash":"3000","effective_context_window_percent":95,"experimental_supported_tools":[],"input_modalities":["text"],"supports_search_tool":false,"supports_experimental_context":false,"use_responses_lite":true,"supports_reasoning_effort_updates":false,"node_repl_auto_review_required":true,"node_repl_disabled":true,"tool_mode":"code_mode_only","multi_agent_version":"v1","base_instructions":""}`

var _ DocumentGenerator = (*cliAdapter)(nil)

func (a *cliAdapter) DocumentContractSupported(detection Detection) bool {
	if a.id == "claude" {
		return a.claudeDocumentContractSupported(detection)
	}
	if a.id != "codex" || runtime.GOOS != "darwin" || !detection.Available || detection.Path != codexDocumentExecutable || isolatedCodexVersion(detection.Version) != codexDocumentVersion {
		return false
	}
	info, err := os.Stat("/usr/bin/sandbox-exec")
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func (a *cliAdapter) GenerateDocument(parent context.Context, request DocumentRequest, progress func(Event)) (DocumentResult, error) {
	if a.id == "claude" && runtime.GOOS == "darwin" {
		return a.generateClaudeDocument(parent, request, progress)
	}
	if a.id != "codex" || runtime.GOOS != "darwin" {
		return DocumentResult{}, ErrCodexDocumentContractUnavailable
	}
	if err := validateCodexDocumentRequest(request); err != nil {
		return DocumentResult{}, err
	}
	// No time limit: a long document takes as long as it takes, and the caller cancels it.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	path, err := a.executable()
	if err != nil || path != codexDocumentExecutable || path != filepath.Clean(request.ExpectedExecutablePath) || !nativeExecutable(path) {
		return DocumentResult{}, ErrCodexDocumentContractUnavailable
	}
	env := buildEnvironment("codex", os.Environ(), runtime.GOOS)
	if err := verifyCodexDocumentVersion(ctx, path, env, request.ExpectedExecutableVersion); err != nil {
		return DocumentResult{}, err
	}
	invocation, err := prepareCodexDocumentInvocation(request, path, env)
	if err != nil {
		return DocumentResult{}, err
	}
	defer os.RemoveAll(invocation.scratch)
	return runCodexDocumentInvocation(ctx, invocation, request, progress)
}

func validateCodexDocumentRequest(r DocumentRequest) error {
	if strings.TrimSpace(r.Prompt) == "" || !utf8.ValidString(r.Prompt) || !utf8.ValidString(r.SystemPrompt) || len(r.Prompt)+len(r.SystemPrompt) > maxLine {
		return errors.New("invalid Codex document prompt")
	}
	if r.Model == "" || !validCatalogText(r.Model, 512) || strings.HasPrefix(r.Model, "-") || strings.ContainsAny(r.Model, "\r\n") {
		return errors.New("invalid Codex document model")
	}
	if r.ReasoningEffort != "" && r.ReasoningEffort != "low" && r.ReasoningEffort != "medium" && r.ReasoningEffort != "high" && r.ReasoningEffort != "xhigh" && r.ReasoningEffort != "max" && r.ReasoningEffort != "ultra" {
		return errors.New("invalid Codex document reasoning effort")
	}
	if len(r.OutputSchema) == 0 || len(r.OutputSchema) > 64*1024 || !json.Valid(r.OutputSchema) || bytes.TrimSpace(r.OutputSchema)[0] != '{' {
		return errors.New("invalid Codex document output schema")
	}
	if r.MaxAssistantOutputBytes < 1 || r.MaxAssistantOutputBytes > maxLine || !filepath.IsAbs(r.ExpectedExecutablePath) || r.ExpectedExecutableVersion == "" {
		return errors.New("invalid Codex document execution selection")
	}
	if r.CWD != "" {
		info, err := os.Stat(r.CWD)
		if !filepath.IsAbs(r.CWD) || err != nil || !info.IsDir() {
			return errors.New("Codex document workspace unavailable")
		}
	}
	return nil
}

func verifyCodexDocumentVersion(parent context.Context, path string, env []string, expected string) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	cmd := exec.Command(path, "--version")
	cmd.Env, cmd.WaitDelay = env, 250*time.Millisecond
	output := &limitedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = output, output
	owned, err := ownCommand(cmd)
	if err != nil {
		return ErrCodexDocumentContractUnavailable
	}
	defer owned.close()
	if err := owned.start(); err != nil {
		return ErrCodexDocumentContractUnavailable
	}
	if err := owned.wait(ctx); err != nil || ctx.Err() != nil || output.truncated {
		return errors.Join(ErrCodexDocumentContractUnavailable, ctx.Err())
	}
	version := isolatedCodexVersion(output.String())
	if version != codexDocumentVersion || version != isolatedCodexVersion(expected) {
		return ErrCodexDocumentContractUnavailable
	}
	return nil
}

func isolatedCodexVersion(output string) string {
	version := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "codex-cli ") {
			if version != "" {
				return ""
			}
			version = strings.TrimSpace(line)
		}
	}
	return version
}

type codexDocumentInvocation struct {
	path, scratch string
	args, env     []string
}

func prepareCodexDocumentInvocation(r DocumentRequest, path string, env []string) (codexDocumentInvocation, error) {
	profile, err := codexDocumentBootstrapProfile(env)
	if err != nil {
		return codexDocumentInvocation{}, err
	}
	scratch, err := os.MkdirTemp("", "harflex-codex-document-")
	if err != nil {
		return codexDocumentInvocation{}, errors.New("Codex document scratch unavailable")
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(scratch)
		}
	}()
	schemaPath, catalogPath := filepath.Join(scratch, "output-schema.json"), filepath.Join(scratch, "model-catalog.json")
	var model map[string]any
	if json.Unmarshal([]byte(codexDocumentModelTemplate), &model) != nil {
		return codexDocumentInvocation{}, ErrCodexDocumentContractUnavailable
	}
	model["slug"] = r.Model
	catalog, err := json.Marshal(map[string]any{"models": []any{model}})
	if err != nil || os.WriteFile(schemaPath, bytes.Clone(r.OutputSchema), 0600) != nil || os.WriteFile(catalogPath, catalog, 0600) != nil {
		return codexDocumentInvocation{}, errors.New("Codex document scratch unavailable")
	}
	args := []string{"-p", profile, path, "exec", "--json", "--color", "never", "--sandbox", "read-only", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--strict-config", "-C", scratch, "--skip-git-repo-check", "--model", r.Model, "--output-schema", schemaPath}
	for _, feature := range []string{"shell_tool", "unified_exec", "shell_snapshot", "view_image", "apps", "plugins", "remote_plugin", "multi_agent", "multi_agent_v2", "memories", "browser_use", "browser_use_external", "browser_use_full_cdp_access", "computer_use", "image_generation", "hooks", "code_mode", "code_mode_host", "code_mode_prewarm", "skill_search", "skill_mcp_dependency_install", "tool_suggest", "goals", "sleep_tool"} {
		args = append(args, "--disable", feature)
	}
	args = append(args, "--enable", "skip_host_skill_discovery")
	configs := []string{"instructions=\"\"", "developer_instructions=" + strconv.Quote(r.SystemPrompt), "model_catalog_json=" + strconv.Quote(catalogPath), "project_doc_max_bytes=0", "skills.include_instructions=false", "agents.enabled=false", "tools.update_plan.enabled=false", "tools.experimental_request_user_input.enabled=false", "web_search=\"disabled\"", "approval_policy=\"never\"", "include_environment_context=false", "include_permissions_instructions=false", "include_apps_instructions=false", "include_collaboration_mode_instructions=false", "suppress_unstable_features_warning=true"}
	if r.ReasoningEffort != "" {
		configs = append(configs, "model_reasoning_effort="+strconv.Quote(r.ReasoningEffort))
	}
	for _, config := range configs {
		args = append(args, "-c", config)
	}
	args = append(args, "-")
	failed = false
	return codexDocumentInvocation{path: "/usr/bin/sandbox-exec", scratch: scratch, args: args, env: append([]string(nil), env...)}, nil
}

func codexDocumentBootstrapProfile(env []string) (string, error) {
	values := make(map[string]string)
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			values[name] = value
		}
	}
	codexRoot := values["CODEX_HOME"]
	if codexRoot == "" {
		codexRoot = filepath.Join(values["HOME"], ".codex")
	}
	if !filepath.IsAbs(codexRoot) || strings.ContainsAny(codexRoot, "\r\n\x00") {
		return "", ErrCodexDocumentContractUnavailable
	}
	paths := make(map[string]bool)
	for _, name := range []string{"AGENTS.md", "AGENTS.override.md"} {
		path := filepath.Join(codexRoot, name)
		paths[path] = true
		if canonical, err := filepath.EvalSymlinks(path); err == nil {
			paths[canonical] = true
		}
	}
	var profile strings.Builder
	profile.WriteString("(version 1) (allow default) (deny file-read*")
	for path := range paths {
		profile.WriteString(" (literal " + strconv.Quote(path) + ")")
	}
	profile.WriteString(")")
	return profile.String(), nil
}

func runCodexDocumentInvocation(parent context.Context, invocation codexDocumentInvocation, r DocumentRequest, progress func(Event)) (DocumentResult, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	events, finished := make(chan Event, 8), make(chan error, 1)
	go func() {
		defer close(events)
		finished <- runProcess(ctx, invocation.path, invocation.args, r.Prompt, Request{Prompt: r.Prompt, CWD: invocation.scratch, Model: r.Model}, invocation.env, events, normalize)
	}()
	state := codexDocumentState{limit: r.MaxAssistantOutputBytes}
	var protocolErr error
	for event := range events {
		if protocolErr != nil || ctx.Err() != nil {
			continue
		}
		if err := state.consume(event.Raw, progress); err != nil {
			protocolErr = err
			cancel()
		}
	}
	processErr := <-finished
	if protocolErr != nil {
		return DocumentResult{}, protocolErr
	}
	if err := parent.Err(); err != nil {
		return DocumentResult{}, err
	}
	if processErr != nil {
		return DocumentResult{}, fmt.Errorf("Codex document process failed: %w", safeCause{processErr})
	}
	if !state.completed || state.result.ThreadID == "" || strings.TrimSpace(state.result.Text) == "" {
		return DocumentResult{}, ErrCodexDocumentProtocol
	}
	return state.result, nil
}

type codexDocumentState struct {
	result                DocumentResult
	limit, assistantBytes int
	started, completed    bool
}

func (s *codexDocumentState) consume(raw json.RawMessage, progress func(Event)) error {
	if !utf8.Valid(raw) || len(raw) == 0 || len(raw) > maxLine || !json.Valid(raw) || s.completed {
		return ErrCodexDocumentProtocol
	}
	var record struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Item     struct {
			ID, Type, Text, Message string
		} `json:"item"`
		Usage struct {
			Input  *int64 `json:"input_tokens"`
			Output *int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &record) != nil {
		return ErrCodexDocumentProtocol
	}
	switch record.Type {
	case "thread.started":
		if s.result.ThreadID != "" || !ValidSessionID(record.ThreadID) {
			return ErrCodexDocumentProtocol
		}
		s.result.ThreadID = record.ThreadID
	case "turn.started":
		if s.started || s.result.ThreadID == "" {
			return ErrCodexDocumentProtocol
		}
		s.started = true
	case "item.started", "item.updated", "item.completed":
		if record.Item.Type == "error" {
			if s.started || record.Type != "item.completed" || !knownCodexDocumentIsolationWarning(record.Item.Message) {
				return ErrCodexDocumentProtocol
			}
			return nil
		}
		if !s.started || (record.Item.Type != "agent_message" && record.Item.Type != "reasoning") {
			return ErrCodexDocumentProtocol
		}
		if record.Item.Type == "agent_message" && record.Type == "item.completed" {
			if !utf8.ValidString(record.Item.Text) || s.assistantBytes+len(record.Item.Text) > s.limit {
				return ErrOutputLimitExceeded
			}
			s.assistantBytes += len(record.Item.Text)
			s.result.Text = record.Item.Text
			if progress != nil && record.Item.Text != "" {
				progress(Event{Type: "text_delta", Text: record.Item.Text, MessageID: record.Item.ID, SessionID: s.result.ThreadID})
			}
		}
	case "turn.completed":
		if !s.started {
			return ErrCodexDocumentProtocol
		}
		if record.Usage.Input != nil && record.Usage.Output != nil {
			if *record.Usage.Input < 0 || *record.Usage.Output < 0 {
				return ErrCodexDocumentProtocol
			}
			s.result.Usage = agentcore.Usage{InputTokens: *record.Usage.Input, OutputTokens: *record.Usage.Output}
			s.result.UsageKnown = true
		}
		s.completed = true
	default:
		return ErrCodexDocumentProtocol
	}
	return nil
}

func knownCodexDocumentIsolationWarning(message string) bool {
	return strings.HasPrefix(message, "Failed to read global AGENTS.md instructions from `") && strings.Contains(message, "Operation not permitted") ||
		message == "Code Mode is unavailable because code-mode host is disabled. Code mode will fail closed; enable `features.code_mode_host` and install `codex-code-mode-host`."
}
