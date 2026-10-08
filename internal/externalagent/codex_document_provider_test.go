package externalagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
)

func TestDocumentProviderContract(t *testing.T) {
	provider := NewDocumentProvider(nil, DocumentRequest{})
	if provider.ID() != "codex" || !reflect.DeepEqual(provider.Capabilities(), agentcore.Capabilities{Streaming: true, ToolCalls: true, Reasoning: true}) {
		t.Fatal("document provider did not expose host-mediated tool calls")
	}
	es, errs := provider.Stream(context.Background(), agentcore.ChatRequest{})
	for range es {
	}
	if err := <-errs; err == nil {
		t.Fatal("document provider admitted a missing generator")
	}
}

type documentGeneratorFunc func(context.Context, DocumentRequest, func(Event)) (DocumentResult, error)

func (f documentGeneratorFunc) GenerateDocument(ctx context.Context, request DocumentRequest, progress func(Event)) (DocumentResult, error) {
	return f(ctx, request, progress)
}

func documentProviderRequestFixture(t *testing.T) (DocumentRequest, agentcore.ChatRequest) {
	t.Helper()
	base := codexDocumentRequestFixture(t)
	request := agentcore.ChatRequest{Model: base.Model, Messages: []agentcore.Message{{Role: agentcore.RoleSystem, Content: "Preserve the requested final JSON."}, {Role: agentcore.RoleUser, Content: "Implement only the selected workspace."}}, Tools: []agentcore.ToolSpec{{Name: "read", Description: "Read a guarded file", Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"limit":{"type":"integer"}},"required":["path"],"additionalProperties":false}`)}}}
	return base, request
}

func collectDocumentProvider(t *testing.T, ctx context.Context, provider agentcore.Provider, request agentcore.ChatRequest) ([]agentcore.StreamEvent, error) {
	t.Helper()
	es, errs := provider.Stream(ctx, request)
	var got []agentcore.StreamEvent
	for e := range es {
		got = append(got, e)
	}
	var failure error
	for err := range errs {
		failure = errors.Join(failure, err)
	}
	return got, failure
}

func TestDocumentProviderPreservesFinalReplyAndOmitsRawEnvelope(t *testing.T) {
	base, request := documentProviderRequestFixture(t)
	final := "  {\"status\":\"approved\"}\n"
	encoded, _ := json.Marshal(map[string]any{"reply": final, "toolCalls": []any{}})
	provider := NewDocumentProvider(documentGeneratorFunc(func(_ context.Context, document DocumentRequest, progress func(Event)) (DocumentResult, error) {
		if progress != nil {
			t.Error("raw JSON progress must not enter the human conversation")
		}
		var payload struct {
			Messages []agentcore.Message  `json:"messages"`
			Tools    []agentcore.ToolSpec `json:"tools"`
		}
		if json.Unmarshal([]byte(document.Prompt), &payload) != nil || !reflect.DeepEqual(payload.Messages, request.Messages) || len(payload.Tools) != 1 || payload.Tools[0].Name != "read" {
			t.Error("host messages or allowed tools were changed")
		}
		if document.ExpectedExecutablePath != base.ExpectedExecutablePath || document.ExpectedExecutableVersion != base.ExpectedExecutableVersion || document.Model != base.Model || !strings.Contains(document.SystemPrompt, "Never use your own built-in tools") || !strings.Contains(document.SystemPrompt, "the commands you send through it are authorized") {
			t.Error("frozen document selection or host-only instruction was lost")
		}
		return DocumentResult{Text: string(encoded), UsageKnown: true, Usage: agentcore.Usage{InputTokens: 7, OutputTokens: 3}}, nil
	}), base)
	events, err := collectDocumentProvider(t, t.Context(), provider, request)
	if err != nil || len(events) != 2 || events[0].Type != "text_delta" || events[0].Delta != final || events[0].Raw != nil || events[1].Type != "usage" || events[1].Usage.InputTokens != 7 {
		t.Fatalf("exact final reply or usage lost: events=%+v err=%v", events, err)
	}
}

func TestDocumentProviderUsesHostIDsAndOmitsOptionalNull(t *testing.T) {
	base, request := documentProviderRequestFixture(t)
	provider := NewDocumentProvider(documentGeneratorFunc(func(context.Context, DocumentRequest, func(Event)) (DocumentResult, error) {
		return DocumentResult{Text: `{"reply":"","toolCalls":[{"name":"read","arguments":{"path":"index.html","limit":null}},{"name":"read","arguments":{"path":"README.md","limit":10}}]}`}, nil
	}), base)
	first, err := collectDocumentProvider(t, t.Context(), provider, request)
	if err != nil || len(first) != 2 || first[0].Type != "tool_call" || first[1].Type != "tool_call" || first[0].ToolCall.ID == first[1].ToolCall.ID || first[0].ToolCall.ID == "" || !ValidSessionID(first[0].ToolCall.ID) {
		t.Fatalf("host tool IDs missing: events=%+v err=%v", first, err)
	}
	if string(first[0].ToolCall.Arguments) != `{"path":"index.html"}` {
		t.Fatal("nullable optional field did not become an omitted host argument")
	}
	second, err := collectDocumentProvider(t, t.Context(), provider, request)
	if err != nil || second[0].ToolCall.ID == first[0].ToolCall.ID {
		t.Fatal("tool IDs were reused across generation turns")
	}
}

func TestDocumentProviderRejectsUnsafeEnvelopeBeforePublishing(t *testing.T) {
	base, request := documentProviderRequestFixture(t)
	for _, raw := range []string{
		`{"reply":"must not publish","toolCalls":[{"name":"exec_command","arguments":{"cmd":"unsafe"}}]}`,
		`{"reply":"must not publish","toolCalls":[{"name":"read","arguments":[]}]}`,
		`{"reply":"must not publish","toolCalls":[{"name":"read","arguments":null}]}`,
		`{"reply":"must not publish","toolCalls":[{"id":"chosen-by-model","name":"read","arguments":{"path":"index.html"}}]}`,
		`{"reply":"must not publish","toolCalls":null}`,
		`{"reply":"must not publish"}`,
		`{"reply":"must not publish","toolCalls":[],"extra":"unexpected"}`,
		`{"reply":"","toolCalls":[]}`,
	} {
		provider := NewDocumentProvider(documentGeneratorFunc(func(context.Context, DocumentRequest, func(Event)) (DocumentResult, error) {
			return DocumentResult{Text: raw}, nil
		}), base)
		events, err := collectDocumentProvider(t, t.Context(), provider, request)
		if !errors.Is(err, ErrDocumentProviderOutput) || len(events) != 0 {
			t.Fatalf("unsafe envelope published: events=%d err=%v", len(events), err)
		}
	}
}

func TestDocumentProviderCapsCallsAndArguments(t *testing.T) {
	base, request := documentProviderRequestFixture(t)
	base.MaxAssistantOutputBytes = maxLine
	var calls []any
	for range maxDocumentToolCalls + 1 {
		calls = append(calls, map[string]any{"name": "read", "arguments": map[string]string{"path": "index.html"}})
	}
	overflow, _ := json.Marshal(map[string]any{"reply": "", "toolCalls": calls})
	oversized, _ := json.Marshal(map[string]any{"reply": "", "toolCalls": []any{map[string]any{"name": "read", "arguments": map[string]string{"path": strings.Repeat("x", maxDocumentToolArgumentBytes+1)}}}})
	for _, raw := range [][]byte{overflow, oversized} {
		provider := NewDocumentProvider(documentGeneratorFunc(func(context.Context, DocumentRequest, func(Event)) (DocumentResult, error) {
			return DocumentResult{Text: string(raw)}, nil
		}), base)
		events, err := collectDocumentProvider(t, t.Context(), provider, request)
		if !errors.Is(err, ErrDocumentProviderOutput) || len(events) != 0 {
			t.Fatal("unbounded declarative tool response was published")
		}
	}
}

func TestDocumentProviderRejectsUnfrozenModelAndCancelledInput(t *testing.T) {
	base, request := documentProviderRequestFixture(t)
	called := false
	provider := NewDocumentProvider(documentGeneratorFunc(func(context.Context, DocumentRequest, func(Event)) (DocumentResult, error) {
		called = true
		return DocumentResult{}, nil
	}), base)
	request.Model = "different-model"
	if _, err := collectDocumentProvider(t, t.Context(), provider, request); !errors.Is(err, ErrDocumentProviderInput) || called {
		t.Fatal("model changed after the host froze the selection")
	}
	request.Model = base.Model
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := collectDocumentProvider(t, ctx, provider, request); !errors.Is(err, context.Canceled) || called {
		t.Fatal("cancelled request launched a generator")
	}
}

func TestDocumentProviderCancellationJoinsGenerator(t *testing.T) {
	base, request := documentProviderRequestFixture(t)
	joined := make(chan struct{})
	provider := NewDocumentProvider(documentGeneratorFunc(func(ctx context.Context, _ DocumentRequest, _ func(Event)) (DocumentResult, error) {
		defer close(joined)
		<-ctx.Done()
		return DocumentResult{}, ctx.Err()
	}), base)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := collectDocumentProvider(t, ctx, provider, request)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("provider stream closed before its generator joined")
	}
}

func TestDocumentProviderSchemaClosesEveryObject(t *testing.T) {
	base, request := documentProviderRequestFixture(t)
	provider := NewDocumentProvider(documentGeneratorFunc(func(context.Context, DocumentRequest, func(Event)) (DocumentResult, error) {
		return DocumentResult{}, nil
	}), base).(*documentProvider)
	document, _, err := provider.prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(document.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	var inspect func(any)
	inspect = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if node["type"] == "object" {
				props, _ := node["properties"].(map[string]any)
				required, _ := node["required"].([]any)
				if node["additionalProperties"] != false || len(props) != len(required) {
					t.Fatal("structured response object is not closed and complete")
				}
			}
			for _, child := range node {
				inspect(child)
			}
		case []any:
			for _, child := range node {
				inspect(child)
			}
		}
	}
	inspect(schema)
	if !strings.Contains(string(document.OutputSchema), `"type":"null"`) || strings.Contains(string(document.OutputSchema), `"default"`) {
		t.Fatal(fmt.Errorf("optional tool field did not receive a strict nullable schema"))
	}
}

func TestDocumentProviderSendsReadImagesAsNotesAndFitsLongRuns(t *testing.T) {
	base, request := documentProviderRequestFixture(t)
	// Two screenshots the QA read, as the read tool returns them, larger together than one CLI turn.
	shot := func(size int) string {
		content, _ := json.Marshal(map[string]string{"mime": "image/png", "data": base64.StdEncoding.EncodeToString(make([]byte, size))})
		return string(content)
	}
	log := strings.Repeat("ok 1 - passou\n", 30000)
	request.Messages = append(request.Messages,
		agentcore.Message{Role: agentcore.RoleTool, ToolCallID: "shot-1", Content: shot(307131)},
		agentcore.Message{Role: agentcore.RoleTool, ToolCallID: "shot-2", Content: shot(104000)},
		agentcore.Message{Role: agentcore.RoleTool, ToolCallID: "log-1", Content: log},
		agentcore.Message{Role: agentcore.RoleTool, ToolCallID: "log-2", Content: log})
	var sent []agentcore.Message
	encoded, _ := json.Marshal(map[string]any{"reply": "{}", "toolCalls": []any{}})
	provider := NewDocumentProvider(documentGeneratorFunc(func(_ context.Context, document DocumentRequest, _ func(Event)) (DocumentResult, error) {
		var payload struct {
			Messages []agentcore.Message `json:"messages"`
		}
		_ = json.Unmarshal([]byte(document.Prompt), &payload)
		sent = payload.Messages
		return DocumentResult{Text: string(encoded)}, nil
	}), base)
	if _, err := collectDocumentProvider(t, t.Context(), provider, request); err != nil {
		t.Fatalf("a run with screenshots and long output did not fit: %v", err)
	}
	byID := map[string]string{}
	for _, message := range sent {
		byID[message.ToolCallID] = message.Content
	}
	if strings.Contains(byID["shot-1"], "AAAA") || !strings.Contains(byID["shot-1"], `"bytes":307131`) || !strings.Contains(byID["shot-1"], "image/png") {
		t.Fatalf("the screenshot went encoded: %.200s", byID["shot-1"])
	}
	if len(byID["log-1"]) > 4096 || byID["log-2"] != log {
		t.Fatalf("the oldest output was not the one shortened: %d %d", len(byID["log-1"]), len(byID["log-2"]))
	}
	if request.Messages[len(request.Messages)-4].Content != shot(307131) {
		t.Fatal("the host conversation was changed")
	}
}
