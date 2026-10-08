package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/sdd"
)

const authoringCodeDecisionColumns = `pipeline_id,request_id,run_id,pipeline_revision,action,patch_hash,baseline_hash,source_manifest_hash,result_manifest_hash,feedback,intent_hash,actor,result_pipeline_revision,created_at`

func scanAuthoringCodeDecision(row rowScanner) (catalog.AuthoringCodeDecision, error) {
	var decision catalog.AuthoringCodeDecision
	var createdAt string
	err := row.Scan(
		&decision.PipelineID, &decision.RequestID, &decision.RunID, &decision.PipelineRevision,
		&decision.Action, &decision.PatchHash, &decision.BaselineHash, &decision.SourceManifestHash,
		&decision.ResultManifestHash, &decision.Feedback, &decision.IntentHash, &decision.Actor,
		&decision.ResultPipelineRevision, &createdAt,
	)
	if err != nil {
		return decision, err
	}
	decision.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return catalog.AuthoringCodeDecision{}, fmt.Errorf("parse authoring Code decision time: %w", err)
	}
	return decision, nil
}

func validAuthoringCodeDecisionRequest(in catalog.AuthoringCodeDecisionRequest) bool {
	if !safeBrainstormText(in.PipelineID, 128) || in.PipelineRevision < 1 || !safeBrainstormText(in.RunID, 128) ||
		!brainstormRequestID.MatchString(in.RequestID) || !brainstormCredentialFingerprint.MatchString(in.PatchHash) ||
		!brainstormCredentialFingerprint.MatchString(in.BaselineHash) || !brainstormCredentialFingerprint.MatchString(in.SourceManifestHash) ||
		!brainstormCredentialFingerprint.MatchString(in.ResultManifestHash) || !brainstormCredentialFingerprint.MatchString(in.IntentHash) {
		return false
	}
	switch in.Action {
	case "approve":
		return in.Feedback == ""
	case "request_revision":
		return strings.TrimSpace(in.Feedback) != "" && len(in.Feedback) <= 16*1024 && utf8.ValidString(in.Feedback) && !strings.ContainsRune(in.Feedback, 0)
	default:
		return false
	}
}

func (s *Store) GetAuthoringCodeDecision(ctx context.Context, pipelineID, requestID string) (catalog.AuthoringCodeDecision, error) {
	if !safeBrainstormText(pipelineID, 128) || !brainstormRequestID.MatchString(requestID) {
		return catalog.AuthoringCodeDecision{}, sdd.ErrInvalidTransition
	}
	return scanAuthoringCodeDecision(s.db.QueryRowContext(ctx, `SELECT `+authoringCodeDecisionColumns+` FROM pipeline_authoring_code_decisions WHERE pipeline_id=? AND request_id=?`, pipelineID, requestID))
}

