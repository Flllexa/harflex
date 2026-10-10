package application

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// WorkCoordinatorDTO is an SDD work and the chat that coordinates it, as the Casual sidebar lists it.
type WorkCoordinatorDTO struct {
	SessionID    string            `json:"sessionId"`
	PipelineID   string            `json:"pipelineId"`
	Title        string            `json:"title"`
	CurrentStage string            `json:"currentStage"`
	StageStatus  map[string]string `json:"stageStatus"`
	// PreviousSessionIDs are the conversations that were the work's chat before this one; the sidebar leaves them out.
	PreviousSessionIDs []string `json:"previousSessionIds"`
	UpdatedAt          string   `json:"updatedAt"`
}

// ListWorkCoordinators returns the works of a project that have a chat coordinating them, newest first.
func (s *Service) ListWorkCoordinators(workspaceID string) ([]WorkCoordinatorDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	found, err := s.store.ListWorkCoordinators(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("list work coordinators", err)
	}
	out := make([]WorkCoordinatorDTO, 0, len(found))
	for _, item := range found {
		status := map[string]string{}
		_ = json.Unmarshal(item.StageStatus, &status)
		previous := []string{}
		_ = json.Unmarshal(item.Previous, &previous)
		out = append(out, WorkCoordinatorDTO{SessionID: item.SessionID, PipelineID: item.PipelineID, Title: item.Title, CurrentStage: item.CurrentStage, StageStatus: status, PreviousSessionIDs: previous, UpdatedAt: item.UpdatedAt})
	}
	return out, nil
}

// EnsureWorkChatsResult is the works with a chat, and how many chats this pass created.
type EnsureWorkChatsResult struct {
	Coordinators []WorkCoordinatorDTO `json:"coordinators"`
	Created      int                  `json:"created"`
}

// EnsureWorkChats gives every work of a project that has no chat one, with the default backend of the settings: that
// chat is the work's main conversation. Safe to call again; it only creates what is missing. Without a usable
// backend the works stay as they are.
func (s *Service) EnsureWorkChats(workspaceID string) (EnsureWorkChatsResult, error) {
	if workspaceID == "" {
		return EnsureWorkChatsResult{}, ErrInvalidInput
	}
	s.workChatMu.Lock()
	defer s.workChatMu.Unlock()
	runs, err := s.ListPipelines(workspaceID)
	if err != nil {
		return EnsureWorkChatsResult{}, err
	}
	existing, err := s.ListWorkCoordinators(workspaceID)
	if err != nil {
		return EnsureWorkChatsResult{}, err
	}
	covered := make(map[string]bool, len(existing))
	for _, item := range existing {
		covered[item.PipelineID] = true
	}
	created := 0
	if settings, err := s.GetSettings(); err == nil && settings.DefaultBackendID != "" {
		for _, run := range runs {
			if covered[run.ID] {
				continue
			}
			chat, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspaceID, BackendID: settings.DefaultBackendID, Reason: run.Title})
			if err != nil {
				break // no usable provider right now: the works wait for the next pass
			}
			if err := s.store.SetPipelineCoordinator(s.ctx, run.ID, chat.ID); err != nil {
				return EnsureWorkChatsResult{}, safe("link work coordinator", err)
			}
			created++
		}
	}
	coordinators, err := s.ListWorkCoordinators(workspaceID)
	return EnsureWorkChatsResult{Coordinators: coordinators, Created: created}, err
}

// coordinatorInstructions tells a chat which work it coordinates and where that work stands, so it can answer "where are we"
// and carry on with what is missing without exploring the project to find out.
func (s *Service) coordinatorInstructions(sessionID string) string {
	pipelineID, err := s.store.GetPipelineIDForCoordinator(s.ctx, sessionID)
	if err != nil {
		return ""
	}
	report, err := s.workStatusReport(pipelineID)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("This conversation IS the main chat of an SDD work (pipelineId %q), not a loose chat. Its phases are Discovery, SPEC, Plan, Code, QA and PRs; the person approves each phase on the Pipelines screen, and they come back here to talk about the work and to ask for more. "+
		"Here is where the work stood when this conversation started:\n\n%s\n"+
		"The state moves while people work, so for a current answer read it again with harflex_get_pipeline (pipelineId %q; includeDocuments when you need the documents) rather than searching the project, other tools or past files. "+
		"When asked what was done or where it stopped, answer from this state, phase by phase. If the person wants more done in the work, do it or say which phase and screen it belongs to. Keep the decisions of the work in this conversation.", pipelineID, report, pipelineID)
}

// ContinueWorkChat makes next the chat of the work that previous coordinated, when it coordinated one: a conversation that
// moved to another model, or that could not be resumed, is still the work's chat. It reports whether anything moved.
func (s *Service) ContinueWorkChat(previousSessionID, nextSessionID string) (bool, error) {
	if err := s.beginCall(); err != nil {
		return false, err
	}
	defer s.endCall()
	if previousSessionID == "" || nextSessionID == "" || previousSessionID == nextSessionID {
		return false, ErrInvalidInput
	}
	pipelineID, err := s.store.GetPipelineIDForCoordinator(s.ctx, previousSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, safe("read work chat", err)
	}
	run, err := s.loadPipeline(pipelineID)
	if err != nil {
		return false, safe("read work", err)
	}
	next, err := s.store.GetSession(s.ctx, nextSessionID)
	if err != nil || next.WorkspaceID != run.WorkspaceID {
		return false, ErrSessionNotFound
	}
	if err := s.store.ReplacePipelineCoordinator(s.ctx, pipelineID, previousSessionID, nextSessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, safe("move work chat", err)
	}
	return true, nil
}
