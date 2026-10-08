package application

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

const authoringSpecOutput = `{"summary":"Scope","requirements":["Required"],"nonGoals":["Excluded"],"acceptanceCriteria":[{"id":"AC-1","criterion":"Observable acceptance"}]}`
const authoringPlanOutput = `{"summary":"Implementation","tasks":[{"id":"T-1","title":"Implement","files":["internal/example.go"],"steps":["Add behavior"],"tests":["Test behavior"],"dependsOn":[]}],"risks":["Integration"]}`

func authoringGenerationFixture(t *testing.T) (*Service, *sqlite.Store, GenerateAuthoringStageInput, string) {
	t.Helper()
	s, db, start, _, path := brainstormingServiceFixture(t)
	brain, err := s.StartBrainstorming(start)
	if err != nil {
		t.Fatal(err)
	}
	ref := catalog.BrainstormRequest{RunID: brain.ID, RequestID: "stage_skip_brain_01", PipelineRevision: brain.PipelineRevision, RunRevision: brain.Revision, DiscoveryVersion: brain.DiscoveryVersion}
	if _, err := db.SkipBrainstormQuestions(t.Context(), catalog.SkipBrainstormQuestionsRequest{BrainstormRequest: ref, Reason: "Discovery is sufficient"}); err != nil {
		t.Fatal(err)
	}
	ref.RequestID = "stage_confirm_brain"
	ref.RunRevision++
	ref.PipelineRevision++
	if _, err := db.ConfirmDiscoveryAfterSkip(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	stage, err := db.GetAuthoringStage(t.Context(), start.PipelineID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	choice := start.Selection
	choice.MaxOutputTokens = 4096
	return s, db, GenerateAuthoringStageInput{Ref: AuthoringStageRefInput{PipelineID: stage.PipelineID, Stage: "spec", RequestID: "generate_spec_0001", PipelineRevision: stage.PipelineRevision, StageRevision: stage.Revision, DiscoveryVersion: stage.DiscoveryVersion, ArtifactVersion: stage.ArtifactVersion}, Selection: choice}, path
}

func authoringPreferenceGenerationInput(t *testing.T, db *sqlite.Store, pipelineID, requestID string, selection APIModelSelectionInput) GenerateAuthoringStageInput {
	t.Helper()
	stage, err := db.GetAuthoringStage(t.Context(), pipelineID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	return GenerateAuthoringStageInput{
		Ref:       AuthoringStageRefInput{PipelineID: pipelineID, Stage: string(sdd.Spec), RequestID: requestID, PipelineRevision: stage.PipelineRevision, StageRevision: stage.Revision, DiscoveryVersion: stage.DiscoveryVersion, ArtifactVersion: stage.ArtifactVersion},
		Selection: selection,
	}
}

func authoringGenerationWithConsent(t *testing.T, input GenerateAuthoringStageInput, jit, unfiltered bool) GenerateAuthoringStageInput {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["confirmJitLoad"], _ = json.Marshal(jit)
	envelope["confirmUnfiltered"], _ = json.Marshal(unfiltered)
	data, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var result GenerateAuthoringStageInput
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func authoringGenerationWithConsentBinding(t *testing.T, input GenerateAuthoringStageInput, jit, unfiltered bool, binding map[string]any) GenerateAuthoringStageInput {
	t.Helper()
	input = authoringGenerationWithConsent(t, input, jit, unfiltered)
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["consentBinding"], _ = json.Marshal(binding)
	data, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var result GenerateAuthoringStageInput
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func authoringOpenRouterGenerationFixture(t *testing.T, accountCatalog bool) (*Service, *sqlite.Store, GenerateAuthoringStageInput, *brainstormTestProvider, *atomic.Int32) {
	t.Helper()
	catalogCalls := new(atomic.Int32)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catalogCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"openai/model","name":"Model"},{"id":"openai/other","name":"Other"}],"total_count":2}`)
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	profile := profileInput()
	profile.ID, profile.Name, profile.ProviderType, profile.BaseURL, profile.Model = "router", "OpenRouter", "openrouter", server.URL+"/api/v1", "openai/model"
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	if accountCatalog {
		if _, err := s.SaveOpenRouterManagementKey(profile.ID, "management-canary"); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "openrouter_global_pipeline_01", Discovery: "OpenRouter default"})
	if err != nil {
		t.Fatal(err)
	}
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: profile.ID}); err != nil {
		t.Fatal(err)
	}
	stage, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	input := GenerateAuthoringStageInput{Ref: AuthoringStageRefInput{PipelineID: pipeline.ID, Stage: "spec", RequestID: "openrouter_global_no_consent_01", PipelineRevision: stage.PipelineRevision, StageRevision: stage.Revision, DiscoveryVersion: stage.DiscoveryVersion, ArtifactVersion: stage.ArtifactVersion}, Selection: APIModelSelectionInput{MaxOutputTokens: 4096}}
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	authoringProviderFactoryCounter(s, provider)
	t.Cleanup(func() { Shutdown(s) })
	return s, db, input, provider, catalogCalls
}

func startSkippedUnloadedBrainstormForPreference(t *testing.T, s *Service, pipeline catalog.PipelineRun, setCatalog func(string)) BrainstormDTO {
	t.Helper()
	setCatalog(`{"models":[{"type":"llm","key":"local/brain","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":["low","high"],"default":"high"}}},{"type":"llm","key":"local/other","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":[],"default":""}}}]}`)
	listed, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil || !listed.Complete {
		t.Fatalf("unloaded Brainstorm catalog: %+v err=%v", listed, err)
	}
	selection := apiSelectionInput(listed, "lm", "local/brain", 256)
	selection.ReasoningEffort, selection.ConfirmJITLoad = "high", true
	brain, err := s.StartBrainstorming(StartBrainstormingInput{PipelineID: pipeline.ID, RequestID: "jit_brainstorm_start_01", PipelineRevision: pipeline.Revision, DiscoveryVersion: 1, Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	ref := BrainstormRequestInput{RunID: brain.ID, RequestID: "jit_brainstorm_skip_01", PipelineRevision: brain.PipelineRevision, RunRevision: brain.Revision, DiscoveryVersion: brain.DiscoveryVersion}
	skipped, err := s.SkipBrainstormQuestions(SkipBrainstormQuestionsInput{Ref: ref, Reason: "Discovery is enough"})
	if err != nil {
		t.Fatal(err)
	}
	ref.RequestID, ref.RunRevision, ref.PipelineRevision = "jit_brainstorm_confirm_01", skipped.Revision, skipped.PipelineRevision
	if _, err := s.ConfirmDiscoveryAfterSkip(ref); err != nil {
		t.Fatal(err)
	}
	return brain
}

func changedClientSelection(reasoning string) APIModelSelectionInput {
	return APIModelSelectionInput{
		ProfileID: "client-profile", ModelID: "client-model", CatalogRevision: "client-revision",
		Source: "openai_models", Destination: "https://client.example/v1", CheckedAt: time.Now().UTC(),
		CredentialToken: strings.Repeat("c", 64), ReasoningEffort: reasoning, MaxOutputTokens: 4096,
	}
}

func authoringProviderFactoryCounter(s *Service, provider *brainstormTestProvider) *atomic.Int32 {
	calls := new(atomic.Int32)
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		calls.Add(1)
		return provider, nil
	}
	return calls
}

func authoringReadOnlySessionCount(t *testing.T, db *sqlite.Store) int {
	t.Helper()
	var count int
	if err := db.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions WHERE mode='sdd_readonly'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAuthoringGenerationUsesResolvedPreferenceInsteadOfWailsSelection(t *testing.T) {
	s, db, pipeline, _, catalogCalls := authoringModelPreferenceFixture(t)
	brain := startSkippedBrainstormForPreference(t, s, pipeline)
	catalogBeforeGenerate := catalogCalls.Load()
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "wails_preference_start_01", changedClientSelection("high"))
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)

	draft, err := s.GenerateAuthoringStage(input)
	if err != nil {
		t.Fatalf("generation using stored Brainstorm preference: %v", err)
	}
	if draft.State != "waiting_user" || len(draft.Attempts) != 1 || factoryCalls.Load() != 1 || provider.calls.Load() != 1 {
		t.Fatalf("generation did not complete through the fake provider: %+v calls=%d provider=%d", draft, factoryCalls.Load(), provider.calls.Load())
	}
	if catalogCalls.Load() <= catalogBeforeGenerate {
		t.Fatal("active Generate did not refresh the model catalog")
	}
	attempt := draft.Attempts[0]
	if attempt.Selection.BackendID != brain.Selection.BackendID || attempt.Selection.ModelID != brain.Selection.ModelID || attempt.Selection.ReasoningEffort != "high" ||
		provider.requests[0].Model != brain.Selection.ModelID || provider.requests[0].ReasoningEffort != "high" || provider.requests[0].MaxOutputTokens != 4096 {
		t.Fatalf("client selection crossed the Wails boundary: attempt=%+v request=%+v", attempt, provider.requests[0])
	}
	for _, marker := range []string{`"modelMode":"inherit"`, `"effortMode":"inherit"`, `"preferenceSource":"brainstorm"`, `"preferenceRevision":0`} {
		data, marshalErr := json.Marshal(draft)
		if marshalErr != nil || !strings.Contains(string(data), marker) {
			t.Fatalf("attempt receipt omitted %s: %s %v", marker, data, marshalErr)
		}
	}
}

func TestAuthoringGenerationConsentRequiresBoundCatalogIdentity(t *testing.T) {
	_, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "consent_binding_validation_01", changedClientSelection(""))
	input.ConfirmJITLoad = true
	if _, err := authoringGenerationIntent(input, "start"); err == nil || ErrorCode(err) != "invalid_input" {
		t.Fatalf("one-attempt JIT consent without a selection binding was accepted: err=%v", err)
	}
	input.ConsentBinding = &AuthoringModelConsentBindingInput{
		BackendID: "lm", ModelID: "local/model", CatalogRevision: "revision", Source: "lm_studio_native",
		Destination: "https://provider.example:" + strings.Repeat("1", 510),
	}
	if _, err := authoringGenerationIntent(input, "start"); err == nil || ErrorCode(err) != "invalid_input" {
		t.Fatalf("oversized consent binding reached intent hashing: err=%v", err)
	}
}

func TestAuthoringGenerationRequiresFreshJITConsentForEachAttempt(t *testing.T) {
	s, db, pipeline, setCatalog, _ := authoringModelPreferenceFixture(t)
	brain := startSkippedUnloadedBrainstormForPreference(t, s, pipeline, setCatalog)
	preference, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
	if err != nil {
		t.Fatal(err)
	}
	if preference.Selection.ConfirmJITLoad {
		t.Fatal("Brainstorm JIT consent leaked into phase preference readback")
	}
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "jit_attempt_without_consent_01", changedClientSelection("high"))
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)
	if _, err := s.GenerateAuthoringStage(input); err == nil || ErrorCode(err) != "backend_changed" {
		t.Fatalf("stored Brainstorm JIT consent was reused without a current attempt signal: err=%v", err)
	}
	stage, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if err != nil || len(stage.Attempts) != 0 || factoryCalls.Load() != 0 || provider.calls.Load() != 0 {
		t.Fatalf("unconsented JIT attempt crossed admission: attempts=%d factory=%d provider=%d err=%v", len(stage.Attempts), factoryCalls.Load(), provider.calls.Load(), err)
	}

	input.Ref.RequestID = "jit_attempt_with_consent_01"
	input = authoringGenerationWithConsentBinding(t, input, true, false, map[string]any{
		"backendId": brain.Selection.BackendID, "modelId": brain.Selection.ModelID,
		"catalogRevision": brain.Selection.CatalogRevision, "source": brain.Selection.Source,
		"destination": brain.Selection.Destination,
	})
	draft, err := s.GenerateAuthoringStage(input)
	if err != nil || len(draft.Attempts) != 1 || provider.calls.Load() != 1 || !draft.Attempts[0].Selection.ConfirmJITLoad {
		t.Fatalf("explicit current-attempt JIT consent was not admitted: attempts=%d provider=%d err=%v", len(draft.Attempts), provider.calls.Load(), err)
	}

	revision := GenerateAuthoringStageInput{Ref: AuthoringStageRefInput{PipelineID: pipeline.ID, Stage: "spec", RequestID: "jit_revision_without_consent_01", PipelineRevision: draft.PipelineRevision, StageRevision: draft.Revision, DiscoveryVersion: draft.DiscoveryVersion, ArtifactVersion: draft.ArtifactVersion}, Selection: APIModelSelectionInput{MaxOutputTokens: 4096}, Feedback: "Revise a aceitação"}
	if _, err := s.RequestAuthoringStageRevision(revision); err == nil || ErrorCode(err) != "backend_changed" {
		t.Fatalf("JIT consent was reused across a later revision: err=%v", err)
	}
	stage, err = db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if err != nil || len(stage.Attempts) != 1 || provider.calls.Load() != 1 {
		t.Fatalf("unconsented later revision crossed admission: attempts=%d provider=%d err=%v", len(stage.Attempts), provider.calls.Load(), err)
	}
}

func TestGlobalOpenRouterGeneralRequiresAttemptConsentBeforeAdmission(t *testing.T) {
	s, db, input, provider, catalogCalls := authoringOpenRouterGenerationFixture(t, false)
	if _, err := s.GenerateAuthoringStage(input); err == nil || ErrorCode(err) != "backend_changed" {
		t.Fatalf("general unfiltered global catalog proceeded without attempt consent: err=%v", err)
	}
	stage, err := db.GetAuthoringStage(t.Context(), input.Ref.PipelineID, sdd.Spec)
	if err != nil || len(stage.Attempts) != 0 || provider.calls.Load() != 0 || catalogCalls.Load() == 0 {
		t.Fatalf("unconsented OpenRouter catalog crossed admission or skipped revalidation: attempts=%d provider=%d catalogs=%d err=%v", len(stage.Attempts), provider.calls.Load(), catalogCalls.Load(), err)
	}

	current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "router", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	input.Ref.RequestID = "openrouter_global_with_consent_01"
	input = authoringGenerationWithConsentBinding(t, input, false, true, map[string]any{
		"backendId": current.BackendID, "modelId": "openai/model", "catalogRevision": current.ProfileRevision,
		"source": current.Source, "destination": current.Destination,
	})
	draft, err := s.GenerateAuthoringStage(input)
	if err != nil || len(draft.Attempts) != 1 || provider.calls.Load() != 1 || !draft.Attempts[0].Selection.ConfirmUnfiltered {
		t.Fatalf("explicit unfiltered catalog consent was not admitted/audited: attempts=%d provider=%d err=%v", len(draft.Attempts), provider.calls.Load(), err)
	}
	replay := input
	replay.ConfirmUnfiltered = false
	replay.ConsentBinding = nil
	if _, err := s.GenerateAuthoringStage(replay); err == nil || ErrorCode(err) != "pipeline_request_conflict" {
		t.Fatalf("attempt consent was not part of the idempotency intent: err=%v", err)
	}
}

func TestGlobalOpenRouterAccountCatalogDoesNotRequireUnfilteredConsent(t *testing.T) {
	s, db, input, provider, _ := authoringOpenRouterGenerationFixture(t, true)
	draft, err := s.GenerateAuthoringStage(input)
	if err != nil || len(draft.Attempts) != 1 || provider.calls.Load() != 1 || draft.Attempts[0].Selection.Source != "openrouter_account" || draft.Attempts[0].Selection.ConfirmUnfiltered {
		t.Fatalf("account-filtered OpenRouter catalog incorrectly required unfiltered consent: attempts=%d provider=%d err=%v", len(draft.Attempts), provider.calls.Load(), err)
	}
	stage, err := db.GetAuthoringStage(t.Context(), input.Ref.PipelineID, sdd.Spec)
	if err != nil || len(stage.Attempts) != 1 {
		t.Fatalf("account-filtered attempt not persisted: attempts=%d err=%v", len(stage.Attempts), err)
	}
}

func TestAuthoringOpenRouterConsentForSelectedModelRemainsDurable(t *testing.T) {
	s, _, _, _, _ := authoringOpenRouterGenerationFixture(t, false)
	current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "router", Refresh: true})
	if err != nil || current.Source != "openrouter_general_unfiltered" {
		t.Fatalf("fresh general catalog: %+v err=%v", current, err)
	}
	input := apiSelectionInput(current, "router", "openai/model", 4096)
	input.ConfirmUnfiltered = true
	selected, err := s.prepareAPIModelSelection(t.Context(), input)
	if err != nil || !selected.ConfirmUnfiltered {
		t.Fatalf("explicit catalog consent was not attached to selected model: %+v err=%v", selected, err)
	}
	checked, err := s.revalidatedAuthoringModelSelection(t.Context(), *selected, authoringStageConsent{enforce: true})
	if err != nil || checked.Source != "openrouter_general_unfiltered" || !checked.ConfirmUnfiltered {
		t.Fatalf("consent for the same inherited model/catalog was not durable: %+v err=%v", checked, err)
	}
}

func TestAuthoringOverrideDoesNotConsumeConsentForInheritedParentModel(t *testing.T) {
	s, db, input, _, _ := authoringOpenRouterGenerationFixture(t, false)
	router, err := db.GetProviderProfile(t.Context(), "router")
	if err != nil {
		t.Fatal(err)
	}
	profile := profileInput()
	profile.ID, profile.Name, profile.ProviderType, profile.BaseURL, profile.Model = "api", "OpenAI", "openai", strings.TrimSuffix(router.BaseURL, "/api/v1")+"/v1", "openai/model"
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: profile.ID, Refresh: true})
	if err != nil || result.Source != "openai_models" {
		t.Fatalf("phase override catalog: %+v err=%v", result, err)
	}
	selection := apiSelectionInput(result, profile.ID, "openai/model", 4096)
	if _, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{PipelineID: input.Ref.PipelineID, Stage: "spec", ExpectedRevision: 0, ModelMode: "override", EffortMode: "inherit", Selection: selection}); err != nil {
		t.Fatal(err)
	}
	preference, err := s.store.GetAuthoringStageModelPreference(t.Context(), input.Ref.PipelineID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.resolveAuthoringStageModelPreferenceDetails(t.Context(), preference, authoringStageConsent{})
	if err != nil || resolved.DTO.Resolution != "ready" || resolved.DTO.ModelSource != "phase_override" || resolved.Selection.BackendID != "api" {
		t.Fatalf("unselected global OpenRouter parent consent blocked the API phase override: preference=%+v resolved=%+v err=%v", preference, resolved, err)
	}
}

func TestAuthoringOverrideCanInheritEffortFromUnloadedGlobalLMStudio(t *testing.T) {
	s, db, pipeline, setCatalog, _ := authoringModelPreferenceFixture(t)
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	setCatalog(`{"models":[{"type":"llm","key":"local/brain","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":["low","high"],"default":"high"}}},{"type":"llm","key":"local/other","loaded_instances":[{"id":"loaded-other","config":{"context_length":8192}}],"capabilities":{"reasoning":{"allowed_options":["high"],"default":"high"}}}]}`)
	catalogResult, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil || catalogResult.Source != "lm_studio_native" {
		t.Fatalf("phase override catalog: %+v err=%v", catalogResult, err)
	}
	selection := apiSelectionInput(catalogResult, "lm", "local/other", 4096)
	if _, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
		PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: 0, ModelMode: "override", EffortMode: "inherit", Selection: selection,
	}); err != nil {
		t.Fatal(err)
	}
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "override_unloaded_parent_01", changedClientSelection(""))
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)
	draft, err := s.GenerateAuthoringStage(input)
	if err != nil || len(draft.Attempts) != 1 || factoryCalls.Load() != 1 || provider.calls.Load() != 1 {
		t.Fatalf("unselected unloaded LM Studio parent blocked the selected override: attempts=%d factory=%d provider=%d err=%v", len(draft.Attempts), factoryCalls.Load(), provider.calls.Load(), err)
	}
	attempt := draft.Attempts[0]
	if attempt.Selection.BackendID != "lm" || attempt.Selection.ModelID != "local/other" || attempt.Selection.ReasoningEffort != "" || attempt.Selection.ConfirmJITLoad {
		t.Fatalf("attempt used or requested JIT for the unselected parent: %+v", attempt.Selection)
	}
}

func TestAttemptConsentCannotBeReusedAfterGlobalModelChanges(t *testing.T) {
	s, db, input, provider, _ := authoringOpenRouterGenerationFixture(t, false)
	current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "router", Refresh: true})
	if err != nil || current.Source != "openrouter_general_unfiltered" {
		t.Fatalf("initial general catalog: %+v err=%v", current, err)
	}
	input.Ref.RequestID = "openrouter_consent_for_model_a_01"
	input = authoringGenerationWithConsentBinding(t, input, false, true, map[string]any{
		"backendId": current.BackendID, "modelId": "openai/model", "catalogRevision": current.ProfileRevision,
		"source": current.Source, "destination": current.Destination,
	})
	router, err := db.GetProviderProfile(t.Context(), "router")
	if err != nil {
		t.Fatal(err)
	}
	profile := profileInput()
	profile.ID, profile.Name, profile.ProviderType, profile.BaseURL, profile.Model = router.ID, router.Name, router.ProviderType, router.BaseURL, "openai/other"
	profile.APIKey = "secret-canary-987"
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GenerateAuthoringStage(input); err == nil || ErrorCode(err) != "backend_changed" {
		t.Fatalf("consent for prior model was reused after the configured default changed: err=%v", err)
	}
	stage, err := db.GetAuthoringStage(t.Context(), input.Ref.PipelineID, sdd.Spec)
	if err != nil || len(stage.Attempts) != 0 || provider.calls.Load() != 0 || authoringReadOnlySessionCount(t, db) != 0 {
		t.Fatalf("changed-model consent crossed admission: attempts=%d provider=%d err=%v", len(stage.Attempts), provider.calls.Load(), err)
	}
}

func TestJITConsentCannotBeReusedAfterGlobalModelChanges(t *testing.T) {
	s, db, pipeline, setCatalog, _ := authoringModelPreferenceFixture(t)
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	setCatalog(`{"models":[{"type":"llm","key":"local/brain","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":["high"],"default":"high"}}},{"type":"llm","key":"local/other","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":["high"],"default":"high"}}}]}`)
	current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil || current.Source != "lm_studio_native" {
		t.Fatalf("initial unloaded LM Studio catalog: %+v err=%v", current, err)
	}
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "jit_consent_for_brain_model_01", changedClientSelection(""))
	input = authoringGenerationWithConsentBinding(t, input, true, false, map[string]any{
		"backendId": current.BackendID, "modelId": "local/brain", "catalogRevision": current.ProfileRevision,
		"source": current.Source, "destination": current.Destination,
	})
	profile, err := db.GetProviderProfile(t.Context(), "lm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(SaveProviderProfileInput{ID: profile.ID, Name: profile.Name, Kind: profile.Kind, ProviderType: profile.ProviderType, BaseURL: profile.BaseURL, Model: "local/other"}); err != nil {
		t.Fatal(err)
	}
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)
	if _, err := s.GenerateAuthoringStage(input); err == nil || ErrorCode(err) != "backend_changed" {
		t.Fatalf("JIT consent for the previous default model was reused: err=%v", err)
	}
	stage, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if err != nil || len(stage.Attempts) != 0 || factoryCalls.Load() != 0 || provider.calls.Load() != 0 || authoringReadOnlySessionCount(t, db) != 0 {
		t.Fatalf("changed-model JIT consent crossed admission: attempts=%d factory=%d provider=%d err=%v", len(stage.Attempts), factoryCalls.Load(), provider.calls.Load(), err)
	}
}

func TestConsentedOpenRouterPhaseOverrideRemainsUsable(t *testing.T) {
	s, _, input, provider, _ := authoringOpenRouterGenerationFixture(t, false)
	current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "router", Refresh: true})
	if err != nil || current.Source != "openrouter_general_unfiltered" {
		t.Fatalf("unfiltered override catalog: %+v err=%v", current, err)
	}
	selection := apiSelectionInput(current, "router", "openai/model", 4096)
	selection.ConfirmUnfiltered = true
	preference, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{PipelineID: input.Ref.PipelineID, Stage: "spec", ExpectedRevision: 0, ModelMode: "override", EffortMode: "automatic", Selection: selection})
	if err != nil || !preference.Selection.ConfirmUnfiltered {
		t.Fatalf("consented OpenRouter override was not saved: preference=%+v err=%v", preference, err)
	}
	input.Ref.RequestID = "openrouter_override_saved_consent_01"
	draft, err := s.GenerateAuthoringStage(input)
	if err != nil || len(draft.Attempts) != 1 || provider.calls.Load() != 1 || !draft.Attempts[0].Selection.ConfirmUnfiltered {
		t.Fatalf("saved override consent stopped working: attempts=%d provider=%d err=%v", len(draft.Attempts), provider.calls.Load(), err)
	}
}

func TestAuthoringGenerationBlocksUnconfiguredBeforeSessionOrInference(t *testing.T) {
	s, db, pipeline, _, catalogCalls := authoringModelPreferenceFixture(t)
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "wails_unconfigured_0001", changedClientSelection("high"))
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)

	_, err := s.GenerateAuthoringStage(input)
	if err == nil || ErrorCode(err) != "backend_not_found" || catalogCalls.Load() != 0 || factoryCalls.Load() != 0 || provider.calls.Load() != 0 {
		t.Fatalf("unconfigured start err=%v catalog=%d factory=%d inference=%d", err, catalogCalls.Load(), factoryCalls.Load(), provider.calls.Load())
	}
	stage, readErr := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if readErr != nil || len(stage.Attempts) != 0 || authoringReadOnlySessionCount(t, db) != 0 {
		t.Fatalf("unconfigured start created execution state: %+v %v", stage.Attempts, readErr)
	}
}

func TestAuthoringGenerationUsesGlobalDefaultWhenBrainstormWasSkipped(t *testing.T) {
	s, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "wails_global_skip_0001", changedClientSelection("high"))
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)

	draft, err := s.GenerateAuthoringStage(input)
	if err != nil {
		t.Fatalf("generation from global default after skipped Brainstorm: %v", err)
	}
	if len(draft.Attempts) != 1 || draft.Attempts[0].Selection.BackendID != "lm" || draft.Attempts[0].Selection.ModelID != "local/brain" ||
		draft.Attempts[0].Selection.ReasoningEffort != "" || factoryCalls.Load() != 1 || provider.calls.Load() != 1 ||
		provider.requests[0].Model != "local/brain" || provider.requests[0].ReasoningEffort != "" {
		t.Fatalf("global default or Auto was not frozen: %+v requests=%+v factory=%d provider=%d", draft.Attempts, provider.requests, factoryCalls.Load(), provider.calls.Load())
	}
	for _, marker := range []string{`"modelMode":"inherit"`, `"effortMode":"inherit"`, `"preferenceSource":"global_default"`, `"preferenceRevision":0`} {
		data, marshalErr := json.Marshal(draft)
		if marshalErr != nil || !strings.Contains(string(data), marker) {
			t.Fatalf("global attempt receipt omitted %s: %s %v", marker, data, marshalErr)
		}
	}
}

func TestAuthoringGenerationRejectsRemovedGlobalDefaultModelBeforeExecution(t *testing.T) {
	s, db, pipeline, setCatalog, catalogCalls := authoringModelPreferenceFixture(t)
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	setCatalog(`{"models":[{"type":"llm","key":"local/other","loaded_instances":[{"id":"loaded-other","config":{"context_length":8192}}],"capabilities":{"reasoning":{"allowed_options":[],"default":""}}}]}`)
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "global_model_removed_01", changedClientSelection("high"))
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)

	_, err := s.GenerateAuthoringStage(input)
	if err == nil || ErrorCode(err) != "backend_changed" || catalogCalls.Load() == 0 || factoryCalls.Load() != 0 || provider.calls.Load() != 0 {
		t.Fatalf("removed default model crossed inference gate: err=%v catalog=%d factory=%d inference=%d", err, catalogCalls.Load(), factoryCalls.Load(), provider.calls.Load())
	}
	stage, readErr := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if readErr != nil || len(stage.Attempts) != 0 || authoringReadOnlySessionCount(t, db) != 0 {
		t.Fatalf("removed model created execution state: %+v %v", stage.Attempts, readErr)
	}
}

func TestAuthoringGenerationStaleBrainstormDoesNotFallBackToGlobalDefault(t *testing.T) {
	s, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, pipeline)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	profile, err := db.GetProviderProfile(t.Context(), "lm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(SaveProviderProfileInput{ID: profile.ID, Name: profile.Name, Kind: profile.Kind, ProviderType: profile.ProviderType, BaseURL: profile.BaseURL, Model: "local/other"}); err != nil {
		t.Fatal(err)
	}
	current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "wails_stale_profile_01", apiSelectionInput(current, "lm", "local/other", 4096))
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)

	_, err = s.GenerateAuthoringStage(input)
	if err == nil || ErrorCode(err) != "backend_changed" || factoryCalls.Load() != 0 || provider.calls.Load() != 0 {
		t.Fatalf("stale Brainstorm used a global fallback: err=%v factory=%d provider=%d", err, factoryCalls.Load(), provider.calls.Load())
	}
	stage, readErr := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if readErr != nil || len(stage.Attempts) != 0 || authoringReadOnlySessionCount(t, db) != 0 {
		t.Fatalf("stale start created execution state: %+v %v", stage.Attempts, readErr)
	}
}

func TestAuthoringGenerationStaleDiscoveryDoesNotReusePriorBrainstorm(t *testing.T) {
	s, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, pipeline)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	const statuses = `{"discovery":"active","spec":"pending","plan":"pending","code":"pending","eval":"pending"}`
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='discovery',stage_status=?,discovery_frozen_version=2 WHERE id=?`, statuses, pipeline.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "wails_stale_discovery_01", apiSelectionInput(current, "lm", "local/brain", 4096))
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)

	_, err = s.GenerateAuthoringStage(input)
	if err == nil || ErrorCode(err) != "backend_changed" || factoryCalls.Load() != 0 || provider.calls.Load() != 0 {
		t.Fatalf("prior Discovery selection was reused or globally substituted: err=%v factory=%d provider=%d", err, factoryCalls.Load(), provider.calls.Load())
	}
	stage, readErr := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if readErr != nil || len(stage.Attempts) != 0 || authoringReadOnlySessionCount(t, db) != 0 {
		t.Fatalf("stale Discovery start created execution state: %+v %v", stage.Attempts, readErr)
	}
}

