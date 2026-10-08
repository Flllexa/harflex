package application

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
)

func apiProfileForPhases(t *testing.T, s *Service, id, model string) {
	t.Helper()
	in := profileInput()
	in.ID, in.Name, in.ProviderType, in.BaseURL, in.Model = id, id, "openai", "https://"+id+".synthetic.invalid/v1", model
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
}

// Each phase of a project can have an executor of its own. The list comes back in pipeline order, a phase that was
// changed keeps one row, and returning a phase to the default is not an error even when it had none.
func TestStageExecutorsAreChosenPerPhaseAndListedInPipelineOrder(t *testing.T) {
	s, _, _ := setup(t)
	apiProfileForPhases(t, s, "alpha", "alpha-model")
	apiProfileForPhases(t, s, "beta", "beta-model")
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if listed, err := s.ListStageExecutors(workspace.ID); err != nil || len(listed) != 0 {
		t.Fatalf("a project that chose nothing lists %+v %v", listed, err)
	}
	for _, in := range []SaveStageExecutorInput{
		{WorkspaceID: workspace.ID, Stage: "plan", BackendID: "beta", ModelID: "beta-large"},
		{WorkspaceID: workspace.ID, Stage: "discovery", BackendID: "alpha"},
		{WorkspaceID: workspace.ID, Stage: "eval", BackendID: "beta", ModelID: "beta-model"},
	} {
		if saved, err := s.SaveStageExecutor(in); err != nil || saved.Stage != in.Stage || saved.BackendID != in.BackendID || saved.ModelID != in.ModelID || saved.UpdatedAt.IsZero() {
			t.Fatalf("save %+v: %+v %v", in, saved, err)
		}
	}
	listed, err := s.ListStageExecutors(workspace.ID)
	if err != nil || len(listed) != 3 || listed[0].Stage != "discovery" || listed[1].Stage != "plan" || listed[2].Stage != "eval" {
		t.Fatalf("the phases are not listed in pipeline order: %+v %v", listed, err)
	}
	if listed[0].BackendID != "alpha" || listed[0].ModelID != "" || listed[1].ModelID != "beta-large" {
		t.Fatalf("what was chosen did not come back: %+v", listed)
	}
	if _, err := s.SaveStageExecutor(SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "plan", BackendID: "alpha", ModelID: "alpha-model"}); err != nil {
		t.Fatal(err)
	}
	if listed, err = s.ListStageExecutors(workspace.ID); err != nil || len(listed) != 3 || listed[1].BackendID != "alpha" || listed[1].ModelID != "alpha-model" {
		t.Fatalf("changing a phase did not replace its row: %+v %v", listed, err)
	}
	if err := s.ClearStageExecutor(workspace.ID, "plan"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearStageExecutor(workspace.ID, "plan"); err != nil {
		t.Fatalf("returning to the default a phase that already used it failed: %v", err)
	}
	if listed, err = s.ListStageExecutors(workspace.ID); err != nil || len(listed) != 2 || listed[0].Stage != "discovery" || listed[1].Stage != "eval" {
		t.Fatalf("clearing a phase left it chosen: %+v %v", listed, err)
	}
	// Another project is not touched by any of it.
	other, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if listed, err = s.ListStageExecutors(other.ID); err != nil || len(listed) != 0 {
		t.Fatalf("the choice of one project reached another: %+v %v", listed, err)
	}
}

