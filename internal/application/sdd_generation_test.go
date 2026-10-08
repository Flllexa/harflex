package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

type corruptBrainstormJournal struct{ Store }

func (s corruptBrainstormJournal) WalkAfter(context.Context, string, int64, int, int, func(events.Event) error) error {
	return ErrSessionCorrupt
}

type interruptedBrainstormReadback struct {
	Store
	calls   int
	failure error
}

func (s *interruptedBrainstormReadback) ValidateBrainstormSession(ctx context.Context, ref catalog.BrainstormRequest, attemptID, sessionID string) (catalog.BrainstormAttempt, error) {
	s.calls++
	if s.calls == 2 {
		return catalog.BrainstormAttempt{}, s.failure
	}
	return s.Store.ValidateBrainstormSession(ctx, ref, attemptID, sessionID)
}

func TestBrainstormGenerationCancelledReadbackKeepsClassification(t *testing.T) {
	for _, test := range []struct {
		failure error
		code    string
	}{{context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timeout"}} {
		t.Run(test.code, func(t *testing.T) {
			s, db, start, _, _ := brainstormingServiceFixture(t)
			run, err := s.StartBrainstorming(start)
			if err != nil {
				t.Fatal(err)
			}
			fake := &fakeProvider{output: `{"question":"Which scope?"}`}
			s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return fake, nil }
			s.store = &interruptedBrainstormReadback{Store: db, failure: test.failure}
			got, err := s.GenerateBrainstormQuestion(generationInput(run, start.Selection, "cancelled_readback1"))
			if err != nil || got.State != "paused" || got.Attempts[0].ErrorCode != test.code || fake.request.Model != "" {
				t.Fatalf("readback classification: %+v %v", got, err)
			}
		})
	}
}

func TestBrainstormGenerationRestartReplayNeverResumesProvider(t *testing.T) {
	s, db, start, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	command := generationInput(run, start.Selection, "interrupted_request1")
	if _, admitted, err := s.admitBrainstormCommand(command, "question", 4096); err != nil || !admitted {
		t.Fatalf("seed admission: %v admitted=%v", err, admitted)
	}
	if err := db.InterruptRunningBrainstormAttempts(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(t.Context(), Dependencies{Store: db, External: map[string]ExternalBackend{}})
	restarted.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	restarted.providerFactory = func(openai.Config) (agentcore.Provider, error) { t.Fatal("restart resumed provider"); return nil, nil }
	t.Cleanup(func() { Shutdown(restarted) })
	command.Selection.CredentialToken = ""
	got, err := restarted.GenerateBrainstormQuestion(command)
	if err != nil || got.State != "paused" || len(got.Attempts) != 1 || got.Attempts[0].Status != "interrupted" || len(got.Turns) != 0 {
		t.Fatalf("restart readback: %+v %v", got, err)
	}
}

func TestBrainstormGenerationUncertainAdmissionCannotCallProvider(t *testing.T) {
	s, _, start, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	s.store = uncertainBrainstormStore{s.store}
	got, err := s.GenerateBrainstormQuestion(generationInput(run, start.Selection, "uncertain_generate1"))
	if err != nil || got.State != "running_question" || len(got.Attempts) != 1 || got.Attempts[0].SessionID != "" {
		t.Fatalf("uncertain admission: %+v %v", got, err)
	}
}

func TestBrainstormGenerationCorruptJournalNeverPublishes(t *testing.T) {
	s, db, start, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{output: `{"question":"Which scope?"}`}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return fake, nil }
	s.store = corruptBrainstormJournal{db}
	got, err := s.GenerateBrainstormQuestion(generationInput(run, start.Selection, "corrupt_question_01"))
	if err != nil || got.State != "paused" || len(got.Turns) != 0 || got.Attempts[0].ErrorCode != "invalid_output" {
		t.Fatalf("corruption: %+v %v", got, err)
	}
}

