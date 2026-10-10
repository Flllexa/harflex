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

var ErrClaudeDocumentContractUnavailable = errors.New("isolated Claude Code document contract unavailable")
var ErrClaudeDocumentProtocol = errors.New("unexpected Claude Code document protocol event")
var ErrClaudeNotLoggedIn = errors.New("Claude Code is not logged in")

// The only tool a document run may see is the one Claude Code adds itself to return the --json-schema answer.
const claudeStructuredOutputTool = "StructuredOutput"

// Claude models reach for native tool calls; told nothing, they call the Harflex tools by name, get "No such tool"
// and give up. This says where those requests go instead.
const claudeDocumentToolInstructions = "In this run you can call exactly one tool: StructuredOutput, and every answer must be a single StructuredOutput call. The Harflex tools named in the conversation (read, write, edit, list, find, grep and any other) are NOT callable here: never call them directly, because a direct call fails. To use one, put it in the toolCalls array of your StructuredOutput answer with its arguments; Harflex runs it and sends the result in the next turn. Do not conclude that a tool is unavailable because a direct call failed. When a supplied Harflex tool is a shell such as bash, the commands you request through it are authorized and do run: use it whenever the task asks you to run commands, tests or the app."

// claudeDocumentContractSupported says whether this Claude Code can write a document with nothing but the model:
// no tools, MCP, skills, settings or personal instructions. macOS's sandbox keeps the personal CLAUDE.md unread.
func (a *cliAdapter) claudeDocumentContractSupported(detection Detection) bool {
	if a.id != "claude" || runtime.GOOS != "darwin" || !detection.Available || !filepath.IsAbs(detection.Path) || claudeVersion(detection.Version) == "" || !nativeExecutable(detection.Path) {
		return false
	}
	info, err := os.Stat("/usr/bin/sandbox-exec")
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

// claudeVersion keeps the "<version> (Claude Code)" line the CLI prints, or nothing when the output is anything else.
func claudeVersion(output string) string {
	line := strings.TrimSpace(output)
	version, ok := strings.CutSuffix(line, " (Claude Code)")
	if !ok || version == "" || strings.ContainsAny(line, "\r\n") || strings.IndexFunc(version, func(r rune) bool { return !(r == '.' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z') }) >= 0 {
		return ""
	}
	return line
}

func (a *cliAdapter) generateClaudeDocument(parent context.Context, request DocumentRequest, progress func(Event)) (DocumentResult, error) {
	if err := validateClaudeDocumentRequest(request); err != nil {
		return DocumentResult{}, err
	}
	// No time limit: a long document takes as long as it takes, and the caller cancels it.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	path, err := a.executable()
	if err != nil || path != filepath.Clean(request.ExpectedExecutablePath) || !nativeExecutable(path) {
		return DocumentResult{}, ErrClaudeDocumentContractUnavailable
	}
	env := buildEnvironment("claude", os.Environ(), runtime.GOOS)
	if err := verifyClaudeDocumentVersion(ctx, path, env, request.ExpectedExecutableVersion); err != nil {
		return DocumentResult{}, err
	}
	invocation, err := prepareClaudeDocumentInvocation(request, path, env)
	if err != nil {
		return DocumentResult{}, err
	}
	defer os.RemoveAll(invocation.scratch)
	return runClaudeDocumentInvocation(ctx, invocation, request, progress)
}

func validateClaudeDocumentRequest(r DocumentRequest) error {
	if strings.TrimSpace(r.Prompt) == "" || !utf8.ValidString(r.Prompt) || !utf8.ValidString(r.SystemPrompt) || len(r.Prompt)+len(r.SystemPrompt) > maxLine {
		return errors.New("invalid Claude Code document prompt")
	}
	if r.Model == "" || !validCatalogText(r.Model, 512) || strings.HasPrefix(r.Model, "-") || strings.ContainsAny(r.Model, "\r\n ") {
		return errors.New("invalid Claude Code document model")
	}
	if r.ReasoningEffort != "" && !validClaudeReasoningEffort(r.ReasoningEffort) {
		return errors.New("invalid Claude Code document effort")
	}
	if len(r.OutputSchema) == 0 || len(r.OutputSchema) > 64*1024 || !json.Valid(r.OutputSchema) || bytes.TrimSpace(r.OutputSchema)[0] != '{' {
		return errors.New("invalid Claude Code document output schema")
	}
	if r.MaxAssistantOutputBytes < 1 || r.MaxAssistantOutputBytes > maxLine || !filepath.IsAbs(r.ExpectedExecutablePath) || claudeVersion(r.ExpectedExecutableVersion) == "" {
		return errors.New("invalid Claude Code document execution selection")
	}
	return nil
}

func verifyClaudeDocumentVersion(parent context.Context, path string, env []string, expected string) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.Command(path, "--version")
	cmd.Env, cmd.WaitDelay = env, 250*time.Millisecond
	output := &limitedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = output, output
	owned, err := ownCommand(cmd)
	if err != nil {
		return ErrClaudeDocumentContractUnavailable
	}
	defer owned.close()
	if err := owned.start(); err != nil {
		return ErrClaudeDocumentContractUnavailable
	}
	if err := owned.wait(ctx); err != nil || ctx.Err() != nil || output.truncated {
		return errors.Join(ErrClaudeDocumentContractUnavailable, ctx.Err())
	}
	if version := claudeVersion(output.String()); version == "" || version != claudeVersion(expected) {
		return ErrClaudeDocumentContractUnavailable
	}
	return nil
}

type claudeDocumentInvocation struct {
	path, scratch string
	args, env     []string
}

func prepareClaudeDocumentInvocation(r DocumentRequest, path string, env []string) (claudeDocumentInvocation, error) {
	profile, err := claudeDocumentSandboxProfile(env)
	if err != nil {
		return claudeDocumentInvocation{}, err
	}
	scratch, err := os.MkdirTemp("", "harflex-claude-document-")
	if err != nil {
		return claudeDocumentInvocation{}, errors.New("Claude Code document scratch unavailable")
	}
	if resolved, err := filepath.EvalSymlinks(scratch); err == nil {
		scratch = resolved
	}
	systemPath := filepath.Join(scratch, "system-prompt.md")
	if err := os.WriteFile(systemPath, []byte(r.SystemPrompt+"\n\n"+claudeDocumentToolInstructions), 0600); err != nil {
		_ = os.RemoveAll(scratch)
		return claudeDocumentInvocation{}, errors.New("Claude Code document scratch unavailable")
	}
	args := []string{"-p", profile, path, "-p", "--output-format", "stream-json", "--verbose",
		"--tools", "", "--setting-sources", "", "--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence",
		"--model", r.Model, "--system-prompt-file", systemPath, "--json-schema", string(bytes.TrimSpace(r.OutputSchema))}
	if r.ReasoningEffort != "" {
		args = append(args, "--effort", r.ReasoningEffort)
	}
	return claudeDocumentInvocation{path: "/usr/bin/sandbox-exec", scratch: scratch, args: args, env: append([]string(nil), env...)}, nil
}

// claudeDocumentSandboxProfile lets Claude Code run normally but keeps the person's own instructions
// (CLAUDE.md and rules in the Claude Code configuration folder) out of a document the Harflex asked for.
func claudeDocumentSandboxProfile(env []string) (string, error) {
	values := make(map[string]string)
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok {
			values[name] = value
		}
	}
	root := values["CLAUDE_CONFIG_DIR"]
	if root == "" {
		root = filepath.Join(values["HOME"], ".claude")
	}
	if !filepath.IsAbs(root) || strings.ContainsAny(root, "\r\n\x00") {
		return "", ErrClaudeDocumentContractUnavailable
	}
	var profile strings.Builder
	profile.WriteString("(version 1) (allow default) (deny file-read*")
	for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md"} {
		path := filepath.Join(root, name)
		profile.WriteString(" (literal " + strconv.Quote(path) + ")")
		if canonical, err := filepath.EvalSymlinks(path); err == nil && canonical != path {
			profile.WriteString(" (literal " + strconv.Quote(canonical) + ")")
		}
	}
	profile.WriteString(" (subpath " + strconv.Quote(filepath.Join(root, "rules")) + ")")
	profile.WriteString(")")
	return profile.String(), nil
}

