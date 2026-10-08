package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/modelcatalog"
)

type cliCatalogStub struct {
	fakeExternal
	id          string
	version     string
	next        string
	efforts     []string
	contextRev  string
	queryCount  int
	detectCount int
	queryCWD    string
	runs        []externalagent.Request
	responses   []string
	block       bool
	started     chan struct{}
	release     chan struct{}
}

func (f *cliCatalogStub) ID() string { return f.id }
func (f *cliCatalogStub) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, Resumable: f.id == "codex" || f.id == "opencode" || f.id == "claude"}
}
func (f *cliCatalogStub) Detect() externalagent.Detection {
	f.detectCount++
	version := f.version
	if f.queryCount > 0 && f.next != "" {
		version = f.next
	}
	return externalagent.Detection{Available: f.available, Path: "/private/cli", Version: version}
}
func (f *cliCatalogStub) QueryModels(ctx context.Context, cwd string) (modelcatalog.Result, error) {
	f.queryCount++
	f.queryCWD = cwd
	if f.block {
		close(f.started)
		<-ctx.Done()
		<-f.release
		return modelcatalog.Result{BackendID: f.id, Status: modelcatalog.StatusInterrupted, ErrorCode: "catalog_cancelled"}, nil
	}
	source := "codex_app_server"
	contextRev := f.contextRev
	if f.id == "opencode" {
		source = "opencode_cli"
	} else if f.id == "claude" {
		source = "claude_cli"
	} else if contextRev == "" {
		contextRev = "codex-effective-context-v1"
	}
	return modelcatalog.Result{BackendID: f.id, Source: source, Destination: f.id + " CLI", ProfileRevision: contextRev, CheckedAt: time.Now().UTC(), Status: modelcatalog.StatusComplete, Complete: true,
		Models: []modelcatalog.Model{{ID: "provider/model-exact", BackendID: f.id, Source: source, Availability: "listed", SupportedReasoningEfforts: f.efforts}}}, nil
}
func (f *cliCatalogStub) Run(_ context.Context, request externalagent.Request) (<-chan externalagent.Event, <-chan error) {
	f.runs = append(f.runs, request)
	events := make(chan externalagent.Event, 4)
	errors := make(chan error)
	if len(f.responses) > 0 {
		index := min(len(f.runs)-1, len(f.responses)-1)
		response := f.responses[index]
		sessionID := "thread_synthetic"
		events <- externalagent.Event{Type: "external.raw", SessionID: sessionID, Raw: json.RawMessage(`{"type":"thread.started","thread_id":"thread_synthetic"}`)}
		events <- externalagent.Event{Type: "external.raw", SessionID: sessionID, Raw: json.RawMessage(`{"type":"turn.started"}`)}
		raw, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]string{"id": "item_1", "type": "agent_message", "text": response}})
		events <- externalagent.Event{Type: "assistant.message", Text: response, MessageID: "item_1", Mode: "replace", SessionID: sessionID, Raw: raw}
		events <- externalagent.Event{Type: "external.raw", SessionID: sessionID, Raw: json.RawMessage(`{"type":"turn.completed","usage":{"input_tokens":12,"output_tokens":8}}`)}
	}
	close(events)
	close(errors)
	return events, errors
}

func TestCLIModelCatalogUsesRegisteredWorkspaceAndExplicitBackend(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "opencode", version: "1.14.27"}
	s.external[stub.id] = stub
	for _, input := range []CLIModelCatalogQuery{{WorkspaceID: "unknown", BackendID: stub.id}, {WorkspaceID: workspace.ID, BackendID: "missing"}} {
		if _, err := s.QueryCLIModelCatalog(t.Context(), input); err == nil {
			t.Fatalf("unregistered query accepted: %+v", input)
		}
	}
	if stub.queryCount != 0 {
		t.Fatal("catalog queried without valid workspace and backend")
	}
	result, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil || result.Status != modelcatalog.StatusComplete || !result.Complete || len(result.Models) != 1 || stub.queryCWD != workspace.Path || result.ProfileRevision == "" {
		t.Fatal(result, err, stub.queryCWD)
	}
}

func TestCLIModelCatalogFailsClosedWhenExecutableChangesDuringQuery(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0", next: "0.158.0"}
	s.external[stub.id] = stub
	result, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil || result.Complete || result.Status != modelcatalog.StatusPartial || result.ErrorCode != "catalog_cli_changed" {
		t.Fatal(result, err)
	}
	if len(result.Models) != 0 {
		t.Fatal("stale model choices still actionable")
	}
}

