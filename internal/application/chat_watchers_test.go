package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

// watchProvider plays a chat agent that leaves a watcher instead of waiting, then answers when the watcher brings
// the conversation back.
type watchProvider struct {
	mu       sync.Mutex
	args     map[string]any
	returned []string
}

func (*watchProvider) ID() string { return "watch" }
func (*watchProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}

func (p *watchProvider) Stream(_ context.Context, r agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	events := make(chan agentcore.StreamEvent, 1)
	errs := make(chan error)
	last := r.Messages[len(r.Messages)-1]
	switch {
	case last.Role == agentcore.RoleUser && strings.Contains(last.Content, "Vigia"):
		p.returned = append(p.returned, last.Content)
		events <- agentcore.StreamEvent{Type: "text_delta", Delta: "Os manuais terminaram."}
	case last.Role == agentcore.RoleTool:
		events <- agentcore.StreamEvent{Type: "text_delta", Delta: "Deixei uma vigia; volto quando terminar."}
	default:
		args, _ := json.Marshal(p.args)
		events <- agentcore.StreamEvent{Type: "tool_call", ToolCall: &agentcore.ToolCall{ID: "watch-1", Name: harflexWatchTool, Arguments: args}}
	}
	close(events)
	close(errs)
	return events, errs
}

func watcherChat(t *testing.T, args map[string]any) (*Service, *watchProvider, string, string) {
	t.Helper()
	_, db, vault := setup(t)
	provider := &watchProvider{args: args}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "full_access", ConfirmFullAccess: true}); err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: chat.ID, Text: "Envie os manuais e me avise quando o Graphiti terminar"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("first turn: %+v %v", result, err)
	}
	return s, provider, workspace.ID, chat.ID
}

func onlyWatcher(t *testing.T, s *Service, workspaceID string) ChatWatcherDTO {
	t.Helper()
	list, err := s.ListChatWatchers(ListChatWatchersInput{WorkspaceID: workspaceID})
	if err != nil || len(list) != 1 {
		t.Fatalf("watchers: %+v %v", list, err)
	}
	return list[0]
}

func waitReturned(t *testing.T, provider *watchProvider, want int) []string {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		provider.mu.Lock()
		got := append([]string(nil), provider.returned...)
		provider.mu.Unlock()
		if len(got) >= want {
			return got
		}
	}
	t.Fatalf("the chat did not come back %d time(s)", want)
	return nil
}

