package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestSDDDefaultModelMigrationAppliesAfterVersion34(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-settings.db")
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		CREATE TABLE app_settings (id INTEGER PRIMARY KEY, default_backend_id TEXT NOT NULL DEFAULT '');
		CREATE TABLE pipeline_sessions (id TEXT PRIMARY KEY, pipeline_id TEXT NOT NULL, session_id TEXT NOT NULL, role TEXT NOT NULL, baseline_git_hash TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
		INSERT INTO app_settings (id, default_backend_id) VALUES (1, 'codex')`); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= lastMigrationVersion(t); version++ {
		if version > 34 && version < 44 {
			continue // 35-43 are what this test applies; 44 onward (AI-authored Code, MCP, PRs) need tables a synthetic catalog lacks
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations VALUES (?, '2026-09-29T12:00:00Z')`, version); err != nil {
			t.Fatal(err)
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

	before, err := store.GetSettings(t.Context())
	if err != nil || before.DefaultBackendID != "codex" || before.DefaultModelBackendID != "" || before.DefaultModelID != "" {
		t.Fatalf("legacy settings after migration: %+v, %v", before, err)
	}
	want := catalog.AppSettings{DefaultBackendID: "codex", DefaultModelBackendID: "codex", DefaultModelID: "gpt-5-codex"}
	if err := store.SaveSettings(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetSettings(t.Context())
	if err != nil || after != want {
		t.Fatalf("settings after round-trip: got %+v, want %+v, err=%v", after, want, err)
	}
	var applied bool
	if err := store.DB().QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = 35)`).Scan(&applied); err != nil || !applied {
		t.Fatalf("migration 035 applied = %v, err=%v", applied, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = 36)`).Scan(&applied); err != nil || !applied {
		t.Fatalf("migration 036 applied = %v, err=%v", applied, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = 37)`).Scan(&applied); err != nil || !applied {
		t.Fatalf("migration 037 applied = %v, err=%v", applied, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = 38)`).Scan(&applied); err != nil || !applied {
		t.Fatalf("migration 038 applied = %v, err=%v", applied, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = 39)`).Scan(&applied); err != nil || !applied {
		t.Fatalf("migration 039 applied = %v, err=%v", applied, err)
	}
}
