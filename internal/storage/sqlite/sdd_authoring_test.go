package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/migrations"
)

func authoringFixture(t *testing.T) (*Store, string, catalog.PipelineRun) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sdd.db")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: "workspace", Path: "/project", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	flow := sdd.NewFlow()
	run := catalog.PipelineRun{ID: "authoring", WorkspaceID: "workspace", Kind: "ai_authoring", Title: "Title from user", Objective: "Objective from user", Current: flow.Current, Status: flow.Status, Revision: 1, CreatedAt: now, UpdatedAt: now}
	return store, path, run
}

func TestSDDMigrationPreservesLegacyPipelineAndArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		prefix, _, _ := strings.Cut(name, "_")
		version, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatal(err)
		}
		if version >= 23 {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations VALUES (?, '2026-09-28T12:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}
	const timestamp = "2026-09-28T12:00:00Z"
	if _, err := raw.Exec(`INSERT INTO workspaces (id,path,profile,created_at) VALUES ('workspace','/legacy','ask',?)`, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO streams (id,kind,created_at) VALUES ('legacy','sdd_pipeline',?)`, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO pipeline_runs (id,workspace_id,title,objective,current_stage,stage_status,revision,created_at,updated_at) VALUES ('legacy','workspace','Old title','Old objective','spec','{"discovery":"completed","spec":"active"}',4,?,?)`, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO pipeline_artifacts (pipeline_id,stage,version,content,updated_at) VALUES ('legacy','discovery',2,'Full old Discovery',?)`, timestamp); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	for cycle := range 2 {
		store, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := store.GetPipeline(t.Context(), "legacy")
		if err != nil || got.Kind != "legacy" || got.Title != "Old title" || got.Objective != "Old objective" || got.Revision != int64(4+cycle) || got.DiscoveryFrozenVersion != 0 || got.DerivedFromPipelineID != "" {
			t.Fatalf("legacy run: %+v, %v", got, err)
		}
		artifact := got.Artifacts[sdd.Discovery]
		if artifact.Content != "Full old Discovery" || artifact.Version != 2 || artifact.Author != "legacy/manual" || artifact.SourceSessionID != "" {
			t.Fatalf("legacy artifact: %+v", artifact)
		}
		if cycle == 0 {
			if err := store.SavePipelineArtifact(t.Context(), "legacy", sdd.Spec, "Old manual SPEC", got.Revision); err != nil {
				t.Fatalf("legacy write: %v", err)
			}
		} else if got.Artifacts[sdd.Spec].Content != "Old manual SPEC" || got.Artifacts[sdd.Spec].Author != "legacy/manual" {
			t.Fatalf("legacy SPEC after restart: %+v", got.Artifacts[sdd.Spec])
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCreateAuthoringPipelineIsAtomicAndPreservesDiscovery(t *testing.T) {
	store, _, run := authoringFixture(t)
	const discovery = "# Integral\n\nObjective, context, constraints, risks, acceptance."
	if err := store.CreateAuthoringPipeline(t.Context(), run, discovery); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil || got.Kind != "ai_authoring" || got.Title != run.Title || got.Objective != run.Objective || got.Revision != 1 || got.DiscoveryFrozenVersion != 0 {
		t.Fatalf("authoring run: %+v, %v", got, err)
	}
	artifact := got.Artifacts[sdd.Discovery]
	if artifact.Version != 1 || artifact.Content != discovery || artifact.Author != "user" || artifact.SourceSessionID != "" {
		t.Fatalf("Discovery changed: %+v", artifact)
	}
	listed, err := store.ListPipelines(t.Context(), run.WorkspaceID)
	if err != nil || len(listed) != 1 || listed[0].Kind != "ai_authoring" || listed[0].DiscoveryFrozenVersion != 0 {
		t.Fatalf("authoring list: %+v, %v", listed, err)
	}
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE stream_id = ?`, run.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("events = %d, %v", count, err)
	}
	var first, second string
	if err := store.DB().QueryRow(`SELECT type FROM events WHERE stream_id = ? AND sequence = 1`, run.ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT type FROM events WHERE stream_id = ? AND sequence = 2`, run.ID).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first != "pipeline.created" || second != "pipeline.artifact.saved" {
		t.Fatalf("event sequence = %q, %q", first, second)
	}

	failed := run
	failed.ID = "stream-already-exists"
	if _, err := store.DB().Exec(`INSERT INTO streams (id,kind,created_at) VALUES (?,?,?)`, failed.ID, "sdd_pipeline", formatCatalogTime(failed.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAuthoringPipeline(t.Context(), failed, discovery); err == nil {
		t.Fatal("stream conflict was accepted")
	}
	for _, table := range []string{"pipeline_runs", "pipeline_artifacts", "events"} {
		column := "id"
		if table == "pipeline_artifacts" {
			column = "pipeline_id"
		} else if table == "events" {
			column = "stream_id"
		}
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+column+` = ?`, failed.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rollback count = %d, %v", table, count, err)
		}
	}
	if _, err := store.DB().Exec(`CREATE TRIGGER reject_discovery_event BEFORE INSERT ON events WHEN NEW.type = 'pipeline.artifact.saved' AND NEW.stream_id = 'late-failure' BEGIN SELECT RAISE(ABORT, 'test late failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed.ID = "late-failure"
	if err := store.CreateAuthoringPipeline(t.Context(), failed, discovery); err == nil {
		t.Fatal("late event failure was accepted")
	}
	for _, table := range []string{"pipeline_runs", "pipeline_artifacts", "streams", "events"} {
		column := "id"
		if table == "pipeline_artifacts" {
			column = "pipeline_id"
		} else if table == "events" {
			column = "stream_id"
		}
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+column+` = ?`, failed.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s late rollback count = %d, %v", table, count, err)
		}
	}
}

func TestFreezeDiscoveryRequiresExactRevisionAndArtifactVersion(t *testing.T) {
	store, _, run := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), run, "Initial Discovery"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ revision, version int64 }{{0, 1}, {1, 2}} {
		if err := store.FreezeDiscovery(t.Context(), run.ID, test.revision, test.version); !errors.Is(err, ErrPipelineConflict) {
			t.Fatalf("freeze stale (%d,%d): %v", test.revision, test.version, err)
		}
	}
	if err := store.ReviseAuthoringDiscovery(t.Context(), run.ID, 1, 1, "Revised Discovery", "Revised title", "Revised objective"); err != nil {
		t.Fatal(err)
	}
	if err := store.FreezeDiscovery(t.Context(), run.ID, 2, 1); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("freeze old artifact version: %v", err)
	}
	if err := store.FreezeDiscovery(t.Context(), run.ID, 2, 2); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil || got.DiscoveryFrozenVersion != 2 || got.Revision != 3 || got.Artifacts[sdd.Discovery].Content != "Revised Discovery" {
		t.Fatalf("frozen run: %+v, %v", got, err)
	}
	if err := store.FreezeDiscovery(t.Context(), run.ID, 3, 2); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("duplicate freeze: %v", err)
	}
	if err := store.SavePipelineArtifact(t.Context(), run.ID, sdd.Discovery, "overwrite", got.Revision); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("frozen Discovery edit: %v", err)
	}
}

