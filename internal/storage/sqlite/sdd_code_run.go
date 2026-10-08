package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/sddworkspace"
)

var ErrAuthoringCodeRunNotFound = errors.New("authoring Code run not found")

const authoringCodeRunColumns = `id,pipeline_id,preparation_id,preparation_request_id,request_id,intent_hash,workspace_id,
 pipeline_revision,plan_stage_revision,plan_artifact_version,code_preference_revision,code_selection_hash,manifest_hash,
 plan_source_hash,private_path,preference_snapshot_json,manifest_snapshot_json,max_prompt_bytes,max_output_tokens,max_output_tokens_per_turn,max_turns,
 max_tool_calls,timeout_millis,COALESCE(session_id,''),status,error_code,usage_json,result_manifest_json,source_manifest_json,changes_json,patch_json,COALESCE(patch_hash,''),created_at,updated_at`

func scanAuthoringCodeRun(row rowScanner) (catalog.AuthoringCodeRun, error) {
	var attempt catalog.AuthoringCodeRun
	var preferenceJSON, manifestJSON, usageJSON, resultManifestJSON, sourceManifestJSON, changesJSON, patchJSON []byte
	var created, updated string
	if err := row.Scan(
		&attempt.ID, &attempt.PipelineID, &attempt.PreparationID, &attempt.PreparationRequestID, &attempt.RequestID, &attempt.IntentHash,
		&attempt.WorkspaceID, &attempt.PipelineRevision, &attempt.PlanStageRevision, &attempt.PlanArtifactVersion,
		&attempt.CodePreferenceRevision, &attempt.CodeSelectionHash, &attempt.ManifestHash, &attempt.PlanSourceHash, &attempt.PrivatePath,
		&preferenceJSON, &manifestJSON, &attempt.MaxPromptBytes, &attempt.MaxOutputTokens, &attempt.MaxOutputTokensPerTurn, &attempt.MaxTurns, &attempt.MaxToolCalls,
		&attempt.TimeoutMillis, &attempt.SessionID, &attempt.Status, &attempt.ErrorCode, &usageJSON, &resultManifestJSON, &sourceManifestJSON, &changesJSON, &patchJSON, &attempt.PatchHash, &created, &updated,
	); err != nil {
		return attempt, err
	}
	if err := json.Unmarshal(preferenceJSON, &attempt.PreferenceSnapshot); err != nil {
		return attempt, fmt.Errorf("decode Code preference snapshot: %w", err)
	}
	if err := json.Unmarshal(manifestJSON, &attempt.ManifestSnapshot); err != nil {
		return attempt, fmt.Errorf("decode Code baseline manifest: %w", err)
	}
	if len(usageJSON) > 0 {
		var usage catalog.BrainstormUsage
		if err := json.Unmarshal(usageJSON, &usage); err != nil {
			return attempt, fmt.Errorf("decode Code usage: %w", err)
		}
		attempt.Usage = &usage
	}
	if len(resultManifestJSON) > 0 {
		var manifest catalog.AuthoringCodeCopyManifest
		if err := json.Unmarshal(resultManifestJSON, &manifest); err != nil {
			return attempt, fmt.Errorf("decode Code result manifest: %w", err)
		}
		attempt.ResultManifest = &manifest
	}
	if len(sourceManifestJSON) > 0 {
		var manifest catalog.AuthoringCodeCopyManifest
		if err := json.Unmarshal(sourceManifestJSON, &manifest); err != nil {
			return attempt, fmt.Errorf("decode Code source readback manifest: %w", err)
		}
		attempt.SourceManifest = &manifest
	}
	if len(changesJSON) > 0 {
		if err := json.Unmarshal(changesJSON, &attempt.Changes); err != nil {
			return attempt, fmt.Errorf("decode Code changes: %w", err)
		}
	}
	attempt.PatchJSON = append([]byte(nil), patchJSON...)
	var err error
	if attempt.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return attempt, fmt.Errorf("parse Code run creation time: %w", err)
	}
	if attempt.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return attempt, fmt.Errorf("parse Code run update time: %w", err)
	}
	if attempt.Status == "completed" {
		if attempt.ResultManifest == nil || attempt.SourceManifest == nil ||
			!validAuthoringCodeCopyManifest(attempt.ManifestSnapshot, attempt.ManifestHash) ||
			!validAuthoringCodeCopyManifest(*attempt.SourceManifest, attempt.ManifestHash) ||
			!validAuthoringCodeCopyManifest(*attempt.ResultManifest, attempt.ResultManifest.Hash) ||
			attempt.SourceManifest.Hash != attempt.ManifestHash ||
			sddworkspace.ValidatePatchEvidence(attempt.PatchJSON, attempt.PatchHash, codeRunManifestToWorkspace(attempt.ManifestSnapshot), attempt.SourceManifest.Hash, codeRunManifestToWorkspace(*attempt.ResultManifest)) != nil {
			return attempt, errors.New("completed authoring Code run has invalid patch evidence")
		}
	} else if len(attempt.PatchJSON) != 0 || attempt.PatchHash != "" {
		return attempt, errors.New("non-completed authoring Code run contains reviewable patch evidence")
	}
	return attempt, nil
}

