package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/sdd"
)

func validAuthoringStage(stage sdd.Stage) bool { return stage == sdd.Spec || stage == sdd.Plan }
func validStageRef(ref catalog.AuthoringStageRequest) bool {
	return safeBrainstormText(ref.PipelineID, 128) && validAuthoringStage(ref.Stage) && brainstormRequestID.MatchString(ref.RequestID) && ref.PipelineRevision > 0 && ref.StageRevision >= 0 && ref.DiscoveryVersion > 0 && ref.ArtifactVersion >= 0 && ref.ArtifactVersion <= sdd.MaxAuthoringDrafts
}
func validStageText(v string) bool {
	return strings.TrimSpace(v) != "" && len(v) <= 16*1024 && utf8.ValidString(v) && !strings.ContainsRune(v, 0)
}

func (s *Store) beginAuthoringStageTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_stages SET revision=revision WHERE 0`); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

const authoringAttemptColumns = `id,pipeline_id,stage,request_id,payload_hash,artifact_version,status,session_id,error_code,input_snapshot,selection_snapshot,reserved_input_tokens,reserved_output_tokens,actual_usage,result_hash,created_at,updated_at,model_mode,effort_mode,preference_source,preference_revision`

func scanAuthoringAttempt(row rowScanner) (catalog.AuthoringStageAttempt, error) {
	var a catalog.AuthoringStageAttempt
	var input, selection, usage []byte
	var created, updated string
	if err := row.Scan(&a.ID, &a.PipelineID, &a.Stage, &a.RequestID, &a.PayloadHash, &a.ArtifactVersion, &a.Status, &a.SessionID, &a.ErrorCode, &input, &selection, &a.ReservedInputTokens, &a.ReservedOutputTokens, &usage, &a.ResultHash, &created, &updated, &a.ModelMode, &a.EffortMode, &a.PreferenceSource, &a.PreferenceRevision); err != nil {
		return a, err
	}
	if err := json.Unmarshal(input, &a.Input); err != nil {
		return a, err
	}
	var err error
	if a.Selection, err = decodeBrainstormSelection(selection); err != nil {
		return a, err
	}
	if len(usage) > 0 {
		if err = json.Unmarshal(usage, &a.Usage); err != nil {
			return a, err
		}
	}
	if a.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return a, err
	}
	a.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return a, err
}

func loadAuthoringStage(ctx context.Context, tx *sql.Tx, pipelineID string, stage sdd.Stage) (catalog.AuthoringStageRun, catalog.PipelineRun, error) {
	var run catalog.AuthoringStageRun
	p, err := scanPipeline(tx.QueryRowContext(ctx, "SELECT "+pipelineColumns+" FROM pipeline_runs WHERE id = ?", pipelineID))
	if err != nil {
		return run, p, err
	}
	if p.Kind != "ai_authoring" || !validAuthoringStage(stage) {
		return run, p, sdd.ErrInvalidTransition
	}
	run = catalog.AuthoringStageRun{PipelineID: pipelineID, Stage: stage, PipelineRevision: p.Revision, DiscoveryVersion: p.DiscoveryFrozenVersion, State: "pending", InputBudgetRemaining: sdd.AuthoringInputBudget, OutputBudgetRemaining: sdd.AuthoringOutputBudget}
	if p.Current == stage && p.Status[stage] == sdd.Active && p.DiscoveryFrozenVersion > 0 {
		run.State = "ready"
	}
	var created, updated string
	err = tx.QueryRowContext(ctx, `SELECT revision,state,artifact_version,attempt_count,input_budget_remaining,output_budget_remaining,created_at,updated_at FROM pipeline_authoring_stages WHERE pipeline_id=? AND stage=?`, pipelineID, stage).Scan(&run.Revision, &run.State, &run.ArtifactVersion, &run.AttemptCount, &run.InputBudgetRemaining, &run.OutputBudgetRemaining, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return run, p, nil
	}
	if err != nil {
		return run, p, err
	}
	if run.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return run, p, err
	}
	run.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err == nil {
		err = loadAuthoringStopState(ctx, tx, &run)
	}
	return run, p, err
}

func loadAuthoringStageSnapshot(ctx context.Context, tx *sql.Tx, pipelineID string, stage sdd.Stage) (catalog.AuthoringStageRun, error) {
	run, _, err := loadAuthoringStage(ctx, tx, pipelineID, stage)
	if err != nil {
		return run, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+authoringAttemptColumns+` FROM pipeline_authoring_attempts WHERE pipeline_id=? AND stage=? ORDER BY created_at,id`, pipelineID, stage)
	if err != nil {
		return run, err
	}
	for rows.Next() {
		a, err := scanAuthoringAttempt(rows)
		if err != nil {
			rows.Close()
			return run, err
		}
		run.Attempts = append(run.Attempts, a)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return run, err
	}
	if err = rows.Close(); err != nil {
		return run, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT version,content,content_hash,author,attempt_id,source_session_id,status,created_at,updated_at FROM pipeline_authoring_artifacts WHERE pipeline_id=? AND stage=? ORDER BY version`, pipelineID, stage)
	if err != nil {
		return run, err
	}
	for rows.Next() {
		var a catalog.AuthoringStageArtifact
		var created, updated string
		if err = rows.Scan(&a.Version, &a.Content, &a.ContentHash, &a.Author, &a.AttemptID, &a.SourceSessionID, &a.Status, &created, &updated); err != nil {
			rows.Close()
			return run, err
		}
		if a.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			rows.Close()
			return run, err
		}
		if a.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			rows.Close()
			return run, err
		}
		run.Artifacts = append(run.Artifacts, a)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return run, err
	}
	if err = rows.Close(); err != nil {
		return run, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT request_id,action,actor,artifact_version,feedback,reason,attempt_id,result_stage_revision,result_pipeline_revision,created_at FROM pipeline_authoring_requests WHERE pipeline_id=? AND stage=? ORDER BY result_stage_revision,request_id`, pipelineID, stage)
	if err != nil {
		return run, err
	}
	defer rows.Close()
	for rows.Next() {
		var a catalog.AuthoringStageAction
		var created string
		if err = rows.Scan(&a.RequestID, &a.Action, &a.Actor, &a.ArtifactVersion, &a.Feedback, &a.Reason, &a.AttemptID, &a.ResultStageRevision, &a.ResultPipelineRevision, &created); err != nil {
			return run, err
		}
		if a.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return run, err
		}
		run.Actions = append(run.Actions, a)
	}
	if err = rows.Err(); err != nil {
		return run, err
	}
	if err = rows.Close(); err != nil {
		return run, err
	}
	err = loadAuthoringStopState(ctx, tx, &run)
	return run, err
}

func (s *Store) GetAuthoringStage(ctx context.Context, pipelineID string, stage sdd.Stage) (catalog.AuthoringStageRun, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return catalog.AuthoringStageRun{}, err
	}
	defer tx.Rollback()
	run, err := loadAuthoringStageSnapshot(ctx, tx, pipelineID, stage)
	if err != nil {
		return run, err
	}
	return run, tx.Commit()
}
func (s *Store) GetAuthoringStageCommand(ctx context.Context, pipelineID, requestID string) (catalog.AuthoringStageCommandReceipt, error) {
	var r catalog.AuthoringStageCommandReceipt
	err := s.db.QueryRowContext(ctx, `SELECT stage,action,client_intent_hash,attempt_id,result_stage_revision,result_pipeline_revision FROM pipeline_authoring_requests WHERE pipeline_id=? AND request_id=?`, pipelineID, requestID).Scan(&r.Stage, &r.Action, &r.ClientIntentHash, &r.AttemptID, &r.ResultStageRevision, &r.ResultPipelineRevision)
	return r, err
}

func checkAuthoringStage(run catalog.AuthoringStageRun, p catalog.PipelineRun, ref catalog.AuthoringStageRequest) error {
	if run.Revision != ref.StageRevision || run.ArtifactVersion != ref.ArtifactVersion || p.Revision != ref.PipelineRevision || p.Current != ref.Stage || p.DiscoveryFrozenVersion != ref.DiscoveryVersion || p.Status[sdd.Discovery] != sdd.Completed {
		return ErrPipelineConflict
	}
	return nil
}

func authoringPipelineCAS(ctx context.Context, tx *sql.Tx, p catalog.PipelineRun, ref catalog.AuthoringStageRequest, status sdd.Status, next sdd.Stage, now time.Time) error {
	if err := legacyPipelineDesignFence(ctx, tx, ref.PipelineID); err != nil {
		return err
	}
	statuses := p.Status
	statuses[ref.Stage] = status
	current := ref.Stage
	if next != "" {
		current = next
		statuses[next] = sdd.Active
	}
	data, err := json.Marshal(statuses)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET revision=revision+1,stage_status=?,current_stage=?,updated_at=? WHERE id=? AND revision=? AND kind='ai_authoring' AND current_stage=? AND discovery_frozen_version=?`, data, current, formatCatalogTime(now), ref.PipelineID, ref.PipelineRevision, ref.Stage, ref.DiscoveryVersion)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPipelineConflict
	}
	return nil
}