func TestDeriveAuthoringPipelinePreservesParentAcrossRestart(t *testing.T) {
	store, path, parent := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), parent, "Original Discovery"); err != nil {
		t.Fatal(err)
	}
	child := parent
	child.ID, child.Title, child.Objective, child.DerivedFromPipelineID = "child", "Derived title", "Derived objective", parent.ID
	if err := store.DeriveAuthoringPipeline(t.Context(), parent.ID, 1, child, "Fresh Discovery"); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("derived unfrozen parent: %v", err)
	}
	if err := store.FreezeDiscovery(t.Context(), parent.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.DeriveAuthoringPipeline(t.Context(), parent.ID, 1, child, "Fresh Discovery"); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("derived stale parent: %v", err)
	}
	if err := store.DeriveAuthoringPipeline(t.Context(), parent.ID, 2, child, "Fresh Discovery"); err != nil {
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
	gotParent, err := store.GetPipeline(t.Context(), parent.ID)
	if err != nil || gotParent.Revision != 2 || gotParent.DiscoveryFrozenVersion != 1 || gotParent.Artifacts[sdd.Discovery].Content != "Original Discovery" {
		t.Fatalf("parent changed: %+v, %v", gotParent, err)
	}
	gotChild, err := store.GetPipeline(t.Context(), child.ID)
	if err != nil || gotChild.DerivedFromPipelineID != parent.ID || gotChild.Kind != "ai_authoring" || gotChild.DiscoveryFrozenVersion != 0 || gotChild.Title != child.Title || gotChild.Objective != child.Objective || gotChild.Artifacts[sdd.Discovery].Content != "Fresh Discovery" || gotChild.Artifacts[sdd.Discovery].Author != "user" {
		t.Fatalf("derived child: %+v, %v", gotChild, err)
	}
	if _, err := store.DB().Exec(`DELETE FROM pipeline_runs WHERE id = ?`, parent.ID); err == nil {
		t.Fatal("parent deleted while child refers to it")
	}
}

