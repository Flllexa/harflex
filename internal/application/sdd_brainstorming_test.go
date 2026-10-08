package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func brainstormingServiceFixture(t *testing.T) (*Service, *sqlite.Store, StartBrainstormingInput, *atomic.Int32, string) {
	t.Helper()
	calls := new(atomic.Int32)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"selected-model"}]}`)
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "brainstorm.db")
	db, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	vault := &memorySecrets{values: map[secrets.Reference]string{}}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}})
	s.modelHTTPClient = server.Client()
	profile := profileInput()
	profile.ID, profile.ProviderType, profile.BaseURL = "api", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: w.ID, RequestID: "create_pipeline_001", Discovery: "Immutable Discovery"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		t.Fatal("read/admission must not compose provider")
		return nil, nil
	}
	t.Cleanup(func() { Shutdown(s) })
	return s, db, StartBrainstormingInput{PipelineID: pipeline.ID, RequestID: "start_brainstorm_01", PipelineRevision: pipeline.Revision, DiscoveryVersion: 1, Selection: apiSelectionInput(result, profile.ID, "selected-model", 256)}, calls, path
}

func TestBrainstormStartAndOfflineReplay(t *testing.T) {
	s, db, in, calls, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(in)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != "ready" || len(run.Attempts) != 0 || run.DiscoveryContent != "Immutable Discovery" || run.PipelineRevision != 2 {
		t.Fatalf("start: %+v", run)
	}
	count := calls.Load()
	s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	in.Selection.CredentialToken = "expired-or-missing-token"
	again, err := s.StartBrainstorming(in)
	if err != nil || again.ID != run.ID || calls.Load() != count {
		t.Fatalf("offline replay: %+v %v calls=%d", again, err, calls.Load())
	}
	byPipeline, err := s.GetBrainstorming(GetBrainstormingInput{PipelineID: in.PipelineID, DiscoveryVersion: 1})
	if err != nil || byPipeline.ID != run.ID {
		t.Fatalf("pipeline read: %+v %v", byPipeline, err)
	}
	current, err := s.GetBrainstorming(GetBrainstormingInput{PipelineID: in.PipelineID})
	if err != nil || current.ID != run.ID {
		t.Fatalf("current pipeline read: %+v %v", current, err)
	}
	stored, _ := db.GetBrainstorming(t.Context(), run.ID)
	data, _ := json.Marshal(run)
	if strings.Contains(string(data), stored.Selection.CredentialIdentity) || strings.Contains(string(data), stored.StartClientIntentHash) {
		t.Fatal("private fingerprint/hash leaked")
	}
	in.Selection.ModelID = "changed"
	if _, err := s.StartBrainstorming(in); !errors.Is(err, ErrPipelineRequestConflict) {
		t.Fatalf("changed payload: %v", err)
	}
	if calls.Load() != count {
		t.Fatal("replay/conflict queried catalog")
	}
}

func TestBrainstormStartRejectsCodexCLISelectionWithoutReadIsolation(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "codex_cli_pipeline", Discovery: "Create a bounded TODO app"})
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0", efforts: []string{"low", "high"}}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil || !listed.Complete || listed.Status != "complete" {
		t.Fatalf("Codex catalog: %+v %v", listed, err)
	}
	payload, err := json.Marshal(map[string]any{
		"pipelineId": pipeline.ID, "requestId": "codex_cli_start_0001", "pipelineRevision": pipeline.Revision, "discoveryVersion": 1,
		"selection": map[string]any{
			"executor": "codex_cli", "backendId": "codex", "modelId": "provider/model-exact",
			"catalogRevision": listed.ProfileRevision, "source": listed.Source, "destination": "",
			"checkedAt": listed.CheckedAt, "reasoningEffort": "high", "maxOutputTokens": 256,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var input StartBrainstormingInput
	if err := json.Unmarshal(payload, &input); err != nil {
		t.Fatal(err)
	}
	queries := stub.queryCount
	if _, err := s.StartBrainstorming(input); !errors.Is(err, ErrSDDCLIReadIsolationUnavailable) {
		t.Fatalf("unconfined Codex CLI was admitted to Professional Discovery: %v", err)
	}
	if stub.queryCount != queries {
		t.Fatalf("blocked Codex CLI request queried the catalog again: before=%d after=%d", queries, stub.queryCount)
	}
	var runs int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM pipeline_brainstorm_runs WHERE pipeline_id=?`, pipeline.ID).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("blocked Codex CLI created %d brainstorm runs: %v", runs, err)
	}
}

