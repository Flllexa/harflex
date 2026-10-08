package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

// GetProjectMemory reads what the Harflex knows about a project; sql.ErrNoRows means it was never read.
func (s *Store) GetProjectMemory(ctx context.Context, workspaceID string) (catalog.ProjectMemory, error) {
	var value catalog.ProjectMemory
	var sources, updated string
	var edited int
	err := s.db.QueryRowContext(ctx, "SELECT workspace_id, status, content, sources, backend_id, model_id, error_code, edited, updated_at FROM project_memories WHERE workspace_id=?", workspaceID).
		Scan(&value.WorkspaceID, &value.Status, &value.Content, &sources, &value.BackendID, &value.ModelID, &value.ErrorCode, &edited, &updated)
	if err != nil {
		return catalog.ProjectMemory{}, fmt.Errorf("get project memory: %w", err)
	}
	if err := json.Unmarshal([]byte(sources), &value.Sources); err != nil {
		return catalog.ProjectMemory{}, fmt.Errorf("parse project memory sources: %w", err)
	}
	value.Edited = edited != 0
	if value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return catalog.ProjectMemory{}, fmt.Errorf("parse project memory timestamp: %w", err)
	}
	return value, nil
}

// SaveProjectMemory replaces what the Harflex knows about a project.
func (s *Store) SaveProjectMemory(ctx context.Context, value catalog.ProjectMemory) error {
	if len(value.Content) > catalog.MaxProjectMemoryBytes {
		return fmt.Errorf("save project memory: content too large")
	}
	sources := value.Sources
	if sources == nil {
		sources = []string{}
	}
	encoded, err := json.Marshal(sources)
	if err != nil {
		return fmt.Errorf("encode project memory sources: %w", err)
	}
	edited := 0
	if value.Edited {
		edited = 1
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO project_memories (workspace_id, status, content, sources, backend_id, model_id, error_code, edited, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id) DO UPDATE SET status=excluded.status, content=excluded.content, sources=excluded.sources, backend_id=excluded.backend_id,
		model_id=excluded.model_id, error_code=excluded.error_code, edited=excluded.edited, updated_at=excluded.updated_at`,
		value.WorkspaceID, value.Status, value.Content, string(encoded), value.BackendID, value.ModelID, value.ErrorCode, edited, formatCatalogTime(value.UpdatedAt)); err != nil {
		return fmt.Errorf("save project memory: %w", err)
	}
	return nil
}