func requireAuthoringCodeRevisionRequest(ctx context.Context, tx *sql.Tx, pipelineID string) error {
	var runID, status string
	err := tx.QueryRowContext(ctx, `SELECT id,status FROM pipeline_authoring_code_runs WHERE pipeline_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, pipelineID).Scan(&runID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read latest authoring Code run: %w", err)
	}
	switch status {
	case "running", "cancelling":
		return ErrPipelineConflict
	case "completed":
	default:
		return nil
	}
	var action string
	err = tx.QueryRowContext(ctx, `SELECT action FROM pipeline_authoring_code_decisions WHERE run_id=?`, runID).Scan(&action)
	if errors.Is(err, sql.ErrNoRows) || err == nil && action != "request_revision" {
		return ErrPipelineConflict
	}
	if err != nil {
		return fmt.Errorf("read latest authoring Code decision: %w", err)
	}
	return nil
}

func requireLatestAuthoringCodeRun(ctx context.Context, tx *sql.Tx, pipelineID, runID string) error {
	var latestRunID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM pipeline_authoring_code_runs WHERE pipeline_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, pipelineID).Scan(&latestRunID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && latestRunID != runID {
		return ErrPipelineConflict
	}
	if err != nil {
		return fmt.Errorf("read latest authoring Code run for decision: %w", err)
	}
	return nil
}

func (s *Store) DecideAuthoringCode(ctx context.Context, in catalog.AuthoringCodeDecisionRequest) (catalog.AuthoringCodeDecision, error) {
	var empty catalog.AuthoringCodeDecision
	if !validAuthoringCodeDecisionRequest(in) {
		return empty, sdd.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, fmt.Errorf("begin authoring Code decision: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_authoring_code_decisions SET actor=actor WHERE 0`); err != nil {
		return empty, fmt.Errorf("serialize authoring Code decision: %w", err)
	}
	prior, err := scanAuthoringCodeDecision(tx.QueryRowContext(ctx, `SELECT `+authoringCodeDecisionColumns+` FROM pipeline_authoring_code_decisions WHERE pipeline_id=? AND request_id=?`, in.PipelineID, in.RequestID))
	if err == nil {
		if prior.IntentHash != in.IntentHash {
			return empty, ErrPipelineConflict
		}
		return prior, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, fmt.Errorf("read authoring Code decision idempotency key: %w", err)
	}
	var existingRunID string
	err = tx.QueryRowContext(ctx, `SELECT run_id FROM pipeline_authoring_code_decisions WHERE run_id=?`, in.RunID).Scan(&existingRunID)
	if err == nil {
		return empty, ErrPipelineConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, fmt.Errorf("read authoring Code run decision: %w", err)
	}
	pipeline, err := scanPipeline(tx.QueryRowContext(ctx, `SELECT `+pipelineColumns+` FROM pipeline_runs WHERE id=?`, in.PipelineID))
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrPipelineConflict
	}
	if err != nil {
		return empty, fmt.Errorf("read authoring Code decision pipeline: %w", err)
	}
	if pipeline.Kind != "ai_authoring" || pipeline.Current != sdd.Code || pipeline.Status[sdd.Code] != sdd.Active || pipeline.Revision != in.PipelineRevision {
		return empty, ErrPipelineConflict
	}
	if err := requireLatestAuthoringCodeRun(ctx, tx, in.PipelineID, in.RunID); err != nil {
		return empty, err
	}
	run, err := scanAuthoringCodeRun(tx.QueryRowContext(ctx, `SELECT `+authoringCodeRunColumns+` FROM pipeline_authoring_code_runs WHERE id=?`, in.RunID))
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrPipelineConflict
	}
	if err != nil {
		return empty, fmt.Errorf("read authoring Code decision run: %w", err)
	}
	if run.PipelineID != in.PipelineID || run.Status != "completed" || run.PipelineRevision != in.PipelineRevision ||
		run.PatchHash != in.PatchHash || run.ManifestHash != in.BaselineHash || run.SourceManifest == nil ||
		run.ResultManifest == nil || run.SourceManifest.Hash != in.SourceManifestHash || run.ResultManifest.Hash != in.ResultManifestHash ||
		len(run.PatchJSON) == 0 {
		return empty, ErrPipelineConflict
	}
	resultRevision := pipeline.Revision + 1
	now := time.Now().UTC()
	decision := catalog.AuthoringCodeDecision{
		AuthoringCodeDecisionRequest: in, Actor: "local_user",
		ResultPipelineRevision: resultRevision, CreatedAt: now,
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pipeline_authoring_code_decisions(
	 pipeline_id,request_id,run_id,pipeline_revision,action,patch_hash,baseline_hash,source_manifest_hash,result_manifest_hash,
	 feedback,intent_hash,actor,result_pipeline_revision,created_at)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		decision.PipelineID, decision.RequestID, decision.RunID, decision.PipelineRevision, decision.Action,
		decision.PatchHash, decision.BaselineHash, decision.SourceManifestHash, decision.ResultManifestHash,
		decision.Feedback, decision.IntentHash, decision.Actor, decision.ResultPipelineRevision, formatCatalogTime(now)); err != nil {
		return empty, fmt.Errorf("insert authoring Code decision: %w", err)
	}
	nextStage := sdd.Code
	statuses := pipeline.Status
	if in.Action == "approve" {
		nextStage = sdd.Eval
		statuses[sdd.Code] = sdd.Completed
		statuses[sdd.Eval] = sdd.Active
	}
	statusJSON, err := json.Marshal(statuses)
	if err != nil {
		return empty, fmt.Errorf("encode authoring Code decision pipeline state: %w", err)
	}
	updated, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET current_stage=?,stage_status=?,revision=revision+1,updated_at=?
	 WHERE id=? AND kind='ai_authoring' AND current_stage='code' AND revision=?`,
		nextStage, statusJSON, formatCatalogTime(now), in.PipelineID, in.PipelineRevision)
	if err != nil {
		return empty, fmt.Errorf("advance authoring Code decision pipeline: %w", err)
	}
	changed, err := updated.RowsAffected()
	if err != nil {
		return empty, fmt.Errorf("read authoring Code pipeline CAS result: %w", err)
	}
	if changed != 1 {
		return empty, ErrPipelineConflict
	}
	action, eventType := "request_code_revision", "pipeline.authoring.code_revision_requested"
	if in.Action == "approve" {
		action, eventType = "approve_code", "pipeline.authoring.code_approved"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pipeline_transitions(id,pipeline_id,stage,action,actor,reason,created_at) VALUES(?,?,?,?,?,?,?)`,
		id.New(), in.PipelineID, sdd.Code, action, "local_user", "verified_patch_decision", formatCatalogTime(now)); err != nil {
		return empty, fmt.Errorf("record authoring Code pipeline transition: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, in.PipelineID, eventType, map[string]any{
		"stage": sdd.Code, "nextStage": nextStage, "runId": in.RunID, "action": in.Action,
		"patchHash": in.PatchHash, "baselineHash": in.BaselineHash,
		"sourceManifestHash": in.SourceManifestHash, "resultManifestHash": in.ResultManifestHash,
		"pipelineRevision": in.PipelineRevision, "resultPipelineRevision": resultRevision,
	}, now); err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, fmt.Errorf("commit authoring Code decision: %w", err)
	}
	return decision, nil
}
