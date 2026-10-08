package agentcore

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Message struct {
	ID         string          `json:"id"`
	Role       Role            `json:"role"`
	Content    string          `json:"content"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolCalls  []ToolCall      `json:"toolCalls,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type Capabilities struct {
	Streaming bool `json:"streaming"`
	ToolCalls bool `json:"toolCalls"`
	Images    bool `json:"images"`
	Reasoning bool `json:"reasoning"`
	Resumable bool `json:"resumable"`
}

type ChatRequest struct {
	ReasoningEffort string     `json:"reasoningEffort,omitempty"`
	Model           string     `json:"model"`
	Messages        []Message  `json:"messages"`
	Tools           []ToolSpec `json:"tools,omitempty"`
	MaxOutputTokens int        `json:"maxOutputTokens,omitempty"`
}

type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

type StreamEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta,omitempty"`
	ToolCall *ToolCall       `json:"toolCall,omitempty"`
	Usage    *Usage          `json:"usage,omitempty"`
	Raw      json.RawMessage `json:"raw,omitempty"`
}

type Provider interface {
	ID() string
	Capabilities() Capabilities
	Stream(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error)
}

type ToolUpdate struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// ToolUpdateSink is a trusted internal sink, not an extension boundary. It must
// return promptly and use the supplied effective execution context for blocking
// work. Ignoring this context can block execution indefinitely.
type ToolUpdateSink func(context.Context, ToolUpdate)

type ToolExecutionResult struct {
	Content json.RawMessage `json:"content"`
	Details json.RawMessage `json:"details,omitempty"`
}

type ToolExecutor interface {
	Specs() []ToolSpec
	Risk(name string) string
	Execute(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error)
}
