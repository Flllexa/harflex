package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func requestTokens(req ChatRequest) int {
	size := 0
	for _, message := range req.Messages {
		size += len(message.Content)
	}
	return size / 4
}

// A long run can fill its context more than once: each refusal after a turn that went through is recovered again.
func TestSessionRecoversFromAFullContextMoreThanOncePerRun(t *testing.T) {
	turns := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		if isSummaryRequest(req) {
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "resumo"})
		}
		turns++
		switch turns {
		case 1, 3:
			return failing(contextFull{})
		case 2:
			return streamEvents(toolEvent("call-1"))
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "fim"})
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		return ToolExecutionResult{Content: []byte(`"ok"`)}, nil
	}}
	j := &sessionJournal{}
	if err := NewSession("session-1", p, tools, j, security.FullAccess).Prompt(t.Context(), "pedido"); err != nil {
		t.Fatalf("the second full context of the run ended it: %v", err)
	}
	if countTypes(j, "context.compacted") != 2 {
		t.Fatalf("compactions = %d", countTypes(j, "context.compacted"))
	}
}

// A model whose real window is smaller than the one assumed refuses the turn; the summary request is cut to well
// under what was refused, so it is not refused too.
func TestSessionSummaryAfterARefusalFitsUnderWhatWasRefused(t *testing.T) {
	const realWindow = 25_000
	turns := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		if requestTokens(req) > realWindow {
			return failing(contextFull{})
		}
		if isSummaryRequest(req) {
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "resumo"})
		}
		if turns++; turns == 1 {
			return streamEvents(toolEvent("call-1"))
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "fim"})
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		encoded, _ := json.Marshal(strings.Repeat("linha de log ", 160_000/13))
		return ToolExecutionResult{Content: encoded}, nil
	}}
	s := NewSession("session-1", p, tools, &sessionJournal{}, security.FullAccess)
	s.SetContextWindow(100_000)
	if err := s.Prompt(t.Context(), "leia o log"); err != nil {
		t.Fatalf("the summary was refused like the turn it was meant to fix: %v", err)
	}
}

// A window too small for what must stay (the first request, the newest exchange) cannot be helped by summarizing
// again: one compaction that barely shrank the history is not repeated on every turn.
func TestSessionDoesNotCompactEveryTurnWhenItCannotShrink(t *testing.T) {
	rounds, summaries := 0, 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		if isSummaryRequest(req) {
			summaries++
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "resumo"})
		}
		if rounds++; rounds <= 3 {
			return streamEvents(toolEvent(fmt.Sprintf("call-%d", rounds)))
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "fim"})
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		return ToolExecutionResult{Content: []byte(`"ok"`)}, nil
	}}
	s := NewSession("session-1", p, tools, &sessionJournal{}, security.FullAccess)
	s.SetContextWindow(1000)
	if err := s.Prompt(t.Context(), strings.Repeat("requisito ", 360)); err != nil {
		t.Fatal(err)
	}
	if summaries > 1 {
		t.Fatalf("summaries = %d for a history that cannot shrink", summaries)
	}
}

// The tools sent with every request weigh on the window too, before any count from the provider.
func TestSessionEstimateCountsTheToolsSentWithEachRequest(t *testing.T) {
	summaries := 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		if isSummaryRequest(req) {
			summaries++
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "resumo"})
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "ok"})
	}}
	tools := &sessionTools{specs: []ToolSpec{{Name: "big", Description: strings.Repeat("descrição longa ", 300), Schema: json.RawMessage(`{"type":"object"}`)}}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, tools, j, security.Ask)
	s.SetContextWindow(1000)
	if err := s.Prompt(t.Context(), strings.Repeat("contexto ", 40)); err != nil {
		t.Fatal(err)
	}
	if err := s.Prompt(t.Context(), "segundo"); err != nil {
		t.Fatal(err)
	}
	if summaries == 0 {
		t.Fatal("a history plus tools past the window was never compacted")
	}
}