func TestAuthoringGenerationResolvesModelAndEffortModes(t *testing.T) {
	tests := []struct {
		name           string
		modelMode      string
		effortMode     string
		explicitEffort string
		clientModel    string
		clientEffort   string
		storedModel    string
		storedEffort   string
		modelSource    string
		preferenceRev  int64
	}{
		{name: "inherit", modelMode: "inherit", effortMode: "inherit", clientModel: "local/brain", clientEffort: "low", storedModel: "local/brain", storedEffort: "high", modelSource: "brainstorm"},
		{name: "automatic", modelMode: "inherit", effortMode: "automatic", clientModel: "local/brain", clientEffort: "high", storedModel: "local/brain", storedEffort: "", modelSource: "brainstorm", preferenceRev: 1},
		{name: "explicit", modelMode: "inherit", effortMode: "explicit", explicitEffort: "high", clientModel: "local/brain", clientEffort: "low", storedModel: "local/brain", storedEffort: "high", modelSource: "brainstorm", preferenceRev: 1},
		{name: "model override with automatic", modelMode: "override", effortMode: "automatic", clientModel: "local/brain", clientEffort: "high", storedModel: "local/other", storedEffort: "", modelSource: "phase_override", preferenceRev: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
			startSkippedBrainstormForPreference(t, s, pipeline)
			current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
			if err != nil {
				t.Fatal(err)
			}
			if test.preferenceRev == 1 {
				preference := SaveAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: 0, ModelMode: test.modelMode, EffortMode: test.effortMode, ExplicitEffort: test.explicitEffort}
				if test.modelMode == "override" {
					preference.Selection = apiSelectionInput(current, "lm", "local/other", 4096)
				}
				if _, err := s.SaveAuthoringStageModelPreference(preference); err != nil {
					t.Fatal(err)
				}
			}
			clientSelection := apiSelectionInput(current, "lm", test.clientModel, 4096)
			clientSelection.ReasoningEffort = test.clientEffort
			input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "effort_mode_generate_01", clientSelection)
			provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
			factoryCalls := authoringProviderFactoryCounter(s, provider)
			draft, err := s.GenerateAuthoringStage(input)
			if err != nil {
				t.Fatalf("generation: %v", err)
			}
			if len(draft.Attempts) != 1 || factoryCalls.Load() != 1 || provider.calls.Load() != 1 {
				t.Fatalf("generation did not execute: %+v factory=%d provider=%d", draft.Attempts, factoryCalls.Load(), provider.calls.Load())
			}
			attempt := draft.Attempts[0]
			if attempt.Selection.ModelID != test.storedModel || attempt.Selection.ReasoningEffort != test.storedEffort ||
				provider.requests[0].Model != test.storedModel || provider.requests[0].ReasoningEffort != test.storedEffort {
				t.Fatalf("resolved modes ignored: attempt=%+v request=%+v", attempt, provider.requests[0])
			}
			data, err := json.Marshal(draft)
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range []string{`"modelMode":"` + test.modelMode + `"`, `"effortMode":"` + test.effortMode + `"`, `"preferenceSource":"` + test.modelSource + `"`, `"preferenceRevision":` + fmt.Sprint(test.preferenceRev)} {
				if !strings.Contains(string(data), marker) {
					t.Fatalf("attempt receipt missing %s: %s", marker, data)
				}
			}
		})
	}
}