func TestBrainstormGenerationContextShrinkAfterAdmissionCannotSpend(t *testing.T) {
	s, _, _ := setup(t)
	t.Cleanup(func() { Shutdown(s) })
	var contextLength atomic.Int64
	contextLength.Store(8192)
	s.modelHTTPClient = &http.Client{Transport: brainstormHTTPTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"data":[{"id":"selected-model","context_length":%d}],"total_count":1}`, contextLength.Load())))}, nil
	})}
	profile := profileInput()
	profile.ID, profile.ProviderType, profile.BaseURL = "api", "openrouter", "https://provider.example/api/v1"
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: w.ID, RequestID: "context_pipeline_01", Discovery: "Immutable Discovery"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	choice := apiSelectionInput(result, profile.ID, "selected-model", 256)
	choice.ConfirmUnfiltered = true
	run, err := s.StartBrainstorming(StartBrainstormingInput{PipelineID: pipeline.ID, PipelineRevision: pipeline.Revision, DiscoveryVersion: 1, RequestID: "context_start_0001", Selection: choice})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{output: `{"question":"Which scope?"}`}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { contextLength.Store(1024); return fake, nil }
	got, err := s.GenerateBrainstormQuestion(generationInput(run, choice, "context_generate_01"))
	if err != nil || got.State != "paused" || len(got.Turns) != 0 || got.Attempts[0].ErrorCode != "provider_failed" || got.Attempts[0].Selection.ContextLength != 8192 || fake.request.Model != "" {
		t.Fatalf("context drift: %+v %v request=%+v", got, err, fake.request)
	}
}

type brainstormTestProvider struct {
	events   []agentcore.StreamEvent
	err      error
	requests []agentcore.ChatRequest
	calls    atomic.Int32
	before   func()
}

type brainstormHTTPTransport func(*http.Request) (*http.Response, error)

func (f brainstormHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBrainstormGenerationExactHTTPBodyAndReservation(t *testing.T) {
	s, _, start, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	var body []byte
	s.providerFactory = func(config openai.Config) (agentcore.Provider, error) {
		config.HTTPClient = &http.Client{Transport: brainstormHTTPTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
				t.Fatalf("unexpected HTTP call: %s %s", r.Method, r.URL.Path)
			}
			var err error
			body, err = io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"question\\\":\\\"Which scope?\\\"}\"}}]}\n\ndata: [DONE]\n\n"))}, nil
		})}
		return openai.New(config)
	}
	got, err := s.GenerateBrainstormQuestion(generationInput(run, start.Selection, "http_question_00001"))
	if err != nil || got.State != "waiting_answer" {
		t.Fatalf("HTTP generation: %+v %v", got, err)
	}
	var wire struct {
		Model           string              `json:"model"`
		Messages        []agentcore.Message `json:"messages"`
		Tools           json.RawMessage     `json:"tools"`
		MaxOutputTokens int                 `json:"max_completion_tokens"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Model != start.Selection.ModelID || len(wire.Messages) != 1 || wire.Messages[0].Role != agentcore.RoleUser || wire.MaxOutputTokens != 256 || len(wire.Tools) != 0 || got.Attempts[0].ReservedInputTokens != int64(len(body))+1024 {
		t.Fatalf("wire mismatch: %s attempt=%+v", body, got.Attempts[0])
	}
}

func TestBrainstormGenerationCompositionFailureSettlesOnce(t *testing.T) {
	s, db, start, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return nil, errors.New("private factory failure") }
	got, err := s.GenerateBrainstormQuestion(generationInput(run, start.Selection, "compose_failure_001"))
	if err != nil || got.State != "paused" || got.Revision != run.Revision+2 || got.Attempts[0].ErrorCode != "provider_failed" {
		t.Fatalf("composition failure: %+v %v", got, err)
	}
	stored, err := db.GetBrainstorming(t.Context(), run.ID)
	if err != nil || stored.Attempts[0].SessionID == "" {
		t.Fatalf("link lost: %+v %v", stored, err)
	}
}

type afterMissingBrainstormReceipt struct {
	Store
	after func()
	fired bool
}

func (s *afterMissingBrainstormReceipt) GetBrainstormCommand(ctx context.Context, runID, requestID string) (catalog.BrainstormCommandReceipt, error) {
	receipt, err := s.Store.GetBrainstormCommand(ctx, runID, requestID)
	if err != nil && !s.fired {
		s.fired = true
		s.after()
	}
	return receipt, err
}

