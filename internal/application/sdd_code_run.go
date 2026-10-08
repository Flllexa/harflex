package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/sddworkspace"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

const (
	authoringCodeMaxTurns        = sdd.MaxAuthoringCodeTurns
	authoringCodeMaxToolCalls    = sdd.MaxAuthoringCodeToolCalls
	authoringCodeMaxOutputTokens = sdd.MaxAuthoringCodeOutputTokens
	authoringCodeMaxPromptBytes  = maxPromptBytes
	authoringCodePersistTimeout  = 10 * time.Second
	authoringCodeJournalTimeout  = 2 * time.Minute
)

var errAuthoringCodeUsageOverflow = errors.New("authoring Code usage exceeds the numeric token range")

type StartAuthoringCodeInput struct {
	PipelineID                     string `json:"pipelineId"`
	PreparationID                  string `json:"preparationId"`
	PreparationRequestID           string `json:"preparationRequestId"`
	RequestID                      string `json:"requestId"`
	ExpectedPipelineRevision       int64  `json:"expectedPipelineRevision"`
	ExpectedPlanStageRevision      int64  `json:"expectedPlanStageRevision"`
	ExpectedPlanArtifactVersion    int64  `json:"expectedPlanArtifactVersion"`
	ExpectedCodePreferenceRevision int64  `json:"expectedCodePreferenceRevision"`
	ExpectedCodeSelectionHash      string `json:"expectedCodeSelectionHash"`
	ExpectedManifestHash           string `json:"expectedManifestHash"`
}

type GetAuthoringCodeRunsInput struct {
	PipelineID string `json:"pipelineId"`
}

type CancelAuthoringCodeInput struct {
	PipelineID string `json:"pipelineId"`
	AttemptID  string `json:"attemptId"`
}

type AuthoringCodeRunLimitsDTO struct {
	MaxPromptBytes         int   `json:"maxPromptBytes"`
	MaxOutputTokens        int   `json:"maxOutputTokens"`
	MaxOutputTokensPerTurn int   `json:"maxOutputTokensPerTurn"`
	MaxTurns               int   `json:"maxTurns"`
	MaxToolCalls           int   `json:"maxToolCalls"`
	TimeoutMillis          int64 `json:"timeoutMillis"`
}

type AuthoringCodeRunDTO struct {
	ID                     string                           `json:"id"`
	PipelineID             string                           `json:"pipelineId"`
	PreparationID          string                           `json:"preparationId"`
	PreparationRequestID   string                           `json:"preparationRequestId"`
	RequestID              string                           `json:"requestId"`
	PipelineRevision       int64                            `json:"pipelineRevision"`
	PlanStageRevision      int64                            `json:"planStageRevision"`
	PlanArtifactVersion    int64                            `json:"planArtifactVersion"`
	CodePreferenceRevision int64                            `json:"codePreferenceRevision"`
	CodeSelectionHash      string                           `json:"codeSelectionHash"`
	ManifestHash           string                           `json:"manifestHash"`
	PlanSourceHash         string                           `json:"planSourceHash"`
	PrivatePath            string                           `json:"privatePath"`
	Preference             AuthoringStageModelPreferenceDTO `json:"preference"`
	Limits                 AuthoringCodeRunLimitsDTO        `json:"limits"`
	SessionID              string                           `json:"sessionId"`
	Status                 string                           `json:"status"`
	ErrorCode              string                           `json:"errorCode,omitempty"`
	CancellationPending    bool                             `json:"cancellationPending"`
	Usage                  *catalog.BrainstormUsage         `json:"usage"`
	Baseline               sddworkspace.Manifest            `json:"baseline"`
	Result                 *sddworkspace.Manifest           `json:"result,omitempty"`
	SourceManifest         *sddworkspace.Manifest           `json:"sourceManifest,omitempty"`
	SourceDrift            bool                             `json:"sourceDrift"`
	SourceReadbackStatus   string                           `json:"sourceReadbackStatus"`
	Changes                []sddworkspace.Change            `json:"changes"`
	PatchHash              string                           `json:"patchHash"`
	CreatedAt              time.Time                        `json:"createdAt"`
	UpdatedAt              time.Time                        `json:"updatedAt"`
}

type AuthoringCodeRunsDTO struct {
	PipelineID string                `json:"pipelineId"`
	Runs       []AuthoringCodeRunDTO `json:"runs"`
}

type authoringCodeRunIntent struct {
	PipelineID                     string `json:"pipelineId"`
	PreparationID                  string `json:"preparationId"`
	PreparationRequestID           string `json:"preparationRequestId"`
	ExpectedPipelineRevision       int64  `json:"expectedPipelineRevision"`
	ExpectedPlanStageRevision      int64  `json:"expectedPlanStageRevision"`
	ExpectedPlanArtifactVersion    int64  `json:"expectedPlanArtifactVersion"`
	ExpectedCodePreferenceRevision int64  `json:"expectedCodePreferenceRevision"`
	ExpectedCodeSelectionHash      string `json:"expectedCodeSelectionHash"`
	ExpectedManifestHash           string `json:"expectedManifestHash"`
}

type authoringCodeOwner struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	runner *selectedAPIRunner
}

func (o *authoringCodeOwner) setRunner(runner *selectedAPIRunner) {
	o.mu.Lock()
	o.runner = runner
	o.mu.Unlock()
}