func authoringPipelineSettlementCAS(ctx context.Context, tx *sql.Tx, p catalog.PipelineRun, ref catalog.AuthoringStageRequest, now time.Time) error {
	owned, err := legacyPipelineDesignOwned(ctx, tx, ref.PipelineID)
	if err != nil || owned {
		return err
	}
	return authoringPipelineCAS(ctx, tx, p, ref, sdd.Active, "", now)
}

func ensureAuthoringStage(ctx context.Context, tx *sql.Tx, run catalog.AuthoringStageRun, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO pipeline_authoring_stages(pipeline_id,stage,revision,state,created_at,updated_at) VALUES (?,?,0,'ready',?,?) ON CONFLICT(pipeline_id,stage) DO NOTHING`, run.PipelineID, run.Stage, formatCatalogTime(now), formatCatalogTime(now))
	return err
}

type authoringStageSnapshot struct {
	Run        catalog.AuthoringStageRun
	Selections []brainstormSelectionSnapshot
}

func encodeAuthoringStage(run catalog.AuthoringStageRun) ([]byte, error) {
	snap := authoringStageSnapshot{Run: run}
	for _, a := range run.Attempts {
		snap.Selections = append(snap.Selections, selectionSnapshot(a.Selection))
	}
	return json.Marshal(snap)
}
func decodeAuthoringStage(data []byte) (catalog.AuthoringStageRun, error) {
	var snap authoringStageSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return snap.Run, err
	}
	if len(snap.Selections) != len(snap.Run.Attempts) {
		return snap.Run, ErrPipelineConflict
	}
	for i, c := range snap.Selections {
		snap.Run.Attempts[i].Selection.CredentialIdentity = c.CredentialIdentity
	}
	return snap.Run, nil
}

func authoringRequestResult(ctx context.Context, tx *sql.Tx, ref catalog.AuthoringStageRequest, action, hash string) (string, []byte, bool, error) {
	var storedStage sdd.Stage
	var storedAction, storedHash, attempt string
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT stage,action,payload_hash,attempt_id,result_snapshot FROM pipeline_authoring_requests WHERE pipeline_id=? AND request_id=?`, ref.PipelineID, ref.RequestID).Scan(&storedStage, &storedAction, &storedHash, &attempt, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	if storedStage != ref.Stage || storedAction != action || storedHash != hash {
		return "", nil, false, ErrPipelineConflict
	}
	return attempt, data, true, nil
}

