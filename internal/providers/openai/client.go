package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
)

type Config struct {
	ID           string
	ProviderType string
	BaseURL      string
	APIKey       func(context.Context) (string, error)
	HTTPClient   *http.Client
}

type Client struct {
	id           string
	providerType string
	endpoint     string
	apiKey       func(context.Context) (string, error)
	http         *http.Client
}

var _ agentcore.Provider = (*Client)(nil)

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.ID) == "" {
		return nil, errors.New("openai: provider ID is required")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base == nil || (base.Scheme != "https" && base.Scheme != "http") || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return nil, errors.New("openai: invalid base URL")
	}
	if cfg.APIKey == nil {
		return nil, errors.New("openai: API key callback is required")
	}
	escapedPath := strings.TrimRight(base.EscapedPath(), "/") + "/chat/completions"
	base.Path = strings.TrimRight(base.Path, "/") + "/chat/completions"
	base.RawPath = escapedPath
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Transport: http.DefaultTransport}
	} else {
		cloned := *hc
		hc = &cloned
	}
	// Provider endpoints must not redirect credential-bearing requests.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{id: cfg.ID, providerType: cfg.ProviderType, endpoint: base.String(), apiKey: cfg.APIKey, http: hc}, nil
}

func (c *Client) ID() string { return c.id }
func (*Client) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}

func (c *Client) Stream(ctx context.Context, request agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	events := make(chan agentcore.StreamEvent, 8)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if err := c.stream(ctx, request, events); err != nil {
			// A single terminal error always fits, even when the consumer has stopped.
			select {
			case errs <- err:
			case <-ctx.Done():
				errs <- fmt.Errorf("openai stream: %w", ctx.Err())
			}
		}
	}()
	return events, errs
}

type function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}
type toolCall struct {
	ID       string   `json:"id,omitempty"`
	Type     string   `json:"type"`
	Function function `json:"function"`
}
type message struct {
	Role       agentcore.Role `json:"role"`
	Content    *string        `json:"content,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall     `json:"tool_calls,omitempty"`
}
type chatRequest struct {
	ReasoningEffort     string        `json:"reasoning_effort,omitempty"`
	Model               string        `json:"model"`
	MaxCompletionTokens int           `json:"max_completion_tokens,omitempty"`
	MaxTokens           int           `json:"max_tokens,omitempty"`
	Messages            []message     `json:"messages"`
	Tools               []toolCall    `json:"tools,omitempty"`
	Stream              bool          `json:"stream"`
	StreamOptions       streamOptions `json:"stream_options"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func objectJSON(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{' && utf8.Valid(raw) && json.Valid(raw)
}

// RequestSize measures the same UTF-8 JSON body sent by Stream without network
// access. Callers account for tokenizer framing separately.
func RequestSize(req agentcore.ChatRequest, providerType string) (int, error) {
	data, err := encodeRequest(req, providerType)
	return len(data), err
}

func encodeRequest(req agentcore.ChatRequest, providerType string) ([]byte, error) {
	mapped := chatRequest{Model: req.Model, ReasoningEffort: req.ReasoningEffort, Messages: make([]message, 0, len(req.Messages)), Stream: true, StreamOptions: streamOptions{IncludeUsage: true}}
	if req.MaxOutputTokens < 0 {
		return nil, errors.New("openai: output limit must be positive")
	}
	if req.MaxOutputTokens > 0 {
		switch providerType {
		case "openai", "openrouter":
			mapped.MaxCompletionTokens = req.MaxOutputTokens
		case "ollama", "lm_studio":
			mapped.MaxTokens = req.MaxOutputTokens
		default:
			return nil, errors.New("openai: output limit unsupported for provider")
		}
	}
	for _, msg := range req.Messages {
		out := message{Role: msg.Role, ToolCallID: msg.ToolCallID}
		if msg.Role != agentcore.RoleAssistant || msg.Content != "" || len(msg.ToolCalls) == 0 {
			content := msg.Content
			out.Content = &content
		}
		for _, call := range msg.ToolCalls {
			if !objectJSON(call.Arguments) {
				return nil, errors.New("openai: tool arguments must be a valid JSON object")
			}
			out.ToolCalls = append(out.ToolCalls, toolCall{ID: call.ID, Type: "function", Function: function{Name: call.Name, Arguments: string(call.Arguments)}})
		}
		mapped.Messages = append(mapped.Messages, out)
	}
	for _, tool := range req.Tools {
		if !objectJSON(tool.Schema) {
			return nil, errors.New("openai: tool schema must be a valid JSON object")
		}
		mapped.Tools = append(mapped.Tools, toolCall{Type: "function", Function: function{Name: tool.Name, Description: tool.Description, Parameters: tool.Schema}})
	}
	data, err := json.Marshal(mapped)
	if err != nil {
		return nil, errors.New("openai: invalid request encoding")
	}
	return data, nil
}

func (c *Client) stream(ctx context.Context, request agentcore.ChatRequest, events chan<- agentcore.StreamEvent) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("openai request: %w", err)
	}
	data, err := encodeRequest(request, c.providerType)
	if err != nil {
		return err
	}
	key, err := c.apiKey(ctx)
	if err != nil {
		return fmt.Errorf("openai API key: %w", safeCause{err})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("openai request: %w", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("openai HTTP request: %w", safeCause{err})
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8193))
		truncated := len(body) > 8192
		detail := string(body)
		if key != "" {
			detail = strings.ReplaceAll(detail, key, "[REDACTED]")
			// The bounded read can end inside a credential; redact its prefix too.
			if truncated {
				for size := min(len(key)-1, len(detail)); size > 0; size-- {
					if strings.HasSuffix(detail, key[:size]) {
						detail = strings.TrimSuffix(detail, key[:size]) + "[REDACTED]"
						break
					}
				}
			}
		}
		// Headers are never included in provider error details.
		detail = strings.ReplaceAll(detail, "Authorization", "[REDACTED]")
		if len(detail) > 8192 {
			detail = detail[:8192]
			truncated = true
		}
		if truncated {
			detail += " [truncated]"
		}
		if readErr != nil {
			return fmt.Errorf("openai HTTP status %d: %s: %w", resp.StatusCode, detail, safeCause{readErr})
		}
		return fmt.Errorf("openai HTTP status %d: %s", resp.StatusCode, detail)
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return errors.New("openai: unexpected response content type")
	}
	send := func(event agentcore.StreamEvent) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- event:
			return nil
		}
	}
	if err := consumeSSE(resp.Body, send); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return fmt.Errorf("openai stream: %w", err)
	}
	return nil
}

// Preserve errors.Is/errors.As without exposing credential-bearing diagnostics.
type safeCause struct{ cause error }

func (safeCause) Error() string   { return "operation failed" }
func (e safeCause) Unwrap() error { return e.cause }
