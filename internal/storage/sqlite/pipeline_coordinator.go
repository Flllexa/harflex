package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/persioflexa/harflex/internal/catalog"
)

// SetPipelineCoordinator records the chat that coordinates a work. An empty session id clears it.
func (s *Store) SetPipelineCoordinator(ctx context.Context, pipelineID, sessionID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE pipeline_runs SET coordinator_session_id = ? WHERE id = ?`, sessionID, pipelineID)
	if err != nil {
		return fmt.Errorf("set pipeline coordinator: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetPipelineIDForCoordinator returns the work a chat coordinates.
func (s *Store) GetPipelineIDForCoordinator(ctx context.Context, sessionID string) (string, error) {
	var pipelineID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM pipeline_runs WHERE coordinator_session_id = ? AND coordinator_session_id <> '' ORDER BY updated_at DESC LIMIT 1`, sessionID).Scan(&pipelineID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", sql.ErrNoRows
		}
		return "", fmt.Errorf("get pipeline for coordinator: %w", err)
	}
	return pipelineID, nil
}

// ListWorkCoordinators returns the works of a project that have a chat coordinating them.
func (s *Store) ListWorkCoordinators(ctx context.Context, workspaceID string) ([]catalog.WorkCoordinator, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT coordinator_session_id, id, title, current_stage, stage_status, coordinator_previous, updated_at FROM pipeline_runs
		WHERE workspace_id = ? AND coordinator_session_id <> '' ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list work coordinators: %w", err)
	}
	defer rows.Close()
	var out []catalog.WorkCoordinator
	for rows.Next() {
		var item catalog.WorkCoordinator
		if err := rows.Scan(&item.SessionID, &item.PipelineID, &item.Title, &item.CurrentStage, &item.StageStatus, &item.Previous, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan work coordinator: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ReplacePipelineCoordinator makes next the chat of a work in place of previous, which stays in the work's history. It
// changes nothing, and reports sql.ErrNoRows, when previous is no longer the work's chat.
func (s *Store) ReplacePipelineCoordinator(ctx context.Context, pipelineID, previous, next string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE pipeline_runs SET coordinator_session_id = ?, coordinator_previous = json_insert(coordinator_previous, '$[#]', ?)
		WHERE id = ? AND coordinator_session_id = ?`, next, previous, pipelineID, previous)
	if err != nil {
		return fmt.Errorf("replace pipeline coordinator: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}
