package sqlite

import (
	"context"
	"database/sql"
	"reflect"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

// A session and its immutable model snapshot become visible in the same commit
// as the attempt link. No caller can replace a link or revive a terminal attempt.
func (s *Store) CreateBrainstormSession(ctx context.Context, ref catalog.BrainstormRequest, expected catalog.BrainstormAttempt, session catalog.SessionRecord) error {
	tx, err := s.beginBrainstormTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	attempt, workspace, err := validateBrainstormSession(ctx, tx, ref, expected.ID, "")
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(attempt, expected) || session.ID == "" || session.WorkspaceID != workspace.ID || session.BackendID != attempt.Selection.BackendID || session.Mode != "sdd_readonly" || session.Status != "ready" {
		return ErrPipelineConflict
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id=?`, session.ID).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return ErrPipelineConflict
	}
	selection := attempt.Selection
	selection.SessionID, selection.WorkspacePath = session.ID, workspace.Path
	if err := createSessionWithSnapshots(ctx, tx, session, nil, "", nil, "agent_session", &selection); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_attempts SET session_id=? WHERE id=? AND status='running' AND session_id=''`, session.ID, attempt.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// ValidateBrainstormSession reads all admission fences in one transaction. It
// does not increment aggregate revisions or reserve another generation budget.
func (s *Store) ValidateBrainstormSession(ctx context.Context, ref catalog.BrainstormRequest, attemptID, sessionID string) (catalog.BrainstormAttempt, error) {
	tx, err := s.beginBrainstormTx(ctx)
	if err != nil {
		return catalog.BrainstormAttempt{}, err
	}
	defer tx.Rollback()
	attempt, _, err := validateBrainstormSession(ctx, tx, ref, attemptID, sessionID)
	if err != nil {
		return catalog.BrainstormAttempt{}, err
	}
	return attempt, tx.Commit()
}

func validateBrainstormSession(ctx context.Context, tx *sql.Tx, ref catalog.BrainstormRequest, attemptID, sessionID string) (catalog.BrainstormAttempt, catalog.Workspace, error) {
	run, err := loadBrainstorm(ctx, tx, ref.RunID)
	if err != nil {
		return catalog.BrainstormAttempt{}, catalog.Workspace{}, err
	}
	if err := checkBrainstormRun(run, ref); err != nil {
		return catalog.BrainstormAttempt{}, catalog.Workspace{}, err
	}
	attempt, err := scanBrainstormAttempt(tx.QueryRowContext(ctx, `SELECT `+brainstormAttemptColumns+` FROM pipeline_brainstorm_attempts WHERE id=? AND run_id=?`, attemptID, ref.RunID))
	if err != nil {
		return attempt, catalog.Workspace{}, err
	}
	if attempt.Status != "running" || attempt.SessionID != sessionID || attempt.DiscoveryVersion != ref.DiscoveryVersion || run.State != "running_"+attempt.Kind || time.Since(attempt.CreatedAt) >= brainstormAttemptTimeout || !validBrainstormSelection(attempt.Selection) {
		return attempt, catalog.Workspace{}, ErrPipelineConflict
	}
	var workspace catalog.Workspace
	err = tx.QueryRowContext(ctx, `SELECT w.id,w.path FROM pipeline_runs p JOIN workspaces w ON w.id=p.workspace_id WHERE p.id=? AND p.revision=? AND p.kind='ai_authoring' AND p.current_stage='discovery' AND p.discovery_frozen_version=0 AND (SELECT MAX(version) FROM pipeline_artifacts WHERE pipeline_id=p.id AND stage='discovery')=?`, run.PipelineID, ref.PipelineRevision, ref.DiscoveryVersion).Scan(&workspace.ID, &workspace.Path)
	if err == sql.ErrNoRows {
		err = ErrPipelineConflict
	}
	return attempt, workspace, err
}
