package application

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func (s *Service) GenerateAuthoringStage(in GenerateAuthoringStageInput) (AuthoringStageDTO, error) {
	return s.generateAuthoringStage(in, "start")
}
func (s *Service) RequestAuthoringStageRevision(in GenerateAuthoringStageInput) (AuthoringStageDTO, error) {
	return s.generateAuthoringStage(in, "revision")
}

func authoringGenerationIntent(in GenerateAuthoringStageInput, action string) (string, error) {
	if !validAuthoringStageRef(in.Ref) || in.Selection.MaxOutputTokens < 1 || in.Selection.MaxOutputTokens > sdd.MaxAuthoringOutputTokens ||
		(action != "start" && action != "revision") || (action == "start" && in.Feedback != "") || (action == "revision" && !validAuthoringFeedback(in.Feedback)) ||
		!validAuthoringGenerationConsent(in) {
		return "", ErrInvalidInput
	}
	// The legacy Wails envelope carries catalog fields that are no longer
	// authoritative here. Only the validated output cap remains an operation input.
	in.Selection = APIModelSelectionInput{MaxOutputTokens: in.Selection.MaxOutputTokens}
	return brainstormIntentHash(action, in)
}

func validAuthoringGenerationConsent(in GenerateAuthoringStageInput) bool {
	hasConsent := in.ConfirmUnfiltered || in.ConfirmJITLoad
	if !hasConsent {
		return in.ConsentBinding == nil
	}
	binding := in.ConsentBinding
	if binding == nil || !validSelectionText(binding.BackendID, 128) || !validSelectionText(binding.ModelID, 512) ||
		!validSelectionText(binding.CatalogRevision, 128) || !validSelectionText(binding.Source, 64) || !validSelectionText(binding.Destination, 512) {
		return false
	}
	if in.ConfirmUnfiltered && binding.Source != "openrouter_general_unfiltered" {
		return false
	}
	if in.ConfirmJITLoad && binding.Source != "lm_studio_native" {
		return false
	}
	return true
}

func (s *Service) resolveAuthoringGeneration(in GenerateAuthoringStageInput, action, hash string) (bool, error) {
	receipt, err := s.store.GetAuthoringStageCommand(s.ctx, in.Ref.PipelineID, in.Ref.RequestID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, safe("read authoring request", err)
	}
	if receipt.Stage != sdd.Stage(in.Ref.Stage) || receipt.Action != action || receipt.ClientIntentHash != hash {
		return true, ErrPipelineRequestConflict
	}
	return true, nil
}

func (s *Service) authoringPreflightFailure(in GenerateAuthoringStageInput, action, hash string, cause error) (AuthoringStageDTO, error) {
	if found, err := s.resolveAuthoringGeneration(in, action, hash); found || err != nil {
		if err != nil {
			return AuthoringStageDTO{}, err
		}
		return s.readAuthoringGeneration(in.Ref, nil)
	}
	return AuthoringStageDTO{}, safe("prepare authoring stage", cause)
}