func TestStageExecutorRefusesWhatThePhaseCannotRunOn(t *testing.T) {
	s, _, _ := setup(t)
	s.external["codex"] = &fakeExternal{available: true}
	s.external["opencode"] = &fakeExternal{available: true}
	s.external["claude"] = &fakeExternal{available: true}
	apiProfileForPhases(t, s, "alpha", "alpha-model")
	if _, err := s.SaveProviderProfile(profileInput()); err != nil { // "local", a generic server with no catalog
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("m", 513)
	for name, test := range map[string]struct {
		in   SaveStageExecutorInput
		want error
	}{
		"an unknown phase":               {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "deploy", BackendID: "alpha"}, ErrInvalidInput},
		"no executor":                    {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "spec"}, ErrInvalidInput},
		"a model with a control byte":    {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "spec", BackendID: "alpha", ModelID: "a\nb"}, ErrInvalidInput},
		"a model that is too long":       {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "spec", BackendID: "alpha", ModelID: long}, ErrInvalidInput},
		"a project that does not exist":  {SaveStageExecutorInput{WorkspaceID: "workspace-gone", Stage: "spec", BackendID: "alpha"}, ErrWorkspaceNotFound},
		"a provider that does not exist": {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "spec", BackendID: "gone"}, ErrBackendNotFound},
		"Claude Code for pull requests":  {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "prs", BackendID: "claude", ModelID: "sonnet"}, ErrStageExecutorUnsupported},
		"a CLI other than Codex":         {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "spec", BackendID: "opencode"}, ErrStageExecutorUnsupported},
		"Codex for the pull requests":    {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "prs", BackendID: "codex", ModelID: "gpt"}, ErrStageExecutorUnsupported},
		"a generic server for SPEC":      {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "spec", BackendID: "local"}, ErrStageExecutorUnsupported},
		"a generic server for QA":        {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "eval", BackendID: "local"}, ErrStageExecutorUnsupported},
		"a model on a generic server":    {SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "prs", BackendID: "local", ModelID: "llama-3"}, ErrStageExecutorUnsupported},
	} {
		if _, err := s.SaveStageExecutor(test.in); !errors.Is(err, test.want) {
			t.Fatalf("%s: got %v, want %v", name, err, test.want)
		}
	}
	// What each phase can take is accepted: an API profile anywhere, Codex and Claude Code in every phase but the pull requests.
	for _, in := range []SaveStageExecutorInput{
		{WorkspaceID: workspace.ID, Stage: "discovery", BackendID: "codex", ModelID: "gpt-5-codex"},
		{WorkspaceID: workspace.ID, Stage: "spec", BackendID: "alpha"},
		{WorkspaceID: workspace.ID, Stage: "plan", BackendID: "claude", ModelID: "opus"},
		{WorkspaceID: workspace.ID, Stage: "code", BackendID: "codex", ModelID: "gpt-5-codex"},
		{WorkspaceID: workspace.ID, Stage: "eval", BackendID: "alpha", ModelID: "alpha-model"},
		{WorkspaceID: workspace.ID, Stage: "prs", BackendID: "alpha", ModelID: "alpha-model"},
		{WorkspaceID: workspace.ID, Stage: "prs", BackendID: "local"}, // the pull requests need no catalog
	} {
		if _, err := s.SaveStageExecutor(in); err != nil {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	if err := s.ClearStageExecutor(workspace.ID, "deploy"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("clearing an unknown phase: %v", err)
	}
	if _, err := s.ListStageExecutors("workspace-gone"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("listing the phases of a project that does not exist: %v", err)
	}
}

// A document phase (Discovery, SPEC, Plan) is written by what the project chose for it, and only the phases it did not
// choose for fall back to the default of Settings. The catalog is asked again at that moment, so what is saved is not a
// promise that the model is still there.
func TestADocumentPhaseIsWrittenByTheProjectsChoiceBeforeTheSettingsDefault(t *testing.T) {
	s, db, pipeline := pipelineDesignServiceFixture(t)
	apiProfileForPhases(t, s, "design-other", "other-profile-model")
	// Both providers list the same two models; only which provider and model are asked for tells them apart.
	s.modelHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"selected-model","context_length":64000},{"id":"other-profile-model","context_length":64000},{"id":"large-model","context_length":64000}]}`))}, nil
	})}
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	pick := func(stage sdd.Stage) catalog.ModelSelection {
		t.Helper()
		selection, err := s.preparePipelineDesignSelection(t.Context(), design.WorkspaceID, stage, nil)
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		return selection
	}
	if got := pick(sdd.Spec); got.BackendID != "design-api" || got.ModelID != "selected-model" {
		t.Fatalf("with nothing chosen the Settings default writes: %+v", got)
	}
	for _, in := range []SaveStageExecutorInput{
		{WorkspaceID: design.WorkspaceID, Stage: "spec", BackendID: "design-other", ModelID: "large-model"},
		{WorkspaceID: design.WorkspaceID, Stage: "plan", BackendID: "design-other"},
	} {
		if _, err := s.SaveStageExecutor(in); err != nil {
			t.Fatal(err)
		}
	}
	if got := pick(sdd.Spec); got.BackendID != "design-other" || got.ModelID != "large-model" {
		t.Fatalf("SPEC ignored what the project chose for it: %+v", got)
	}
	// A phase that chose a provider and no model takes that provider's own model, not the model of the default.
	if got := pick(sdd.Plan); got.BackendID != "design-other" || got.ModelID != "other-profile-model" {
		t.Fatalf("Plan did not take the model of the provider it chose: %+v", got)
	}
	// Discovery chose nothing, so it still follows the Settings default.
	if got := pick(sdd.Discovery); got.BackendID != "design-api" || got.ModelID != "selected-model" {
		t.Fatalf("a phase that chose nothing stopped following the default: %+v", got)
	}
	// What the person picks for one run still wins over both.
	catalogResult, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "design-api"})
	if err != nil {
		t.Fatal(err)
	}
	override := apiSelectionInput(catalogResult, "design-api", "selected-model", 0)
	if got, err := s.preparePipelineDesignSelection(t.Context(), design.WorkspaceID, sdd.Spec, &override); err != nil || got.BackendID != "design-api" || got.ModelID != "selected-model" {
		t.Fatalf("a model picked for the run lost to the project's choice: %+v %v", got, err)
	}
	// A phase works without any default of Settings once it has its own choice.
	if err := db.SaveSettings(t.Context(), catalog.AppSettings{}); err != nil {
		t.Fatal(err)
	}
	if got := pick(sdd.Spec); got.BackendID != "design-other" {
		t.Fatalf("a phase with its own executor needed the default: %+v", got)
	}
	if _, err := s.preparePipelineDesignSelection(t.Context(), design.WorkspaceID, sdd.Discovery, nil); !errors.Is(err, ErrPipelineDesignModelRequired) {
		t.Fatalf("a phase with neither a choice nor a default did not say a model is required: %v", err)
	}
	// An executor that is gone says so instead of silently writing with another one.
	if _, err := db.DB().ExecContext(t.Context(), `DELETE FROM provider_profiles WHERE id='design-other'`); err != nil {
		t.Fatal(err)
	}
	_, err = s.preparePipelineDesignSelection(t.Context(), design.WorkspaceID, sdd.Spec, nil)
	var phase phaseExecutorError
	if !errors.Is(err, ErrPipelineDesignPhaseExecutor) || !errors.As(err, &phase) || phase.stage != sdd.Spec {
		t.Fatalf("a phase whose provider was removed did not say it was this phase that could not start: %v", err)
	}
	if got := string(MarshalError(err)); got != `{"code":"pipeline_design_phase_executor_unusable","stage":"spec"}` {
		t.Fatalf("the screen is not told which phase it was: %s", got)
	}
	// A phase that chose nothing keeps the plain error: there is no choice of the project to blame.
	if _, err := s.preparePipelineDesignSelection(t.Context(), design.WorkspaceID, sdd.Discovery, nil); errors.Is(err, ErrPipelineDesignPhaseExecutor) {
		t.Fatalf("a phase that chose nothing blamed a choice: %v", err)
	}
}

// The choice reaches the request itself: a SPEC and a Plan prepared together are written on the model each phase chose,
// and a phase whose choice cannot start stops the whole request before anything is written, naming the phase.
func TestPreparingDocumentsWritesEachPhaseOnItsOwnModelAndNamesAPhaseThatCannotStart(t *testing.T) {
	s, db, pipeline := pipelineDesignServiceFixture(t)
	apiProfileForPhases(t, s, "design-other", "other-profile-model")
	list := func(ids ...string) {
		body := `{"data":[`
		for index, id := range ids {
			if index > 0 {
				body += ","
			}
			body += `{"id":"` + id + `","context_length":64000}`
		}
		body += `]}`
		s.modelHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
	}
	list("selected-model", "other-profile-model", "large-model")
	providers := installDesignAnswers(t, s, db, pipeline.ID)
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []SaveStageExecutorInput{
		{WorkspaceID: design.WorkspaceID, Stage: "spec", BackendID: "design-other", ModelID: "large-model"},
		{WorkspaceID: design.WorkspaceID, Stage: "plan", BackendID: "design-other"},
	} {
		if _, err := s.SaveStageExecutor(in); err != nil {
			t.Fatal(err)
		}
	}
	// Plan's choice is not there any more: the request stops before SPEC is written, and says it was Plan.
	list("selected-model", "large-model")
	_, err = s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_phase_executors_01"), Target: "all", Message: "Preparar documentos"})
	var phase phaseExecutorError
	if !errors.Is(err, ErrPipelineDesignPhaseExecutor) || !errors.As(err, &phase) || phase.stage != sdd.Plan || len(*providers) != 0 {
		t.Fatalf("a Plan that cannot start did not stop the request, naming Plan: stage=%q providers=%d %v", phase.stage, len(*providers), err)
	}
	list("selected-model", "other-profile-model", "large-model")
	prepared, err := s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_phase_executors_02"), Target: "all", Message: "Preparar documentos"})
	if err != nil || prepared.State != "ready" || len(*providers) != 2 {
		t.Fatalf("preparing SPEC and Plan: %+v %v", prepared, err)
	}
	models := []string{(*providers)[0].requests[0].Model, (*providers)[1].requests[0].Model}
	if models[0] != "large-model" || models[1] != "other-profile-model" {
		t.Fatalf("each phase must be written on the model it chose, got %v", models)
	}
}

// Codex has no model of its own, so a phase that chose it and no model leans on the model Settings saved for Codex.
func TestACodexPhaseLeansOnTheSettingsModelOnlyForCodex(t *testing.T) {
	s, db, _ := setup(t)
	stub := &designCLIStub{cliCatalogStub: cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0", efforts: []string{"low"}}}
	s.external["codex"] = stub
	apiProfileForPhases(t, s, "api", "api-model")
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveStageExecutor(SaveStageExecutorInput{WorkspaceID: workspace.ID, Stage: "spec", BackendID: "codex"}); err != nil {
		t.Fatal(err)
	}
	// Settings default to an API profile: Codex has nothing to lean on, and the phase says a model is required.
	if err := db.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: "api", DefaultModelBackendID: "api", DefaultModelID: "api-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.preparePipelineDesignSelection(t.Context(), workspace.ID, sdd.Spec, nil); !errors.Is(err, ErrPipelineDesignPhaseExecutor) {
		t.Fatalf("Codex with no model of its own wrote on the model of another provider: %v", err)
	}
	if err := db.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: "codex", DefaultModelBackendID: "codex", DefaultModelID: "provider/model-exact"}); err != nil {
		t.Fatal(err)
	}
	selection, err := s.preparePipelineDesignSelection(t.Context(), workspace.ID, sdd.Spec, nil)
	if err != nil || selection.BackendID != "codex" || selection.ModelID != "provider/model-exact" || selection.Source != "codex_app_server" {
		t.Fatalf("Codex did not take the model Settings saved for it: %+v %v", selection, err)
	}
}

// The pull request conversation may run on a model picked from the catalog. It holds that model for as long as it
// lives, however long the person's approvals take, and it still refuses what the Harflex tool loop cannot give it.
func TestPullRequestConversationRunsOnAModelPickedFromTheCatalog(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"chosen-model"},{"id":"other-model"}]}`)
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	// A session composes its runner, and so its provider, when it is created.
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: "Pronto."}}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.Model = "api", "openai", server.URL+"/v1", "other-model"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "pull-request-model-0001", Discovery: "Export invoices"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='prs',stage_status=?,revision=revision+1 WHERE id=?`, `{"discovery":"completed","spec":"completed","plan":"completed","code":"completed","eval":"completed","prs":"active"}`, run.ID); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	pick := apiSelectionInput(result, in.ID, "chosen-model", 0)
	pick.Executor = "api"
	conversation, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: in.ID, Role: "publisher", Selection: &pick})
	if err != nil || conversation.Role != "publisher" {
		t.Fatalf("pull request conversation on a picked model: %+v %v", conversation, err)
	}
	chosen, err := db.GetSessionModelSelection(t.Context(), conversation.Session.ID)
	if err != nil || chosen.ModelID != "chosen-model" || chosen.BackendID != in.ID {
		t.Fatalf("the conversation does not hold the model that was picked: %+v %v", chosen, err)
	}
	// Without a pick it keeps the profile's own model, as before: nothing is bound to it.
	plain, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: in.ID, Role: "publisher"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetSessionModelSelection(t.Context(), plain.Session.ID); err == nil {
		t.Fatal("a conversation started without a pick holds a model selection")
	}
	// What the provider is asked is what was chosen: the bound conversation on its model, with the room of a chat
	// with a terminal, and the plain one on the profile's model with no cap, as before.
	if result, err := s.Prompt(PromptInput{SessionID: conversation.Session.ID, Text: conversation.Prompt}); err != nil || result.Status != RunCompleted {
		t.Fatalf("bound conversation run: %+v %v", result, err)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "chosen-model" || provider.requests[0].MaxOutputTokens != pullRequestOutputTokens {
		t.Fatalf("the bound conversation did not ask for its model with its room: %+v", provider.requests)
	}
	if result, err := s.Prompt(PromptInput{SessionID: plain.Session.ID, Text: plain.Prompt}); err != nil || result.Status != RunCompleted {
		t.Fatalf("plain conversation run: %+v %v", result, err)
	}
	if len(provider.requests) != 2 || provider.requests[1].Model != "other-model" || provider.requests[1].MaxOutputTokens != 0 {
		t.Fatalf("the plain conversation changed: %+v", provider.requests)
	}
	// The conversation needs the tool loop, so a CLI pick is refused.
	cli := pick
	cli.Executor, cli.ProfileID, cli.BackendID = "codex_cli", "", "codex"
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: in.ID, Role: "publisher", Selection: &cli}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("a CLI pick reached the pull request conversation: %v", err)
	}
}