func TestAuthoringGenerationUpdateWinsBeforeStartAdmissionWithoutExecution(t *testing.T) {
	s, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, pipeline)
	current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	selection := apiSelectionInput(current, "lm", "local/brain", 4096)
	selection.ReasoningEffort = "high"
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "wails_update_race_0001", selection)
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	s.modelHTTPClient = &http.Client{Transport: brainstormHTTPTransport(func(request *http.Request) (*http.Response, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(preferenceCatalogWithEfforts)), Request: request}, nil
	})}
	type result struct {
		draft AuthoringStageDTO
		err   error
	}
	completed := make(chan result, 1)
	go func() {
		draft, err := s.GenerateAuthoringStage(input)
		completed <- result{draft: draft, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not reach catalog revalidation")
	}
	if _, err := db.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{PipelineID: pipeline.ID, Stage: sdd.Spec, ModelMode: "inherit", EffortMode: "automatic"}, 0); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case got := <-completed:
		if got.err == nil || ErrorCode(got.err) != "pipeline_conflict" || factoryCalls.Load() != 0 || provider.calls.Load() != 0 {
			t.Fatalf("start did not stop at the preference revision fence: err=%v factory=%d inference=%d", got.err, factoryCalls.Load(), provider.calls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not settle after preference update")
	}
	stage, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if err != nil || len(stage.Attempts) != 0 || authoringReadOnlySessionCount(t, db) != 0 {
		t.Fatalf("concurrent update left an attempt or session: %+v %v", stage.Attempts, err)
	}
}

