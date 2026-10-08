package application

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
)

type nonCooperativeAuthoringProvider struct {
	started chan int32
	release chan struct{}
	calls   atomic.Int32
}

func TestAuthoringShutdownPreservesUnjoinedStop(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	p := &nonCooperativeAuthoringProvider{started: make(chan int32, 4), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(p.release) })
	defer release()
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return p, nil }
	done := make(chan error, 1)
	go func() { _, err := s.GenerateAuthoringStage(in); done <- err }()
	select {
	case <-p.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	if err := Shutdown(s); !errors.Is(err, sdd.ErrAuthoringCancellationPending) {
		t.Fatalf("unjoined shutdown: %v", err)
	}
	run, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil || !run.CancellationPending || len(run.Artifacts) != 0 {
		t.Fatalf("uncertainty lost: %+v %v", run, err)
	}
	last := run.Attempts[0]
	next := catalog.BeginAuthoringStageRequest{Ref: stageCommandRef(run, "shutdown_new_start").request(), Selection: last.Selection, ModelMode: last.ModelMode, EffortMode: last.EffortMode, PreferenceSource: last.PreferenceSource, PreferenceRevision: last.PreferenceRevision, EstimatedInputTokens: 4096, ClientIntentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if _, admitted, err := db.BeginAuthoringStage(t.Context(), next); !errors.Is(err, sdd.ErrAuthoringCancellationPending) || admitted {
		t.Fatalf("pending admitted: %v %v", admitted, err)
	}
	if _, err := db.SkipAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: next.Ref, Reason: "Must remain blocked"}); !errors.Is(err, sdd.ErrAuthoringCancellationPending) {
		t.Fatalf("pending skip: %v", err)
	}
	next.Feedback = "Must remain blocked"
	if _, admitted, err := db.ReviseAuthoringStage(t.Context(), next); !errors.Is(err, sdd.ErrAuthoringCancellationPending) || admitted {
		t.Fatalf("pending revision: %v %v", admitted, err)
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not join")
	}
	if err := Shutdown(s); err != nil {
		t.Fatalf("joined shutdown: %v", err)
	}
}

type unavailableStopConfirmation struct{ Store }

func (s unavailableStopConfirmation) ConfirmAuthoringStageStop(context.Context, string, sdd.Stage, string, string) (catalog.AuthoringStageRun, error) {
	return catalog.AuthoringStageRun{}, errors.New("confirmation unavailable")
}

func TestAuthoringRecoveryRequiresRealTerminalEvidence(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	s.store = unavailableStopConfirmation{db}
	p := &nonCooperativeAuthoringProvider{started: make(chan int32, 4), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(p.release) })
	defer release()
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return p, nil }
	done := make(chan error, 1)
	go func() { _, err := s.GenerateAuthoringStage(in); done <- err }()
	select {
	case <-p.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not started")
	}
	run, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelAuthoringStage(AuthoringStageDecisionInput{Ref: stageCommandRef(run, "recover_stop_cancel"), AttemptID: run.Attempts[0].ID}); !errors.Is(err, sdd.ErrAuthoringCancellationPending) {
		t.Fatalf("cancel: %v", err)
	}
	peer := NewService(t.Context(), Dependencies{Store: db, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { t.Fatal("recovery invoked provider"); return nil, nil }})
	t.Cleanup(func() { Shutdown(peer) })
	peer.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	if err := peer.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	blocked, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil || !blocked.CancellationPending || blocked.AttemptCount != 1 {
		t.Fatal("recovery invented termination")
	}
	release()
	select {
	case err := <-done:
		if !errors.Is(err, sdd.ErrAuthoringCancellationPending) {
			t.Fatalf("unpersisted confirmation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner did not join")
	}
	if err := peer.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	settled, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil || settled.CancellationPending || settled.State != "paused" || settled.AttemptCount != 1 || p.calls.Load() != 1 {
		t.Fatalf("terminal recovery: %+v %v", settled, err)
	}
}

func (*nonCooperativeAuthoringProvider) ID() string { return "non-cooperative-authoring" }
func (*nonCooperativeAuthoringProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true}
}
func (p *nonCooperativeAuthoringProvider) Stream(_ context.Context, _ agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	p.started <- p.calls.Add(1)
	// Deliberately ignore ctx.Done until the test proves the pending state.
	<-p.release
	output := make(chan agentcore.StreamEvent, 1)
	errs := make(chan error)
	output <- agentcore.StreamEvent{Type: "text_delta", Delta: authoringSpecOutput}
	close(output)
	close(errs)
	return output, errs
}

func TestAuthoringCancelPendingBlocksNewStartUntilJoin(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	p := &nonCooperativeAuthoringProvider{started: make(chan int32, 4), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(p.release) })
	defer release()
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return p, nil }
	done := make(chan error, 1)
	go func() { _, err := s.GenerateAuthoringStage(in); done <- err }()
	select {
	case <-p.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not started")
	}
	run, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.CancelAuthoringStage(AuthoringStageDecisionInput{Ref: stageCommandRef(run, "uncertain_cancel_01"), AttemptID: run.Attempts[0].ID})
	if err == nil {
		t.Error("abort timeout reported terminal cancellation success")
	}
	if result.State != "cancellation_pending" {
		t.Errorf("uncertainty not exposed: state=%q", result.State)
	}
	current, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	next := in
	next.Ref = stageCommandRef(current, "new_start_while_stop")
	nextDone := make(chan error, 1)
	go func() { _, err := s.GenerateAuthoringStage(next); nextDone <- err }()
	select {
	case err := <-nextDone:
		if err == nil {
			t.Error("new generation admitted while cancellation is uncertain")
		}
	case <-p.started:
		t.Error("second provider call overlapped the unjoined attempt")
	case <-time.After(5 * time.Second):
		t.Error("new command did not reject pending cancellation")
	}
	if p.calls.Load() != 1 {
		t.Errorf("provider calls=%d, want 1", p.calls.Load())
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("original call did not join after release")
	}
	// Drain any incorrectly admitted contender before fixture shutdown.
	if p.calls.Load() > 1 {
		select {
		case <-nextDone:
		case <-time.After(5 * time.Second):
			t.Fatal("contender did not finish")
		}
	}
	joined, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil || joined.CancellationPending || joined.State != "paused" || joined.Attempts[0].CancellationState != "confirmed" || len(joined.Artifacts) != 0 || joined.InputBudgetRemaining != run.InputBudgetRemaining {
		t.Fatalf("join not settled or reservation changed: %+v %v", joined, err)
	}
	afterJoin := in
	afterJoin.Ref = stageCommandRef(joined, "explicit_after_join")
	if _, err := s.GenerateAuthoringStage(afterJoin); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 2 {
		t.Fatal("explicit post-join generation did not run")
	}
}
