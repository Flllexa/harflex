package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/sddworkspace"
)

var (
	ErrAuthoringCodeCopyNotFound = errors.New("authoring Code copy preparation not found")
	ErrAuthoringCodeCopyLimit    = errors.New("authoring Code copy preparation limit reached")
)

const authoringCodeCopyColumns = `id,pipeline_id,workspace_id,request_id,intent_hash,pipeline_revision,code_preference_revision,source_path,private_path,manifest_hash,preference_snapshot_json,manifest_snapshot_json,status,error_code,created_at,updated_at`

func scanAuthoringCodeCopyAttempt(row rowScanner) (catalog.AuthoringCodeCopyAttempt, error) {
	var attempt catalog.AuthoringCodeCopyAttempt
	var preferenceJSON, manifestJSON []byte
	var created, updated string
	if err := row.Scan(
		&attempt.ID, &attempt.PipelineID, &attempt.WorkspaceID, &attempt.RequestID, &attempt.IntentHash,
		&attempt.PipelineRevision, &attempt.CodePreferenceRevision, &attempt.SourcePath, &attempt.PrivatePath,
		&attempt.ManifestHash, &preferenceJSON, &manifestJSON, &attempt.Status, &attempt.ErrorCode, &created, &updated,
	); err != nil {
		return attempt, err
	}
	if err := json.Unmarshal(preferenceJSON, &attempt.PreferenceSnapshot); err != nil {
		return attempt, fmt.Errorf("decode authoring Code copy preference snapshot: %w", err)
	}
	if err := json.Unmarshal(manifestJSON, &attempt.ManifestSnapshot); err != nil {
		return attempt, fmt.Errorf("decode authoring Code copy manifest snapshot: %w", err)
	}
	var err error
	if attempt.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return attempt, fmt.Errorf("parse authoring Code copy creation time: %w", err)
	}
	if attempt.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return attempt, fmt.Errorf("parse authoring Code copy update time: %w", err)
	}
	return attempt, nil
}

func validAuthoringCodeCopyAttempt(attempt catalog.AuthoringCodeCopyAttempt) bool {
	if !safeBrainstormText(attempt.ID, 128) || !safeBrainstormText(attempt.PipelineID, 128) ||
		!safeBrainstormText(attempt.WorkspaceID, 128) || !safeBrainstormText(attempt.RequestID, 128) ||
		!brainstormCredentialFingerprint.MatchString(attempt.IntentHash) || attempt.PipelineRevision < 1 ||
		attempt.CodePreferenceRevision < 0 || !filepath.IsAbs(attempt.SourcePath) || !filepath.IsAbs(attempt.PrivatePath) ||
		!brainstormCredentialFingerprint.MatchString(attempt.ManifestHash) || attempt.Status != "preparing" || attempt.ErrorCode != "" {
		return false
	}
	preference := attempt.PreferenceSnapshot
	if preference.ModelMode != "inherit" && preference.ModelMode != "override" ||
		(preference.EffortMode != "inherit" && preference.EffortMode != "automatic" && preference.EffortMode != "explicit") ||
		(preference.EffortMode == "explicit" && !safeBrainstormText(preference.ExplicitEffort, 64)) ||
		(preference.EffortMode != "explicit" && preference.ExplicitEffort != "") {
		return false
	}
	selectionHash, err := catalog.HashAuthoringCodeSelection(preference)
	if err != nil || selectionHash != preference.SelectionHash {
		return false
	}
	return preference.PipelineID == attempt.PipelineID && preference.Stage == string(sdd.Code) &&
		preference.PreferenceRevision == attempt.CodePreferenceRevision &&
		preference.Resolution == "ready" && !preference.CatalogValidationRequired &&
		brainstormCredentialFingerprint.MatchString(preference.SelectionHash) && preference.ErrorCode == "" &&
		validAuthoringCodeCopySelection(preference.Selection) &&
		validAuthoringCodeCopyManifest(attempt.ManifestSnapshot, attempt.ManifestHash)
}