func TestBrainstormGenerationReplayWinsOverConcurrentSnapshotChange(t *testing.T) {
	s, db, start, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: `{"question":"Only once?"}`}}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	command := generationInput(run, start.Selection, "racing_receipt_0001")
	s.store = &afterMissingBrainstormReceipt{Store: db, after: func() {
		if _, err := s.GenerateBrainstormQuestion(command); err != nil {
			t.Fatal(err)
		}
	}}
	got, err := s.GenerateBrainstormQuestion(command)
	if err != nil || got.State != "waiting_answer" || provider.calls.Load() != 1 {
		t.Fatalf("snapshot race: %+v %v calls=%d", got, err, provider.calls.Load())
	}
}

func (*brainstormTestProvider) ID() string { return "brainstorm-fake" }
func (*brainstormTestProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}
func (p *brainstormTestProvider) Stream(_ context.Context, request agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	p.calls.Add(1)
	p.requests = append(p.requests, request)
	if p.before != nil {
		p.before()
	}
	events := make(chan agentcore.StreamEvent, len(p.events))
	errs := make(chan error, 1)
	for _, event := range p.events {
		events <- event
	}
	if p.err != nil {
		errs <- p.err
	}
	close(events)
	close(errs)
	return events, errs
}

func generationInput(run BrainstormDTO, selection APIModelSelectionInput, request string) GenerateBrainstormInput {
	return GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: request, PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: run.DiscoveryVersion}, Selection: selection}
}

func TestBrainstormGenerationFailuresAreTerminalAndSanitized(t *testing.T) {
	for _, test := range []struct {
		name, output, code string
		tool               bool
		failure            error
	}{
		{name: "malformed", output: `{"question":`, code: "invalid_output"},
		{name: "overflow", output: strings.Repeat("x", 2049), code: "output_overflow"},
		{name: "tool", tool: true, code: "invalid_output"},
		{name: "provider", failure: errors.New("private-provider-content"), code: "provider_failed"},
		{name: "timeout", failure: context.DeadlineExceeded, code: "timeout"},
		{name: "cancelled", failure: context.Canceled, code: "cancelled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, start, _, _ := brainstormingServiceFixture(t)
			run, err := s.StartBrainstorming(start)
			if err != nil {
				t.Fatal(err)
			}
			provider := &brainstormTestProvider{err: test.failure}
			if test.tool {
				provider.events = []agentcore.StreamEvent{{Type: "tool_call", ToolCall: &agentcore.ToolCall{ID: "call", Name: "write_file", Arguments: json.RawMessage(`{"path":"forbidden"}`)}}}
			} else if test.output != "" {
				provider.events = []agentcore.StreamEvent{{Type: "text_delta", Delta: test.output}}
			}
			s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
			command := generationInput(run, start.Selection, "failure_question_01")
			got, err := s.GenerateBrainstormQuestion(command)
			if err != nil || got.State != "paused" || len(got.Turns) != 0 || len(got.Attempts) != 1 || got.Attempts[0].ErrorCode != test.code || got.Attempts[0].Status != "failed" {
				t.Fatalf("failure readback: %+v err=%v", got, err)
			}
			s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
			replay, err := s.GenerateBrainstormQuestion(command)
			if err != nil || !reflect.DeepEqual(replay, got) || provider.calls.Load() != 1 {
				t.Fatalf("failure replay: %+v err=%v calls=%d", replay, err, provider.calls.Load())
			}
		})
	}
}