func (o *authoringCodeOwner) abort(ctx context.Context) error {
	o.cancel()
	o.mu.Lock()
	runner := o.runner
	o.mu.Unlock()
	if runner == nil {
		return nil
	}
	return runner.Abort(ctx)
}

func authoringCodeRunIntentHash(in StartAuthoringCodeInput) (string, error) {
	data, err := json.Marshal(authoringCodeRunIntent{
		PipelineID: in.PipelineID, PreparationID: in.PreparationID,
		PreparationRequestID: in.PreparationRequestID, ExpectedPipelineRevision: in.ExpectedPipelineRevision,
		ExpectedPlanStageRevision: in.ExpectedPlanStageRevision, ExpectedPlanArtifactVersion: in.ExpectedPlanArtifactVersion,
		ExpectedCodePreferenceRevision: in.ExpectedCodePreferenceRevision, ExpectedCodeSelectionHash: in.ExpectedCodeSelectionHash,
		ExpectedManifestHash: in.ExpectedManifestHash,
	})
	if err != nil {
		return "", fmt.Errorf("encode authoring Code run intent: %w", err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func validAuthoringCodeRunInput(in StartAuthoringCodeInput) bool {
	return validSelectionText(in.PipelineID, 128) && validSelectionText(in.PreparationID, 128) &&
		authoringRequestID.MatchString(in.PreparationRequestID) && authoringRequestID.MatchString(in.RequestID) &&
		in.ExpectedPipelineRevision > 0 && in.ExpectedPlanStageRevision >= 0 && in.ExpectedPlanArtifactVersion >= 0 &&
		in.ExpectedCodePreferenceRevision >= 0 && authoringCodeManifestHash.MatchString(in.ExpectedCodeSelectionHash) &&
		authoringCodeManifestHash.MatchString(in.ExpectedManifestHash)
}

func (s *Service) StartAuthoringCode(in StartAuthoringCodeInput) (AuthoringCodeRunDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringCodeRunDTO{}, err
	}
	defer s.endCall()
	s.recoveryGate.RLock()
	defer s.recoveryGate.RUnlock()
	if !validAuthoringCodeRunInput(in) {
		return AuthoringCodeRunDTO{}, ErrInvalidInput
	}
	intentHash, err := authoringCodeRunIntentHash(in)
	if err != nil {
		return AuthoringCodeRunDTO{}, err
	}
	if existing, err := s.store.GetAuthoringCodeRun(s.ctx, in.PipelineID, in.RequestID); err == nil {
		if existing.IntentHash != intentHash {
			return AuthoringCodeRunDTO{}, sqlite.ErrPipelineConflict
		}
		return authoringCodeRunDTO(existing), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return AuthoringCodeRunDTO{}, safe("read authoring Code request", err)
	}

	prepared, err := s.store.GetAuthoringCodeCopyAttempt(s.ctx, in.PipelineID, in.PreparationRequestID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (prepared.ID != in.PreparationID || prepared.Status != "prepared")) {
		return AuthoringCodeRunDTO{}, ErrInvalidInput
	}
	if err != nil {
		return AuthoringCodeRunDTO{}, safe("read confirmed private Code preparation", err)
	}
	if prepared.PipelineRevision != in.ExpectedPipelineRevision || prepared.CodePreferenceRevision != in.ExpectedCodePreferenceRevision ||
		prepared.PreferenceSnapshot.SelectionHash != in.ExpectedCodeSelectionHash || prepared.ManifestHash != in.ExpectedManifestHash {
		return AuthoringCodeRunDTO{}, sqlite.ErrPipelineConflict
	}

	pipeline, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return AuthoringCodeRunDTO{}, err
	}
	if pipeline.Revision != in.ExpectedPipelineRevision || !validAuthoringCodePreflightStage(pipeline) {
		return AuthoringCodeRunDTO{}, sqlite.ErrPipelineConflict
	}
	plan, err := s.store.GetAuthoringStage(s.ctx, pipeline.ID, sdd.Plan)
	if err != nil {
		return AuthoringCodeRunDTO{}, safe("read current approved Plan", err)
	}
	if plan.PipelineRevision != pipeline.Revision || plan.Revision != in.ExpectedPlanStageRevision || plan.ArtifactVersion != in.ExpectedPlanArtifactVersion ||
		(pipeline.Status[sdd.Plan] == sdd.Completed && plan.State != "approved") || (pipeline.Status[sdd.Plan] == sdd.Skipped && plan.State != "skipped") {
		return AuthoringCodeRunDTO{}, sqlite.ErrPipelineConflict
	}
	source, err := s.authoringCodePromptSource(pipeline, plan)
	if err != nil {
		return AuthoringCodeRunDTO{}, err
	}
	prompt := "You are the Harflex Code agent. Implement the exact approved Plan or the explicitly authorized upstream sources below. Work only through the available read, write, and edit file tools in this private copy. Do not use shell, processes, MCP, or general network access. Do not apply changes to the original project. Preserve user intent and report concise completion details.\n\n" + source
	if len(prompt) > authoringCodeMaxPromptBytes || !utf8.ValidString(prompt) {
		return AuthoringCodeRunDTO{}, ErrInvalidInput
	}
	planHash := sha256.Sum256([]byte(source))
	planSourceHash := hex.EncodeToString(planHash[:])
	if s.privateWorkspaceParent == "" {
		return AuthoringCodeRunDTO{}, ErrInvalidInput
	}
	workspace, err := s.store.GetWorkspace(s.ctx, pipeline.WorkspaceID)
	if err != nil || workspace.Path != prepared.SourcePath {
		return AuthoringCodeRunDTO{}, ErrBackendChanged
	}
	baseline := authoringCodeCopyManifest(prepared.ManifestSnapshot)
	if err := sddworkspace.VerifyPrivateCopyAt(s.ctx, prepared.SourcePath, s.privateWorkspaceParent, prepared.PrivatePath, baseline); err != nil {
		return AuthoringCodeRunDTO{}, safe("verify confirmed private Code baseline", err)
	}

	preference, err := s.store.GetAuthoringStageModelPreference(s.ctx, pipeline.ID, sdd.Code)
	if err != nil {
		return AuthoringCodeRunDTO{}, safe("read current Code model preference", err)
	}
	if preference.Revision != in.ExpectedCodePreferenceRevision {
		return AuthoringCodeRunDTO{}, sqlite.ErrPipelineConflict
	}
	resolved, err := s.resolveAuthoringStageModelPreferenceWithCatalog(s.ctx, preference, true)
	if err != nil {
		return AuthoringCodeRunDTO{}, err
	}
	if resolved.DTO.Resolution != "ready" || resolved.DTO.CatalogValidationRequired || !trustedAuthoringPreferenceSource(preference, resolved.DTO.ModelSource) ||
		!authoringCodePreferenceMatches(prepared.PreferenceSnapshot, resolved.DTO, resolved.Selection) {
		return AuthoringCodeRunDTO{}, authoringPreferenceExecutionError(resolved.DTO)
	}
	selection := resolved.Selection
	selection.MaxOutputTokens = sdd.MaxAuthoringOutputTokens
	if _, external := s.external[selection.BackendID]; external {
		return AuthoringCodeRunDTO{}, ErrBackendChanged
	}
	profile, err := s.store.GetProviderProfile(s.ctx, selection.BackendID)
	profileBase, validProfileBase := parseProfileURL(profile.BaseURL)
	if err != nil || profile.Kind != "openai_compatible" || profileRevision(profile) != prepared.PreferenceSnapshot.Selection.CatalogRevision ||
		!validProfileBase || canonicalProfileOrigin(profileBase) != prepared.PreferenceSnapshot.Selection.Destination {
		return AuthoringCodeRunDTO{}, ErrBackendChanged
	}
	if err := profileNetworkAccess(profile); err != nil {
		return AuthoringCodeRunDTO{}, err
	}

	attempt := catalog.AuthoringCodeRun{
		ID: "authoring_code_run_" + id.New(), PipelineID: pipeline.ID, PreparationID: prepared.ID,
		PreparationRequestID: prepared.RequestID, RequestID: in.RequestID, IntentHash: intentHash,
		WorkspaceID: pipeline.WorkspaceID, PipelineRevision: pipeline.Revision, PlanStageRevision: plan.Revision,
		PlanArtifactVersion: plan.ArtifactVersion, CodePreferenceRevision: preference.Revision,
		CodeSelectionHash: prepared.PreferenceSnapshot.SelectionHash, ManifestHash: prepared.ManifestHash,
		PlanSourceHash: planSourceHash, PrivatePath: prepared.PrivatePath, PreferenceSnapshot: prepared.PreferenceSnapshot,
		ManifestSnapshot: prepared.ManifestSnapshot, MaxPromptBytes: authoringCodeMaxPromptBytes,
		MaxOutputTokens: authoringCodeMaxOutputTokens, MaxOutputTokensPerTurn: selection.MaxOutputTokens, MaxTurns: authoringCodeMaxTurns,
		MaxToolCalls: authoringCodeMaxToolCalls, TimeoutMillis: sdd.AuthoringAttemptTimeout.Milliseconds(), Status: "running",
	}

	s.authoringAdmissionGate.Lock()
	s.mu.RLock()
	closing := s.closing
	s.mu.RUnlock()
	if closing {
		s.authoringAdmissionGate.Unlock()
		return AuthoringCodeRunDTO{}, context.Canceled
	}
	s.profileGate.RLock()
	currentProfile, profileErr := s.store.GetProviderProfile(s.ctx, selection.BackendID)
	if profileErr != nil || profileRevision(currentProfile) != profileRevision(profile) || currentProfile.BaseURL != profile.BaseURL || currentProfile.Model != profile.Model {
		s.profileGate.RUnlock()
		s.authoringAdmissionGate.Unlock()
		return AuthoringCodeRunDTO{}, ErrBackendChanged
	}
	reserved, created, err := s.store.BeginAuthoringCodeRun(s.ctx, attempt)
	if err != nil {
		s.profileGate.RUnlock()
		s.authoringAdmissionGate.Unlock()
		return AuthoringCodeRunDTO{}, safe("admit authoring Code run", err)
	}
	if !created {
		s.profileGate.RUnlock()
		s.authoringAdmissionGate.Unlock()
		if reserved.IntentHash != intentHash {
			return AuthoringCodeRunDTO{}, sqlite.ErrPipelineConflict
		}
		return authoringCodeRunDTO(reserved), nil
	}
	timeout := time.Duration(reserved.TimeoutMillis) * time.Millisecond
	runCtx, cancel := context.WithTimeout(s.ctx, timeout)
	owner := &authoringCodeOwner{cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.authoringCodeOwners[reserved.ID] = owner
	s.mu.Unlock()
	s.profileGate.RUnlock()
	s.authoringAdmissionGate.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.authoringCodeOwners, reserved.ID)
		s.mu.Unlock()
		close(owner.done)
	}()

	finishBeforeRun := func(cause error, code string) (AuthoringCodeRunDTO, error) {
		status := "failed"
		if errors.Is(runCtx.Err(), context.Canceled) || errors.Is(cause, context.Canceled) {
			status, code = "cancelled", "cancelled"
		} else if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(cause, context.DeadlineExceeded) {
			status, code = "failed", "timeout"
		}
		settled, settleErr := s.finishAuthoringCodeRun(reserved.ID, status, code, nil, nil, nil, []catalog.CodeFileChange{}, nil, "")
		if settleErr != nil {
			return AuthoringCodeRunDTO{}, errors.Join(safe("persist authoring Code failure", cause), safe("read authoring Code failure", settleErr))
		}
		if cause == nil || status == "cancelled" {
			return authoringCodeRunDTO(settled), nil
		}
		return authoringCodeRunDTO(settled), safe("execute authoring Code", cause)
	}

	currentRun, err := s.store.GetAuthoringCodeRunByID(runCtx, reserved.ID)
	if err != nil {
		return finishBeforeRun(err, "result_unavailable")
	}
	if currentRun.Status != "running" {
		return finishBeforeRun(context.Canceled, "cancelled")
	}
	originManifest, originErr := s.authoringCodeScan(runCtx, prepared.SourcePath)
	if originErr != nil {
		return finishBeforeRun(originErr, "source_readback_failed")
	}
	if originManifest.Hash != reserved.ManifestHash {
		sourceSnapshot := authoringCodeCopyManifestSnapshot(originManifest)
		settled, settleErr := s.finishAuthoringCodeRun(reserved.ID, "failed", "snapshot_stale", nil, nil, &sourceSnapshot, []catalog.CodeFileChange{}, nil, "")
		if settleErr != nil {
			return AuthoringCodeRunDTO{}, safe("persist authoring Code source drift", settleErr)
		}
		return authoringCodeRunDTO(settled), safe("execute authoring Code", errors.New("snapshot_stale"))
	}
	selection.SessionID = "session_" + id.New()
	selection.WorkspacePath = prepared.PrivatePath
	record := catalog.SessionRecord{
		ID: selection.SessionID, WorkspaceID: workspace.ID, BackendID: selection.BackendID,
		BackendRevision: profileRevision(profile), Mode: catalog.AuthoringCodeSessionMode, Status: "ready",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateAuthoringCodeSession(runCtx, reserved.ID, record, selection); err != nil {
		return finishBeforeRun(err, "composition_failed")
	}
	codeWorkspace := workspace
	codeWorkspace.Path = prepared.PrivatePath
	runner, journal, err := s.makeRunner(&record, codeWorkspace, restoredHistory{}, false, nil, "", &selection)
	if err != nil {
		return finishBeforeRun(err, "composition_failed")
	}
	api, ok := runner.(*selectedAPIRunner)
	if !ok {
		journal.Close()
		return finishBeforeRun(ErrBackendChanged, "composition_failed")
	}
	core, ok := api.base.(*agentcore.Session)
	if !ok {
		journal.Close()
		return finishBeforeRun(ErrBackendChanged, "composition_failed")
	}
	if err := core.SetRunLimits(reserved.MaxTurns, int64(reserved.MaxOutputTokens), reserved.MaxToolCalls); err != nil {
		journal.Close()
		return finishBeforeRun(err, "composition_failed")
	}
	api.validateAttempt = func(ctx context.Context) error {
		return s.validateAuthoringCodeRunSnapshot(ctx, reserved, prepared, plan, preference)
	}
	owner.setRunner(api)
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		journal.Close()
		return finishBeforeRun(context.Canceled, "cancelled")
	}
	s.sessions[record.ID], s.journals[record.ID] = runner, journal
	s.mu.Unlock()
	if err := runCtx.Err(); err != nil {
		return finishBeforeRun(err, "cancelled")
	}

	runErr := api.Prompt(runCtx, prompt)
	result, resultErr := s.sessionResult(record.ID, "authoring Code run", runErr)
	journalCtx, journalCancel := context.WithTimeout(context.WithoutCancel(s.ctx), authoringCodeJournalTimeout)
	usage, terminal, journalErr := s.readAuthoringCodeJournal(journalCtx, record.ID)
	journalCancel()
	scanTimeout := s.authoringCodeReadbackTimeout
	if scanTimeout <= 0 {
		scanTimeout = 15 * time.Minute
	}
	privateScanCtx, privateScanCancel := context.WithTimeout(context.WithoutCancel(s.ctx), scanTimeout)
	resultManifest, scanErr := s.authoringCodeScan(privateScanCtx, prepared.PrivatePath)
	privateScanCancel()
	sourceScanCtx, sourceScanCancel := context.WithTimeout(context.WithoutCancel(s.ctx), scanTimeout)
	sourceManifest, sourceErr := s.authoringCodeScan(sourceScanCtx, prepared.SourcePath)
	sourceScanCancel()
	var changes []catalog.CodeFileChange
	if scanErr == nil {
		for _, change := range sddworkspace.Compare(baseline, resultManifest) {
			changes = append(changes, catalog.CodeFileChange{Path: change.Path, Kind: string(change.Kind)})
		}
	} else {
		changes = []catalog.CodeFileChange{}
	}
	var resultSnapshot *catalog.AuthoringCodeCopyManifest
	if scanErr == nil {
		snapshot := authoringCodeCopyManifestSnapshot(resultManifest)
		resultSnapshot = &snapshot
	}
	var sourceSnapshot *catalog.AuthoringCodeCopyManifest
	if sourceErr == nil {
		snapshot := authoringCodeCopyManifestSnapshot(sourceManifest)
		sourceSnapshot = &snapshot
	}
	status, errorCode := authoringCodeTerminalStatus(terminal, runCtx.Err(), runErr, result)
	if sourceErr == nil && sourceManifest.Hash != prepared.ManifestHash {
		status, errorCode = "failed", "snapshot_stale"
	} else if errors.Is(journalErr, errAuthoringCodeUsageOverflow) {
		status, errorCode = "failed", "usage_unavailable"
	} else if journalErr != nil {
		status, errorCode = "failed", "journal_failed"
	} else if resultErr != nil {
		status, errorCode = "failed", "result_unavailable"
	} else if scanErr != nil {
		status, errorCode = "failed", "result_unavailable"
	} else if sourceErr != nil {
		status, errorCode = "failed", "source_readback_failed"
	}
	var patchJSON []byte
	var patchHash string
	if status == "completed" {
		if resultSnapshot == nil || sourceSnapshot == nil || sourceManifest.Hash != baseline.Hash || resultManifest.Hash == "" {
			status, errorCode = "failed", "result_unavailable"
		} else {
			patchCtx, patchCancel := context.WithTimeout(context.WithoutCancel(s.ctx), scanTimeout)
			_, patchJSON, patchHash, scanErr = sddworkspace.BuildPatchWithLimit(patchCtx, prepared.SourcePath, prepared.PrivatePath, baseline, sourceManifest, resultManifest, s.authoringCodePatchMaxBytes)
			patchCancel()
			switch {
			case scanErr == nil:
			case errors.Is(scanErr, sddworkspace.ErrPatchTooLarge):
				status, errorCode = "failed", "patch_too_large"
			case errors.Is(scanErr, sddworkspace.ErrPatchSourceDrift):
				status, errorCode = "failed", "snapshot_stale"
				sourceSnapshot = nil
			case errors.Is(scanErr, sddworkspace.ErrPatchResultDrift):
				status, errorCode = "failed", "result_unavailable"
				resultSnapshot = nil
				changes = []catalog.CodeFileChange{}
			default:
				status, errorCode = "failed", "result_unavailable"
				resultSnapshot = nil
				changes = []catalog.CodeFileChange{}
			}
		}
	}
	settled, settleErr := s.finishAuthoringCodeRun(reserved.ID, status, errorCode, usage, resultSnapshot, sourceSnapshot, changes, patchJSON, patchHash)
	if settleErr != nil {
		return AuthoringCodeRunDTO{}, safe("persist authoring Code result", settleErr)
	}
	if status != "completed" {
		return authoringCodeRunDTO(settled), safe("execute authoring Code", errors.New(errorCode))
	}
	return authoringCodeRunDTO(settled), nil
}

