package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func TestSDDSelectionPreservesFreshCatalogContextLength(t *testing.T) {
	var contextLength atomic.Int64
	contextLength.Store(8192)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"data":[{"id":"chosen","context_length":%d}],"total_count":1}`, contextLength.Load()))
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	defer Shutdown(s)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL = "context-api", "openrouter", server.URL+"/api/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	input := apiSelectionInput(result, in.ID, "chosen", 256)
	input.ConfirmUnfiltered = true
	choice, err := s.prepareAPIModelSelection(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if choice.ContextLength != 8192 || brainstormSelectionDTO(*choice).ContextLength != 8192 {
		t.Fatal("context length discarded")
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: "context-session", WorkspaceID: workspace.ID, BackendID: in.ID, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	choice.SessionID, choice.WorkspacePath = record.ID, workspace.Path
	if err := db.CreateSessionWithSnapshots(t.Context(), record, nil, "", nil, "agent_session", choice); err != nil {
		t.Fatal(err)
	}
	dto, err := s.GetSessionModelSelection(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(dto)
	var wire map[string]any
	_ = json.Unmarshal(data, &wire)
	if wire["contextLength"] != float64(8192) {
		t.Fatalf("session readback lost context: %s", data)
	}
	contextLength.Store(1024)
	if err := s.revalidateAPIModelSelection(t.Context(), *choice, true); !errors.Is(err, ErrBackendChanged) {
		t.Fatalf("context changed after preflight: %v", err)
	}
	// Exercise the same frozen-context check at the final inference boundary.
	contextLength.Store(8192)
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "context_pipeline_001", Discovery: "Confirmed Discovery"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.StartBrainstorming(StartBrainstormingInput{PipelineID: pipeline.ID, RequestID: "context_start_00001", PipelineRevision: pipeline.Revision, DiscoveryVersion: 1, Selection: input})
	if err != nil {
		t.Fatal(err)
	}
	command := GenerateBrainstormInput{Ref: BrainstormRequestInput{RunID: run.ID, RequestID: "context_question_01", PipelineRevision: run.PipelineRevision, RunRevision: run.Revision, DiscoveryVersion: 1}, Selection: input}
	attempt, admitted, err := s.admitBrainstormCommand(command, "question", 4096)
	if err != nil || !admitted {
		t.Fatal("admission", err)
	}
	ref := command.Ref.request()
	ref.PipelineRevision++
	ref.RunRevision++
	fake := &fakeProvider{}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return fake, nil }
	session, err := s.createSDDReadOnlySession(ref, attempt)
	if err != nil {
		t.Fatal(err)
	}
	attempt.SessionID = session.ID
	contextLength.Store(1024)
	if _, err := s.promptSDDAttempt(t.Context(), ref, attempt, "bounded question"); !errors.Is(err, ErrBackendChanged) {
		t.Fatalf("shrinking context reached inference: %v", err)
	}
	if fake.request.Model != "" {
		t.Fatal("provider called after context shrink")
	}
}
