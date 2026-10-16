package externalagent

import (
	"encoding/json"

	"github.com/persioflexa/harflex/internal/agentcore"
)

// RunUsage reads the token totals a CLI reports for a whole turn: Claude Code's result record and Codex's
// turn.completed. Per-message counts are left out, so a turn is never counted twice. Input includes cached tokens,
// as in the document runs.
func RunUsage(raw json.RawMessage) (agentcore.Usage, bool) {
	var record struct {
		Type  string `json:"type"`
		Usage *struct {
			Input         *int64 `json:"input_tokens"`
			Output        *int64 `json:"output_tokens"`
			CacheCreation int64  `json:"cache_creation_input_tokens"`
			CacheRead     int64  `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &record) != nil || record.Usage == nil || record.Usage.Input == nil || record.Usage.Output == nil {
		return agentcore.Usage{}, false
	}
	var input int64
	switch record.Type {
	case "result":
		input = *record.Usage.Input + record.Usage.CacheCreation + record.Usage.CacheRead
	case "turn.completed":
		// Codex's input_tokens already counts the cached ones.
		input = *record.Usage.Input
	default:
		return agentcore.Usage{}, false
	}
	if input < 0 || *record.Usage.Output < 0 {
		return agentcore.Usage{}, false
	}
	return agentcore.Usage{InputTokens: input, OutputTokens: *record.Usage.Output}, true
}
