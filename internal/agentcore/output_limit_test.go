package agentcore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/security"
)

func TestAssistantOutputByteLimitCancelsProviderBeforeFinalMessage(t *testing.T) {
	providerCancelled := make(chan struct{})
	provider := &sessionProvider{stream: func(ctx context.Context, _ ChatRequest) (<-chan StreamEvent, <-chan error) {
		items := make(chan StreamEvent, 2)
		items <- StreamEvent{Type: "text_delta", Delta: "1234"}
		items <- StreamEvent{Type: "text_delta", Delta: "5"}
		go func() { <-ctx.Done(); close(providerCancelled); close(items) }()
		return items, nil
	}}
	journal := &sessionJournal{}
	session := NewSession("session-1", provider, &sessionTools{}, journal, security.Ask)
	if err := session.SetAssistantOutputByteLimit(4); err != nil {
		t.Fatal(err)
	}
	err := session.Prompt(t.Context(), "bounded")
	var ended *RunError
	if !errors.As(err, &ended) || ended.Reason != "output_limit_exceeded" {
		t.Fatalf("run = %v", err)
	}
	<-providerCancelled
	for _, event := range journal.snapshot() {
		if event.Type == "message.assistant" || event.Type == "run.completed" {
			t.Fatalf("accepted output after overflow: %s", event.Type)
		}
	}
}

func TestAssistantOutputByteLimitIsOptIn(t *testing.T) {
	provider := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(StreamEvent{Type: "text_delta", Delta: strings.Repeat("a", 2049)})
	}}
	journal := &sessionJournal{}
	session := NewSession("session-1", provider, &sessionTools{}, journal, security.Ask)
	if err := session.Prompt(t.Context(), "ordinary"); err != nil {
		t.Fatal(err)
	}
	assertTypes(t, journal, "run.started", "message.user", "assistant.delta", "message.assistant", "run.completed")
}

func TestAssistantOutputByteLimitConfiguration(t *testing.T) {
	provider := &sessionProvider{stream: func(context.Context, ChatRequest) (<-chan StreamEvent, <-chan error) {
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "a"})
	}}
	session := NewSession("session-1", provider, &sessionTools{}, &sessionJournal{}, security.Ask)
	for _, limit := range []int{-1, 0, maxMessageBytes + 1} {
		if err := session.SetAssistantOutputByteLimit(limit); err == nil {
			t.Fatalf("accepted invalid limit %d", limit)
		}
	}
	for _, limit := range []int{maxMessageBytes, 1} {
		if err := session.SetAssistantOutputByteLimit(limit); err != nil {
			t.Fatalf("valid limit %d: %v", limit, err)
		}
	}
	if err := session.Prompt(t.Context(), "execute"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetAssistantOutputByteLimit(2); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("changed limit after execution: %v", err)
	}
}

func TestAssistantOutputByteLimitCanBeConfiguredBeforeUserTurnWithSystemInstructions(t *testing.T) {
	provider := &sessionProvider{stream: func(_ context.Context, request ChatRequest) (<-chan StreamEvent, <-chan error) {
		if len(request.Messages) != 2 || request.Messages[0].Role != RoleSystem {
			t.Fatalf("system instructions were not retained: %+v", request.Messages)
		}
		return streamEvents(StreamEvent{Type: "text_delta", Delta: "12345"})
	}}
	session, err := RestoreSessionWithLimit("session-1", provider, &sessionTools{}, &sessionJournal{}, security.Ask, []Message{{Role: RoleSystem, Content: "Generate a bounded document."}}, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SetAssistantOutputByteLimit(4); err != nil {
		t.Fatalf("cannot set limit before the first user turn: %v", err)
	}
	var ended *RunError
	if err := session.Prompt(t.Context(), "prepare document"); !errors.As(err, &ended) || ended.Reason != "output_limit_exceeded" {
		t.Fatalf("system-instructed session did not enforce byte limit: %v", err)
	}
}

func TestAssistantOutputByteLimitCountsUTF8AcrossChunks(t *testing.T) {
	for _, extra := range []string{"", "!"} {
		name := "exact"
		if extra != "" {
			name = "overflow"
		}
		t.Run(name, func(t *testing.T) {
			providerCancelled := make(chan struct{})
			provider := &sessionProvider{stream: func(ctx context.Context, _ ChatRequest) (<-chan StreamEvent, <-chan error) {
				items := make(chan StreamEvent, 3)
				items <- StreamEvent{Type: "text_delta", Delta: "é"}
				items <- StreamEvent{Type: "text_delta", Delta: "界"}
				if extra != "" {
					items <- StreamEvent{Type: "text_delta", Delta: extra}
					go func() { <-ctx.Done(); close(providerCancelled); close(items) }()
				} else {
					close(items)
				}
				return items, nil
			}}
			journal := &sessionJournal{}
			session := NewSession("session-1", provider, &sessionTools{}, journal, security.Ask)
			if err := session.SetAssistantOutputByteLimit(len("é界")); err != nil {
				t.Fatal(err)
			}
			err := session.Prompt(t.Context(), "bounded")
			if extra == "" {
				if err != nil {
					t.Fatal(err)
				}
				assertTypes(t, journal, "run.started", "message.user", "assistant.delta", "message.assistant", "run.completed")
				if got := eventField(t, journal.snapshot()[3], "content"); got != "é界" {
					t.Fatalf("accepted content = %q", got)
				}
				return
			}
			var ended *RunError
			if !errors.As(err, &ended) || ended.Reason != "output_limit_exceeded" {
				t.Fatalf("overflow = %v", err)
			}
			<-providerCancelled
			for _, event := range journal.snapshot() {
				if event.Type == "message.assistant" || event.Type == "run.completed" {
					t.Fatalf("overflow accepted: %s", event.Type)
				}
			}
		})
	}
}
