package application

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/sddworkspace"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

var authoringCodeManifestHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

type PrepareAuthoringCodeInput struct {
	PipelineID                     string `json:"pipelineId"`
	RequestID                      string `json:"requestId"`
	ExpectedPipelineRevision       int64  `json:"expectedPipelineRevision"`
	ExpectedCodePreferenceRevision int64  `json:"expectedCodePreferenceRevision"`
	ExpectedCodeSelectionHash      string `json:"expectedCodeSelectionHash"`
	ExpectedManifestHash           string `json:"expectedManifestHash"`
	ConfirmCopy                    bool   `json:"confirmCopy"`
}

type AuthoringCodeCopyPreparationDTO struct {
	ID                     string                           `json:"id"`
	PipelineID             string                           `json:"pipelineId"`
	WorkspaceID            string                           `json:"workspaceId"`
	RequestID              string                           `json:"requestId"`
	PipelineRevision       int64                            `json:"pipelineRevision"`
	CodePreferenceRevision int64                            `json:"codePreferenceRevision"`
	CodeSelectionHash      string                           `json:"codeSelectionHash"`
	CodePreference         AuthoringStageModelPreferenceDTO `json:"codePreference"`
	SourcePath             string                           `json:"sourcePath"`
	PrivatePath            string                           `json:"privatePath"`
	Manifest               sddworkspace.Manifest            `json:"manifest"`
	ManifestHash           string                           `json:"manifestHash"`
	Status                 string                           `json:"status"`
	ErrorCode              string                           `json:"errorCode,omitempty"`
	CreatedAt              time.Time                        `json:"createdAt"`
	UpdatedAt              time.Time                        `json:"updatedAt"`
}

type GetAuthoringCodeCopyPreparationsInput struct {
	PipelineID string `json:"pipelineId"`
}

type AuthoringCodeCopyPreparationsDTO struct {
	PipelineID      string                            `json:"pipelineId"`
	AttemptCount    int                               `json:"attemptCount"`
	MaxAttemptCount int                               `json:"maxAttemptCount"`
	Attempts        []AuthoringCodeCopyPreparationDTO `json:"attempts"`
}

type authoringCodeCopyIntent struct {
	PipelineID                     string `json:"pipelineId"`
	ExpectedPipelineRevision       int64  `json:"expectedPipelineRevision"`
	ExpectedCodePreferenceRevision int64  `json:"expectedCodePreferenceRevision"`
	ExpectedCodeSelectionHash      string `json:"expectedCodeSelectionHash"`
	ExpectedManifestHash           string `json:"expectedManifestHash"`
	ConfirmCopy                    bool   `json:"confirmCopy"`
}

