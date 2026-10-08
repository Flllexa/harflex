//go:build (linux && !android) || (darwin && !ios && cgo)

package application

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestAuthoringCodePreflightIsReadOnlyAndBindsCurrentState(t *testing.T) {
	s, db, created, _, catalogCalls := authoringModelPreferenceFixture(t)
	brain := startSkippedBrainstormForPreference(t, s, catalog.PipelineRun{ID: created.ID, Revision: created.Revision})
	markPipelineCodePreflightReady(t, db, created.ID, "completed")
	pipeline, err := db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace.Path, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "bin", "app"), []byte("local source"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, ".env"), []byte("PRIVATE_VALUE=local"), 0o600); err != nil {
		t.Fatal(err)
	}
	privateParent := t.TempDir()
	s.privateWorkspaceParent = privateParent

	beforeTables := codePreflightTableCounts(t, db)
	beforeParent, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	var emitted int
	s.emit = func(string, any) { emitted++ }
	catalogCalls.Store(0)

	result, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil {
		t.Fatalf("PreflightAuthoringCode() error = %v", err)
	}
	if result.PipelineRevision != pipeline.Revision || result.CodePreferenceRevision != 0 || result.ManifestHash == "" || result.ManifestHash != result.Manifest.Hash {
		t.Fatalf("preflight state binding = pipeline %d preference %d hash %q manifest hash %q", result.PipelineRevision, result.CodePreferenceRevision, result.ManifestHash, result.Manifest.Hash)
	}
	if result.CodePreference.Stage != "code" || result.CodePreference.ModelMode != "inherit" || result.CodePreference.Resolution != "ready" ||
		result.CodePreference.Selection.ModelID != brain.Selection.ModelID || result.CodePreference.ModelSource != "brainstorm" {
		t.Fatalf("Code inherited preference was not read from its persisted parent: %+v", result.CodePreference)
	}
	if result.SourcePath != workspace.Path || result.PrivateParentPath != privateParent || result.Manifest.FileCount != 1 || len(result.Manifest.Excluded) != 1 {
		t.Fatalf("preflight did not return the local source and bounded manifest: %+v", result)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PRIVATE_VALUE=local") || strings.Contains(string(data), "PRIVATE_VALUE") {
		t.Fatal("preflight returned source file contents")
	}
	if emitted != 0 || catalogCalls.Load() != 0 {
		t.Fatalf("preflight emitted an event or queried the provider catalog: emitted=%d catalogCalls=%d", emitted, catalogCalls.Load())
	}
	afterTables := codePreflightTableCounts(t, db)
	if !reflect.DeepEqual(beforeTables, afterTables) {
		t.Fatalf("preflight changed SQLite rows: before=%v after=%v", beforeTables, afterTables)
	}
	afterParent, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeParent) != len(afterParent) {
		t.Fatalf("preflight created a private copy: parent entries before=%d after=%d", len(beforeParent), len(afterParent))
	}

	_, err = s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision - 1, ExpectedCodePreferenceRevision: 0,
	})
	if !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("stale pipeline revision error = %v; want sqlite.ErrPipelineConflict", err)
	}
	_, err = s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 1,
	})
	if !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("stale Code preference revision error = %v; want sqlite.ErrPipelineConflict", err)
	}
}

