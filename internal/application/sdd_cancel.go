package application

import (
	"context"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

type CancelBrainstormAttemptInput struct {
	Ref       BrainstormRequestInput `json:"ref"`
	AttemptID string                 `json:"attemptId"`
}

func (s *Service) CancelBrainstormAttempt(in CancelBrainstormAttemptInput) (BrainstormDTO, error) {
	if err := s.beginCall(); err != nil {
		return BrainstormDTO{}, err
	}
	defer s.endCall()
	if !validBrainstormRef(in.Ref) || in.AttemptID == "" {
		return BrainstormDTO{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	receipt, err := s.store.CancelBrainstormAttempt(ctx, catalog.CancelBrainstormAttemptRequest{BrainstormRequest: in.Ref.request(), AttemptID: in.AttemptID})
	if err != nil {
		return BrainstormDTO{}, safe("cancel brainstorm attempt", err)
	}
	// Durably fence publication before stopping the runner, including preflight.
	s.mu.Lock()
	var bound *sddAttemptRunner
	for _, runner := range s.sessions {
		if r, ok := runner.(*sddAttemptRunner); ok && r.attempt.RunID == in.Ref.RunID && r.attempt.ID == in.AttemptID {
			bound = r
			break
		}
	}
	s.mu.Unlock()
	if bound != nil {
		// A provider may ignore cancellation. The durable fence still prevents
		// publication; do not expose raw abort errors or wait without a bound.
		_ = bound.Abort(ctx)
	}
	readCtx, readCancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer readCancel()
	current, err := s.store.GetBrainstorming(readCtx, in.Ref.RunID)
	if err != nil {
		return brainstormDTO(receipt), safe("confirm brainstorm cancellation", err)
	}
	for _, attempt := range current.Attempts {
		if attempt.ID == in.AttemptID && attempt.Status == "interrupted" && attempt.ErrorCode == "cancelled" {
			return brainstormDTO(receipt), nil
		}
	}
	return brainstormDTO(receipt), ErrInvalidInput
}

func (s *Service) ResumePausedBrainstorm(in BrainstormRequestInput) (BrainstormDTO, error) {
	return s.brainstormHumanCall(in, func(ctx context.Context) (catalog.BrainstormRun, error) {
		return s.store.ResumePausedBrainstorm(ctx, in.request())
	})
}
