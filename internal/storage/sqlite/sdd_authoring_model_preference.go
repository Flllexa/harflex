package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func validAuthoringModelPreferenceStage(stage sdd.Stage) bool {
	return validAuthoringStage(stage) || stage == sdd.Code || stage == sdd.Eval
}

func loadAuthoringModelPreferenceTarget(ctx context.Context, tx *sql.Tx, pipelineID string, stage sdd.Stage) error {
	if validAuthoringStage(stage) {
		_, _, err := loadAuthoringStage(ctx, tx, pipelineID, stage)
		return err
	}
	if stage != sdd.Code && stage != sdd.Eval {
		return sdd.ErrInvalidTransition
	}
	pipeline, err := scanPipeline(tx.QueryRowContext(ctx, "SELECT "+pipelineColumns+" FROM pipeline_runs WHERE id = ?", pipelineID))
	if err != nil {
		return err
	}
	if pipeline.Kind != "ai_authoring" {
		return sdd.ErrInvalidTransition
	}
	return nil
}

func validAuthoringModelPreference(preference catalog.AuthoringStageModelPreference) bool {
	if !safeBrainstormText(preference.PipelineID, 128) || !validAuthoringModelPreferenceStage(preference.Stage) ||
		(preference.ModelMode != "inherit" && preference.ModelMode != "override") ||
		(preference.EffortMode != "inherit" && preference.EffortMode != "automatic" && preference.EffortMode != "explicit") {
		return false
	}
	if preference.ModelMode == "inherit" {
		if preference.Selection.BackendID != "" || preference.Selection.ModelID != "" || preference.Selection.ReasoningEffort != "" ||
			preference.Selection.CatalogRevision != "" || preference.Selection.Destination != "" || preference.Selection.CredentialIdentity != "" {
			return false
		}
	} else if preference.Selection.ReasoningEffort != "" || !validBrainstormSelection(preference.Selection) {
		return false
	}
	if preference.EffortMode == "explicit" {
		return safeBrainstormText(preference.ExplicitEffort, 64) && strings.TrimSpace(preference.ExplicitEffort) == preference.ExplicitEffort
	}
	return preference.ExplicitEffort == ""
}

func loadAuthoringStageModelPreference(ctx context.Context, tx *sql.Tx, pipelineID string, stage sdd.Stage) (catalog.AuthoringStageModelPreference, error) {
	preference := catalog.AuthoringStageModelPreference{PipelineID: pipelineID, Stage: stage, ModelMode: "inherit", EffortMode: "inherit"}
	var selection []byte
	var created, updated string
	err := tx.QueryRowContext(ctx, `SELECT model_mode,effort_mode,explicit_effort,model_selection_snapshot,revision,created_at,updated_at FROM pipeline_authoring_model_preferences WHERE pipeline_id=? AND stage=?`, pipelineID, stage).
		Scan(&preference.ModelMode, &preference.EffortMode, &preference.ExplicitEffort, &selection, &preference.Revision, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return preference, nil
	}
	if err != nil {
		return preference, err
	}
	if preference.ModelMode == "override" {
		if preference.Selection, err = decodeBrainstormSelection(selection); err != nil {
			return preference, err
		}
	}
	if preference.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return preference, err
	}
	if preference.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return preference, err
	}
	if !validAuthoringModelPreference(preference) || preference.Revision < 1 {
		return preference, ErrPipelineConflict
	}
	return preference, nil
}

func (s *Store) GetAuthoringStageModelPreference(ctx context.Context, pipelineID string, stage sdd.Stage) (catalog.AuthoringStageModelPreference, error) {
	if !safeBrainstormText(pipelineID, 128) || !validAuthoringModelPreferenceStage(stage) {
		return catalog.AuthoringStageModelPreference{}, sdd.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return catalog.AuthoringStageModelPreference{}, err
	}
	defer tx.Rollback()
	if err := loadAuthoringModelPreferenceTarget(ctx, tx, pipelineID, stage); err != nil {
		return catalog.AuthoringStageModelPreference{}, err
	}
	preference, err := loadAuthoringStageModelPreference(ctx, tx, pipelineID, stage)
	if err != nil {
		return preference, err
	}
	return preference, tx.Commit()
}