func (s *Service) generateAuthoringStage(in GenerateAuthoringStageInput, action string) (resultDTO AuthoringStageDTO, resultErr error) {
	if err := s.beginCall(); err != nil {
		return AuthoringStageDTO{}, err
	}
	defer s.endCall()
	hash, err := authoringGenerationIntent(in, action)
	if err != nil {
		return AuthoringStageDTO{}, err
	}
	if found, err := s.resolveAuthoringGeneration(in, action, hash); found || err != nil {
		if err != nil {
			return AuthoringStageDTO{}, err
		}
		return s.readAuthoringGeneration(in.Ref, nil)
	}
	run, err := s.store.GetAuthoringStage(s.ctx, in.Ref.PipelineID, sdd.Stage(in.Ref.Stage))
	if err != nil {
		return s.authoringPreflightFailure(in, action, hash, err)
	}
	if run.PipelineRevision != in.Ref.PipelineRevision || run.Revision != in.Ref.StageRevision || run.DiscoveryVersion != in.Ref.DiscoveryVersion || run.ArtifactVersion != in.Ref.ArtifactVersion {
		return s.authoringPreflightFailure(in, action, hash, sqlite.ErrPipelineConflict)
	}
	if err := sddExecutorAdmitted(in.Selection); err != nil {
		return s.authoringPreflightFailure(in, action, hash, err)
	}
	preference, err := s.store.GetAuthoringStageModelPreference(s.ctx, in.Ref.PipelineID, sdd.Stage(in.Ref.Stage))
	if err != nil {
		return s.authoringPreflightFailure(in, action, hash, err)
	}
	resolved, err := s.resolveAuthoringStageModelPreferenceDetails(s.ctx, preference, authoringStageConsent{
		confirmUnfiltered: in.ConfirmUnfiltered,
		confirmJITLoad:    in.ConfirmJITLoad,
		binding:           in.ConsentBinding,
	})
	if err != nil {
		return s.authoringPreflightFailure(in, action, hash, err)
	}
	if resolved.DTO.Resolution != "ready" || !trustedAuthoringPreferenceSource(preference, resolved.DTO.ModelSource) {
		return s.authoringPreflightFailure(in, action, hash, authoringPreferenceExecutionError(resolved.DTO))
	}
	choice := resolved.Selection
	choice.MaxOutputTokens = in.Selection.MaxOutputTokens
	input, err := s.store.PreviewAuthoringStageInput(s.ctx, in.Ref.request(), in.Feedback)
	if err != nil {
		return s.authoringPreflightFailure(in, action, hash, err)
	}
	var prompt authoringStagePrompt
	var attempt catalog.AuthoringStageAttempt
	var admitted bool
	var admissionErr error
	func() {
		// Keep the validated profile revision stable through the SQLite admission
		// fence. A later profile update is a future-state change.
		s.profileGate.RLock()
		defer s.profileGate.RUnlock()
		profile, err := s.store.GetProviderProfile(s.ctx, choice.BackendID)
		if err != nil || profileRevision(profile) != choice.CatalogRevision {
			admissionErr = ErrBackendChanged
			return
		}
		prompt, admissionErr = prepareAuthoringStagePrompt(run, input, choice, profile.ProviderType)
		if admissionErr != nil {
			return
		}
		request := catalog.BeginAuthoringStageRequest{
			Ref: in.Ref.request(), Selection: choice, ModelMode: preference.ModelMode, EffortMode: preference.EffortMode,
			PreferenceSource: resolved.DTO.ModelSource, PreferenceRevision: preference.Revision,
			EstimatedInputTokens: prompt.EstimatedInputTokens, ClientIntentHash: hash, Feedback: in.Feedback,
		}
		attempt, admitted, admissionErr = s.admitOwnedAuthoringStage(request, action)
	}()
	if admissionErr != nil {
		return s.authoringPreflightFailure(in, action, hash, admissionErr)
	}
	if !admitted {
		return s.readAuthoringGeneration(in.Ref, nil)
	}
	defer func() {
		// Returning from this synchronous owner means composition and Prompt have
		// joined. A duplicate command never owns this proof or executes this defer.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		defer cancel()
		settled, err := s.store.ConfirmAuthoringStageStop(ctx, attempt.PipelineID, attempt.Stage, attempt.ID, "joined")
		if err == nil && resultDTO.CancellationPending {
			resultDTO = authoringStageDTO(settled)
		} else if err != nil && resultDTO.CancellationPending {
			resultErr = safe("confirm authoring join", errors.Join(sdd.ErrAuthoringCancellationPending, err))
		}
		s.releaseAuthoringOwner(attempt.ID)
	}()
	ref := in.Ref.request()
	ref.PipelineRevision++
	ref.StageRevision++
	if !reflect.DeepEqual(attempt.Input, input) || !reflect.DeepEqual(attempt.Selection, choice) || attempt.ReservedInputTokens != prompt.EstimatedInputTokens || attempt.ReservedOutputTokens != int64(choice.MaxOutputTokens) {
		return s.failAuthoringGeneration(ref, attempt, "provider_failed")
	}
	session, err := s.createAuthoringReadOnlySession(ref, attempt)
	if err != nil {
		return s.failAuthoringGeneration(ref, attempt, authoringFailureCode(RunResultDTO{}, err, attempt))
	}
	linked, err := s.store.ValidateAuthoringStageSession(s.ctx, ref, attempt.ID, session.ID)
	if err != nil {
		return s.failAuthoringGeneration(ref, attempt, authoringFailureCode(RunResultDTO{}, err, attempt))
	}
	result, err := s.promptAuthoringStage(s.ctx, ref, linked, prompt.Text)
	if err != nil || result.Status != RunCompleted {
		return s.failAuthoringGeneration(ref, linked, authoringFailureCode(result, err, linked))
	}
	content, usage, err := s.readSDDReadOnlyEvidence(s.ctx, linked.SessionID)
	if err != nil {
		code := authoringFailureCode(RunResultDTO{}, err, linked)
		if code == "provider_failed" {
			code = "invalid_output"
		}
		return s.failAuthoringGeneration(ref, linked, code)
	}
	canonical, err := sdd.CanonicalAuthoringDocument(ref.Stage, []byte(content))
	if err != nil {
		return s.failAuthoringGeneration(ref, linked, "invalid_output")
	}
	// Publication and closing share the same linearization point. No provider
	// or journal work runs while this short storage gate is held.
	s.authoringAdmissionGate.Lock()
	s.mu.RLock()
	closing := s.closing
	s.mu.RUnlock()
	if closing {
		s.authoringAdmissionGate.Unlock()
		return s.failAuthoringGeneration(ref, linked, "cancelled")
	}
	_, err = s.store.CompleteAuthoringStage(s.ctx, catalog.CompleteAuthoringStageRequest{Ref: ref, AttemptID: linked.ID, SessionID: linked.SessionID, Content: canonical, Usage: usage})
	s.authoringAdmissionGate.Unlock()
	if err != nil && !errors.Is(err, sdd.ErrAuthoringBudgetExceeded) {
		return s.failAuthoringGeneration(ref, linked, authoringFailureCode(RunResultDTO{}, err, linked))
	}
	return s.readAuthoringGeneration(in.Ref, err)
}