func authoringCodeCopyIntentHash(in PrepareAuthoringCodeInput) (string, error) {
	data, err := json.Marshal(authoringCodeCopyIntent{
		PipelineID: in.PipelineID, ExpectedPipelineRevision: in.ExpectedPipelineRevision,
		ExpectedCodePreferenceRevision: in.ExpectedCodePreferenceRevision,
		ExpectedCodeSelectionHash:      in.ExpectedCodeSelectionHash,
		ExpectedManifestHash:           in.ExpectedManifestHash, ConfirmCopy: in.ConfirmCopy,
	})
	if err != nil {
		return "", fmt.Errorf("encode authoring Code copy intent: %w", err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func validAuthoringCodeCopyInput(in PrepareAuthoringCodeInput) bool {
	return validSelectionText(in.PipelineID, 128) && authoringRequestID.MatchString(in.RequestID) &&
		in.ExpectedPipelineRevision > 0 && in.ExpectedCodePreferenceRevision >= 0 &&
		authoringCodeManifestHash.MatchString(in.ExpectedCodeSelectionHash) &&
		authoringCodeManifestHash.MatchString(in.ExpectedManifestHash)
}

func (s *Service) PrepareAuthoringCode(in PrepareAuthoringCodeInput) (AuthoringCodeCopyPreparationDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringCodeCopyPreparationDTO{}, err
	}
	defer s.endCall()
	if !in.ConfirmCopy {
		return AuthoringCodeCopyPreparationDTO{}, ErrInvalidInput
	}
	if !validAuthoringCodeCopyInput(in) {
		return AuthoringCodeCopyPreparationDTO{}, ErrInvalidInput
	}
	intentHash, err := authoringCodeCopyIntentHash(in)
	if err != nil {
		return AuthoringCodeCopyPreparationDTO{}, err
	}
	existing, err := s.store.GetAuthoringCodeCopyAttempt(s.ctx, in.PipelineID, in.RequestID)
	if err == nil {
		if existing.IntentHash != intentHash {
			return AuthoringCodeCopyPreparationDTO{}, sqlite.ErrPipelineConflict
		}
		if existing.Status == "preparing" && !s.privateCodeCopyIsInFlight(existing.ID) {
			return s.resumePreparingAuthoringCodeCopy(existing)
		}
		return authoringCodeCopyPreparationDTO(existing), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AuthoringCodeCopyPreparationDTO{}, safe("read authoring Code copy request", err)
	}
	if strings.TrimSpace(s.privateWorkspaceParent) == "" {
		return AuthoringCodeCopyPreparationDTO{}, ErrInvalidInput
	}

	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return AuthoringCodeCopyPreparationDTO{}, err
	}
	if run.Revision != in.ExpectedPipelineRevision {
		return AuthoringCodeCopyPreparationDTO{}, sqlite.ErrPipelineConflict
	}
	if !validAuthoringCodePreflightStage(run) {
		return AuthoringCodeCopyPreparationDTO{}, sdd.ErrInvalidTransition
	}
	preference, err := s.store.GetAuthoringStageModelPreference(s.ctx, run.ID, sdd.Code)
	if err != nil {
		return AuthoringCodeCopyPreparationDTO{}, safe("read Code model preference", err)
	}
	if preference.Revision != in.ExpectedCodePreferenceRevision {
		return AuthoringCodeCopyPreparationDTO{}, sqlite.ErrPipelineConflict
	}
	resolved, err := s.resolveAuthoringStageModelPreference(s.ctx, preference)
	if err != nil {
		return AuthoringCodeCopyPreparationDTO{}, err
	}
	selectionHash, err := authoringCodeSelectionHash(resolved)
	if err != nil {
		return AuthoringCodeCopyPreparationDTO{}, safe("fingerprint effective Code model selection", err)
	}
	if selectionHash != in.ExpectedCodeSelectionHash {
		return AuthoringCodeCopyPreparationDTO{}, sqlite.ErrPipelineConflict
	}
	if resolved.Resolution != "ready" || resolved.CatalogValidationRequired ||
		resolved.Selection.BackendID == "" || resolved.Selection.ModelID == "" {
		return AuthoringCodeCopyPreparationDTO{}, authoringPreferenceExecutionError(resolved)
	}

	preflight, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: in.PipelineID, ExpectedPipelineRevision: in.ExpectedPipelineRevision,
		ExpectedCodePreferenceRevision: in.ExpectedCodePreferenceRevision,
	})
	if err != nil {
		return AuthoringCodeCopyPreparationDTO{}, err
	}
	if preflight.CodePreference.Resolution != "ready" || preflight.CodePreference.CatalogValidationRequired ||
		preflight.CodePreference.Selection.BackendID == "" || preflight.CodePreference.Selection.ModelID == "" {
		return AuthoringCodeCopyPreparationDTO{}, authoringPreferenceExecutionError(preflight.CodePreference)
	}
	if preflight.CodeSelectionHash != in.ExpectedCodeSelectionHash {
		return AuthoringCodeCopyPreparationDTO{}, sqlite.ErrPipelineConflict
	}
	if preflight.ManifestHash != in.ExpectedManifestHash || preflight.Manifest.Hash != in.ExpectedManifestHash {
		return AuthoringCodeCopyPreparationDTO{}, sqlite.ErrPipelineConflict
	}

	attemptID := "authoring_code_copy_" + id.New()
	privateName := ".harflex-sdd-code-" + attemptID
	privatePath, err := sddworkspace.PrivateCopyDestination(s.privateWorkspaceParent, privateName)
	if err != nil {
		return AuthoringCodeCopyPreparationDTO{}, safe("plan private Code copy path", err)
	}
	attempt := catalog.AuthoringCodeCopyAttempt{
		ID: attemptID, PipelineID: preflight.PipelineID, WorkspaceID: preflight.WorkspaceID,
		RequestID: in.RequestID, IntentHash: intentHash, PipelineRevision: preflight.PipelineRevision,
		CodePreferenceRevision: preflight.CodePreferenceRevision, SourcePath: preflight.SourcePath,
		PrivatePath: privatePath, ManifestHash: preflight.ManifestHash,
		PreferenceSnapshot: authoringCodeCopyPreferenceSnapshot(preflight.CodePreference, preflight.CodeSelectionHash),
		ManifestSnapshot:   authoringCodeCopyManifestSnapshot(preflight.Manifest), Status: "preparing",
	}
	var reserved catalog.AuthoringCodeCopyAttempt
	var created bool
	err = func() error {
		s.profileGate.RLock()
		defer s.profileGate.RUnlock()
		latestPreference, err := s.store.GetAuthoringStageModelPreference(s.ctx, run.ID, sdd.Code)
		if err != nil {
			return safe("recheck Code model preference", err)
		}
		if latestPreference.Revision != in.ExpectedCodePreferenceRevision {
			return sqlite.ErrPipelineConflict
		}
		latestResolved, err := s.resolveAuthoringStageModelPreference(s.ctx, latestPreference)
		if err != nil {
			return err
		}
		latestSelectionHash, err := authoringCodeSelectionHash(latestResolved)
		if err != nil {
			return safe("recheck effective Code model selection", err)
		}
		if latestSelectionHash != in.ExpectedCodeSelectionHash || latestSelectionHash != preflight.CodeSelectionHash {
			return sqlite.ErrPipelineConflict
		}
		if latestResolved.Resolution != "ready" || latestResolved.CatalogValidationRequired ||
			latestResolved.Selection.BackendID == "" || latestResolved.Selection.ModelID == "" {
			return authoringPreferenceExecutionError(latestResolved)
		}
		s.setPrivateCodeCopyInFlight(attemptID, true)
		reserved, created, err = s.store.BeginAuthoringCodeCopyAttempt(s.ctx, attempt)
		if err != nil {
			receipt, readErr := s.store.GetAuthoringCodeCopyAttempt(s.ctx, attempt.PipelineID, attempt.RequestID)
			if readErr == nil && receipt.ID == attemptID && receipt.IntentHash == intentHash && receipt.Status == "preparing" {
				if failErr := s.store.FailAuthoringCodeCopyAttempt(s.ctx, attemptID, "reservation_failed"); failErr != nil {
					s.setPrivateCodeCopyInFlight(attemptID, false)
					return errors.Join(safe("reserve authoring Code copy", err), safe("terminalize uncertain reservation", failErr))
				}
			} else if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
				s.setPrivateCodeCopyInFlight(attemptID, false)
				return errors.Join(safe("reserve authoring Code copy", err), safe("read uncertain reservation receipt", readErr))
			}
			s.setPrivateCodeCopyInFlight(attemptID, false)
			return safe("reserve authoring Code copy", err)
		}
		if !created {
			s.setPrivateCodeCopyInFlight(attemptID, false)
		}
		return nil
	}()
	if err != nil {
		return AuthoringCodeCopyPreparationDTO{}, err
	}
	if !created {
		if reserved.IntentHash != intentHash {
			return AuthoringCodeCopyPreparationDTO{}, sqlite.ErrPipelineConflict
		}
		if reserved.Status == "preparing" && !s.privateCodeCopyIsInFlight(reserved.ID) {
			return s.resumePreparingAuthoringCodeCopy(reserved)
		}
		return authoringCodeCopyPreparationDTO(reserved), nil
	}
	defer s.setPrivateCodeCopyInFlight(reserved.ID, false)
	copyResult, err := s.privateCodeCopy(s.ctx, preflight.SourcePath, s.privateWorkspaceParent, reserved.PrivatePath, preflight.Manifest)
	if err != nil {
		if persistErr := s.store.FailAuthoringCodeCopyAttempt(s.ctx, reserved.ID, "private_copy_failed"); persistErr != nil {
			return AuthoringCodeCopyPreparationDTO{}, errors.Join(safe("create private Code copy", err), safe("persist private Code copy failure", persistErr))
		}
		return AuthoringCodeCopyPreparationDTO{}, safe("create private Code copy", err)
	}
	if copyResult.Root != reserved.PrivatePath || copyResult.Baseline.Hash != reserved.ManifestHash {
		persistErr := s.store.FailAuthoringCodeCopyAttempt(s.ctx, reserved.ID, "copy_verification_failed")
		if persistErr != nil {
			return AuthoringCodeCopyPreparationDTO{}, errors.Join(sqlite.ErrPipelineConflict, safe("persist private Code copy verification failure", persistErr))
		}
		return AuthoringCodeCopyPreparationDTO{}, sqlite.ErrPipelineConflict
	}
	s.profileGate.RLock()
	latestPreference, err := s.store.GetAuthoringStageModelPreference(s.ctx, reserved.PipelineID, sdd.Code)
	var latestSelectionHash string
	if err == nil {
		latestResolved, resolveErr := s.resolveAuthoringStageModelPreference(s.ctx, latestPreference)
		if resolveErr != nil {
			err = resolveErr
		} else {
			latestSelectionHash, err = authoringCodeSelectionHash(latestResolved)
		}
	}
	if err != nil {
		persistErr := s.store.FailAuthoringCodeCopyAttempt(s.ctx, reserved.ID, "selection_recheck_failed")
		s.profileGate.RUnlock()
		if persistErr != nil {
			return AuthoringCodeCopyPreparationDTO{}, errors.Join(safe("recheck effective Code model selection", err), safe("persist Code selection recheck failure", persistErr))
		}
		return AuthoringCodeCopyPreparationDTO{}, safe("recheck effective Code model selection", err)
	}
	completed, err := s.completeVerifiedAuthoringCodeCopy(reserved, copyResult.Root, copyResult.Baseline.Hash, latestSelectionHash)
	s.profileGate.RUnlock()
	if err != nil {
		return authoringCodeCopyPreparationDTO(completed), safe("complete authoring Code copy", err)
	}
	return authoringCodeCopyPreparationDTO(completed), nil
}

