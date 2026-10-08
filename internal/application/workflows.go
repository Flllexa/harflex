package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

var ErrWorkflowNotFound = errors.New("workflow not found")
var ErrWorkflowRunNotFound = errors.New("workflow run not found")
var ErrWorkflowCancelUnconfirmed = errors.New("workflow session cancellation was not confirmed")

func workflowDTO(item catalog.Workflow) WorkflowDTO {
	steps := make([]WorkflowStepInput, 0, len(item.Steps))
	for _, step := range item.Steps {
		steps = append(steps, WorkflowStepInput{Name: step.Name, Prompt: step.Prompt})
	}
	return WorkflowDTO{ID: item.ID, WorkspaceID: item.WorkspaceID, Name: item.Name, Steps: steps, Revision: item.Revision, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func workflowRunDTO(item catalog.WorkflowRun) WorkflowRunDTO {
	steps := make([]WorkflowStepInput, 0, len(item.Steps))
	for _, step := range item.Steps {
		steps = append(steps, WorkflowStepInput{Name: step.Name, Prompt: step.Prompt})
	}
	return WorkflowRunDTO{ID: item.ID, WorkflowID: item.WorkflowID, WorkspaceID: item.WorkspaceID, BackendID: item.BackendID, Steps: steps, CurrentStep: item.CurrentStep, Status: item.Status, LastSessionID: item.LastSessionID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func (s *Service) SaveWorkflow(in SaveWorkflowInput) (WorkflowDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorkflowDTO{}, err
	}
	defer s.endCall()
	in.Name = strings.TrimSpace(in.Name)
	if in.WorkspaceID == "" || in.Name == "" || len(in.Name) > 200 || len(in.Steps) == 0 || len(in.Steps) > 20 {
		return WorkflowDTO{}, ErrInvalidInput
	}
	if _, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID); errors.Is(err, sql.ErrNoRows) {
		return WorkflowDTO{}, ErrWorkspaceNotFound
	} else if err != nil {
		return WorkflowDTO{}, safe("get workflow workspace", err)
	}
	steps := make([]catalog.WorkflowStep, 0, len(in.Steps))
	for _, step := range in.Steps {
		step.Name, step.Prompt = strings.TrimSpace(step.Name), strings.TrimSpace(step.Prompt)
		if step.Name == "" || len(step.Name) > 128 || step.Prompt == "" || len(step.Prompt) > maxPromptBytes {
			return WorkflowDTO{}, ErrInvalidInput
		}
		steps = append(steps, catalog.WorkflowStep{Name: step.Name, Prompt: step.Prompt})
	}
	now := time.Now().UTC()
	item := catalog.Workflow{ID: in.ID, WorkspaceID: in.WorkspaceID, Name: in.Name, Steps: steps, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if in.ID == "" {
		item.ID = id.New()
	} else {
		previous, err := s.store.GetWorkflow(s.ctx, in.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowDTO{}, ErrWorkflowNotFound
		}
		if err != nil {
			return WorkflowDTO{}, safe("get workflow", err)
		}
		if previous.WorkspaceID != in.WorkspaceID {
			return WorkflowDTO{}, ErrWorkflowNotFound
		}
		item.CreatedAt, item.Revision = previous.CreatedAt, previous.Revision+1
	}
	if err := s.store.SaveWorkflow(s.ctx, item); err != nil {
		return WorkflowDTO{}, safe("save workflow", err)
	}
	return workflowDTO(item), nil
}

func (s *Service) ListWorkflows(workspaceID string) ([]WorkflowDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	items, err := s.store.ListWorkflows(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("list workflows", err)
	}
	result := make([]WorkflowDTO, 0, len(items))
	for _, item := range items {
		result = append(result, workflowDTO(item))
	}
	return result, nil
}

func (s *Service) GetWorkflowRun(id string) (WorkflowRunDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorkflowRunDTO{}, err
	}
	defer s.endCall()
	item, err := s.store.GetWorkflowRun(s.ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowRunDTO{}, ErrWorkflowRunNotFound
	}
	if err != nil {
		return WorkflowRunDTO{}, safe("get workflow run", err)
	}
	return workflowRunDTO(item), nil
}

func (s *Service) ListWorkflowRuns(workspaceID string) ([]WorkflowRunDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	items, err := s.store.ListWorkflowRuns(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("list workflow runs", err)
	}
	result := make([]WorkflowRunDTO, 0, len(items))
	for _, item := range items {
		result = append(result, workflowRunDTO(item))
	}
	return result, nil
}

func (s *Service) StartWorkflow(in StartWorkflowInput) (WorkflowRunDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorkflowRunDTO{}, err
	}
	defer s.endCall()
	if in.WorkflowID == "" || in.BackendID == "" {
		return WorkflowRunDTO{}, ErrInvalidInput
	}
	workflow, err := s.store.GetWorkflow(s.ctx, in.WorkflowID)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowRunDTO{}, ErrWorkflowNotFound
	}
	if err != nil {
		return WorkflowRunDTO{}, safe("get workflow", err)
	}
	available := false
	for _, backend := range s.ListBackends() {
		if backend.ID == in.BackendID && backend.Available {
			available = true
			break
		}
	}
	if !available {
		return WorkflowRunDTO{}, ErrBackendNotFound
	}
	now := time.Now().UTC()
	item := catalog.WorkflowRun{ID: id.New(), WorkflowID: workflow.ID, WorkspaceID: workflow.WorkspaceID, BackendID: in.BackendID, Steps: append([]catalog.WorkflowStep(nil), workflow.Steps...), Status: "ready", CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreateWorkflowRun(s.ctx, item); err != nil {
		return WorkflowRunDTO{}, safe("create workflow run", err)
	}
	return workflowRunDTO(item), nil
}

// ResumeWorkflowRun records the user's recovery choice. Retry only returns
// the step to ready; a second, separate action is required to execute it.
func (s *Service) ResumeWorkflowRun(in ResumeWorkflowRunInput) (WorkflowRunDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorkflowRunDTO{}, err
	}
	defer s.endCall()
	if in.RunID == "" || in.ReviewedSessionID == "" || (in.Choice != "retry" && in.Choice != "skip") {
		return WorkflowRunDTO{}, ErrInvalidInput
	}
	item, err := s.store.GetWorkflowRun(s.ctx, in.RunID)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowRunDTO{}, ErrWorkflowRunNotFound
	}
	if err != nil {
		return WorkflowRunDTO{}, safe("get paused workflow", err)
	}
	if item.Status != "paused" || item.LastSessionID != in.ReviewedSessionID || item.CurrentStep >= len(item.Steps) {
		return WorkflowRunDTO{}, ErrInvalidInput
	}
	record, err := s.store.GetSession(s.ctx, item.LastSessionID)
	if err != nil {
		return WorkflowRunDTO{}, safe("get reviewed workflow session", err)
	}
	if record.WorkspaceID != item.WorkspaceID || (record.Status != "failed" && record.Status != "cancelled" && record.Status != "paused") {
		return WorkflowRunDTO{}, ErrInvalidInput
	}
	item.UpdatedAt = time.Now().UTC()
	eventType := "workflow.step.retry_selected"
	if in.Choice == "retry" {
		item.Status = "ready"
	} else {
		item.CurrentStep++
		item.Status = "ready"
		if item.CurrentStep >= len(item.Steps) {
			item.Status = "completed"
		}
		eventType = "workflow.step.skipped"
	}
	if err := s.store.UpdateWorkflowRun(s.ctx, item, "paused", eventType); err != nil {
		if errors.Is(err, sqlite.ErrWorkflowConflict) {
			return WorkflowRunDTO{}, ErrInvalidInput
		}
		return WorkflowRunDTO{}, safe("record workflow recovery choice", err)
	}
	return workflowRunDTO(item), nil
}

