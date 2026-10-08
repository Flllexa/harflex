package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestOpenRouterManagementCredentialRoundTripAndHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	profile := catalog.ProviderProfile{
		ID: "router", Name: "Router", Kind: "openai_compatible", ProviderType: "openrouter",
		BaseURL: "https://router.example/api/v1", Model: "m", CredentialProvider: "inference",
		CredentialAccount: "infer-ref", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.PublishProviderProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"management-a", "management-b"} {
		item := catalog.OpenRouterManagementCredential{
			ProfileID: "router", Origin: "https://router.example:443",
			CredentialProvider: "openrouter_management", CredentialAccount: account, UpdatedAt: now,
		}
		if err := db.PublishOpenRouterManagementCredential(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	active, err := db.GetOpenRouterManagementCredential(t.Context(), "router")
	if err != nil || active.CredentialAccount != "management-b" {
		t.Fatal(active, err)
	}
	refs, err := db.ListCredentialReferences(t.Context())
	if err != nil || len(refs) != 3 {
		t.Fatal(refs, err)
	}
	invalid := catalog.OpenRouterManagementCredential{
		ProfileID: "router", Origin: "https://router.example:443",
		CredentialProvider: "openrouter_management", UpdatedAt: now,
	}
	if err := db.PublishOpenRouterManagementCredential(t.Context(), invalid); err == nil {
		t.Fatal("partial reference was published")
	}
	if err := db.PublishOpenRouterManagementCredential(t.Context(), catalog.OpenRouterManagementCredential{
		ProfileID: "missing", Origin: "https://router.example:443",
		CredentialProvider: "openrouter_management", CredentialAccount: "orphan", UpdatedAt: now,
	}); err == nil {
		t.Fatal("orphan reference was published")
	}
	active, err = db.GetOpenRouterManagementCredential(t.Context(), "router")
	if err != nil || active.CredentialAccount != "management-b" {
		t.Fatal(active, err)
	}
	refs, err = db.ListCredentialReferences(t.Context())
	if err != nil || len(refs) != 3 {
		t.Fatal(refs, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	active, err = db.GetOpenRouterManagementCredential(t.Context(), "router")
	if err != nil || active.CredentialAccount != "management-b" {
		t.Fatal(active, err)
	}
	if err := db.ClearOpenRouterManagementCredential(t.Context(), "router"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetOpenRouterManagementCredential(t.Context(), "router"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cleared active reference: %v", err)
	}
	refs, err = db.ListCredentialReferences(t.Context())
	if err != nil || len(refs) != 3 {
		t.Fatal(refs, err)
	}
}
