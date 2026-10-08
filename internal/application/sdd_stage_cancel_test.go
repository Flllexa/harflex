package application

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
)

type authoringBlockingProvider struct {
	started      chan struct{}
	fenced       chan bool
	beforeCancel func() bool
}

func (*authoringBlockingProvider) ID() string { return "authoring-blocking" }
func (*authoringBlockingProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true}
}
func (p *authoringBlockingProvider) Stream(ctx context.Context, _ agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	output, errs := make(chan agentcore.StreamEvent, 1), make(chan error, 1)
	go func() {
		close(p.started)
		<-ctx.Done()
		p.fenced <- p.beforeCancel()
		// Deliberately return plausible output after cancellation: it must not publish.
		output <- agentcore.StreamEvent{Type: "text_delta", Delta: authoringSpecOutput}
		close(output)
		close(errs)
	}()
	return output, errs
}

func stageCommandRef(run catalog.AuthoringStageRun, key string) AuthoringStageRefInput {
	return AuthoringStageRefInput{PipelineID: run.PipelineID, Stage: string(run.Stage), RequestID: key, PipelineRevision: run.PipelineRevision, StageRevision: run.Revision, DiscoveryVersion: run.DiscoveryVersion, ArtifactVersion: run.ArtifactVersion}
}

func TestAuthoringCancellationFencesBeforeRunnerAndRejectsLateOutput(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	canceller, ok := any(s).(interface {
		CancelAuthoringStage(AuthoringStageDecisionInput) (AuthoringStageDTO, error)
	})
	if !ok {
		t.Fatal("explicit authoring cancellation API is missing")
	}
	provider := &authoringBlockingProvider{started: make(chan struct{}), fenced: make(chan bool, 1)}
	provider.beforeCancel = func() bool {
		run, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
		return err == nil && run.State == "cancellation_pending" && run.CancellationPending && run.Attempts[0].Status == "interrupted" && run.Attempts[0].ErrorCode == "cancelled"
	}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	done := make(chan error, 1)
	go func() { _, err := s.GenerateAuthoringStage(in); done <- err }()
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	run, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	// Source drift must not obstruct the user's stop command.
	if _, err := db.DB().Exec(`UPDATE pipeline_artifacts SET content='Drift after admission' WHERE pipeline_id=? AND stage='discovery'`, in.Ref.PipelineID); err != nil {
		t.Fatal(err)
	}
	command := AuthoringStageDecisionInput{Ref: stageCommandRef(run, "user_cancel_stage_1"), AttemptID: run.Attempts[0].ID}
	receipt, err := canceller.CancelAuthoringStage(command)
	if err != nil || receipt.State != "paused" {
		t.Fatalf("cancel: %+v %v", receipt, err)
	}
	select {
	case fenced := <-provider.fenced:
		if !fenced {
			t.Fatal("runner aborted before durable cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner was not aborted")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not drain")
	}
	after, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil || len(after.Artifacts) != 0 || after.Attempts[0].Status != "interrupted" {
		t.Fatalf("late output published: %+v %v", after, err)
	}
}

type observeAuthoringFenceStore struct {
	Store
	fenced chan struct{}
	once   sync.Once
}

func (s *observeAuthoringFenceStore) FailAuthoringStage(ctx context.Context, in catalog.FailAuthoringStageRequest) (catalog.AuthoringStageRun, error) {
	run, err := s.Store.FailAuthoringStage(ctx, in)
	if err == nil {
		s.once.Do(func() { close(s.fenced) })
	}
	return run, err
}

func TestAuthoringShutdownFencesBeforeCompositionFinishes(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	observed := &observeAuthoringFenceStore{Store: db, fenced: make(chan struct{})}
	s.store = observed
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		close(entered)
		<-release
		return &brainstormTestProvider{}, nil
	}
	generated := make(chan error, 1)
	go func() { _, err := s.GenerateAuthoringStage(in); generated <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("composition not entered")
	}
	closed := make(chan struct{})
	go func() { Shutdown(s); close(closed) }()
	select {
	case <-observed.fenced:
	case <-time.After(3 * time.Second):
		unblock()
		<-generated
		<-closed
		t.Fatal("shutdown joined composition without first fencing the admitted attempt")
	}
	run, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil || run.State != "cancellation_pending" || !run.CancellationPending || run.Attempts[0].ErrorCode != "cancelled" {
		t.Fatalf("shutdown fence: %+v %v", run, err)
	}
	unblock()
	select {
	case err := <-generated:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not finish")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not drain")
	}
}

func TestAuthoringGenericSessionCancelUsesDurableFence(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	p := &authoringBlockingProvider{started: make(chan struct{}), fenced: make(chan bool, 1)}
	p.beforeCancel = func() bool {
		run, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
		return err == nil && run.State == "cancellation_pending" && run.CancellationPending && run.Attempts[0].Status == "interrupted"
	}
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
	if err := s.Cancel(run.Attempts[0].SessionID); err != nil {
		t.Fatal(err)
	}
	select {
	case fenced := <-p.fenced:
		if !fenced {
			t.Error("generic Cancel aborted before the durable fence")
		}
	case <-time.After(5 * time.Second):
		t.Error("provider not cancelled")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not drain")
	}
}

