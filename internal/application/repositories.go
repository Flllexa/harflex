package application

import (
	"database/sql"
	"errors"

	"github.com/persioflexa/harflex/internal/repositories"
)

func (s *Service) InspectRepository(workspaceID string) (RepositoryDTO, error) {
	if err := s.beginCall(); err != nil {
		return RepositoryDTO{}, err
	}
	defer s.endCall()
	if workspaceID == "" {
		return RepositoryDTO{}, ErrInvalidInput
	}
	workspace, err := s.store.GetWorkspace(s.ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryDTO{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return RepositoryDTO{}, safe("get workspace", err)
	}
	snapshot, err := repositories.Inspect(s.ctx, workspace.Path)
	if errors.Is(err, repositories.ErrGitUnavailable) {
		return RepositoryDTO{}, err
	}
	if err != nil {
		return RepositoryDTO{}, safe("inspect repository", err)
	}
	return RepositoryDTO{IsRepository: snapshot.IsRepository, Root: snapshot.Root, Branch: snapshot.Branch, Files: snapshot.Files, StagedDiff: snapshot.StagedDiff, UnstagedDiff: snapshot.UnstagedDiff, Truncated: snapshot.Truncated}, nil
}
