package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/id"
)

func (s *Store) Append(ctx context.Context, streamID, kind, eventType string, data any) (events.Event, error) {
	event, _, err := s.appendEvent(ctx, streamID, kind, eventType, data, nil)
	return event, err
}

// AppendIfNoTerminalAfter rechecks the run boundary while holding SQLite's write
// lock, so separate services or database connections cannot interrupt it twice.
func (s *Store) AppendIfNoTerminalAfter(ctx context.Context, streamID, kind string, startedSequence int64, eventType string, data any) (events.Event, bool, error) {
	return s.appendEvent(ctx, streamID, kind, eventType, data, &startedSequence)
}

func (s *Store) appendEvent(ctx context.Context, streamID, kind, eventType string, data any, startedSequence *int64) (events.Event, bool, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return events.Event{}, false, fmt.Errorf("marshal journal event: %w", err)
	}
	if len(payload) > events.MaxDataBytes {
		return events.Event{}, false, events.ErrEventBudgetExceeded
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return events.Event{}, false, fmt.Errorf("begin journal append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	event := events.Event{ID: id.New(), StreamID: streamID, Type: eventType, Data: payload, CreatedAt: time.Now().UTC()}
	createdAt := event.CreatedAt.Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO streams (id, kind, created_at) VALUES (?, ?, ?)", streamID, kind, createdAt); err != nil {
		return events.Event{}, false, fmt.Errorf("ensure journal stream: %w", err)
	}
	if startedSequence != nil {
		var eligible bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE stream_id = ? AND sequence = ? AND type IN ('run.started','external.run.started'))
		 AND NOT EXISTS(SELECT 1 FROM events WHERE stream_id = ? AND sequence > ? AND type IN
		 ('run.started','external.run.started','run.completed','run.failed','run.cancelled','run.interrupted','external.run.completed','external.run.failed','external.run.cancelled','external.run.interrupted'))`, streamID, *startedSequence, streamID, *startedSequence).Scan(&eligible)
		if err != nil {
			return events.Event{}, false, fmt.Errorf("check journal run boundary: %w", err)
		}
		if !eligible {
			return events.Event{}, false, nil
		}
	}
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence), 0) + 1 FROM events WHERE stream_id = ?", streamID).Scan(&event.Sequence); err != nil {
		return events.Event{}, false, fmt.Errorf("allocate journal sequence: %w", err)
	}
	if err := ensureStreamBudget(ctx, tx, streamID, len(payload)); err != nil {
		return events.Event{}, false, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO events (id, stream_id, sequence, type, data, created_at) VALUES (?, ?, ?, ?, ?, ?)", event.ID, event.StreamID, event.Sequence, event.Type, []byte(event.Data), createdAt); err != nil {
		return events.Event{}, false, fmt.Errorf("insert journal event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return events.Event{}, false, fmt.Errorf("commit journal append: %w", err)
	}
	return event, true, nil
}

func (s *Store) ListAfter(ctx context.Context, streamID string, sequence int64) ([]events.Event, error) {
	return s.listEvents(ctx, "SELECT id, stream_id, sequence, type, data, created_at FROM events WHERE stream_id = ? AND sequence > ? ORDER BY sequence ASC", streamID, sequence)
}

func (s *Store) ListAfterLimit(ctx context.Context, streamID string, sequence int64, limit int) ([]events.Event, error) {
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("list journal events: limit must be between 1 and 1000")
	}
	return s.listEvents(ctx, "SELECT id, stream_id, sequence, type, data, created_at FROM events WHERE stream_id = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?", streamID, sequence, limit)
}

func (s *Store) listEvents(ctx context.Context, query string, args ...any) ([]events.Event, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list journal events: %w", err)
	}
	defer rows.Close()
	result := make([]events.Event, 0)
	for rows.Next() {
		var event events.Event
		var createdAt string
		if err := rows.Scan(&event.ID, &event.StreamID, &event.Sequence, &event.Type, &event.Data, &createdAt); err != nil {
			return nil, fmt.Errorf("scan journal event: %w", err)
		}
		event.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse journal event timestamp: %w", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate journal events: %w", err)
	}
	return result, nil
}
