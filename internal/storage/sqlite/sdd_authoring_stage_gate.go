package sqlite

import (
	"context"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func (s *Store) ApproveAuthoringStage(ctx context.Context, in catalog.AuthoringStageDecisionRequest) (catalog.AuthoringStageRun, error) {
	return s.decideAuthoringStage(ctx, in, "approve")
}
func (s *Store) SkipAuthoringStage(ctx context.Context, in catalog.AuthoringStageDecisionRequest) (catalog.AuthoringStageRun, error) {
	return s.decideAuthoringStage(ctx, in, "skip")
}
func (s *Store) CancelAuthoringStage(ctx context.Context, in catalog.AuthoringStageDecisionRequest) (catalog.AuthoringStageRun, error) {
	return s.decideAuthoringStage(ctx, in, "cancel")
}

func (s *Store) decideAuthoringStage(ctx context.Context, in catalog.AuthoringStageDecisionRequest, action string) (catalog.AuthoringStageRun, error) {
	empty := catalog.AuthoringStageRun{}
	if !validStageRef(in.Ref) || (action == "skip" && !validStageText(in.Reason)) || (action != "skip" && in.Reason != "") || (action == "cancel" && !safeBrainstormText(in.AttemptID, 128)) || (action != "cancel" && in.AttemptID != "") {
		return empty, sdd.ErrInvalidTransition
	}
	hash := brainstormHash(struct {
		Action  string
		Request catalog.AuthoringStageDecisionRequest
	}{action, in})
	tx, err := s.beginAuthoringStageTx(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	_, data, exists, err := authoringRequestResult(ctx, tx, in.Ref, action, hash)
	if err != nil {
		return empty, err
	}
	if exists {
		run, err := decodeAuthoringStage(data)
		if err != nil {
			return empty, err
		}
		return run, tx.Commit()
	}
	run, p, err := loadAuthoringStage(ctx, tx, in.Ref.PipelineID, in.Ref.Stage)
	if err != nil {
		return empty, err
	}
	if action != "cancel" {
		if err = checkAuthoringStage(run, p, in.Ref); err != nil {
			return empty, err
		}
	}
	if run.CancellationPending && action != "cancel" {
		return empty, sdd.ErrAuthoringCancellationPending
	}
	if action != "cancel" {
		if _, err = loadAuthoringInput(ctx, tx, p, in.Ref.Stage); err != nil {
			return empty, err
		}
	}
	state, status, next := "approved", sdd.Completed, sdd.Plan
	var cancelled catalog.AuthoringStageAttempt
	if in.Ref.Stage == sdd.Plan {
		next = sdd.Code
	}
	switch action {
	case "approve":
		if run.State != "waiting_user" || p.Status[in.Ref.Stage] != sdd.WaitingUser || run.ArtifactVersion < 1 {
			return empty, ErrPipelineConflict
		}
		var artifactStatus string
		if err = tx.QueryRowContext(ctx, `SELECT status FROM pipeline_authoring_artifacts WHERE pipeline_id=? AND stage=? AND version=?`, p.ID, in.Ref.Stage, run.ArtifactVersion).Scan(&artifactStatus); err != nil {
			return empty, err
		}
		if artifactStatus != "waiting_user" {
			return empty, ErrPipelineConflict
		}
	case "skip":
		if run.State != "ready" && run.State != "paused" && run.State != "waiting_user" {
			return empty, sdd.ErrInvalidTransition
		}
		state, status = "skipped", sdd.Skipped
	case "cancel":
		if _, _, cancelled, err = validateAuthoringAttempt(ctx, tx, in.Ref, in.AttemptID, true); err != nil {
			return empty, err
		}
		state, status, next = "paused", sdd.Active, ""
	default:
		return empty, sdd.ErrInvalidTransition
	}
	now := time.Now().UTC()
	if action == "cancel" {
		if err = authoringPipelineSettlementCAS(ctx, tx, p, in.Ref, now); err != nil {
			return empty, err
		}
	} else {
		if err = authoringPipelineCAS(ctx, tx, p, in.Ref, status, next, now); err != nil {
			return empty, err
		}
	}
	if err = ensureAuthoringStage(ctx, tx, run, now); err != nil {
		return empty, err
	}
	if action == "cancel" {
		if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_attempts SET status='interrupted',error_code='cancelled',updated_at=? WHERE id=?`, formatCatalogTime(now), in.AttemptID); err != nil {
			return empty, err
		}
		if err = markAuthoringStop(ctx, tx, cancelled, now); err != nil {
			return empty, err
		}
	}
	if action == "approve" || action == "skip" {
		artifactStatus := "approved"
		if action == "skip" {
			artifactStatus = "bypassed"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_artifacts SET status=?,updated_at=? WHERE pipeline_id=? AND stage=? AND version=? AND status='waiting_user'`, artifactStatus, formatCatalogTime(now), p.ID, in.Ref.Stage, run.ArtifactVersion); err != nil {
			return empty, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_stages SET state=?,revision=revision+1,updated_at=? WHERE pipeline_id=? AND stage=?`, state, formatCatalogTime(now), p.ID, in.Ref.Stage); err != nil {
		return empty, err
	}
	run, err = authoringReceipt(ctx, tx, in.Ref, action, hash, hash, in.AttemptID, "", in.Reason, now)
	if err != nil {
		return empty, err
	}
	return run, tx.Commit()
}