func authoringReceipt(ctx context.Context, tx *sql.Tx, ref catalog.AuthoringStageRequest, action, hash, intentHash, attemptID, feedback, reason string, now time.Time) (catalog.AuthoringStageRun, error) {
	run, err := loadAuthoringStageSnapshot(ctx, tx, ref.PipelineID, ref.Stage)
	if err != nil {
		return run, err
	}
	run.Actions = append(run.Actions, catalog.AuthoringStageAction{RequestID: ref.RequestID, Action: action, Actor: "local_user", ArtifactVersion: ref.ArtifactVersion, Feedback: feedback, Reason: reason, AttemptID: attemptID, ResultStageRevision: run.Revision, ResultPipelineRevision: run.PipelineRevision, CreatedAt: now})
	data, err := encodeAuthoringStage(run)
	if err != nil {
		return run, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_authoring_requests(pipeline_id,request_id,stage,action,payload_hash,client_intent_hash,attempt_id,artifact_version,actor,feedback,reason,result_stage_revision,result_pipeline_revision,result_snapshot,created_at) VALUES (?,?,?,?,?,?,?,?,'local_user',?,?,?,?,?,?)`, ref.PipelineID, ref.RequestID, ref.Stage, action, hash, intentHash, attemptID, ref.ArtifactVersion, feedback, reason, run.Revision, run.PipelineRevision, data, formatCatalogTime(now))
	if err != nil {
		return run, err
	}
	err = appendPipelineEvent(ctx, tx, ref.PipelineID, "pipeline.authoring."+action, map[string]any{"stage": ref.Stage, "stageRevision": run.Revision, "pipelineRevision": run.PipelineRevision, "artifactVersion": run.ArtifactVersion, "attemptId": attemptID}, now)
	return run, err
}

func loadAuthoringInput(ctx context.Context, tx *sql.Tx, p catalog.PipelineRun, stage sdd.Stage) (catalog.AuthoringStageInput, error) {
	in := catalog.AuthoringStageInput{DiscoveryVersion: p.DiscoveryFrozenVersion}
	if err := tx.QueryRowContext(ctx, `SELECT content FROM pipeline_artifacts WHERE pipeline_id=? AND stage='discovery' AND version=? AND author='user'`, p.ID, p.DiscoveryFrozenVersion).Scan(&in.DiscoveryContent); err != nil {
		return in, err
	}
	in.DiscoveryHash = brainstormHash(in.DiscoveryContent)
	if !utf8.ValidString(in.DiscoveryContent) || strings.TrimSpace(in.DiscoveryContent) == "" {
		return in, ErrPipelineConflict
	}
	var brainstormID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM pipeline_brainstorm_runs WHERE pipeline_id=? AND discovery_version=?`, p.ID, p.DiscoveryFrozenVersion).Scan(&brainstormID)
	if err == nil {
		in.BrainstormRunID = brainstormID
		brain, err := loadBrainstormSnapshot(ctx, tx, brainstormID)
		if err != nil {
			return in, err
		}
		if brain.State != "approved" || brain.DiscoveryVersion != p.DiscoveryFrozenVersion ||
			brain.DiscoveryContent != in.DiscoveryContent || brain.DiscoveryHash != in.DiscoveryHash {
			return in, ErrPipelineConflict
		}
		for _, synthesis := range brain.Syntheses {
			if synthesis.Status == "approved" {
				content := synthesis.Content
				in.Synthesis = &content
				in.SynthesisVersion = synthesis.Version
			}
		}
		confirmed := false
		for _, action := range brain.HumanActions {
			if action.Kind == "confirm" && action.Actor == "local_user" {
				confirmed = true
			}
			if action.Kind == "bypass" && action.Actor == "local_user" {
				in.DiscoveryBypassReason = action.Reason
			}
		}
		if in.Synthesis == nil && (!confirmed || in.DiscoveryBypassReason == "") {
			return in, ErrPipelineConflict
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		if p.DiscoveryFrozenVersion < 1 || p.Status[sdd.Discovery] != sdd.Completed || p.Status[sdd.Spec] == sdd.Pending {
			return in, ErrPipelineConflict
		}
		// With no Brainstorm for the frozen version and no incomplete run, the
		// active authoring phase is the durable evidence that Brainstorm was skipped.
		in.DiscoveryBypassReason = "Brainstorm skipped"
	} else {
		return in, err
	}
	if stage == sdd.Plan {
		spec, _, err := loadAuthoringStage(ctx, tx, p.ID, sdd.Spec)
		if err != nil {
			return in, err
		}
		switch {
		case p.Status[sdd.Spec] == sdd.Completed && spec.State == "approved":
			in.SpecVersion = spec.ArtifactVersion
			if err := tx.QueryRowContext(ctx, `SELECT content,content_hash FROM pipeline_authoring_artifacts WHERE pipeline_id=? AND stage='spec' AND version=? AND status='approved'`, p.ID, in.SpecVersion).Scan(&in.SpecContent, &in.SpecHash); err != nil {
				return in, err
			}
		case p.Status[sdd.Spec] == sdd.Skipped && spec.State == "skipped":
			if err := tx.QueryRowContext(ctx, `SELECT reason FROM pipeline_authoring_requests WHERE pipeline_id=? AND stage='spec' AND action='skip' AND actor='local_user' ORDER BY result_stage_revision DESC LIMIT 1`, p.ID).Scan(&in.SpecBypassReason); err != nil {
				return in, err
			}
		default:
			return in, ErrPipelineConflict
		}
	}
	return in, nil
}

func (s *Store) BeginAuthoringStage(ctx context.Context, in catalog.BeginAuthoringStageRequest) (catalog.AuthoringStageAttempt, bool, error) {
	return s.beginAuthoringStage(ctx, in, "start")
}
func (s *Store) ReviseAuthoringStage(ctx context.Context, in catalog.BeginAuthoringStageRequest) (catalog.AuthoringStageAttempt, bool, error) {
	return s.beginAuthoringStage(ctx, in, "revision")
}

// PreviewAuthoringStageInput captures only approved sources and inspected
// revision feedback. The caller can size its full prompt before admission;
// admission rechecks the same fences and freezes this same input independently.
func (s *Store) PreviewAuthoringStageInput(ctx context.Context, ref catalog.AuthoringStageRequest, feedback string) (catalog.AuthoringStageInput, error) {
	empty := catalog.AuthoringStageInput{}
	if !validStageRef(ref) || (feedback != "" && !validStageText(feedback)) {
		return empty, sdd.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	run, p, err := loadAuthoringStage(ctx, tx, ref.PipelineID, ref.Stage)
	if err != nil {
		return empty, err
	}
	action := "start"
	if feedback != "" {
		action = "revision"
	}
	input, err := authoringAttemptInput(ctx, tx, run, p, ref, action, feedback)
	if err != nil {
		return empty, err
	}
	return input, tx.Commit()
}

func authoringAttemptInput(ctx context.Context, tx *sql.Tx, run catalog.AuthoringStageRun, p catalog.PipelineRun, ref catalog.AuthoringStageRequest, action, feedback string) (catalog.AuthoringStageInput, error) {
	empty := catalog.AuthoringStageInput{}
	if run.CancellationPending {
		return empty, sdd.ErrAuthoringCancellationPending
	}
	if err := checkAuthoringStage(run, p, ref); err != nil {
		return empty, err
	}
	if (action == "start" && (run.State != "ready" && run.State != "paused")) || (action == "revision" && (run.State != "waiting_user" || p.Status[ref.Stage] != sdd.WaitingUser)) || run.AttemptCount >= sdd.MaxAuthoringAttempts || run.ArtifactVersion >= sdd.MaxAuthoringDrafts {
		return empty, sdd.ErrInvalidTransition
	}
	if action == "start" && p.Status[ref.Stage] != sdd.Active {
		return empty, sdd.ErrInvalidTransition
	}
	input, err := loadAuthoringInput(ctx, tx, p, ref.Stage)
	if err != nil {
		return empty, err
	}
	if action == "revision" {
		input.PreviousArtifactVersion = run.ArtifactVersion
		input.Feedback = feedback
		if err = tx.QueryRowContext(ctx, `SELECT content FROM pipeline_authoring_artifacts WHERE pipeline_id=? AND stage=? AND version=? AND status='waiting_user'`, p.ID, ref.Stage, run.ArtifactVersion).Scan(&input.PreviousContent); err != nil {
			return empty, err
		}
	} else if run.ArtifactVersion > 0 {
		// An explicit retry retains the failed revision's inspected feedback.
		last, err := scanAuthoringAttempt(tx.QueryRowContext(ctx, `SELECT `+authoringAttemptColumns+` FROM pipeline_authoring_attempts WHERE pipeline_id=? AND stage=? ORDER BY created_at DESC,id DESC LIMIT 1`, p.ID, ref.Stage))
		if err != nil {
			return empty, err
		}
		input.PreviousArtifactVersion = last.Input.PreviousArtifactVersion
		input.PreviousContent = last.Input.PreviousContent
		input.Feedback = last.Input.Feedback
	}
	return input, nil
}

func (s *Store) beginAuthoringStage(ctx context.Context, in catalog.BeginAuthoringStageRequest, action string) (catalog.AuthoringStageAttempt, bool, error) {
	empty := catalog.AuthoringStageAttempt{}
	if in.ModelMode == "" {
		in.ModelMode = "inherit"
	}
	if in.EffortMode == "" {
		in.EffortMode = "inherit"
	}
	if !validStageRef(in.Ref) || !validBrainstormSelection(in.Selection) || in.Selection.MaxOutputTokens > sdd.MaxAuthoringOutputTokens || in.EstimatedInputTokens <= 0 || in.EstimatedInputTokens > sdd.MaxAuthoringInputTokens || !brainstormCredentialFingerprint.MatchString(in.ClientIntentHash) || (action == "revision" && !validStageText(in.Feedback)) || (action == "start" && in.Feedback != "") {
		return empty, false, sdd.ErrInvalidTransition
	}
	if (in.ModelMode != "inherit" && in.ModelMode != "override") ||
		(in.EffortMode != "inherit" && in.EffortMode != "automatic" && in.EffortMode != "explicit") ||
		(in.PreferenceSource != "brainstorm" && in.PreferenceSource != "global_default" && in.PreferenceSource != "phase_override") ||
		in.PreferenceRevision < 0 {
		return empty, false, sdd.ErrInvalidTransition
	}
	hash := brainstormHash(struct {
		Action   string
		Request  catalog.BeginAuthoringStageRequest
		Identity string
	}{action, in, in.Selection.CredentialIdentity})
	tx, err := s.beginAuthoringStageTx(ctx)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback()
	attemptID, _, exists, err := authoringRequestResult(ctx, tx, in.Ref, action, hash)
	if err != nil {
		return empty, false, err
	}
	if exists {
		a, err := scanAuthoringAttempt(tx.QueryRowContext(ctx, `SELECT `+authoringAttemptColumns+` FROM pipeline_authoring_attempts WHERE id=?`, attemptID))
		if err != nil {
			return empty, false, err
		}
		return a, false, tx.Commit()
	}
	preference, err := loadAuthoringStageModelPreference(ctx, tx, in.Ref.PipelineID, in.Ref.Stage)
	if err != nil {
		return empty, false, err
	}
	if preference.Revision != in.PreferenceRevision {
		return empty, false, ErrPipelineConflict
	}
	if preference.ModelMode != in.ModelMode || preference.EffortMode != in.EffortMode {
		return empty, false, ErrPipelineConflict
	}
	if preference.Revision == 0 {
		if in.PreferenceSource != "brainstorm" && in.PreferenceSource != "global_default" {
			return empty, false, sdd.ErrInvalidTransition
		}
	} else if (preference.ModelMode == "override" && in.PreferenceSource != "phase_override") ||
		(preference.ModelMode == "inherit" && in.PreferenceSource != "brainstorm" && in.PreferenceSource != "global_default") {
		return empty, false, ErrPipelineConflict
	}
	run, p, err := loadAuthoringStage(ctx, tx, in.Ref.PipelineID, in.Ref.Stage)
	if err != nil {
		return empty, false, err
	}
	input, err := authoringAttemptInput(ctx, tx, run, p, in.Ref, action, in.Feedback)
	if err != nil {
		return empty, false, err
	}
	if err := validateAuthoringAttemptSelectionSource(ctx, tx, p, input, preference, in); err != nil {
		return empty, false, err
	}
	inputData, err := json.Marshal(input)
	if err != nil {
		return empty, false, err
	}
	reserved := max(in.EstimatedInputTokens, int64(len(inputData))+1024)
	output := int64(in.Selection.MaxOutputTokens)
	if reserved > sdd.MaxAuthoringInputTokens || reserved > run.InputBudgetRemaining || output > run.OutputBudgetRemaining || (in.Selection.ContextLength > 0 && reserved+output > int64(in.Selection.ContextLength)) {
		return empty, false, sdd.ErrAuthoringBudgetExceeded
	}
	now := time.Now().UTC()
	if err = authoringPipelineCAS(ctx, tx, p, in.Ref, sdd.Active, "", now); err != nil {
		return empty, false, err
	}
	if err = ensureAuthoringStage(ctx, tx, run, now); err != nil {
		return empty, false, err
	}
	if action == "revision" {
		if _, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_artifacts SET status='rejected',updated_at=? WHERE pipeline_id=? AND stage=? AND version=?`, formatCatalogTime(now), p.ID, in.Ref.Stage, run.ArtifactVersion); err != nil {
			return empty, false, err
		}
	}
	attemptID = id.New()
	selection, _ := json.Marshal(selectionSnapshot(in.Selection))
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_authoring_attempts(id,pipeline_id,stage,request_id,payload_hash,artifact_version,status,input_snapshot,selection_snapshot,reserved_input_tokens,reserved_output_tokens,created_at,updated_at,model_mode,effort_mode,preference_source,preference_revision) VALUES (?,?,?,?,?,?,'running',?,?,?,?,?,?,?,?,?,?)`, attemptID, p.ID, in.Ref.Stage, in.Ref.RequestID, hash, run.ArtifactVersion+1, inputData, selection, reserved, output, formatCatalogTime(now), formatCatalogTime(now), in.ModelMode, in.EffortMode, in.PreferenceSource, in.PreferenceRevision)
	if err != nil {
		return empty, false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_stages SET state='running',revision=revision+1,attempt_count=attempt_count+1,input_budget_remaining=input_budget_remaining-?,output_budget_remaining=output_budget_remaining-?,updated_at=? WHERE pipeline_id=? AND stage=?`, reserved, output, formatCatalogTime(now), p.ID, in.Ref.Stage)
	if err != nil {
		return empty, false, err
	}
	if _, err = authoringReceipt(ctx, tx, in.Ref, action, hash, in.ClientIntentHash, attemptID, in.Feedback, "", now); err != nil {
		return empty, false, err
	}
	a, err := scanAuthoringAttempt(tx.QueryRowContext(ctx, `SELECT `+authoringAttemptColumns+` FROM pipeline_authoring_attempts WHERE id=?`, attemptID))
	if err != nil {
		return empty, false, err
	}
	if err = tx.Commit(); err != nil {
		return empty, false, err
	}
	return a, true, nil
}