func (s *Service) finishAuthoringCodeRun(id, status, errorCode string, usage *catalog.BrainstormUsage, result, source *catalog.AuthoringCodeCopyManifest, changes []catalog.CodeFileChange, patchJSON []byte, patchHash string) (catalog.AuthoringCodeRun, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), authoringCodePersistTimeout)
	defer cancel()
	return s.store.FinishAuthoringCodeRun(ctx, id, status, errorCode, usage, result, source, changes, patchJSON, patchHash)
}

func (s *Service) validateAuthoringCodeRunSnapshot(ctx context.Context, expected catalog.AuthoringCodeRun, prepared catalog.AuthoringCodeCopyAttempt, expectedPlan catalog.AuthoringStageRun, expectedPreference catalog.AuthoringStageModelPreference) error {
	current, err := s.store.GetAuthoringCodeRunByID(ctx, expected.ID)
	if err != nil || current.Status != "running" || current.IntentHash != expected.IntentHash || current.PreparationID != expected.PreparationID {
		return sqlite.ErrPipelineConflict
	}
	copy, err := s.store.GetAuthoringCodeCopyAttempt(ctx, prepared.PipelineID, prepared.RequestID)
	if err != nil || copy.ID != prepared.ID || copy.Status != "prepared" || copy.ManifestHash != expected.ManifestHash || copy.PrivatePath != expected.PrivatePath {
		return sqlite.ErrPipelineConflict
	}
	pipeline, err := s.store.GetPipeline(ctx, expected.PipelineID)
	if err != nil || pipeline.Revision != expected.PipelineRevision || !validAuthoringCodePreflightStage(pipeline) {
		return sqlite.ErrPipelineConflict
	}
	plan, err := s.store.GetAuthoringStage(ctx, expected.PipelineID, sdd.Plan)
	if err != nil || plan.Revision != expectedPlan.Revision || plan.ArtifactVersion != expectedPlan.ArtifactVersion || plan.State != expectedPlan.State {
		return sqlite.ErrPipelineConflict
	}
	preference, err := s.store.GetAuthoringStageModelPreference(ctx, expected.PipelineID, sdd.Code)
	if err != nil || preference.Revision != expectedPreference.Revision || preference.ModelMode != expectedPreference.ModelMode ||
		preference.EffortMode != expectedPreference.EffortMode || preference.ExplicitEffort != expectedPreference.ExplicitEffort {
		return sqlite.ErrPipelineConflict
	}
	if s.privateWorkspaceParent == "" {
		return ErrInvalidInput
	}
	if err := sddworkspace.VerifyPrivateCopyAt(ctx, prepared.SourcePath, s.privateWorkspaceParent, prepared.PrivatePath, authoringCodeCopyManifest(prepared.ManifestSnapshot)); err != nil {
		return safe("verify private Code baseline before inference", err)
	}
	return nil
}

