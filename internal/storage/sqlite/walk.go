package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/persioflexa/harflex/internal/events"
	"time"
)

// WalkAfter keeps a read snapshot and scans metadata before loading each BLOB.
// The callback must not reenter this Store: it owns the single connection until
// iteration returns. No page or oversized/over-budget BLOB is materialized.
func (s *Store) WalkAfter(ctx context.Context, stream string, after int64, maxEvents, maxBytes int, visit func(events.Event) error) error {
	if after < 0 || maxEvents <= 0 || maxEvents > 1_000_000 || maxBytes <= 0 || visit == nil {
		return fmt.Errorf("invalid journal iteration bounds")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin journal iteration: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,stream_id,sequence,type,CASE WHEN typeof(data)='blob' THEN length(data) ELSE -1 END,created_at FROM events WHERE stream_id=? AND sequence>? ORDER BY sequence LIMIT ?`, stream, after, maxEvents+1)
	if err != nil {
		return fmt.Errorf("read journal metadata: %w", err)
	}
	defer rows.Close()
	count, total := 0, 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var event events.Event
		var size int64
		var created string
		if err := rows.Scan(&event.ID, &event.StreamID, &event.Sequence, &event.Type, &size, &created); err != nil {
			return fmt.Errorf("scan journal metadata: %w", err)
		}
		if count >= maxEvents || size < 0 || size > events.MaxDataBytes || size > int64(maxBytes-total) {
			return events.ErrEventBudgetExceeded
		}
		event.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return fmt.Errorf("parse journal timestamp: %w", err)
		}
		// Same transaction/connection guarantees metadata and payload are one snapshot.
		var payload []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM events WHERE id=? AND length(data)=?`, event.ID, size).Scan(&payload); err != nil {
			return fmt.Errorf("read journal payload: %w", err)
		}
		event.Data = payload
		count++
		total += len(event.Data)
		if err := visit(event); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate journal metadata: %w", err)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	err = tx.Commit()
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
