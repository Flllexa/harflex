package application

import (
	"context"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func (s *Service) ApproveAuthoringStage(in AuthoringStageDecisionInput) (AuthoringStageDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringStageDTO{}, err
	}
	defer s.endCall()
	if !validAuthoringStageRef(in.Ref) || in.AttemptID != "" || in.Reason != "" {
		return AuthoringStageDTO{}, ErrInvalidInput
	}
	run, err := s.store.ApproveAuthoringStage(s.ctx, catalog.AuthoringStageDecisionRequest{Ref: in.Ref.request()})
	if err != nil {
		return AuthoringStageDTO{}, safe("approve authoring stage", err)
	}
	return authoringStageDTO(run), nil
}

func (s *Service) SkipAuthoringStage(in AuthoringStageDecisionInput) (AuthoringStageDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringStageDTO{}, err
	}
	defer s.endCall()
	if !validAuthoringStageRef(in.Ref) || in.AttemptID != "" || !validAuthoringFeedback(in.Reason) {
		return AuthoringStageDTO{}, ErrInvalidInput
	}
	run, err := s.store.SkipAuthoringStage(s.ctx, catalog.AuthoringStageDecisionRequest{Ref: in.Ref.request(), Reason: in.Reason})
	if err != nil {
		return AuthoringStageDTO{}, safe("skip authoring stage", err)
	}
	return authoringStageDTO(run), nil
}

func (s *Service) CancelAuthoringStage(in AuthoringStageDecisionInput) (AuthoringStageDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringStageDTO{}, err
	}
	defer s.endCall()
	if !validAuthoringStageRef(in.Ref) || !validSelectionText(in.AttemptID, 128) || in.Reason != "" {
		return AuthoringStageDTO{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	receipt, err := s.store.CancelAuthoringStage(ctx, catalog.AuthoringStageDecisionRequest{Ref: in.Ref.request(), AttemptID: in.AttemptID})
	if err != nil {
		return AuthoringStageDTO{}, safe("cancel authoring stage", err)
	}
	var sessionID string
	for _, a := range receipt.Attempts {
		if a.ID == in.AttemptID {
			sessionID = a.SessionID
			break
		}
	}
	s.mu.RLock()
	bound, _ := s.sessions[sessionID].(*authoringStageRunner)
	s.mu.RUnlock()
	var abortErr error
	joined := sessionID == ""
	if bound != nil && bound.attempt.ID == in.AttemptID && bound.attempt.SessionID == sessionID && bound.ref.PipelineID == in.Ref.PipelineID && bound.ref.Stage == sdd.Stage(in.Ref.Stage) {
		abortErr = bound.Abort(ctx)
		joined = abortErr == nil
	}
	readCtx, readCancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer readCancel()
	if joined && sessionID != "" {
		if _, err := s.store.ConfirmAuthoringStageStop(readCtx, in.Ref.PipelineID, sdd.Stage(in.Ref.Stage), in.AttemptID, "joined"); err != nil {
			return authoringStageDTO(receipt), safe("confirm authoring stop", err)
		}
	}
	current, err := s.store.GetAuthoringStage(readCtx, in.Ref.PipelineID, sdd.Stage(in.Ref.Stage))
	if err != nil {
		return authoringStageDTO(receipt), safe("confirm authoring cancellation", err)
	}
	for _, a := range current.Attempts {
		if a.ID == in.AttemptID && a.Status == "interrupted" && a.ErrorCode == "cancelled" {
			if a.CancellationState == "pending" || (abortErr != nil && a.CancellationState != "confirmed") {
				return authoringStageDTO(current), sdd.ErrAuthoringCancellationPending
			}
			return authoringStageDTO(current), nil
		}
	}
	return authoringStageDTO(receipt), ErrInvalidInput
}

func (s *Service) authoringStopConfirmed(ctx context.Context, ref catalog.AuthoringStageRequest, attemptID string) bool {
	run, err := s.store.GetAuthoringStage(ctx, ref.PipelineID, ref.Stage)
	if err != nil {
		return false
	}
	for _, a := range run.Attempts {
		if a.ID == attemptID {
			return a.Status == "completed" || a.CancellationState == "confirmed" || a.SessionID == "" && a.Status != "running"
		}
	}
	return false
}

func (s *Service) fenceAuthoringOwner(ctx context.Context, attemptID string, ref catalog.AuthoringStageRequest) bool {
	_, _ = s.store.FailAuthoringStage(ctx, catalog.FailAuthoringStageRequest{Ref: ref, AttemptID: attemptID, ErrorCode: "cancelled"})
	current, err := s.store.GetAuthoringStage(ctx, ref.PipelineID, ref.Stage)
	if err != nil {
		return false
	}
	for _, a := range current.Attempts {
		if a.ID == attemptID {
			return a.Status != "running" || current.Revision != ref.StageRevision || current.PipelineRevision != ref.PipelineRevision || current.DiscoveryVersion != ref.DiscoveryVersion
		}
	}
	return false
}