func (s *Service) admitOwnedAuthoringStage(request catalog.BeginAuthoringStageRequest, action string) (catalog.AuthoringStageAttempt, bool, error) {
	s.authoringAdmissionGate.Lock()
	defer s.authoringAdmissionGate.Unlock()
	s.mu.RLock()
	closing := s.closing
	s.mu.RUnlock()
	if closing {
		return catalog.AuthoringStageAttempt{}, false, context.Canceled
	}
	var attempt catalog.AuthoringStageAttempt
	var admitted bool
	var err error
	if action == "revision" {
		attempt, admitted, err = s.store.ReviseAuthoringStage(s.ctx, request)
	} else {
		attempt, admitted, err = s.store.BeginAuthoringStage(s.ctx, request)
	}
	if err == nil && admitted {
		ref := request.Ref
		ref.PipelineRevision++
		ref.StageRevision++
		s.mu.Lock()
		if s.authoringActive == nil {
			s.authoringActive = make(map[string]catalog.AuthoringStageRequest)
		}
		s.authoringActive[attempt.ID] = ref
		s.mu.Unlock()
	}
	return attempt, admitted, err
}

func (s *Service) releaseAuthoringOwner(attemptID string) {
	s.mu.Lock()
	delete(s.authoringActive, attemptID)
	s.mu.Unlock()
}

func authoringFailureCode(result RunResultDTO, err error, attempt catalog.AuthoringStageAttempt) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), !time.Now().Before(attempt.CreatedAt.Add(sdd.AuthoringAttemptTimeout)):
		return "timeout"
	case errors.Is(err, context.Canceled), result.Status == RunCancelled:
		return "cancelled"
	case result.Reason == "output_limit_exceeded":
		return "output_overflow"
	case result.Reason == "tool_failed", result.Reason == "unknown_tool", result.Reason == "policy_denied", result.Status == RunAwaitingApproval:
		return "invalid_output"
	default:
		return "provider_failed"
	}
}

func (s *Service) readAuthoringGeneration(ref AuthoringStageRefInput, cause error) (AuthoringStageDTO, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	run, err := s.store.GetAuthoringStage(ctx, ref.PipelineID, sdd.Stage(ref.Stage))
	if err != nil {
		return AuthoringStageDTO{}, safe("read authoring result", err)
	}
	if cause != nil {
		return authoringStageDTO(run), safe("settle authoring stage", cause)
	}
	return authoringStageDTO(run), nil
}

func (s *Service) failAuthoringGeneration(ref catalog.AuthoringStageRequest, attempt catalog.AuthoringStageAttempt, code string) (AuthoringStageDTO, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	run, err := s.store.GetAuthoringStage(ctx, ref.PipelineID, ref.Stage)
	if err != nil {
		return AuthoringStageDTO{}, safe("read failed authoring stage", err)
	}
	for _, a := range run.Attempts {
		if a.ID == attempt.ID && a.Status != "running" {
			return authoringStageDTO(run), nil
		}
	}
	_, err = s.store.FailAuthoringStage(ctx, catalog.FailAuthoringStageRequest{Ref: ref, AttemptID: attempt.ID, ErrorCode: code})
	return s.readAuthoringGeneration(AuthoringStageRefInput{PipelineID: ref.PipelineID, Stage: string(ref.Stage)}, err)
}
