package application

import (
	"context"
	"errors"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func (s *Service) GenerateBrainstormQuestion(in GenerateBrainstormInput) (BrainstormDTO, error) {
	return s.generateBrainstorm(in, "question")
}

func (s *Service) QuestionsSufficient(in GenerateBrainstormInput) (BrainstormDTO, error) {
	return s.generateBrainstorm(in, "questions_sufficient")
}

func (s *Service) FinishAndGenerateSynthesis(in GenerateBrainstormInput) (BrainstormDTO, error) {
	return s.generateBrainstorm(in, "finish_synthesis")
}

func (s *Service) generateBrainstorm(in GenerateBrainstormInput, action string) (BrainstormDTO, error) {
	if err := s.beginCall(); err != nil {
		return BrainstormDTO{}, err
	}
	defer s.endCall()
	hash, err := brainstormCommandIntent(in, action)
	if err != nil {
		return BrainstormDTO{}, err
	}
	if _, found, err := s.resolveBrainstormCommand(in, action, hash); found || err != nil {
		if err != nil {
			return BrainstormDTO{}, err
		}
		return s.readBrainstormGeneration(in.Ref.RunID, nil)
	}
	run, err := s.store.GetBrainstorming(s.ctx, in.Ref.RunID)
	if err != nil {
		return BrainstormDTO{}, safe("read brainstorm input", err)
	}
	if run.Revision != in.Ref.RunRevision || run.PipelineRevision != in.Ref.PipelineRevision || run.DiscoveryVersion != in.Ref.DiscoveryVersion {
		// Another service may have committed this command between receipt lookup
		// and snapshot read. That winner still owns the only provider execution.
		if _, found, err := s.resolveBrainstormCommand(in, action, hash); found || err != nil {
			if err != nil {
				return BrainstormDTO{}, err
			}
			return s.readBrainstormGeneration(in.Ref.RunID, nil)
		}
		return BrainstormDTO{}, safe("validate brainstorm revision", sqlite.ErrPipelineConflict)
	}
	selectionInput := in.Selection
	selectionInput.ForSDD = true
	selectionInput.MaxOutputTokens = 256
	kind := "question"
	if action != "question" {
		kind, selectionInput.MaxOutputTokens = "synthesis", 1024
	}
	pipeline, err := s.store.GetPipeline(s.ctx, run.PipelineID)
	if err != nil {
		return BrainstormDTO{}, safe("read brainstorm workspace", err)
	}
	selection, err := s.prepareSDDModelSelection(s.ctx, pipeline.WorkspaceID, selectionInput)
	if err != nil {
		return BrainstormDTO{}, safe("validate brainstorm model", err)
	}
	providerType, err := s.sddSelectionEstimator(s.ctx, *selection)
	if err != nil {
		return BrainstormDTO{}, err
	}
	prompt, err := prepareSDDBrainstormPrompt(run, kind, *selection, providerType)
	if err != nil {
		return BrainstormDTO{}, safe("prepare brainstorm prompt", err)
	}
	attempt, admitted, err := s.admitPreparedBrainstormCommand(in, action, hash, *selection, prompt.EstimatedInputTokens)
	if err != nil {
		return BrainstormDTO{}, err
	}
	if !admitted {
		return s.readBrainstormGeneration(in.Ref.RunID, nil)
	}
	// Admission increments both fences once. Retain those exact fences instead
	// of accepting a newer snapshot that another command could have changed.
	ref := in.Ref.request()
	ref.RunRevision++
	ref.PipelineRevision++
	session, err := s.createSDDReadOnlySession(ref, attempt)
	if err != nil {
		return s.failBrainstormGeneration(ref, attempt, brainstormFailureCode(RunResultDTO{}, err, attempt))
	}
	linked, err := s.store.ValidateBrainstormSession(s.ctx, ref, attempt.ID, session.ID)
	if err != nil {
		return s.failBrainstormGeneration(ref, attempt, brainstormReadbackFailureCode(err, attempt))
	}
	result, err := s.promptSDDAttempt(s.ctx, ref, linked, prompt.Text)
	if err != nil || result.Status != RunCompleted {
		return s.failBrainstormGeneration(ref, linked, brainstormFailureCode(result, err, linked))
	}
	evidence, err := s.readSDDBrainstormEvidence(s.ctx, linked.SessionID, kind)
	if err != nil {
		return s.failBrainstormGeneration(ref, linked, brainstormReadbackFailureCode(err, linked))
	}
	_, err = s.store.CompleteBrainstormAttempt(s.ctx, catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: linked.ID, SessionID: linked.SessionID, Question: evidence.Question, Synthesis: evidence.Synthesis, Usage: evidence.Usage})
	if err != nil && !errors.Is(err, sdd.ErrBrainstormBudgetExceeded) {
		return s.failBrainstormGeneration(ref, linked, brainstormFailureCode(RunResultDTO{}, err, linked))
	}
	return s.readBrainstormGeneration(ref.RunID, err)
}

func brainstormReadbackFailureCode(err error, attempt catalog.BrainstormAttempt) string {
	code := brainstormFailureCode(RunResultDTO{}, err, attempt)
	if code == "provider_failed" {
		return "invalid_output"
	}
	return code
}

func brainstormFailureCode(result RunResultDTO, err error, attempt catalog.BrainstormAttempt) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), !time.Now().Before(attempt.CreatedAt.Add(sddAttemptTimeout)):
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

// Terminal readback and failure settlement survive cancellation, but remain
// bounded and covered by the outer call lifetime so shutdown cannot close DB.
func (s *Service) readBrainstormGeneration(runID string, cause error) (BrainstormDTO, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	run, err := s.store.GetBrainstorming(ctx, runID)
	if err != nil {
		return BrainstormDTO{}, safe("read brainstorm result", err)
	}
	if cause != nil {
		return brainstormDTO(run), safe("settle brainstorm", cause)
	}
	return brainstormDTO(run), nil
}

func (s *Service) failBrainstormGeneration(ref catalog.BrainstormRequest, attempt catalog.BrainstormAttempt, code string) (BrainstormDTO, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	run, err := s.store.GetBrainstorming(ctx, ref.RunID)
	if err != nil {
		return BrainstormDTO{}, safe("read failed brainstorm", err)
	}
	for _, current := range run.Attempts {
		if current.ID == attempt.ID && current.Status != "running" {
			// Composition, invalidation or recovery already settled this attempt.
			return brainstormDTO(run), nil
		}
	}
	_, err = s.store.FailBrainstormAttempt(ctx, catalog.FailBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: attempt.ID, ErrorCode: code})
	return s.readBrainstormGeneration(ref.RunID, err)
}
