package sqlite

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func TestAuthoringModelPreferenceMigrationCreatesVersion34(t *testing.T) {
	store, path, brain := authoringStageFixture(t)
	const insertPreference = `INSERT INTO pipeline_authoring_model_preferences(
		pipeline_id,stage,model_mode,effort_mode,explicit_effort,
		model_selection_snapshot,revision,created_at,updated_at
	) VALUES (?,?,'override','automatic','',?,1,'2026-09-28T12:00:00Z','2026-09-28T12:00:00Z')`
	selectionSnapshot := []byte(`{"backendId":"api","modelId":"model-exact","credentialIdentity":"a2e44ac51a3f4c50e4d4f87b693e75f97f48d9ad912bff50321087ef779e7a81"}`)
	if _, err := store.DB().ExecContext(t.Context(), insertPreference, brain.PipelineID, "spec", selectionSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var applied int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE version=34`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("migration 34 applied = %d, want 1", applied)
	}
	var table string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name='pipeline_authoring_model_preferences'`).Scan(&table); err != nil {
		t.Fatalf("preference table missing: %v", err)
	}
	var modelMode, effortMode, effort string
	var selection []byte
	var revision int64
	if err := store.DB().QueryRowContext(t.Context(), `SELECT model_mode,effort_mode,explicit_effort,model_selection_snapshot,revision FROM pipeline_authoring_model_preferences WHERE pipeline_id=? AND stage='spec'`, brain.PipelineID).Scan(&modelMode, &effortMode, &effort, &selection, &revision); err != nil {
		t.Fatal(err)
	}
	if modelMode != "override" || effortMode != "automatic" || effort != "" || string(selection) != string(selectionSnapshot) || revision != 1 {
		t.Fatalf("migration/restart changed preference: %q %q %q %s %d", modelMode, effortMode, effort, selection, revision)
	}
}

func TestAuthoringModelPreferenceMigrationFromV34PreservesRowsAndAddsCodeEval(t *testing.T) {
	store, path, brain := authoringStageFixture(t)
	selection := brainstormChoice()
	selection.ReasoningEffort = ""
	if _, err := store.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
		PipelineID: brain.PipelineID, Stage: sdd.Spec, ModelMode: "override", EffortMode: "automatic", Selection: selection,
	}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
		PipelineID: brain.PipelineID, Stage: sdd.Spec, ModelMode: "override", EffortMode: "inherit", Selection: selection,
	}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
		PipelineID: brain.PipelineID, Stage: sdd.Plan, ModelMode: "inherit", EffortMode: "automatic",
	}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
		PipelineID: brain.PipelineID, Stage: sdd.Plan, ModelMode: "inherit", EffortMode: "inherit",
	}, 1); err != nil {
		t.Fatal(err)
	}
	beforeRows := readAuthoringModelPreferenceMigrationRows(t, store, brain.PipelineID)
	beforeTriggers := readAuthoringModelPreferenceTriggers(t, store)
	if len(beforeRows) != 2 || beforeRows[0].Revision != 2 || beforeRows[1].Revision != 2 {
		t.Fatalf("unexpected v34 seed rows: %+v", beforeRows)
	}
	resetAuthoringModelPreferencesToV34(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var applied int
	if err := upgraded.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE version=44`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("migration 35 applied = %d, want 1", applied)
	}
	if afterRows := readAuthoringModelPreferenceMigrationRows(t, upgraded, brain.PipelineID); !reflect.DeepEqual(afterRows, beforeRows) {
		t.Fatalf("migration changed v34 preferences:\nbefore=%+v\nafter=%+v", beforeRows, afterRows)
	}
	if afterTriggers := readAuthoringModelPreferenceTriggers(t, upgraded); !reflect.DeepEqual(afterTriggers, beforeTriggers) {
		t.Fatalf("migration changed preference triggers:\nbefore=%+v\nafter=%+v", beforeTriggers, afterTriggers)
	}

	for _, stage := range []sdd.Stage{sdd.Code, sdd.Eval} {
		missing, err := upgraded.GetAuthoringStageModelPreference(t.Context(), brain.PipelineID, stage)
		if err != nil || missing.ModelMode != "inherit" || missing.EffortMode != "inherit" || missing.Revision != 0 {
			t.Fatalf("missing %s preference: %+v err=%v", stage, missing, err)
		}
		preference := catalog.AuthoringStageModelPreference{PipelineID: brain.PipelineID, Stage: stage, ModelMode: "inherit", EffortMode: "automatic"}
		saved, err := upgraded.SaveAuthoringStageModelPreference(t.Context(), preference, 0)
		if err != nil || saved.Revision != 1 {
			t.Fatalf("save %s preference: %+v err=%v", stage, saved, err)
		}
		if _, err := upgraded.SaveAuthoringStageModelPreference(t.Context(), preference, 0); !errors.Is(err, ErrPipelineConflict) {
			t.Fatalf("stale %s revision = %v, want pipeline conflict", stage, err)
		}
	}
	if _, err := upgraded.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Code); err == nil {
		t.Fatal("Code became an authoring artifact stage")
	}
	if _, err := upgraded.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Eval); err == nil {
		t.Fatal("Eval became an authoring artifact stage")
	}
	for _, stage := range []sdd.Stage{sdd.Code, sdd.Eval} {
		in := catalog.BeginAuthoringStageRequest{
			Ref:       catalog.AuthoringStageRequest{PipelineID: brain.PipelineID, Stage: stage, RequestID: "stage_attempt_001", PipelineRevision: 1, DiscoveryVersion: 1},
			Selection: brainstormChoice(), ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm",
			EstimatedInputTokens: 1, ClientIntentHash: strings.Repeat("a", 64),
		}
		if _, admitted, err := upgraded.BeginAuthoringStage(t.Context(), in); err == nil || admitted {
			t.Fatalf("%s became an eligible authoring attempt: admitted=%v err=%v", stage, admitted, err)
		}
	}
	if _, err := upgraded.GetAuthoringStageModelPreference(t.Context(), brain.PipelineID, sdd.Discovery); err == nil {
		t.Fatal("Discovery became a model preference stage")
	}
	if _, err := upgraded.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
		PipelineID: brain.PipelineID, Stage: sdd.Discovery, ModelMode: "inherit", EffortMode: "inherit",
	}, 0); err == nil {
		t.Fatal("Discovery preference was accepted")
	}
	if _, err := upgraded.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET kind='legacy' WHERE id=?`, brain.PipelineID); err != nil {
		t.Fatal(err)
	}
	if _, err := upgraded.GetAuthoringStageModelPreference(t.Context(), brain.PipelineID, sdd.Code); err == nil {
		t.Fatal("legacy pipeline gained a Code preference")
	}
	if _, err := upgraded.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
		PipelineID: brain.PipelineID, Stage: sdd.Eval, ModelMode: "inherit", EffortMode: "inherit",
	}, 0); err == nil {
		t.Fatal("legacy pipeline gained an Eval preference")
	}
}

