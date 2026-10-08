package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
)

var ErrWorkflowConflict = errors.New("workflow run changed concurrently")

func (s *Store) SaveWorkflow(ctx context.Context, item catalog.Workflow) error {
	steps, err := json.Marshal(item.Steps)
	if err != nil {
		return fmt.Errorf("encode workflow steps: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workflows(id,workspace_id,name,steps,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
	ON CONFLICT(id) DO UPDATE SET name=excluded.name,steps=excluded.steps,revision=workflows.revision+1,updated_at=excluded.updated_at`,
		item.ID, item.WorkspaceID, item.Name, steps, item.Revision, formatCatalogTime(item.CreatedAt), formatCatalogTime(item.UpdatedAt))
	if err != nil {
		return fmt.Errorf("save workflow: %w", err)
	}
	return nil
}

const workflowColumns = "id,workspace_id,name,steps,revision,created_at,updated_at"

func scanWorkflow(row rowScanner) (catalog.Workflow, error) {
	var item catalog.Workflow
	var steps []byte
	var created, updated string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.Name, &steps, &item.Revision, &created, &updated); err != nil {
		return catalog.Workflow{}, err
	}
	if err := json.Unmarshal(steps, &item.Steps); err != nil {
		return catalog.Workflow{}, fmt.Errorf("decode workflow steps: %w", err)
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return catalog.Workflow{}, fmt.Errorf("parse workflow created timestamp: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return catalog.Workflow{}, fmt.Errorf("parse workflow updated timestamp: %w", err)
	}
	return item, nil
}

func (s *Store) GetWorkflow(ctx context.Context, id string) (catalog.Workflow, error) {
	item, err := scanWorkflow(s.db.QueryRowContext(ctx, "SELECT "+workflowColumns+" FROM workflows WHERE id=?", id))
	if err != nil {
		return catalog.Workflow{}, fmt.Errorf("get workflow: %w", err)
	}
	return item, nil
}

func (s *Store) ListWorkflows(ctx context.Context, workspaceID string) ([]catalog.Workflow, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+workflowColumns+" FROM workflows WHERE workspace_id=? ORDER BY updated_at DESC,id", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.Workflow, 0)
	for rows.Next() {
		item, err := scanWorkflow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workflow: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflows: %w", err)
	}
	return items, nil
}

func appendRunEvent(ctx context.Context, tx *sql.Tx, runID, eventType string, data any, now time.Time) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode workflow event: %w", err)
	}
	var sequence int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0)+1 FROM events WHERE stream_id=?", runID).Scan(&sequence); err != nil {
		return fmt.Errorf("read workflow event sequence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO events(id,stream_id,sequence,type,data,created_at) VALUES(?,?,?,?,?,?)", id.New(), runID, sequence, eventType, encoded, formatCatalogTime(now)); err != nil {
		return fmt.Errorf("append workflow event: %w", err)
	}
	return nil
}

func (s *Store) CreateWorkflowRun(ctx context.Context, item catalog.WorkflowRun) error {
	steps, err := json.Marshal(item.Steps)
	if err != nil {
		return fmt.Errorf("encode workflow run steps: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin workflow run: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO workflow_runs(id,workflow_id,workspace_id,backend_id,steps,current_step,status,last_session_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, item.ID, item.WorkflowID, item.WorkspaceID, item.BackendID, steps, item.CurrentStep, item.Status, item.LastSessionID, formatCatalogTime(item.CreatedAt), formatCatalogTime(item.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert workflow run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO streams(id,kind,created_at) VALUES(?,?,?)", item.ID, "workflow_run", formatCatalogTime(item.CreatedAt)); err != nil {
		return fmt.Errorf("create workflow stream: %w", err)
	}
	if err := appendRunEvent(ctx, tx, item.ID, "workflow.started", map[string]any{"workflowId": item.WorkflowID, "stepCount": len(item.Steps)}, item.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workflow run: %w", err)
	}
	return nil
}

const workflowRunColumns = "id,workflow_id,workspace_id,backend_id,steps,current_step,status,last_session_id,created_at,updated_at"

func scanWorkflowRun(row rowScanner) (catalog.WorkflowRun, error) {
	var item catalog.WorkflowRun
	var steps []byte
	var created, updated string
	if err := row.Scan(&item.ID, &item.WorkflowID, &item.WorkspaceID, &item.BackendID, &steps, &item.CurrentStep, &item.Status, &item.LastSessionID, &created, &updated); err != nil {
		return catalog.WorkflowRun{}, err
	}
	if err := json.Unmarshal(steps, &item.Steps); err != nil {
		return catalog.WorkflowRun{}, fmt.Errorf("decode workflow run steps: %w", err)
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return catalog.WorkflowRun{}, fmt.Errorf("parse workflow run created timestamp: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return catalog.WorkflowRun{}, fmt.Errorf("parse workflow run updated timestamp: %w", err)
	}
	return item, nil
}

func (s *Store) GetWorkflowRun(ctx context.Context, id string) (catalog.WorkflowRun, error) {
	item, err := scanWorkflowRun(s.db.QueryRowContext(ctx, "SELECT "+workflowRunColumns+" FROM workflow_runs WHERE id=?", id))
	if err != nil {
		return catalog.WorkflowRun{}, fmt.Errorf("get workflow run: %w", err)
	}
	return item, nil
}

func (s *Store) ListWorkflowRuns(ctx context.Context, workspaceID string) ([]catalog.WorkflowRun, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+workflowRunColumns+" FROM workflow_runs WHERE workspace_id=? ORDER BY updated_at DESC,id", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list workflow runs: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.WorkflowRun, 0)
	for rows.Next() {
		item, err := scanWorkflowRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workflow run: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow runs: %w", err)
	}
	return items, nil
}

func (s *Store) UpdateWorkflowRun(ctx context.Context, item catalog.WorkflowRun, expectedStatus, eventType string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin workflow transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, "UPDATE workflow_runs SET current_step=?,status=?,last_session_id=?,updated_at=? WHERE id=? AND status=?", item.CurrentStep, item.Status, item.LastSessionID, formatCatalogTime(item.UpdatedAt), item.ID, expectedStatus)
	if err != nil {
		return fmt.Errorf("update workflow run: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read workflow transition: %w", err)
	}
	if changed != 1 {
		return ErrWorkflowConflict
	}
	if err := appendRunEvent(ctx, tx, item.ID, eventType, map[string]any{"step": item.CurrentStep, "status": item.Status, "sessionId": item.LastSessionID}, item.UpdatedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workflow transition: %w", err)
	}
	return nil
}