func validAuthoringCodeCopySelection(selection catalog.AuthoringCodeCopySelection) bool {
	if !safeBrainstormText(selection.BackendID, 128) || !safeBrainstormText(selection.ModelID, 512) ||
		!safeBrainstormText(selection.CatalogRevision, 128) || !safeBrainstormText(selection.Source, 128) ||
		!safeBrainstormText(selection.Destination, 512) || selection.MaxOutputTokens < 1 || selection.MaxOutputTokens > sdd.MaxAuthoringOutputTokens ||
		selection.ContextLength < 0 || selection.CheckedAt.IsZero() || selection.ConfirmJITLoad {
		return false
	}
	switch selection.Source {
	case "openai_models", "openrouter_account", "ollama_tags", "lm_studio_native":
		if selection.Status != "listed" || selection.ConfirmUnfiltered {
			return false
		}
	case "openrouter_general_unfiltered":
		if selection.Status != "listed_unfiltered" || !selection.ConfirmUnfiltered {
			return false
		}
	default:
		return false
	}
	destination, err := url.Parse(selection.Destination)
	return err == nil && (destination.Scheme == "http" || destination.Scheme == "https") &&
		destination.Host != "" && destination.User == nil && destination.RawQuery == "" && destination.Fragment == ""
}

func validAuthoringCodeCopyManifest(manifest catalog.AuthoringCodeCopyManifest, expectedHash string) bool {
	if manifest.Version != 1 || manifest.Hash != expectedHash || len(manifest.Entries) > 40_000 || manifest.FileCount < 0 || manifest.TotalBytes < 0 {
		return false
	}
	var files int
	var totalBytes int64
	for i, entry := range manifest.Entries {
		if !safeCodeCopyManifestPath(entry.Path) || entry.Size < 0 {
			return false
		}
		if i > 0 && manifest.Entries[i-1].Path >= entry.Path {
			return false
		}
		switch entry.Type {
		case "directory":
			if entry.Mode != 0o040000 {
				return false
			}
		case "file":
			if entry.Mode != 0o100644 && entry.Mode != 0o100755 || !brainstormCredentialFingerprint.MatchString(entry.SHA256) {
				return false
			}
			files++
			totalBytes += entry.Size
		case "symlink":
			if entry.Mode != 0o120000 || !brainstormCredentialFingerprint.MatchString(entry.SHA256) || !utf8.ValidString(entry.Target) {
				return false
			}
			files++
			totalBytes += int64(len(entry.Target))
		default:
			return false
		}
	}
	if files != manifest.FileCount || totalBytes != manifest.TotalBytes {
		return false
	}
	for i, exclusion := range manifest.Excluded {
		if !safeCodeCopyManifestPath(exclusion.Path) || !safeBrainstormText(exclusion.Reason, 128) {
			return false
		}
		if i > 0 && (manifest.Excluded[i-1].Path > exclusion.Path ||
			(manifest.Excluded[i-1].Path == exclusion.Path && manifest.Excluded[i-1].Reason >= exclusion.Reason)) {
			return false
		}
	}
	canonical := sddworkspace.Manifest{
		Version: manifest.Version, FileCount: manifest.FileCount, TotalBytes: manifest.TotalBytes, Hash: manifest.Hash,
		Entries:  make([]sddworkspace.Entry, 0, len(manifest.Entries)),
		Excluded: make([]sddworkspace.Exclusion, 0, len(manifest.Excluded)),
	}
	for _, entry := range manifest.Entries {
		canonical.Entries = append(canonical.Entries, sddworkspace.Entry{
			Path: entry.Path, Type: sddworkspace.EntryType(entry.Type), Mode: entry.Mode, Size: entry.Size, SHA256: entry.SHA256, Target: entry.Target,
		})
	}
	for _, exclusion := range manifest.Excluded {
		canonical.Excluded = append(canonical.Excluded, sddworkspace.Exclusion{Path: exclusion.Path, Reason: exclusion.Reason})
	}
	return sddworkspace.HashManifest(canonical) == expectedHash
}