type authoringModelPreferenceMigrationRow struct {
	Stage, ModelMode, EffortMode, ExplicitEffort string
	Selection                                    []byte
	Revision                                     int64
	CreatedAt, UpdatedAt                         string
}

func readAuthoringModelPreferenceMigrationRows(t *testing.T, store *Store, pipelineID string) []authoringModelPreferenceMigrationRow {
	t.Helper()
	rows, err := store.DB().QueryContext(t.Context(), `SELECT stage,model_mode,effort_mode,explicit_effort,model_selection_snapshot,revision,created_at,updated_at FROM pipeline_authoring_model_preferences WHERE pipeline_id=? ORDER BY stage`, pipelineID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []authoringModelPreferenceMigrationRow
	for rows.Next() {
		var row authoringModelPreferenceMigrationRow
		if err := rows.Scan(&row.Stage, &row.ModelMode, &row.EffortMode, &row.ExplicitEffort, &row.Selection, &row.Revision, &row.CreatedAt, &row.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func readAuthoringModelPreferenceTriggers(t *testing.T, store *Store) map[string]string {
	t.Helper()
	rows, err := store.DB().QueryContext(t.Context(), `SELECT name,sql FROM sqlite_master WHERE type='trigger' AND name IN ('authoring_model_preference_revision','authoring_model_preference_no_delete') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		result[name] = definition
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func resetAuthoringModelPreferencesToV34(t *testing.T, store *Store) {
	t.Helper()
	for _, statement := range []string{
		`DROP TRIGGER IF EXISTS authoring_model_preference_revision`,
		`DROP TRIGGER IF EXISTS authoring_model_preference_no_delete`,
		`ALTER TABLE pipeline_authoring_model_preferences RENAME TO pipeline_authoring_model_preferences_v35`,
		`CREATE TABLE pipeline_authoring_model_preferences (
 pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
 stage TEXT NOT NULL CHECK(stage IN ('spec','plan')),
 model_mode TEXT NOT NULL CHECK(model_mode IN ('inherit','override')),
 effort_mode TEXT NOT NULL CHECK(effort_mode IN ('inherit','automatic','explicit')),
 explicit_effort TEXT NOT NULL DEFAULT '' CHECK(length(explicit_effort)<=64),
 model_selection_snapshot BLOB,
 revision INTEGER NOT NULL CHECK(revision>0),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY(pipeline_id,stage),
 CHECK((model_mode='inherit' AND model_selection_snapshot IS NULL) OR
       (model_mode='override' AND model_selection_snapshot IS NOT NULL)),
 CHECK((effort_mode='explicit' AND explicit_effort!='') OR (effort_mode!='explicit' AND explicit_effort=''))
)`,
		`INSERT INTO pipeline_authoring_model_preferences SELECT * FROM pipeline_authoring_model_preferences_v35`,
		`DROP TABLE pipeline_authoring_model_preferences_v35`,
		`CREATE TRIGGER authoring_model_preference_revision BEFORE UPDATE ON pipeline_authoring_model_preferences WHEN NEW.revision != OLD.revision + 1 BEGIN SELECT RAISE(ABORT,'invalid authoring model preference revision'); END`,
		`CREATE TRIGGER authoring_model_preference_no_delete BEFORE DELETE ON pipeline_authoring_model_preferences BEGIN SELECT RAISE(ABORT,'immutable authoring model preference history'); END`,
		`DELETE FROM schema_migrations WHERE version=44`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("reset v34 preference schema: %v (%s)", err, statement)
		}
	}
}

func TestAuthoringModelPreferenceStoreCASAndRestart(t *testing.T) {
	store, path, brain := authoringStageFixture(t)
	before, err := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := store.GetAuthoringStageModelPreference(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil || missing.ModelMode != "inherit" || missing.EffortMode != "inherit" || missing.Revision != 0 {
		t.Fatalf("missing preference default: %+v %v", missing, err)
	}
	selection := brainstormChoice()
	selection.ReasoningEffort = ""
	preference := catalog.AuthoringStageModelPreference{PipelineID: brain.PipelineID, Stage: sdd.Spec, ModelMode: "override", EffortMode: "automatic", Selection: selection}
	saved, err := store.SaveAuthoringStageModelPreference(t.Context(), preference, 0)
	if err != nil || saved.Revision != 1 {
		t.Fatalf("save preference: %+v %v", saved, err)
	}
	if _, err := store.SaveAuthoringStageModelPreference(t.Context(), preference, 0); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("stale update = %v, want pipeline conflict", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reopened, err := store.GetAuthoringStageModelPreference(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil || reopened.Revision != 1 || reopened.ModelMode != "override" || reopened.EffortMode != "automatic" ||
		reopened.Selection.BackendID != selection.BackendID || reopened.Selection.ModelID != selection.ModelID || reopened.Selection.CredentialIdentity != selection.CredentialIdentity {
		t.Fatalf("reopened preference: %+v %v", reopened, err)
	}
	data, err := json.Marshal(reopened)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), selection.CredentialIdentity) {
		t.Fatal("internal selection digest became a public preference field")
	}
	after, err := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil || after.Revision != before.Revision {
		t.Fatalf("preference edit changed stage revision: before=%d after=%d err=%v", before.Revision, after.Revision, err)
	}
}

func TestAuthoringStageAdmissionFencesPreferenceAndFreezesItsRevision(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	choice := brainstormChoice()
	choice.ReasoningEffort = ""
	preference := catalog.AuthoringStageModelPreference{PipelineID: brain.PipelineID, Stage: sdd.Spec, ModelMode: "override", EffortMode: "automatic", Selection: choice}
	if _, err := store.SaveAuthoringStageModelPreference(t.Context(), preference, 0); err != nil {
		t.Fatal(err)
	}
	in := catalog.BeginAuthoringStageRequest{
		Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "preference_fence_start_01"), Selection: choice,
		ModelMode: "override", EffortMode: "automatic", PreferenceSource: "phase_override", PreferenceRevision: 0,
		EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("a", 64),
	}
	if _, admitted, err := store.BeginAuthoringStage(t.Context(), in); !errors.Is(err, ErrPipelineConflict) || admitted {
		t.Fatalf("stale start admitted=%v err=%v", admitted, err)
	}
	in.PreferenceRevision = 1
	attempt, admitted, err := store.BeginAuthoringStage(t.Context(), in)
	if err != nil || !admitted || attempt.PreferenceRevision != 1 || attempt.ModelMode != "override" || attempt.EffortMode != "automatic" || attempt.PreferenceSource != "phase_override" {
		t.Fatalf("current start snapshot=%+v admitted=%v err=%v", attempt, admitted, err)
	}
	preference.EffortMode = "inherit"
	if _, err := store.SaveAuthoringStageModelPreference(t.Context(), preference, 1); err != nil {
		t.Fatal(err)
	}
	run, err := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil || len(run.Attempts) != 1 || run.Attempts[0].PreferenceRevision != 1 || run.Attempts[0].EffortMode != "automatic" || run.Attempts[0].Selection.ModelID != choice.ModelID {
		t.Fatalf("preference update rewrote attempt snapshot: %+v %v", run.Attempts, err)
	}
}

func TestAuthoringStageAdmissionUsesRevisionZeroWithoutPreference(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	choice := brainstormChoice()
	in := catalog.BeginAuthoringStageRequest{
		Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "preference_absent_start_01"), Selection: choice,
		ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", PreferenceRevision: 0,
		EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("a", 64),
	}
	attempt, admitted, err := store.BeginAuthoringStage(t.Context(), in)
	if err != nil || !admitted || attempt.PreferenceRevision != 0 || attempt.ModelMode != "inherit" || attempt.EffortMode != "inherit" {
		t.Fatalf("absent preference admission=%+v admitted=%v err=%v", attempt, admitted, err)
	}
}

func TestAuthoringStageAdmissionRejectsUntrustedRevisionZeroSelection(t *testing.T) {
	for _, test := range []struct {
		name       string
		modelMode  string
		effortMode string
		source     string
	}{
		{name: "missing origin", modelMode: "inherit", effortMode: "inherit"},
		{name: "phase override without stored preference", modelMode: "override", effortMode: "automatic", source: "phase_override"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, brain := authoringStageFixture(t)
			choice := brainstormChoice()
			choice.ReasoningEffort = ""
			in := catalog.BeginAuthoringStageRequest{
				Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "untrusted_revision_zero_01"), Selection: choice,
				ModelMode: test.modelMode, EffortMode: test.effortMode, PreferenceSource: test.source, PreferenceRevision: 0,
				EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("d", 64),
			}
			if _, admitted, err := store.BeginAuthoringStage(t.Context(), in); err == nil || admitted {
				t.Fatalf("untrusted revision zero admitted=%v err=%v", admitted, err)
			}
		})
	}
}

func TestAuthoringStageAdmissionBindsBrainstormSourceToCanonicalSelection(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	selection := brainstormChoice()
	selection.ModelID = "attacker/model"
	in := catalog.BeginAuthoringStageRequest{
		Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "wrong_brain_selection_01"), Selection: selection,
		ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", PreferenceRevision: 0,
		EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("e", 64),
	}
	if _, admitted, err := store.BeginAuthoringStage(t.Context(), in); !errors.Is(err, ErrPipelineConflict) || admitted {
		t.Fatalf("source/selection mismatch admitted=%v err=%v", admitted, err)
	}
	run, err := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil || len(run.Attempts) != 0 {
		t.Fatalf("mismatched source created an attempt: %+v %v", run.Attempts, err)
	}
}

func TestAuthoringStageAdmissionBindsPhaseOverrideAndAutomaticEffort(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*catalog.ModelSelection)
	}{
		{name: "model identity", mutate: func(selection *catalog.ModelSelection) { selection.ModelID = "attacker/model" }},
		{name: "automatic effort", mutate: func(selection *catalog.ModelSelection) { selection.ReasoningEffort = "low" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, brain := authoringStageFixture(t)
			selection := brainstormChoice()
			selection.ReasoningEffort = ""
			selection.SupportedReasoningEfforts = []string{"low"}
			preference := catalog.AuthoringStageModelPreference{PipelineID: brain.PipelineID, Stage: sdd.Spec, ModelMode: "override", EffortMode: "automatic", Selection: selection}
			if _, err := store.SaveAuthoringStageModelPreference(t.Context(), preference, 0); err != nil {
				t.Fatal(err)
			}
			attemptSelection := selection
			test.mutate(&attemptSelection)
			in := catalog.BeginAuthoringStageRequest{
				Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "phase_override_source_01"), Selection: attemptSelection,
				ModelMode: "override", EffortMode: "automatic", PreferenceSource: "phase_override", PreferenceRevision: 1,
				EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("f", 64),
			}
			if _, admitted, err := store.BeginAuthoringStage(t.Context(), in); !errors.Is(err, ErrPipelineConflict) || admitted {
				t.Fatalf("phase override source mismatch admitted=%v err=%v", admitted, err)
			}
		})
	}
}

func TestAuthoringStageAdmissionRechecksGlobalDefaultInTransaction(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, store *Store, profile catalog.ProviderProfile)
	}{
		{name: "default backend changed", mutate: func(t *testing.T, store *Store, _ catalog.ProviderProfile) {
			t.Helper()
			if err := store.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: "global-b"}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "profile model changed", mutate: func(t *testing.T, store *Store, profile catalog.ProviderProfile) {
			t.Helper()
			profile.Model = "different-model"
			profile.UpdatedAt = profile.UpdatedAt.Add(time.Second)
			if err := store.UpsertProviderProfile(t.Context(), profile); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, pipeline := authoringFixture(t)
			if err := store.CreateAuthoringPipeline(t.Context(), pipeline, "Global default Discovery"); err != nil {
				t.Fatal(err)
			}
			const status = `{"discovery":"completed","spec":"active","plan":"pending","code":"pending","eval":"pending"}`
			if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='spec',stage_status=?,discovery_frozen_version=1 WHERE id=?`, status, pipeline.ID); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			profile := catalog.ProviderProfile{ID: "global-a", Name: "Global A", Kind: "openai_compatible", ProviderType: "openai", BaseURL: "https://provider.example/v1", Model: "model-a", CreatedAt: now, UpdatedAt: now}
			other := catalog.ProviderProfile{ID: "global-b", Name: "Global B", Kind: "openai_compatible", ProviderType: "openai", BaseURL: "https://provider.example/v1", Model: "model-b", CreatedAt: now, UpdatedAt: now.Add(time.Second)}
			if err := store.UpsertProviderProfile(t.Context(), profile); err != nil {
				t.Fatal(err)
			}
			if err := store.UpsertProviderProfile(t.Context(), other); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: profile.ID}); err != nil {
				t.Fatal(err)
			}
			selection := catalog.ModelSelection{BackendID: profile.ID, ModelID: profile.Model, ReasoningEffort: "", CatalogRevision: "profile-revision", Source: "openai_models", Destination: "https://provider.example", CredentialIdentity: strings.Repeat("a", 64), Status: "listed", MaxOutputTokens: 4096, ContextLength: 8192, CheckedAt: now}
			in := catalog.BeginAuthoringStageRequest{
				Ref: stageRef(t, store, pipeline.ID, sdd.Spec, "global_default_fence_01"), Selection: selection,
				ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "global_default", PreferenceRevision: 0,
				EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("a", 64),
			}
			test.mutate(t, store, profile)
			if _, admitted, err := store.BeginAuthoringStage(t.Context(), in); !errors.Is(err, ErrPipelineConflict) || admitted {
				t.Fatalf("stale global source admitted=%v err=%v", admitted, err)
			}
			run, err := store.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
			if err != nil || len(run.Attempts) != 0 {
				t.Fatalf("stale global source created attempt: %+v %v", run.Attempts, err)
			}
		})
	}
}

