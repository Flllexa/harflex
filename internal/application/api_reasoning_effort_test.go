package application

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
)

func TestAPIReasoningEffortVerifiedAndFrozenThroughRunner(t *testing.T) {
	var removed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if removed.Load() {
			_, _ = io.WriteString(w, `{"models":[{"type":"llm","key":"chosen","loaded_instances":[]}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"models":[{"type":"llm","key":"chosen","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":["low","high"],"default":"low"}}}]}`)
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.APIKey = "lm", "lm_studio", server.URL+"/v1", ""
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil || !result.Complete {
		t.Fatal(result, err)
	}
	input := apiSelectionInput(result, in.ID, "chosen", 321)
	input.ReasoningEffort = "high"
	input.ConfirmJITLoad = true
	selection, err := s.prepareAPIModelSelection(t.Context(), input)
	if err != nil {
		t.Fatal("advertised effort rejected", err)
	}
	if selection.ReasoningEffort != "high" {
		t.Fatalf("effort changed: %+v", selection)
	}
	if !validBrainstormIntentSelection(input) {
		t.Fatal("verified selection cannot enter command intent")
	}
	for _, effort := range []string{"medium", "HIGH", " high", "high\n"} {
		bad := input
		bad.ReasoningEffort = effort
		if _, err := s.prepareAPIModelSelection(t.Context(), bad); err == nil {
			t.Fatalf("accepted unavailable effort %q", effort)
		}
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: "effort-attempt", WorkspaceID: workspace.ID, BackendID: in.ID, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	selection.SessionID, selection.WorkspacePath = record.ID, workspace.Path
	if err := db.CreateSessionWithSnapshots(t.Context(), record, nil, "", nil, "agent_session", selection); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return fake, nil }
	storedWorkspace, err := db.GetWorkspace(t.Context(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner, journal, err := s.makeRunner(&record, storedWorkspace, restoredHistory{}, true, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(journal.Close)
	if err := runner.Prompt(t.Context(), "generate"); err != nil {
		t.Fatal(err)
	}
	if fake.request.Model != "chosen" || fake.request.ReasoningEffort != "high" || fake.request.MaxOutputTokens != 321 {
		t.Fatalf("request lost selection: %+v", fake.request)
	}
	removed.Store(true)
	if err := s.revalidateAPIModelSelection(t.Context(), *selection, true); err == nil {
		t.Fatal("removed effort silently became Auto")
	}
	stored, err := db.GetSessionModelSelection(t.Context(), record.ID)
	if err != nil || stored.ReasoningEffort != "high" {
		t.Fatal("historical snapshot changed", stored, err)
	}
	if !slices.Equal(stored.SupportedReasoningEfforts, []string{"low", "high"}) {
		t.Fatal("historical capability evidence was lost", stored.SupportedReasoningEfforts)
	}
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE session_model_selections SET supported_reasoning_efforts='["medium"]' WHERE session_id=?`, record.ID); err == nil {
		t.Fatal("immutable capability snapshot accepted overwrite")
	}
}

func TestReasoningEffortIncludedInAllSDDPromptReservations(t *testing.T) {
	choice := catalog.ModelSelection{ModelID: "chosen", ReasoningEffort: "high", MaxOutputTokens: 256}
	brain := catalog.BrainstormRun{DiscoveryContent: "Full Discovery", InputBudgetRemaining: 100000, OutputBudgetRemaining: 20000}
	question, err := prepareSDDBrainstormPrompt(brain, "question", choice, "openai")
	if err != nil {
		t.Fatal(err)
	}
	choice.MaxOutputTokens = 4096
	stage := catalog.AuthoringStageRun{Stage: sdd.Spec, DiscoveryVersion: 1, InputBudgetRemaining: 100000, OutputBudgetRemaining: 20000}
	input := catalog.AuthoringStageInput{DiscoveryVersion: 1, DiscoveryContent: "Full Discovery", DiscoveryBypassReason: "Explicit confirmation"}
	document, err := prepareAuthoringStagePrompt(stage, input, choice, "openai")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		text        string
		cap         int
		bytes       int
		reservation int64
	}{{question.Text, 256, question.SerializedBytes, question.EstimatedInputTokens}, {document.Text, 4096, document.SerializedBytes, document.EstimatedInputTokens}} {
		req := agentcore.ChatRequest{Model: choice.ModelID, ReasoningEffort: "high", MaxOutputTokens: test.cap, Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: test.text}}}
		actual, err := openai.RequestSize(req, "openai")
		if err != nil || actual != test.bytes || int64(actual)+1024 != test.reservation {
			t.Fatal("effort outside request reservation", actual, test, err)
		}
	}
}