func TestCLIModelCatalogRejectsUnavailableBackendAndCancelledContext(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: false}, id: "codex"}
	s.external[stub.id] = stub
	result, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil || result.Status != modelcatalog.StatusFailed || result.ErrorCode != "catalog_cli_unavailable" || stub.queryCount != 0 {
		t.Fatal(result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.QueryCLIModelCatalog(ctx, CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id}); !errors.Is(err, context.Canceled) || stub.queryCount != 0 {
		t.Fatal(err)
	}
}

func TestSelectedCLIModelAndEffortPersistAndReachRunner(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0", efforts: []string{"low", "high"}}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil {
		t.Fatal(err)
	}
	input := CreateSessionInput{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", ReasoningEffort: "high", CatalogRevision: listed.ProfileRevision}
	session, err := s.CreateSession(input)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := db.GetSessionModelSelection(t.Context(), session.ID)
	if err != nil || selection.ModelID != input.ModelID || selection.ReasoningEffort != input.ReasoningEffort || selection.CatalogRevision != input.CatalogRevision || selection.WorkspacePath != workspace.Path {
		t.Fatal(selection, err)
	}
	if len(selection.SupportedReasoningEfforts) != 2 || selection.SupportedReasoningEfforts[0] != "low" || selection.SupportedReasoningEfforts[1] != "high" {
		t.Fatal("CLI catalog capability snapshot lost", selection.SupportedReasoningEfforts)
	}
	if got, err := s.GetSessionModelSelection(session.ID); err != nil || got.ModelID != input.ModelID || got.ReasoningEffort != "high" {
		t.Fatal(got, err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "go"})
	if err != nil || result.Status != RunCompleted || len(stub.runs) != 1 || stub.runs[0].Model != input.ModelID || stub.runs[0].ReasoningEffort != "high" {
		t.Fatal(result, err, stub.runs)
	}
	stub.next = "0.158.0"
	result, err = s.Prompt(PromptInput{SessionID: session.ID, Text: "again"})
	if err == nil && result.Status == RunCompleted || len(stub.runs) != 1 {
		t.Fatal("changed executable still ran", result, err, stub.runs)
	}
}

func TestDirectSessionCarriesSelectedCLIModelAndRecordsBypass(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0"}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: stub.id, Reason: "pesquisa local", ModelID: "provider/model-exact", CatalogRevision: listed.ProfileRevision})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := db.GetSessionModelSelection(t.Context(), session.ID)
	if err != nil || selected.ModelID != "provider/model-exact" {
		t.Fatal(selected, err)
	}
	events, err := s.ListEvents(ListEventsInput{SessionID: session.ID})
	if err != nil || len(events) != 1 || events[0].Type != "sdd.bypassed" {
		t.Fatal(events, err)
	}
}

func TestSelectedCLIModelRejectsUnlistedEffortAndStaleRevision(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0", efforts: []string{"low"}}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []CreateSessionInput{
		{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "other-model", CatalogRevision: listed.ProfileRevision},
		{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", ReasoningEffort: "high", CatalogRevision: listed.ProfileRevision},
		{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", CatalogRevision: "stale"},
	} {
		if _, err := s.CreateSession(input); err == nil {
			t.Fatalf("invalid selected model admitted: %+v", input)
		}
	}
	sessions, err := db.ListSessions(t.Context(), workspace.ID)
	if err != nil || len(sessions) != 0 || len(stub.runs) != 0 {
		t.Fatal(sessions, err)
	}
}

func TestSelectedCLIModelSurvivesRestartButChangedCLIIsNotResumable(t *testing.T) {
	s, db, vault := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "opencode", version: "1.14.27"}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", CatalogRevision: listed.ProfileRevision})
	if err != nil {
		t.Fatal(err)
	}
	reopened := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{stub.id: stub}})
	got, err := reopened.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: workspace.ID})
	if err != nil || !got.Resumable {
		t.Fatal(got, err)
	}
	if result, err := reopened.Prompt(PromptInput{SessionID: session.ID, Text: "go"}); err != nil || result.Status != RunCompleted || len(stub.runs) != 1 || stub.runs[0].Model != "provider/model-exact" || stub.runs[0].ReasoningEffort != "" {
		t.Fatal(result, err, stub.runs)
	}
	pending, err := reopened.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", CatalogRevision: listed.ProfileRevision})
	if err != nil {
		t.Fatal(err)
	}
	stub.next = "1.14.28"
	listedSessions, err := reopened.ListSessions(ListSessionsInput{WorkspaceID: workspace.ID})
	if err != nil || len(listedSessions) != 2 {
		t.Fatal(listedSessions, err)
	}
	for _, item := range listedSessions {
		if item.ID == pending.ID && item.Resumable {
			t.Fatal("changed CLI still advertised as resumable", listedSessions)
		}
	}
	if result, err := reopened.Prompt(PromptInput{SessionID: session.ID, Text: "again"}); err == nil && result.Status == RunCompleted || len(stub.runs) != 1 {
		t.Fatal("changed CLI resumed", result, err, stub.runs)
	}
}

