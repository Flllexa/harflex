package application

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/security"
	"github.com/persioflexa/harflex/internal/tools"
)

// Chat watchers. A CLI agent's turn ends with its process, and every background job it started dies with it, so an
// agent that promises "I'll tell you when it finishes" needs Harflex to keep the watch. A watcher runs a command in
// the project folder every few seconds until it exits 0, or rings at a time; when it ends it writes in the same
// conversation, which starts the next reply. A busy conversation gets the message once its run is over.

const (
	harflexWatchTool       = "harflex_watch"
	maxActiveWatchers      = 5
	minWatchInterval       = 30
	maxWatchInterval       = 3600
	defaultWatchInterval   = 60
	defaultWatchMinutes    = 60
	maxWatchMinutes        = 24 * 60
	watchCheckTimeout      = 60 * time.Second
	watchOutputBytes       = 4096
	watchStartFailureLimit = 5
	watchHistory           = 24 * time.Hour
)

var (
	errTooManyWatchers        = errors.New("too many active watchers in this conversation")
	errWatcherNeedsFullAccess = errors.New("a command watcher needs Full access")
)

// ChatWatcherDTO is a watcher as the side panel shows it.
type ChatWatcherDTO struct {
	ID              string     `json:"id"`
	SessionID       string     `json:"sessionId"`
	ChatTitle       string     `json:"chatTitle"`
	Title           string     `json:"title"`
	Command         string     `json:"command"`
	IntervalSeconds int        `json:"intervalSeconds"`
	Status          string     `json:"status"`
	Reason          string     `json:"reason"`
	LastOutput      string     `json:"lastOutput"`
	LastExitCode    int        `json:"lastExitCode"`
	Checks          int        `json:"checks"`
	Delivered       bool       `json:"delivered"`
	LastCheckAt     *time.Time `json:"lastCheckAt,omitempty"`
	NextCheckAt     time.Time  `json:"nextCheckAt"`
	DeadlineAt      time.Time  `json:"deadlineAt"`
	CreatedAt       time.Time  `json:"createdAt"`
	EndedAt         *time.Time `json:"endedAt,omitempty"`
}

type ListChatWatchersInput struct {
	WorkspaceID string `json:"workspaceId"`
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func (s *Service) chatWatcherDTO(w catalog.ChatWatcher) ChatWatcherDTO {
	return ChatWatcherDTO{ID: w.ID, SessionID: w.SessionID, ChatTitle: s.sessionTitle(w.SessionID), Title: w.Title, Command: w.Command, IntervalSeconds: w.IntervalSeconds,
		Status: w.Status, Reason: w.Reason, LastOutput: w.LastOutput, LastExitCode: w.LastExitCode, Checks: w.Checks, Delivered: w.Delivered,
		LastCheckAt: optionalTime(w.LastCheckAt), NextCheckAt: w.NextCheckAt, DeadlineAt: w.DeadlineAt, CreatedAt: w.CreatedAt, EndedAt: optionalTime(w.EndedAt)}
}

// ListChatWatchers lists a project's watchers: those still watching or waiting to bring their chat back, and those
// that ended in the last day.
func (s *Service) ListChatWatchers(in ListChatWatchersInput) ([]ChatWatcherDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if strings.TrimSpace(in.WorkspaceID) == "" {
		return nil, ErrInvalidInput
	}
	items, err := s.store.ListChatWatchers(s.ctx, in.WorkspaceID, time.Now().UTC().Add(-watchHistory))
	if err != nil {
		return nil, safe("list chat watchers", err)
	}
	result := make([]ChatWatcherDTO, 0, len(items))
	for _, item := range items {
		result = append(result, s.chatWatcherDTO(item))
	}
	return result, nil
}

// StopChatWatcher ends a watcher by the person's hand: it checks no more and does not bring its chat back.
func (s *Service) StopChatWatcher(watcherID string) (ChatWatcherDTO, error) {
	if err := s.beginCall(); err != nil {
		return ChatWatcherDTO{}, err
	}
	defer s.endCall()
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	w, err := s.store.GetChatWatcher(s.ctx, watcherID)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatWatcherDTO{}, ErrInvalidInput
	}
	if err != nil {
		return ChatWatcherDTO{}, safe("read chat watcher", err)
	}
	if w.Status == "watching" || !w.Delivered {
		if w.Status == "watching" {
			w.Status, w.Reason, w.EndedAt = "stopped", "stopped_by_person", time.Now().UTC()
		}
		w.Delivered = true
		if err := s.store.SaveChatWatcher(s.ctx, w); err != nil {
			return ChatWatcherDTO{}, safe("stop chat watcher", err)
		}
	}
	return s.chatWatcherDTO(w), nil
}