func (s *Service) completeVerifiedAuthoringCodeCopy(attempt catalog.AuthoringCodeCopyAttempt, privatePath, manifestHash, selectionHash string) (catalog.AuthoringCodeCopyAttempt, error) {
	completed, err := s.store.CompleteAuthoringCodeCopyAttempt(s.ctx, attempt.ID, privatePath, manifestHash, selectionHash)
	if err == nil {
		return completed, nil
	}
	receipt, readErr := s.store.GetAuthoringCodeCopyAttempt(s.ctx, attempt.PipelineID, attempt.RequestID)
	if readErr != nil {
		return completed, errors.Join(err, safe("read authoring Code copy receipt after completion error", readErr))
	}
	if receipt.ID != attempt.ID || receipt.IntentHash != attempt.IntentHash || receipt.PrivatePath != privatePath || receipt.ManifestHash != manifestHash {
		return receipt, sqlite.ErrPipelineConflict
	}
	switch receipt.Status {
	case "prepared":
		if receipt.PreferenceSnapshot.SelectionHash == selectionHash {
			return receipt, nil
		}
	case "preparing":
		if receipt.PreferenceSnapshot.SelectionHash == selectionHash {
			retried, retryErr := s.store.CompleteAuthoringCodeCopyAttempt(s.ctx, attempt.ID, privatePath, manifestHash, selectionHash)
			if retryErr == nil {
				return retried, nil
			}
			afterRetry, retryReadErr := s.store.GetAuthoringCodeCopyAttempt(s.ctx, attempt.PipelineID, attempt.RequestID)
			if retryReadErr == nil && afterRetry.ID == attempt.ID {
				return afterRetry, retryErr
			}
			return receipt, errors.Join(retryErr, safe("read authoring Code copy receipt after retry error", retryReadErr))
		}
	}
	return receipt, err
}

