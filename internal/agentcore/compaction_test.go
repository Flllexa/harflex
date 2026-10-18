package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/security"
)

type contextFull struct{}

func (contextFull) Error() string               { return "maximum context length exceeded" }
func (contextFull) ContextLengthExceeded() bool { return true }

func failing(err error) (<-chan StreamEvent, <-chan error) {
	items, failures := make(chan StreamEvent), make(chan error, 1)
	failures <- err
	close(items)
	close(failures)
	return items, failures
}

func isSummaryRequest(req ChatRequest) bool {
	last := req.Messages[len(req.Messages)-1]
	return len(req.Tools) == 0 && last.Role == RoleUser && strings.Contains(last.Content, "Resuma a conversa")
}

func countTypes(j *sessionJournal, typ string) int {
	count := 0
	for _, item := range j.snapshot() {
		if item.Type == typ {
			count++
		}
	}
	return count
}

func TestSessionCompactsBeforeATurnThatWouldPassTheWindow(t *testing.T) {
	var requests []ChatRequest
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		requests = append(requests, req)
		if isSummaryRequest(req) {
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "Pedido: lista TODO. Estado: pronta."})
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "feito"}, StreamEvent{Type: "usage", Usage: &Usage{InputTokens: 900, OutputTokens: 10}})
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	s.SetContextWindow(1000)
	if err := s.Prompt(t.Context(), "primeiro pedido"); err != nil {
		t.Fatal(err)
	}
	if err := s.Prompt(t.Context(), "segundo pedido"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 || !isSummaryRequest(requests[1]) {
		t.Fatalf("requests = %d; the second must be the summary", len(requests))
	}
	if countTypes(j, "context.compacted") != 1 {
		t.Fatalf("compactions = %d", countTypes(j, "context.compacted"))
	}
	final := requests[2].Messages
	joined := ""
	for _, message := range final {
		joined += message.Content + "\n"
	}
	if !strings.Contains(joined, "[Resumo da conversa até aqui]") || !strings.Contains(joined, "primeiro pedido") || !strings.Contains(joined, "segundo pedido") || strings.Contains(joined, "feito") {
		t.Fatalf("the turn after compaction carried %q", joined)
	}
}

func TestSessionNeverCompactsWithToolCallsPending(t *testing.T) {
	rounds := 0
	var summaryAt int
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		if isSummaryRequest(req) {
			summaryAt = rounds
			for _, message := range req.Messages {
				for _, call := range message.ToolCalls {
					answered := false
					for _, other := range req.Messages {
						if other.Role == RoleTool && other.ToolCallID == call.ID {
							answered = true
						}
					}
					if !answered {
						t.Errorf("compacted with %s still unanswered", call.ID)
					}
				}
			}
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "resumo"})
		}
		rounds++
		if rounds == 1 {
			return streamEvents(toolEvent("call-1"), toolEvent("call-2"), StreamEvent{Type: "usage", Usage: &Usage{InputTokens: 950}})
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "fim"})
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		return ToolExecutionResult{Content: []byte(`"ok"`)}, nil
	}}
	s := NewSession("session-1", p, tools, &sessionJournal{}, security.FullAccess)
	s.SetContextWindow(1000)
	if err := s.Prompt(t.Context(), "use as ferramentas"); err != nil {
		t.Fatal(err)
	}
	if summaryAt != 1 {
		t.Fatalf("the summary came at round %d, want right after the tool results of round 1", summaryAt)
	}
}

func TestSessionCompactsAndRetriesOnAContextError(t *testing.T) {
	turns := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		if isSummaryRequest(req) {
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "resumo"})
		}
		if turns++; turns == 1 {
			return failing(contextFull{})
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "coube"})
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	if err := s.Prompt(t.Context(), "pedido longo"); err != nil {
		t.Fatalf("the run did not recover from a full context: %v", err)
	}
	if turns != 2 || countTypes(j, "context.compacted") != 1 {
		t.Fatalf("turns=%d compactions=%d", turns, countTypes(j, "context.compacted"))
	}
}

func TestSessionEndsWhenTheContextErrorRepeats(t *testing.T) {
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		if isSummaryRequest(req) {
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "resumo"})
		}
		return failing(contextFull{})
	}}
	j := &sessionJournal{}
	err := NewSession("session-1", p, &sessionTools{}, j, security.Ask).Prompt(t.Context(), "pedido")
	var ended *RunError
	if !errors.As(err, &ended) || ended.Reason != "context_length_exceeded" {
		t.Fatalf("err = %v", err)
	}
	if countTypes(j, "context.compacted") != 1 {
		t.Fatalf("compactions = %d, want exactly one", countTypes(j, "context.compacted"))
	}
}
