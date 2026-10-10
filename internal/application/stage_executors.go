package application

import (
	"database/sql"
	"errors"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

// ErrStageExecutorUnsupported says the phase cannot be worked on by that executor: only API profiles, Codex and Claude Code run
// the phases, and the pull requests need the Harflex tool loop, which only API profiles have.
var ErrStageExecutorUnsupported = errors.New("this phase cannot be worked on by that executor")

// StageExecutorDTO is the executor a project chose for one phase of its pipelines.
type StageExecutorDTO struct {
	WorkspaceID string    `json:"workspaceId"`
	Stage       string    `json:"stage"`
	BackendID   string    `json:"backendId"`
	ModelID     string    `json:"modelId"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type SaveStageExecutorInput struct {
	WorkspaceID string `json:"workspaceId"`
	Stage       string `json:"stage"`
	BackendID   string `json:"backendId"`
	// ModelID is empty when the executor's own model is meant (the model of an API profile).
	ModelID string `json:"modelId"`
}

func validStageExecutorStage(stage string) bool {
	for _, known := range sdd.Stages {
		if string(known) == stage {
			return true
		}
	}
	return false
}

func stageExecutorDTO(value catalog.StageExecutor) StageExecutorDTO {
	return StageExecutorDTO{WorkspaceID: value.WorkspaceID, Stage: string(value.Stage), BackendID: value.BackendID, ModelID: value.ModelID, UpdatedAt: value.UpdatedAt}
}

// ListStageExecutors returns the phases of a project that have an executor of their own; the others use the default
// of Settings.
func (s *Service) ListStageExecutors(workspaceID string) ([]StageExecutorDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if !validSelectionText(workspaceID, 128) {
		return nil, ErrInvalidInput
	}
	if _, err := s.store.GetWorkspace(s.ctx, workspaceID); err != nil {
		return nil, ErrWorkspaceNotFound
	}
	values, err := s.store.ListWorkspaceStageExecutors(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("read phase executors", err)
	}
	result := make([]StageExecutorDTO, 0, len(values))
	for _, value := range values {
		result = append(result, stageExecutorDTO(value))
	}
	return result, nil
}

// SaveStageExecutor chooses who works on a phase. It keeps identifiers only: the model is checked against the
// catalog when the phase runs, because a catalog answer expires and a saved choice must not.
func (s *Service) SaveStageExecutor(in SaveStageExecutorInput) (StageExecutorDTO, error) {
	if err := s.beginCall(); err != nil {
		return StageExecutorDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(in.WorkspaceID, 128) || !validStageExecutorStage(in.Stage) || !validSelectionText(in.BackendID, 128) || (in.ModelID != "" && !validSelectionText(in.ModelID, 512)) {
		return StageExecutorDTO{}, ErrInvalidInput
	}
	if _, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID); err != nil {
		return StageExecutorDTO{}, ErrWorkspaceNotFound
	}
	if _, external := s.external[in.BackendID]; external {
		// The pull requests return to their conversation again and again, so only a CLI that resumes it (Claude Code) can run them.
		if !documentCLI(in.BackendID) || (sdd.Stage(in.Stage) == sdd.PRs && in.BackendID != "claude") {
			return StageExecutorDTO{}, ErrStageExecutorUnsupported
		}
	} else {
		profile, err := s.store.GetProviderProfile(s.ctx, in.BackendID)
		if errors.Is(err, sql.ErrNoRows) {
			return StageExecutorDTO{}, ErrBackendNotFound
		}
		if err != nil {
			return StageExecutorDTO{}, safe("read the phase provider", err)
		}
		// A generic server publishes no catalog the Harflex can confirm a model against. The document, Code and QA phases
		// need one; the pull requests run on the profile's own model, and cannot be bound to another.
		generic := profile.ProviderType == "generic"
		if profile.Kind != "openai_compatible" || (generic && (sdd.Stage(in.Stage) != sdd.PRs || in.ModelID != "")) {
			return StageExecutorDTO{}, ErrStageExecutorUnsupported
		}
	}
	value := catalog.StageExecutor{WorkspaceID: in.WorkspaceID, Stage: sdd.Stage(in.Stage), BackendID: in.BackendID, ModelID: in.ModelID, UpdatedAt: time.Now().UTC()}
	if err := s.store.SaveWorkspaceStageExecutor(s.ctx, value); err != nil {
		return StageExecutorDTO{}, safe("save the phase executor", err)
	}
	return stageExecutorDTO(value), nil
}

// ClearStageExecutor returns a phase to the default of Settings.
func (s *Service) ClearStageExecutor(workspaceID, stage string) error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	if !validSelectionText(workspaceID, 128) || !validStageExecutorStage(stage) {
		return ErrInvalidInput
	}
	if _, err := s.store.GetWorkspace(s.ctx, workspaceID); err != nil {
		return ErrWorkspaceNotFound
	}
	if err := s.store.DeleteWorkspaceStageExecutor(s.ctx, workspaceID, sdd.Stage(stage)); err != nil {
		return safe("clear the phase executor", err)
	}
	return nil
}