// Leaving old exchanges out of the summary request takes each one whole, and never the newest: a tool result
// without the call it answers is a request the provider refuses.
func TestFitForSummaryDropsWholeExchangesAndKeepsTheNewest(t *testing.T) {
	big := strings.Repeat("x", 40_000)
	messages := []Message{
		{Role: RoleSystem, Content: "sistema"},
		{Role: RoleUser, Content: "pedido"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call-1", Name: "test"}}},
		{Role: RoleTool, ToolCallID: "call-1", Content: big},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call-2", Name: "test"}, {ID: "call-3", Name: "test"}}},
		{Role: RoleTool, ToolCallID: "call-2", Content: big},
		{Role: RoleTool, ToolCallID: "call-3", Content: big},
	}
	for _, budget := range []int{10, 3000, 12_000} {
		fitted := fitForSummary(messages, 1, budget)
		calls := map[string]bool{}
		for _, message := range fitted {
			for _, call := range message.ToolCalls {
				calls[call.ID] = true
			}
			if message.Role == RoleTool && !calls[message.ToolCallID] {
				t.Fatalf("budget %d: %s kept without its call: %+v", budget, message.ToolCallID, fitted)
			}
		}
		if !calls["call-2"] || fitted[len(fitted)-1].ToolCallID != "call-3" {
			t.Fatalf("budget %d: the newest exchange was cut: %+v", budget, fitted)
		}
	}
}

// The provider's own count after a compaction is the truth: when it still comes back past the threshold (a CLI adds
// its own instructions, the estimate was low), summarizing again on the next turn does not help, so it waits.
func TestSessionTrustsTheProviderCountAfterACompaction(t *testing.T) {
	rounds, summaries := 0, 0
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		if isSummaryRequest(req) {
			summaries++
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "resumo"})
		}
		usage := StreamEvent{Type: "usage", Usage: &Usage{InputTokens: 900}}
		if rounds++; rounds <= 4 {
			return streamEvents(toolEvent(fmt.Sprintf("call-%d", rounds)), usage)
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "fim"}, usage)
	}}
	tools := &sessionTools{risk: "read_only", execute: func(context.Context, string, json.RawMessage, ToolUpdateSink) (ToolExecutionResult, error) {
		return ToolExecutionResult{Content: []byte(`"ok"`)}, nil
	}}
	s := NewSession("session-1", p, tools, &sessionJournal{}, security.FullAccess)
	s.SetContextWindow(1000)
	if err := s.Prompt(t.Context(), "pedido"); err != nil {
		t.Fatal(err)
	}
	if summaries > 1 {
		t.Fatalf("summaries = %d: it compacted again although the provider's count did not go down", summaries)
	}
}

// A first request larger than the room a compaction leaves (instructions carrying a whole diff) is kept by its head
// and tail, so the conversation actually gets its room back.
func TestCompactionKeepsAHugeFirstRequestShortened(t *testing.T) {
	var after []Message
	p := &sessionProvider{stream: func(_ context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
		if isSummaryRequest(req) {
			return streamEvents(StreamEvent{Type: "text_delta", Delta: "resumo"})
		}
		after = req.Messages
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "ok"})
	}}
	j := &sessionJournal{}
	s := NewSession("session-1", p, &sessionTools{}, j, security.Ask)
	s.SetContextWindow(10_000)
	first := "INÍCIO DO PEDIDO\n" + strings.Repeat("linha do diff\n", 4000) + "FIM DO PEDIDO"
	if err := s.Prompt(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Prompt(t.Context(), "segundo"); err != nil {
		t.Fatal(err)
	}
	kept := after[0].Content
	if len(kept) > 8*1024 || !strings.Contains(kept, "INÍCIO DO PEDIDO") || !strings.Contains(kept, "FIM DO PEDIDO") {
		t.Fatalf("first request kept: %d bytes", len(kept))
	}
}