func TestSelectedCLIModelBlocksChangedProjectProviderConfig(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(workspace.Path, "opencode.json")
	if err := os.WriteFile(config, []byte(`{"provider":{"local":{"baseURL":"http://127.0.0.1:1234/v1"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "opencode", version: "1.14.27"}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil || listed.ProfileRevision == "" {
		t.Fatal(listed, err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", CatalogRevision: listed.ProfileRevision})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"provider":{"local":{"baseURL":"https://different.example/v1"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "go"})
	if err == nil && result.Status == RunCompleted || len(stub.runs) != 0 {
		t.Fatal("changed provider config still ran", result, err, stub.runs)
	}
}

func TestClaudeCodeChatRunsTheSelectedModelAndStopsWhenProjectSettingsChange(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "claude", version: "2.1.286 (Claude Code)", efforts: []string{"low", "medium", "high", "xhigh", "max"}, responses: []string{"pronto"}}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil || listed.Source != "claude_cli" || listed.ProfileRevision == "" {
		t.Fatal(listed, err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", ReasoningEffort: "xhigh", CatalogRevision: listed.ProfileRevision})
	if err != nil {
		t.Fatal(err)
	}
	if selection, err := db.GetSessionModelSelection(t.Context(), session.ID); err != nil || selection.Source != "claude_cli" || selection.ReasoningEffort != "xhigh" {
		t.Fatal(selection, err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "go"})
	if err != nil || result.Status != RunCompleted || len(stub.runs) != 1 || stub.runs[0].Model != "provider/model-exact" || stub.runs[0].ReasoningEffort != "xhigh" || stub.runs[0].CWD != workspace.Path {
		t.Fatal(result, err, stub.runs)
	}
	settings := filepath.Join(workspace.Path, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"model":"opus"}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = s.Prompt(PromptInput{SessionID: session.ID, Text: "again"})
	if err == nil && result.Status == RunCompleted || len(stub.runs) != 1 {
		t.Fatal("changed Claude Code project settings still ran", result, err, stub.runs)
	}
}

func TestSelectedCodexModelBlocksChangedEffectiveAppServerContext(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0"}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", CatalogRevision: listed.ProfileRevision})
	if err != nil {
		t.Fatal(err)
	}
	stub.contextRev = "codex-effective-context-v2"
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "go"})
	if err == nil && result.Status == RunCompleted || len(stub.runs) != 0 {
		t.Fatal("changed effective Codex context still ran", result, err, stub.runs)
	}
}

func TestSelectedCLIRunnerAbortJoinsCatalogPreflight(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0"}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", CatalogRevision: listed.ProfileRevision})
	if err != nil {
		t.Fatal(err)
	}
	stub.block, stub.started, stub.release = true, make(chan struct{}), make(chan struct{})
	runner := s.sessions[session.ID].(*selectedCLIRunner)
	promptDone := make(chan error, 1)
	go func() { promptDone <- runner.Prompt(t.Context(), "go") }()
	select {
	case <-stub.started:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog preflight did not start")
	}
	abortDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	go func() { abortDone <- runner.Abort(ctx) }()
	select {
	case err := <-abortDone:
		t.Fatalf("Abort returned before catalog process joined: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(stub.release)
	select {
	case err := <-abortDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Abort did not join catalog preflight")
	}
	select {
	case err := <-promptDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt still active after Abort")
	}
	if len(stub.runs) != 0 {
		t.Fatal("agent ran after aborted catalog preflight")
	}
}

func TestListSessionsReusesSelectedCLIRevisionWithinOneRead(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0"}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", CatalogRevision: listed.ProfileRevision}); err != nil {
			t.Fatal(err)
		}
	}
	stub.detectCount = 0
	sessions, err := s.ListSessions(ListSessionsInput{WorkspaceID: workspace.ID})
	if err != nil || len(sessions) != 3 || stub.detectCount != 1 {
		t.Fatal(sessions, err, stub.detectCount)
	}
}
