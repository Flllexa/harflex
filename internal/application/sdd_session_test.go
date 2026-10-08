package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestSDDSessionPublicExecutionIsDenied(t *testing.T) {
	s, db, _ := setup(t)
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: "internal-attempt", WorkspaceID: w.ID, BackendID: "local", Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	runner := &countingAPIRunner{}
	s.sessions[record.ID] = runner
	if _, err := s.Prompt(PromptInput{SessionID: record.ID, Text: "arbitrary public prompt"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("public prompt = %v", err)
	}
	if runner.prompts.Load() != 0 {
		t.Fatal("public API invoked internal runner")
	}
	opened, err := s.OpenSession(OpenSessionInput{WorkspaceID: w.ID, SessionID: record.ID})
	if err != nil || opened.Resumable {
		t.Fatalf("open = %+v, %v", opened, err)
	}
	if _, err := s.CreateSession(CreateSessionInput{WorkspaceID: w.ID, BackendID: "local", Mode: "sdd_readonly"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("public create = %v", err)
	}
}

func TestSDDOpenSessionDoesNotConstructRunner(t *testing.T) {
	s, db, vault := setup(t)
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: "history-only", WorkspaceID: w.ID, BackendID: "local", Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	profile, _ := db.GetProviderProfile(t.Context(), "local")
	record.BackendRevision = profileRevision(profile)
	selection := catalog.ModelSelection{SessionID: record.ID, BackendID: "local", ModelID: "model", WorkspacePath: w.Path, CatalogRevision: "revision", Source: "openai_models", Destination: "https://example.test", CredentialIdentity: "identity", Status: "listed", MaxOutputTokens: 256, CheckedAt: now}
	if err := db.CreateSessionWithSnapshots(t.Context(), record, nil, "", nil, "agent_session", &selection); err != nil {
		t.Fatal(err)
	}
	before := vault.gets
	opened, err := s.OpenSession(OpenSessionInput{WorkspaceID: w.ID, SessionID: record.ID})
	if err != nil || opened.Resumable {
		t.Fatalf("history open = %+v, %v", opened, err)
	}
	if vault.gets != before {
		t.Fatal("history opening loaded credentials")
	}
	if runner, _ := s.runner(record.ID); runner != nil {
		if _, ok := runner.(*readOnlySessionRunner); !ok {
			t.Fatal("history opening constructed executable runner")
		}
	}
}

type noSDDContextStore struct {
	Store
	t *testing.T
}

func (s noSDDContextStore) ListMCPServers(context.Context, string) ([]catalog.MCPServer, error) {
	s.t.Fatal("SDD loaded MCP servers")
	return nil, nil
}
func (s noSDDContextStore) GetSessionAgentSnapshot(context.Context, string) (catalog.AgentSnapshot, error) {
	s.t.Fatal("SDD loaded agent snapshot")
	return catalog.AgentSnapshot{}, nil
}
func (s noSDDContextStore) GetSessionSkillSnapshot(context.Context, string) (string, error) {
	s.t.Fatal("SDD loaded skill snapshot")
	return "", nil
}
func (s noSDDContextStore) ListSkills(context.Context, string) ([]catalog.Skill, error) {
	s.t.Fatal("SDD loaded skills")
	return nil, nil
}

func sddSessionFixture(t *testing.T, provider *fakeProvider) (*Service, *sqlite.Store, catalog.BrainstormRequest, catalog.BrainstormAttempt) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"selected-model"}]}`)
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL = "api", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.prepareAPIModelSelection(t.Context(), apiSelectionInput(result, in.ID, "selected-model", 1024))
	if err != nil {
		t.Fatal(err)
	}
	now, flow := time.Now().UTC(), sdd.NewFlow()
	pipeline := catalog.PipelineRun{ID: "sdd-pipeline", WorkspaceID: w.ID, Kind: "ai_authoring", Title: "title", Objective: "objective", Current: flow.Current, Status: flow.Status, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAuthoringPipeline(t.Context(), pipeline, "Discovery"); err != nil {
		t.Fatal(err)
	}
	run, err := db.StartBrainstorming(t.Context(), catalog.StartBrainstormingRequest{PipelineID: pipeline.ID, RequestID: "sdd_start_0000001", PipelineRevision: 1, DiscoveryVersion: 1, Selection: *selection})
	if err != nil {
		t.Fatal(err)
	}
	ref := catalog.BrainstormRequest{RunID: run.ID, RequestID: "sdd_question_0001", PipelineRevision: 2, RunRevision: 1, DiscoveryVersion: 1}
	attempt, err := db.BeginBrainstormAttempt(t.Context(), catalog.BeginBrainstormAttemptRequest{BrainstormRequest: ref, Kind: "question", Selection: *selection, EstimatedInputTokens: 4096})
	if err != nil {
		t.Fatal(err)
	}
	ref.PipelineRevision++
	ref.RunRevision++
	s.providerFactory = func(c openai.Config) (agentcore.Provider, error) {
		linked, err := db.GetBrainstorming(t.Context(), run.ID)
		if err != nil || linked.Attempts[0].SessionID == "" {
			t.Fatal("provider created before committed link")
		}
		provider.key = c.APIKey
		return provider, nil
	}
	s.store = noSDDContextStore{Store: db, t: t}
	t.Cleanup(func() { Shutdown(s) })
	return s, db, ref, attempt
}

func TestSDDInternalSessionIsLinkedToollessAndSingleUse(t *testing.T) {
	fake := &fakeProvider{}
	s, db, ref, attempt := sddSessionFixture(t, fake)
	session, err := s.createSDDReadOnlySession(ref, attempt)
	if err != nil {
		t.Fatal(err)
	}
	run, err := db.GetBrainstorming(t.Context(), ref.RunID)
	if err != nil || run.Attempts[0].SessionID != session.ID || session.Resumable {
		t.Fatalf("link = %+v %v", run, err)
	}
	attempt = run.Attempts[0]
	if _, err := s.promptSDDAttempt(t.Context(), ref, attempt, "bounded question"); err != nil {
		t.Fatal(err)
	}
	if len(fake.request.Tools) != 0 || len(fake.request.Messages) != 1 || fake.request.MaxOutputTokens != 256 || fake.request.Model != "selected-model" {
		t.Fatalf("request = %+v", fake.request)
	}
	if _, err := s.promptSDDAttempt(t.Context(), ref, attempt, "retry"); err == nil {
		t.Fatal("attempt retried")
	}
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "public"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("public = %v", err)
	}
}

func TestSDDQuestionOutputOverflowFailsBeforeAssistantPublication(t *testing.T) {
	fake := &fakeProvider{output: strings.Repeat("a", sddQuestionOutputBytes+1)}
	s, db, ref, attempt := sddSessionFixture(t, fake)
	session, err := s.createSDDReadOnlySession(ref, attempt)
	if err != nil {
		t.Fatal(err)
	}
	attempt.SessionID = session.ID
	result, err := s.promptSDDAttempt(t.Context(), ref, attempt, "bounded question")
	if err != nil || result.Status != RunFailed || result.Reason != "output_limit_exceeded" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if fake.request.MaxOutputTokens != 256 || len(fake.request.Tools) != 0 {
		t.Fatalf("request = %+v", fake.request)
	}
	events, err := db.ListAfter(t.Context(), session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "message.assistant" || event.Type == "run.completed" {
			t.Fatalf("overflow accepted: %s", event.Type)
		}
	}
}

func TestSDDInternalPromptRejectsWrongOrStaleAttempt(t *testing.T) {
	for _, mutation := range []string{"attempt", "run", "pipeline", "selection", "oversize", "interrupted"} {
		t.Run(mutation, func(t *testing.T) {
			fake := &fakeProvider{}
			s, db, ref, attempt := sddSessionFixture(t, fake)
			session, err := s.createSDDReadOnlySession(ref, attempt)
			if err != nil {
				t.Fatal(err)
			}
			attempt.SessionID = session.ID
			prompt := "bounded"
			switch mutation {
			case "attempt":
				attempt.ID = "wrong"
			case "run":
				ref.RunRevision++
			case "pipeline":
				ref.PipelineRevision++
			case "selection":
				attempt.Selection.ModelID = "wrong"
			case "oversize":
				prompt = string(make([]byte, 4097))
			case "interrupted":
				if err := db.InterruptRunningBrainstormAttempts(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.promptSDDAttempt(t.Context(), ref, attempt, prompt); err == nil {
				t.Fatal("invalid prompt accepted")
			}
			if fake.request.Model != "" {
				t.Fatal("provider invoked")
			}
		})
	}
}

func TestSDDProviderToolCallCannotExecute(t *testing.T) {
	fake := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "forbidden-call", Name: "read", Arguments: json.RawMessage(`{"path":"secret.txt"}`)}}
	s, db, ref, attempt := sddSessionFixture(t, fake)
	session, err := s.createSDDReadOnlySession(ref, attempt)
	if err != nil {
		t.Fatal(err)
	}
	attempt.SessionID = session.ID
	_, _ = s.promptSDDAttempt(t.Context(), ref, attempt, "bounded question")
	if len(fake.request.Tools) != 0 {
		t.Fatal("provider received tools")
	}
	events, err := db.ListAfter(t.Context(), session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	denied := false
	for _, event := range events {
		if event.Type == "tool.called" || event.Type == "tool.completed" {
			t.Fatalf("tool executed: %s", event.Type)
		}
		if event.Type == "tool.denied" {
			denied = true
		}
	}
	if !denied {
		t.Fatal("provider tool call was not denied")
	}
}

func TestSDDInterruptionDuringCatalogPreflightBlocksInference(t *testing.T) {
	fake := &fakeProvider{}
	s, db, ref, attempt := sddSessionFixture(t, fake)
	session, err := s.createSDDReadOnlySession(ref, attempt)
	if err != nil {
		t.Fatal(err)
	}
	attempt.SessionID = session.ID
	transport := s.modelHTTPClient.Transport
	s.modelHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(r)
		if pauseErr := db.InterruptRunningBrainstormAttempts(t.Context()); pauseErr != nil {
			t.Error(pauseErr)
		}
		return response, err
	})}
	if _, err := s.promptSDDAttempt(t.Context(), ref, attempt, "bounded question"); err == nil {
		t.Fatal("interrupted attempt executed")
	}
	if fake.request.Model != "" {
		t.Fatal("inference began after attempt interruption")
	}
}

func TestSDDShutdownInterruptsWithoutRetryAndRecoveryPauses(t *testing.T) {
	fake := &fakeProvider{block: true, started: make(chan struct{})}
	s, db, ref, attempt := sddSessionFixture(t, fake)
	session, err := s.createSDDReadOnlySession(ref, attempt)
	if err != nil {
		t.Fatal(err)
	}
	attempt.SessionID = session.ID
	done := make(chan struct{})
	go func() { _, _ = s.promptSDDAttempt(t.Context(), ref, attempt, "bounded question"); close(done) }()
	select {
	case <-fake.started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	closed := make(chan struct{})
	go func() { Shutdown(s); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		// Unblock cleanup even while the RED implementation merely drains.
		runner, _ := s.runner(session.ID)
		runner.Cancel()
		<-closed
		t.Fatal("shutdown failed to interrupt internal run")
	}
	<-done
	fresh := NewService(t.Context(), Dependencies{Store: db, External: map[string]ExternalBackend{}})
	t.Cleanup(func() { Shutdown(fresh) })
	if err := fresh.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, err := db.GetBrainstorming(t.Context(), ref.RunID)
	if err != nil || run.State != "paused" || run.Attempts[0].Status != "interrupted" {
		t.Fatalf("recovery = %+v %v", run, err)
	}
	opened, err := fresh.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
	if err != nil || opened.Resumable {
		t.Fatalf("reopened = %+v %v", opened, err)
	}
}

func TestSDDInternalAdmissionRejectsExternalBackend(t *testing.T) {
	s, db, ref, attempt := sddSessionFixture(t, &fakeProvider{})
	s.external[attempt.Selection.BackendID] = nil
	if _, err := s.createSDDReadOnlySession(ref, attempt); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("external = %v", err)
	}
	run, err := db.GetBrainstorming(t.Context(), ref.RunID)
	if err != nil || run.Attempts[0].SessionID != "" {
		t.Fatalf("external linked = %+v %v", run, err)
	}
}

func TestSDDCompositionFailurePausesLinkedAttemptWithoutRetry(t *testing.T) {
	s, db, ref, attempt := sddSessionFixture(t, &fakeProvider{})
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		return nil, errors.New("provider initialization failed")
	}
	if _, err := s.createSDDReadOnlySession(ref, attempt); err == nil {
		t.Fatal("composition failure accepted")
	}
	run, err := db.GetBrainstorming(t.Context(), ref.RunID)
	if err != nil || run.State != "paused" || run.Attempts[0].Status != "failed" || run.Attempts[0].ErrorCode != "provider_failed" || run.Attempts[0].SessionID == "" {
		t.Fatalf("failed admission = %+v %v", run, err)
	}
	if _, err := s.createSDDReadOnlySession(ref, attempt); err == nil {
		t.Fatal("failed attempt retried")
	}
	if len(s.sessions) != 0 {
		t.Fatal("failed attempt retained runner")
	}
}
