package sqlite

import (
	"context"
	"fmt"
	"time"
)

// ListSessionPins returns the pinned conversations with the moment each was pinned.
func (s *Store) ListSessionPins(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT session_id, pinned_at FROM session_pins")
	if err != nil {
		return nil, fmt.Errorf("list session pins: %w", err)
	}
	defer rows.Close()
	pins := map[string]time.Time{}
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			return nil, fmt.Errorf("scan session pin: %w", err)
		}
		parsed, _ := time.Parse(time.RFC3339Nano, at)
		pins[id] = parsed
	}
	return pins, rows.Err()
}

// SetSessionPinned pins a conversation or takes the pin off. Pinning twice keeps the first moment.
func (s *Store) SetSessionPinned(ctx context.Context, sessionID string, pinned bool) error {
	var err error
	if pinned {
		_, err = s.db.ExecContext(ctx, "INSERT INTO session_pins (session_id, pinned_at) VALUES (?, ?) ON CONFLICT(session_id) DO NOTHING", sessionID, formatCatalogTime(time.Now().UTC()))
	} else {
		_, err = s.db.ExecContext(ctx, "DELETE FROM session_pins WHERE session_id=?", sessionID)
	}
	if err != nil {
		return fmt.Errorf("set session pin: %w", err)
	}
	return nil
}