func TestAuthoringGenerationDefaultChangeBeforeAdmissionConflictsWithoutExecution(t *testing.T) {
	s, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	profile, err := db.GetProviderProfile(t.Context(), "lm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(SaveProviderProfileInput{ID: "other", Name: "Other", Kind: profile.Kind, ProviderType: profile.ProviderType, BaseURL: profile.BaseURL, Model: "local/other"}); err != nil {
		t.Fatal(err)
	}
	input := authoringPreferenceGenerationInput(t, db, pipeline.ID, "wails_default_race_001", changedClientSelection("high"))
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	factoryCalls := authoringProviderFactoryCounter(s, provider)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	s.modelHTTPClient = &http.Client{Transport: brainstormHTTPTransport(func(request *http.Request) (*http.Response, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(preferenceCatalogWithEfforts)), Request: request}, nil
	})}
	type result struct {
		draft AuthoringStageDTO
		err   error
	}
	completed := make(chan result, 1)
	go func() {
		draft, err := s.GenerateAuthoringStage(input)
		completed <- result{draft: draft, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not reach global catalog validation")
	}
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "other"}); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case got := <-completed:
		if got.err == nil || ErrorCode(got.err) != "pipeline_conflict" || factoryCalls.Load() != 0 || provider.calls.Load() != 0 {
			t.Fatalf("changed global default crossed admission: err=%v factory=%d inference=%d", got.err, factoryCalls.Load(), provider.calls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not settle after global default update")
	}
	stage, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if err != nil || len(stage.Attempts) != 0 || authoringReadOnlySessionCount(t, db) != 0 {
		t.Fatalf("global default race created execution state: %+v %v", stage.Attempts, err)
	}
}

