package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func (s *Store) ListLogEvents(ctx context.Context, workspaceID, eventType string, beforeID int64, limit int) ([]catalog.LogEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.rowid, e.id, e.stream_id, ss.workspace_id, e.sequence, e.type, e.created_at
		FROM events e JOIN sessions ss ON ss.id = e.stream_id
		WHERE (? = '' OR ss.workspace_id = ?) AND (? = '' OR e.type = ?) AND (? = 0 OR e.rowid < ?)
		ORDER BY e.rowid DESC LIMIT ?`, workspaceID, workspaceID, eventType, eventType, beforeID, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list log events: %w", err)
	}
	defer rows.Close()
	result := make([]catalog.LogEntry, 0)
	for rows.Next() {
		var item catalog.LogEntry
		var created string
		if err := rows.Scan(&item.Cursor, &item.ID, &item.SessionID, &item.WorkspaceID, &item.Sequence, &item.Type, &created); err != nil {
			return nil, fmt.Errorf("scan log event: %w", err)
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse log timestamp: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate log events: %w", err)
	}
	return result, nil
}