func TestBrainstormAdmissionAndOfflineReplay(t *testing.T) {
	s, db, in, calls, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(in)
	if err != nil {
		t.Fatal(err)
	}
	command := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "question_command_01", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: in.Selection}
	attempt, admitted, err := s.admitBrainstormCommand(command, "question", 4096)
	if err != nil || !admitted || attempt.Selection.MaxOutputTokens != 256 || attempt.SessionID != "" {
		t.Fatalf("admit: %+v %v %v", attempt, admitted, err)
	}
	count := calls.Load()
	s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	command.Selection.CredentialToken = ""
	replay, admitted, err := s.admitBrainstormCommand(command, "question", 4096)
	if err != nil || admitted || replay.ID != attempt.ID || calls.Load() != count {
		t.Fatalf("replay: %+v %v %v", replay, admitted, err)
	}
	stored, _ := db.GetBrainstorming(t.Context(), run.ID)
	if len(stored.Attempts) != 1 || stored.InputBudgetRemaining != run.InputBudgetRemaining-4096 {
		t.Fatalf("duplicate reservation: %+v", stored)
	}
	command.Ref.RunRevision++
	if _, admitted, err := s.admitBrainstormCommand(command, "question", 4096); !errors.Is(err, ErrPipelineRequestConflict) || admitted {
		t.Fatalf("mutated replay: %v %v", admitted, err)
	}
}

type uncertainBrainstormStore struct{ Store }

func (s uncertainBrainstormStore) StartBrainstorming(ctx context.Context, in catalog.StartBrainstormingRequest) (catalog.BrainstormRun, error) {
	r, err := s.Store.StartBrainstorming(ctx, in)
	if err != nil {
		return r, err
	}
	return r, errors.New("uncertain commit")
}
func (s uncertainBrainstormStore) BeginBrainstormQuestion(ctx context.Context, in catalog.BeginBrainstormAttemptRequest) (catalog.BrainstormAttempt, bool, error) {
	a, _, err := s.Store.BeginBrainstormQuestion(ctx, in)
	if err != nil {
		return a, false, err
	}
	return a, false, errors.New("uncertain commit")
}

func TestBrainstormUncertainAdmissionNeverAuthorizesInference(t *testing.T) {
	s, _, in, _, _ := brainstormingServiceFixture(t)
	s.store = uncertainBrainstormStore{s.store}
	run, err := s.StartBrainstorming(in)
	if err != nil {
		t.Fatal(err)
	}
	input := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "uncertain_command_1", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: in.Selection}
	attempt, admitted, err := s.admitBrainstormCommand(input, "question", 4096)
	if err != nil || admitted || attempt.ID == "" {
		t.Fatalf("uncertain result: %+v %v %v", attempt, admitted, err)
	}
}

type offlineBrainstormCatalog struct{ t *testing.T }

type afterBrainstormReadStore struct {
	Store
	after func()
}

func (s afterBrainstormReadStore) GetBrainstorming(ctx context.Context, id string) (catalog.BrainstormRun, error) {
	run, err := s.Store.GetBrainstorming(ctx, id)
	if err == nil {
		s.after()
	}
	return run, err
}

