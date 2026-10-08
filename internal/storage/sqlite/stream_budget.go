package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/persioflexa/harflex/internal/events"
)

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func streamDataBytes(ctx context.Context, query rowQueryer, stream string) (int64, error) {
	var size int64
	if err := query.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(data)),0) FROM events WHERE stream_id=?`, stream).Scan(&size); err != nil {
		return 0, fmt.Errorf("read journal byte count: %w", err)
	}
	return size, nil
}

func (s *Store) StreamDataBytes(ctx context.Context, stream string) (int64, error) {
	return streamDataBytes(ctx, s.db, stream)
}

// The caller must hold SQLite's write reservation before checking this sum.
// A transactional counter can replace this O(n) foundation query later.
func ensureStreamBudget(ctx context.Context, tx *sql.Tx, stream string, incoming int) error {
	size, err := streamDataBytes(ctx, tx, stream)
	if err != nil {
		return err
	}
	if size < 0 || incoming > events.MaxStreamDataBytes || size > int64(events.MaxStreamDataBytes-incoming) {
		return events.ErrStreamBudgetExceeded
	}
	return nil
}
