package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func authoringContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateAuthoringAttempt(ctx context.Context, tx *sql.Tx, ref catalog.AuthoringStageRequest, attemptID string, settlement ...bool) (catalog.AuthoringStageRun, catalog.PipelineRun, catalog.AuthoringStageAttempt, error) {
	run, p, err := loadAuthoringStage(ctx, tx, ref.PipelineID, ref.Stage)
	if err != nil {
		return run, p, catalog.AuthoringStageAttempt{}, err
	}
	designOwned, err := pipelineDesignOwnsAuthoring(ctx, tx, ref.PipelineID)
	if err != nil {
		return run, p, catalog.AuthoringStageAttempt{}, err
	}
	allowSettlement := len(settlement) == 1 && settlement[0]
	if designOwned {
		if !allowSettlement || run.Revision != ref.StageRevision || run.ArtifactVersion != ref.ArtifactVersion {
			return run, p, catalog.AuthoringStageAttempt{}, ErrPipelineConflict
		}
	} else if err = checkAuthoringStage(run, p, ref); err != nil {
		return run, p, catalog.AuthoringStageAttempt{}, err
	}
	a, err := scanAuthoringAttempt(tx.QueryRowContext(ctx, `SELECT `+authoringAttemptColumns+` FROM pipeline_authoring_attempts WHERE id=? AND pipeline_id=? AND stage=?`, attemptID, ref.PipelineID, ref.Stage))
	if err != nil {
		return run, p, a, err
	}
	if run.State != "running" || (!designOwned && p.Status[ref.Stage] != sdd.Active) || a.Status != "running" || a.ArtifactVersion != run.ArtifactVersion+1 || a.Input.DiscoveryVersion != ref.DiscoveryVersion {
		return run, p, a, ErrPipelineConflict
	}
	return run, p, a, nil
}