func TestBrainstormDTOUsesAtomicStoreSnapshot(t *testing.T) {
	s, db, in, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(in)
	if err != nil {
		t.Fatal(err)
	}
	s.store = afterBrainstormReadStore{Store: db, after: func() {
		if err := db.ReviseAuthoringDiscovery(t.Context(), in.PipelineID, run.PipelineRevision, 1, "New Discovery", "New title", "New objective"); err != nil {
			t.Fatal(err)
		}
	}}
	read, err := s.GetBrainstorming(GetBrainstormingInput{RunID: run.ID})
	if err != nil || read.PipelineRevision != run.PipelineRevision || read.Revision != run.Revision || read.State != run.State {
		t.Fatalf("mixed snapshot: %+v %v", read, err)
	}
	latest, err := db.GetPipeline(t.Context(), in.PipelineID)
	if err != nil || latest.Revision != run.PipelineRevision+1 {
		t.Fatalf("concurrent revision not committed: %+v %v", latest, err)
	}
}

func (o offlineBrainstormCatalog) RoundTrip(*http.Request) (*http.Response, error) {
	o.t.Error("offline replay accessed catalog")
	return nil, errors.New("offline")
}

func TestBrainstormRestartReadbackDoesNotSpend(t *testing.T) {
	s, db, in, _, path := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(in)
	if err != nil {
		t.Fatal(err)
	}
	command := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "restart_question_01", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: in.Selection}
	attempt, _, err := s.admitBrainstormCommand(command, "question", 4096)
	if err != nil {
		t.Fatal(err)
	}
	Shutdown(s)
	other, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	restarted := NewService(t.Context(), Dependencies{Store: other, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) {
		t.Error("restart composed provider")
		return nil, errors.New("forbidden")
	}})
	t.Cleanup(func() { Shutdown(restarted) })
	restarted.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	if err := other.InterruptRunningBrainstormAttempts(t.Context()); err != nil {
		t.Fatal(err)
	}
	replay, err := restarted.StartBrainstorming(in)
	if err != nil || replay.State != "paused" || len(replay.Attempts) != 1 {
		t.Fatalf("start restart readback: %+v %v", replay, err)
	}
	prior, admitted, err := restarted.admitBrainstormCommand(command, "question", 4096)
	if err != nil || admitted || prior.ID != attempt.ID || prior.Status != "interrupted" {
		t.Fatalf("attempt restart: %+v %v %v", prior, admitted, err)
	}
	after, _ := db.GetBrainstorming(t.Context(), run.ID)
	if after.AttemptCount != 1 || after.InputBudgetRemaining != run.InputBudgetRemaining-4096 {
		t.Fatalf("restart spent: %+v", after)
	}
}

func TestBrainstormAdmissionAcrossTwoStores(t *testing.T) {
	s, _, in, _, path := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(in)
	if err != nil {
		t.Fatal(err)
	}
	other, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	copiedSecrets := &memorySecrets{values: map[secrets.Reference]string{}}
	for key, value := range s.secrets.(*memorySecrets).values {
		copiedSecrets.values[key] = value
	}
	peer := NewService(t.Context(), Dependencies{Store: other, Secrets: copiedSecrets, External: map[string]ExternalBackend{}, ProviderFactory: s.providerFactory})
	peer.modelHTTPClient = s.modelHTTPClient
	t.Cleanup(func() { Shutdown(peer) })
	stored, _ := other.GetBrainstorming(t.Context(), run.ID)
	command := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "concurrent_question_1", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: in.Selection}
	peerCommand := command
	c := peerCommand.Selection
	peerCommand.Selection.CredentialToken = peer.catalogSelectionToken(stored.Selection.CredentialIdentity, c.ProfileID, c.CatalogRevision, c.Source, c.Destination, c.CheckedAt)
	type result struct {
		a        catalog.BrainstormAttempt
		admitted bool
		err      error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, v := range []struct {
		s  *Service
		in GenerateBrainstormInput
	}{{s, command}, {peer, peerCommand}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, admitted, err := v.s.admitBrainstormCommand(v.in, "question", 4096)
			results <- result{a, admitted, err}
		}()
	}
	wg.Wait()
	close(results)
	admittedCount := 0
	attemptID := ""
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.admitted {
			admittedCount++
		}
		if attemptID != "" && attemptID != r.a.ID {
			t.Fatal("two attempts")
		}
		attemptID = r.a.ID
	}
	if admittedCount != 1 {
		t.Fatalf("admissions=%d", admittedCount)
	}
}

