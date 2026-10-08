package externalagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
)

type Request struct {
	Prompt, CWD, Model, ReasoningEffort, SessionID, SystemPrompt string
	Policy                                                       string
	Sandbox                                                      string
	IgnoreUserConfig, Ephemeral, ApproveForMe                    bool
	MaxAssistantOutputBytes                                      int
	// Harflex's own MCP server for this conversation (its tools act on the platform). The token reaches the CLI
	// only through the environment, never its arguments.
	HarflexMCPURL, HarflexMCPToken string
}

// HarflexMCPTokenEnv carries the conversation's token to the CLI, which sends it as a bearer token.
const HarflexMCPTokenEnv = "HARFLEX_MCP_TOKEN"

type Event struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Raw       json.RawMessage `json:"raw"`
	SessionID string          `json:"sessionId,omitempty"`
	MessageID string          `json:"messageId,omitempty"`
	Mode      string          `json:"mode,omitempty"`
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:][A-Za-z0-9._:-]{0,255}$`)

func ValidSessionID(value string) bool { return sessionIDPattern.MatchString(value) }

type Detection struct {
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
}
type Adapter interface {
	ID() string
	Detect() Detection
	Capabilities() agentcore.Capabilities
	Run(context.Context, Request) (<-chan Event, <-chan error)
}
type cliAdapter struct {
	id, path      string
	resumable     bool
	build         func(Request) ([]string, string)
	newNormalizer func() eventNormalizer
}

func (a *cliAdapter) ID() string { return a.id }
func (a *cliAdapter) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, Resumable: a.resumable}
}
func executableCandidates(id, platform, explicit, home string) []string {
	if explicit != "" {
		return []string{explicit}
	}
	if (id == "codex" || id == "opencode" || id == "claude") && platform == "darwin" {
		paths := []string{"/opt/homebrew/bin/" + id}
		if home != "" {
			paths = append(paths, filepath.Join(home, ".local", "bin", id))
			if id == "claude" {
				paths = append(paths, filepath.Join(home, ".claude", "local", "claude"))
			}
		}
		return append(paths, id)
	}
	return []string{id}
}
func (a *cliAdapter) executable() (string, error) {
	home, _ := os.UserHomeDir()
	candidates := executableCandidates(a.id, runtime.GOOS, a.path, home)
	if a.id == "opencode" && a.path == "" {
		candidates = append(candidates, openCodeNativePackageCandidates(home)...)
	}
	if a.id == "claude" && a.path == "" && runtime.GOOS == "darwin" {
		candidates = append(candidates, claudeDesktopCandidates(home)...)
	}
	for _, path := range candidates {
		resolved, err := exec.LookPath(path)
		if err != nil {
			continue
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err == nil && info.Mode().IsRegular() && (a.id != "opencode" || nativeExecutable(resolved)) {
			return resolved, nil
		}
	}
	return "", errors.New("external agent executable unavailable")
}

func openCodeNativePackageCandidates(home string) []string {
	if home == "" {
		return nil
	}
	name := "opencode"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	packageName := "opencode-" + runtime.GOOS + "-" + runtime.GOARCH + "*"
	patterns := []string{
		filepath.Join(home, ".asdf", "installs", "nodejs", "*", "lib", "node_modules", "opencode-ai", "node_modules", packageName, "bin", name),
		filepath.Join(home, ".nvm", "versions", "node", "*", "lib", "node_modules", "opencode-ai", "node_modules", packageName, "bin", name),
	}
	var found []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		found = append(found, matches...)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(found)))
	return found
}

func nativeExecutable(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var signature [4]byte
	if _, err := io.ReadFull(file, signature[:]); err != nil {
		return false
	}
	if bytes.Equal(signature[:4], []byte{0x7f, 'E', 'L', 'F'}) || bytes.Equal(signature[:2], []byte{'M', 'Z'}) {
		return true
	}
	switch signature {
	case [4]byte{0xfe, 0xed, 0xfa, 0xce}, [4]byte{0xce, 0xfa, 0xed, 0xfe},
		[4]byte{0xfe, 0xed, 0xfa, 0xcf}, [4]byte{0xcf, 0xfa, 0xed, 0xfe},
		[4]byte{0xca, 0xfe, 0xba, 0xbe}, [4]byte{0xbe, 0xba, 0xfe, 0xca}:
		return true
	}
	return false
}
func (a *cliAdapter) Detect() Detection {
	if a.id == "opencode" && a.path == "" {
		// The default local installation has not passed an isolated positive
		// plugin-control attestation. Do not claim safe CLI availability.
		return Detection{}
	}
	path, err := a.executable()
	if err != nil {
		return Detection{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.Command(path, "--version")
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Env = buildEnvironment(a.id, os.Environ(), runtime.GOOS)
	output := &limitedBuffer{limit: 8192}
	cmd.Stdout = output
	cmd.Stderr = output
	owned, err := ownCommand(cmd)
	if err != nil {
		return Detection{}
	}
	defer owned.close()
	if err = owned.start(); err != nil {
		return Detection{}
	}
	err = owned.wait(ctx)
	err = errors.Join(err, owned.close())
	if err != nil || ctx.Err() != nil || output.truncated {
		return Detection{}
	}
	return Detection{Available: true, Path: path, Version: strings.TrimSpace(strings.ToValidUTF8(output.String(), "�"))}
}
func (a *cliAdapter) Run(ctx context.Context, r Request) (<-chan Event, <-chan error) {
	es := make(chan Event, 8)
	errs := make(chan error, 1)
	go func() {
		defer close(errs)
		defer close(es)
		err := ctx.Err()
		if err == nil {
			err = validateRequest(r, a.id, a.resumable)
		}
		if err == nil {
			if a.id == "opencode" && a.path == "" {
				err = ErrOpenCodePureUnavailable
			}
		}
		if err == nil {
			if a.id == "opencode" && r.ReasoningEffort != "" {
				err = errors.New("OpenCode reasoning variant is unverified")
			}
		}
		if err == nil {
			var path string
			path, err = a.executable()
			if err == nil {
				if a.id == "opencode" {
					err = verifyOpenCodePure(ctx, path)
				}
			}
			if err == nil {
				args, input := a.build(r)
				normalizer := eventNormalizer(normalize)
				if a.newNormalizer != nil {
					normalizer = a.newNormalizer()
				}
				env := buildEnvironment(a.id, os.Environ(), runtime.GOOS)
				if r.HarflexMCPURL != "" && r.HarflexMCPToken != "" {
					env = append(env, HarflexMCPTokenEnv+"="+r.HarflexMCPToken)
				}
				err = runProcess(ctx, path, args, input, r, env, es, normalizer)
			}
		}
		if err != nil {
			errs <- err
		}
	}()
	return es, errs
}
func validateRequest(r Request, adapterID string, resumable bool) error {
	if r.SessionID != "" && !ValidSessionID(r.SessionID) {
		return errors.New("invalid external session ID")
	}
	if strings.HasPrefix(r.Model, "-") || strings.HasPrefix(r.SessionID, "-") {
		return errors.New("external agent option value must not begin with a hyphen")
	}
	if adapterID == "claude" && r.ReasoningEffort != "" && !validClaudeReasoningEffort(r.ReasoningEffort) {
		return errors.New("invalid Claude Code effort")
	}
	if len(r.ReasoningEffort) > 64 || !validCatalogText(r.ReasoningEffort, 64) || strings.HasPrefix(r.ReasoningEffort, "-") || strings.ContainsAny(r.ReasoningEffort, " =") {
		return errors.New("invalid external agent reasoning effort")
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return errors.New("external agent prompt is required")
	}
	if !filepath.IsAbs(r.CWD) {
		return errors.New("external agent working directory must be absolute")
	}
	info, err := os.Stat(r.CWD)
	if err != nil || !info.IsDir() {
		return errors.New("external agent working directory unavailable")
	}
	if r.SessionID != "" && !resumable {
		return errors.New("external agent resume is unsupported")
	}
	if r.Sandbox != "" && r.Sandbox != "read-only" && r.Sandbox != "workspace-write" {
		return errors.New("unsupported external agent sandbox")
	}
	if r.Policy != "" && (adapterID != "codex" || r.Policy != "sdd_code") {
		return errors.New("unsupported external agent execution policy")
	}
	if r.ApproveForMe && (adapterID != "codex" || r.Policy != "sdd_code" || r.Sandbox != "workspace-write") {
		return errors.New("automatic review is only available for isolated Code")
	}
	if r.MaxAssistantOutputBytes < 0 || r.MaxAssistantOutputBytes > 1024*1024 {
		return errors.New("invalid external assistant output limit")
	}
	if adapterID != "codex" && (r.Sandbox != "" || r.IgnoreUserConfig || r.Ephemeral || r.MaxAssistantOutputBytes > 0) {
		return errors.New("Codex execution policy used with a different CLI")
	}
	if r.MaxAssistantOutputBytes > 0 {
		readOnlyBounded := r.Sandbox == "read-only" && r.IgnoreUserConfig && r.Ephemeral && r.SessionID == ""
		isolatedCodeBounded := r.Policy == "sdd_code" && r.Sandbox == "workspace-write" && r.IgnoreUserConfig && !r.Ephemeral
		if !readOnlyBounded && !isolatedCodeBounded {
			return errors.New("bounded Codex execution must use a supported isolated policy")
		}
	}
	if r.Policy == "sdd_code" && (r.Sandbox != "workspace-write" || !r.IgnoreUserConfig || r.Ephemeral || !r.ApproveForMe) {
		return errors.New("isolated Code requires workspace-write, automatic review and a resumable thread")
	}
	return nil
}

// Authentication uses the CLI's local credential storage. API-key environment
// injection requires a future explicit keyring/service boundary; it is never inherited.
func buildEnvironment(adapter string, source []string, goos string) []string {
	canonical := func(name string) string {
		if goos == "windows" {
			return strings.ToUpper(name)
		}
		return name
	}
	allowed := make(map[string]bool)
	for _, name := range []string{"PATH", "HOME", "USERPROFILE", "TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL", "LC_CTYPE", "SHELL", "SystemRoot", "WINDIR", "ComSpec", "PATHEXT", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		allowed[canonical(name)] = true
	}
	if adapter == "codex" {
		allowed[canonical("CODEX_HOME")] = true
	}
	if adapter == "claude" {
		// The macOS keychain entry that holds the Claude Code login is keyed by USER; without it the CLI reads as logged out.
		allowed[canonical("USER")] = true
		allowed[canonical("CLAUDE_CONFIG_DIR")] = true
	}
	env := make([]string, 0, len(allowed)+1)
	positions := make(map[string]int)
	for _, entry := range source {
		name, _, ok := strings.Cut(entry, "=")
		key := canonical(name)
		if !ok || !allowed[key] {
			continue
		}
		if i, exists := positions[key]; exists {
			env[i] = entry
		} else {
			positions[key] = len(env)
			env = append(env, entry)
		}
	}
	if adapter == "claude" {
		env = append(env, "DISABLE_AUTOUPDATER=1")
	}
	if adapter == "opencode" {
		env = append(env, "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_LSP_DOWNLOAD=1", "OPENCODE_DISABLE_MODELS_FETCH=1")
	}
	return append(env, "NO_COLOR=1")
}
