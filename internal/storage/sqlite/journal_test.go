package sqlite_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/storage/migrations"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestOpenPreservesLiteralDatabasePath(t *testing.T) {
	for _, name := range []string{"journal#percent%25.db", "journal?fragment#percent%25.db"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.Contains(name, "?") {
				t.Skip("Windows filenames cannot contain a question mark")
			}
			ctx := context.Background()
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			store := openStore(t, path)
			want, err := store.Append(ctx, "stream", "session", "created", map[string]string{"text": "persisted"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("database missing at exact path: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != name && !strings.HasPrefix(entry.Name(), name+"-") {
					t.Fatalf("unexpected sibling database: %s", entry.Name())
				}
			}
			store = openStore(t, path)
			got, err := store.ListAfter(ctx, "stream", 0)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, []events.Event{want}) {
				t.Fatalf("reopened events = %#v, want %#v", got, want)
			}
		})
	}
}

func TestMemoryDatabaseIsPrivateAndEphemeral(t *testing.T) {
	ctx := context.Background()
	first := openStore(t, ":memory:")
	if _, err := first.Append(ctx, "stream", "session", "created", nil); err != nil {
		t.Fatal(err)
	}
	second := openStore(t, ":memory:")
	got, err := second.ListAfter(ctx, "stream", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatal("memory databases must not share events")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openStore(t, ":memory:")
	got, err = reopened.ListAfter(ctx, "stream", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatal("memory events survived close")
	}
}

func openStore(t *testing.T, path string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func TestJournalSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "journal.db")
	store := openStore(t, path)
	before := time.Now().UTC()
	want := make([]events.Event, 0, 2)
	for _, eventType := range []string{"session.created", "session.started"} {
		event, err := store.Append(ctx, "session-1", "session", eventType, map[string]string{"message": eventType})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, event)
	}
	after := time.Now().UTC()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openStore(t, path)
	got, err := store.ListAfter(ctx, "session-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened events = %#v, want %#v", got, want)
	}
	for i, event := range got {
		if event.Sequence != int64(i+1) || event.ID == "" || event.StreamID != "session-1" {
			t.Fatalf("invalid event identity: %#v", event)
		}
		if event.CreatedAt.Before(before) || event.CreatedAt.After(after) || event.CreatedAt.Location() != time.UTC {
			t.Fatalf("invalid timestamp: %v", event.CreatedAt)
		}
		var payload map[string]string
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["message"] != event.Type {
			t.Fatalf("payload = %v, type = %s", payload, event.Type)
		}
	}
	if got[0].ID == got[1].ID {
		t.Fatal("event IDs must be unique")
	}
	tail, err := store.ListAfter(ctx, "session-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tail, want[1:]) {
		t.Fatalf("tail = %#v", tail)
	}
	other, err := store.Append(ctx, "session-2", "session", "session.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	if other.Sequence != 1 {
		t.Fatalf("independent stream sequence = %d", other.Sequence)
	}
}

func TestOpenEnablesPragmasAndVersionsMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store := openStore(t, path)
		var mode string
		if err := store.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if mode != "wal" {
			t.Fatalf("journal mode = %q", mode)
		}
		var enabled, migrations int
		if err := store.DB().QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil {
			t.Fatal(err)
		}
		if enabled != 1 {
			t.Fatal("foreign keys disabled")
		}
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil {
			t.Fatal(err)
		}
		if migrations != len(files) {
			t.Fatalf("migration count = %d", migrations)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAppendFailureDoesNotConsumeSequence(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, filepath.Join(t.TempDir(), "journal.db"))
	if _, err := store.Append(ctx, "stream", "session", "invalid", make(chan int)); err == nil {
		t.Fatal("unsupported JSON must fail")
	}
	if _, err := store.DB().Exec(`CREATE TRIGGER reject_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT, 'rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, "stream", "session", "rejected", nil); err == nil {
		t.Fatal("rejected insert must fail")
	}
	var count int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM streams").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed append persisted a stream")
	}
	if _, err := store.DB().Exec("DROP TRIGGER reject_event"); err != nil {
		t.Fatal(err)
	}
	event, err := store.Append(ctx, "stream", "session", "accepted", nil)
	if err != nil {
		t.Fatal(err)
	}
	if event.Sequence != 1 {
		t.Fatalf("sequence = %d", event.Sequence)
	}
}

func TestListAfterLimitBoundsSQLAndClosesRows(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "journal.db"))
	for range 128 {
		if _, err := store.Append(t.Context(), "stream", "session", "test", nil); err != nil {
			t.Fatal(err)
		}
	}
	// A malformed row beyond the requested page must never be fetched or decoded.
	if _, err := store.DB().Exec("UPDATE events SET created_at = 'invalid' WHERE stream_id = ? AND sequence = ?", "stream", 128); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		after int64
		limit int
		count int
	}{{0, 1, 1}, {0, 10, 10}, {17, 10, 10}, {125, 2, 2}, {128, 10, 0}} {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		got, err := store.ListAfterLimit(ctx, "stream", tc.after, tc.limit)
		if err != nil || len(got) != tc.count {
			cancel()
			t.Fatalf("after=%d limit=%d count=%d: %v", tc.after, tc.limit, len(got), err)
		}
		for i, event := range got {
			if event.Sequence != tc.after+int64(i)+1 {
				t.Fatal("incorrect page sequence", event.Sequence)
			}
		}
		if store.DB().Stats().InUse != 0 {
			t.Fatal("limited query retained a connection")
		}
		if err := store.DB().PingContext(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
	for _, limit := range []int{-1, 0, 1001} {
		if _, err := store.ListAfterLimit(t.Context(), "stream", 0, limit); err == nil {
			t.Fatal("invalid limit accepted", limit)
		}
	}
	if _, err := store.ListAfter(t.Context(), "stream", 0); err == nil {
		t.Fatal("unbounded query must reach malformed row")
	}
}

func TestConcurrentAppendAndLimitedReads(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "journal.db"))
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for worker := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				if worker == 0 {
					if _, err := store.Append(t.Context(), "stream", "session", "test", nil); err != nil {
						failures <- err
						return
					}
				} else {
					items, err := store.ListAfterLimit(t.Context(), "stream", 0, 10)
					if err != nil {
						failures <- err
						return
					}
					for i, event := range items {
						if event.Sequence != int64(i+1) {
							failures <- fmt.Errorf("incorrect sequence %d", event.Sequence)
							return
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	items, err := store.ListAfterLimit(t.Context(), "stream", 20, 10)
	if err != nil || len(items) != 10 {
		t.Fatal(len(items), err)
	}
}
