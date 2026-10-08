package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

// PublishOpenRouterManagementCredential atomically rotates the active reference
// and records it in credential history. An orphaned active reference is never
// committed if historical publication fails.
func (s *Store) PublishOpenRouterManagementCredential(ctx context.Context, item catalog.OpenRouterManagementCredential) error {
	if item.ProfileID == "" || item.Origin == "" || item.CredentialProvider == "" || item.CredentialAccount == "" {
		return fmt.Errorf("publish catalog credential: incomplete reference")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin catalog credential publication: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO openrouter_catalog_credentials
		(profile_id, origin, credential_provider, credential_account, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(profile_id) DO UPDATE SET origin = excluded.origin,
		credential_provider = excluded.credential_provider,
		credential_account = excluded.credential_account, updated_at = excluded.updated_at`,
		item.ProfileID, item.Origin, item.CredentialProvider, item.CredentialAccount, formatCatalogTime(item.UpdatedAt))
	if err != nil {
		return fmt.Errorf("publish catalog credential: %w", err)
	}
	ref := catalog.CredentialReference{
		ProfileID: item.ProfileID, Provider: item.CredentialProvider,
		Account: item.CredentialAccount, CreatedAt: item.UpdatedAt,
	}
	if err := addCredentialReference(ctx, tx, ref); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit catalog credential publication: %w", err)
	}
	return nil
}

func (s *Store) GetOpenRouterManagementCredential(ctx context.Context, profileID string) (catalog.OpenRouterManagementCredential, error) {
	var item catalog.OpenRouterManagementCredential
	var timestamp string
	err := s.db.QueryRowContext(ctx, `SELECT profile_id, origin, credential_provider, credential_account, updated_at
		FROM openrouter_catalog_credentials WHERE profile_id = ?`, profileID).
		Scan(&item.ProfileID, &item.Origin, &item.CredentialProvider, &item.CredentialAccount, &timestamp)
	if err != nil {
		return catalog.OpenRouterManagementCredential{}, fmt.Errorf("get catalog credential: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return catalog.OpenRouterManagementCredential{}, fmt.Errorf("parse catalog credential timestamp: %w", err)
	}
	return item, nil
}

func (s *Store) ClearOpenRouterManagementCredential(ctx context.Context, profileID string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM openrouter_catalog_credentials WHERE profile_id = ?", profileID)
	if err != nil {
		return fmt.Errorf("clear catalog credential: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read catalog credential deletion: %w", err)
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
