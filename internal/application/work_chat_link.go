package application

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/sdd"
)

// Every chat is a work, and every work has a chat: the first message of a new chat opens a work with that message as
// its Discovery, coordinated by the chat; a work created anywhere else gets its chat right away.

// instructionFollower is a conversation already open that can take a note at the head of its instructions.
type instructionFollower interface{ AddInstructions(note string) bool }

func (r *selectedCLIRunner) AddInstructions(note string) bool {
	follower, ok := r.base.(instructionFollower)
	return ok && follower.AddInstructions(note)
}

func (r *selectedAPIRunner) AddInstructions(note string) bool {
	follower, ok := r.base.(instructionFollower)
	return ok && follower.AddInstructions(note)
}

// emitWork announces a work and its chat: reason is "chat" when the chat's first message opened it, "agent" when the agent wrote its Discovery.
func (s *Service) emitWork(workspaceID, pipelineID, sessionID, reason string) {
	s.mu.RLock()
	emit := s.emit
	s.mu.RUnlock()
	if emit != nil {
		emit("harflex:pipeline", map[string]string{"workspaceId": workspaceID, "pipelineId": pipelineID, "sessionId": sessionID, "reason": reason})
	}
}

// linkNewChatToWork opens the work of a new chat on its first message. A conversation that belongs to something else (a
// pipeline stage, the pull requests, a delegated task) or that already coordinates a work is left alone.
func (s *Service) linkNewChatToWork(record catalog.SessionRecord, runner sessionRunner, text string) {
	if record.Mode != "" || record.ID == "" {
		return
	}
	for _, check := range []func() error{
		func() error { _, err := s.store.GetPipelineIDForCoordinator(s.ctx, record.ID); return err },
		func() error { _, err := s.store.GetPipelineIDForSession(s.ctx, record.ID); return err },
		func() error { _, err := s.store.GetPipelineIDForPRSession(s.ctx, record.ID); return err },
		func() error { _, err := s.store.GetParentDelegation(s.ctx, record.ID); return err },
	} {
		if err := check(); !errors.Is(err, sql.ErrNoRows) {
			return // linked to something, or unknown: nothing is created
		}
	}
	first := true
	_ = s.walkEvents(s.ctx, record.ID, func(event events.Event) error {
		if event.Type == "message.user" {
			first = false
		}
		return nil
	})
	if !first {
		return
	}
	request := text
	if at := strings.Index(request, continuationMarker); at >= 0 {
		request = request[:at]
	}
	request = strings.TrimSpace(request)
	if request == "" {
		return
	}
	// The Discovery is stored as written: it gets the same credential redaction as the chat's own journal.
	request, ok := s.redactedForSession(record.ID, request)
	if !ok || strings.TrimSpace(request) == "" {
		return
	}
	created, err := s.createAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: record.WorkspaceID, RequestID: randomRequestID(), Discovery: clipUTF8(request, maxDiscoveryBytes)})
	if err != nil {
		return
	}
	if err := s.store.SetPipelineCoordinator(s.ctx, created.ID, record.ID); err != nil {
		return
	}
	// The conversation is already open: it learns from this turn on which work it coordinates.
	if follower, ok := runner.(instructionFollower); ok {
		follower.AddInstructions(s.coordinatorInstructions(record.ID))
	}
	s.emitWork(record.WorkspaceID, created.ID, record.ID, "chat")
}

// ensureWorkChat gives a work created outside a chat its chat, with the default backend of the settings. Without a
// usable backend it waits for EnsureWorkChats.
func (s *Service) ensureWorkChat(run PipelineDTO) {
	if coordinators, err := s.store.ListWorkCoordinators(s.ctx, run.WorkspaceID); err == nil {
		for _, item := range coordinators {
			if item.PipelineID == run.ID {
				return
			}
		}
	}
	settings, err := s.GetSettings()
	if err != nil || settings.DefaultBackendID == "" {
		return
	}
	title := strings.TrimSpace(run.Title)
	if title == "" {
		title = "Trabalho"
	}
	chat, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: run.WorkspaceID, BackendID: settings.DefaultBackendID, Reason: clipUTF8(strings.ReplaceAll(title, "\n", " "), 200)})
	if err != nil {
		return
	}
	_ = s.store.SetPipelineCoordinator(s.ctx, run.ID, chat.ID)
}

// updateWorkDiscovery replaces the Discovery of the work a chat coordinates, while the work is still at Discovery: the
// agent agreed the problem with the person and writes it down in the chat's own work instead of opening another.
func (s *Service) updateWorkDiscovery(pipelineID, discovery string) (PipelineDTO, error) {
	run, err := s.GetPipeline(pipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	if run.Kind != "ai_authoring" || run.CurrentStage != string(sdd.Discovery) {
		return PipelineDTO{}, errWorkPastDiscovery
	}
	if current, ok := run.Artifacts[string(sdd.Discovery)]; ok && current.Version > 0 {
		if updated, err := s.ReviseAuthoringDiscovery(ReviseAuthoringDiscoveryInput{PipelineID: pipelineID, ExpectedRevision: run.Revision, ExpectedVersion: current.Version, Discovery: discovery}); err == nil {
			return updated, nil
		}
	}
	// A work whose documents are prepared in the conversational studio keeps its Discovery there.
	design, err := s.OpenPipelineDesign(pipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	if _, err := s.EditPipelineDesignDocument(EditPipelineDesignDocumentInput{Ref: PipelineDesignRefInput{PipelineID: pipelineID, RequestID: randomRequestID(), PipelineRevision: design.CurrentPipelineRevision, DesignRevision: design.Revision}, Stage: "discovery", Content: discovery}); err != nil {
		return PipelineDTO{}, err
	}
	return s.GetPipeline(pipelineID)
}

var errWorkPastDiscovery = errors.New("the work of this chat is already past Discovery")

// redactedForSession hides from text the credentials the session's journal hides; false when redaction is unavailable.
func (s *Service) redactedForSession(sessionID, text string) (string, bool) {
	s.mu.RLock()
	journal := s.journals[sessionID]
	s.mu.RUnlock()
	if journal == nil || journal.redactor == nil {
		return "", false
	}
	journal.mu.Lock()
	raw, err := journal.redactor.eventPayload(map[string]string{"text": text}, "message.user")
	journal.mu.Unlock()
	if err != nil {
		return "", false
	}
	var out struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return "", false
	}
	return out.Text, true
}