func TestGenericArtifactWriteGuardsAIAuthoringButKeepsLegacyBehavior(t *testing.T) {
	store, _, run := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), run, "Discovery"); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []sdd.Stage{sdd.Spec, sdd.Plan, sdd.Code, sdd.Eval} {
		if err := store.SavePipelineArtifact(t.Context(), run.ID, stage, "manual overwrite", 1); !errors.Is(err, sdd.ErrInvalidTransition) {
			t.Fatalf("manual %s accepted: %v", stage, err)
		}
	}
	got, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil || got.Revision != 1 || len(got.Artifacts) != 1 {
		t.Fatalf("blocked writes mutated run: %+v, %v", got, err)
	}
	if err := store.SavePipelineArtifact(t.Context(), run.ID, sdd.Discovery, " \n\t ", 1); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("blank authoring Discovery accepted: %v", err)
	}
	got, err = store.GetPipeline(t.Context(), run.ID)
	if err != nil || got.Revision != 1 || got.Artifacts[sdd.Discovery].Version != 1 || got.Artifacts[sdd.Discovery].Content != "Discovery" {
		t.Fatalf("blank write mutated Discovery: %+v, %v", got, err)
	}
	var eventCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE stream_id = ?`, run.ID).Scan(&eventCount); err != nil || eventCount != 2 {
		t.Fatalf("blank write event count = %d, %v", eventCount, err)
	}
	if err := store.SavePipelineArtifact(t.Context(), run.ID, sdd.Discovery, "Discovery revised", 1); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("generic authoring Discovery edit accepted: %v", err)
	}
	if err := store.ReviseAuthoringDiscovery(t.Context(), run.ID, 1, 1, "Discovery revised", "Revised title", "Revised objective"); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetPipeline(t.Context(), run.ID)
	if err != nil || got.Artifacts[sdd.Discovery].Author != "user" || got.Artifacts[sdd.Discovery].Version != 2 {
		t.Fatalf("authoring revision provenance: %+v, %v", got, err)
	}
	legacy := run
	legacy.ID, legacy.Kind = "legacy", "legacy"
	if err := store.CreatePipeline(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePipelineArtifact(t.Context(), legacy.ID, sdd.Discovery, "manual Discovery", 1); err != nil {
		t.Fatal(err)
	}
	legacyGot, err := store.GetPipeline(t.Context(), legacy.ID)
	if err != nil || legacyGot.Kind != "legacy" || legacyGot.Artifacts[sdd.Discovery].Author != "legacy/manual" {
		t.Fatalf("legacy behavior: %+v, %v", legacyGot, err)
	}
}

func TestReviseAuthoringDiscoveryCASUpdatesSummaryAndArtifactAtomically(t *testing.T) {
	store, _, run := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), run, "Original Discovery"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name              string
		revision, version int64
		content           string
		want              error
	}{
		{"stale pipeline", 0, 1, "valid replacement", ErrPipelineConflict},
		{"stale artifact", 1, 2, "valid replacement", ErrPipelineConflict},
		{"blank Discovery", 1, 1, " \n\t ", sdd.ErrInvalidTransition},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := store.ReviseAuthoringDiscovery(t.Context(), run.ID, test.revision, test.version, test.content, "Wrong title", "Wrong objective"); !errors.Is(err, test.want) {
				t.Fatalf("revision result = %v, want %v", err, test.want)
			}
			got, err := store.GetPipeline(t.Context(), run.ID)
			if err != nil || got.Revision != 1 || got.Title != run.Title || got.Objective != run.Objective || got.Artifacts[sdd.Discovery].Version != 1 || got.Artifacts[sdd.Discovery].Content != "Original Discovery" {
				t.Fatalf("rejected edit changed state: %+v, %v", got, err)
			}
		})
	}
	var events int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE stream_id = ?`, run.ID).Scan(&events); err != nil || events != 2 {
		t.Fatalf("rejected edits emitted %d events: %v", events, err)
	}
	if err := store.ReviseAuthoringDiscovery(t.Context(), run.ID, 1, 1, "# New full Discovery\n\nDetails", "New title", "New objective"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil || got.Revision != 2 || got.Title != "New title" || got.Objective != "New objective" || got.Artifacts[sdd.Discovery].Version != 2 || got.Artifacts[sdd.Discovery].Content != "# New full Discovery\n\nDetails" || got.Artifacts[sdd.Discovery].Author != "user" {
		t.Fatalf("revision was not atomic: %+v, %v", got, err)
	}
	var eventType string
	var data []byte
	if err := store.DB().QueryRow(`SELECT type,data FROM events WHERE stream_id = ? AND sequence = 3`, run.ID).Scan(&eventType, &data); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Stage   string `json:"stage"`
		Version int64  `json:"version"`
		Author  string `json:"author"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || eventType != "pipeline.artifact.saved" || payload.Stage != "discovery" || payload.Version != 2 || payload.Author != "user" {
		t.Fatalf("revision event = %s %+v, %v", eventType, payload, err)
	}
	if err := store.ReviseAuthoringDiscovery(t.Context(), run.ID, 1, 1, "Stale overwrite", "Stale title", "Stale objective"); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("stale editor overwrote revision: %v", err)
	}
	if err := store.FreezeDiscovery(t.Context(), run.ID, 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.ReviseAuthoringDiscovery(t.Context(), run.ID, 3, 2, "Frozen overwrite", "Frozen title", "Frozen objective"); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("frozen Discovery overwritten: %v", err)
	}
}

func TestReviseAuthoringDiscoveryRejectsOneOfTwoSimultaneousEditors(t *testing.T) {
	store, _, run := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), run, "Original Discovery"); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	type result struct {
		content string
		err     error
	}
	results := make(chan result, 2)
	var group sync.WaitGroup
	for _, content := range []string{"First complete Discovery", "Second complete Discovery"} {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- result{content, store.ReviseAuthoringDiscovery(ctx, run.ID, 1, 1, content, content+" title", content+" objective")}
		}()
	}
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	winner := ""
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result.content
		case errors.Is(result.err, ErrPipelineConflict):
			conflicts++
		default:
			t.Fatalf("unexpected editor result: %v", result.err)
		}
	}
	got, err := store.GetPipeline(ctx, run.ID)
	if err != nil || successes != 1 || conflicts != 1 || got.Revision != 2 || got.Artifacts[sdd.Discovery].Version != 2 || got.Artifacts[sdd.Discovery].Content != winner || got.Title != winner+" title" || got.Objective != winner+" objective" {
		t.Fatalf("concurrent editors: successes=%d conflicts=%d winner=%q run=%+v err=%v", successes, conflicts, winner, got, err)
	}
}

func TestReviseAuthoringDiscoveryRollsBackSummaryOnLateEventFailure(t *testing.T) {
	store, _, run := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), run, "Original Discovery"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TRIGGER reject_revision_event BEFORE INSERT ON events WHEN NEW.type = 'pipeline.artifact.saved' AND NEW.stream_id = 'authoring' AND NEW.sequence = 3 BEGIN SELECT RAISE(ABORT, 'test late failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.ReviseAuthoringDiscovery(t.Context(), run.ID, 1, 1, "New Discovery", "New title", "New objective"); err == nil {
		t.Fatal("late event failure was accepted")
	}
	got, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil || got.Revision != 1 || got.Title != run.Title || got.Objective != run.Objective || got.Artifacts[sdd.Discovery].Version != 1 || got.Artifacts[sdd.Discovery].Content != "Original Discovery" {
		t.Fatalf("late failure left partial revision: %+v, %v", got, err)
	}
	var events int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE stream_id = ?`, run.ID).Scan(&events); err != nil || events != 2 {
		t.Fatalf("late failure event count = %d, %v", events, err)
	}
}