func safeCodeCopyManifestPath(value string) bool {
	return value != "" && len(value) <= 4096 && utf8.ValidString(value) && !filepath.IsAbs(value) &&
		filepath.ToSlash(filepath.Clean(filepath.FromSlash(value))) == value && value != "." && !strings.ContainsRune(value, 0)
}

func (s *Store) beginAuthoringCodeCopyTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_authoring_code_copy_attempts SET updated_at=updated_at WHERE 0`); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func loadAuthoringCodeCopyAttempt(ctx context.Context, tx *sql.Tx, pipelineID, requestID string) (catalog.AuthoringCodeCopyAttempt, error) {
	return scanAuthoringCodeCopyAttempt(tx.QueryRowContext(ctx, `SELECT `+authoringCodeCopyColumns+` FROM pipeline_authoring_code_copy_attempts WHERE pipeline_id=? AND request_id=?`, pipelineID, requestID))
}

func (s *Store) GetAuthoringCodeCopyAttempt(ctx context.Context, pipelineID, requestID string) (catalog.AuthoringCodeCopyAttempt, error) {
	if !safeBrainstormText(pipelineID, 128) || !safeBrainstormText(requestID, 128) {
		return catalog.AuthoringCodeCopyAttempt{}, sdd.ErrInvalidTransition
	}
	return scanAuthoringCodeCopyAttempt(s.db.QueryRowContext(ctx, `SELECT `+authoringCodeCopyColumns+` FROM pipeline_authoring_code_copy_attempts WHERE pipeline_id=? AND request_id=?`, pipelineID, requestID))
}

func validAuthoringCodeCopyPipeline(run catalog.PipelineRun) bool {
	return run.Kind == "ai_authoring" && run.Current == sdd.Code && run.Status[sdd.Code] == sdd.Active &&
		(run.Status[sdd.Plan] == sdd.Completed || run.Status[sdd.Plan] == sdd.Skipped)
}

func (s *Store) BeginAuthoringCodeCopyAttempt(ctx context.Context, attempt catalog.AuthoringCodeCopyAttempt) (catalog.AuthoringCodeCopyAttempt, bool, error) {
	if !validAuthoringCodeCopyAttempt(attempt) {
		return catalog.AuthoringCodeCopyAttempt{}, false, sdd.ErrInvalidTransition
	}
	tx, err := s.beginAuthoringCodeCopyTx(ctx)
	if err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, false, fmt.Errorf("begin authoring Code copy reservation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existingID, existingIntent string
	err = tx.QueryRowContext(ctx, `SELECT id,intent_hash FROM pipeline_authoring_code_copy_attempts WHERE pipeline_id=? AND request_id=?`, attempt.PipelineID, attempt.RequestID).Scan(&existingID, &existingIntent)
	if err == nil {
		if existingIntent != attempt.IntentHash {
			return catalog.AuthoringCodeCopyAttempt{}, false, ErrPipelineConflict
		}
		existing, err := scanAuthoringCodeCopyAttempt(tx.QueryRowContext(ctx, `SELECT `+authoringCodeCopyColumns+` FROM pipeline_authoring_code_copy_attempts WHERE id=?`, existingID))
		if err != nil {
			return catalog.AuthoringCodeCopyAttempt{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return catalog.AuthoringCodeCopyAttempt{}, false, fmt.Errorf("commit authoring Code copy replay: %w", err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return catalog.AuthoringCodeCopyAttempt{}, false, err
	}

	run, err := scanPipeline(tx.QueryRowContext(ctx, `SELECT `+pipelineColumns+` FROM pipeline_runs WHERE id=?`, attempt.PipelineID))
	if err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, false, err
	}
	if run.Revision != attempt.PipelineRevision || run.WorkspaceID != attempt.WorkspaceID {
		return catalog.AuthoringCodeCopyAttempt{}, false, ErrPipelineConflict
	}
	if !validAuthoringCodeCopyPipeline(run) {
		return catalog.AuthoringCodeCopyAttempt{}, false, sdd.ErrInvalidTransition
	}
	if err := requireAuthoringCodeRevisionRequest(ctx, tx, attempt.PipelineID); err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, false, err
	}
	preference, err := loadAuthoringStageModelPreference(ctx, tx, attempt.PipelineID, sdd.Code)
	if err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, false, err
	}
	if preference.Revision != attempt.CodePreferenceRevision {
		return catalog.AuthoringCodeCopyAttempt{}, false, ErrPipelineConflict
	}
	var count, active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN status='preparing' THEN 1 ELSE 0 END),0) FROM pipeline_authoring_code_copy_attempts WHERE pipeline_id=?`, attempt.PipelineID).Scan(&count, &active); err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, false, err
	}
	if count >= sdd.MaxAuthoringAttempts {
		return catalog.AuthoringCodeCopyAttempt{}, false, ErrAuthoringCodeCopyLimit
	}
	if active > 0 {
		return catalog.AuthoringCodeCopyAttempt{}, false, ErrPipelineConflict
	}
	preferenceJSON, err := json.Marshal(attempt.PreferenceSnapshot)
	if err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, false, err
	}
	manifestJSON, err := json.Marshal(attempt.ManifestSnapshot)
	if err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, false, err
	}
	now := time.Now().UTC()
	attempt.CreatedAt, attempt.UpdatedAt = now, now
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_authoring_code_copy_attempts(
		id,pipeline_id,workspace_id,request_id,intent_hash,pipeline_revision,code_preference_revision,
		source_path,private_path,manifest_hash,preference_snapshot_json,manifest_snapshot_json,status,error_code,created_at,updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		attempt.ID, attempt.PipelineID, attempt.WorkspaceID, attempt.RequestID, attempt.IntentHash,
		attempt.PipelineRevision, attempt.CodePreferenceRevision, attempt.SourcePath, attempt.PrivatePath,
		attempt.ManifestHash, preferenceJSON, manifestJSON, attempt.Status, attempt.ErrorCode,
		formatCatalogTime(now), formatCatalogTime(now),
	)
	if err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, false, fmt.Errorf("insert authoring Code copy attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, false, fmt.Errorf("commit authoring Code copy attempt: %w", err)
	}
	return attempt, true, nil
}

