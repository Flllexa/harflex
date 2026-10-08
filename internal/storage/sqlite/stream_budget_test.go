package sqlite

import (
	"encoding/json"
	"errors"
	"github.com/persioflexa/harflex/internal/events"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentAppendsCannotExceedStreamBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.db")
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
	payload := json.RawMessage(`"` + strings.Repeat("x", (32<<20)-2) + `"`)
	if _, err := first.Append(t.Context(), "stream", "test", "data", payload); err != nil {
		t.Fatal(err)
	}
	var successes, failures atomic.Int32
	var wg sync.WaitGroup
	for _, db := range []*Store{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := db.Append(t.Context(), "stream", "test", "data", payload); err != nil {
				if !errors.Is(err, events.ErrStreamBudgetExceeded) {
					t.Error("wrong refusal", err)
				}
				failures.Add(1)
			} else {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	var total int64
	if err := first.DB().QueryRow(`SELECT COALESCE(SUM(length(data)),0) FROM events WHERE stream_id='stream'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if successes.Load() != 1 || failures.Load() != 1 || total != 64<<20 {
		t.Fatal("concurrent writers exceeded stream budget", successes.Load(), failures.Load(), total)
	}
}

func TestRecoveryWritersCannotExceedStreamBudget(t *testing.T) {
	for _, mode := range []string{"conditional", "repair"} {
		t.Run(mode, func(t *testing.T) {
			db, err := Open(t.Context(), filepath.Join(t.TempDir(), "budget.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			start, err := db.Append(t.Context(), "stream", "agent_session", "run.started", nil)
			if err != nil {
				t.Fatal(err)
			}
			// Bypass normal payload construction to fill the valid legacy stream exactly.
			if _, err := db.DB().Exec(`UPDATE events SET data=zeroblob(?) WHERE stream_id='stream'`, 64<<20); err != nil {
				t.Fatal(err)
			}
			if mode == "conditional" {
				if _, ok, err := db.AppendIfNoTerminalAfter(t.Context(), "stream", "agent_session", start.Sequence, "run.interrupted", nil); !errors.Is(err, events.ErrStreamBudgetExceeded) || ok {
					t.Fatal("conditional writer exceeded budget", ok, err)
				}
			} else {
				request := events.RecoveryRequest{StartSequence: start.Sequence, ObservedSequence: start.Sequence, Closures: []events.ToolClosure{{ToolCallID: "call", ErrorCode: "outcome_unknown"}}}
				if appended, err := db.RepairInterruptedRun(t.Context(), "stream", request); !errors.Is(err, events.ErrStreamBudgetExceeded) || len(appended) != 0 {
					t.Fatal("repair writer exceeded budget", appended, err)
				}
			}
			var count int
			if err := db.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE stream_id='stream'`).Scan(&count); err != nil || count != 1 {
				t.Fatal(count, err)
			}
		})
	}
}