func TestBrainstormGenerationUsageAndOverrun(t *testing.T) {
	for _, test := range []struct {
		name    string
		usages  []*agentcore.Usage
		want    *agentcore.Usage
		overrun bool
	}{
		{name: "unknown"},
		{name: "negative", usages: []*agentcore.Usage{{InputTokens: -1, OutputTokens: 1}}},
		{name: "latest", usages: []*agentcore.Usage{{InputTokens: 10, OutputTokens: 1}, {InputTokens: 20, OutputTokens: 2}}, want: &agentcore.Usage{InputTokens: 20, OutputTokens: 2}},
		{name: "overrun", usages: []*agentcore.Usage{{InputTokens: 10, OutputTokens: 257}}, overrun: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, start, _, _ := brainstormingServiceFixture(t)
			run, err := s.StartBrainstorming(start)
			if err != nil {
				t.Fatal(err)
			}
			provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: `{"question":"Which scope?"}`}}}
			for _, usage := range test.usages {
				provider.events = append(provider.events, agentcore.StreamEvent{Type: "usage", Usage: usage})
			}
			s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
			got, err := s.GenerateBrainstormQuestion(generationInput(run, start.Selection, "usage_question_0001"))
			if test.overrun {
				if !errors.Is(err, sdd.ErrBrainstormBudgetExceeded) || ErrorCode(err) != "brainstorm_budget_exceeded" || got.State != "paused" || len(got.Turns) != 0 || got.Attempts[0].ErrorCode != "budget_overrun" {
					t.Fatalf("overrun: %+v %v", got, err)
				}
				return
			}
			if err != nil || got.State != "waiting_answer" {
				t.Fatalf("usage generation: %+v %v", got, err)
			}
			usage := got.Attempts[0].Usage
			if test.want == nil {
				if usage != nil {
					t.Fatalf("unknown usage: %+v", usage)
				}
			} else if usage == nil || usage.InputTokens != test.want.InputTokens || usage.OutputTokens != test.want.OutputTokens || usage.CostUSD != nil {
				t.Fatalf("usage: %+v", usage)
			}
		})
	}
}

func TestBrainstormGenerationSynthesisCommandsAndFifthAnswer(t *testing.T) {
	for _, sufficient := range []bool{true, false} {
		t.Run(fmt.Sprint(sufficient), func(t *testing.T) {
			s, _, start, _, _ := brainstormingServiceFixture(t)
			run, err := s.StartBrainstorming(start)
			if err != nil {
				t.Fatal(err)
			}
			provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: `{"question":"Confirmed scope?"}`}}}
			s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
			count := 5
			if sufficient {
				count = 1
			}
			for i := 0; i < count; i++ {
				run, err = s.GenerateBrainstormQuestion(generationInput(run, start.Selection, fmt.Sprintf("synthesis_question_%02d", i)))
				if err != nil {
					t.Fatal(err)
				}
				run, err = s.AnswerBrainstormQuestion(AnswerBrainstormQuestionInput{Ref: generationInput(run, start.Selection, fmt.Sprintf("synthesis_answer_%02d", i)).Ref, QuestionID: run.CurrentQuestionID, Answer: "Human confirmed"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if provider.calls.Load() != int32(count) || len(run.Syntheses) != 0 {
				t.Fatal("answer spent automatically")
			}
			if sufficient {
				provider.events = []agentcore.StreamEvent{{Type: "text_delta", Delta: `{"question":"Pending omitted question?"}`}}
				run, err = s.GenerateBrainstormQuestion(generationInput(run, start.Selection, "pending_question_001"))
				if err != nil {
					t.Fatal(err)
				}
			} else if run.State != "ready_for_synthesis" {
				t.Fatalf("fifth answer: %+v", run)
			}
			provider.events = []agentcore.StreamEvent{{Type: "text_delta", Delta: `{"scope":"Confirmed scope","decisions":["Human confirmed"],"openQuestions":[]}`}}
			command := generationInput(run, start.Selection, "synthesize_command_1")
			if sufficient {
				run, err = s.QuestionsSufficient(command)
			} else {
				run, err = s.FinishAndGenerateSynthesis(command)
			}
			if err != nil || run.State != "waiting_user" || len(run.Syntheses) != 1 {
				t.Fatalf("synthesis: %+v %v", run, err)
			}
			request := provider.requests[len(provider.requests)-1]
			if request.MaxOutputTokens != 1024 || len(request.Tools) != 0 || len(request.Messages) != 1 || strings.Contains(request.Messages[0].Content, "Pending omitted") || !strings.Contains(request.Messages[0].Content, "Human confirmed") {
				t.Fatalf("synthesis request: %+v", request)
			}
			if sufficient && run.Turns[1].Status != "skipped" {
				t.Fatalf("pending question not skipped: %+v", run.Turns)
			}
			pipeline, err := s.GetPipeline(start.PipelineID)
			if err != nil || pipeline.CurrentStage != "discovery" || pipeline.StageStatus["discovery"] != "waiting_user" {
				t.Fatalf("pipeline: %+v %v", pipeline, err)
			}
			s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
			var replay BrainstormDTO
			if sufficient {
				replay, err = s.QuestionsSufficient(command)
			} else {
				replay, err = s.FinishAndGenerateSynthesis(command)
			}
			if err != nil || !reflect.DeepEqual(replay, run) {
				t.Fatalf("synthesis replay: %+v %v", replay, err)
			}
		})
	}
}

