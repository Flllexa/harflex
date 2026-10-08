package application

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

const preferenceCatalogWithEfforts = `{"models":[
	{"type":"llm","key":"local/brain","display_name":"Brain","loaded_instances":[{"id":"loaded-brain","config":{"context_length":8192}}],"capabilities":{"reasoning":{"allowed_options":["low","high"],"default":"high"}}},
	{"type":"llm","key":"local/other","display_name":"Other","loaded_instances":[{"id":"loaded-other","config":{"context_length":8192}}],"capabilities":{"reasoning":{"allowed_options":[],"default":""}}}
]}`

func authoringModelPreferenceFixture(t *testing.T) (*Service, *sqlite.Store, catalog.PipelineRun, func(string), *atomic.Int32) {
	t.Helper()
	var response atomic.Value
	response.Store(preferenceCatalogWithEfforts)
	catalogCalls := new(atomic.Int32)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catalogCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"api/model","owned_by":"openai"}]}`)
			return
		}
		_, _ = io.WriteString(w, response.Load().(string))
	}))
	t.Cleanup(server.Close)
	db, err := sqlite.Open(t.Context(), t.TempDir()+"/authoring-preferences.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: &memorySecrets{values: map[secrets.Reference]string{}}, External: map[string]ExternalBackend{}})
	s.modelHTTPClient = server.Client()
	profile := profileInput()
	profile.ID, profile.ProviderType, profile.BaseURL, profile.Model, profile.APIKey = "lm", "lm_studio", server.URL+"/v1", "local/brain", ""
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "preference_pipeline_0001", Discovery: "Preference discovery"})
	if err != nil {
		t.Fatal(err)
	}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		t.Fatal("preference resolution must not start inference")
		return nil, nil
	}
	t.Cleanup(func() { Shutdown(s) })
	setCatalog := func(value string) { response.Store(value) }
	return s, db, catalog.PipelineRun{ID: pipeline.ID, Revision: pipeline.Revision}, setCatalog, catalogCalls
}

func startSkippedBrainstormForPreference(t *testing.T, s *Service, pipeline catalog.PipelineRun) BrainstormDTO {
	t.Helper()
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	selection := apiSelectionInput(result, "lm", "local/brain", 256)
	selection.ReasoningEffort = "high"
	brain, err := s.StartBrainstorming(StartBrainstormingInput{PipelineID: pipeline.ID, RequestID: "preference_brainstorm_0001", PipelineRevision: pipeline.Revision, DiscoveryVersion: 1, Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	ref := BrainstormRequestInput{RunID: brain.ID, RequestID: "preference_skip_questions_1", PipelineRevision: brain.PipelineRevision, RunRevision: brain.Revision, DiscoveryVersion: brain.DiscoveryVersion}
	skipped, err := s.SkipBrainstormQuestions(SkipBrainstormQuestionsInput{Ref: ref, Reason: "Discovery is enough"})
	if err != nil {
		t.Fatal(err)
	}
	ref.RequestID, ref.RunRevision, ref.PipelineRevision = "preference_confirm_skip_01", skipped.Revision, skipped.PipelineRevision
	if _, err := s.ConfirmDiscoveryAfterSkip(ref); err != nil {
		t.Fatal(err)
	}
	return brain
}

func TestAuthoringModelPreferenceInheritAndAutomaticAreDistinct(t *testing.T) {
	s, _, pipeline, _, _ := authoringModelPreferenceFixture(t)
	brain := startSkippedBrainstormForPreference(t, s, pipeline)
	input := GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"}

	inherited, err := s.GetAuthoringStageModelPreference(input)
	if err != nil {
		t.Fatal(err)
	}
	if inherited.ModelMode != "inherit" || inherited.EffortMode != "inherit" || inherited.Selection.BackendID != brain.Selection.BackendID ||
		inherited.Selection.ModelID != brain.Selection.ModelID || inherited.Selection.ReasoningEffort != "high" || inherited.Resolution != "ready" || inherited.ModelSource != "brainstorm" {
		t.Fatalf("unexpected inherited preference: %+v", inherited)
	}
	if len(inherited.Selection.SupportedReasoningEfforts) != 2 || inherited.Selection.SupportedReasoningEfforts[0] != "low" || inherited.Selection.SupportedReasoningEfforts[1] != "high" {
		t.Fatalf("capability levels missing: %+v", inherited.Selection)
	}

	automatic, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: 0, ModelMode: "inherit", EffortMode: "automatic"})
	if err != nil {
		t.Fatal(err)
	}
	if automatic.PreferenceRevision != 1 || automatic.ModelMode != "inherit" || automatic.EffortMode != "automatic" ||
		automatic.Selection.ModelID != brain.Selection.ModelID || automatic.Selection.ReasoningEffort != "" || automatic.EffortSource != "automatic" {
		t.Fatalf("Automatic did not clear inherited effort: %+v", automatic)
	}
	data, err := json.Marshal(automatic)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.store.GetBrainstorming(s.ctx, brain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), stored.Selection.CredentialIdentity) || strings.Contains(string(data), "credentialToken") || strings.Contains(string(data), "secret-canary-987") {
		t.Fatal("preference API exposed a credential or internal credential digest")
	}
}

func TestAuthoringStagePreferenceNeverPersistsJITConsent(t *testing.T) {
	s, db, pipeline, setCatalog, _ := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, pipeline)
	setCatalog(`{"models":[{"type":"llm","key":"local/brain","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":["low","high"],"default":"high"}}},{"type":"llm","key":"local/other","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":[],"default":""}}}]}`)
	current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil || current.Source != "lm_studio_native" || !current.Complete {
		t.Fatalf("fresh unloaded-model catalog: %+v err=%v", current, err)
	}
	selection := apiSelectionInput(current, "lm", "local/other", 4096)
	selection.ConfirmJITLoad = true // selection/save is not the generation consent.
	saved, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
		PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: 0, ModelMode: "override", EffortMode: "automatic", Selection: selection,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetAuthoringStageModelPreference(t.Context(), pipeline.ID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	readback, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Selection.ConfirmJITLoad || saved.Selection.ConfirmJITLoad || readback.Selection.ConfirmJITLoad {
		t.Fatalf("one-attempt JIT consent persisted or was exposed by preference readback: stored=%+v saved=%+v readback=%+v", stored.Selection, saved.Selection, readback.Selection)
	}
}

func TestAuthoringStagePreferenceCanSaveUnloadedModelWithoutInferenceConsent(t *testing.T) {
	s, _, pipeline, setCatalog, _ := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, pipeline)
	setCatalog(`{"models":[{"type":"llm","key":"local/brain","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":["low","high"],"default":"high"}}},{"type":"llm","key":"local/other","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":[],"default":""}}}]}`)
	current, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil || !current.Complete {
		t.Fatalf("fresh unloaded-model catalog: %+v err=%v", current, err)
	}
	selection := apiSelectionInput(current, "lm", "local/other", 4096)
	saved, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
		PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: 0, ModelMode: "override", EffortMode: "automatic", Selection: selection,
	})
	if err != nil || saved.Resolution != "ready" || saved.Selection.ConfirmJITLoad {
		t.Fatalf("saving an unloaded model incorrectly required inference consent or persisted it: preference=%+v err=%v", saved, err)
	}
}

func TestGetAuthoringStageModelPreferenceDoesNotQueryCatalog(t *testing.T) {
	t.Run("Brainstorm snapshot", func(t *testing.T) {
		s, _, pipeline, _, catalogCalls := authoringModelPreferenceFixture(t)
		brain := startSkippedBrainstormForPreference(t, s, pipeline)
		catalogCalls.Store(0)
		preference, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
		if err != nil || catalogCalls.Load() != 0 || preference.Resolution != "ready" || preference.Selection.ModelID != brain.Selection.ModelID ||
			len(preference.Selection.SupportedReasoningEfforts) != 2 || preference.Selection.ReasoningEffort != "high" {
			t.Fatalf("passive Brainstorm read preference=%+v catalogCalls=%d err=%v", preference, catalogCalls.Load(), err)
		}
		data, _ := json.Marshal(preference)
		if !strings.Contains(string(data), `"catalogValidationRequired":false`) {
			t.Fatalf("Brainstorm snapshot freshness missing: %s", data)
		}
	})

	t.Run("phase override snapshot", func(t *testing.T) {
		s, _, pipeline, _, catalogCalls := authoringModelPreferenceFixture(t)
		startSkippedBrainstormForPreference(t, s, pipeline)
		result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
		if err != nil {
			t.Fatal(err)
		}
		choice := apiSelectionInput(result, "lm", "local/other", 4096)
		beforeSave := catalogCalls.Load()
		if _, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: 0, ModelMode: "override", EffortMode: "automatic", Selection: choice}); err != nil {
			t.Fatal(err)
		}
		if catalogCalls.Load() <= beforeSave {
			t.Fatal("explicit model override save did not validate the current catalog")
		}
		catalogCalls.Store(0)
		preference, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
		if err != nil || catalogCalls.Load() != 0 || preference.Resolution != "ready" || preference.ModelSource != "phase_override" || preference.Selection.ModelID != "local/other" {
			t.Fatalf("passive override read preference=%+v catalogCalls=%d err=%v", preference, catalogCalls.Load(), err)
		}
		data, _ := json.Marshal(preference)
		if !strings.Contains(string(data), `"catalogValidationRequired":false`) {
			t.Fatalf("override catalog evidence missing: %s", data)
		}
	})

	t.Run("global default without catalog snapshot", func(t *testing.T) {
		s, db, pipeline, _, catalogCalls := authoringModelPreferenceFixture(t)
		markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
		if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
			t.Fatal(err)
		}
		catalogCalls.Store(0)
		preference, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
		if err != nil || catalogCalls.Load() != 0 || preference.Resolution != "ready" || preference.ModelSource != "global_default" ||
			preference.Selection.ModelID != "local/brain" || preference.Selection.ReasoningEffort != "" || len(preference.Selection.SupportedReasoningEfforts) != 0 {
			t.Fatalf("passive global read preference=%+v catalogCalls=%d err=%v", preference, catalogCalls.Load(), err)
		}
		data, _ := json.Marshal(preference)
		if !strings.Contains(string(data), `"catalogValidationRequired":true`) {
			t.Fatalf("global default was presented as catalog validated: %s", data)
		}
	})
}

func TestAuthoringModelOverrideAutomaticUsesNewModelWithoutFallback(t *testing.T) {
	s, _, pipeline, _, _ := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, pipeline)
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	choice := apiSelectionInput(result, "lm", "local/other", 4096)
	choice.ReasoningEffort = ""
	preference, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
		PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: 0,
		ModelMode: "override", EffortMode: "automatic", Selection: choice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preference.Resolution != "ready" || preference.ModelMode != "override" || preference.EffortMode != "automatic" ||
		preference.ModelSource != "phase_override" || preference.Selection.ModelID != "local/other" || preference.Selection.ReasoningEffort != "" || len(preference.Selection.SupportedReasoningEfforts) != 0 {
		t.Fatalf("phase Auto did not preserve the selected model: %+v", preference)
	}
}

func TestAuthoringModelOverrideCannotSilentlyChangeUnsupportedInheritedEffort(t *testing.T) {
	s, _, pipeline, _, _ := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, pipeline)
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	choice := apiSelectionInput(result, "lm", "local/other", 4096)
	stale, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
		PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: 0,
		ModelMode: "override", EffortMode: "inherit", Selection: choice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Resolution != "stale" || stale.Selection.ModelID != "local/other" || stale.Selection.ReasoningEffort != "high" || stale.ErrorCode == "" {
		t.Fatalf("unsupported inherited effort was hidden: %+v", stale)
	}
	automatic, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
		PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: stale.PreferenceRevision,
		ModelMode: "override", EffortMode: "automatic", Selection: choice,
	})
	if err != nil || automatic.Resolution != "ready" || automatic.Selection.ModelID != "local/other" || automatic.Selection.ReasoningEffort != "" {
		t.Fatalf("explicit Auto repair failed: %+v %v", automatic, err)
	}
	if _, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
		PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: automatic.PreferenceRevision,
		ModelMode: "override", EffortMode: "explicit", ExplicitEffort: "high", Selection: choice,
	}); err == nil {
		t.Fatal("explicit unsupported effort was accepted")
	}
}

func TestAuthoringModelPreferenceFailsClosedWhenBrainstormProfileChanges(t *testing.T) {
	s, _, pipeline, setCatalog, _ := authoringModelPreferenceFixture(t)
	brain := startSkippedBrainstormForPreference(t, s, pipeline)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	profile, err := s.store.GetProviderProfile(s.ctx, "lm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(SaveProviderProfileInput{ID: profile.ID, Name: profile.Name, Kind: profile.Kind, ProviderType: profile.ProviderType, BaseURL: profile.BaseURL, Model: "local/other"}); err != nil {
		t.Fatal(err)
	}
	setCatalog(preferenceCatalogWithEfforts)
	preference, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
	if err != nil {
		t.Fatal(err)
	}
	if preference.Resolution != "stale" || preference.ModelSource != "brainstorm" || preference.Selection.ModelID != brain.Selection.ModelID || preference.ErrorCode == "" {
		t.Fatalf("stale Brainstorm fell back to global default: %+v", preference)
	}
}

func TestAuthoringModelPreferenceDoesNotReuseBrainstormFromPriorDiscovery(t *testing.T) {
	s, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, pipeline)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	const status = `{"discovery":"active","spec":"pending","plan":"pending","code":"pending","eval":"pending"}`
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='discovery',stage_status=?,discovery_frozen_version=2 WHERE id=?`, status, pipeline.ID); err != nil {
		t.Fatal(err)
	}
	preference, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
	if err != nil || preference.Resolution != "stale" || preference.ModelSource != "brainstorm" ||
		preference.ErrorCode == "" {
		t.Fatalf("prior Discovery selection was reused or fell back globally: %+v %v", preference, err)
	}
}

