package sqlite

import (
	"database/sql"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/storage/migrations"
)

// migrateRawThrough builds a catalog with the real migrations up to last, as an older Harflex left it.
func migrateRawThrough(t *testing.T, path string, last int) *sql.DB {
	t.Helper()
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
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil || version > last {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations VALUES (?, '2026-10-01T12:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}
	return raw
}

func TestMigration059AddsBrainstorm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "before-brainstorm.db")
	raw := migrateRawThrough(t, path, 58)
	const at = "2026-10-01T12:00:00Z"
	status := func(current string) string {
		values := map[string]string{"discovery": "completed", "spec": "completed", "plan": "completed", "code": "completed", "eval": "completed", "prs": "completed"}
		order := []string{"discovery", "spec", "plan", "code", "eval", "prs"}
		reached := current == ""
		for _, stage := range order {
			if stage == current {
				values[stage], reached = "active", true
			} else if reached && current != "" {
				values[stage] = "pending"
			}
		}
		encoded, _ := json.Marshal(values)
		return string(encoded)
	}
	digest := strings.Repeat("a", 64)
	for _, statement := range []string{
		`INSERT INTO workspaces (id, path, profile, created_at) VALUES ('w1', '/tmp/w1', 'ask', '` + at + `')`,
		`INSERT INTO sessions (id, workspace_id, backend_id, status, created_at, updated_at) VALUES ('s1', 'w1', 'local', 'completed', '` + at + `', '` + at + `')`,
		`INSERT INTO pipeline_runs (id, workspace_id, title, objective, current_stage, stage_status, revision, created_at, updated_at, kind) VALUES ('in-code', 'w1', 'A', 'a', 'code', '` + status("code") + `', 5, '` + at + `', '` + at + `', 'ai_authoring')`,
		`INSERT INTO pipeline_runs (id, workspace_id, title, objective, current_stage, stage_status, revision, created_at, updated_at, kind) VALUES ('in-spec', 'w1', 'B', 'b', 'spec', '` + status("spec") + `', 3, '` + at + `', '` + at + `', 'ai_authoring')`,
		`INSERT INTO pipeline_runs (id, workspace_id, title, objective, current_stage, stage_status, revision, created_at, updated_at, kind) VALUES ('finished', 'w1', 'C', 'c', '', '` + status("") + `', 9, '` + at + `', '` + at + `', 'legacy')`,
		`INSERT INTO pipeline_design_workspaces (pipeline_id, pipeline_revision, revision, state, phase, active_attempt_id, created_at, updated_at) VALUES ('in-spec', 3, 2, 'ready', '', '', '` + at + `', '` + at + `')`,
		`INSERT INTO pipeline_design_versions (pipeline_id, stage, version, content, content_digest, author, source_session_id, source_digest, selection_snapshot, attempt_id, reason, restored_from_version, created_at) VALUES ('in-spec', 'spec', 1, '# SPEC', '` + digest + `', 'ai', 's1', '', x'7b7d', 'att1', 'generated', 0, '` + at + `')`,
		`INSERT INTO pipeline_design_documents (pipeline_id, stage, current_version, stale, updated_at) VALUES ('in-spec', 'spec', 1, 0, '` + at + `')`,
		`INSERT INTO pipeline_design_attempts (id, pipeline_id, request_id, intent_hash, owner_pid, target, status, phase, source_revision, source_pipeline_revision, input_snapshot, selection_snapshot, error_code, result_hash, created_at, updated_at) VALUES ('att1', 'in-spec', 'req1', '` + digest + `', 0, 'all', 'completed', 'spec', 1, 3, x'7b7d', x'7b7d', '', '', '` + at + `', '` + at + `')`,
		`INSERT INTO pipeline_design_attempt_sessions (attempt_id, stage, session_id, created_at) VALUES ('att1', 'spec', 's1', '` + at + `')`,
		`INSERT INTO pipeline_design_messages (id, pipeline_id, role, content, target, attempt_id, created_at) VALUES ('m1', 'in-spec', 'user', 'Prepare a SPEC', 'all', 'att1', '` + at + `')`,
		`INSERT INTO pipeline_design_commands (pipeline_id, request_id, action, intent_hash, attempt_id, result_snapshot, created_at) VALUES ('in-spec', 'req1', 'prepare', '` + digest + `', 'att1', x'7b7d', '` + at + `')`,
		`INSERT INTO workspace_stage_executors (workspace_id, stage, backend_id, model_id, updated_at) VALUES ('w1', 'spec', 'claude', 'opus', '` + at + `')`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	brainstorm := func(pipelineID string) string {
		var encoded []byte
		if err := db.QueryRowContext(t.Context(), `SELECT stage_status FROM pipeline_runs WHERE id=?`, pipelineID).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		var values map[string]string
		if err := json.Unmarshal(encoded, &values); err != nil {
			t.Fatalf("stage_status of %s no longer JSON: %q", pipelineID, encoded)
		}
		return values["brainstorm"]
	}
	for pipelineID, want := range map[string]string{"in-code": "skipped", "finished": "skipped", "in-spec": "pending"} {
		if got := brainstorm(pipelineID); got != want {
			t.Fatalf("%s brainstorm = %q, want %q", pipelineID, got, want)
		}
	}
	var reason string
	if err := db.QueryRowContext(t.Context(), `SELECT reason FROM pipeline_transitions WHERE pipeline_id='in-code' AND stage='brainstorm' AND action='skip'`).Scan(&reason); err != nil || reason != "a etapa não existia quando este trabalho foi preparado" {
		t.Fatalf("skip transition = %q, %v", reason, err)
	}
	var transitions int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pipeline_transitions WHERE pipeline_id='in-spec'`).Scan(&transitions); err != nil || transitions != 0 {
		t.Fatalf("a pipeline still before SPEC must get no skip: %d, %v", transitions, err)
	}
	for _, check := range []struct{ query, want string }{
		{`SELECT content FROM pipeline_design_versions WHERE pipeline_id='in-spec' AND stage='spec'`, "# SPEC"},
		{`SELECT CAST(current_version AS TEXT) FROM pipeline_design_documents WHERE pipeline_id='in-spec' AND stage='spec'`, "1"},
		{`SELECT status FROM pipeline_design_attempts WHERE id='att1'`, "completed"},
		{`SELECT session_id FROM pipeline_design_attempt_sessions WHERE attempt_id='att1'`, "s1"},
		{`SELECT content FROM pipeline_design_messages WHERE id='m1'`, "Prepare a SPEC"},
		{`SELECT action FROM pipeline_design_commands WHERE request_id='req1'`, "prepare"},
		{`SELECT model_id FROM workspace_stage_executors WHERE workspace_id='w1' AND stage='spec'`, "opus"},
		{`SELECT name FROM sqlite_master WHERE type='index' AND name='pipeline_design_single_owner'`, "pipeline_design_single_owner"},
	} {
		var got string
		if err := db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatalf("%s = %q, %v; want %q", check.query, got, err, check.want)
		}
	}
	for _, statement := range []string{
		`INSERT INTO pipeline_design_versions (pipeline_id, stage, version, content, content_digest, author, selection_snapshot, reason, created_at) VALUES ('in-spec', 'brainstorm', 1, '{}', '` + digest + `', 'ai', x'7b7d', 'generated', '` + at + `')`,
		`INSERT INTO pipeline_design_attempts (id, pipeline_id, request_id, intent_hash, target, status, phase, source_revision, source_pipeline_revision, input_snapshot, selection_snapshot, created_at, updated_at) VALUES ('att2', 'in-spec', 'req2', '` + digest + `', 'brainstorm', 'running', 'brainstorm', 2, 3, x'7b7d', x'7b7d', '` + at + `', '` + at + `')`,
		`INSERT INTO pipeline_design_messages (id, pipeline_id, role, content, target, created_at) VALUES ('m2', 'in-spec', 'user', 'Ajuste o fluxo', 'brainstorm', '` + at + `')`,
		`UPDATE pipeline_design_workspaces SET phase='brainstorm' WHERE pipeline_id='in-spec'`,
		`INSERT INTO workspace_stage_executors (workspace_id, stage, backend_id, updated_at) VALUES ('w1', 'brainstorm', 'claude', '` + at + `')`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("brainstorm refused: %s: %v", statement, err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE pipeline_design_versions SET content='changed' WHERE pipeline_id='in-spec' AND stage='spec'`); err == nil || !strings.Contains(err.Error(), "immutable design version") {
		t.Fatalf("the immutability trigger must survive the rebuild: %v", err)
	}
	rows, err := db.QueryContext(t.Context(), `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("the rebuild left dangling foreign keys")
	}
}
