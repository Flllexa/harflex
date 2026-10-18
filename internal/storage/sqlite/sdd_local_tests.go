package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

const localTestColumns = "id, pipeline_id, workspace_id, artifact_version, session_id, backup_path, applied, services, status, created_at, undone_at"

// CreatePipelineLocalTest records a local test as applied. A project holds one applied test at a time.
func (s *Store) CreatePipelineLocalTest(ctx context.Context, test catalog.PipelineLocalTest) error {
	services, err := json.Marshal(nonNilServices(test.Services))
	if err != nil {
		return fmt.Errorf("encode local test services: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin local test: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var holder string
	switch err := tx.QueryRowContext(ctx, "SELECT pipeline_id FROM pipeline_local_tests WHERE workspace_id=? AND status='applied'", test.WorkspaceID).Scan(&holder); {
	case err == nil:
		return &catalog.LocalTestBusyError{PipelineID: holder}
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read the project's local test: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO pipeline_local_tests ("+localTestColumns+") VALUES (?,?,?,?,?,?,?,?,'applied',?,'')",
		test.ID, test.PipelineID, test.WorkspaceID, test.ArtifactVersion, test.SessionID, test.BackupPath, string(test.Applied), string(services), formatCatalogTime(test.CreatedAt)); err != nil {
		return fmt.Errorf("record local test: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, test.PipelineID, "pipeline.local_test.applied", map[string]any{"version": test.ArtifactVersion}, test.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func nonNilServices(services []catalog.LocalTestService) []catalog.LocalTestService {
	if services == nil {
		return []catalog.LocalTestService{}
	}
	return services
}

func scanLocalTest(row interface{ Scan(...any) error }) (catalog.PipelineLocalTest, error) {
	var test catalog.PipelineLocalTest
	var applied, services, created, undone string
	if err := row.Scan(&test.ID, &test.PipelineID, &test.WorkspaceID, &test.ArtifactVersion, &test.SessionID, &test.BackupPath, &applied, &services, &test.Status, &created, &undone); err != nil {
		return catalog.PipelineLocalTest{}, err
	}
	test.Applied = json.RawMessage(applied)
	if err := json.Unmarshal([]byte(services), &test.Services); err != nil {
		return catalog.PipelineLocalTest{}, fmt.Errorf("decode local test services: %w", err)
	}
	test.Services = nonNilServices(test.Services)
	var err error
	if test.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return catalog.PipelineLocalTest{}, fmt.Errorf("parse local test time: %w", err)
	}
	if undone != "" {
		if test.UndoneAt, err = time.Parse(time.RFC3339Nano, undone); err != nil {
			return catalog.PipelineLocalTest{}, fmt.Errorf("parse local test time: %w", err)
		}
	}
	return test, nil
}

// ActivePipelineLocalTest is the project's applied local test, or sql.ErrNoRows.
func (s *Store) ActivePipelineLocalTest(ctx context.Context, workspaceID string) (catalog.PipelineLocalTest, error) {
	return scanLocalTest(s.db.QueryRowContext(ctx, "SELECT "+localTestColumns+" FROM pipeline_local_tests WHERE workspace_id=? AND status='applied'", workspaceID))
}

// LatestPipelineLocalTest is the pipeline's latest local test, applied or undone, or sql.ErrNoRows.
func (s *Store) LatestPipelineLocalTest(ctx context.Context, pipelineID string) (catalog.PipelineLocalTest, error) {
	return scanLocalTest(s.db.QueryRowContext(ctx, "SELECT "+localTestColumns+" FROM pipeline_local_tests WHERE pipeline_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1", pipelineID))
}

// SetPipelineLocalTestSession binds the test to the conversation that prepares the project.
func (s *Store) SetPipelineLocalTestSession(ctx context.Context, id, sessionID string) error {
	return s.updateLocalTest(ctx, "UPDATE pipeline_local_tests SET session_id=? WHERE id=? AND status='applied'", sessionID, id)
}

// AddPipelineLocalTestService records a service the tester started; one with the same name is replaced.
func (s *Store) AddPipelineLocalTestService(ctx context.Context, id string, service catalog.LocalTestService) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin local test service: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT services FROM pipeline_local_tests WHERE id=? AND status='applied'", id).Scan(&raw); err != nil {
		return fmt.Errorf("read local test services: %w", err)
	}
	var services []catalog.LocalTestService
	if err := json.Unmarshal([]byte(raw), &services); err != nil {
		return fmt.Errorf("decode local test services: %w", err)
	}
	kept := services[:0]
	for _, item := range services {
		if item.Name != service.Name {
			kept = append(kept, item)
		}
	}
	kept = append(kept, service)
	sort.Slice(kept, func(i, j int) bool { return kept[i].Name < kept[j].Name })
	encoded, err := json.Marshal(kept)
	if err != nil {
		return fmt.Errorf("encode local test services: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE pipeline_local_tests SET services=? WHERE id=?", string(encoded), id); err != nil {
		return fmt.Errorf("record local test service: %w", err)
	}
	return tx.Commit()
}

// FinishPipelineLocalTest marks a test undone, freeing the project for another.
func (s *Store) FinishPipelineLocalTest(ctx context.Context, id string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin local test finish: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var pipelineID string
	if err := tx.QueryRowContext(ctx, "SELECT pipeline_id FROM pipeline_local_tests WHERE id=? AND status='applied'", id).Scan(&pipelineID); err != nil {
		return fmt.Errorf("read local test: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE pipeline_local_tests SET status='undone', undone_at=? WHERE id=?", formatCatalogTime(at), id); err != nil {
		return fmt.Errorf("finish local test: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.local_test.undone", map[string]any{"id": id}, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) updateLocalTest(ctx context.Context, query string, args ...any) error {
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update local test: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return fmt.Errorf("update local test: %w", sql.ErrNoRows)
	}
	return nil
}
