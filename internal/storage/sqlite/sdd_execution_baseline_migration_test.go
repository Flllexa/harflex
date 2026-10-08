package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestExecutionBaselineMigrationRepairsPreviouslyRecordedVersions(t *testing.T) {
	for _, test := range []struct {
		name, columns string
	}{
		{name: "both missing"},
		{name: "only file names present", columns: ", baseline_git_files TEXT NOT NULL DEFAULT '[]'"},
		{name: "only hashes present", columns: ", baseline_git_file_hashes TEXT NOT NULL DEFAULT '{}'"},
		{name: "both present", columns: ", baseline_git_files TEXT NOT NULL DEFAULT '[]', baseline_git_file_hashes TEXT NOT NULL DEFAULT '{}'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy-execution.db")
			raw, err := sql.Open("sqlite", sqliteDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
				CREATE TABLE pipeline_sessions (id TEXT PRIMARY KEY, pipeline_id TEXT NOT NULL, session_id TEXT NOT NULL, role TEXT NOT NULL,
				baseline_git_hash TEXT NOT NULL DEFAULT '', execution_snapshot TEXT NOT NULL DEFAULT '', execution_applied_at TEXT NOT NULL DEFAULT '',
				evaluation_criteria TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL` + test.columns + `);
				INSERT INTO pipeline_sessions (id, pipeline_id, session_id, role, baseline_git_hash, created_at)
				VALUES ('link-legacy', 'pipeline-legacy', 'session-legacy', 'coder', 'original-head', '2026-09-30T12:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			for version := 1; version <= lastMigrationVersion(t); version++ {
				if version == 43 {
					continue // the repair under test; 44 onward are recorded because this synthetic catalog lacks their tables
				}
				if _, err := raw.Exec(`INSERT INTO schema_migrations VALUES (?, '2026-09-30T12:00:00Z')`, version); err != nil {
					t.Fatal(err)
				}
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			for reopen := 0; reopen < 2; reopen++ {
				store, err := Open(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				links, err := store.ListPipelineSessions(t.Context(), "pipeline-legacy", "coder")
				if err != nil || len(links) != 1 || links[0].ID != "link-legacy" || links[0].BaselineGitHash != "original-head" || links[0].BaselineGitFiles == nil || links[0].BaselineGitFileHashes == nil {
					t.Fatalf("legacy session not preserved after upgrade: %+v, %v", links, err)
				}
				var applied bool
				if err := store.DB().QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=43)`).Scan(&applied); err != nil || !applied {
					t.Fatalf("repair not recorded: %v, %v", applied, err)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