func (s *Service) resumePreparingAuthoringCodeCopy(attempt catalog.AuthoringCodeCopyAttempt) (AuthoringCodeCopyPreparationDTO, error) {
	prepared := authoringCodeCopyPreparationDTO(attempt)
	if strings.TrimSpace(s.privateWorkspaceParent) == "" {
		return prepared, nil
	}
	if err := sddworkspace.VerifyPrivateCopyAt(s.ctx, attempt.SourcePath, s.privateWorkspaceParent, attempt.PrivatePath, prepared.Manifest); err != nil {
		if failErr := s.store.FailAuthoringCodeCopyAttempt(s.ctx, attempt.ID, "copy_reconciliation_failed"); failErr != nil {
			return prepared, safe("terminalize unverifiable authoring Code copy", failErr)
		}
		receipt, readErr := s.store.GetAuthoringCodeCopyAttempt(s.ctx, attempt.PipelineID, attempt.RequestID)
		if readErr != nil {
			return prepared, safe("read terminal authoring Code copy receipt", readErr)
		}
		return authoringCodeCopyPreparationDTO(receipt), nil
	}
	s.profileGate.RLock()
	preference, err := s.store.GetAuthoringStageModelPreference(s.ctx, attempt.PipelineID, sdd.Code)
	if err != nil {
		s.profileGate.RUnlock()
		return prepared, safe("read Code model preference to resume verified copy", err)
	}
	resolved, err := s.resolveAuthoringStageModelPreference(s.ctx, preference)
	if err != nil {
		s.profileGate.RUnlock()
		return prepared, err
	}
	selectionHash, err := authoringCodeSelectionHash(resolved)
	if err != nil {
		s.profileGate.RUnlock()
		return prepared, safe("fingerprint Code model selection to resume verified copy", err)
	}
	completed, err := s.completeVerifiedAuthoringCodeCopy(attempt, attempt.PrivatePath, attempt.ManifestHash, selectionHash)
	s.profileGate.RUnlock()
	if err != nil {
		return authoringCodeCopyPreparationDTO(completed), safe("resume verified authoring Code copy", err)
	}
	return authoringCodeCopyPreparationDTO(completed), nil
}