func TestAuthoringModelPreferenceUsesGlobalAfterCurrentSkipDespitePriorHistory(t *testing.T) {
	s, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, pipeline)
	profile, err := db.GetProviderProfile(t.Context(), "lm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(SaveProviderProfileInput{ID: profile.ID, Name: profile.Name, Kind: profile.Kind, ProviderType: profile.ProviderType, BaseURL: profile.BaseURL, Model: "local/other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET discovery_frozen_version=2 WHERE id=?`, pipeline.ID); err != nil {
		t.Fatal(err)
	}
	preference, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
	if err != nil || preference.Resolution != "ready" || preference.ModelSource != "global_default" || preference.Selection.ModelID != "local/other" || preference.Selection.ReasoningEffort != "" {
		t.Fatalf("current skip did not use global default without reusing prior run: %+v %v", preference, err)
	}
}

func TestAuthoringModelPreferenceFallsBackToGlobalOnlyWhenBrainstormWasSkipped(t *testing.T) {
	s, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	preference, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
	if err != nil {
		t.Fatal(err)
	}
	if preference.Resolution != "ready" || preference.ModelSource != "global_default" || preference.EffortSource != "global_default" ||
		preference.ModelMode != "inherit" || preference.EffortMode != "inherit" || preference.Selection.BackendID != "lm" ||
		preference.Selection.ModelID != "local/brain" || preference.Selection.ReasoningEffort != "" {
		t.Fatalf("global fallback did not resolve the profile default: %+v", preference)
	}
}

func TestAuthoringModelPreferenceReportsUnconfiguredBeforeCatalogLookup(t *testing.T) {
	s, db, pipeline, _, catalogCalls := authoringModelPreferenceFixture(t)
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	preference, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
	if err != nil || preference.Resolution != "unconfigured" || preference.ErrorCode == "" || catalogCalls.Load() != 0 {
		t.Fatalf("unconfigured preference state=%+v catalogCalls=%d err=%v", preference, catalogCalls.Load(), err)
	}
}

func TestAuthoringModelPreferenceSupportsCodeAndEval(t *testing.T) {
	for _, stage := range []string{"code", "eval"} {
		t.Run(stage, func(t *testing.T) {
			s, _, pipeline, setCatalog, catalogCalls := authoringModelPreferenceFixture(t)
			brain := startSkippedBrainstormForPreference(t, s, pipeline)
			catalogCalls.Store(0)

			inherited, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: stage})
			if err != nil || inherited.ModelMode != "inherit" || inherited.EffortMode != "inherit" || inherited.Resolution != "ready" ||
				inherited.ModelSource != "brainstorm" || inherited.Selection.ModelID != brain.Selection.ModelID || inherited.Selection.ReasoningEffort != "high" {
				t.Fatalf("missing %s preference did not inherit: preference=%+v err=%v", stage, inherited, err)
			}
			if catalogCalls.Load() != 0 {
				t.Fatalf("GET %s preference queried catalog %d times", stage, catalogCalls.Load())
			}

			setCatalog(`{"models":[{"type":"llm","key":"local/brain","loaded_instances":[{"id":"loaded-brain","config":{"context_length":8192}}],"capabilities":{"reasoning":{"allowed_options":["low","high"],"default":"high"}}},{"type":"llm","key":"local/other","loaded_instances":[{"id":"loaded-other","config":{"context_length":8192}}],"capabilities":{"reasoning":{"allowed_options":["low","high"],"default":"low"}}}]}`)
			catalogResult, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
			if err != nil {
				t.Fatal(err)
			}
			selection := apiSelectionInput(catalogResult, "lm", "local/other", 4096)
			if _, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
				PipelineID: pipeline.ID, Stage: stage, ExpectedRevision: 0, ModelMode: "override", EffortMode: "explicit", ExplicitEffort: "medium", Selection: selection,
			}); err == nil {
				t.Fatalf("%s accepted an effort not confirmed for the selected model", stage)
			}
			override, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
				PipelineID: pipeline.ID, Stage: stage, ExpectedRevision: 0, ModelMode: "override", EffortMode: "explicit", ExplicitEffort: "high", Selection: selection,
			})
			if err != nil || override.PreferenceRevision != 1 || override.Resolution != "ready" || override.ModelSource != "phase_override" ||
				override.Selection.ModelID != "local/other" || override.Selection.ReasoningEffort != "high" {
				t.Fatalf("%s override failed: preference=%+v err=%v", stage, override, err)
			}

			catalogCalls.Store(0)
			readback, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: stage})
			if err != nil || catalogCalls.Load() != 0 || readback.PreferenceRevision != 1 || readback.Resolution != "ready" ||
				readback.ModelSource != "phase_override" || readback.Selection.ModelID != "local/other" || readback.Selection.ReasoningEffort != "high" {
				t.Fatalf("passive %s override readback=%+v catalogCalls=%d err=%v", stage, readback, catalogCalls.Load(), err)
			}
			if _, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
				PipelineID: pipeline.ID, Stage: stage, ExpectedRevision: 0, ModelMode: "inherit", EffortMode: "automatic",
			}); err == nil {
				t.Fatalf("stale %s revision was accepted", stage)
			}

			automatic, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
				PipelineID: pipeline.ID, Stage: stage, ExpectedRevision: 1, ModelMode: "inherit", EffortMode: "automatic",
			})
			if err != nil || automatic.PreferenceRevision != 2 || automatic.ModelMode != "inherit" || automatic.EffortMode != "automatic" ||
				automatic.Selection.ModelID != brain.Selection.ModelID || automatic.Selection.ReasoningEffort != "" || automatic.EffortSource != "automatic" {
				t.Fatalf("%s automatic preference did not clear inherited effort: preference=%+v err=%v", stage, automatic, err)
			}
			inheritedAgain, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
				PipelineID: pipeline.ID, Stage: stage, ExpectedRevision: 2, ModelMode: "inherit", EffortMode: "inherit",
			})
			if err != nil || inheritedAgain.PreferenceRevision != 3 || inheritedAgain.ModelMode != "inherit" || inheritedAgain.EffortMode != "inherit" ||
				inheritedAgain.Selection.ModelID != brain.Selection.ModelID || inheritedAgain.Selection.ReasoningEffort != "high" {
				t.Fatalf("%s inheritance did not resolve to parent: preference=%+v err=%v", stage, inheritedAgain, err)
			}
		})
	}
}

func TestAuthoringModelPreferenceAPIRejectsNonPreferenceStages(t *testing.T) {
	s, _, pipeline, _, _ := authoringModelPreferenceFixture(t)
	for _, stage := range []string{"discovery", "invalid"} {
		if _, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: stage}); err == nil {
			t.Fatalf("GET accepted preference stage %q", stage)
		}
		if _, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
			PipelineID: pipeline.ID, Stage: stage, ExpectedRevision: 0, ModelMode: "inherit", EffortMode: "inherit",
		}); err == nil {
			t.Fatalf("SAVE accepted preference stage %q", stage)
		}
	}
}

func TestAuthoringGlobalDefaultModelRemovalFailsClosed(t *testing.T) {
	s, db, pipeline, setCatalog, catalogCalls := authoringModelPreferenceFixture(t)
	markPipelineReadyWithoutBrainstorm(t, db, pipeline.ID)
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	setCatalog(`{"models":[{"type":"llm","key":"local/other","loaded_instances":[{"id":"loaded-other","config":{"context_length":8192}}],"capabilities":{"reasoning":{"allowed_options":[],"default":""}}}]}`)
	catalogCalls.Store(0)
	preference, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
	data, marshalErr := json.Marshal(preference)
	if err != nil || marshalErr != nil || preference.Resolution != "ready" || preference.ModelSource != "global_default" || !strings.Contains(string(data), `"catalogValidationRequired":true`) ||
		preference.Selection.ModelID != "local/brain" || preference.Selection.ReasoningEffort != "" || len(preference.Selection.SupportedReasoningEfforts) != 0 || catalogCalls.Load() != 0 {
		t.Fatalf("passive GET guessed or refreshed the global catalog: %+v catalogCalls=%d err=%v marshal=%v", preference, catalogCalls.Load(), err, marshalErr)
	}
}

func markPipelineReadyWithoutBrainstorm(t *testing.T, db *sqlite.Store, pipelineID string) {
	t.Helper()
	const statuses = `{"discovery":"completed","spec":"active","plan":"pending","code":"pending","eval":"pending"}`
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='spec',stage_status=?,discovery_frozen_version=1 WHERE id=?`, statuses, pipelineID); err != nil {
		t.Fatal(err)
	}
}