func TestAuthoringGenerationPublishesLinkedDraftAndReplaysOffline(t *testing.T) {
	s, db, in, _ := authoringGenerationFixture(t)
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		stored, err := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
		if err != nil || len(stored.Attempts) != 1 || stored.Attempts[0].SessionID == "" {
			t.Fatal("provider composed before durable link")
		}
		return provider, nil
	}
	draft, err := s.GenerateAuthoringStage(in)
	if err != nil {
		t.Fatal(err)
	}
	if draft.State != "waiting_user" || len(draft.Artifacts) != 1 || string(draft.Artifacts[0].Content) != authoringSpecOutput || draft.Attempts[0].Status != "completed" || provider.calls.Load() != 1 {
		t.Fatalf("generation: %+v %v", draft, err)
	}
	request := provider.requests[0]
	if len(request.Tools) != 0 || len(request.Messages) != 1 || request.Model != in.Selection.ModelID || request.MaxOutputTokens != 4096 || !strings.Contains(request.Messages[0].Content, "Immutable Discovery") || !strings.Contains(request.Messages[0].Content, "Discovery is sufficient") {
		t.Fatalf("request: %+v", request)
	}
	bytes, err := openai.RequestSize(request, "openai")
	if err != nil || draft.Attempts[0].ReservedInputTokens != int64(bytes)+1024 {
		t.Fatalf("request reservation: %d %+v %v", bytes, draft.Attempts[0], err)
	}
	pipeline, err := s.GetPipeline(in.Ref.PipelineID)
	if err != nil || pipeline.CurrentStage != "spec" || pipeline.StageStatus["spec"] != "waiting_user" {
		t.Fatal("draft bypassed human approval")
	}
	stored, _ := db.GetAuthoringStage(t.Context(), in.Ref.PipelineID, sdd.Spec)
	data, _ := json.Marshal(draft)
	if strings.Contains(string(data), stored.Attempts[0].Selection.CredentialIdentity) || strings.Contains(string(data), in.Selection.CredentialToken) {
		t.Fatal("private selection proof leaked")
	}
	s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	in.Selection.CredentialToken = ""
	replayed, err := s.GenerateAuthoringStage(in)
	if err != nil || !reflect.DeepEqual(draft, replayed) || provider.calls.Load() != 1 {
		t.Fatalf("offline replay: %+v %v", replayed, err)
	}
}

func TestAuthoringStageGetDoesNotGenerate(t *testing.T) {
	s, _, in, _ := authoringGenerationFixture(t)
	s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	run, err := s.GetAuthoringStage(GetAuthoringStageInput{PipelineID: in.Ref.PipelineID, Stage: "spec"})
	if err != nil || run.State != "ready" || len(run.Attempts) != 0 {
		t.Fatalf("readback: %+v %v", run, err)
	}
}

func TestAuthoringBudgetErrorHasStablePublicCode(t *testing.T) {
	if code := ErrorCode(sdd.ErrAuthoringBudgetExceeded); code != "authoring_budget_exceeded" {
		t.Fatalf("budget code: %q", code)
	}
}