func (s *Service) watchTool(workspaceID, sessionID string) tools.Tool {
	return harflexTool{service: s, workspaceID: workspaceID, sessionID: sessionID, risk: security.ReadOnly, spec: harflexWatchSpec, run: harflexWatch}
}

var harflexWatchSpec = agentcore.ToolSpec{Name: harflexWatchTool, Description: "Leave a watcher that brings this conversation back later. Your background processes (a terminal job, a monitor, a sleep loop) die when your reply ends, " +
	"so never promise to report back on your own: call this instead and end your reply. Either give a shell command that Harflex runs in the project folder " +
	"every everySeconds (default 60, at least 30) until it exits 0 — write it so it exits 0 when what you wait for is done, e.g. `curl -s URL | grep -q ready` — " +
	"or give afterMinutes to come back at a time. When the command passes, the time comes or timeoutMinutes (default 60, at most 1440) runs out, Harflex " +
	"writes in this conversation with the last output and you continue from there. A command watcher needs Full access in the project; a timer does not. At most 5 active per conversation.",
	Schema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","minLength":1,"maxLength":120,"description":"What you are waiting for, as the person reads it in the side panel."},` +
		`"command":{"type":"string","minLength":1,"maxLength":4000},"everySeconds":{"type":"integer","minimum":30,"maximum":3600},` +
		`"timeoutMinutes":{"type":"integer","minimum":1,"maximum":1440},"afterMinutes":{"type":"integer","minimum":1,"maximum":1440}},"required":["title"],"additionalProperties":false}`)}

