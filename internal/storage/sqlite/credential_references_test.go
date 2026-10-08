package sqlite

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/storage/migrations"
)

func TestCredentialReferenceMigrationBackfillsOldDatabaseIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := migrations.FS.ReadFile("001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(initial)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL); INSERT INTO schema_migrations VALUES (1, '2026-09-25T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	legacy := catalog.ProviderProfile{ID: "legacy", Name: "Legacy", Kind: "openai_compatible", ProviderType: "generic", CredentialProvider: "vault", CredentialAccount: "legacy-reference", CreatedAt: created, UpdatedAt: created}
	if _, err := db.Exec(`INSERT INTO provider_profiles (id,name,kind,base_url,model,credential_provider,credential_account,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		legacy.ID, legacy.Name, legacy.Kind, legacy.BaseURL, legacy.Model, legacy.CredentialProvider, legacy.CredentialAccount, formatCatalogTime(legacy.CreatedAt), formatCatalogTime(legacy.UpdatedAt)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	want := []catalog.CredentialReference{{ProfileID: legacy.ID, Provider: "vault", Account: "legacy-reference", CreatedAt: created}}
	for range 2 {
		store, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := store.ListCredentialReferences(t.Context())
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("backfill lost or duplicated historical reference", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProviderPublicationRollsBackOnInventoryFailure(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created := time.Now().UTC()
	profile := catalog.ProviderProfile{ID: "profile", Name: "Before", ProviderType: "generic", CredentialProvider: "vault", CredentialAccount: "reference-a", CreatedAt: created, UpdatedAt: created}
	if err := store.PublishProviderProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TRIGGER reject_reference BEFORE INSERT ON credential_references BEGIN SELECT RAISE(ABORT, 'synthetic inventory failure'); END`); err != nil {
		t.Fatal(err)
	}
	changed := profile
	changed.Name, changed.CredentialAccount = "After", "reference-b"
	if err := store.PublishProviderProfile(t.Context(), changed); err == nil {
		t.Fatal("inventory failure allowed publication")
	}
	got, err := store.GetProviderProfile(t.Context(), profile.ID)
	if err != nil || got != profile {
		t.Fatal("profile escaped failed transaction", err)
	}
	refs, err := store.ListCredentialReferences(t.Context())
	if err != nil || len(refs) != 1 || refs[0].Account != profile.CredentialAccount {
		t.Fatal("inventory changed on rollback", err)
	}
}

func TestCredentialReferenceInventoryIsDeterministicAndImmutable(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created := time.Now().UTC()
	first := catalog.CredentialReference{ProfileID: "one", Provider: "vault", Account: "a", CreatedAt: created}
	second := first
	second.Account = "b"
	for _, ref := range []catalog.CredentialReference{second, first, first} {
		if err := store.AddCredentialReference(t.Context(), ref); err != nil {
			t.Fatal(err)
		}
	}
	changed := first
	changed.ProfileID = "other"
	changed.CreatedAt = created.Add(time.Hour)
	if err := store.AddCredentialReference(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	refs, err := store.ListCredentialReferences(t.Context())
	if err != nil || !reflect.DeepEqual(refs, []catalog.CredentialReference{first, second}) {
		t.Fatal("reference inventory changed order or historical metadata", err)
	}
	var columns int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM pragma_table_info('credential_references')").Scan(&columns); err != nil || columns != 4 {
		t.Fatal("inventory must contain only references and publication timestamp", err)
	}
}
