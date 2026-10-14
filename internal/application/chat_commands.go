package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/tools"
)

// A command the agent suggested in a chat, run because the person clicked Run on it. It runs in the chat's project, with
// the person's own environment (as their terminal would), its output streams to the chat as the "harflex:command" event,
// and the frontend sends the result back to the agent when it ends, so the conversation goes on from it.

const (
	maxChatCommandBytes  = 16 * 1024
	chatCommandMaxOutput = 1024 * 1024
	chatCommandTimeout   = 2 * time.Hour
)

var ErrChatCommandNotFound = errors.New("chat command not found")

// ChatCommandEventDTO is one chunk of a chat command's output, or its end.
type ChatCommandEventDTO struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	Text       string `json:"text,omitempty"`
	Done       bool   `json:"done"`
	ExitCode   int    `json:"exitCode"`
	Cancelled  bool   `json:"cancelled"`
	TimedOut   bool   `json:"timedOut"`
	Truncated  bool   `json:"truncated"`
	DurationMs int64  `json:"durationMs"`
	// Output is the whole output (up to 1 MiB), sent with the end.
	Output string `json:"output,omitempty"`
}

type RunChatCommandInput struct {
	SessionID string `json:"sessionId"`
	Command   string `json:"command"`
}

type chatCommands struct {
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// RunChatCommand starts a command in the chat's project and returns its id at once; the output follows as events.
func (s *Service) RunChatCommand(in RunChatCommandInput) (string, error) {
	if err := s.beginCall(); err != nil {
		return "", err
	}
	defer s.endCall()
	command := strings.TrimSpace(in.Command)
	if in.SessionID == "" || command == "" || len(command) > maxChatCommandBytes || !utf8.ValidString(command) || strings.ContainsRune(command, 0) {
		return "", ErrInvalidInput
	}
	record, err := s.store.GetSession(s.ctx, in.SessionID)
	if err != nil {
		return "", ErrSessionNotFound
	}
	workspace, err := s.store.GetWorkspace(s.ctx, record.WorkspaceID)
	if err != nil {
		return "", ErrWorkspaceNotFound
	}
	runID := id.New()
	ctx, cancel := context.WithCancel(s.ctx)
	s.commands.mu.Lock()
	if s.commands.running == nil {
		s.commands.running = map[string]context.CancelFunc{}
	}
	s.commands.running[runID] = cancel
	s.commands.mu.Unlock()
	emit := func(event ChatCommandEventDTO) {
		s.mu.RLock()
		send := s.emit
		s.mu.RUnlock()
		if send != nil {
			event.ID, event.SessionID = runID, in.SessionID
			send("harflex:command", event)
		}
	}
	shell := tools.NewShellTool(tools.ShellConfig{CWD: workspace.Path, MaxOutputBytes: chatCommandMaxOutput, DefaultTimeout: chatCommandTimeout, Env: append(os.Environ(), "TERM=dumb", "NO_COLOR=1")})
	args, _ := json.Marshal(map[string]string{"command": command})
	go func() {
		defer func() {
			cancel()
			s.commands.mu.Lock()
			delete(s.commands.running, runID)
			s.commands.mu.Unlock()
		}()
		result, _ := shell.Execute(ctx, args, func(_ context.Context, update agentcore.ToolUpdate) {
			if update.Text != "" {
				emit(ChatCommandEventDTO{Text: update.Text})
			}
		})
		var content struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(result.Content, &content)
		var details struct {
			ExitCode   int   `json:"exitCode"`
			TimedOut   bool  `json:"timedOut"`
			Cancelled  bool  `json:"cancelled"`
			Truncated  bool  `json:"truncated"`
			DurationMs int64 `json:"durationMs"`
		}
		details.ExitCode = -1
		_ = json.Unmarshal(result.Details, &details)
		emit(ChatCommandEventDTO{Done: true, ExitCode: details.ExitCode, Cancelled: details.Cancelled || errors.Is(ctx.Err(), context.Canceled), TimedOut: details.TimedOut, Truncated: details.Truncated, DurationMs: details.DurationMs, Output: content.Text})
	}()
	return runID, nil
}

// CancelChatCommand stops a running chat command; its end still arrives as an event.
func (s *Service) CancelChatCommand(runID string) error {
	s.commands.mu.Lock()
	cancel, ok := s.commands.running[runID]
	s.commands.mu.Unlock()
	if !ok {
		return ErrChatCommandNotFound
	}
	cancel()
	return nil
}