func TestBrainstormAnswersNeverGenerateAndRequireExactQuestion(t *testing.T) {
	s, db, in, calls, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(in)
	if err != nil {
		t.Fatal(err)
	}
	for number := 1; number <= 5; number++ {
		command := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: fmt.Sprintf("question_number_%02d", number), PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: in.Selection}
		attempt, _, err := s.admitBrainstormCommand(command, "question", 4096)
		if err != nil {
			t.Fatal(err)
		}
		ref := command.Ref.request()
		ref.RunRevision++
		ref.PipelineRevision++
		completed, err := db.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: attempt.ID, SessionID: fmt.Sprintf("test_session_%02d", number), Question: "Which scope?"})
		if err != nil {
			t.Fatal(err)
		}
		run, err = s.GetBrainstorming(GetBrainstormingInput{RunID: completed.ID})
		if err != nil {
			t.Fatal(err)
		}
		answer := AnswerBrainstormQuestionInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: fmt.Sprintf("answer_number_%02d", number), PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, QuestionID: run.CurrentQuestionID, Answer: "Confirmed scope"}
		wrong := answer
		wrong.QuestionID = "different-question"
		if _, err := s.AnswerBrainstormQuestion(wrong); err == nil {
			t.Fatal("wrong question accepted")
		}
		before := calls.Load()
		run, err = s.AnswerBrainstormQuestion(answer)
		if err != nil {
			t.Fatal(err)
		}
		intent, err := brainstormIntentHash("answer", answer)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := db.GetBrainstormCommand(t.Context(), run.ID, answer.Ref.RequestID)
		if err != nil || receipt.ClientIntentHash != intent || receipt.Action != "answer" || receipt.AttemptID != answer.QuestionID {
			t.Fatalf("answer intent: %+v %v", receipt, err)
		}
		changed := answer
		changed.Answer = "Changed content"
		if _, err := s.AnswerBrainstormQuestion(changed); !errors.Is(err, sqlite.ErrPipelineConflict) {
			t.Fatalf("changed answer replay accepted: %v", err)
		}
		if _, err := db.DB().Exec(`UPDATE pipeline_brainstorm_requests SET client_intent_hash='changed' WHERE run_id=? AND request_id=?`, run.ID, answer.Ref.RequestID); err == nil {
			t.Fatal("mutable answer intent")
		}
		if calls.Load() != before || run.AttemptCount != number || len(run.Syntheses) != 0 || run.Turns[number-1].Answer != "Confirmed scope" {
			t.Fatalf("answer generated: %+v", run)
		}
		replay, err := s.AnswerBrainstormQuestion(answer)
		if err != nil || replay.Revision != run.Revision {
			t.Fatalf("answer replay: %+v %v", replay, err)
		}
	}
	if run.State != "ready_for_synthesis" {
		t.Fatalf("five answers: %s", run.State)
	}
	command := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "finish_synthesis_01", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: in.Selection}
	a, admitted, err := s.admitBrainstormCommand(command, "finish_synthesis", 4096)
	if err != nil || !admitted || a.Kind != "synthesis" || a.Selection.MaxOutputTokens != 1024 || a.ReservedOutputTokens != 1024 {
		t.Fatalf("explicit synthesis admission: %+v %v %v", a, admitted, err)
	}
}