func (s *Store) SaveAuthoringStageModelPreference(ctx context.Context, preference catalog.AuthoringStageModelPreference, expectedRevision int64) (catalog.AuthoringStageModelPreference, error) {
	if !validAuthoringModelPreference(preference) || expectedRevision < 0 {
		return catalog.AuthoringStageModelPreference{}, sdd.ErrInvalidTransition
	}
	tx, err := s.beginAuthoringStageTx(ctx)
	if err != nil {
		return catalog.AuthoringStageModelPreference{}, err
	}
	defer tx.Rollback()
	if err := loadAuthoringModelPreferenceTarget(ctx, tx, preference.PipelineID, preference.Stage); err != nil {
		return catalog.AuthoringStageModelPreference{}, err
	}
	current, err := loadAuthoringStageModelPreference(ctx, tx, preference.PipelineID, preference.Stage)
	if err != nil {
		return catalog.AuthoringStageModelPreference{}, err
	}
	if current.Revision != expectedRevision {
		return catalog.AuthoringStageModelPreference{}, ErrPipelineConflict
	}
	nextRevision := current.Revision + 1
	now := time.Now().UTC()
	selectionData := []byte(nil)
	if preference.ModelMode == "override" {
		selectionData, err = json.Marshal(selectionSnapshot(preference.Selection))
		if err != nil {
			return catalog.AuthoringStageModelPreference{}, err
		}
	}
	if current.Revision == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_authoring_model_preferences(pipeline_id,stage,model_mode,effort_mode,explicit_effort,model_selection_snapshot,revision,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`, preference.PipelineID, preference.Stage, preference.ModelMode, preference.EffortMode, preference.ExplicitEffort, selectionData, nextRevision, formatCatalogTime(now), formatCatalogTime(now))
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE pipeline_authoring_model_preferences SET model_mode=?,effort_mode=?,explicit_effort=?,model_selection_snapshot=?,revision=?,updated_at=? WHERE pipeline_id=? AND stage=? AND revision=?`, preference.ModelMode, preference.EffortMode, preference.ExplicitEffort, selectionData, nextRevision, formatCatalogTime(now), preference.PipelineID, preference.Stage, current.Revision)
		if err == nil {
			var changed int64
			changed, err = result.RowsAffected()
			if err == nil && changed != 1 {
				err = ErrPipelineConflict
			}
		}
	}
	if err != nil {
		return catalog.AuthoringStageModelPreference{}, err
	}
	saved, err := loadAuthoringStageModelPreference(ctx, tx, preference.PipelineID, preference.Stage)
	if err != nil {
		return catalog.AuthoringStageModelPreference{}, err
	}
	if saved.Revision != nextRevision {
		return catalog.AuthoringStageModelPreference{}, ErrPipelineConflict
	}
	return saved, tx.Commit()
}

