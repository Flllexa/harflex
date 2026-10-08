package sqlite

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func TestAuthoringStageMigrationPreservesLegacyAndDiscoveryGate(t *testing.T) {
	store, path, brain := authoringStageFixture(t)
	pBefore, err := store.GetPipeline(t.Context(), brain.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	bBefore, err := store.GetBrainstorming(t.Context(), brain.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy := pBefore
	legacy.ID = "legacy-authoring-migration"
	legacy.Kind = "legacy"
	legacy.Revision = 1
	legacy.DiscoveryFrozenVersion = 0
	if err := store.CreatePipeline(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePipelineArtifact(t.Context(), legacy.ID, sdd.Spec, "Historical manual SPEC", 1); err != nil {
		t.Fatal(err)
	}
	lBefore, err := store.GetPipeline(t.Context(), legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Restore v30 before replaying the real v31-v34 upgrade chain.
	for _, statement := range []string{
		`DROP TABLE pipeline_authoring_model_preferences`,
		`DROP TABLE pipeline_authoring_stops`,
		`DROP TRIGGER session_model_selection_immutable`,
		`ALTER TABLE session_model_selections DROP COLUMN supported_reasoning_efforts`,
		`DROP TABLE pipeline_authoring_requests`,
		`DROP TABLE pipeline_authoring_artifacts`,
		`DROP TABLE pipeline_authoring_attempts`,
		`DROP TABLE pipeline_authoring_stages`,
		`DELETE FROM schema_migrations WHERE version IN (31,32,33,34,44)`,
	} {
		if _, err := store.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var remainingMarkers int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE version IN (31,32,33,34,44)`).Scan(&remainingMarkers); err != nil {
		t.Fatal(err)
	}
	if remainingMarkers != 0 {
		t.Fatalf("downgrade is not v30; migrations 31-34 and 44 still marked: %d", remainingMarkers)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var appliedMarkers int
	if err := upgraded.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE version IN (31,32,33,34,44)`).Scan(&appliedMarkers); err != nil {
		t.Fatal(err)
	}
	if appliedMarkers != 5 {
		t.Fatalf("upgrade did not apply migrations 31-34 and 44: %d", appliedMarkers)
	}
	var preferenceTable, stopTable string
	if err := upgraded.DB().QueryRowContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name='pipeline_authoring_model_preferences'`).Scan(&preferenceTable); err != nil {
		t.Fatalf("preference table missing after upgrade: %v", err)
	}
	if err := upgraded.DB().QueryRowContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name='pipeline_authoring_stops'`).Scan(&stopTable); err != nil {
		t.Fatalf("authoring stop table missing after upgrade: %v", err)
	}
	for _, query := range []string{`SELECT model_mode,preference_revision FROM pipeline_authoring_attempts LIMIT 0`, `SELECT supported_reasoning_efforts FROM session_model_selections LIMIT 0`} {
		statement, err := upgraded.DB().PrepareContext(t.Context(), query)
		if err != nil {
			t.Fatalf("schema column missing after upgrade (%s): %v", query, err)
		}
		if err := statement.Close(); err != nil {
			t.Fatal(err)
		}
	}
	pAfter, err := upgraded.GetPipeline(t.Context(), brain.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	bAfter, err := upgraded.GetBrainstorming(t.Context(), brain.ID)
	if err != nil {
		t.Fatal(err)
	}
	lAfter, err := upgraded.GetPipeline(t.Context(), legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pBefore, pAfter) || !reflect.DeepEqual(bBefore, bAfter) || !reflect.DeepEqual(lBefore, lAfter) {
		t.Fatal("migration rewrote existing evidence")
	}
	if _, err := upgraded.GetAuthoringStage(t.Context(), legacy.ID, sdd.Spec); err == nil {
		t.Fatal("legacy became AI authoring")
	}
	in := stageAdmission(t, upgraded, brain.PipelineID, sdd.Spec, "migrated_stage_001")
	if _, admitted, err := upgraded.BeginAuthoringStage(t.Context(), in); err != nil || !admitted {
		t.Fatalf("new phase after migration: %v %v", admitted, err)
	}
	for _, stage := range []sdd.Stage{sdd.Code, sdd.Eval} {
		preference, err := upgraded.GetAuthoringStageModelPreference(t.Context(), brain.PipelineID, stage)
		if err != nil || preference.ModelMode != "inherit" || preference.EffortMode != "inherit" || preference.Revision != 0 {
			t.Fatalf("phase %s preference after full upgrade: %+v err=%v", stage, preference, err)
		}
	}
}

func TestAuthoringStageDraftSurvivesRestartWithoutApproval(t *testing.T) {
	store, path, brain := authoringStageFixture(t)
	draft := completedSpec(t, store, brain.PipelineID)
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.InterruptRunningAuthoringStages(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := other.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil || !reflect.DeepEqual(draft, after) {
		t.Fatalf("restart changed draft: %+v %v", after, err)
	}
	p, err := other.GetPipeline(t.Context(), brain.PipelineID)
	if err != nil || p.Current != sdd.Spec || p.Status[sdd.Spec] != sdd.WaitingUser {
		t.Fatalf("restart approved phase: %+v %v", p, err)
	}
	ref := stageRef(t, other, brain.PipelineID, sdd.Spec, "restart_approval_01")
	approved, err := other.ApproveAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.ApproveAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: ref})
	if err != nil || !reflect.DeepEqual(approved, replayed) {
		t.Fatal("restart approval receipt changed")
	}
}

func TestCrossStoreAuthoringStageReadUsesOneSnapshot(t *testing.T) {
	writer, path, brain := authoringStageFixture(t)
	in := stageAdmission(t, writer, brain.PipelineID, sdd.Spec, "snapshot_generate_1")
	a, _, err := writer.BeginAuthoringStage(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	before, err := writer.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil {
		t.Fatal(err)
	}
	ref := stageRef(t, writer, brain.PipelineID, sdd.Spec, "snapshot_cancel_01")
	gate := newSnapshotStatementGate()
	reader := openSnapshotTraceStore(t, path, gate)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	release := sync.OnceFunc(func() { close(gate.release) })
	defer release()
	type readResult struct {
		run catalog.AuthoringStageRun
		err error
	}
	result := make(chan readResult, 1)
	go func() {
		run, err := reader.GetAuthoringStage(ctx, brain.PipelineID, sdd.Spec)
		result <- readResult{run, err}
	}()
	select {
	case <-gate.observed:
	case <-ctx.Done():
		t.Fatal("reader did not establish snapshot")
	}
	if _, err := writer.CancelAuthoringStage(ctx, catalog.AuthoringStageDecisionRequest{Ref: ref, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case got := <-result:
		if got.err != nil || !reflect.DeepEqual(before, got.run) {
			t.Fatalf("mixed snapshot: %+v %v", got.run, got.err)
		}
	case <-ctx.Done():
		t.Fatal("reader did not finish")
	}
	after, err := writer.GetAuthoringStage(ctx, brain.PipelineID, sdd.Spec)
	if err != nil || after.State != "paused" || after.Revision != before.Revision+1 || after.PipelineRevision != before.PipelineRevision+1 {
		t.Fatalf("cancel not committed: %+v %v", after, err)
	}
}
