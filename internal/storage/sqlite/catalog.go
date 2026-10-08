package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func (s *Store) GetWorkspace(ctx context.Context, id string) (catalog.Workspace, error) {
	var value catalog.Workspace
	var created string
	err := s.db.QueryRowContext(ctx, "SELECT id, path, profile, created_at FROM workspaces WHERE id = ?", id).Scan(&value.ID, &value.Path, &value.Profile, &created)
	if err == nil {
		value.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	}
	if err != nil {
		return catalog.Workspace{}, fmt.Errorf("get workspace: %w", err)
	}
	return value, nil
}

func (s *Store) ListWorkspaces(ctx context.Context) ([]catalog.Workspace, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, path, profile, created_at, archived_at <> '' FROM workspaces ORDER BY last_opened_at DESC, id")
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()
	result := make([]catalog.Workspace, 0)
	for rows.Next() {
		var item catalog.Workspace
		var created string
		if err := rows.Scan(&item.ID, &item.Path, &item.Profile, &created, &item.Archived); err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse workspace created timestamp: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspaces: %w", err)
	}
	return result, nil
}

func (s *Store) SetWorkspaceProfile(ctx context.Context, id, profile string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE workspaces SET profile = ? WHERE id = ?", profile, id)
	if err != nil {
		return fmt.Errorf("set workspace profile: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read workspace profile update: %w", err)
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetWorkspaceArchived archives a project or brings it back. The project keeps its sessions, pipelines and settings either way.
func (s *Store) SetWorkspaceArchived(ctx context.Context, id string, archived bool) error {
	archivedAt := ""
	if archived {
		archivedAt = formatCatalogTime(time.Now().UTC())
	}
	result, err := s.db.ExecContext(ctx, "UPDATE workspaces SET archived_at = ? WHERE id = ?", archivedAt, id)
	if err != nil {
		return fmt.Errorf("set workspace archived: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read workspace archive update: %w", err)
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetSettings(ctx context.Context) (catalog.AppSettings, error) {
	var settings catalog.AppSettings
	if err := s.db.QueryRowContext(ctx, "SELECT default_backend_id, default_model_backend_id, default_model_id FROM app_settings WHERE id = 1").Scan(&settings.DefaultBackendID, &settings.DefaultModelBackendID, &settings.DefaultModelID); err != nil {
		return catalog.AppSettings{}, fmt.Errorf("get settings: %w", err)
	}
	return settings, nil
}

func (s *Store) SaveSettings(ctx context.Context, settings catalog.AppSettings) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE app_settings SET default_backend_id = ?, default_model_backend_id = ?, default_model_id = ? WHERE id = 1", settings.DefaultBackendID, settings.DefaultModelBackendID, settings.DefaultModelID); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

func (s *Store) GetProviderProfile(ctx context.Context, id string) (catalog.ProviderProfile, error) {
	var value catalog.ProviderProfile
	var created, updated string
	err := s.db.QueryRowContext(ctx, "SELECT id, name, kind, provider_type, base_url, model, credential_provider, credential_account, created_at, updated_at FROM provider_profiles WHERE id = ?", id).Scan(&value.ID, &value.Name, &value.Kind, &value.ProviderType, &value.BaseURL, &value.Model, &value.CredentialProvider, &value.CredentialAccount, &created, &updated)
	if err == nil {
		value.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	}
	if err == nil {
		value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	}
	if err != nil {
		return catalog.ProviderProfile{}, fmt.Errorf("get provider profile: %w", err)
	}
	return value, nil
}

func (s *Store) GetSession(ctx context.Context, id string) (catalog.SessionRecord, error) {
	var value catalog.SessionRecord
	var created, updated string
	err := s.db.QueryRowContext(ctx, "SELECT id, workspace_id, backend_id, status, created_at, updated_at, backend_revision, mode FROM sessions WHERE id = ?", id).Scan(&value.ID, &value.WorkspaceID, &value.BackendID, &value.Status, &created, &updated, &value.BackendRevision, &value.Mode)
	if err == nil {
		value.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	}
	if err == nil {
		value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	}
	if err != nil {
		return catalog.SessionRecord{}, fmt.Errorf("get session: %w", err)
	}
	return value, nil
}

func formatCatalogTime(value time.Time) string {
	// Fixed precision preserves chronological ordering when timestamps share a second.
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func (s *Store) UpsertWorkspace(ctx context.Context, workspace catalog.Workspace) error {
	// Opening a folder is what brings an archived project back, so the upsert always clears archived_at.
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspaces (id, path, profile, created_at, last_opened_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET path = excluded.path, profile = excluded.profile, last_opened_at = excluded.last_opened_at, archived_at = ''`,
		workspace.ID, workspace.Path, workspace.Profile, formatCatalogTime(workspace.CreatedAt), formatCatalogTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("upsert workspace: %w", err)
	}
	return nil
}

func (s *Store) UpsertProviderProfile(ctx context.Context, profile catalog.ProviderProfile) error {
	return upsertProviderProfile(ctx, s.db, profile)
}

type catalogExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func upsertProviderProfile(ctx context.Context, executor catalogExecutor, profile catalog.ProviderProfile) error {
	if profile.ProviderType == "" {
		profile.ProviderType = "generic" // Transitional callers before application validation.
	}
	_, err := executor.ExecContext(ctx, `INSERT INTO provider_profiles (id, name, kind, provider_type, base_url, model, credential_provider, credential_account, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, kind = excluded.kind, provider_type = excluded.provider_type, base_url = excluded.base_url, model = excluded.model, credential_provider = excluded.credential_provider, credential_account = excluded.credential_account, updated_at = excluded.updated_at`,
		profile.ID, profile.Name, profile.Kind, profile.ProviderType, profile.BaseURL, profile.Model, profile.CredentialProvider, profile.CredentialAccount, formatCatalogTime(profile.CreatedAt), formatCatalogTime(profile.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upsert provider profile: %w", err)
	}
	return nil
}

// PublishProviderProfile makes the active profile and its historical reference
// visible together. Failed publication cannot leave an untracked active key.
func (s *Store) PublishProviderProfile(ctx context.Context, profile catalog.ProviderProfile) error {
	if (profile.CredentialProvider == "") != (profile.CredentialAccount == "") {
		return fmt.Errorf("publish provider profile: partial credential reference")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider publication: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertProviderProfile(ctx, tx, profile); err != nil {
		return err
	}
	if profile.CredentialProvider != "" {
		ref := catalog.CredentialReference{ProfileID: profile.ID, Provider: profile.CredentialProvider, Account: profile.CredentialAccount, CreatedAt: profile.UpdatedAt}
		if err := addCredentialReference(ctx, tx, ref); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit provider publication: %w", err)
	}
	return nil
}

func (s *Store) AddCredentialReference(ctx context.Context, ref catalog.CredentialReference) error {
	return addCredentialReference(ctx, s.db, ref)
}

func addCredentialReference(ctx context.Context, executor catalogExecutor, ref catalog.CredentialReference) error {
	if ref.ProfileID == "" || ref.Provider == "" || ref.Account == "" {
		return fmt.Errorf("invalid credential reference")
	}
	_, err := executor.ExecContext(ctx, `INSERT INTO credential_references (profile_id, provider, account, created_at) VALUES (?, ?, ?, ?) ON CONFLICT(provider, account) DO NOTHING`, ref.ProfileID, ref.Provider, ref.Account, formatCatalogTime(ref.CreatedAt))
	if err != nil {
		return fmt.Errorf("add credential reference: %w", err)
	}
	return nil
}

func (s *Store) ListCredentialReferences(ctx context.Context) ([]catalog.CredentialReference, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT profile_id, provider, account, created_at FROM credential_references ORDER BY created_at, provider, account")
	if err != nil {
		return nil, fmt.Errorf("list credential references: %w", err)
	}
	defer rows.Close()
	refs := make([]catalog.CredentialReference, 0)
	for rows.Next() {
		var ref catalog.CredentialReference
		var created string
		if err := rows.Scan(&ref.ProfileID, &ref.Provider, &ref.Account, &created); err != nil {
			return nil, fmt.Errorf("scan credential reference: %w", err)
		}
		ref.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse credential reference timestamp: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate credential references: %w", err)
	}
	return refs, nil
}

func (s *Store) ListProviderProfiles(ctx context.Context) ([]catalog.ProviderProfile, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, kind, provider_type, base_url, model, credential_provider, credential_account, created_at, updated_at FROM provider_profiles ORDER BY created_at, id")
	if err != nil {
		return nil, fmt.Errorf("list provider profiles: %w", err)
	}
	defer rows.Close()
	result := make([]catalog.ProviderProfile, 0)
	for rows.Next() {
		var profile catalog.ProviderProfile
		var createdAt, updatedAt string
		if err := rows.Scan(&profile.ID, &profile.Name, &profile.Kind, &profile.ProviderType, &profile.BaseURL, &profile.Model, &profile.CredentialProvider, &profile.CredentialAccount, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan provider profile: %w", err)
		}
		profile.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse provider profile created timestamp: %w", err)
		}
		profile.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse provider profile updated timestamp: %w", err)
		}
		result = append(result, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider profiles: %w", err)
	}
	return result, nil
}

func (s *Store) UpsertSession(ctx context.Context, session catalog.SessionRecord) error {
	return upsertSession(ctx, s.db, session)
}

func upsertSession(ctx context.Context, executor catalogExecutor, session catalog.SessionRecord) error {
	_, err := executor.ExecContext(ctx, `INSERT INTO sessions (id, workspace_id, backend_id, status, created_at, updated_at, backend_revision, mode) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET workspace_id = excluded.workspace_id, backend_id = excluded.backend_id, status = excluded.status, updated_at = excluded.updated_at, backend_revision = excluded.backend_revision, mode = excluded.mode`,
		session.ID, session.WorkspaceID, session.BackendID, session.Status, formatCatalogTime(session.CreatedAt), formatCatalogTime(session.UpdatedAt), session.BackendRevision, session.Mode)
	if err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}
	return nil
}

func (s *Store) ListSessions(ctx context.Context, workspaceID string) ([]catalog.SessionRecord, error) {
	return s.listSessions(ctx, "SELECT id, workspace_id, backend_id, status, created_at, updated_at, backend_revision, mode FROM sessions WHERE workspace_id = ? ORDER BY created_at, id", workspaceID)
}

func (s *Store) ListAllSessions(ctx context.Context) ([]catalog.SessionRecord, error) {
	return s.listSessions(ctx, "SELECT id, workspace_id, backend_id, status, created_at, updated_at, backend_revision, mode FROM sessions ORDER BY created_at, id")
}

func (s *Store) listSessions(ctx context.Context, query string, args ...any) ([]catalog.SessionRecord, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	result := make([]catalog.SessionRecord, 0)
	for rows.Next() {
		var session catalog.SessionRecord
		var createdAt, updatedAt string
		if err := rows.Scan(&session.ID, &session.WorkspaceID, &session.BackendID, &session.Status, &createdAt, &updatedAt, &session.BackendRevision, &session.Mode); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		session.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse session created timestamp: %w", err)
		}
		session.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse session updated timestamp: %w", err)
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return result, nil
}
