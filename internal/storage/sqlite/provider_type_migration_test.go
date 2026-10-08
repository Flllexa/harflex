package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/persioflexa/harflex/internal/storage/migrations"
)

func TestProviderTypeMigrationPreservesLegacyProfileAndSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_init.sql", "002_credential_references.sql", "003_session_backend_revision.sql"} {
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if name == "002_credential_references.sql" {
			if _, err := raw.Exec(`INSERT INTO provider_profiles VALUES ('legacy','Legacy','openai_compatible','http://legacy.example/v1','old-model','vault','old-ref','2026-09-25T10:00:00Z','2026-09-25T10:00:00Z')`); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL); INSERT INTO schema_migrations VALUES (1,'2026-09-25T10:00:00Z'),(2,'2026-09-25T10:00:00Z'),(3,'2026-09-25T10:00:00Z'); INSERT INTO workspaces VALUES ('w','/legacy','ask','2026-09-25T10:00:00Z'); INSERT INTO sessions (id,workspace_id,backend_id,status,created_at,updated_at,backend_revision) VALUES ('s','w','legacy','ready','2026-09-25T10:00:00Z','2026-09-25T10:00:00Z','api:original-revision')`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		profile, err := store.GetProviderProfile(t.Context(), "legacy")
		if err != nil || profile.ProviderType != "generic" || profile.BaseURL != "http://legacy.example/v1" || profile.Model != "old-model" || profile.CredentialProvider != "vault" || profile.CredentialAccount != "old-ref" || profile.CreatedAt.Format("2006-01-02T15:04:05Z") != "2026-09-25T10:00:00Z" || profile.UpdatedAt.Format("2006-01-02T15:04:05Z") != "2026-09-25T10:00:00Z" {
			t.Fatalf("profile changed: %+v %v", profile, err)
		}
		refs, err := store.ListCredentialReferences(t.Context())
		if err != nil || len(refs) != 1 || refs[0].Account != "old-ref" {
			t.Fatalf("references changed: %+v %v", refs, err)
		}
		session, err := store.GetSession(t.Context(), "s")
		if err != nil || session.BackendRevision != "api:original-revision" {
			t.Fatalf("session revision changed: %+v %v", session, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
