package catalog

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// CLIModelSelection reports whether the selection comes from a local CLI catalog. Those carry a
// Status of "listed" in SDD sessions too, but their effort levels are checked against the CLI
// itself, not against the evidence an API model stores.
func CLIModelSelection(selection ModelSelection) bool {
	return selection.Source == "codex_app_server" || selection.Source == "opencode_cli" || selection.Source == "claude_cli"
}

// ValidAPIReasoningEffort requires evidence captured from the exact verified
// catalog model. Automatic has no override and needs no advertised levels.
func ValidAPIReasoningEffort(selection ModelSelection) bool {
	if selection.ReasoningEffort == "" {
		return true
	}
	valid := func(value string) bool {
		return value != "" && len(value) <= 64 && utf8.ValidString(value) && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
	}
	if !valid(selection.ReasoningEffort) || len(selection.SupportedReasoningEfforts) == 0 || len(selection.SupportedReasoningEfforts) > 32 {
		return false
	}
	found := false
	for _, effort := range selection.SupportedReasoningEfforts {
		if !valid(effort) {
			return false
		}
		found = found || effort == selection.ReasoningEffort
	}
	return found
}