func (s *Store) CompleteAuthoringCodeCopyAttempt(ctx context.Context, id, privatePath, manifestHash, selectionHash string) (catalog.AuthoringCodeCopyAttempt, error) {
	if !safeBrainstormText(id, 128) || !brainstormCredentialFingerprint.MatchString(selectionHash) {
		return catalog.AuthoringCodeCopyAttempt{}, sdd.ErrInvalidTransition
	}
	tx, err := s.beginAuthoringCodeCopyTx(ctx)
	if err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, err
	}
	defer func() { _ = tx.Rollback() }()
	attempt, err := scanAuthoringCodeCopyAttempt(tx.QueryRowContext(ctx, `SELECT `+authoringCodeCopyColumns+` FROM pipeline_authoring_code_copy_attempts WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.AuthoringCodeCopyAttempt{}, ErrAuthoringCodeCopyNotFound
	}
	if err != nil {
		return catalog.AuthoringCodeCopyAttempt{}, err
	}
	if attempt.Status == "prepared" && attempt.PrivatePath == privatePath && attempt.ManifestHash == manifestHash &&
		attempt.PreferenceSnapshot.SelectionHash == selectionHash {
		if err := tx.Commit(); err != nil {
			return catalog.AuthoringCodeCopyAttempt{}, err
		}
		return attempt, nil
	}
	if attempt.Status != "preparing" {
		return attempt, ErrPipelineConflict
	}
	if attempt.PrivatePath != privatePath || attempt.ManifestHash != manifestHash {
		if _, err := tx.ExecContext(ctx, `UPDATE pipeline_authoring_code_copy_attempts SET status='failed',error_code='copy_verification_failed',updated_at=? WHERE id=? AND status='preparing'`, formatCatalogTime(time.Now().UTC()), id); err != nil {
			return attempt, err
		}
		attempt.Status, attempt.ErrorCode = "failed", "copy_verification_failed"
		if err := tx.Commit(); err != nil {
			return attempt, err
		}
		return attempt, ErrPipelineConflict
	}
	run, err := scanPipeline(tx.QueryRowContext(ctx, `SELECT `+pipelineColumns+` FROM pipeline_runs WHERE id=?`, attempt.PipelineID))
	if err != nil {
		return attempt, err
	}
	preference, err := loadAuthoringStageModelPreference(ctx, tx, attempt.PipelineID, sdd.Code)
	if err != nil {
		return attempt, err
	}
	staleCode := ""
	if run.Revision != attempt.PipelineRevision || !validAuthoringCodeCopyPipeline(run) {
		staleCode = "pipeline_revision_changed"
	} else if preference.Revision != attempt.CodePreferenceRevision {
		staleCode = "code_preference_revision_changed"
	} else if attempt.PreferenceSnapshot.SelectionHash != selectionHash {
		staleCode = "code_selection_changed"
	}
	if staleCode != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE pipeline_authoring_code_copy_attempts SET status='stale',error_code=?,updated_at=? WHERE id=? AND status='preparing'`, staleCode, formatCatalogTime(time.Now().UTC()), id); err != nil {
			return attempt, err
		}
		attempt.Status, attempt.ErrorCode, attempt.UpdatedAt = "stale", staleCode, time.Now().UTC()
		if err := tx.Commit(); err != nil {
			return attempt, err
		}
		return attempt, ErrPipelineConflict
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_authoring_code_copy_attempts SET status='prepared',updated_at=? WHERE id=? AND status='preparing' AND pipeline_revision=? AND code_preference_revision=?`, formatCatalogTime(now), id, attempt.PipelineRevision, attempt.CodePreferenceRevision)
	if err != nil {
		return attempt, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return attempt, err
	}
	if changed != 1 {
		return attempt, ErrPipelineConflict
	}
	attempt.Status, attempt.ErrorCode, attempt.UpdatedAt = "prepared", "", now
	if err := tx.Commit(); err != nil {
		return attempt, err
	}
	return attempt, nil
}

func (s *Store) FailAuthoringCodeCopyAttempt(ctx context.Context, id, errorCode string) error {
	if !safeBrainstormText(id, 128) || !safeCodeCopyError(errorCode) {
		return sdd.ErrInvalidTransition
	}
	result, err := s.db.ExecContext(ctx, `UPDATE pipeline_authoring_code_copy_attempts SET status='failed',error_code=?,updated_at=? WHERE id=? AND status='preparing'`, errorCode, formatCatalogTime(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 1 {
		return nil
	}
	var status, storedCode string
	err = s.db.QueryRowContext(ctx, `SELECT status,error_code FROM pipeline_authoring_code_copy_attempts WHERE id=?`, id).Scan(&status, &storedCode)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAuthoringCodeCopyNotFound
	}
	if err != nil {
		return err
	}
	if status == "failed" && storedCode == errorCode {
		return nil
	}
	return ErrPipelineConflict
}

func safeCodeCopyError(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func (s *Store) InterruptPreparingAuthoringCodeCopyAttempts(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pipeline_authoring_code_copy_attempts SET status='interrupted',error_code='process_interrupted',updated_at=? WHERE status='preparing'`, formatCatalogTime(time.Now().UTC()))
	return err
}

func (s *Store) ListAuthoringCodeCopyAttempts(ctx context.Context, pipelineID string) ([]catalog.AuthoringCodeCopyAttempt, error) {
	if !safeBrainstormText(pipelineID, 128) {
		return nil, sdd.ErrInvalidTransition
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+authoringCodeCopyColumns+` FROM pipeline_authoring_code_copy_attempts WHERE pipeline_id=? ORDER BY created_at,id`, pipelineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attempts := make([]catalog.AuthoringCodeCopyAttempt, 0)
	for rows.Next() {
		attempt, err := scanAuthoringCodeCopyAttempt(rows)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return attempts, nil
}