func TestConcurrentPreferenceUpdateAndStartHaveOneSQLiteOrder(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	choice := brainstormChoice()
	start := catalog.BeginAuthoringStageRequest{
		Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "preference_race_start_01"), Selection: choice,
		ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", PreferenceRevision: 0,
		EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("b", 64),
	}
	update := catalog.AuthoringStageModelPreference{PipelineID: brain.PipelineID, Stage: sdd.Spec, ModelMode: "inherit", EffortMode: "automatic"}
	gate := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var admittedAttempt catalog.AuthoringStageAttempt
	var admitted bool
	var startErr error
	var saved catalog.AuthoringStageModelPreference
	var updateErr error
	go func() {
		defer wait.Done()
		<-gate
		admittedAttempt, admitted, startErr = store.BeginAuthoringStage(t.Context(), start)
	}()
	go func() {
		defer wait.Done()
		<-gate
		saved, updateErr = store.SaveAuthoringStageModelPreference(t.Context(), update, 0)
	}()
	close(gate)
	wait.Wait()
	if updateErr != nil || saved.Revision != 1 {
		t.Fatalf("preference update: %+v %v", saved, updateErr)
	}
	run, err := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case admitted && startErr == nil:
		if len(run.Attempts) != 1 || admittedAttempt.PreferenceRevision != 0 || run.Attempts[0].PreferenceRevision != 0 {
			t.Fatalf("start-first order lost its immutable snapshot: %+v %+v", admittedAttempt, run.Attempts)
		}
	case !admitted && errors.Is(startErr, ErrPipelineConflict):
		if len(run.Attempts) != 0 {
			t.Fatalf("update-first order admitted a stale attempt: %+v", run.Attempts)
		}
	default:
		t.Fatalf("ambiguous update/start order: admitted=%v err=%v attempts=%d", admitted, startErr, len(run.Attempts))
	}
}
