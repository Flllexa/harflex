package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/events"
)

// A long read of a journal (a screen following a run, a conversation reopening) no longer holds the connection the
// runs write through: a write goes through while the read is still going.
func TestAJournalReadDoesNotHoldUpWrites(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "harflex.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for range 3 {
		if _, err := store.Append(t.Context(), "reading", "agent_session", "tool.completed", map[string]string{"text": "linha"}); err != nil {
			t.Fatal(err)
		}
	}
	reading, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		first := true
		done <- store.WalkAfter(context.Background(), "reading", 0, 100, 1<<20, func(events.Event) error {
			if first {
				first = false
				close(reading)
				<-release
			}
			return nil
		})
	}()
	<-reading
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := store.Append(ctx, "writing", "agent_session", "assistant.delta", map[string]string{"delta": "ok"}); err != nil {
		t.Fatalf("a write waited behind a read: %v", err)
	}
	if found, err := store.ListAfter(ctx, "writing", 0); err != nil || len(found) != 1 {
		t.Fatalf("the written event is not read back: %d %v", len(found), err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
