package sqlite

import (
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/migrations"
)

func TestAuthoringRequestRootUniquenessAndCompleteLookup(t *testing.T) {
	store, _, first := authoringFixture(t)
	first.CreationRequestID = "same-request"
	first.CreationRequestHash = "original-hash"
	if err := store.CreateAuthoringPipeline(t.Context(), first, "Original Discovery"); err != nil {
		t.Fatal(err)
	}

	second := first
	second.ID = "duplicate-root"
	second.CreationRequestHash = "different-hash"
	if err := store.CreateAuthoringPipeline(t.Context(), second, "Different Discovery"); err == nil {
		t.Fatal("same scoped request created a second root")
	}
	assertNoPipelineRecords(t, store, second.ID)

	got, err := store.GetAuthoringPipelineByRequest(t.Context(), first.WorkspaceID, "", first.CreationRequestID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID || got.CreationRequestID != first.CreationRequestID || got.CreationRequestHash != first.CreationRequestHash ||
		got.Artifacts[sdd.Discovery].Content != "Original Discovery" || got.Artifacts[sdd.Discovery].Version != 1 {
		t.Fatalf("lookup did not return original complete pipeline: %+v", got)
	}
	listed, err := store.ListPipelines(t.Context(), first.WorkspaceID)
	if err != nil || len(listed) != 1 || listed[0].CreationRequestID != first.CreationRequestID || listed[0].CreationRequestHash != first.CreationRequestHash {
		t.Fatalf("list request identity: %+v, %v", listed, err)
	}
	var events int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE stream_id = ?`, first.ID).Scan(&events); err != nil || events != 2 {
		t.Fatalf("original events = %d, %v", events, err)
	}
	for _, scope := range []struct{ workspace, parent, request string }{
		{first.WorkspaceID, "another-parent", first.CreationRequestID},
		{"another-workspace", "", first.CreationRequestID},
		{first.WorkspaceID, "", "missing"},
		{first.WorkspaceID, "", ""},
	} {
		if _, err := store.GetAuthoringPipelineByRequest(t.Context(), scope.workspace, scope.parent, scope.request); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("lookup for %+v = %v, want no rows", scope, err)
		}
	}

	otherWorkspace := catalog.Workspace{ID: "other-workspace", Path: "/other", CreatedAt: first.CreatedAt}
	if err := store.UpsertWorkspace(t.Context(), otherWorkspace); err != nil {
		t.Fatal(err)
	}
	independent := first
	independent.ID, independent.WorkspaceID = "other-root", otherWorkspace.ID
	if err := store.CreateAuthoringPipeline(t.Context(), independent, "Other workspace Discovery"); err != nil {
		t.Fatalf("request key was not workspace scoped: %v", err)
	}
}

func TestAuthoringRequestDerivedScopeAndUniqueness(t *testing.T) {
	store, _, firstParent := authoringFixture(t)
	firstParent.CreationRequestID = "shared-request"
	firstParent.CreationRequestHash = "root-hash"
	if err := store.CreateAuthoringPipeline(t.Context(), firstParent, "First parent Discovery"); err != nil {
		t.Fatal(err)
	}
	secondParent := firstParent
	secondParent.ID = "second-parent"
	secondParent.CreationRequestID = "second-parent-request"
	if err := store.CreateAuthoringPipeline(t.Context(), secondParent, "Second parent Discovery"); err != nil {
		t.Fatal(err)
	}
	for _, parent := range []catalog.PipelineRun{firstParent, secondParent} {
		if err := store.FreezeDiscovery(t.Context(), parent.ID, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	firstChild := firstParent
	firstChild.ID = "first-child"
	firstChild.DerivedFromPipelineID = firstParent.ID
	firstChild.CreationRequestHash = "first-child-hash"
	if err := store.DeriveAuthoringPipeline(t.Context(), firstParent.ID, 2, firstChild, "First child Discovery"); err != nil {
		t.Fatalf("derived key collided with root: %v", err)
	}
	duplicate := firstChild
	duplicate.ID = "duplicate-child"
	duplicate.CreationRequestHash = "different-child-hash"
	if err := store.DeriveAuthoringPipeline(t.Context(), firstParent.ID, 2, duplicate, "Different child Discovery"); err == nil {
		t.Fatal("same parent and request created a second child")
	}
	assertNoPipelineRecords(t, store, duplicate.ID)

	secondChild := firstChild
	secondChild.ID = "second-child"
	secondChild.DerivedFromPipelineID = secondParent.ID
	if err := store.DeriveAuthoringPipeline(t.Context(), secondParent.ID, 2, secondChild, "Second child Discovery"); err != nil {
		t.Fatalf("request key was not parent scoped: %v", err)
	}
	for _, expected := range []catalog.PipelineRun{firstChild, secondChild} {
		got, err := store.GetAuthoringPipelineByRequest(t.Context(), expected.WorkspaceID, expected.DerivedFromPipelineID, expected.CreationRequestID)
		if err != nil || got.ID != expected.ID || got.CreationRequestHash != expected.CreationRequestHash || got.Artifacts[sdd.Discovery].Content == "" {
			t.Fatalf("derived lookup for %s: %+v, %v", expected.ID, got, err)
		}
	}
	root, err := store.GetAuthoringPipelineByRequest(t.Context(), firstParent.WorkspaceID, "", firstParent.CreationRequestID)
	if err != nil || root.ID != firstParent.ID {
		t.Fatalf("root lookup crossed derived scope: %+v, %v", root, err)
	}
}

func TestAuthoringRequestHashSurvivesDiscoveryRevisionAndFreeze(t *testing.T) {
	store, _, run := authoringFixture(t)
	run.CreationRequestID = "stable-request"
	run.CreationRequestHash = "hash-of-original-request"
	if err := store.CreateAuthoringPipeline(t.Context(), run, "Original Discovery"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReviseAuthoringDiscovery(t.Context(), run.ID, 1, 1, "Revised Discovery", "New title", "New objective"); err != nil {
		t.Fatal(err)
	}
	if err := store.FreezeDiscovery(t.Context(), run.ID, 2, 2); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetAuthoringPipelineByRequest(t.Context(), run.WorkspaceID, "", run.CreationRequestID)
	if err != nil || got.CreationRequestID != run.CreationRequestID || got.CreationRequestHash != run.CreationRequestHash ||
		got.Artifacts[sdd.Discovery].Content != "Revised Discovery" || got.DiscoveryFrozenVersion != 2 || got.Revision != 3 {
		t.Fatalf("request identity changed with Discovery: %+v, %v", got, err)
	}
}

func TestAuthoringRequestRejectsPartialIdentity(t *testing.T) {
	for _, missing := range []string{"key", "hash"} {
		t.Run(missing, func(t *testing.T) {
			store, _, run := authoringFixture(t)
			if missing == "key" {
				run.CreationRequestHash = "hash-without-key"
			} else {
				run.CreationRequestID = "key-without-hash"
			}
			if err := store.CreateAuthoringPipeline(t.Context(), run, "Discovery"); !errors.Is(err, sdd.ErrInvalidTransition) {
				t.Fatalf("partial root identity = %v", err)
			}
			assertNoPipelineRecords(t, store, run.ID)

			parent := run
			parent.ID, parent.CreationRequestID, parent.CreationRequestHash = "parent", "", ""
			if err := store.CreateAuthoringPipeline(t.Context(), parent, "Parent Discovery"); err != nil {
				t.Fatal(err)
			}
			if err := store.FreezeDiscovery(t.Context(), parent.ID, 1, 1); err != nil {
				t.Fatal(err)
			}
			child := run
			child.ID, child.DerivedFromPipelineID = "partial-child", parent.ID
			if err := store.DeriveAuthoringPipeline(t.Context(), parent.ID, 2, child, "Child Discovery"); !errors.Is(err, sdd.ErrInvalidTransition) {
				t.Fatalf("partial derived identity = %v", err)
			}
			assertNoPipelineRecords(t, store, child.ID)
		})
	}
}

func TestAuthoringRequestMigrationDefaultsAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "before-idempotency.db")
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
		if version >= 24 {
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
	for _, kind := range []string{"legacy", "ai_authoring"} {
		if _, err := raw.Exec(`INSERT INTO pipeline_runs (id,workspace_id,title,objective,current_stage,stage_status,revision,created_at,updated_at,kind)
			VALUES (?,'workspace','old','old','discovery','{"discovery":"active"}',1,?,?,?)`, kind, timestamp, timestamp, kind); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"legacy", "ai_authoring"} {
			got, err := store.GetPipeline(t.Context(), kind)
			if err != nil || got.Kind != kind || got.CreationRequestID != "" || got.CreationRequestHash != "" {
				t.Fatalf("migrated %s: %+v, %v", kind, got, err)
			}
		}
		if _, err := store.GetAuthoringPipelineByRequest(t.Context(), "workspace", "", ""); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("empty request matched historical row: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func assertNoPipelineRecords(t *testing.T, store *Store, pipelineID string) {
	t.Helper()
	for _, table := range []struct{ name, column string }{
		{"pipeline_runs", "id"},
		{"pipeline_artifacts", "pipeline_id"},
		{"streams", "id"},
		{"events", "stream_id"},
	} {
		var count int
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM `+table.name+` WHERE `+table.column+` = ?`, pipelineID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s records for rejected pipeline = %d, %v", table.name, count, err)
		}
	}
}
