package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/externalagent"
)

var ErrSessionCorrupt = errors.New("session history is corrupt")
var ErrHistoryTooLarge = errors.New("session history exceeds the safety limit")

func (s *Service) sessionDTO(r catalog.SessionRecord) SessionDTO {
	return s.sessionDTOCached(r, nil)
}

func (s *Service) sessionDTOCached(r catalog.SessionRecord, cache map[string]cliContinuationSnapshot) SessionDTO {
	s.mu.RLock()
	_, readOnly := s.sessions[r.ID].(*readOnlySessionRunner)
	s.mu.RUnlock()
	resumable := false
	if !readOnly && r.Mode != "sdd_readonly" {
		resumable, _, _ = s.sessionContinuationCached(r, cache)
	}
	purpose, title := s.sessionPurpose(r)
	return SessionDTO{ID: r.ID, WorkspaceID: r.WorkspaceID, BackendID: r.BackendID, Status: r.Status, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Resumable: resumable, Purpose: purpose, Title: title}
}

const (
	sessionTitleEvents = 8   // events inspected per session; the first user message comes early
	sessionTitleLimit  = 40  // most recent sessions that receive a title
	sessionTitleRunes  = 120 // preview length
)

// continuationMarker opens the part of a message that is reference, not request. When a conversation that cannot be
// resumed is continued in a new one, the screen sends the person's words first and the old conversation's context after
// this line (frontend/src/features/workbench/continuation.ts), so the chat is named after the words, not the context.
const continuationMarker = "\n\n---\nContexto da conversa anterior"

func (s *Service) sessionPurpose(record catalog.SessionRecord) (string, string) {
	switch record.Mode {
	case "sdd_readonly":
		return "preparation", "Preparação de documentos"
	case "sdd_code":
		return "code", "Execução de Code"
	case catalog.AuthoringCodeSessionMode:
		return "code", "Execução de Code (SDD autoral)"
	case "evaluation":
		return "evaluation", "Avaliação do trabalho"
	}
	if pipelineID, err := s.store.GetPipelineIDForSession(s.ctx, record.ID); err == nil {
		if run, err := s.loadPipeline(pipelineID); err == nil {
			return "code", run.Title
		}
	}
	// The conversation where the agent opens a pipeline's pull requests is an ordinary chat, named for its work so it
	// can be found again to apply the fixes the review asks for.
	if pipelineID, err := s.store.GetPipelineIDForPRSession(s.ctx, record.ID); err == nil {
		if run, err := s.loadPipeline(pipelineID); err == nil {
			return "chat", "PRs · " + run.Title
		}
	}
	return "chat", ""
}

// sessionTitle previews the first user message in one line. The journal already
// holds redacted content, so the preview never reveals more than the history.
func (s *Service) sessionTitle(sessionID string) string {
	items, err := s.store.ListAfterLimit(s.ctx, sessionID, 0, sessionTitleEvents)
	if err != nil {
		return ""
	}
	for _, item := range items {
		if item.Type != "message.user" {
			continue
		}
		var data struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(item.Data, &data) != nil {
			return ""
		}
		content := data.Content
		if at := strings.Index(content, continuationMarker); at > 0 {
			content = content[:at]
		}
		line := strings.Join(strings.Fields(content), " ")
		if runes := []rune(line); len(runes) > sessionTitleRunes {
			line = string(runes[:sessionTitleRunes-1]) + "…"
		}
		return line
	}
	return ""
}

type cliContinuationSnapshot struct {
	detected externalagent.Detection
	revision string
	err      error
}

// Opening history does not grant permission or capability to continue a run.
// The refusal reason remains stable for the lifetime of this admitted runner.
type readOnlySessionRunner struct{ reason error }

func (r *readOnlySessionRunner) Prompt(context.Context, string) error { return r.reason }
func (*readOnlySessionRunner) Cancel() bool                           { return false }