func (s *Service) RunWorkflowStep(runID string) (WorkflowRunDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorkflowRunDTO{}, err
	}
	defer s.endCall()
	item, err := s.store.GetWorkflowRun(s.ctx, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowRunDTO{}, ErrWorkflowRunNotFound
	}
	if err != nil {
		return WorkflowRunDTO{}, safe("get workflow run", err)
	}
	if item.Status == "waiting_user" || item.Status == "running" {
		if item.LastSessionID == "" {
			return WorkflowRunDTO{}, ErrInvalidInput
		}
		record, err := s.store.GetSession(s.ctx, item.LastSessionID)
		if err != nil {
			return WorkflowRunDTO{}, safe("get workflow step session", err)
		}
		if record.Status == "completed" {
			before := item.Status
			item.CurrentStep++
			item.UpdatedAt = time.Now().UTC()
			item.Status = "ready"
			eventType := "workflow.step.completed"
			if item.CurrentStep >= len(item.Steps) {
				item.Status = "completed"
				eventType = "workflow.completed"
			}
			if err := s.store.UpdateWorkflowRun(s.ctx, item, before, eventType); err != nil {
				return WorkflowRunDTO{}, safe("finish workflow step", err)
			}
		} else if record.Status == "failed" || record.Status == "cancelled" || record.Status == "paused" {
			before := item.Status
			item.Status = "paused"
			item.UpdatedAt = time.Now().UTC()
			if err := s.store.UpdateWorkflowRun(s.ctx, item, before, "workflow.step.paused"); err != nil {
				return WorkflowRunDTO{}, safe("pause workflow step", err)
			}
		}
		return workflowRunDTO(item), nil
	}
	if item.Status != "ready" || item.CurrentStep >= len(item.Steps) {
		return WorkflowRunDTO{}, ErrInvalidInput
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: item.WorkspaceID, BackendID: item.BackendID})
	if err != nil {
		return WorkflowRunDTO{}, err
	}
	item.LastSessionID = session.ID
	item.Status = "running"
	item.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateWorkflowRun(s.ctx, item, "ready", "workflow.step.started"); err != nil {
		return WorkflowRunDTO{}, safe("start workflow step", err)
	}
	result, runErr := s.Prompt(PromptInput{SessionID: session.ID, Text: item.Steps[item.CurrentStep].Prompt})
	before := item.Status
	item.UpdatedAt = time.Now().UTC()
	switch {
	case runErr != nil || result.Status == RunFailed || result.Status == RunCancelled:
		item.Status = "paused"
	case result.Status == RunAwaitingApproval:
		item.Status = "waiting_user"
	case result.Status == RunCompleted:
		item.CurrentStep++
		item.Status = "ready"
		if item.CurrentStep >= len(item.Steps) {
			item.Status = "completed"
		}
	default:
		item.Status = "paused"
	}
	eventType := "workflow.step.paused"
	if item.Status == "waiting_user" {
		eventType = "workflow.step.waiting_user"
	} else if item.Status == "ready" {
		eventType = "workflow.step.completed"
	} else if item.Status == "completed" {
		eventType = "workflow.completed"
	}
	if err := s.store.UpdateWorkflowRun(s.ctx, item, before, eventType); err != nil {
		if errors.Is(err, sqlite.ErrWorkflowConflict) {
			latest, readErr := s.store.GetWorkflowRun(s.ctx, item.ID)
			if readErr == nil && latest.Status == "cancelled" {
				return workflowRunDTO(latest), nil
			}
		}
		return WorkflowRunDTO{}, safe("record workflow outcome", err)
	}
	if runErr != nil {
		return workflowRunDTO(item), runErr
	}
	return workflowRunDTO(item), nil
}