func authoringCodeTerminalStatus(terminal string, contextErr, runErr error, result RunResultDTO) (string, string) {
	if terminal != "run.completed" && (errors.Is(contextErr, context.DeadlineExceeded) || errors.Is(runErr, context.DeadlineExceeded)) {
		return "failed", "timeout"
	}
	switch terminal {
	case "run.completed":
		return "completed", ""
	case "run.cancelled":
		return "cancelled", "cancelled"
	case "run.failed":
		if errors.Is(contextErr, context.DeadlineExceeded) || errors.Is(runErr, context.DeadlineExceeded) {
			return "failed", "timeout"
		}
		if result.Reason == "output_limit_exceeded" {
			return "failed", "output_limit_exceeded"
		}
		if result.Reason == "turn_limit" {
			return "failed", "turn_limit"
		}
		if result.Reason == "tool_limit_exceeded" {
			return "failed", "tool_limit_exceeded"
		}
		if result.Reason == "tool_failed" || result.Reason == "unknown_tool" || result.Reason == "policy_denied" {
			return "failed", "tool_failed"
		}
		return "failed", "provider_failed"
	default:
		if errors.Is(contextErr, context.DeadlineExceeded) || errors.Is(runErr, context.DeadlineExceeded) {
			return "failed", "timeout"
		}
		if errors.Is(contextErr, context.Canceled) || errors.Is(runErr, context.Canceled) {
			return "cancelled", "cancelled"
		}
		return "failed", "result_unavailable"
	}
}

