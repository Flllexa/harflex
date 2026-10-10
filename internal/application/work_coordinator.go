package application

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// WorkCoordinatorDTO is an SDD work and the chat that coordinates it, as the Casual sidebar lists it.
type WorkCoordinatorDTO struct {
	SessionID    string            `json:"sessionId"`
	PipelineID   string            `json:"pipelineId"`
	Title        string            `json:"title"`
	CurrentStage string            `json:"currentStage"`
	StageStatus  map[string]string `json:"stageStatus"`
	UpdatedAt    string            `json:"updatedAt"`
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
		out = append(out, WorkCoordinatorDTO{SessionID: item.SessionID, PipelineID: item.PipelineID, Title: item.Title, CurrentStage: item.CurrentStage, StageStatus: status, UpdatedAt: item.UpdatedAt})
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

// coordinatorInstructions tells a chat which work it coordinates, so it can read and explain that work.
func (s *Service) coordinatorInstructions(sessionID string) string {
	pipelineID, err := s.store.GetPipelineIDForCoordinator(s.ctx, sessionID)
	if err != nil {
		return ""
	}
	run, err := s.loadPipeline(pipelineID)
	if err != nil {
		return ""
	}
	title := strings.TrimSpace(run.Title)
	if title == "" || !utf8.ValidString(title) {
		title = "untitled work"
	}
	quoted, _ := json.Marshal(title)
	return fmt.Sprintf("This conversation coordinates the SDD work %s (pipelineId %q). Its phases are Discovery, SPEC, Plan, Code, QA and PRs; the person approves each phase on the Pipelines screen. "+
		"When the person asks where the work stands, what is next or what to do, read it with harflex_get_pipeline (pipelineId %q; includeDocuments when you need the documents) and answer from what it says. "+
		"Keep the decisions of the work in this conversation, and say what each phase needs from the person.", quoted, pipelineID, pipelineID)
}
