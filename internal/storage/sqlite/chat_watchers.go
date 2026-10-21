package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

const chatWatcherColumns = "id, session_id, workspace_id, title, command, interval_seconds, deadline_at, status, reason, last_output, last_exit_code, checks, failures, last_check_at, next_check_at, delivered, created_at, ended_at"

func watcherTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

// SaveChatWatcher inserts a watcher or replaces its state.
func (s *Store) SaveChatWatcher(ctx context.Context, w catalog.ChatWatcher) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO chat_watchers ("+chatWatcherColumns+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) "+
		"ON CONFLICT(id) DO UPDATE SET status=excluded.status, reason=excluded.reason, last_output=excluded.last_output, last_exit_code=excluded.last_exit_code, "+
		"checks=excluded.checks, failures=excluded.failures, last_check_at=excluded.last_check_at, next_check_at=excluded.next_check_at, delivered=excluded.delivered, ended_at=excluded.ended_at",
		w.ID, w.SessionID, w.WorkspaceID, w.Title, w.Command, w.IntervalSeconds, watcherTime(w.DeadlineAt), w.Status, w.Reason, w.LastOutput, w.LastExitCode,
		w.Checks, w.Failures, watcherTime(w.LastCheckAt), watcherTime(w.NextCheckAt), w.Delivered, watcherTime(w.CreatedAt), watcherTime(w.EndedAt))
	if err != nil {
		return fmt.Errorf("save chat watcher: %w", err)
	}
	return nil
}

func (s *Store) GetChatWatcher(ctx context.Context, id string) (catalog.ChatWatcher, error) {
	items, err := s.listChatWatchers(ctx, "SELECT "+chatWatcherColumns+" FROM chat_watchers WHERE id = ?", id)
	if err != nil {
		return catalog.ChatWatcher{}, err
	}
	if len(items) == 0 {
		return catalog.ChatWatcher{}, sql.ErrNoRows
	}
	return items[0], nil
}

// ListChatWatchers lists a project's watchers that are running, or ended since a time, newest first.
func (s *Store) ListChatWatchers(ctx context.Context, workspaceID string, endedSince time.Time) ([]catalog.ChatWatcher, error) {
	return s.listChatWatchers(ctx, "SELECT "+chatWatcherColumns+" FROM chat_watchers WHERE workspace_id = ? AND (status = 'watching' OR delivered = 0 OR ended_at >= ?) ORDER BY created_at DESC LIMIT 100", workspaceID, watcherTime(endedSince))
}

// ListPendingChatWatchers lists the watchers that still need Harflex: those watching and those whose message is not sent.
func (s *Store) ListPendingChatWatchers(ctx context.Context) ([]catalog.ChatWatcher, error) {
	return s.listChatWatchers(ctx, "SELECT "+chatWatcherColumns+" FROM chat_watchers WHERE status = 'watching' OR delivered = 0 ORDER BY next_check_at")
}

func (s *Store) CountActiveChatWatchers(ctx context.Context, sessionID string) (int, error) {
	var count int
	if err := s.read().QueryRowContext(ctx, "SELECT count(*) FROM chat_watchers WHERE session_id = ? AND status = 'watching'", sessionID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count chat watchers: %w", err)
	}
	return count, nil
}

func (s *Store) listChatWatchers(ctx context.Context, query string, args ...any) ([]catalog.ChatWatcher, error) {
	rows, err := s.read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list chat watchers: %w", err)
	}
	defer rows.Close()
	result := make([]catalog.ChatWatcher, 0)
	for rows.Next() {
		var w catalog.ChatWatcher
		var deadline, lastCheck, nextCheck, created, ended string
		if err := rows.Scan(&w.ID, &w.SessionID, &w.WorkspaceID, &w.Title, &w.Command, &w.IntervalSeconds, &deadline, &w.Status, &w.Reason, &w.LastOutput, &w.LastExitCode,
			&w.Checks, &w.Failures, &lastCheck, &nextCheck, &w.Delivered, &created, &ended); err != nil {
			return nil, fmt.Errorf("scan chat watcher: %w", err)
		}
		for _, field := range []struct {
			text   string
			target *time.Time
		}{{deadline, &w.DeadlineAt}, {lastCheck, &w.LastCheckAt}, {nextCheck, &w.NextCheckAt}, {created, &w.CreatedAt}, {ended, &w.EndedAt}} {
			if field.text == "" {
				continue
			}
			parsed, err := time.Parse(time.RFC3339Nano, field.text)
			if err != nil {
				return nil, fmt.Errorf("parse chat watcher time: %w", err)
			}
			*field.target = parsed
		}
		result = append(result, w)
	}
	return result, rows.Err()
}
