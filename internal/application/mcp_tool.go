package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	mcpclient "github.com/persioflexa/harflex/internal/mcp"
	"github.com/persioflexa/harflex/internal/security"
	"github.com/persioflexa/harflex/internal/tools"
)

type mcpTool struct {
	service *Service
	server  catalog.MCPServer
	tool    catalog.MCPTool
}

var _ tools.Tool = mcpTool{}

func (t mcpTool) Spec() agentcore.ToolSpec {
	return agentcore.ToolSpec{Name: t.tool.AgentName, Description: "MCP " + t.server.Name + " · " + t.tool.Description, Schema: append(json.RawMessage(nil), t.tool.Schema...)}
}
func (t mcpTool) Risk() security.Risk {
	if t.server.Transport == "stdio" {
		return security.Shell
	}
	return security.Network
}
func (t mcpTool) Execute(ctx context.Context, args json.RawMessage, _ agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	current, err := t.service.store.GetMCPServer(ctx, t.server.ID)
	if err != nil || !current.Enabled || !current.UpdatedAt.Equal(t.server.UpdatedAt) {
		return agentcore.ToolExecutionResult{}, errors.New("MCP server configuration changed; start a new session")
	}
	cfg, err := t.service.mcpConfig(ctx, current)
	if err != nil {
		return agentcore.ToolExecutionResult{}, errors.New("MCP credential unavailable")
	}
	forms := mcpSecretForms(cfg.AuthScheme, cfg.Token)
	result, err := mcpclient.Call(ctx, cfg, t.tool.Name, args)
	cfg.Token = ""
	if err != nil {
		return agentcore.ToolExecutionResult{}, errors.New("MCP tool invocation failed")
	}
	// A server that fails a call may repeat what it received; the credential must not reach the model through it.
	encoded, err := json.Marshal(map[string]any{"text": scrubMCPSecrets(result.Text, forms)})
	if err != nil {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("encode MCP result: %w", err)
	}
	if result.IsError {
		// The server understood the call and said no (a branch that already exists, a missing field, a pull request
		// that is already open). Its answer is what the model needs to correct course, so the run goes on.
		failure := &agentcore.ToolFailure{Code: "mcp_error", Message: "the MCP server reported an error; its answer is in result"}
		return agentcore.ToolExecutionResult{Content: encoded}, failure.WithCause(errors.New("MCP tool reported failure"))
	}
	details, _ := json.Marshal(map[string]string{"server": t.server.Name, "tool": t.tool.Name})
	return agentcore.ToolExecutionResult{Content: encoded, Details: details}, nil
}
