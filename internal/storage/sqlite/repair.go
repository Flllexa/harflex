package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/id"
	"time"
)

// RepairInterruptedRun uses the observed head as a compare-and-swap boundary.
// All closures and the optional terminal commit together; stale concurrent
// recovery snapshots append nothing, including when repairing legacy terminals.
func (s *Store) RepairInterruptedRun(ctx context.Context, stream string, request events.RecoveryRequest) ([]events.Event, error) {
	if request.StartSequence <= 0 || request.ObservedSequence < request.StartSequence || len(request.Closures) > 64 {
		return nil, fmt.Errorf("invalid recovery request")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Acquire SQLite's write reservation before comparing the snapshot.
	if _, err := tx.ExecContext(ctx, `UPDATE streams SET kind=kind WHERE id=?`, stream); err != nil {
		return nil, err
	}
	var head int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM events WHERE stream_id=?`, stream).Scan(&head); err != nil {
		return nil, err
	}
	if head != request.ObservedSequence {
		return nil, nil
	}
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE stream_id=? AND sequence=? AND type IN ('run.started','external.run.started')) AND NOT EXISTS(SELECT 1 FROM events WHERE stream_id=? AND sequence>? AND sequence!=? AND type IN ('run.started','external.run.started','run.completed','run.failed','run.cancelled','run.interrupted','external.run.completed','external.run.failed','external.run.cancelled','external.run.interrupted'))`, stream, request.StartSequence, stream, request.StartSequence, request.InterruptedSequence).Scan(&eligible)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, nil
	}
	if request.InterruptedSequence > 0 {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE stream_id=? AND sequence=? AND sequence>? AND type IN ('run.interrupted','external.run.interrupted'))`, stream, request.InterruptedSequence, request.StartSequence).Scan(&eligible); err != nil {
			return nil, err
		}
		if !eligible {
			return nil, nil
		}
	}
	result := make([]events.Event, 0, len(request.Closures)+1)
	appendEvent := func(typ string, data any) error {
		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if len(payload) > events.MaxDataBytes {
			return events.ErrEventBudgetExceeded
		}
		if err := ensureStreamBudget(ctx, tx, stream, len(payload)); err != nil {
			return err
		}
		head++
		event := events.Event{ID: id.New(), StreamID: stream, Sequence: head, Type: typ, Data: payload, CreatedAt: time.Now().UTC()}
		if _, err := tx.ExecContext(ctx, `INSERT INTO events(id,stream_id,sequence,type,data,created_at) VALUES(?,?,?,?,?,?)`, event.ID, stream, head, typ, []byte(payload), event.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		result = append(result, event)
		return nil
	}
	for _, closure := range request.Closures {
		typ := "tool.skipped"
		switch closure.ErrorCode {
		case "not_executed":
		case "outcome_unknown":
			typ = "tool.failed"
		default:
			return nil, fmt.Errorf("invalid recovery outcome")
		}
		if closure.ToolCallID == "" {
			return nil, fmt.Errorf("invalid recovery tool")
		}
		if err := appendEvent(typ, closure); err != nil {
			return nil, err
		}
	}
	if request.InterruptedSequence == 0 {
		if err := appendEvent("run.interrupted", map[string]string{"reason": "app_restart"}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
