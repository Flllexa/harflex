package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/id"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWalkAfterStopsBeforeExceedingByteBudget(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "walk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	payload := json.RawMessage(`"` + strings.Repeat("x", (1<<20)-2) + `"`)
	for range 64 {
		if _, err := db.Append(t.Context(), "stream", "test", "data", payload); err != nil {
			t.Fatal(err)
		}
	}
	appendLegacyBudgetRow(t, db, "stream", payload)
	count, total := 0, 0
	err = db.WalkAfter(t.Context(), "stream", 0, 100, 64<<20, func(e events.Event) error { count++; total += len(e.Data); return nil })
	if !errors.Is(err, events.ErrEventBudgetExceeded) || count != 64 || total != 64<<20 {
		t.Fatal(count, total, err)
	}
	if _, err := db.Append(t.Context(), "other", "test", "data", nil); err != nil {
		t.Fatal("rows not released", err)
	}
}

func TestWalkAfterLargeEventsShareTotalBudget(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "large.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	payload := json.RawMessage(`"` + strings.Repeat("x", (32<<20)-2) + `"`)
	for range 2 {
		if _, err := db.Append(t.Context(), "stream", "test", "data", payload); err != nil {
			t.Fatal(err)
		}
	}
	appendLegacyBudgetRow(t, db, "stream", payload)
	count, total := 0, 0
	err = db.WalkAfter(t.Context(), "stream", 0, 10, 64<<20, func(event events.Event) error { count++; total += len(event.Data); return nil })
	if !errors.Is(err, events.ErrEventBudgetExceeded) || count != 2 || total != 64<<20 {
		t.Fatal(count, total, err)
	}
}

// Simulates a pre-budget journal without weakening the production writer.
func appendLegacyBudgetRow(t *testing.T, db *Store, stream string, payload []byte) {
	t.Helper()
	_, err := db.DB().Exec(`INSERT INTO events(id,stream_id,sequence,type,data,created_at) SELECT ?,?,COALESCE(MAX(sequence),0)+1,'data',?,? FROM events WHERE stream_id=?`, id.New(), stream, payload, time.Now().UTC().Format(time.RFC3339Nano), stream)
	if err != nil {
		t.Fatal(err)
	}
}

func TestWalkAfterBoundsIndividualRowsAndReleasesOnCallbackOrContextError(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "walk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, size := range []int{events.MaxDataBytes, events.MaxDataBytes + 1} {
		stream := map[bool]string{true: "max", false: "oversized"}[size == events.MaxDataBytes]
		payload := json.RawMessage(`"` + strings.Repeat("x", size-2) + `"`)
		_, appendErr := db.Append(t.Context(), stream, "test", "data", payload)
		validationErr := events.ValidateDataSize(payload)
		if size == events.MaxDataBytes {
			if appendErr != nil || validationErr != nil {
				t.Fatal(appendErr, validationErr)
			}
		} else {
			if !errors.Is(appendErr, events.ErrEventBudgetExceeded) || !errors.Is(validationErr, events.ErrEventBudgetExceeded) {
				t.Fatal("writer accepted oversized data", appendErr, validationErr)
			}
			// Simulate an oversized legacy/corrupt row bypassing the guarded writer.
			if _, err := db.Append(t.Context(), stream, "test", "data", nil); err != nil {
				t.Fatal(err)
			}
			if _, err := db.DB().Exec(`UPDATE events SET data=? WHERE stream_id=?`, []byte(payload), stream); err != nil {
				t.Fatal(err)
			}
		}
		count := 0
		err := db.WalkAfter(t.Context(), stream, 0, 10, 64<<20, func(e events.Event) error { count++; return nil })
		if size == events.MaxDataBytes {
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
		} else if !errors.Is(err, events.ErrEventBudgetExceeded) || count != 0 {
			t.Fatal(count, err)
		}
	}
	if _, err := db.Append(t.Context(), "small", "test", "data", nil); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("callback stopped")
	if err := db.WalkAfter(t.Context(), "small", 0, 10, 64<<20, func(events.Event) error { return stop }); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := db.WalkAfter(ctx, "small", 0, 10, 64<<20, func(events.Event) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	if err := db.WalkAfter(ctx, "small", 0, 10, 64<<20, func(events.Event) error { cancel(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.Append(t.Context(), "after", "test", "data", nil); err != nil {
		t.Fatal("rows not released", err)
	}
}