func harflexWatch(s *Service, workspaceID, sessionID string, args json.RawMessage) (any, error) {
	var in struct {
		Title          string `json:"title"`
		Command        string `json:"command"`
		EverySeconds   int    `json:"everySeconds"`
		TimeoutMinutes int    `json:"timeoutMinutes"`
		AfterMinutes   int    `json:"afterMinutes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errHarflexArguments
	}
	in.Title, in.Command = strings.TrimSpace(in.Title), strings.TrimSpace(in.Command)
	if in.Title == "" || utf8.RuneCountInString(in.Title) > 120 || len(in.Command) > 4000 || (in.Command == "") == (in.AfterMinutes == 0) {
		return nil, errHarflexArguments
	}
	now := time.Now().UTC()
	w := catalog.ChatWatcher{ID: id.New(), SessionID: sessionID, WorkspaceID: workspaceID, Title: in.Title, Command: in.Command, Status: "watching", CreatedAt: now}
	if in.Command != "" {
		if in.EverySeconds == 0 {
			in.EverySeconds = defaultWatchInterval
		}
		if in.TimeoutMinutes == 0 {
			in.TimeoutMinutes = defaultWatchMinutes
		}
		if in.EverySeconds < minWatchInterval || in.EverySeconds > maxWatchInterval || in.TimeoutMinutes < 1 || in.TimeoutMinutes > maxWatchMinutes || in.AfterMinutes != 0 {
			return nil, errHarflexArguments
		}
		workspace, err := s.store.GetWorkspace(s.ctx, workspaceID)
		if err != nil {
			return nil, ErrWorkspaceNotFound
		}
		if security.Profile(workspace.Profile) != security.FullAccess {
			return nil, errWatcherNeedsFullAccess
		}
		w.IntervalSeconds = in.EverySeconds
		w.NextCheckAt = now.Add(time.Duration(in.EverySeconds) * time.Second)
		w.DeadlineAt = now.Add(time.Duration(in.TimeoutMinutes) * time.Minute)
	} else {
		if in.AfterMinutes < 1 || in.AfterMinutes > maxWatchMinutes || in.EverySeconds != 0 || in.TimeoutMinutes != 0 {
			return nil, errHarflexArguments
		}
		w.NextCheckAt = now.Add(time.Duration(in.AfterMinutes) * time.Minute)
		w.DeadlineAt = w.NextCheckAt
	}
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	active, err := s.store.CountActiveChatWatchers(s.ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if active >= maxActiveWatchers {
		return nil, errTooManyWatchers
	}
	if err := s.store.SaveChatWatcher(s.ctx, w); err != nil {
		return nil, err
	}
	next := fmt.Sprintf("Harflex runs the command in the project folder every %d seconds and, when it exits 0 or the deadline passes, writes in this conversation and you continue from there.", w.IntervalSeconds)
	if w.Command == "" {
		next = fmt.Sprintf("Harflex writes in this conversation at %s and you continue from there.", w.NextCheckAt.Format(time.RFC3339))
	}
	return map[string]any{"watcherId": w.ID, "status": w.Status, "next": next + " Do not wait or loop: tell the person what you are waiting for and end your reply now."}, nil
}

type watchState struct {
	checking   map[string]bool
	delivering map[string]bool
}

func (s *Service) watchClaim(kind map[string]bool, watcherID string) bool {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if kind[watcherID] {
		return false
	}
	kind[watcherID] = true
	return true
}

func (s *Service) watchRelease(kind map[string]bool, watcherID string) {
	s.watchMu.Lock()
	delete(kind, watcherID)
	s.watchMu.Unlock()
}

func (s *Service) watchMaps() *watchState {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watch == nil {
		s.watch = &watchState{checking: map[string]bool{}, delivering: map[string]bool{}}
	}
	return s.watch
}

// tickChatWatchers runs the checks that are due, ends the watchers whose time came, and brings back the chats whose
// watcher ended. Checks run side by side; the call returns when they are done.
func (s *Service) tickChatWatchers(now time.Time) {
	if s.ctx.Err() != nil {
		return
	}
	state := s.watchMaps()
	items, err := s.store.ListPendingChatWatchers(s.ctx)
	if err != nil {
		return
	}
	var checks sync.WaitGroup
	for _, item := range items {
		if item.Status != "watching" || now.Before(item.NextCheckAt) {
			continue
		}
		if item.Command == "" {
			item.Status, item.Reason, item.EndedAt = "fired", "timer", now
			_ = s.store.SaveChatWatcher(s.ctx, item)
			continue
		}
		if !s.watchClaim(state.checking, item.ID) {
			continue
		}
		checks.Add(1)
		go func(w catalog.ChatWatcher) {
			defer checks.Done()
			defer s.watchRelease(state.checking, w.ID)
			s.runWatchCheck(w, now)
		}(item)
	}
	checks.Wait()
	if items, err = s.store.ListPendingChatWatchers(s.ctx); err != nil {
		return
	}
	for _, item := range items {
		if item.Status != "watching" && !item.Delivered {
			s.deliverChatWatcher(item)
		}
	}
}

func (s *Service) runWatchCheck(w catalog.ChatWatcher, now time.Time) {
	workspace, err := s.store.GetWorkspace(s.ctx, w.WorkspaceID)
	if err != nil {
		w.Status, w.Reason, w.EndedAt, w.Delivered = "failed", "project_gone", now, true
		_ = s.store.SaveChatWatcher(s.ctx, w)
		return
	}
	args, _ := json.Marshal(map[string]string{"command": w.Command})
	shell := tools.NewShellTool(tools.ShellConfig{CWD: workspace.Path, DefaultTimeout: watchCheckTimeout, MaxOutputBytes: 4 * watchOutputBytes})
	result, _ := shell.Execute(s.ctx, args, nil)
	if s.ctx.Err() != nil {
		return
	}
	var details struct {
		ExitCode int  `json:"exitCode"`
		TimedOut bool `json:"timedOut"`
	}
	details.ExitCode = -1
	_ = json.Unmarshal(result.Details, &details)
	var content struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(result.Content, &content)
	w.Checks++
	w.LastCheckAt, w.LastExitCode, w.LastOutput = now, details.ExitCode, tailText(content.Text, watchOutputBytes)
	switch {
	case details.ExitCode == 0 && !details.TimedOut:
		w.Status, w.Reason, w.EndedAt = "fired", "condition_met", now
	case details.ExitCode == 126 || details.ExitCode == 127:
		// The command cannot run at all (not found, not executable): a few tries, then the agent hears of it.
		w.Failures++
		if w.Failures >= watchStartFailureLimit {
			w.Status, w.Reason, w.EndedAt = "failed", "command_not_runnable", now
		}
	default:
		w.Failures = 0
	}
	if w.Status == "watching" {
		if !now.Before(w.DeadlineAt) {
			w.Status, w.Reason, w.EndedAt = "expired", "deadline", now
		} else {
			w.NextCheckAt = now.Add(time.Duration(w.IntervalSeconds) * time.Second)
		}
	}
	_ = s.store.SaveChatWatcher(s.ctx, w)
}

func tailText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	cut := len(text) - limit
	for cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut++
	}
	return "…" + text[cut:]
}

// deliverChatWatcher writes the watcher's outcome in its conversation, which starts the agent's next reply. A
// conversation that is running keeps the message for a later tick.
func (s *Service) deliverChatWatcher(w catalog.ChatWatcher) {
	state := s.watchMaps()
	record, err := s.store.GetSession(s.ctx, w.SessionID)
	if err != nil {
		w.Delivered = true
		_ = s.store.SaveChatWatcher(s.ctx, w)
		return
	}
	if record.Status == "running" || record.Status == "awaiting_approval" || !s.watchClaim(state.delivering, w.ID) {
		return
	}
	w.Delivered = true
	if err := s.store.SaveChatWatcher(s.ctx, w); err != nil {
		s.watchRelease(state.delivering, w.ID)
		return
	}
	go func() {
		defer s.watchRelease(state.delivering, w.ID)
		if _, err := s.Prompt(PromptInput{SessionID: w.SessionID, Text: watcherMessage(w)}); err != nil && s.ctx.Err() == nil && (errors.Is(err, agentcore.ErrSessionBusy) || errors.Is(err, externalagent.ErrSessionBusy)) {
			// Someone started a run first: the message waits for the next tick.
			w.Delivered = false
			_ = s.store.SaveChatWatcher(s.ctx, w)
		}
	}()
}

func watcherMessage(w catalog.ChatWatcher) string {
	var text strings.Builder
	switch w.Reason {
	case "condition_met":
		fmt.Fprintf(&text, "Vigia «%s» terminou: o comando saiu com código 0.", w.Title)
	case "timer":
		fmt.Fprintf(&text, "Vigia «%s»: chegou a hora que você marcou.", w.Title)
	case "deadline":
		fmt.Fprintf(&text, "Vigia «%s» acabou o prazo sem o comando sair com código 0 (último código: %d, %d checagens).", w.Title, w.LastExitCode, w.Checks)
	case "command_not_runnable":
		fmt.Fprintf(&text, "Vigia «%s» parou: o comando não pôde rodar (código %d) %d vezes seguidas.", w.Title, w.LastExitCode, w.Failures)
	default:
		fmt.Fprintf(&text, "Vigia «%s» terminou (%s).", w.Title, w.Reason)
	}
	if w.Command != "" {
		fmt.Fprintf(&text, "\nComando: `%s`", w.Command)
	}
	if w.LastOutput != "" {
		fmt.Fprintf(&text, "\nÚltima saída:\n```\n%s\n```", w.LastOutput)
	}
	text.WriteString("\nContinue de onde parou e conte à pessoa o resultado.")
	return text.String()
}