func runClaudeDocumentInvocation(parent context.Context, invocation claudeDocumentInvocation, r DocumentRequest, progress func(Event)) (DocumentResult, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	events, finished := make(chan Event, 8), make(chan error, 1)
	go func() {
		defer close(events)
		finished <- runProcess(ctx, invocation.path, invocation.args, r.Prompt, Request{Prompt: r.Prompt, CWD: invocation.scratch, Model: r.Model}, invocation.env, events, normalize)
	}()
	state := claudeDocumentState{limit: r.MaxAssistantOutputBytes}
	var protocolErr error
	for event := range events {
		if protocolErr != nil || ctx.Err() != nil {
			continue
		}
		if err := state.consume(event.Raw); err != nil {
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
	if state.notLoggedIn {
		return DocumentResult{}, ErrClaudeNotLoggedIn
	}
	if processErr != nil {
		return DocumentResult{}, fmt.Errorf("Claude Code document process failed: %w", safeCause{processErr})
	}
	if !state.completed || state.result.ThreadID == "" || strings.TrimSpace(state.result.Text) == "" {
		return DocumentResult{}, ErrClaudeDocumentProtocol
	}
	if progress != nil {
		progress(Event{Type: "text_delta", Text: state.result.Text, SessionID: state.result.ThreadID})
	}
	return state.result, nil
}

type claudeDocumentState struct {
	result             DocumentResult
	limit              int
	started, completed bool
	notLoggedIn        bool
}

func (s *claudeDocumentState) consume(raw json.RawMessage) error {
	if !utf8.Valid(raw) || len(raw) == 0 || len(raw) > maxLine || !json.Valid(raw) || s.completed {
		return ErrClaudeDocumentProtocol
	}
	var record struct {
		Type       string            `json:"type"`
		Subtype    string            `json:"subtype"`
		SessionID  string            `json:"session_id"`
		Tools      []string          `json:"tools"`
		MCPServers []json.RawMessage `json:"mcp_servers"`
		Error      string            `json:"error"`
		IsError    bool              `json:"is_error"`
		Result     *string           `json:"result"`
		Structured json.RawMessage   `json:"structured_output"`
		Usage      struct {
			Input         *int64 `json:"input_tokens"`
			Output        *int64 `json:"output_tokens"`
			CacheCreation int64  `json:"cache_creation_input_tokens"`
			CacheRead     int64  `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &record) != nil {
		return ErrClaudeDocumentProtocol
	}
	switch record.Type {
	case "system":
		if record.Subtype != "init" {
			// Status notices (compaction, hooks off, retries) carry nothing for the document.
			return nil
		}
		if s.started || !ValidSessionID(record.SessionID) || len(record.MCPServers) != 0 {
			return ErrClaudeDocumentProtocol
		}
		for _, tool := range record.Tools {
			if tool != claudeStructuredOutputTool {
				return ErrClaudeDocumentProtocol
			}
		}
		s.started, s.result.ThreadID = true, record.SessionID
	case "assistant", "user":
		if !s.started || record.SessionID != s.result.ThreadID {
			return ErrClaudeDocumentProtocol
		}
		if record.Error == "authentication_failed" {
			s.notLoggedIn = true
		}
		// A model may still try a tool by name (often a Harflex tool from the conversation). The CLI announced only
		// StructuredOutput at init, so it refuses the call itself and the model answers again; nothing runs.
	case "result":
		if !s.started || record.SessionID != s.result.ThreadID {
			return ErrClaudeDocumentProtocol
		}
		s.completed = true
		if record.IsError || record.Subtype != "success" {
			return nil
		}
		text := ""
		if trimmed := bytes.TrimSpace(record.Structured); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
			var compact bytes.Buffer
			if json.Compact(&compact, trimmed) != nil {
				return ErrClaudeDocumentProtocol
			}
			text = compact.String()
		} else if record.Result != nil {
			text = strings.TrimSpace(*record.Result)
		}
		if !utf8.ValidString(text) || len(text) > s.limit {
			return ErrOutputLimitExceeded
		}
		s.result.Text = text
		if record.Usage.Input != nil && record.Usage.Output != nil {
			input := *record.Usage.Input + record.Usage.CacheCreation + record.Usage.CacheRead
			if input < 0 || *record.Usage.Output < 0 {
				return ErrClaudeDocumentProtocol
			}
			s.result.Usage = agentcore.Usage{InputTokens: input, OutputTokens: *record.Usage.Output}
			s.result.UsageKnown = true
		}
	default:
		// stream_event, rate_limit_event and other notices are not part of the answer.
	}
	return nil
}