func TestBrainstormAnswerRejectsMalformedQuestionIDBeforeStorage(t *testing.T) {
	s, _, _, _, _ := brainstormingServiceFixture(t)
	for _, questionID := range []string{"\xff", strings.Repeat("x", 129), "question\nline", ""} {
		_, err := s.AnswerBrainstormQuestion(AnswerBrainstormQuestionInput{Ref: BrainstormRequestInput{RunID: "run", RequestID: "invalid_question_01", PipelineRevision: 1, RunRevision: 1, DiscoveryVersion: 1}, QuestionID: questionID, Answer: "Confirmed answer"})
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("malformed question ID error=%v", err)
		}
	}
}

func TestBrainstormQuestionsSufficientSkipsPendingOnlyWithEarlierAnswer(t *testing.T) {
	s, db, in, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(in)
	if err != nil {
		t.Fatal(err)
	}
	for number := 1; number <= 2; number++ {
		command := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: fmt.Sprintf("pending_question_%02d", number), PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: in.Selection}
		a, _, err := s.admitBrainstormCommand(command, "question", 4096)
		if err != nil {
			t.Fatal(err)
		}
		ref := command.Ref.request()
		ref.RunRevision++
		ref.PipelineRevision++
		if _, err := db.CompleteBrainstormAttempt(t.Context(), catalog.CompleteBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: a.ID, SessionID: fmt.Sprintf("seed_session_%02d", number), Question: "What is needed?"}); err != nil {
			t.Fatal(err)
		}
		run, err = s.GetBrainstorming(GetBrainstormingInput{RunID: run.ID})
		if err != nil {
			t.Fatal(err)
		}
		if number == 1 {
			noAnswer := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "no_answer_sufficient", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: in.Selection}
			if _, admitted, err := s.admitBrainstormCommand(noAnswer, "questions_sufficient", 4096); err == nil || admitted {
				t.Fatal("zero-answer bypass admitted")
			}
			run, err = s.AnswerBrainstormQuestion(AnswerBrainstormQuestionInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "first_answer_00001", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, QuestionID: run.CurrentQuestionID, Answer: "Human confirmed"})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	command := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "enough_questions_01", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: in.Selection}
	a, admitted, err := s.admitBrainstormCommand(command, "questions_sufficient", 4096)
	if err != nil || !admitted || a.Selection.MaxOutputTokens != 1024 {
		t.Fatalf("sufficient: %+v %v %v", a, admitted, err)
	}
	state, _ := db.GetBrainstorming(t.Context(), run.ID)
	if state.Turns[0].Status != "answered" || state.Turns[1].Status != "skipped" || state.AttemptCount != 3 || state.State != "running_synthesis" {
		t.Fatalf("atomic skip: %+v", state)
	}
	s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	replay, admitted, err := s.admitBrainstormCommand(command, "questions_sufficient", 4096)
	if err != nil || admitted || replay.ID != a.ID {
		t.Fatalf("sufficient replay: %+v %v %v", replay, admitted, err)
	}
	if _, admitted, err := s.admitBrainstormCommand(command, "finish_synthesis", 4096); !errors.Is(err, ErrPipelineRequestConflict) || admitted {
		t.Fatalf("different command reused key: %v %v", admitted, err)
	}
}

func TestBrainstormIntentRejectsMalformedVisibleSelection(t *testing.T) {
	_, _, in, _, _ := brainstormingServiceFixture(t)
	for _, mutate := range []func(*APIModelSelectionInput){func(s *APIModelSelectionInput) { s.ModelID = "\xff" }, func(s *APIModelSelectionInput) { s.ModelID = strings.Repeat("x", 513) }, func(s *APIModelSelectionInput) { s.Destination = "" }} {
		choice := in.Selection
		mutate(&choice)
		_, err := brainstormCommandIntent(GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: "run", RequestID: "invalid_selection_01", PipelineRevision: 1, RunRevision: 1, DiscoveryVersion: 1}, Selection: choice}, "question")
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("malformed intent accepted: %v", err)
		}
	}
}

