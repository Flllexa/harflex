package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func (s *Store) ListPendingAuthoringStops(ctx context.Context) ([]catalog.AuthoringStageStop, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pipeline_id,stage,attempt_id,session_id FROM pipeline_authoring_stops WHERE state='pending' ORDER BY created_at,attempt_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []catalog.AuthoringStageStop
	for rows.Next() {
		var item catalog.AuthoringStageStop
		if err := rows.Scan(&item.PipelineID, &item.Stage, &item.AttemptID, &item.SessionID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func markAuthoringStop(ctx context.Context, tx *sql.Tx, a catalog.AuthoringStageAttempt, now time.Time) error {
	// No linked session means no authorized provider could have started. The
	// attempt fence prevents any future link, so there is no runner to join.
	if a.SessionID == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO pipeline_authoring_stops(attempt_id,pipeline_id,stage,session_id,state,created_at) VALUES (?,?,?,?,'pending',?) ON CONFLICT(attempt_id) DO NOTHING`, a.ID, a.PipelineID, a.Stage, a.SessionID, formatCatalogTime(now))
	return err
}

func loadAuthoringStopState(ctx context.Context, tx *sql.Tx, run *catalog.AuthoringStageRun) error {
	rows, err := tx.QueryContext(ctx, `SELECT attempt_id,state FROM pipeline_authoring_stops WHERE pipeline_id=? AND stage=?`, run.PipelineID, run.Stage)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return err
		}
		if state == "pending" {
			run.State = "cancellation_pending"
			run.CancellationPending = true
			run.CancellationAttemptID = id
		}
		for i := range run.Attempts {
			if run.Attempts[i].ID == id {
				run.Attempts[i].CancellationState = state
			}
		}
	}
	return rows.Err()
}

// ConfirmAuthoringStageStop is internal runtime settlement, not a human gate.
// The application must establish the supplied proof before invoking it.
func (s *Store) ConfirmAuthoringStageStop(ctx context.Context, pipelineID string, stage sdd.Stage, attemptID, proof string) (catalog.AuthoringStageRun, error) {
	empty := catalog.AuthoringStageRun{}
	if !validAuthoringStage(stage) || !safeBrainstormText(attemptID, 128) {
		return empty, sdd.ErrInvalidTransition
	}
	switch proof {
	case "joined", "terminal_journal", "owner_exited":
	default:
		return empty, sdd.ErrInvalidTransition
	}
	tx, err := s.beginAuthoringStageTx(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	var state, status string
	err = tx.QueryRowContext(ctx, `SELECT x.state,a.status FROM pipeline_authoring_stops x JOIN pipeline_authoring_attempts a ON a.id=x.attempt_id WHERE x.attempt_id=? AND x.pipeline_id=? AND x.stage=?`, attemptID, pipelineID, stage).Scan(&state, &status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	if err == nil && state == "pending" {
		if status == "running" {
			return empty, ErrPipelineConflict
		}
		now := time.Now().UTC()
		if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_stops SET state='confirmed',proof=?,confirmed_at=? WHERE attempt_id=? AND state='pending'`, proof, formatCatalogTime(now), attemptID); err != nil {
			return empty, err
		}
		if err = appendPipelineEvent(ctx, tx, pipelineID, "pipeline.authoring.cancellation_confirmed", map[string]any{"stage": stage, "attemptId": attemptID, "proof": proof}, now); err != nil {
			return empty, err
		}
	}
	run, err := loadAuthoringStageSnapshot(ctx, tx, pipelineID, stage)
	if err != nil {
		return empty, err
	}
	return run, tx.Commit()
}