// Brainstorm's catalog snapshot lives five minutes. A phase override carries its own freshly
// validated model, so the expired parent must only lend its stored effort and never strand it.
func TestAuthoringOverrideInheritsStoredEffortAfterBrainstormSnapshotExpires(t *testing.T) {
	s, db, pipeline, _, _ := authoringModelPreferenceFixture(t)
	brain := startSkippedBrainstormForPreference(t, s, pipeline)
	var raw []byte
	if err := db.DB().QueryRowContext(t.Context(), `SELECT selection_snapshot FROM pipeline_brainstorm_runs WHERE id=?`, brain.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot["checkedAt"]; !ok {
		t.Fatalf("snapshot layout changed, cannot age it: %s", raw)
	}
	snapshot["checkedAt"] = time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339Nano)
	aged, _ := json.Marshal(snapshot)
	// The production trigger forbids rewriting the snapshot; this throwaway test database only simulates elapsed time.
	if _, err := db.DB().ExecContext(t.Context(), `DROP TRIGGER brainstorm_immutable_input`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_brainstorm_runs SET selection_snapshot=? WHERE id=?`, aged, brain.ID); err != nil {
		t.Fatal(err)
	}

	inherited, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
	if err != nil || inherited.Resolution != "stale" || inherited.ModelSource != "brainstorm" {
		t.Fatalf("an inherited model must still expire with its snapshot: %+v %v", inherited, err)
	}

	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	choice := apiSelectionInput(result, "lm", "local/brain", 4096)
	saved, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
		PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: inherited.PreferenceRevision,
		ModelMode: "override", EffortMode: "inherit", Selection: choice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Resolution != "ready" || saved.ErrorCode != "" || saved.ModelSource != "phase_override" || saved.InheritedFrom != "brainstorm" ||
		saved.Selection.ModelID != "local/brain" || saved.Selection.ReasoningEffort != "high" {
		t.Fatalf("a fresh override was stranded by the expired parent: %+v", saved)
	}
	read, err := s.GetAuthoringStageModelPreference(GetAuthoringStageModelPreferenceInput{PipelineID: pipeline.ID, Stage: "spec"})
	if err != nil || read.Resolution != "ready" || read.Selection.ReasoningEffort != "high" {
		t.Fatalf("readback disagrees with the save: %+v %v", read, err)
	}

	// The stored effort is still checked against the override model's advertised levels.
	other := apiSelectionInput(result, "lm", "local/other", 4096)
	conflict, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
		PipelineID: pipeline.ID, Stage: "spec", ExpectedRevision: saved.PreferenceRevision,
		ModelMode: "override", EffortMode: "inherit", Selection: other,
	})
	if err != nil || conflict.Resolution != "stale" || conflict.Selection.ReasoningEffort != "high" {
		t.Fatalf("an unsupported inherited effort must stay visible: %+v %v", conflict, err)
	}
}
