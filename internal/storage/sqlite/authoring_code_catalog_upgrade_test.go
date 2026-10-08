package sqlite

import (
	"testing"
	"time"
)

// A catalog that ran the AI-authored Code line before it was merged with the execution line holds
// that line's tables under versions the execution line also used. Opening it with the merged
// migrations must leave those tables alone, widen the preference table and add what is missing.
func TestOpenUpgradesACatalogThatAlreadyHoldsTheAuthoringCodeTables(t *testing.T) {
	store, path, brain := authoringStageFixture(t)
	resetAuthoringModelPreferencesToV34(t, store)
	now := formatCatalogTime(time.Now().UTC())
	for _, statement := range []string{
		// The decision table is newer than the catalog being simulated.
		`DROP TABLE pipeline_authoring_code_decisions`,
		`DELETE FROM schema_migrations WHERE version IN (45, 46, 47)`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("prepare the earlier catalog: %v (%s)", err, statement)
		}
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO pipeline_authoring_model_preferences(pipeline_id,stage,model_mode,effort_mode,explicit_effort,model_selection_snapshot,revision,created_at,updated_at)
		VALUES(?, 'spec', 'inherit', 'explicit', 'high', NULL, 1, ?, ?)`, brain.PipelineID, now, now); err != nil {
		t.Fatalf("seed a preference in the earlier shape: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO pipeline_authoring_model_preferences(pipeline_id,stage,model_mode,effort_mode,explicit_effort,model_selection_snapshot,revision,created_at,updated_at)
		VALUES(?, 'code', 'inherit', 'inherit', '', NULL, 1, ?, ?)`, brain.PipelineID, now, now); err == nil {
		t.Fatal("the earlier preference table already accepts the code stage; the simulation is not an earlier catalog")
	}
	var before int
	if err := store.DB().QueryRowContext(t.Context(), authoringCodeObjectCount).Scan(&before); err != nil || before != 14 {
		t.Fatalf("authoring Code objects before upgrade = %d, %v; want the 14 the earlier line created", before, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("a catalog that already holds the authoring Code tables must open: %v", err)
	}
	defer upgraded.Close()
	var recorded int
	if err := upgraded.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations WHERE version BETWEEN 44 AND 47`).Scan(&recorded); err != nil || recorded != 4 {
		t.Fatalf("migrations 44-47 recorded = %d, %v; want 4", recorded, err)
	}
	var effort string
	if err := upgraded.DB().QueryRowContext(t.Context(), `SELECT explicit_effort FROM pipeline_authoring_model_preferences WHERE pipeline_id=? AND stage='spec'`, brain.PipelineID).Scan(&effort); err != nil || effort != "high" {
		t.Fatalf("preference row after the rebuild = %q, %v; want it preserved", effort, err)
	}
	if _, err := upgraded.DB().ExecContext(t.Context(), `INSERT INTO pipeline_authoring_model_preferences(pipeline_id,stage,model_mode,effort_mode,explicit_effort,model_selection_snapshot,revision,created_at,updated_at)
		VALUES(?, 'code', 'inherit', 'inherit', '', NULL, 1, ?, ?)`, brain.PipelineID, now, now); err != nil {
		t.Fatalf("the widened preference table must accept the code stage: %v", err)
	}
	var after, decisions int
	if err := upgraded.DB().QueryRowContext(t.Context(), authoringCodeObjectCount).Scan(&after); err != nil || after != 14 {
		t.Fatalf("authoring Code objects after upgrade = %d, %v; want the same 14, untouched", after, err)
	}
	if err := upgraded.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE name='pipeline_authoring_code_decisions'`).Scan(&decisions); err != nil || decisions != 1 {
		t.Fatalf("decision table after upgrade = %d, %v; want it created", decisions, err)
	}
}

// Both lines' tables are reachable from a catalog created today.
func TestFreshCatalogHoldsBothMigrationLines(t *testing.T) {
	store, _, _ := authoringStageFixture(t)
	for _, name := range []string{
		"pipeline_authoring_code_copy_attempts", "pipeline_authoring_code_runs", "pipeline_authoring_code_decisions",
		"pipeline_execution_reviews", "pipeline_archived_artifacts", "pipeline_design_workspaces",
	} {
		var found int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found); err != nil || found != 1 {
			t.Fatalf("table %s = %d, %v; want it in a fresh catalog", name, found, err)
		}
	}
	var versions, distinct int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*), COUNT(DISTINCT version) FROM schema_migrations`).Scan(&versions, &distinct); err != nil || versions != distinct || versions < lastMigrationVersion(t) {
		t.Fatalf("recorded migrations = %d (%d distinct), %v; want one per version up to %d", versions, distinct, err, lastMigrationVersion(t))
	}
}

const authoringCodeObjectCount = `SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'pipeline_authoring_code_copy%' OR name LIKE 'pipeline_authoring_code_runs%' OR name LIKE 'authoring_code_copy%' OR name LIKE 'authoring_code_runs%'`