func TestGenericTransitionRejectsAuthoringBeforeAndAfterFreeze(t *testing.T) {
	for _, frozen := range []bool{false, true} {
		name := "unfrozen"
		if frozen {
			name = "frozen"
		}
		t.Run(name, func(t *testing.T) {
			store, _, run := authoringFixture(t)
			if err := store.CreateAuthoringPipeline(t.Context(), run, "Discovery"); err != nil {
				t.Fatal(err)
			}
			if frozen {
				if err := store.FreezeDiscovery(t.Context(), run.ID, 1, 1); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.GetPipeline(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			flow := sdd.Flow{Current: before.Current, Status: before.Status}
			advance, err := sdd.Advance(flow, true)
			if err != nil {
				t.Fatal(err)
			}
			skip, err := sdd.Skip(flow)
			if err != nil {
				t.Fatal(err)
			}
			for _, attempt := range []struct {
				action string
				next   sdd.Flow
			}{{"advance", advance}, {"skip", skip}} {
				if err := store.TransitionPipeline(t.Context(), run.ID, sdd.Discovery, attempt.next, attempt.action, "reason", before.Revision); !errors.Is(err, sdd.ErrInvalidTransition) {
					t.Fatalf("%s escaped authoring Discovery: %v", attempt.action, err)
				}
			}
			after, err := store.GetPipeline(t.Context(), run.ID)
			if err != nil || after.Current != sdd.Discovery || after.Revision != before.Revision || after.Status[sdd.Discovery] != sdd.Active || after.DiscoveryFrozenVersion != before.DiscoveryFrozenVersion {
				t.Fatalf("blocked transition mutated run: %+v, %v", after, err)
			}
			var transitions, events int
			if err := store.DB().QueryRow(`SELECT COUNT(*) FROM pipeline_transitions WHERE pipeline_id = ?`, run.ID).Scan(&transitions); err != nil || transitions != 0 {
				t.Fatalf("blocked transition records = %d, %v", transitions, err)
			}
			expectedEvents := 2
			if frozen {
				expectedEvents++
			}
			if err := store.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE stream_id = ?`, run.ID).Scan(&events); err != nil || events != expectedEvents {
				t.Fatalf("blocked transition events = %d, %v", events, err)
			}
		})
	}

	store, _, legacy := authoringFixture(t)
	legacy.ID, legacy.Kind = "legacy", "legacy"
	if err := store.CreatePipeline(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	advance, err := sdd.Advance(sdd.Flow{Current: legacy.Current, Status: legacy.Status}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionPipeline(t.Context(), legacy.ID, sdd.Discovery, advance, "advance", "", 1); err != nil {
		t.Fatalf("legacy transition rejected: %v", err)
	}
	got, err := store.GetPipeline(t.Context(), legacy.ID)
	if err != nil || got.Current != sdd.Spec || got.Revision != 2 || got.Status[sdd.Discovery] != sdd.Completed {
		t.Fatalf("legacy transition changed: %+v, %v", got, err)
	}
}