func TestAuthoringCodePreflightRejectsLegacyPlanPendingAndInactiveCode(t *testing.T) {
	s, db, created, _, _ := authoringModelPreferenceFixture(t)
	markPipelineCodePreflightReady(t, db, created.ID, "pending")
	pipeline, err := db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("Plan pending preflight error = %v; want sdd.ErrInvalidTransition", err)
	}
	inactiveStatuses := `{"discovery":"completed","spec":"completed","plan":"completed","code":"pending","eval":"pending"}`
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET stage_status=?,revision=revision+1 WHERE id=?`, inactiveStatuses, created.ID); err != nil {
		t.Fatal(err)
	}
	pipeline, err = db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("inactive Code preflight error = %v; want sdd.ErrInvalidTransition", err)
	}

	legacyWorkspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: legacyWorkspace.ID, Title: "Legacy", Objective: "Manual legacy pipeline"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: legacy.ID, ExpectedPipelineRevision: legacy.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("legacy pipeline preflight error = %v; want sdd.ErrInvalidTransition", err)
	}
}

func TestAuthoringCodePreflightAcceptsSkippedPlanAndPreservesUnconfiguredState(t *testing.T) {
	s, db, created, _, catalogCalls := authoringModelPreferenceFixture(t)
	markPipelineCodePreflightReady(t, db, created.ID, "skipped")
	pipeline, err := db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	privateParent := t.TempDir()
	s.privateWorkspaceParent = privateParent
	catalogCalls.Store(0)

	result, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil {
		t.Fatalf("PreflightAuthoringCode() after a skipped Plan error = %v", err)
	}
	if result.CodePreference.Resolution != "unconfigured" || result.CodePreference.ModelSource != "global_default" || result.CodePreference.Selection.ModelID != "" {
		t.Fatalf("preflight replaced an unconfigured preference with a fallback: %+v", result.CodePreference)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("unconfigured preflight called the model catalog %d times", catalogCalls.Load())
	}
}

func TestAuthoringCodePreflightPreservesStalePreferenceWithoutFallback(t *testing.T) {
	s, db, created, _, catalogCalls := authoringModelPreferenceFixture(t)
	brain := startSkippedBrainstormForPreference(t, s, catalog.PipelineRun{ID: created.ID, Revision: created.Revision})
	markPipelineCodePreflightReady(t, db, created.ID, "completed")
	storedBrain, err := db.GetBrainstorming(t.Context(), brain.ID)
	if err != nil {
		t.Fatal(err)
	}
	staleSelection := storedBrain.Selection
	staleSelection.ModelID = "stale-code-model"
	staleSelection.CatalogRevision = "stale-catalog-revision"
	staleSelection.ReasoningEffort = ""
	if _, err := db.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
		PipelineID: created.ID, Stage: sdd.Code, ModelMode: "override", EffortMode: "automatic", Selection: staleSelection,
	}, 0); err != nil {
		t.Fatalf("save stale test preference from selection %+v: %v", staleSelection, err)
	}
	pipeline, err := db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.privateWorkspaceParent = t.TempDir()
	catalogCalls.Store(0)

	result, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 1,
	})
	if err != nil {
		t.Fatalf("PreflightAuthoringCode() with stale Code preference error = %v", err)
	}
	if result.CodePreference.Resolution != "stale" || result.CodePreference.ModelSource != "phase_override" || result.CodePreference.Selection.ModelID != "stale-code-model" {
		t.Fatalf("preflight replaced a stale Code override with a fallback: %+v", result.CodePreference)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("stale preference preflight called the model catalog %d times", catalogCalls.Load())
	}
}

func TestAuthoringCodePreflightPropagatesCatalogValidationRequired(t *testing.T) {
	s, db, created, _, catalogCalls := authoringModelPreferenceFixture(t)
	markPipelineCodePreflightReady(t, db, created.ID, "completed")
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
		PipelineID: created.ID, Stage: sdd.Code, ModelMode: "inherit", EffortMode: "explicit", ExplicitEffort: "high",
	}, 0); err != nil {
		t.Fatal(err)
	}
	pipeline, err := db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.privateWorkspaceParent = t.TempDir()
	catalogCalls.Store(0)

	result, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 1,
	})
	if err != nil {
		t.Fatalf("PreflightAuthoringCode() with pending catalog validation error = %v", err)
	}
	if result.CodePreference.Resolution != "ready" || !result.CodePreference.CatalogValidationRequired || result.CodePreference.ModelSource != "global_default" {
		t.Fatalf("preflight hid or refreshed required catalog validation: %+v", result.CodePreference)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("catalog-validation preflight called the model catalog %d times", catalogCalls.Load())
	}
}

func TestAuthoringCodePreflightFingerprintsEffectiveGlobalDefaultSelection(t *testing.T) {
	s, db, created, _, catalogCalls := authoringModelPreferenceFixture(t)
	markPipelineCodePreflightReady(t, db, created.ID, "completed")
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	pipeline, err := db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := db.GetProviderProfile(t.Context(), "lm")
	if err != nil {
		t.Fatal(err)
	}
	s.privateWorkspaceParent = t.TempDir()
	catalogCalls.Store(0)
	first, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.CodeSelectionHash == "" || first.CodePreference.Selection.ModelID != "local/brain" || !first.CodePreference.CatalogValidationRequired {
		t.Fatalf("global-default preflight did not bind the unverified local selection: %+v", first)
	}
	if _, err := s.SaveProviderProfile(SaveProviderProfileInput{
		ID: profile.ID, Name: profile.Name, Kind: profile.Kind, ProviderType: profile.ProviderType,
		BaseURL: profile.BaseURL, Model: "local/other",
	}); err != nil {
		t.Fatal(err)
	}
	second, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.CodePreference.Selection.ModelID != "local/other" || second.CodeSelectionHash == "" ||
		second.CodeSelectionHash == first.CodeSelectionHash || second.ManifestHash != first.ManifestHash {
		t.Fatalf("effective selection fingerprint did not follow a local default/profile change: first=%+v second=%+v", first, second)
	}
	if second.PipelineRevision != first.PipelineRevision || second.CodePreferenceRevision != first.CodePreferenceRevision || catalogCalls.Load() != 0 {
		t.Fatalf("selection fingerprint change altered revisions or called provider catalog: first=%d/%d second=%d/%d calls=%d",
			first.PipelineRevision, first.CodePreferenceRevision, second.PipelineRevision, second.CodePreferenceRevision, catalogCalls.Load())
	}
}

func markPipelineCodePreflightReady(t *testing.T, db *sqlite.Store, pipelineID, planStatus string) {
	t.Helper()
	statuses := `{"discovery":"completed","spec":"completed","plan":"` + planStatus + `","code":"active","eval":"pending"}`
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='code',stage_status=?,discovery_frozen_version=1,revision=revision+1 WHERE id=?`, statuses, pipelineID); err != nil {
		t.Fatal(err)
	}
}

func codePreflightTableCounts(t *testing.T, db *sqlite.Store) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	for _, table := range []string{
		"events", "sessions", "pipeline_runs", "pipeline_transitions", "pipeline_brainstorm_runs",
		"pipeline_brainstorm_attempts", "pipeline_brainstorm_requests", "pipeline_authoring_stages",
		"pipeline_authoring_attempts", "pipeline_authoring_artifacts", "pipeline_authoring_requests",
		"pipeline_authoring_model_preferences", "pipeline_authoring_code_copy_attempts",
	} {
		var count int64
		if err := db.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}