func TestAuthoringShutdownFencesRunningProviderAndRestartNeverSpends(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	p := &authoringBlockingProvider{started: make(chan struct{}), fenced: make(chan bool, 1)}
	p.beforeCancel = func() bool {
		run, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
		return err == nil && run.State == "cancellation_pending" && run.CancellationPending && run.Attempts[0].Status == "failed" && run.Attempts[0].ErrorCode == "cancelled"
	}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return p, nil }
	done := make(chan error, 1)
	go func() { _, err := s.GenerateAuthoringStage(in); done <- err }()
	select {
	case <-p.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not started")
	}
	closed := make(chan struct{})
	go func() { Shutdown(s); close(closed) }()
	select {
	case fenced := <-p.fenced:
		if !fenced {
			t.Fatal("shutdown aborted before fence")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider not stopped")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not drain")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not drain")
	}
	restarted := NewService(t.Context(), Dependencies{Store: db, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { t.Fatal("restart composed provider"); return nil, nil }})
	t.Cleanup(func() { Shutdown(restarted) })
	restarted.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	if err := restarted.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	in.Selection.CredentialToken = ""
	replayed, err := restarted.GenerateAuthoringStage(in)
	if err != nil || replayed.State != "paused" || len(replayed.Artifacts) != 0 || len(replayed.Attempts) != 1 {
		t.Fatalf("restart replay: %+v %v", replayed, err)
	}
}

func TestAuthoringApprovalAndSkipAreExplicitReadOnlyCommands(t *testing.T) {
	for _, action := range []string{"approve", "skip"} {
		t.Run(action, func(t *testing.T) {
			s, db, in, _ := authoringGenerationFixture(t)
			provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
			s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
			if action == "approve" {
				if _, err := s.GenerateAuthoringStage(in); err != nil {
					t.Fatal(err)
				}
			}
			stored, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
			if err != nil {
				t.Fatal(err)
			}
			command := AuthoringStageDecisionInput{Ref: stageCommandRef(stored, "explicit_stage_gate")}
			before := provider.calls.Load()
			s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
			var result, replay AuthoringStageDTO
			if action == "approve" {
				result, err = s.ApproveAuthoringStage(command)
			} else {
				command.Reason = "Use approved Discovery"
				result, err = s.SkipAuthoringStage(command)
			}
			if err != nil {
				t.Fatal(err)
			}
			if action == "approve" {
				replay, err = s.ApproveAuthoringStage(command)
			} else {
				replay, err = s.SkipAuthoringStage(command)
			}
			if err != nil || !reflect.DeepEqual(result, replay) || provider.calls.Load() != before {
				t.Fatalf("gate replay: %+v %v", replay, err)
			}
			pipeline, err := s.GetPipeline(in.Ref.PipelineID)
			if err != nil || pipeline.CurrentStage != "plan" {
				t.Fatalf("gate transition: %+v %v", pipeline, err)
			}
			plan, err := s.GetAuthoringStage(GetAuthoringStageInput{PipelineID: in.Ref.PipelineID, Stage: "plan"})
			if err != nil || len(plan.Attempts) != 0 {
				t.Fatal("gate/readback generated Plan")
			}
		})
	}
}

func TestAuthoringOutputAfterClosingCannotPublish(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	provider.before = func() { s.mu.Lock(); s.closing = true; s.mu.Unlock() }
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	got, err := s.GenerateAuthoringStage(in)
	if err != nil || got.State != "paused" || len(got.Artifacts) != 0 || got.Attempts[0].ErrorCode != "cancelled" {
		t.Fatalf("closing allowed publication: %+v %v", got, err)
	}
	stored, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	if err != nil || len(stored.Artifacts) != 0 {
		t.Fatal("late artifact persisted")
	}
}

type failedAuthoringFenceStore struct{ Store }

func (s failedAuthoringFenceStore) FailAuthoringStage(context.Context, catalog.FailAuthoringStageRequest) (catalog.AuthoringStageRun, error) {
	return catalog.AuthoringStageRun{}, errors.New("storage unavailable")
}

func TestAuthoringFailedShutdownWriteDoesNotProveFence(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	fencer, ok := any(s).(interface {
		fenceAuthoringOwner(context.Context, string, catalog.AuthoringStageRequest) bool
	})
	if !ok {
		t.Fatal("shutdown lacks verified fencing")
	}
	choice, err := s.prepareAPIModelSelection(t.Context(), in.Selection)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := db.BeginAuthoringStage(t.Context(), catalog.BeginAuthoringStageRequest{Ref: in.Ref.request(), Selection: *choice, ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", PreferenceRevision: 0, EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	ref := in.Ref.request()
	ref.PipelineRevision++
	ref.StageRevision++
	s.store = failedAuthoringFenceStore{db}
	if fencer.fenceAuthoringOwner(t.Context(), a.ID, ref) {
		t.Fatal("failed write reported a fence while attempt is still current/running")
	}
	ref.RequestID = "cleanup_failed_fence"
	if _, err := db.CancelAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: ref, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
}