func (s *Service) readAuthoringCodeJournal(ctx context.Context, sessionID string) (*catalog.BrainstormUsage, string, error) {
	const maxTokenCount = int64(1<<63 - 1)
	var usage catalog.BrainstormUsage
	var haveUsage bool
	var terminal string
	err := s.walkEvents(ctx, sessionID, func(event events.Event) error {
		terminal = event.Type
		if event.Type != "usage.recorded" {
			return nil
		}
		var update agentcore.Usage
		if err := json.Unmarshal(event.Data, &update); err != nil || update.InputTokens < 0 || update.OutputTokens < 0 {
			return ErrSessionCorrupt
		}
		if update.InputTokens > maxTokenCount-usage.InputTokens || update.OutputTokens > maxTokenCount-usage.OutputTokens {
			return errAuthoringCodeUsageOverflow
		}
		usage.InputTokens += update.InputTokens
		usage.OutputTokens += update.OutputTokens
		haveUsage = true
		return nil
	})
	if err != nil {
		return nil, terminal, err
	}
	if !haveUsage {
		return nil, terminal, nil
	}
	// The configured provider does not expose a trustworthy price in its usage
	// contract. Nil cost is deliberately distinct from USD 0.
	usage.CostUSD = nil
	return &usage, terminal, nil
}

