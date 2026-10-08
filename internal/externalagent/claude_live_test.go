package externalagent

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/modelcatalog"
)

// Opt-in only: finds the installed Claude Code, reads its version and login state, and lists the aliases it would offer.
// With HARFLEX_LIVE_CLAUDE_PROMPT=1 it also sends one short prompt, which spends the account's usage.
func TestLiveClaudeCode(t *testing.T) {
	if os.Getenv("HARFLEX_LIVE_CLAUDE") != "1" {
		t.Skip("set HARFLEX_LIVE_CLAUDE=1 to check the installed Claude Code")
	}
	adapter := NewClaudeCode(os.Getenv("HARFLEX_LIVE_CLAUDE_PATH"))
	detected := adapter.Detect()
	if !detected.Available {
		t.Fatal("Claude Code was not found")
	}
	t.Logf("Claude Code %s", detected.Version)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	result, err := adapter.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	}).QueryModels(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("catalog status=%s code=%s models=%d", result.Status, result.ErrorCode, len(result.Models))
	if result.Status != modelcatalog.StatusComplete || os.Getenv("HARFLEX_LIVE_CLAUDE_PROMPT") != "1" {
		return
	}
	events, errs := adapter.Run(ctx, Request{Prompt: "Responda apenas: ok", CWD: t.TempDir(), Model: "haiku", ReasoningEffort: "low"})
	var answer, session string
	for event := range events {
		if event.Type == "assistant.message" {
			answer = event.Text
		}
		if event.SessionID != "" {
			session = event.SessionID
		}
	}
	if err := <-errs; err != nil || answer == "" || session == "" {
		t.Fatalf("run: answer=%q session=%q err=%v", answer, session, err)
	}
	t.Logf("answer bytes=%d", len(answer))
}

// Opt-in only: one isolated document call, as the SDD phases make it, which spends the account's usage.
func TestLiveClaudeCodeDocument(t *testing.T) {
	if os.Getenv("HARFLEX_LIVE_CLAUDE_DOCUMENT") != "1" {
		t.Skip("set HARFLEX_LIVE_CLAUDE_DOCUMENT=1 to write one isolated document with the installed Claude Code")
	}
	adapter := NewClaudeCode(os.Getenv("HARFLEX_LIVE_CLAUDE_PATH")).(*cliAdapter)
	detected := adapter.Detect()
	if !adapter.DocumentContractSupported(detected) {
		t.Fatalf("contract unavailable: %+v", detected)
	}
	schema := json.RawMessage(`{"type":"object","properties":{"reply":{"type":"string"},"toolCalls":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"arguments":{"type":"object","properties":{},"required":[],"additionalProperties":false}},"required":["name","arguments"],"additionalProperties":false},"maxItems":0}},"required":["reply","toolCalls"],"additionalProperties":false}`)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	result, err := adapter.GenerateDocument(ctx, DocumentRequest{Prompt: "Escreva em uma frase o objetivo de um app de lista de tarefas que salva no localStorage.", SystemPrompt: "Você escreve documentos curtos em português, sem ferramentas.",
		Model: "haiku", ReasoningEffort: "low", ExpectedExecutablePath: detected.Path, ExpectedExecutableVersion: detected.Version, OutputSchema: schema, MaxAssistantOutputBytes: 64 * 1024}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Reply     string `json:"reply"`
		ToolCalls []any  `json:"toolCalls"`
	}
	if json.Unmarshal([]byte(result.Text), &envelope) != nil || envelope.Reply == "" || envelope.ToolCalls == nil {
		t.Fatalf("envelope: %s", result.Text)
	}
	t.Logf("reply=%q thread=%t usage=%+v known=%t", envelope.Reply, result.ThreadID != "", result.Usage, result.UsageKnown)
}