func validAuthoringCodeRun(attempt catalog.AuthoringCodeRun) bool {
	if !safeBrainstormText(attempt.ID, 128) || !safeBrainstormText(attempt.PipelineID, 128) ||
		!safeBrainstormText(attempt.PreparationID, 128) || !safeBrainstormText(attempt.PreparationRequestID, 128) ||
		!safeBrainstormText(attempt.RequestID, 128) || !brainstormCredentialFingerprint.MatchString(attempt.IntentHash) ||
		!safeBrainstormText(attempt.WorkspaceID, 128) || attempt.PipelineRevision < 1 || attempt.PlanStageRevision < 0 ||
		attempt.PlanArtifactVersion < 0 || attempt.CodePreferenceRevision < 0 ||
		!brainstormCredentialFingerprint.MatchString(attempt.CodeSelectionHash) ||
		!brainstormCredentialFingerprint.MatchString(attempt.ManifestHash) ||
		!brainstormCredentialFingerprint.MatchString(attempt.PlanSourceHash) || !filepath.IsAbs(attempt.PrivatePath) ||
		attempt.MaxPromptBytes < 1 || attempt.MaxPromptBytes > 1024*1024 || attempt.MaxOutputTokens != sdd.MaxAuthoringCodeOutputTokens ||
		attempt.MaxOutputTokensPerTurn != sdd.MaxAuthoringOutputTokens ||
		attempt.MaxTurns != sdd.MaxAuthoringCodeTurns || attempt.MaxToolCalls != sdd.MaxAuthoringCodeToolCalls || attempt.TimeoutMillis < 1000 || attempt.TimeoutMillis > int64(10*time.Minute/time.Millisecond) ||
		attempt.Status != "running" || attempt.SessionID != "" || attempt.ErrorCode != "" || attempt.Usage != nil || attempt.ResultManifest != nil || attempt.SourceManifest != nil || len(attempt.Changes) != 0 || len(attempt.PatchJSON) != 0 || attempt.PatchHash != "" {
		return false
	}
	preference := attempt.PreferenceSnapshot
	selectionHash, err := catalog.HashAuthoringCodeSelection(preference)
	if err != nil || selectionHash != attempt.CodeSelectionHash || preference.SelectionHash != attempt.CodeSelectionHash ||
		preference.PipelineID != attempt.PipelineID || preference.Stage != string(sdd.Code) ||
		preference.PreferenceRevision != attempt.CodePreferenceRevision || preference.Resolution != "ready" ||
		preference.CatalogValidationRequired {
		return false
	}
	return validAuthoringCodeCopySelection(preference.Selection) && validAuthoringCodeCopyManifest(attempt.ManifestSnapshot, attempt.ManifestHash)
}

func (s *Store) GetAuthoringCodeRun(ctx context.Context, pipelineID, requestID string) (catalog.AuthoringCodeRun, error) {
	if !safeBrainstormText(pipelineID, 128) || !safeBrainstormText(requestID, 128) {
		return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
	}
	return scanAuthoringCodeRun(s.db.QueryRowContext(ctx, `SELECT `+authoringCodeRunColumns+` FROM pipeline_authoring_code_runs WHERE pipeline_id=? AND request_id=?`, pipelineID, requestID))
}

func (s *Store) GetAuthoringCodeRunByID(ctx context.Context, id string) (catalog.AuthoringCodeRun, error) {
	if !safeBrainstormText(id, 128) {
		return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
	}
	return scanAuthoringCodeRun(s.db.QueryRowContext(ctx, `SELECT `+authoringCodeRunColumns+` FROM pipeline_authoring_code_runs WHERE id=?`, id))
}

