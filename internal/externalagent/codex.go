package externalagent

import (
	"strconv"
	"strings"
)

// NewCodex creates the adapter; an empty path uses known desktop locations, then PATH.
func NewCodex(path string) Adapter {
	return &cliAdapter{id: "codex", path: path, resumable: true, newNormalizer: newCodexNormalizer, build: func(r Request) ([]string, string) {
		sandbox := r.Sandbox
		if sandbox == "" {
			sandbox = "workspace-write"
		}
		args := []string{"exec", "--json", "--color", "never"}
		if r.ApproveForMe {
			// Codex CLI defines --approve-for-me as a workspace-write sandbox
			// policy and rejects combining it with an explicit --sandbox value.
			args = append(args, "--approve-for-me")
		} else {
			args = append(args, "--sandbox", sandbox)
		}
		if r.IgnoreUserConfig {
			args = append(args, "--ignore-user-config")
		}
		if r.Ephemeral {
			args = append(args, "--ephemeral")
		}
		args = append(args, "-C", r.CWD, "--skip-git-repo-check")
		if r.Model != "" {
			args = append(args, "--model", r.Model)
		}
		if r.ReasoningEffort != "" {
			args = append(args, "-c", "model_reasoning_effort="+r.ReasoningEffort)
		}
		// The instructions go as developer instructions on every turn, so a resumed conversation gets the current ones.
		if strings.TrimSpace(r.SystemPrompt) != "" {
			args = append(args, "-c", "developer_instructions="+strconv.Quote(r.SystemPrompt))
		}
		if r.HarflexMCPURL != "" && r.HarflexMCPToken != "" {
			args = append(args, "-c", "mcp_servers.harflex.url="+strconv.Quote(r.HarflexMCPURL),
				"-c", "mcp_servers.harflex.bearer_token_env_var="+strconv.Quote(HarflexMCPTokenEnv),
				// codex exec cannot ask: Harflex's own tools are pre-approved; other servers keep their rules.
				"-c", `mcp_servers.harflex.default_tools_approval_mode="approve"`)
		}
		if r.SessionID != "" {
			args = append(args, "resume", r.SessionID)
		}
		return append(args, "-"), r.Prompt
	}}
}