func (s *Service) GetAuthoringCodeCopyPreparations(in GetAuthoringCodeCopyPreparationsInput) (AuthoringCodeCopyPreparationsDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringCodeCopyPreparationsDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(in.PipelineID, 128) {
		return AuthoringCodeCopyPreparationsDTO{}, ErrInvalidInput
	}
	if _, err := s.loadPipeline(in.PipelineID); err != nil {
		return AuthoringCodeCopyPreparationsDTO{}, err
	}
	attempts, err := s.store.ListAuthoringCodeCopyAttempts(s.ctx, in.PipelineID)
	if err != nil {
		return AuthoringCodeCopyPreparationsDTO{}, safe("list authoring Code copy preparations", err)
	}
	out := AuthoringCodeCopyPreparationsDTO{
		PipelineID: in.PipelineID, AttemptCount: len(attempts), MaxAttemptCount: sdd.MaxAuthoringAttempts,
		Attempts: make([]AuthoringCodeCopyPreparationDTO, 0, len(attempts)),
	}
	for _, attempt := range attempts {
		out.Attempts = append(out.Attempts, authoringCodeCopyPreparationDTO(attempt))
	}
	return out, nil
}

func authoringCodeCopyPreferenceSnapshot(preference AuthoringStageModelPreferenceDTO, selectionHash string) catalog.AuthoringCodeCopyPreferenceSnapshot {
	selection := preference.Selection
	selection.SupportedReasoningEfforts = append([]string(nil), selection.SupportedReasoningEfforts...)
	return catalog.AuthoringCodeCopyPreferenceSnapshot{
		PipelineID: preference.PipelineID, Stage: preference.Stage, ModelMode: preference.ModelMode,
		EffortMode: preference.EffortMode, ExplicitEffort: preference.ExplicitEffort,
		PreferenceRevision: preference.PreferenceRevision, Resolution: preference.Resolution,
		ModelSource: preference.ModelSource, EffortSource: preference.EffortSource, InheritedFrom: preference.InheritedFrom,
		CatalogValidationRequired: preference.CatalogValidationRequired, SelectionHash: selectionHash, ErrorCode: preference.ErrorCode,
		Selection: catalog.AuthoringCodeCopySelection{
			BackendID: selection.BackendID, ModelID: selection.ModelID, ReasoningEffort: selection.ReasoningEffort,
			SupportedReasoningEfforts: selection.SupportedReasoningEfforts, CatalogRevision: selection.CatalogRevision,
			Source: selection.Source, Destination: selection.Destination, Status: selection.Status,
			ConfirmUnfiltered: selection.ConfirmUnfiltered, ConfirmJITLoad: selection.ConfirmJITLoad,
			MaxOutputTokens: selection.MaxOutputTokens,
			ContextLength:   selection.ContextLength, CheckedAt: selection.CheckedAt,
		},
	}
}