// This is a read-only availability snapshot: no credential reads or provider
// construction. Executable availability can change between listing and opening.
// Nonresumable CLIs report false even when unused; they can still start once.
func (s *Service) sessionContinuation(record catalog.SessionRecord) (resumable, used bool, err error) {
	return s.sessionContinuationCached(record, nil)
}

func (s *Service) sessionContinuationCached(record catalog.SessionRecord, cache map[string]cliContinuationSnapshot) (resumable, used bool, err error) {
	if record.Status == "history_too_large" {
		return false, false, ErrHistoryTooLarge
	}
	if record.Mode == "sdd_readonly" {
		return false, false, ErrInvalidInput
	}
	// Codex Evaluator sessions are isolated and ephemeral. A CLI can still emit
	// a bound thread ID while producing history, but that thread is not a
	// continuation capability for this evaluation session.
	if record.Mode == "evaluation" {
		// An API QA session that has not run yet can start; one that has run holds its verdict and is not continued.
		if _, external := s.external[record.BackendID]; external || record.Status != "ready" {
			return false, false, externalagent.ErrNotResumable
		}
	}
	if pipelineID, pipelineErr := s.store.GetPipelineIDForSession(s.ctx, record.ID); pipelineErr == nil {
		links, err := s.store.ListPipelineSessions(s.ctx, pipelineID, "coder")
		if err != nil {
			return false, false, safe("read linked Code session", err)
		}
		for _, link := range links {
			if link.SessionID != record.ID {
				continue
			}
			if len(link.ExecutionSnapshot) == 0 {
				// Older Code sessions ran in the registered project. Keep their
				// journal readable after upgrade, but never resume their tools.
				return false, true, externalagent.ErrNotResumable
			}
			if record.Mode == "sdd_code" {
				active, err := s.pipelineCodeSessionActive(record.ID)
				if err != nil || !active {
					return false, true, externalagent.ErrNotResumable
				}
				// A finished run does not close an API conversation: the person can keep refining the Code until they verify
				// it. A CLI thread cannot be picked up again once its run is over.
				if _, external := s.external[record.BackendID]; external {
					completed := false
					if err := s.walkEvents(s.ctx, record.ID, func(event events.Event) error {
						if event.Type == "run.completed" || event.Type == "external.run.completed" {
							completed = true
						}
						return nil
					}); err != nil {
						return false, true, externalagent.ErrNotResumable
					}
					if completed {
						return false, true, externalagent.ErrNotResumable
					}
				}
			}
			break
		}
	} else if !errors.Is(pipelineErr, sql.ErrNoRows) {
		return false, false, safe("read linked pipeline session", pipelineErr)
	}
	if adapter, ok := s.external[record.BackendID]; ok {
		if adapter == nil {
			return false, false, ErrBackendNotFound
		}
		hostSession := isCodexHostSession(record)
		if hostSession && !s.codexHostModeSupported(record.BackendID, record.Mode) {
			return false, false, ErrBackendChanged
		}
		if record.BackendRevision != "" && record.BackendRevision != "cli:"+adapter.ID() && !hostSession {
			return false, false, ErrBackendChanged
		}
		selection, selectionErr := s.store.GetSessionModelSelection(s.ctx, record.ID)
		var selectedDetection *externalagent.Detection
		if selectionErr == nil {
			workspace, workspaceErr := s.store.GetWorkspace(s.ctx, record.WorkspaceID)
			if workspaceErr != nil || workspace.Path != selection.WorkspacePath {
				return false, false, ErrBackendChanged
			}
			key := record.BackendID + "\x00" + workspace.Path
			snapshot, found := cache[key]
			if !found {
				snapshot.detected = adapter.Detect()
				snapshot.revision, snapshot.err = cliCatalogRevision(record.BackendID, snapshot.detected.Path, snapshot.detected.Version, workspace.Path)
				if cache != nil {
					cache[key] = snapshot
				}
			}
			selectedDetection = &snapshot.detected
			if !snapshot.detected.Available || snapshot.err != nil || selection.BackendID != record.BackendID || selection.ExecutablePath != snapshot.detected.Path || selection.ExecutableVersion != snapshot.detected.Version || selection.LocalRevision != snapshot.revision || hostSession && record.BackendRevision != codexHostRevisionPrefix+snapshot.revision {
				return false, false, ErrBackendChanged
			}
		} else if !errors.Is(selectionErr, sql.ErrNoRows) {
			return false, false, safe("read session model", selectionErr)
		}
		if hostSession {
			if selectionErr != nil {
				return false, false, ErrBackendChanged
			}
			return true, false, nil
		}
		bound := ""
		total := 0
		err := s.walkEvents(s.ctx, record.ID, func(event events.Event) error {
			total += len(event.Data)
			if total > agentcore.MaxHistoryBytes {
				return ErrSessionCorrupt
			}
			switch event.Type {
			case "external.run.started", "external.run.completed", "external.run.failed", "external.run.cancelled", "external.run.interrupted", "run.interrupted":
				used = true
			case "external.session.bound":
				var value struct {
					SessionID string `json:"sessionId"`
				}
				bound = ""
				if json.Unmarshal(event.Data, &value) == nil && externalagent.ValidSessionID(value.SessionID) {
					bound = value.SessionID
				}
			}
			return nil
		})
		if err != nil {
			return false, used, safe("read session continuation", err)
		}
		if !adapter.Capabilities().Resumable {
			if used {
				return false, true, externalagent.ErrNotResumable
			}
			if selectedDetection == nil && !adapter.Detect().Available {
				return false, false, ErrBackendNotFound
			}
			return false, false, nil
		}
		if used && bound == "" {
			return false, true, externalagent.ErrNotResumable
		}
		if record.BackendID == "codex" && bound == "" {
			if selectedDetection == nil && !adapter.Detect().Available {
				return false, false, ErrBackendNotFound
			}
			return false, false, nil
		}
		if selectedDetection == nil && !adapter.Detect().Available {
			return false, used, ErrBackendNotFound
		}
		return true, used, nil
	}
	profile, err := s.store.GetProviderProfile(s.ctx, record.BackendID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, ErrBackendNotFound
	}
	if err != nil {
		return false, false, safe("get backend", err)
	}
	if profile.Kind != "openai_compatible" {
		return false, false, ErrBackendNotFound
	}
	if err := profileNetworkAccess(profile); err != nil {
		return false, false, err
	}
	if !matchesSessionProfile(record, profile) {
		return false, false, ErrBackendChanged
	}
	return true, false, nil
}