func loadAuthoringParentModelSelection(ctx context.Context, tx *sql.Tx, pipeline catalog.PipelineRun) (catalog.ModelSelection, string, error) {
	if pipeline.DiscoveryFrozenVersion < 1 || pipeline.Status[sdd.Discovery] != sdd.Completed {
		return catalog.ModelSelection{}, "", ErrPipelineConflict
	}
	var runID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM pipeline_brainstorm_runs WHERE pipeline_id=? AND discovery_version=?`, pipeline.ID, pipeline.DiscoveryFrozenVersion).Scan(&runID)
	if err == nil {
		brain, err := loadBrainstormSnapshot(ctx, tx, runID)
		if err != nil {
			return catalog.ModelSelection{}, "", err
		}
		if brain.State != "approved" || brain.DiscoveryVersion != pipeline.DiscoveryFrozenVersion {
			return catalog.ModelSelection{}, "", ErrPipelineConflict
		}
		return brain.Selection, "brainstorm", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return catalog.ModelSelection{}, "", err
	}
	if pipeline.Status[sdd.Spec] == sdd.Pending {
		return catalog.ModelSelection{}, "", ErrPipelineConflict
	}
	var backendID string
	if err := tx.QueryRowContext(ctx, `SELECT default_backend_id FROM app_settings WHERE id=1`).Scan(&backendID); err != nil {
		return catalog.ModelSelection{}, "", err
	}
	if backendID == "" {
		return catalog.ModelSelection{}, "", ErrPipelineConflict
	}
	var modelID string
	if err := tx.QueryRowContext(ctx, `SELECT model FROM provider_profiles WHERE id=?`, backendID).Scan(&modelID); err != nil {
		return catalog.ModelSelection{}, "", err
	}
	if strings.TrimSpace(modelID) == "" {
		return catalog.ModelSelection{}, "", ErrPipelineConflict
	}
	return catalog.ModelSelection{BackendID: backendID, ModelID: modelID}, "global_default", nil
}

func sameAuthoringModelSource(selection, source catalog.ModelSelection, sourceKind string) bool {
	if selection.BackendID != source.BackendID || selection.ModelID != source.ModelID {
		return false
	}
	if sourceKind == "global_default" {
		return true
	}
	return selection.CatalogRevision == source.CatalogRevision && selection.Source == source.Source &&
		selection.Destination == source.Destination && selection.CredentialIdentity == source.CredentialIdentity &&
		selection.Status == source.Status && selection.ConfirmUnfiltered == source.ConfirmUnfiltered &&
		selection.ConfirmJITLoad == source.ConfirmJITLoad && selection.ContextLength == source.ContextLength
}

func loadAuthoringParentSelectionFromInput(ctx context.Context, tx *sql.Tx, pipeline catalog.PipelineRun, input catalog.AuthoringStageInput) (catalog.ModelSelection, string, error) {
	if input.BrainstormRunID != "" {
		brain, err := loadBrainstormSnapshot(ctx, tx, input.BrainstormRunID)
		if err != nil {
			return catalog.ModelSelection{}, "", err
		}
		if brain.PipelineID != pipeline.ID || brain.DiscoveryVersion != pipeline.DiscoveryFrozenVersion || brain.State != "approved" {
			return catalog.ModelSelection{}, "", ErrPipelineConflict
		}
		return brain.Selection, "brainstorm", nil
	}
	if input.DiscoveryBypassReason != "Brainstorm skipped" || pipeline.DiscoveryFrozenVersion < 1 ||
		pipeline.Status[sdd.Discovery] != sdd.Completed || pipeline.Status[sdd.Spec] == sdd.Pending {
		return catalog.ModelSelection{}, "", ErrPipelineConflict
	}
	var backendID string
	if err := tx.QueryRowContext(ctx, `SELECT default_backend_id FROM app_settings WHERE id=1`).Scan(&backendID); err != nil {
		return catalog.ModelSelection{}, "", err
	}
	if backendID == "" {
		return catalog.ModelSelection{}, "", ErrPipelineConflict
	}
	var modelID string
	if err := tx.QueryRowContext(ctx, `SELECT model FROM provider_profiles WHERE id=?`, backendID).Scan(&modelID); err != nil {
		return catalog.ModelSelection{}, "", err
	}
	if strings.TrimSpace(modelID) == "" {
		return catalog.ModelSelection{}, "", ErrPipelineConflict
	}
	return catalog.ModelSelection{BackendID: backendID, ModelID: modelID}, "global_default", nil
}

func validateAuthoringAttemptSelectionSource(ctx context.Context, tx *sql.Tx, pipeline catalog.PipelineRun, input catalog.AuthoringStageInput, preference catalog.AuthoringStageModelPreference, in catalog.BeginAuthoringStageRequest) error {
	var parent catalog.ModelSelection
	var parentSource string
	if preference.ModelMode == "inherit" || preference.EffortMode == "inherit" {
		var err error
		parent, parentSource, err = loadAuthoringParentSelectionFromInput(ctx, tx, pipeline, input)
		if err != nil {
			return ErrPipelineConflict
		}
	}
	var expectedModel catalog.ModelSelection
	var expectedSource string
	if preference.ModelMode == "override" {
		expectedModel, expectedSource = preference.Selection, "phase_override"
	} else {
		expectedModel, expectedSource = parent, parentSource
	}
	if in.PreferenceSource != expectedSource || !sameAuthoringModelSource(in.Selection, expectedModel, expectedSource) {
		return ErrPipelineConflict
	}
	expectedEffort := ""
	switch preference.EffortMode {
	case "inherit":
		expectedEffort = parent.ReasoningEffort
	case "explicit":
		expectedEffort = preference.ExplicitEffort
	case "automatic":
	default:
		return ErrPipelineConflict
	}
	if in.Selection.ReasoningEffort != expectedEffort {
		return ErrPipelineConflict
	}
	return nil
}
