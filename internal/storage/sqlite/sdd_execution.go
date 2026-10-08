package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func (s *Store) LinkPipelineSession(ctx context.Context, link catalog.PipelineSession) error {
	baselineFiles := link.BaselineGitFiles
	if baselineFiles == nil {
		baselineFiles = []string{}
	}
	encodedBaselineFiles, err := json.Marshal(baselineFiles)
	if err != nil {
		return fmt.Errorf("marshal pipeline Git baseline: %w", err)
	}
	baselineFileHashes := link.BaselineGitFileHashes
	if baselineFileHashes == nil {
		baselineFileHashes = map[string]string{}
	}
	encodedBaselineFileHashes, err := json.Marshal(baselineFileHashes)
	if err != nil {
		return fmt.Errorf("marshal pipeline Git file hashes: %w", err)
	}
	if len(link.ExecutionSnapshot) > 0 && !json.Valid(link.ExecutionSnapshot) {
		return fmt.Errorf("marshal pipeline execution snapshot: invalid JSON")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pipeline session link: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "INSERT INTO pipeline_sessions (id, pipeline_id, session_id, role, baseline_git_hash, baseline_git_files, baseline_git_file_hashes, execution_snapshot, evaluation_criteria, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", link.ID, link.PipelineID, link.SessionID, link.Role, link.BaselineGitHash, string(encodedBaselineFiles), string(encodedBaselineFileHashes), string(link.ExecutionSnapshot), string(link.EvaluationCriteria), formatCatalogTime(link.CreatedAt)); err != nil {
		return fmt.Errorf("link pipeline session: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, link.PipelineID, "pipeline.session.linked", map[string]any{"sessionId": link.SessionID, "role": link.Role}, link.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pipeline session link: %w", err)
	}
	return nil
}

func (s *Store) ListPipelineSessions(ctx context.Context, pipelineID, role string) ([]catalog.PipelineSession, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, pipeline_id, session_id, role, baseline_git_hash, baseline_git_files, baseline_git_file_hashes, execution_snapshot, execution_applied_at, evaluation_criteria, created_at FROM pipeline_sessions WHERE pipeline_id = ? AND role = ? ORDER BY rowid DESC", pipelineID, role)
	if err != nil {
		return nil, fmt.Errorf("list pipeline sessions: %w", err)
	}
	defer rows.Close()
	result := make([]catalog.PipelineSession, 0)
	for rows.Next() {
		var link catalog.PipelineSession
		var baselineFiles, baselineFileHashes, executionSnapshot, appliedAt, evaluationCriteria, created string
		if err := rows.Scan(&link.ID, &link.PipelineID, &link.SessionID, &link.Role, &link.BaselineGitHash, &baselineFiles, &baselineFileHashes, &executionSnapshot, &appliedAt, &evaluationCriteria, &created); err != nil {
			return nil, fmt.Errorf("scan pipeline session: %w", err)
		}
		if err := json.Unmarshal([]byte(baselineFiles), &link.BaselineGitFiles); err != nil {
			return nil, fmt.Errorf("decode pipeline Git baseline: %w", err)
		}
		if err := json.Unmarshal([]byte(baselineFileHashes), &link.BaselineGitFileHashes); err != nil {
			return nil, fmt.Errorf("decode pipeline Git file hashes: %w", err)
		}
		if executionSnapshot != "" {
			if !json.Valid([]byte(executionSnapshot)) {
				return nil, fmt.Errorf("decode pipeline execution snapshot: invalid JSON")
			}
			link.ExecutionSnapshot = json.RawMessage(executionSnapshot)
			link.EvaluationCriteria = json.RawMessage(evaluationCriteria)
		}
		if appliedAt != "" {
			link.ExecutionAppliedAt, err = time.Parse(time.RFC3339Nano, appliedAt)
			if err != nil {
				return nil, fmt.Errorf("parse pipeline patch application timestamp: %w", err)
			}
		}
		link.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse pipeline session timestamp: %w", err)
		}
		result = append(result, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pipeline sessions: %w", err)
	}
	return result, nil
}

func (s *Store) MarkPipelineCodeApplied(ctx context.Context, pipelineID, sessionID string, expectedRevision int64, appliedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pipeline patch readback: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var currentStage, statuses string
	var revision int64
	if err := tx.QueryRowContext(ctx, "SELECT current_stage, stage_status, revision FROM pipeline_runs WHERE id=?", pipelineID).Scan(&currentStage, &statuses, &revision); err != nil {
		return fmt.Errorf("read pipeline before patch readback: %w", err)
	}
	var stageStatus map[string]string
	if err := json.Unmarshal([]byte(statuses), &stageStatus); err != nil {
		return fmt.Errorf("decode pipeline before patch readback: %w", err)
	}
	if revision != expectedRevision || (currentStage != "" && currentStage != "prs") || stageStatus["code"] != "completed" || stageStatus["eval"] != "completed" {
		return ErrPipelineConflict
	}
	var sourceSession string
	if err := tx.QueryRowContext(ctx, "SELECT source_session_id FROM pipeline_artifacts WHERE pipeline_id=? AND stage='code'", pipelineID).Scan(&sourceSession); err != nil {
		return fmt.Errorf("read Code artifact before patch readback: %w", err)
	}
	if sourceSession != sessionID {
		return ErrPipelineConflict
	}
	result, err := tx.ExecContext(ctx, "UPDATE pipeline_sessions SET execution_applied_at=? WHERE pipeline_id=? AND session_id=? AND role='coder' AND execution_applied_at=''", formatCatalogTime(appliedAt), pipelineID, sessionID)
	if err != nil {
		return fmt.Errorf("store pipeline patch readback: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read patch readback update result: %w", err)
	}
	if rows != 1 {
		return ErrPipelineConflict
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.code.applied", map[string]any{"sessionId": sessionID}, appliedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pipeline patch readback: %w", err)
	}
	return nil
}

func (s *Store) GetPipelineIDForSession(ctx context.Context, sessionID string) (string, error) {
	var pipelineID string
	if err := s.db.QueryRowContext(ctx, "SELECT pipeline_id FROM pipeline_sessions WHERE session_id=? ORDER BY rowid DESC LIMIT 1", sessionID).Scan(&pipelineID); err != nil {
		return "", fmt.Errorf("get pipeline for session: %w", err)
	}
	return pipelineID, nil
}