func authoringCodeCopyManifestSnapshot(manifest sddworkspace.Manifest) catalog.AuthoringCodeCopyManifest {
	out := catalog.AuthoringCodeCopyManifest{
		Version: manifest.Version, FileCount: manifest.FileCount, TotalBytes: manifest.TotalBytes,
		Hash: manifest.Hash, Entries: make([]catalog.AuthoringCodeCopyEntry, 0, len(manifest.Entries)),
		Excluded: make([]catalog.AuthoringCodeCopyExclusion, 0, len(manifest.Excluded)),
	}
	for _, entry := range manifest.Entries {
		out.Entries = append(out.Entries, catalog.AuthoringCodeCopyEntry{
			Path: entry.Path, Type: string(entry.Type), Mode: entry.Mode, Size: entry.Size, SHA256: entry.SHA256, Target: entry.Target,
		})
	}
	for _, excluded := range manifest.Excluded {
		out.Excluded = append(out.Excluded, catalog.AuthoringCodeCopyExclusion{Path: excluded.Path, Reason: excluded.Reason})
	}
	return out
}

func authoringCodeCopyPreparationDTO(attempt catalog.AuthoringCodeCopyAttempt) AuthoringCodeCopyPreparationDTO {
	preference := attempt.PreferenceSnapshot
	selection := preference.Selection
	manifest := attempt.ManifestSnapshot
	out := AuthoringCodeCopyPreparationDTO{
		ID: attempt.ID, PipelineID: attempt.PipelineID, WorkspaceID: attempt.WorkspaceID,
		RequestID: attempt.RequestID, PipelineRevision: attempt.PipelineRevision,
		CodePreferenceRevision: attempt.CodePreferenceRevision, CodeSelectionHash: preference.SelectionHash,
		SourcePath:  attempt.SourcePath,
		PrivatePath: attempt.PrivatePath, ManifestHash: attempt.ManifestHash, Status: attempt.Status,
		ErrorCode: attempt.ErrorCode, CreatedAt: attempt.CreatedAt, UpdatedAt: attempt.UpdatedAt,
		CodePreference: AuthoringStageModelPreferenceDTO{
			PipelineID: preference.PipelineID, Stage: preference.Stage, ModelMode: preference.ModelMode,
			EffortMode: preference.EffortMode, ExplicitEffort: preference.ExplicitEffort,
			PreferenceRevision: preference.PreferenceRevision, Resolution: preference.Resolution,
			ModelSource: preference.ModelSource, EffortSource: preference.EffortSource, InheritedFrom: preference.InheritedFrom,
			CatalogValidationRequired: preference.CatalogValidationRequired, ErrorCode: preference.ErrorCode,
			Selection: BrainstormSelectionDTO{
				BackendID: selection.BackendID, ModelID: selection.ModelID, ReasoningEffort: selection.ReasoningEffort,
				SupportedReasoningEfforts: append([]string(nil), selection.SupportedReasoningEfforts...),
				CatalogRevision:           selection.CatalogRevision, Source: selection.Source, Destination: selection.Destination,
				Status: selection.Status, ConfirmUnfiltered: selection.ConfirmUnfiltered,
				MaxOutputTokens: selection.MaxOutputTokens, ContextLength: selection.ContextLength, CheckedAt: selection.CheckedAt,
			},
		},
		Manifest: sddworkspace.Manifest{
			Version: manifest.Version, FileCount: manifest.FileCount, TotalBytes: manifest.TotalBytes, Hash: manifest.Hash,
			Entries:  make([]sddworkspace.Entry, 0, len(manifest.Entries)),
			Excluded: make([]sddworkspace.Exclusion, 0, len(manifest.Excluded)),
		},
	}
	for _, entry := range manifest.Entries {
		out.Manifest.Entries = append(out.Manifest.Entries, sddworkspace.Entry{
			Path: entry.Path, Type: sddworkspace.EntryType(entry.Type), Mode: entry.Mode, Size: entry.Size, SHA256: entry.SHA256, Target: entry.Target,
		})
	}
	for _, excluded := range manifest.Excluded {
		out.Manifest.Excluded = append(out.Manifest.Excluded, sddworkspace.Exclusion{Path: excluded.Path, Reason: excluded.Reason})
	}
	return out
}
