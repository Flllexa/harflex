package application

import (
	"context"

	"github.com/persioflexa/harflex/internal/catalog"
)

type ApproveBrainstormSynthesisInput struct {
	Ref              BrainstormRequestInput `json:"ref"`
	SynthesisVersion int                    `json:"synthesisVersion"`
}
type RequestBrainstormRevisionInput struct {
	Ref              BrainstormRequestInput `json:"ref"`
	SynthesisVersion int                    `json:"synthesisVersion"`
	Choice           string                 `json:"choice"`
	Feedback         string                 `json:"feedback"`
}
type SkipBrainstormQuestionsInput struct {
	Ref    BrainstormRequestInput `json:"ref"`
	Reason string                 `json:"reason"`
}

// Human actions return their immutable transactional receipt, without refreshing
// catalog credentials or triggering generation. GetBrainstorming is the live view.
func (s *Service) brainstormHumanCall(ref BrainstormRequestInput, call func(context.Context) (catalog.BrainstormRun, error)) (BrainstormDTO, error) {
	if err := s.beginCall(); err != nil {
		return BrainstormDTO{}, err
	}
	defer s.endCall()
	if !validBrainstormRef(ref) {
		return BrainstormDTO{}, ErrInvalidInput
	}
	run, err := call(s.ctx)
	if err != nil {
		return BrainstormDTO{}, safe("apply brainstorm human action", err)
	}
	return brainstormDTO(run), nil
}
func (s *Service) ApproveBrainstormSynthesis(in ApproveBrainstormSynthesisInput) (BrainstormDTO, error) {
	return s.brainstormHumanCall(in.Ref, func(ctx context.Context) (catalog.BrainstormRun, error) {
		return s.store.ApproveBrainstormSynthesis(ctx, catalog.ApproveBrainstormSynthesisRequest{BrainstormRequest: in.Ref.request(), SynthesisVersion: in.SynthesisVersion})
	})
}
func (s *Service) RequestBrainstormRevision(in RequestBrainstormRevisionInput) (BrainstormDTO, error) {
	return s.brainstormHumanCall(in.Ref, func(ctx context.Context) (catalog.BrainstormRun, error) {
		return s.store.RequestBrainstormRevision(ctx, catalog.RequestBrainstormRevisionRequest{BrainstormRequest: in.Ref.request(), SynthesisVersion: in.SynthesisVersion, Choice: in.Choice, Feedback: in.Feedback})
	})
}
func (s *Service) SkipBrainstormQuestions(in SkipBrainstormQuestionsInput) (BrainstormDTO, error) {
	return s.brainstormHumanCall(in.Ref, func(ctx context.Context) (catalog.BrainstormRun, error) {
		return s.store.SkipBrainstormQuestions(ctx, catalog.SkipBrainstormQuestionsRequest{BrainstormRequest: in.Ref.request(), Reason: in.Reason})
	})
}
func (s *Service) ConfirmDiscoveryAfterSkip(in BrainstormRequestInput) (BrainstormDTO, error) {
	return s.brainstormHumanCall(in, func(ctx context.Context) (catalog.BrainstormRun, error) {
		return s.store.ConfirmDiscoveryAfterSkip(ctx, in.request())
	})
}