func matchesSessionProfile(record catalog.SessionRecord, profile catalog.ProviderProfile) bool {
	if record.BackendRevision == "" {
		return !profile.UpdatedAt.After(record.CreatedAt)
	}
	return record.BackendRevision == profileRevision(profile)
}

func (s *Service) ListSessions(in ListSessionsInput) ([]SessionDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if strings.TrimSpace(in.WorkspaceID) == "" {
		return nil, ErrInvalidInput
	}
	if _, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkspaceNotFound
	} else if err != nil {
		return nil, safe("get workspace", err)
	}
	records, err := s.store.ListSessions(s.ctx, in.WorkspaceID)
	if err != nil {
		return nil, safe("list sessions", err)
	}
	result := make([]SessionDTO, 0)
	cliCache := make(map[string]cliContinuationSnapshot)
	for _, record := range records {
		if record.WorkspaceID == in.WorkspaceID {
			result = append(result, s.sessionDTOCached(record, cliCache))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	for i := range result {
		if i >= sessionTitleLimit {
			break
		}
		if result[i].Purpose == "chat" && result[i].Title == "" {
			result[i].Title = s.sessionTitle(result[i].ID)
		}
	}
	return result, nil
}

func (s *Service) OpenSession(in OpenSessionInput) (SessionDTO, error) {
	if err := s.beginCall(); err != nil {
		return SessionDTO{}, err
	}
	defer s.endCall()
	if strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.WorkspaceID) == "" {
		return SessionDTO{}, ErrInvalidInput
	}
	// Serializes admission and recovery without holding the callback/lifecycle lock.
	s.recoveryGate.Lock()
	defer s.recoveryGate.Unlock()
	record, err := s.store.GetSession(s.ctx, in.SessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionDTO{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionDTO{}, safe("get session", err)
	}
	if record.WorkspaceID != in.WorkspaceID {
		return SessionDTO{}, ErrSessionNotFound
	}
	if record.Status == "history_too_large" {
		return SessionDTO{}, ErrHistoryTooLarge
	}
	if record.Mode == "sdd_readonly" {
		return s.sessionDTO(record), nil
	}
	workspace, err := s.store.GetWorkspace(s.ctx, record.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionDTO{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return SessionDTO{}, safe("get workspace", err)
	}
	if info, err := os.Stat(workspace.Path); err != nil || !info.IsDir() {
		return SessionDTO{}, ErrWorkspaceNotFound
	}
	s.mu.RLock()
	_, exists := s.sessions[record.ID]
	s.mu.RUnlock()
	if exists {
		return s.sessionDTO(record), nil
	}
	if err := s.recoverSession(s.ctx, record); err != nil {
		return SessionDTO{}, safe("recover session", err)
	}
	record, err = s.store.GetSession(s.ctx, record.ID)
	if err != nil {
		return SessionDTO{}, safe("get session", err)
	}
	if record.Status == "history_too_large" {
		return SessionDTO{}, ErrHistoryTooLarge
	}
	history, err := s.restoreHistory(s.ctx, record.ID, false)
	if err != nil {
		return SessionDTO{}, err
	}
	_, _, continuationErr := s.sessionContinuation(record)
	var runner sessionRunner
	var journal *eventJournal
	if continuationErr == nil {
		runner, journal, continuationErr = s.makeRunner(&record, workspace, history, true, nil, "", nil)
	}
	if continuationErr != nil {
		runner = &readOnlySessionRunner{reason: continuationErr}
	}
	s.mu.Lock()
	s.sessions[record.ID] = runner
	if journal != nil {
		s.journals[record.ID] = journal
	}
	s.mu.Unlock()
	return s.sessionDTO(record), nil
}

// A hash binds the session to its structural profile and immutable credential
// reference; credential bytes never enter this catalog field.
func profileRevision(profile catalog.ProviderProfile) string {
	fields := []string{profile.ID, profile.Kind, profile.BaseURL, profile.Model, profile.CredentialProvider, profile.CredentialAccount, profile.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")}
	if profile.ProviderType != "generic" {
		fields = append(fields, profile.ProviderType)
	}
	data, _ := json.Marshal(fields)
	digest := sha256.Sum256(data)
	return "api:" + hex.EncodeToString(digest[:])
}

type restoredHistory struct {
	messages                                           []agentcore.Message
	pending                                            []agentcore.ToolCall
	externalID                                         string
	used                                               bool
	called                                             map[string]bool
	active                                             bool
	startedSequence, interruptedSequence, lastSequence int64
	status                                             string
	statusAt                                           time.Time
}

func (s *Service) restoreHistory(ctx context.Context, id string, allowPending bool) (restoredHistory, error) {
	result := restoredHistory{called: make(map[string]bool)}
	total := 0
	err := s.walkEvents(ctx, id, func(event events.Event) error {
		result.lastSequence = event.Sequence
		if status := catalogStatus(event.Type); status != "" {
			result.status, result.statusAt = status, event.CreatedAt
		}
		total += len(event.Data)
		if total > agentcore.MaxHistoryBytes || !json.Valid(event.Data) {
			return ErrSessionCorrupt
		}
		switch event.Type {
		case "run.started", "external.run.started":
			result.startedSequence, result.interruptedSequence, result.active = event.Sequence, 0, true
			if event.Type == "external.run.started" {
				result.used = true
			}
		case "run.interrupted", "external.run.interrupted":
			result.interruptedSequence, result.active = event.Sequence, false
		case "run.completed", "run.failed", "run.cancelled", "external.run.completed", "external.run.failed", "external.run.cancelled":
			result.interruptedSequence, result.active = 0, false
		case "external.session.bound":
			var data struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(event.Data, &data) != nil || !externalagent.ValidSessionID(data.SessionID) {
				return ErrSessionCorrupt
			}
			result.externalID = data.SessionID
		case "message.user", "message.assistant":
			var message agentcore.Message
			if json.Unmarshal(event.Data, &message) != nil || (event.Type == "message.user" && message.Role != agentcore.RoleUser) || (event.Type == "message.assistant" && message.Role != agentcore.RoleAssistant) || len(result.pending) > 0 {
				return ErrSessionCorrupt
			}
			result.messages = append(result.messages, message)
			result.pending = message.ToolCalls
			clear(result.called)
		case "tool.called":
			var data struct {
				ToolCallID string `json:"toolCallId"`
			}
			if json.Unmarshal(event.Data, &data) != nil {
				return ErrSessionCorrupt
			}
			for _, call := range result.pending {
				if call.ID == data.ToolCallID {
					result.called[call.ID] = true
					break
				}
			}
		case "tool.completed", "tool.failed", "tool.skipped", "tool.denied":
			var data struct {
				ToolCallID  string          `json:"toolCallId"`
				Content     json.RawMessage `json:"content"`
				ErrorCode   string          `json:"errorCode"`
				Error       string          `json:"error"`
				Recoverable bool            `json:"recoverable"`
			}
			if json.Unmarshal(event.Data, &data) != nil || len(result.pending) == 0 || data.ToolCallID != result.pending[0].ID {
				return ErrSessionCorrupt
			}
			content := data.Content
			if event.Type != "tool.completed" {
				if data.ErrorCode == "" {
					return ErrSessionCorrupt
				}
				if event.Type == "tool.failed" && data.Recoverable && data.Error != "" {
					// What the model read when the run went on: the code, the message and what the tool had produced.
					content = json.RawMessage(agentcore.ToolFailureResponse(data.ErrorCode, data.Error, data.Content))
				} else {
					content, _ = json.Marshal(map[string]string{"error": data.ErrorCode})
				}
			}
			if !json.Valid(content) {
				return ErrSessionCorrupt
			}
			result.messages = append(result.messages, agentcore.Message{Role: agentcore.RoleTool, ToolCallID: data.ToolCallID, Content: string(content)})
			result.pending = result.pending[1:]
			if result.interruptedSequence > 0 && (data.ErrorCode == "not_executed" || data.ErrorCode == "outcome_unknown") {
				result.statusAt = event.CreatedAt
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, events.ErrEventBudgetExceeded) {
			return restoredHistory{}, err
		}
		if ctx.Err() != nil {
			return restoredHistory{}, ctx.Err()
		}
		return restoredHistory{}, ErrSessionCorrupt
	}
	check := append([]agentcore.Message(nil), result.messages...)
	if allowPending && (result.active || result.interruptedSequence > 0) {
		for _, call := range result.pending {
			check = append(check, agentcore.Message{Role: agentcore.RoleTool, ToolCallID: call.ID, Content: `{"error":"app_restart"}`})
		}
	}
	if err := agentcore.ValidateHistory(check); err != nil {
		return restoredHistory{}, ErrSessionCorrupt
	}
	return result, nil
}

func catalogStatus(typ string) string {
	switch typ {
	case "run.started", "external.run.started", "approval.approved", "approval.denied":
		return "running"
	case "approval.requested":
		return "awaiting_approval"
	case "run.completed", "external.run.completed":
		return "completed"
	case "run.failed", "external.run.failed":
		return "failed"
	case "run.cancelled", "external.run.cancelled":
		return "cancelled"
	case "run.interrupted", "external.run.interrupted":
		return "paused"
	}
	return ""
}

func (j *eventJournal) updateCatalog(ctx context.Context, event events.Event) error {
	status := catalogStatus(event.Type)
	if status == "" {
		return nil
	}
	record, err := j.store.GetSession(ctx, event.StreamID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	record.Status, record.UpdatedAt = status, event.CreatedAt
	return j.store.UpsertSession(ctx, record)
}
