package application

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

type pausedAuthoringCompletion struct {
	Store
	service        *Service
	entered        chan struct{}
	release        chan struct{}
	closingAtWrite atomic.Bool
}

func (s *pausedAuthoringCompletion) CompleteAuthoringStage(ctx context.Context, in catalog.CompleteAuthoringStageRequest) (catalog.AuthoringStageRun, error) {
	close(s.entered)
	<-s.release
	s.service.mu.RLock()
	s.closingAtWrite.Store(s.service.closing)
	s.service.mu.RUnlock()
	return s.Store.CompleteAuthoringStage(ctx, in)
}

func TestAuthoringCompletionAndClosingHaveOneLinearizationGate(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	store := &pausedAuthoringCompletion{Store: db, service: s, entered: make(chan struct{}), release: make(chan struct{})}
	s.store = store
	release := sync.OnceFunc(func() { close(store.release) })
	defer release()
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		return &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}, nil
	}
	type result struct {
		run AuthoringStageDTO
		err error
	}
	done := make(chan result, 1)
	go func() { run, err := s.GenerateAuthoringStage(in); done <- result{run, err} }()
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not reach writer boundary")
	}
	if s.authoringAdmissionGate.TryLock() {
		// Model Shutdown's closing transition while holding its admission gate.
		// The old implementation lets Complete race past this durable boundary.
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		release()
		var got result
		select {
		case got = <-done:
		case <-time.After(5 * time.Second):
			s.authoringAdmissionGate.Unlock()
			t.Fatal("unprotected completion did not finish")
		}
		s.authoringAdmissionGate.Unlock()
		if store.closingAtWrite.Load() && len(got.run.Artifacts) > 0 {
			t.Fatal("Complete published after closing was set under the shutdown gate")
		}
		t.Fatal("completion did not own the shared closing gate")
	}
	closed := make(chan error, 1)
	go func() { closed <- Shutdown(s) }()
	release()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not finish")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if store.closingAtWrite.Load() {
		t.Fatal("closing was set before the already-linearized completion committed")
	}
}
