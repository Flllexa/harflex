package sqlite

import (
	"github.com/persioflexa/harflex/internal/events"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLegacyRepairIsAtomicAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repair.db")
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
	start, _ := first.Append(t.Context(), "stream", "agent_session", "run.started", nil)
	terminal, _ := first.Append(t.Context(), "stream", "agent_session", "run.interrupted", nil)
	request := events.RecoveryRequest{StartSequence: start.Sequence, ObservedSequence: terminal.Sequence, InterruptedSequence: terminal.Sequence, Closures: []events.ToolClosure{{ToolCallID: "called", ErrorCode: "outcome_unknown", Error: "tool outcome unknown after interruption"}}}
	var writes atomic.Int32
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db := first
			if i%2 == 1 {
				db = second
			}
			items, err := db.RepairInterruptedRun(t.Context(), "stream", request)
			if err != nil {
				t.Error(err)
			}
			writes.Add(int32(len(items)))
		}()
	}
	wg.Wait()
	items, err := first.ListAfter(t.Context(), "stream", 0)
	if err != nil || writes.Load() != 1 || len(items) != 3 || items[2].Type != "tool.failed" {
		t.Fatal(writes.Load(), items, err)
	}
}

func TestRepairRollsBackEveryClosureAndTerminal(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "repair.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	start, _ := db.Append(t.Context(), "stream", "agent_session", "run.started", nil)
	db.DB().Exec(`CREATE TRIGGER reject_unknown BEFORE INSERT ON events WHEN NEW.type='tool.failed' BEGIN SELECT RAISE(FAIL,'failure'); END`)
	request := events.RecoveryRequest{StartSequence: start.Sequence, ObservedSequence: start.Sequence, Closures: []events.ToolClosure{{ToolCallID: "not-called", ErrorCode: "not_executed"}, {ToolCallID: "called", ErrorCode: "outcome_unknown"}}}
	if items, err := db.RepairInterruptedRun(t.Context(), "stream", request); err == nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	items, err := db.ListAfter(t.Context(), "stream", 0)
	if err != nil || len(items) != 1 {
		t.Fatal("partial recovery escaped transaction", items, err)
	}
}