func TestBrainstormNewAdmissionRejectsChangedCredential(t *testing.T) {
	s, db, in, _, _ := brainstormingServiceFixture(t)
	run, err := s.StartBrainstorming(in)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := db.GetProviderProfile(t.Context(), in.Selection.ProfileID)
	if err := s.secrets.Put(t.Context(), secrets.Reference{Provider: profile.CredentialProvider, Account: profile.CredentialAccount}, "rotated-secret"); err != nil {
		t.Fatal(err)
	}
	command := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "rotated_question_01", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: in.Selection}
	if _, admitted, err := s.admitBrainstormCommand(command, "question", 4096); err == nil || admitted {
		t.Fatalf("rotated credential admitted: %v %v", admitted, err)
	}
	after, _ := db.GetBrainstorming(t.Context(), run.ID)
	if after.AttemptCount != 0 || after.InputBudgetRemaining != run.InputBudgetRemaining {
		t.Fatal("failed preflight reserved budget")
	}
}

func TestListBrainstormingReturnsCurrentAndHistoricalDiscoveryRounds(t *testing.T) {
	s, db, in, _, _ := brainstormingServiceFixture(t)
	pipeline, err := s.GetPipeline(in.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.ListBrainstorming(ListBrainstormingInput{PipelineID: in.PipelineID, WorkspaceID: pipeline.WorkspaceID})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty history = %+v %v", empty, err)
	}
	first, err := s.StartBrainstorming(in)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.ReviseAuthoringDiscovery(ReviseAuthoringDiscoveryInput{PipelineID: in.PipelineID, ExpectedRevision: first.PipelineRevision, ExpectedVersion: 1, Discovery: "Second immutable Discovery"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.Selection.ProfileID, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	secondInput := StartBrainstormingInput{PipelineID: in.PipelineID, RequestID: "start_brainstorm_v2_01", PipelineRevision: updated.Revision, DiscoveryVersion: 2, Selection: apiSelectionInput(catalog, in.Selection.ProfileID, in.Selection.ModelID, 256)}
	second, err := s.StartBrainstorming(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	history, err := s.ListBrainstorming(ListBrainstormingInput{PipelineID: in.PipelineID, WorkspaceID: pipeline.WorkspaceID})
	if err != nil || len(history) != 2 || history[0].ID != second.ID || history[0].DiscoveryVersion != 2 || history[1].ID != first.ID || history[1].DiscoveryVersion != 1 {
		t.Fatalf("history = %+v %v", history, err)
	}
	limited, err := s.ListBrainstorming(ListBrainstormingInput{PipelineID: in.PipelineID, WorkspaceID: pipeline.WorkspaceID, Limit: 1})
	if err != nil || len(limited) != 1 || limited[0].ID != second.ID {
		t.Fatalf("limited history = %+v %v", limited, err)
	}
	for _, limit := range []int{-1, 101} {
		if _, err := s.ListBrainstorming(ListBrainstormingInput{PipelineID: in.PipelineID, WorkspaceID: pipeline.WorkspaceID, Limit: limit}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid limit %d: %v", limit, err)
		}
	}
	if _, err := s.ListBrainstorming(ListBrainstormingInput{PipelineID: in.PipelineID, WorkspaceID: "another-workspace"}); !errors.Is(err, ErrPipelineNotFound) {
		t.Fatalf("workspace mismatch: %v", err)
	}
	stored, err := db.GetBrainstorming(t.Context(), second.ID)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), stored.Selection.CredentialIdentity) || strings.Contains(string(serialized), stored.StartClientIntentHash) {
		t.Fatal("history exposed private selection or request hashes")
	}
}

func TestGetBrainstormingDistinguishesMissingRun(t *testing.T) {
	s, _, in, _, _ := brainstormingServiceFixture(t)
	_, err := s.GetBrainstorming(GetBrainstormingInput{PipelineID: in.PipelineID})
	if !errors.Is(err, ErrBrainstormNotFound) || ErrorCode(err) != "brainstorm_not_found" {
		t.Fatalf("missing run classification = %v (%s)", err, ErrorCode(err))
	}
}
