package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

const stageExecutorColumns = "workspace_id, stage, backend_id, model_id, updated_at"

func scanStageExecutor(scan func(...any) error) (catalog.StageExecutor, error) {
	var value catalog.StageExecutor
	var stage, updated string
	if err := scan(&value.WorkspaceID, &stage, &value.BackendID, &value.ModelID, &updated); err != nil {
		return catalog.StageExecutor{}, err
	}
	value.Stage = sdd.Stage(stage)
	parsed, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return catalog.StageExecutor{}, fmt.Errorf("parse phase executor timestamp: %w", err)
	}
	value.UpdatedAt = parsed
	return value, nil
}

// ListWorkspaceStageExecutors returns the phases of a project that have an executor of their own, in pipeline order.
func (s *Store) ListWorkspaceStageExecutors(ctx context.Context, workspaceID string) ([]catalog.StageExecutor, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+stageExecutorColumns+" FROM workspace_stage_executors WHERE workspace_id=?", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list phase executors: %w", err)
	}
	defer rows.Close()
	byStage := map[sdd.Stage]catalog.StageExecutor{}
	for rows.Next() {
		value, err := scanStageExecutor(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan phase executor: %w", err)
		}
		byStage[value.Stage] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate phase executors: %w", err)
	}
	result := make([]catalog.StageExecutor, 0, len(byStage))
	for _, stage := range sdd.Stages {
		if value, found := byStage[stage]; found {
			result = append(result, value)
		}
	}
	return result, nil
}

// GetWorkspaceStageExecutor reads the executor of one phase; sql.ErrNoRows means the phase uses the default.
func (s *Store) GetWorkspaceStageExecutor(ctx context.Context, workspaceID string, stage sdd.Stage) (catalog.StageExecutor, error) {
	value, err := scanStageExecutor(s.db.QueryRowContext(ctx, "SELECT "+stageExecutorColumns+" FROM workspace_stage_executors WHERE workspace_id=? AND stage=?", workspaceID, string(stage)).Scan)
	if err != nil {
		return catalog.StageExecutor{}, fmt.Errorf("get phase executor: %w", err)
	}
	return value, nil
}

// SaveWorkspaceStageExecutor sets the executor of a phase, replacing the one it had.
func (s *Store) SaveWorkspaceStageExecutor(ctx context.Context, value catalog.StageExecutor) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO workspace_stage_executors (workspace_id, stage, backend_id, model_id, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, stage) DO UPDATE SET backend_id=excluded.backend_id, model_id=excluded.model_id, updated_at=excluded.updated_at`,
		value.WorkspaceID, string(value.Stage), value.BackendID, value.ModelID, formatCatalogTime(value.UpdatedAt)); err != nil {
		return fmt.Errorf("save phase executor: %w", err)
	}
	return nil
}

// DeleteWorkspaceStageExecutor returns a phase to the default of Settings. A phase that had none is not an error.
func (s *Store) DeleteWorkspaceStageExecutor(ctx context.Context, workspaceID string, stage sdd.Stage) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM workspace_stage_executors WHERE workspace_id=? AND stage=?", workspaceID, string(stage)); err != nil {
		return fmt.Errorf("delete phase executor: %w", err)
	}
	return nil
}