func (s *Store) ListAuthoringCodeRuns(ctx context.Context, pipelineID string) ([]catalog.AuthoringCodeRun, error) {
	if !safeBrainstormText(pipelineID, 128) {
		return nil, sdd.ErrInvalidTransition
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+authoringCodeRunColumns+` FROM pipeline_authoring_code_runs WHERE pipeline_id=? ORDER BY created_at,id`, pipelineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]catalog.AuthoringCodeRun, 0)
	for rows.Next() {
		attempt, err := scanAuthoringCodeRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, attempt)
	}
	return result, rows.Err()
}

func (s *Store) BeginAuthoringCodeRun(ctx context.Context, attempt catalog.AuthoringCodeRun) (catalog.AuthoringCodeRun, bool, error) {
	if !validAuthoringCodeRun(attempt) {
		return catalog.AuthoringCodeRun{}, false, sdd.ErrInvalidTransition
	}
	tx, err := s.beginAuthoringCodeCopyTx(ctx)
	if err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var existingID, existingIntent string
	err = tx.QueryRowContext(ctx, `SELECT id,intent_hash FROM pipeline_authoring_code_runs WHERE pipeline_id=? AND request_id=?`, attempt.PipelineID, attempt.RequestID).Scan(&existingID, &existingIntent)
	if err == nil {
		if existingIntent != attempt.IntentHash {
			return catalog.AuthoringCodeRun{}, false, ErrPipelineConflict
		}
		existing, err := scanAuthoringCodeRun(tx.QueryRowContext(ctx, `SELECT `+authoringCodeRunColumns+` FROM pipeline_authoring_code_runs WHERE id=?`, existingID))
		if err != nil {
			return catalog.AuthoringCodeRun{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return catalog.AuthoringCodeRun{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return catalog.AuthoringCodeRun{}, false, err
	}

	prepared, err := loadAuthoringCodeCopyAttempt(ctx, tx, attempt.PipelineID, attempt.PreparationRequestID)
	if err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	if prepared.ID != attempt.PreparationID || prepared.Status != "prepared" || prepared.WorkspaceID != attempt.WorkspaceID ||
		prepared.PipelineRevision != attempt.PipelineRevision || prepared.CodePreferenceRevision != attempt.CodePreferenceRevision ||
		prepared.PreferenceSnapshot.SelectionHash != attempt.CodeSelectionHash || prepared.ManifestHash != attempt.ManifestHash ||
		prepared.PrivatePath != attempt.PrivatePath || !equalJSON(prepared.PreferenceSnapshot, attempt.PreferenceSnapshot) ||
		!equalJSON(prepared.ManifestSnapshot, attempt.ManifestSnapshot) {
		return catalog.AuthoringCodeRun{}, false, ErrPipelineConflict
	}

	var sourcePath string
	if err := tx.QueryRowContext(ctx, `SELECT path FROM workspaces WHERE id=?`, attempt.WorkspaceID).Scan(&sourcePath); err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	if sourcePath != prepared.SourcePath {
		return catalog.AuthoringCodeRun{}, false, ErrPipelineConflict
	}
	pipeline, err := scanPipeline(tx.QueryRowContext(ctx, `SELECT `+pipelineColumns+` FROM pipeline_runs WHERE id=?`, attempt.PipelineID))
	if err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	if pipeline.Revision != attempt.PipelineRevision || pipeline.WorkspaceID != attempt.WorkspaceID || !validAuthoringCodeCopyPipeline(pipeline) {
		return catalog.AuthoringCodeRun{}, false, ErrPipelineConflict
	}
	if err := requireAuthoringCodeRevisionRequest(ctx, tx, attempt.PipelineID); err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	codePreference, err := loadAuthoringStageModelPreference(ctx, tx, attempt.PipelineID, sdd.Code)
	if err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	if codePreference.Revision != attempt.CodePreferenceRevision {
		return catalog.AuthoringCodeRun{}, false, ErrPipelineConflict
	}
	var planRevision, planArtifactVersion int64
	var planState string
	if err := tx.QueryRowContext(ctx, `SELECT revision,state,artifact_version FROM pipeline_authoring_stages WHERE pipeline_id=? AND stage='plan'`, attempt.PipelineID).Scan(&planRevision, &planState, &planArtifactVersion); err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	if planRevision != attempt.PlanStageRevision || planArtifactVersion != attempt.PlanArtifactVersion ||
		(pipeline.Status[sdd.Plan] == sdd.Completed && planState != "approved") || (pipeline.Status[sdd.Plan] == sdd.Skipped && planState != "skipped") {
		return catalog.AuthoringCodeRun{}, false, ErrPipelineConflict
	}
	if pipeline.Status[sdd.Plan] == sdd.Completed {
		var approvedVersion int64
		if err := tx.QueryRowContext(ctx, `SELECT version FROM pipeline_authoring_artifacts WHERE pipeline_id=? AND stage='plan' AND status='approved'`, attempt.PipelineID).Scan(&approvedVersion); err != nil || approvedVersion != attempt.PlanArtifactVersion {
			return catalog.AuthoringCodeRun{}, false, ErrPipelineConflict
		}
	} else {
		var skips int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pipeline_authoring_requests WHERE pipeline_id=? AND stage='plan' AND action='skip' AND actor='local_user'`, attempt.PipelineID).Scan(&skips); err != nil || skips == 0 {
			return catalog.AuthoringCodeRun{}, false, ErrPipelineConflict
		}
	}

	attempt.CreatedAt, attempt.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	preferenceJSON, err := json.Marshal(attempt.PreferenceSnapshot)
	if err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	manifestJSON, err := json.Marshal(attempt.ManifestSnapshot)
	if err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_authoring_code_runs(
	 id,pipeline_id,preparation_id,preparation_request_id,request_id,intent_hash,workspace_id,pipeline_revision,
	 plan_stage_revision,plan_artifact_version,code_preference_revision,code_selection_hash,manifest_hash,plan_source_hash,
		 private_path,preference_snapshot_json,manifest_snapshot_json,max_prompt_bytes,max_output_tokens,max_output_tokens_per_turn,max_turns,max_tool_calls,
		 timeout_millis,session_id,status,error_code,created_at,updated_at)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,'running','',?,?)`,
		attempt.ID, attempt.PipelineID, attempt.PreparationID, attempt.PreparationRequestID, attempt.RequestID, attempt.IntentHash,
		attempt.WorkspaceID, attempt.PipelineRevision, attempt.PlanStageRevision, attempt.PlanArtifactVersion,
		attempt.CodePreferenceRevision, attempt.CodeSelectionHash, attempt.ManifestHash, attempt.PlanSourceHash, attempt.PrivatePath,
		preferenceJSON, manifestJSON, attempt.MaxPromptBytes, attempt.MaxOutputTokens, attempt.MaxOutputTokensPerTurn, attempt.MaxTurns, attempt.MaxToolCalls,
		attempt.TimeoutMillis, formatCatalogTime(attempt.CreatedAt), formatCatalogTime(attempt.UpdatedAt))
	if err != nil {
		prior, readErr := scanAuthoringCodeRun(tx.QueryRowContext(ctx, `SELECT `+authoringCodeRunColumns+` FROM pipeline_authoring_code_runs WHERE pipeline_id=? AND request_id=?`, attempt.PipelineID, attempt.RequestID))
		if readErr == nil && prior.IntentHash == attempt.IntentHash {
			if commitErr := tx.Commit(); commitErr == nil {
				return prior, false, nil
			}
		}
		return catalog.AuthoringCodeRun{}, false, ErrPipelineConflict
	}
	created, err := scanAuthoringCodeRun(tx.QueryRowContext(ctx, `SELECT `+authoringCodeRunColumns+` FROM pipeline_authoring_code_runs WHERE id=?`, attempt.ID))
	if err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return catalog.AuthoringCodeRun{}, false, err
	}
	return created, true, nil
}

func equalJSON(left, right any) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && string(a) == string(b)
}

func (s *Store) CreateAuthoringCodeSession(ctx context.Context, attemptID string, session catalog.SessionRecord, selection catalog.ModelSelection) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	attempt, err := scanAuthoringCodeRun(tx.QueryRowContext(ctx, `SELECT `+authoringCodeRunColumns+` FROM pipeline_authoring_code_runs WHERE id=?`, attemptID))
	if err != nil {
		return err
	}
	if attempt.Status != "running" || attempt.SessionID != "" || session.Mode != catalog.AuthoringCodeSessionMode || session.ID != selection.SessionID ||
		session.WorkspaceID != attempt.WorkspaceID || session.BackendID != attempt.PreferenceSnapshot.Selection.BackendID ||
		selection.BackendID != session.BackendID || selection.WorkspacePath != attempt.PrivatePath || selection.ModelID != attempt.PreferenceSnapshot.Selection.ModelID ||
		selection.MaxOutputTokens != attempt.MaxOutputTokensPerTurn || selection.ReasoningEffort != attempt.PreferenceSnapshot.Selection.ReasoningEffort {
		return ErrPipelineConflict
	}
	if err := createSessionWithSnapshots(ctx, tx, session, nil, "", nil, "agent_session", &selection); err != nil {
		return err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_authoring_code_runs SET session_id=?,updated_at=? WHERE id=? AND status='running' AND session_id IS NULL`, session.ID, formatCatalogTime(now), attemptID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrPipelineConflict
	}
	return tx.Commit()
}

func (s *Store) RequestAuthoringCodeRunCancel(ctx context.Context, id string) (catalog.AuthoringCodeRun, error) {
	attempt, err := s.GetAuthoringCodeRunByID(ctx, id)
	if err != nil {
		return attempt, err
	}
	if attempt.Status == "running" {
		_, err = s.db.ExecContext(ctx, `UPDATE pipeline_authoring_code_runs SET status='cancelling',updated_at=? WHERE id=? AND status='running'`, formatCatalogTime(time.Now().UTC()), id)
		if err != nil {
			return attempt, err
		}
		attempt, err = s.GetAuthoringCodeRunByID(ctx, id)
	}
	return attempt, err
}

func (s *Store) FinishAuthoringCodeRun(ctx context.Context, id, status, errorCode string, usage *catalog.BrainstormUsage, result, source *catalog.AuthoringCodeCopyManifest, changes []catalog.CodeFileChange, patchJSON []byte, patchHash string) (catalog.AuthoringCodeRun, error) {
	if status != "completed" && status != "failed" && status != "cancelled" {
		return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
	}
	if errorCode != "" && !safeAuthoringCodeRunError(errorCode) {
		return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
	}
	if usage != nil && (usage.InputTokens < 0 || usage.OutputTokens < 0 || (usage.CostUSD != nil && (math.IsNaN(*usage.CostUSD) || math.IsInf(*usage.CostUSD, 0) || *usage.CostUSD < 0))) {
		return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
	}
	if result != nil && !validAuthoringCodeCopyManifest(*result, result.Hash) {
		return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
	}
	if source != nil && !validAuthoringCodeCopyManifest(*source, source.Hash) {
		return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
	}
	if !validCodeFileChanges(changes) {
		return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
	}
	if status == "completed" {
		if result == nil || source == nil || len(patchJSON) == 0 || int64(len(patchJSON)) > sddworkspace.MaxPatchEvidenceBytes || !brainstormCredentialFingerprint.MatchString(patchHash) {
			return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
		}
	} else if len(patchJSON) != 0 || patchHash != "" {
		return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
	}
	usageJSON, resultJSON, sourceJSON, changesJSON := []byte(nil), []byte(nil), []byte(nil), []byte(nil)
	var err error
	if usage != nil {
		usageJSON, err = json.Marshal(usage)
		if err != nil {
			return catalog.AuthoringCodeRun{}, err
		}
	}
	if result != nil {
		resultJSON, err = json.Marshal(result)
		if err != nil {
			return catalog.AuthoringCodeRun{}, err
		}
	}
	if source != nil {
		sourceJSON, err = json.Marshal(source)
		if err != nil {
			return catalog.AuthoringCodeRun{}, err
		}
	}
	if changes != nil {
		changesJSON, err = json.Marshal(changes)
		if err != nil {
			return catalog.AuthoringCodeRun{}, err
		}
	}
	if status == "completed" {
		attempt, err := s.GetAuthoringCodeRunByID(ctx, id)
		if err != nil {
			return attempt, err
		}
		if source.Hash != attempt.ManifestHash || sddworkspace.ValidatePatchEvidence(patchJSON, patchHash, codeRunManifestToWorkspace(attempt.ManifestSnapshot), source.Hash, codeRunManifestToWorkspace(*result)) != nil {
			return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
		}
		expectedChanges := sddworkspace.Compare(codeRunManifestToWorkspace(attempt.ManifestSnapshot), codeRunManifestToWorkspace(*result))
		if len(expectedChanges) != len(changes) {
			return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
		}
		for index, change := range expectedChanges {
			if changes[index] != (catalog.CodeFileChange{Path: change.Path, Kind: string(change.Kind)}) {
				return catalog.AuthoringCodeRun{}, sdd.ErrInvalidTransition
			}
		}
	}
	attempt, err := s.GetAuthoringCodeRunByID(ctx, id)
	if err != nil {
		return attempt, err
	}
	if attempt.Status == status && attempt.ErrorCode == errorCode && equalJSON(attempt.Usage, usage) && equalJSON(attempt.ResultManifest, result) && equalJSON(attempt.SourceManifest, source) && equalJSON(attempt.Changes, changes) && bytes.Equal(attempt.PatchJSON, patchJSON) && attempt.PatchHash == patchHash {
		return attempt, nil
	}
	if attempt.Status != "running" && attempt.Status != "cancelling" {
		return attempt, ErrPipelineConflict
	}
	var patchHashValue any
	if patchHash != "" {
		patchHashValue = patchHash
	}
	resultRow, err := s.db.ExecContext(ctx, `UPDATE pipeline_authoring_code_runs SET status=?,error_code=?,usage_json=?,result_manifest_json=?,source_manifest_json=?,changes_json=?,patch_json=?,patch_hash=?,updated_at=? WHERE id=? AND status IN ('running','cancelling')`,
		status, errorCode, nullableJSON(usageJSON), nullableJSON(resultJSON), nullableJSON(sourceJSON), nullableJSON(changesJSON), nullableJSON(patchJSON), patchHashValue, formatCatalogTime(time.Now().UTC()), id)
	if err != nil {
		return attempt, err
	}
	changed, err := resultRow.RowsAffected()
	if err != nil {
		return attempt, err
	}
	if changed != 1 {
		return attempt, ErrPipelineConflict
	}
	return s.GetAuthoringCodeRunByID(ctx, id)
}

func codeRunManifestToWorkspace(manifest catalog.AuthoringCodeCopyManifest) sddworkspace.Manifest {
	out := sddworkspace.Manifest{Version: manifest.Version, FileCount: manifest.FileCount, TotalBytes: manifest.TotalBytes, Hash: manifest.Hash}
	out.Entries = make([]sddworkspace.Entry, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		out.Entries = append(out.Entries, sddworkspace.Entry{Path: entry.Path, Type: sddworkspace.EntryType(entry.Type), Mode: entry.Mode, Size: entry.Size, SHA256: entry.SHA256, Target: entry.Target})
	}
	out.Excluded = make([]sddworkspace.Exclusion, 0, len(manifest.Excluded))
	for _, exclusion := range manifest.Excluded {
		out.Excluded = append(out.Excluded, sddworkspace.Exclusion{Path: exclusion.Path, Reason: exclusion.Reason})
	}
	return out
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func safeAuthoringCodeRunError(code string) bool {
	switch code {
	case "cancelled", "timeout", "provider_failed", "composition_failed", "journal_failed", "usage_unavailable", "output_limit_exceeded", "turn_limit", "tool_failed", "tool_limit_exceeded", "snapshot_stale", "result_unavailable", "source_readback_failed", "patch_too_large", "app_restart":
		return true
	default:
		return false
	}
}

func validCodeFileChanges(changes []catalog.CodeFileChange) bool {
	for index, change := range changes {
		if !safeCodeCopyManifestPath(change.Path) || (change.Kind != "added" && change.Kind != "deleted" && change.Kind != "content" && change.Kind != "mode" && change.Kind != "type" && change.Kind != "target") ||
			(index > 0 && (changes[index-1].Path > change.Path || (changes[index-1].Path == change.Path && changes[index-1].Kind >= change.Kind))) {
			return false
		}
	}
	return true
}

func (s *Store) InterruptRunningAuthoringCodeRuns(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pipeline_authoring_code_runs SET status='interrupted',error_code='app_restart',updated_at=? WHERE status IN ('running','cancelling')`, formatCatalogTime(time.Now().UTC()))
	return err
}
