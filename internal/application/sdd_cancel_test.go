package application

import (
	"context"
	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"net/http"
	"testing"
	"time"
)

func TestSDDCancelBeforePromptAndDuringPreflight(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_prompt", true: "preflight"}[blocked], func(t *testing.T) {
			fake := &fakeProvider{}
			s, db, ref, a := sddSessionFixture(t, fake)
			session, err := s.createSDDReadOnlySession(ref, a)
			if err != nil {
				t.Fatal(err)
			}
			a.SessionID = session.ID
			done := make(chan struct{})
			if blocked {
				entered := make(chan struct{})
				s.modelHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					close(entered)
					<-r.Context().Done()
					return nil, r.Context().Err()
				})}
				go func() { _, _ = s.promptSDDAttempt(t.Context(), ref, a, "bounded"); close(done) }()
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("no preflight")
				}
			}
			input := CancelBrainstormAttemptInput{Ref: BrainstormRequestInput{RunID: ref.RunID, RequestID: "cancel_request_01", RunRevision: ref.RunRevision, PipelineRevision: ref.PipelineRevision, DiscoveryVersion: ref.DiscoveryVersion}, AttemptID: a.ID}
			got, err := s.CancelBrainstormAttempt(input)
			if err != nil || got.State != "paused" {
				t.Fatalf("cancel %+v %v", got, err)
			}
			if blocked {
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("preflight not cancelled")
				}
			} else {
				if _, err := s.promptSDDAttempt(t.Context(), ref, a, "bounded"); err == nil {
					t.Fatal("cancelled prompt admitted")
				}
			}
			if fake.request.Model != "" {
				t.Fatal("provider called")
			}
			if _, err := s.failBrainstormGeneration(ref, a, "cancelled"); err != nil {
				t.Fatal(err)
			}
			current, err := db.GetBrainstorming(t.Context(), ref.RunID)
			if err != nil || current.State != "paused" || current.Attempts[0].Status != "interrupted" {
				t.Fatalf("settlement %+v %v", current, err)
			}
			resumed, err := s.ResumePausedBrainstorm(BrainstormRequestInput{RunID: ref.RunID, RequestID: "resume_request_01", RunRevision: current.Revision, PipelineRevision: current.PipelineRevision, DiscoveryVersion: current.DiscoveryVersion})
			if err != nil || resumed.State != "ready" {
				t.Fatalf("resume %+v %v", resumed, err)
			}
		})
	}
}

type cancelBarrierStore struct {
	Store
	persisted chan struct{}
}

func (s cancelBarrierStore) CancelBrainstormAttempt(ctx context.Context, in catalog.CancelBrainstormAttemptRequest) (catalog.BrainstormRun, error) {
	run, err := s.Store.CancelBrainstormAttempt(ctx, in)
	if err == nil {
		close(s.persisted)
	}
	return run, err
}

func TestSDDCancelLateProviderResultCannotPublishOrReplay(t *testing.T) {
	s, db, start, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	persisted := make(chan struct{})
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: `{"question":"Late scope?"}`}}}
	provider.before = func() { close(entered); <-persisted }
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	s.store = cancelBarrierStore{Store: db, persisted: persisted}
	command := generationInput(run, start.Selection, "generation_request1")
	done := make(chan BrainstormDTO, 1)
	errs := make(chan error, 1)
	go func() { got, err := s.GenerateBrainstormQuestion(command); done <- got; errs <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not started")
	}
	current, err := db.GetBrainstorming(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := CancelBrainstormAttemptInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "cancel_request_01", RunRevision: current.Revision, PipelineRevision: current.PipelineRevision, DiscoveryVersion: 1}, AttemptID: current.Attempts[0].ID}
	paused, err := s.CancelBrainstormAttempt(input)
	if err != nil || paused.State != "paused" {
		t.Fatalf("cancel %+v %v", paused, err)
	}
	select {
	case got := <-done:
		if err := <-errs; err != nil || got.State != "paused" || len(got.Turns) != 0 {
			t.Fatalf("late result %+v %v", got, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("generation stuck")
	}
	s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { t.Fatal("provider replayed"); return nil, nil }
	current, err = db.GetBrainstorming(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ResumePausedBrainstorm(BrainstormRequestInput{RunID: run.ID, RequestID: "resume_request_01", RunRevision: current.Revision, PipelineRevision: current.PipelineRevision, DiscoveryVersion: 1})
	if err != nil || resumed.State != "ready" {
		t.Fatalf("resume %+v %v", resumed, err)
	}
	replay, err := s.GenerateBrainstormQuestion(command)
	if err != nil || replay.State != "ready" || len(replay.Attempts) != 1 {
		t.Fatalf("replay %+v %v", replay, err)
	}
}