func (s *Store) CreateAuthoringStageSession(ctx context.Context, ref catalog.AuthoringStageRequest, expected catalog.AuthoringStageAttempt, session catalog.SessionRecord) error {
	if !validStageRef(ref) || !safeBrainstormText(session.ID, 128) {
		return sdd.ErrInvalidTransition
	}
	tx, err := s.beginAuthoringStageTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, p, a, err := validateAuthoringAttempt(ctx, tx, ref, expected.ID)
	if err != nil {
		return err
	}
	backendRevision := a.Selection.CatalogRevision
	if a.Selection.BackendID == "codex" && a.Selection.Source == "codex_app_server" {
		backendRevision = "cli:codex"
	}
	if !reflect.DeepEqual(a, expected) || a.SessionID != "" || time.Since(a.CreatedAt) >= sdd.AuthoringAttemptTimeout || session.WorkspaceID != p.WorkspaceID || session.Mode != "sdd_readonly" || session.Status != "ready" || session.BackendID != a.Selection.BackendID || session.BackendRevision != backendRevision {
		return ErrPipelineConflict
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id=?`, session.ID).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return ErrPipelineConflict
	}
	choice := a.Selection
	choice.SessionID = session.ID
	if err = tx.QueryRowContext(ctx, `SELECT path FROM workspaces WHERE id=?`, p.WorkspaceID).Scan(&choice.WorkspacePath); err != nil {
		return err
	}
	if err = createSessionWithSnapshots(ctx, tx, session, nil, "", nil, "agent_session", &choice); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_attempts SET session_id=? WHERE id=? AND status='running' AND session_id=''`, session.ID, a.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ValidateAuthoringStageSession(ctx context.Context, ref catalog.AuthoringStageRequest, attemptID, sessionID string) (catalog.AuthoringStageAttempt, error) {
	if !validStageRef(ref) || !safeBrainstormText(attemptID, 128) || !safeBrainstormText(sessionID, 128) {
		return catalog.AuthoringStageAttempt{}, sdd.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return catalog.AuthoringStageAttempt{}, err
	}
	defer tx.Rollback()
	_, _, a, err := validateAuthoringAttempt(ctx, tx, ref, attemptID)
	if err != nil {
		return a, err
	}
	if a.SessionID != sessionID || time.Since(a.CreatedAt) >= sdd.AuthoringAttemptTimeout {
		return a, ErrPipelineConflict
	}
	return a, tx.Commit()
}

func validAuthoringUsage(u *catalog.BrainstormUsage) bool {
	return u == nil || (u.InputTokens >= 0 && u.OutputTokens >= 0 && (u.CostUSD == nil || (*u.CostUSD >= 0 && !math.IsNaN(*u.CostUSD) && !math.IsInf(*u.CostUSD, 0))))
}

func (s *Store) CompleteAuthoringStage(ctx context.Context, in catalog.CompleteAuthoringStageRequest) (catalog.AuthoringStageRun, error) {
	empty := catalog.AuthoringStageRun{}
	if !validStageRef(in.Ref) || !safeBrainstormText(in.AttemptID, 128) || !safeBrainstormText(in.SessionID, 128) || !validAuthoringUsage(in.Usage) || len(in.Content) > sdd.MaxAuthoringDocumentBytes {
		return empty, sdd.ErrInvalidTransition
	}
	hash := brainstormHash(struct {
		SessionID, ContentHash string
		Usage                  *catalog.BrainstormUsage
	}{in.SessionID, authoringContentHash(in.Content), in.Usage})
	tx, err := s.beginAuthoringStageTx(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	a, err := scanAuthoringAttempt(tx.QueryRowContext(ctx, `SELECT `+authoringAttemptColumns+` FROM pipeline_authoring_attempts WHERE id=? AND pipeline_id=? AND stage=?`, in.AttemptID, in.Ref.PipelineID, in.Ref.Stage))
	if err != nil {
		return empty, err
	}
	if a.ResultHash == hash && (a.Status == "completed" || (a.Status == "failed" && a.ErrorCode == "budget_overrun")) {
		run, err := loadAuthoringStageSnapshot(ctx, tx, in.Ref.PipelineID, in.Ref.Stage)
		if err != nil {
			return empty, err
		}
		if err = tx.Commit(); err != nil {
			return empty, err
		}
		if a.Status == "failed" {
			return run, sdd.ErrAuthoringBudgetExceeded
		}
		return run, nil
	}
	budgetSettlement := in.Usage != nil && (in.Usage.InputTokens > a.ReservedInputTokens || in.Usage.OutputTokens > a.ReservedOutputTokens)
	run, p, a, err := validateAuthoringAttempt(ctx, tx, in.Ref, in.AttemptID, budgetSettlement)
	if err != nil {
		return empty, err
	}
	if a.SessionID == "" || a.SessionID != in.SessionID || time.Since(a.CreatedAt) >= sdd.AuthoringAttemptTimeout {
		return empty, ErrPipelineConflict
	}
	now := time.Now().UTC()
	var usage any
	if in.Usage != nil {
		data, _ := json.Marshal(in.Usage)
		usage = data
	}
	if in.Usage != nil && (in.Usage.InputTokens > a.ReservedInputTokens || in.Usage.OutputTokens > a.ReservedOutputTokens) {
		if err = authoringPipelineSettlementCAS(ctx, tx, p, in.Ref, now); err != nil {
			return empty, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_attempts SET status='failed',error_code='budget_overrun',actual_usage=?,result_hash=?,updated_at=? WHERE id=?`, usage, hash, formatCatalogTime(now), a.ID); err != nil {
			return empty, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_stages SET state='paused',revision=revision+1,input_budget_remaining=MAX(0,input_budget_remaining-?),output_budget_remaining=MAX(0,output_budget_remaining-?),updated_at=? WHERE pipeline_id=? AND stage=?`, max(int64(0), in.Usage.InputTokens-a.ReservedInputTokens), max(int64(0), in.Usage.OutputTokens-a.ReservedOutputTokens), formatCatalogTime(now), p.ID, in.Ref.Stage); err != nil {
			return empty, err
		}
		if err = appendPipelineEvent(ctx, tx, p.ID, "pipeline.authoring.failed", map[string]any{"stage": in.Ref.Stage, "attemptId": a.ID, "errorCode": "budget_overrun"}, now); err != nil {
			return empty, err
		}
		run, err = loadAuthoringStageSnapshot(ctx, tx, p.ID, in.Ref.Stage)
		if err != nil {
			return empty, err
		}
		if err = tx.Commit(); err != nil {
			return empty, err
		}
		return run, sdd.ErrAuthoringBudgetExceeded
	}
	content, err := sdd.CanonicalAuthoringDocument(in.Ref.Stage, in.Content)
	if err != nil {
		return empty, err
	}
	if err = authoringPipelineCAS(ctx, tx, p, in.Ref, sdd.WaitingUser, "", now); err != nil {
		return empty, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_authoring_artifacts(pipeline_id,stage,version,content,content_hash,author,attempt_id,source_session_id,status,created_at,updated_at) VALUES (?,?,?,?,?,'ai',?,?,'waiting_user',?,?)`, p.ID, in.Ref.Stage, a.ArtifactVersion, []byte(content), authoringContentHash(content), a.ID, a.SessionID, formatCatalogTime(now), formatCatalogTime(now))
	if err != nil {
		return empty, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_artifacts(pipeline_id,stage,version,content,author,source_session_id,updated_at) VALUES (?,?,?,?,'ai',?,?) ON CONFLICT(pipeline_id,stage) DO UPDATE SET version=excluded.version,content=excluded.content,author=excluded.author,source_session_id=excluded.source_session_id,updated_at=excluded.updated_at`, p.ID, in.Ref.Stage, a.ArtifactVersion, string(content), a.SessionID, formatCatalogTime(now))
	if err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_attempts SET status='completed',actual_usage=?,result_hash=?,updated_at=? WHERE id=?`, usage, hash, formatCatalogTime(now), a.ID); err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_stages SET state='waiting_user',artifact_version=?,revision=revision+1,updated_at=? WHERE pipeline_id=? AND stage=?`, a.ArtifactVersion, formatCatalogTime(now), p.ID, in.Ref.Stage); err != nil {
		return empty, err
	}
	if err = appendPipelineEvent(ctx, tx, p.ID, "pipeline.authoring.drafted", map[string]any{"stage": in.Ref.Stage, "version": a.ArtifactVersion, "attemptId": a.ID, "sessionId": a.SessionID}, now); err != nil {
		return empty, err
	}
	run, err = loadAuthoringStageSnapshot(ctx, tx, p.ID, in.Ref.Stage)
	if err != nil {
		return empty, err
	}
	return run, tx.Commit()
}

func (s *Store) FailAuthoringStage(ctx context.Context, in catalog.FailAuthoringStageRequest) (catalog.AuthoringStageRun, error) {
	empty := catalog.AuthoringStageRun{}
	if !validStageRef(in.Ref) || !safeBrainstormText(in.AttemptID, 128) {
		return empty, sdd.ErrInvalidTransition
	}
	switch in.ErrorCode {
	case "provider_failed", "invalid_output", "output_overflow", "timeout", "cancelled":
	default:
		return empty, sdd.ErrInvalidTransition
	}
	tx, err := s.beginAuthoringStageTx(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	a, err := scanAuthoringAttempt(tx.QueryRowContext(ctx, `SELECT `+authoringAttemptColumns+` FROM pipeline_authoring_attempts WHERE id=? AND pipeline_id=? AND stage=?`, in.AttemptID, in.Ref.PipelineID, in.Ref.Stage))
	if err != nil {
		return empty, err
	}
	if a.Status == "failed" && a.ErrorCode == in.ErrorCode {
		run, err := loadAuthoringStageSnapshot(ctx, tx, in.Ref.PipelineID, in.Ref.Stage)
		if err != nil {
			return empty, err
		}
		return run, tx.Commit()
	}
	_, p, _, err := validateAuthoringAttempt(ctx, tx, in.Ref, in.AttemptID, true)
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC()
	if err = authoringPipelineSettlementCAS(ctx, tx, p, in.Ref, now); err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_attempts SET status='failed',error_code=?,updated_at=? WHERE id=?`, in.ErrorCode, formatCatalogTime(now), a.ID); err != nil {
		return empty, err
	}
	if err = markAuthoringStop(ctx, tx, a, now); err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_stages SET state='paused',revision=revision+1,updated_at=? WHERE pipeline_id=? AND stage=?`, formatCatalogTime(now), p.ID, in.Ref.Stage); err != nil {
		return empty, err
	}
	if err = appendPipelineEvent(ctx, tx, p.ID, "pipeline.authoring.failed", map[string]any{"stage": in.Ref.Stage, "attemptId": a.ID, "errorCode": in.ErrorCode}, now); err != nil {
		return empty, err
	}
	run, err := loadAuthoringStageSnapshot(ctx, tx, p.ID, in.Ref.Stage)
	if err != nil {
		return empty, err
	}
	return run, tx.Commit()
}

func (s *Store) InterruptRunningAuthoringStages(ctx context.Context) error {
	tx, err := s.beginAuthoringStageTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT pipeline_id,stage FROM pipeline_authoring_stages WHERE state='running'`)
	if err != nil {
		return err
	}
	var refs []catalog.AuthoringStageRequest
	for rows.Next() {
		var ref catalog.AuthoringStageRequest
		if err = rows.Scan(&ref.PipelineID, &ref.Stage); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, ref)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, ref := range refs {
		run, p, err := loadAuthoringStage(ctx, tx, ref.PipelineID, ref.Stage)
		if err != nil {
			return err
		}
		ref.PipelineRevision = p.Revision
		ref.DiscoveryVersion = p.DiscoveryFrozenVersion
		attempt, err := scanAuthoringAttempt(tx.QueryRowContext(ctx, `SELECT `+authoringAttemptColumns+` FROM pipeline_authoring_attempts WHERE pipeline_id=? AND stage=? AND status='running'`, ref.PipelineID, ref.Stage))
		if err != nil {
			return err
		}
		if err = authoringPipelineSettlementCAS(ctx, tx, p, ref, now); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_attempts SET status='interrupted',error_code='interrupted',updated_at=? WHERE pipeline_id=? AND stage=? AND status='running'`, formatCatalogTime(now), ref.PipelineID, ref.Stage); err != nil {
			return err
		}
		if err = markAuthoringStop(ctx, tx, attempt, now); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_stages SET state='paused',revision=revision+1,updated_at=? WHERE pipeline_id=? AND stage=?`, formatCatalogTime(now), ref.PipelineID, ref.Stage); err != nil {
			return err
		}
		if err = appendPipelineEvent(ctx, tx, ref.PipelineID, "pipeline.authoring.interrupted", map[string]any{"stage": ref.Stage, "stageRevision": run.Revision + 1}, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
