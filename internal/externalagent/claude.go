package externalagent

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
)

// claudeReasoningEfforts are the levels `claude --effort` accepts.
var claudeReasoningEfforts = []string{"low", "medium", "high", "xhigh", "max"}

func validClaudeReasoningEffort(value string) bool {
	for _, effort := range claudeReasoningEfforts {
		if value == effort {
			return true
		}
	}
	return false
}

// NewClaudeCode creates the Claude Code adapter; an empty path uses known install locations, then PATH.
// The prompt goes through stdin. Edits inside the working directory are accepted; any other tool that would
// ask for permission is refused, because a print-mode run has nobody to answer it.
func NewClaudeCode(path string) Adapter {
	return &cliAdapter{id: "claude", path: path, resumable: true, newNormalizer: newClaudeNormalizer, build: func(r Request) ([]string, string) {
		args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"}
		if r.Model != "" {
			args = append(args, "--model", r.Model)
		}
		if r.ReasoningEffort != "" {
			args = append(args, "--effort", r.ReasoningEffort)
		}
		if strings.TrimSpace(r.SystemPrompt) != "" {
			args = append(args, "--append-system-prompt", r.SystemPrompt)
		}
		if r.HarflexMCPURL != "" && r.HarflexMCPToken != "" {
			// Claude Code expands the variable from its environment, so the token never appears in the arguments.
			config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"harflex": map[string]any{
				"type": "http", "url": r.HarflexMCPURL, "headers": map[string]string{"Authorization": "Bearer ${" + HarflexMCPTokenEnv + "}"}}}})
			args = append(args, "--mcp-config", string(config), "--allowedTools", "mcp__harflex")
		}
		if r.SessionID != "" {
			args = append(args, "--resume", r.SessionID)
		}
		return args, r.Prompt
	}}
}

// claudeDesktopCandidates finds the Claude Code build that the Claude desktop app keeps for itself,
// newest version first. It is used only when no standalone install is found.
func claudeDesktopCandidates(home string) []string {
	if home == "" {
		return nil
	}
	found, _ := filepath.Glob(filepath.Join(home, "Library", "Application Support", "Claude", "claude-code", "*", "*", "claude.app", "Contents", "MacOS", "claude"))
	sort.Slice(found, func(i, j int) bool {
		return compareDottedVersions(claudeDesktopVersion(found[i]), claudeDesktopVersion(found[j])) > 0
	})
	return found
}

func claudeDesktopVersion(path string) string {
	// .../claude-code/<version>/<build>/claude.app/Contents/MacOS/claude
	return filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path))))))
}

func compareDottedVersions(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(left), len(right)); i++ {
		var x, y int
		if i < len(left) {
			x = leadingNumber(left[i])
		}
		if i < len(right) {
			y = leadingNumber(right[i])
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return strings.Compare(a, b)
}

func leadingNumber(value string) int {
	n := 0
	for _, r := range value {
		if r < '0' || r > '9' || n > 1_000_000 {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func newClaudeNormalizer() eventNormalizer {
	var sessionID string
	// One assistant message can arrive as several events, one per content block, sharing the message ID.
	texts := make(map[string][]string)
	return func(line []byte) Event {
		e, obj := rawProtocolEvent(line)
		if id := validProtocolID(obj.string("session_id")); id != "" {
			sessionID = id
		}
		e.SessionID = sessionID
		if obj.string("type") != "assistant" {
			return e
		}
		message := obj.object("message")
		var blocks []protocolObject
		if raw, ok := message["content"]; !ok || json.Unmarshal(raw, &blocks) != nil {
			return e
		}
		var parts []string
		for _, block := range blocks {
			if block.string("type") != "text" {
				continue
			}
			if value, ok := block.text(); ok && value != "" {
				parts = append(parts, value)
			}
		}
		if len(parts) == 0 {
			return e
		}
		id := validProtocolID(message.string("id"))
		if id == "" {
			e.Type, e.Text = "assistant.message", strings.Join(parts, "\n\n")
			return e
		}
		texts[id] = append(texts[id], parts...)
		value := strings.Join(texts[id], "\n\n")
		if len(value) > maxLine {
			return e
		}
		e.Type, e.Text, e.MessageID, e.Mode = "assistant.message", value, id, "replace"
		return e
	}
}