func TestAWatcherBringsTheChatBackWhenItsCheckPasses(t *testing.T) {
	s, provider, workspaceID, chatID := watcherChat(t, map[string]any{"title": "4 manuais processados", "command": "test -f done.flag && echo pronto", "everySeconds": 30, "timeoutMinutes": 60})
	watcher := onlyWatcher(t, s, workspaceID)
	if watcher.SessionID != chatID || watcher.Status != "watching" || watcher.IntervalSeconds != 30 || watcher.Title != "4 manuais processados" {
		t.Fatalf("watcher after the agent left it: %+v", watcher)
	}
	start := time.Now().UTC()
	s.tickChatWatchers(start.Add(31 * time.Second))
	if watcher = onlyWatcher(t, s, workspaceID); watcher.Status != "watching" || watcher.Checks != 1 || watcher.LastExitCode == 0 {
		t.Fatalf("a check that does not pass keeps watching: %+v", watcher)
	}
	workspace, _ := s.store.GetWorkspace(s.ctx, workspaceID)
	if err := os.WriteFile(filepath.Join(workspace.Path, "done.flag"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.tickChatWatchers(start.Add(62 * time.Second))
	returned := waitReturned(t, provider, 1)
	if !strings.Contains(returned[0], "4 manuais processados") || !strings.Contains(returned[0], "pronto") {
		t.Fatalf("the message that brings the chat back: %q", returned[0])
	}
	if watcher = onlyWatcher(t, s, workspaceID); watcher.Status != "fired" || !watcher.Delivered || watcher.Reason != "condition_met" {
		t.Fatalf("watcher after it fired: %+v", watcher)
	}
	s.tickChatWatchers(start.Add(200 * time.Second))
	if got := waitReturned(t, provider, 1); len(got) != 1 {
		t.Fatalf("the chat came back twice: %v", got)
	}
}

func TestAWatcherWaitsForABusyChat(t *testing.T) {
	s, provider, workspaceID, chatID := watcherChat(t, map[string]any{"title": "lembrete", "afterMinutes": 5})
	record, _ := s.store.GetSession(s.ctx, chatID)
	record.Status = "running"
	if err := s.store.UpsertSession(s.ctx, record); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	s.tickChatWatchers(start.Add(6 * time.Minute))
	if watcher := onlyWatcher(t, s, workspaceID); watcher.Status != "fired" || watcher.Reason != "timer" || watcher.Delivered {
		t.Fatalf("a timer that fires into a busy chat waits: %+v", watcher)
	}
	record.Status = "completed"
	if err := s.store.UpsertSession(s.ctx, record); err != nil {
		t.Fatal(err)
	}
	s.tickChatWatchers(start.Add(7 * time.Minute))
	if returned := waitReturned(t, provider, 1); !strings.Contains(returned[0], "lembrete") {
		t.Fatalf("the waiting message: %q", returned[0])
	}
}

func TestAWatcherThatNeverPassesExpiresAndSaysSo(t *testing.T) {
	s, provider, workspaceID, _ := watcherChat(t, map[string]any{"title": "deploy", "command": "exit 1", "everySeconds": 60, "timeoutMinutes": 2})
	start := time.Now().UTC()
	s.tickChatWatchers(start.Add(3 * time.Minute))
	if returned := waitReturned(t, provider, 1); !strings.Contains(returned[0], "deploy") || !strings.Contains(returned[0], "prazo") {
		t.Fatalf("the expiry message: %q", returned[0])
	}
	if watcher := onlyWatcher(t, s, workspaceID); watcher.Status != "expired" {
		t.Fatalf("watcher past its deadline: %+v", watcher)
	}
}

func TestAStoppedWatcherNeverComesBack(t *testing.T) {
	s, provider, workspaceID, _ := watcherChat(t, map[string]any{"title": "lembrete", "afterMinutes": 1})
	watcher := onlyWatcher(t, s, workspaceID)
	if _, err := s.StopChatWatcher(watcher.ID); err != nil {
		t.Fatal(err)
	}
	s.tickChatWatchers(time.Now().UTC().Add(5 * time.Minute))
	time.Sleep(100 * time.Millisecond)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.returned) != 0 || onlyWatcher(t, s, workspaceID).Status != "stopped" {
		t.Fatalf("a stopped watcher came back: %v", provider.returned)
	}
}

func TestWatcherArgumentsAndLimits(t *testing.T) {
	s, _, workspaceID, chatID := watcherChat(t, map[string]any{"title": "um", "afterMinutes": 30})
	for _, bad := range []map[string]any{
		{"title": "rápido demais", "command": "true", "everySeconds": 5},
		{"title": "sem nada"},
		{"title": "os dois", "command": "true", "afterMinutes": 3},
		{"title": "longo demais", "command": "true", "timeoutMinutes": 25 * 60},
	} {
		args, _ := json.Marshal(bad)
		if _, err := harflexWatch(s, workspaceID, chatID, args); !errors.Is(err, errHarflexArguments) {
			t.Fatalf("%v was accepted: %v", bad, err)
		}
	}
	for i := 0; i < 9; i++ {
		args, _ := json.Marshal(map[string]any{"title": "mais um", "afterMinutes": 30})
		if _, err := harflexWatch(s, workspaceID, chatID, args); err != nil {
			t.Fatal(err)
		}
	}
	args, _ := json.Marshal(map[string]any{"title": "o décimo primeiro", "afterMinutes": 30})
	if _, err := harflexWatch(s, workspaceID, chatID, args); !errors.Is(err, errTooManyWatchers) {
		t.Fatalf("an eleventh active watcher: %v", err)
	}
}

// Each check of a command watcher runs without asking; without Full access only the timer is offered.
func TestACommandWatcherNeedsFullAccess(t *testing.T) {
	s, _, workspaceID, chatID := watcherChat(t, map[string]any{"title": "um", "afterMinutes": 30})
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspaceID, Profile: "ask"}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"title": "deploy", "command": "true"})
	if _, err := harflexWatch(s, workspaceID, chatID, args); !errors.Is(err, errWatcherNeedsFullAccess) {
		t.Fatalf("a command watcher without Full access: %v", err)
	}
	args, _ = json.Marshal(map[string]any{"title": "lembrete", "afterMinutes": 10})
	if _, err := harflexWatch(s, workspaceID, chatID, args); err != nil {
		t.Fatalf("a timer without Full access: %v", err)
	}
}

// Several watchers run side by side; each brings the chat back on its own, saying which are still out.
func TestWatchersComeBackOneByOneSayingWhichAreStillOut(t *testing.T) {
	s, provider, workspaceID, chatID := watcherChat(t, map[string]any{"title": "manuais", "afterMinutes": 2})
	args, _ := json.Marshal(map[string]any{"title": "outros documentos", "afterMinutes": 30})
	if _, err := harflexWatch(s, workspaceID, chatID, args); err != nil {
		t.Fatal(err)
	}
	s.tickChatWatchers(time.Now().UTC().Add(3 * time.Minute))
	returned := waitReturned(t, provider, 1)
	if !strings.Contains(returned[0], "manuais") || !strings.Contains(returned[0], "Ainda aguardando") || !strings.Contains(returned[0], "outros documentos") {
		t.Fatalf("the first watcher back: %q", returned[0])
	}
	s.tickChatWatchers(time.Now().UTC().Add(31 * time.Minute))
	returned = waitReturned(t, provider, 2)
	if !strings.Contains(returned[1], "outros documentos") || strings.Contains(returned[1], "Ainda aguardando") {
		t.Fatalf("the last watcher back: %q", returned[1])
	}
}
