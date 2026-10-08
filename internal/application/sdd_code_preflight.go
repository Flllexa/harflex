package application

import (
	"database/sql"
	"errors"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/sddworkspace"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

type PreflightAuthoringCodeInput struct {
	PipelineID                     string `json:"pipelineId"`
	ExpectedPipelineRevision       int64  `json:"expectedPipelineRevision"`
	ExpectedCodePreferenceRevision int64  `json:"expectedCodePreferenceRevision"`
}

// AuthoringCodePreflightDTO contains only local metadata required for a human
// to review a future private copy and bind the next call to this snapshot.
type AuthoringCodePreflightDTO struct {
	PipelineID             string                           `json:"pipelineId"`
	WorkspaceID            string                           `json:"workspaceId"`
	PipelineRevision       int64                            `json:"pipelineRevision"`
	CodePreferenceRevision int64                            `json:"codePreferenceRevision"`
	CodePreference         AuthoringStageModelPreferenceDTO `json:"codePreference"`
	SourcePath             string                           `json:"sourcePath"`
	PrivateParentPath      string                           `json:"privateParentPath"`
	Manifest               sddworkspace.Manifest            `json:"manifest"`
	ManifestHash           string                           `json:"manifestHash"`
	CodeSelectionHash      string                           `json:"codeSelectionHash"`
}

func authoringCodeSelectionHash(preference AuthoringStageModelPreferenceDTO) (string, error) {
	return catalog.HashAuthoringCodeSelection(authoringCodeCopyPreferenceSnapshot(preference, ""))
}

func validAuthoringCodePreflightStage(run catalog.PipelineRun) bool {
	if run.Kind != "ai_authoring" || run.Current != sdd.Code || run.Status[sdd.Code] != sdd.Active {
		return false
	}
	return run.Status[sdd.Plan] == sdd.Completed || run.Status[sdd.Plan] == sdd.Skipped
}

func (s *Service) PreflightAuthoringCode(in PreflightAuthoringCodeInput) (AuthoringCodePreflightDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringCodePreflightDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(in.PipelineID, 128) || in.ExpectedPipelineRevision < 1 || in.ExpectedCodePreferenceRevision < 0 {
		return AuthoringCodePreflightDTO{}, ErrInvalidInput
	}

	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return AuthoringCodePreflightDTO{}, err
	}
	if run.Revision != in.ExpectedPipelineRevision {
		return AuthoringCodePreflightDTO{}, sqlite.ErrPipelineConflict
	}
	if !validAuthoringCodePreflightStage(run) {
		return AuthoringCodePreflightDTO{}, sdd.ErrInvalidTransition
	}

	preference, err := s.store.GetAuthoringStageModelPreference(s.ctx, run.ID, sdd.Code)
	if err != nil {
		return AuthoringCodePreflightDTO{}, safe("read Code model preference", err)
	}
	if preference.Revision != in.ExpectedCodePreferenceRevision {
		return AuthoringCodePreflightDTO{}, sqlite.ErrPipelineConflict
	}

	workspace, err := s.store.GetWorkspace(s.ctx, run.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthoringCodePreflightDTO{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return AuthoringCodePreflightDTO{}, safe("read authoring workspace", err)
	}
	manifest, err := sddworkspace.PreflightPrivateCopy(s.ctx, workspace.Path, s.privateWorkspaceParent)
	if err != nil {
		return AuthoringCodePreflightDTO{}, safe("preflight private Code copy", err)
	}

	current, err := s.loadPipeline(run.ID)
	if err != nil {
		return AuthoringCodePreflightDTO{}, err
	}
	if current.Revision != in.ExpectedPipelineRevision {
		return AuthoringCodePreflightDTO{}, sqlite.ErrPipelineConflict
	}
	if !validAuthoringCodePreflightStage(current) {
		return AuthoringCodePreflightDTO{}, sdd.ErrInvalidTransition
	}
	preference, err = s.store.GetAuthoringStageModelPreference(s.ctx, run.ID, sdd.Code)
	if err != nil {
		return AuthoringCodePreflightDTO{}, safe("recheck Code model preference", err)
	}
	if preference.Revision != in.ExpectedCodePreferenceRevision {
		return AuthoringCodePreflightDTO{}, sqlite.ErrPipelineConflict
	}
	resolved, err := s.resolveAuthoringStageModelPreference(s.ctx, preference)
	if err != nil {
		return AuthoringCodePreflightDTO{}, err
	}
	current, err = s.loadPipeline(run.ID)
	if err != nil {
		return AuthoringCodePreflightDTO{}, err
	}
	if current.Revision != in.ExpectedPipelineRevision {
		return AuthoringCodePreflightDTO{}, sqlite.ErrPipelineConflict
	}
	if !validAuthoringCodePreflightStage(current) {
		return AuthoringCodePreflightDTO{}, sdd.ErrInvalidTransition
	}
	latestPreference, err := s.store.GetAuthoringStageModelPreference(s.ctx, run.ID, sdd.Code)
	if err != nil {
		return AuthoringCodePreflightDTO{}, safe("confirm Code model preference revision", err)
	}
	if latestPreference.Revision != in.ExpectedCodePreferenceRevision {
		return AuthoringCodePreflightDTO{}, sqlite.ErrPipelineConflict
	}
	selectionHash, err := authoringCodeSelectionHash(resolved)
	if err != nil {
		return AuthoringCodePreflightDTO{}, safe("fingerprint Code model selection", err)
	}

	return AuthoringCodePreflightDTO{
		PipelineID: run.ID, WorkspaceID: workspace.ID, PipelineRevision: run.Revision,
		CodePreferenceRevision: preference.Revision, CodePreference: resolved,
		SourcePath: workspace.Path, PrivateParentPath: s.privateWorkspaceParent,
		Manifest: manifest, ManifestHash: manifest.Hash, CodeSelectionHash: selectionHash,
	}, nil
}
