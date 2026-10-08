package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConditionalInterruptionAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	first, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	started, err := first.Append(t.Context(), "session", "agent_session", "run.started", nil)
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	gate := make(chan struct{})
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			db := first
			if i%2 == 1 {
				db = second
			}
			_, ok, err := db.AppendIfNoTerminalAfter(t.Context(), "session", "agent_session", started.Sequence, "run.interrupted", map[string]string{"reason": "app_restart"})
			if ok {
				count.Add(1)
			}
			errs <- err
		}()
	}
	close(gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	items, _ := first.ListAfter(t.Context(), "session", 0)
	if count.Load() != 1 || len(items) != 2 || items[1].Type != "run.interrupted" {
		t.Fatal("duplicate interruption", count.Load(), len(items))
	}
}

func TestConditionalInterruptionRechecksTerminalAndNewerRun(t *testing.T) {
	for _, next := range []string{"run.completed", "run.failed", "run.cancelled", "run.interrupted", "external.run.completed", "external.run.failed", "external.run.cancelled", "run.started", "external.run.started"} {
		t.Run(next, func(t *testing.T) {
			db, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			started, _ := db.Append(t.Context(), "session", "agent_session", "run.started", nil)
			db.Append(t.Context(), "session", "agent_session", next, nil)
			_, ok, err := db.AppendIfNoTerminalAfter(t.Context(), "session", "agent_session", started.Sequence, "run.interrupted", nil)
			if err != nil || ok {
				t.Fatal("stale recovery appended", ok, err)
			}
		})
	}
}

func TestConditionalInterruptionRollsBackOnInsertFailure(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	started, _ := db.Append(t.Context(), "session", "agent_session", "run.started", nil)
	_, err = db.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_interruption BEFORE INSERT ON events WHEN NEW.type = 'run.interrupted' BEGIN SELECT RAISE(FAIL, 'failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.AppendIfNoTerminalAfter(context.Background(), "session", "agent_session", started.Sequence, "run.interrupted", map[string]string{"reason": "app_restart"}); err == nil || ok {
		t.Fatal(ok, err)
	}
	items, _ := db.ListAfter(t.Context(), "session", 0)
	if len(items) != 1 {
		t.Fatal("failed append changed journal")
	}
}