func TestBrainstormGenerationStaleDiscoveryNeverPublishes(t *testing.T) {
	s, _, start, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: `{"question":"Old discovery question?"}`}}}
	provider.before = func() {
		pipeline, err := s.GetPipeline(start.PipelineID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.ReviseAuthoringDiscovery(ReviseAuthoringDiscoveryInput{PipelineID: pipeline.ID, ExpectedRevision: pipeline.Revision, ExpectedVersion: 1, Discovery: "Revised Discovery"})
		if err != nil {
			t.Fatal(err)
		}
	}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	got, err := s.GenerateBrainstormQuestion(generationInput(run, start.Selection, "stale_question_0001"))
	if err != nil || got.State != "invalidated" || len(got.Turns) != 0 || got.Attempts[0].Status != "stale" {
		t.Fatalf("stale: %+v %v", got, err)
	}
}

func TestBrainstormGenerationTwoServicesOnlyOneProviderCall(t *testing.T) {
	s, db, start, _, path := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	other, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: `{"question":"Only once?"}`}}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	peer := NewService(t.Context(), Dependencies{Store: other, Secrets: s.secrets, External: map[string]ExternalBackend{}, ProviderFactory: s.providerFactory})
	peer.modelHTTPClient = s.modelHTTPClient
	t.Cleanup(func() { Shutdown(peer) })
	stored, err := db.GetBrainstorming(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	command := generationInput(run, start.Selection, "concurrent_generate_1")
	peerCommand := command
	c := command.Selection
	peerCommand.Selection.CredentialToken = peer.catalogSelectionToken(stored.Selection.CredentialIdentity, c.ProfileID, c.CatalogRevision, c.Source, c.Destination, c.CheckedAt)
	results := make(chan error, 2)
	go func() { _, err := s.GenerateBrainstormQuestion(command); results <- err }()
	go func() { _, err := peer.GenerateBrainstormQuestion(peerCommand); results <- err }()
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetBrainstorming(GetBrainstormingInput{RunID: run.ID})
	if err != nil || provider.calls.Load() != 1 || len(got.Attempts) != 1 || len(got.Turns) != 1 {
		t.Fatalf("concurrency: %+v %v calls=%d", got, err, provider.calls.Load())
	}
}

func TestBrainstormGenerationPublishesQuestionAndReplaysOffline(t *testing.T) {
	s, db, start, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{output: `{"question":"Which scope?"}`}
	calls := 0
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { calls++; return fake, nil }
	command := generationInput(run, start.Selection, "generate_question_01")
	generated, err := s.GenerateBrainstormQuestion(command)
	if err != nil {
		t.Fatal(err)
	}
	if generated.State != "waiting_answer" || generated.QuestionCount != 1 || len(generated.Turns) != 1 || generated.Turns[0].Question != "Which scope?" || generated.Attempts[0].Status != "completed" || calls != 1 {
		t.Fatalf("generation: %+v calls=%d", generated, calls)
	}
	if fake.request.MaxOutputTokens != 256 || len(fake.request.Tools) != 0 || len(fake.request.Messages) != 1 || fake.request.Model != start.Selection.ModelID {
		t.Fatalf("request: %+v", fake.request)
	}
	stored, err := db.GetBrainstorming(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	size, err := openai.RequestSize(fake.request, "openai")
	if err != nil || stored.Attempts[0].ReservedInputTokens != int64(size)+1024 || stored.Attempts[0].Usage != nil {
		t.Fatalf("reservation/unknown usage: %+v size=%d err=%v", stored.Attempts[0], size, err)
	}
	s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	command.Selection.CredentialToken = ""
	replayed, err := s.GenerateBrainstormQuestion(command)
	if err != nil || !reflect.DeepEqual(replayed, generated) || calls != 1 {
		t.Fatalf("offline replay: %+v %v calls=%d", replayed, err, calls)
	}
}