func (s *Service) CancelAuthoringCode(in CancelAuthoringCodeInput) (AuthoringCodeRunDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringCodeRunDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(in.PipelineID, 128) || !validSelectionText(in.AttemptID, 128) {
		return AuthoringCodeRunDTO{}, ErrInvalidInput
	}
	attempt, err := s.store.GetAuthoringCodeRunByID(s.ctx, in.AttemptID)
	if err != nil || attempt.PipelineID != in.PipelineID {
		return AuthoringCodeRunDTO{}, ErrInvalidInput
	}
	if attempt.Status == "completed" || attempt.Status == "failed" || attempt.Status == "cancelled" || attempt.Status == "interrupted" {
		return authoringCodeRunDTO(attempt), nil
	}
	attempt, err = s.store.RequestAuthoringCodeRunCancel(s.ctx, in.AttemptID)
	if err != nil {
		return authoringCodeRunDTO(attempt), safe("request authoring Code cancellation", err)
	}
	s.mu.RLock()
	owner := s.authoringCodeOwners[in.AttemptID]
	s.mu.RUnlock()
	if owner == nil {
		return authoringCodeRunDTO(attempt), sdd.ErrAuthoringCancellationPending
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	abortErr := owner.abort(ctx)
	select {
	case <-owner.done:
	case <-ctx.Done():
		return authoringCodeRunDTO(attempt), sdd.ErrAuthoringCancellationPending
	}
	readCtx, readCancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer readCancel()
	settled, err := s.store.GetAuthoringCodeRunByID(readCtx, in.AttemptID)
	if err != nil {
		return authoringCodeRunDTO(attempt), safe("read authoring Code cancellation", errors.Join(abortErr, err))
	}
	if settled.Status == "cancelling" || settled.Status == "running" {
		return authoringCodeRunDTO(settled), sdd.ErrAuthoringCancellationPending
	}
	return authoringCodeRunDTO(settled), nil
}

func (s *Service) GetAuthoringCodeRuns(in GetAuthoringCodeRunsInput) (AuthoringCodeRunsDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringCodeRunsDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(in.PipelineID, 128) {
		return AuthoringCodeRunsDTO{}, ErrInvalidInput
	}
	if _, err := s.loadPipeline(in.PipelineID); err != nil {
		return AuthoringCodeRunsDTO{}, err
	}
	runs, err := s.store.ListAuthoringCodeRuns(s.ctx, in.PipelineID)
	if err != nil {
		return AuthoringCodeRunsDTO{}, safe("list authoring Code runs", err)
	}
	out := AuthoringCodeRunsDTO{PipelineID: in.PipelineID, Runs: make([]AuthoringCodeRunDTO, 0, len(runs))}
	for _, run := range runs {
		out.Runs = append(out.Runs, authoringCodeRunDTO(run))
	}
	return out, nil
}

func (s *Service) authoringCodePromptSource(pipeline catalog.PipelineRun, plan catalog.AuthoringStageRun) (string, error) {
	if pipeline.Status[sdd.Plan] == sdd.Completed && plan.State == "approved" {
		for _, artifact := range plan.Artifacts {
			if artifact.Version == plan.ArtifactVersion && artifact.Status == "approved" && artifact.Author == "ai" && json.Valid(artifact.Content) {
				return "Approved Plan version " + fmt.Sprint(artifact.Version) + ":\n" + string(artifact.Content), nil
			}
		}
		return "", sqlite.ErrPipelineConflict
	}
	if pipeline.Status[sdd.Plan] != sdd.Skipped || plan.State != "skipped" {
		return "", sqlite.ErrPipelineConflict
	}
	var planReason string
	for _, action := range plan.Actions {
		if action.Action == "skip" && action.Actor == "local_user" {
			planReason = action.Reason
		}
	}
	if planReason == "" {
		return "", sqlite.ErrPipelineConflict
	}
	discovery := pipeline.Artifacts[sdd.Discovery]
	if discovery.Author != "user" || discovery.Version != pipeline.DiscoveryFrozenVersion || strings.TrimSpace(discovery.Content) == "" {
		return "", sqlite.ErrPipelineConflict
	}
	sources := map[string]any{"planBypassReason": planReason, "discoveryVersion": discovery.Version, "discovery": discovery.Content}
	specRun, err := s.store.GetAuthoringStage(s.ctx, pipeline.ID, sdd.Spec)
	if err != nil {
		return "", err
	}
	if pipeline.Status[sdd.Spec] == sdd.Completed && specRun.State == "approved" {
		for _, artifact := range specRun.Artifacts {
			if artifact.Version == specRun.ArtifactVersion && artifact.Status == "approved" && artifact.Author == "ai" && json.Valid(artifact.Content) {
				sources["approvedSpec"] = json.RawMessage(artifact.Content)
				break
			}
		}
	} else if pipeline.Status[sdd.Spec] == sdd.Skipped && specRun.State == "skipped" {
		for _, action := range specRun.Actions {
			if action.Action == "skip" && action.Actor == "local_user" {
				sources["specBypassReason"] = action.Reason
			}
		}
	} else {
		return "", sqlite.ErrPipelineConflict
	}
	if brain, brainErr := s.store.GetBrainstormingByPipeline(s.ctx, pipeline.ID, pipeline.DiscoveryFrozenVersion); brainErr == nil {
		if brain.State != "approved" {
			return "", sqlite.ErrPipelineConflict
		}
		for _, synthesis := range brain.Syntheses {
			if synthesis.Status == "approved" {
				sources["approvedBrainstormSynthesis"] = synthesis.Content
			}
		}
	} else if !errors.Is(brainErr, sql.ErrNoRows) {
		return "", brainErr
	}
	data, err := json.Marshal(sources)
	if err != nil {
		return "", err
	}
	return "Plan was explicitly skipped by the user. Use only the approved or user-authored upstream sources below:\n" + string(data), nil
}

func authoringCodePreferenceMatches(snapshot catalog.AuthoringCodeCopyPreferenceSnapshot, current AuthoringStageModelPreferenceDTO, selection catalog.ModelSelection) bool {
	stored := snapshot.Selection
	return current.PipelineID == snapshot.PipelineID && current.Stage == snapshot.Stage &&
		current.PreferenceRevision == snapshot.PreferenceRevision && current.ModelMode == snapshot.ModelMode &&
		current.EffortMode == snapshot.EffortMode && current.ExplicitEffort == snapshot.ExplicitEffort &&
		current.Resolution == "ready" && current.ModelSource == snapshot.ModelSource && current.EffortSource == snapshot.EffortSource &&
		current.InheritedFrom == snapshot.InheritedFrom && !current.CatalogValidationRequired &&
		selection.BackendID == stored.BackendID && selection.ModelID == stored.ModelID &&
		selection.ReasoningEffort == stored.ReasoningEffort && slices.Equal(selection.SupportedReasoningEfforts, stored.SupportedReasoningEfforts) &&
		selection.CatalogRevision == stored.CatalogRevision && selection.Source == stored.Source && selection.Destination == stored.Destination &&
		selection.Status == stored.Status && selection.ConfirmUnfiltered == stored.ConfirmUnfiltered && !selection.ConfirmJITLoad &&
		selection.MaxOutputTokens == stored.MaxOutputTokens && selection.ContextLength == stored.ContextLength &&
		!selection.CheckedAt.Before(stored.CheckedAt)
}

func authoringCodeCopyManifest(manifest catalog.AuthoringCodeCopyManifest) sddworkspace.Manifest {
	out := sddworkspace.Manifest{Version: manifest.Version, FileCount: manifest.FileCount, TotalBytes: manifest.TotalBytes, Hash: manifest.Hash, Entries: make([]sddworkspace.Entry, 0, len(manifest.Entries)), Excluded: make([]sddworkspace.Exclusion, 0, len(manifest.Excluded))}
	for _, entry := range manifest.Entries {
		out.Entries = append(out.Entries, sddworkspace.Entry{Path: entry.Path, Type: sddworkspace.EntryType(entry.Type), Mode: entry.Mode, Size: entry.Size, SHA256: entry.SHA256, Target: entry.Target})
	}
	for _, excluded := range manifest.Excluded {
		out.Excluded = append(out.Excluded, sddworkspace.Exclusion{Path: excluded.Path, Reason: excluded.Reason})
	}
	return out
}

func authoringCodeRunDTO(run catalog.AuthoringCodeRun) AuthoringCodeRunDTO {
	pref := run.PreferenceSnapshot
	selection := pref.Selection
	preference := AuthoringStageModelPreferenceDTO{
		PipelineID: pref.PipelineID, Stage: pref.Stage, ModelMode: pref.ModelMode, EffortMode: pref.EffortMode,
		ExplicitEffort: pref.ExplicitEffort, PreferenceRevision: pref.PreferenceRevision, Resolution: pref.Resolution,
		ModelSource: pref.ModelSource, EffortSource: pref.EffortSource, InheritedFrom: pref.InheritedFrom,
		CatalogValidationRequired: pref.CatalogValidationRequired, ErrorCode: pref.ErrorCode,
		Selection: BrainstormSelectionDTO{
			BackendID: selection.BackendID, ModelID: selection.ModelID, ReasoningEffort: selection.ReasoningEffort,
			SupportedReasoningEfforts: append([]string(nil), selection.SupportedReasoningEfforts...), CatalogRevision: selection.CatalogRevision,
			Source: selection.Source, Destination: selection.Destination, Status: selection.Status,
			ConfirmUnfiltered: selection.ConfirmUnfiltered, MaxOutputTokens: selection.MaxOutputTokens,
			ContextLength: selection.ContextLength, CheckedAt: selection.CheckedAt,
		},
	}
	out := AuthoringCodeRunDTO{
		ID: run.ID, PipelineID: run.PipelineID, PreparationID: run.PreparationID,
		PreparationRequestID: run.PreparationRequestID, RequestID: run.RequestID,
		PipelineRevision: run.PipelineRevision, PlanStageRevision: run.PlanStageRevision,
		PlanArtifactVersion: run.PlanArtifactVersion, CodePreferenceRevision: run.CodePreferenceRevision,
		CodeSelectionHash: run.CodeSelectionHash, ManifestHash: run.ManifestHash, PlanSourceHash: run.PlanSourceHash,
		PrivatePath: run.PrivatePath, Preference: preference,
		Limits: AuthoringCodeRunLimitsDTO{MaxPromptBytes: run.MaxPromptBytes, MaxOutputTokens: run.MaxOutputTokens, MaxOutputTokensPerTurn: run.MaxOutputTokensPerTurn,
			MaxTurns: run.MaxTurns, MaxToolCalls: run.MaxToolCalls, TimeoutMillis: run.TimeoutMillis},
		SessionID: run.SessionID, Status: run.Status, ErrorCode: run.ErrorCode, CancellationPending: run.Status == "cancelling",
		Usage: run.Usage, Baseline: authoringCodeCopyManifest(run.ManifestSnapshot), Changes: make([]sddworkspace.Change, 0, len(run.Changes)),
		PatchHash: run.PatchHash,
		CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
	if run.ResultManifest != nil {
		result := authoringCodeCopyManifest(*run.ResultManifest)
		out.Result = &result
	}
	switch {
	case run.SourceManifest != nil:
		source := authoringCodeCopyManifest(*run.SourceManifest)
		out.SourceManifest = &source
		out.SourceDrift = source.Hash != run.ManifestHash
		if out.SourceDrift {
			out.SourceReadbackStatus = "drifted"
		} else {
			out.SourceReadbackStatus = "matched"
		}
	case run.Status == "running" || run.Status == "cancelling" || run.Status == "interrupted":
		out.SourceReadbackStatus = "pending"
	default:
		out.SourceReadbackStatus = "unavailable"
	}
	for _, change := range run.Changes {
		out.Changes = append(out.Changes, sddworkspace.Change{Path: change.Path, Kind: sddworkspace.ChangeKind(change.Kind)})
	}
	return out
}
