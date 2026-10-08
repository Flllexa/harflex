package application

import (
	"database/sql"
	"errors"
	"os"
)

func (s *Service) ListWorkspaces() ([]WorkspaceSummaryDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	workspaces, err := s.store.ListWorkspaces(s.ctx)
	if err != nil {
		return nil, safe("list workspaces", err)
	}
	result := make([]WorkspaceSummaryDTO, 0, len(workspaces))
	for _, workspace := range workspaces {
		result = append(result, workspaceSummary(workspace.ID, workspace.Path, workspace.Profile, workspace.Archived))
	}
	return result, nil
}

// SetWorkspaceArchived takes a project off the Projects list or puts it back. Nothing the project owns is deleted.
func (s *Service) SetWorkspaceArchived(in SetWorkspaceArchivedInput) (WorkspaceSummaryDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorkspaceSummaryDTO{}, err
	}
	defer s.endCall()
	if in.WorkspaceID == "" {
		return WorkspaceSummaryDTO{}, ErrInvalidInput
	}
	workspace, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceSummaryDTO{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return WorkspaceSummaryDTO{}, safe("get workspace", err)
	}
	if err := s.store.SetWorkspaceArchived(s.ctx, in.WorkspaceID, in.Archived); err != nil {
		return WorkspaceSummaryDTO{}, safe("archive workspace", err)
	}
	return workspaceSummary(workspace.ID, workspace.Path, workspace.Profile, in.Archived), nil
}

func workspaceSummary(id, path, profile string, archived bool) WorkspaceSummaryDTO {
	info, statErr := os.Stat(path)
	return WorkspaceSummaryDTO{ID: id, Path: path, Profile: profile, Available: statErr == nil && info.IsDir(), Archived: archived}
}