// The GUI never automatically repeats a paused step because a tool may have
// produced effects before a crash. The user must inspect the linked session.
func (s *Service) CancelWorkflowRun(runID string) (WorkflowRunDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorkflowRunDTO{}, err
	}
	defer s.endCall()
	item, err := s.store.GetWorkflowRun(s.ctx, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowRunDTO{}, ErrWorkflowRunNotFound
	}
	if err != nil {
		return WorkflowRunDTO{}, safe("get workflow run", err)
	}
	if item.Status == "completed" {
		return workflowRunDTO(item), nil
	}
	if item.Status == "cancelled" {
		if item.LastSessionID != "" {
			record, err := s.store.GetSession(s.ctx, item.LastSessionID)
			if err != nil {
				return WorkflowRunDTO{}, errors.Join(ErrWorkflowCancelUnconfirmed, err)
			}
			if record.Status == "ready" || record.Status == "running" || record.Status == "awaiting_approval" {
				if err := s.abortWorkflowSession(item.LastSessionID); err != nil {
					return WorkflowRunDTO{}, err
				}
			} else if record.Status == "completed" {
				return WorkflowRunDTO{}, ErrWorkflowCancelUnconfirmed
			}
		}
		return workflowRunDTO(item), nil
	}
	if item.Status == "running" || item.Status == "waiting_user" {
		if item.LastSessionID == "" {
			return WorkflowRunDTO{}, ErrWorkflowCancelUnconfirmed
		}
		if err := s.abortWorkflowSession(item.LastSessionID); err != nil {
			return WorkflowRunDTO{}, err
		}
	}
	before := item.Status
	item.Status = "cancelled"
	item.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateWorkflowRun(s.ctx, item, before, "workflow.cancelled"); err != nil {
		if errors.Is(err, sqlite.ErrWorkflowConflict) {
			return WorkflowRunDTO{}, ErrInvalidInput
		}
		return WorkflowRunDTO{}, safe("cancel workflow run", err)
	}
	return workflowRunDTO(item), nil
}

func (s *Service) abortWorkflowSession(sessionID string) error {
	record, err := s.store.GetSession(s.ctx, sessionID)
	if err != nil {
		return errors.Join(ErrWorkflowCancelUnconfirmed, err)
	}
	if record.Status == "cancelled" || record.Status == "failed" {
		return nil
	}
	if record.Status == "completed" || record.Status == "paused" {
		return ErrWorkflowCancelUnconfirmed
	}
	runner, err := s.runner(sessionID)
	if err != nil {
		return errors.Join(ErrWorkflowCancelUnconfirmed, err)
	}
	aborter, ok := runner.(interface{ Abort(context.Context) error })
	if !ok {
		return ErrWorkflowCancelUnconfirmed
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 5*time.Second)
	defer cancel()
	if err := aborter.Abort(ctx); err != nil {
		return errors.Join(ErrWorkflowCancelUnconfirmed, err)
	}
	record, err = s.store.GetSession(ctx, sessionID)
	if err != nil {
		return errors.Join(ErrWorkflowCancelUnconfirmed, err)
	}
	if record.Status != "cancelled" && record.Status != "failed" {
		return ErrWorkflowCancelUnconfirmed
	}
	return nil
}
