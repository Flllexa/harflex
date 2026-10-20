package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
)

// A long conversation opens on its last turns; older turns come a page at a time, each starting at the person's
// message so no reply or tool call is cut in half.
func TestListTurnsBeforeReturnsWholeTurnsFromTheEnd(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "harflex.db"))
	ctx := context.Background()
	kinds := []string{"message.user", "run.started", "assistant.delta", "run.completed"}
	for turn := 0; turn < 4; turn++ {
		for _, kind := range kinds {
			if _, err := store.Append(ctx, "chat", "agent_session", kind, map[string]int{"turn": turn}); err != nil {
				t.Fatal(err)
			}
		}
	}
	last, err := store.ListTurnsBefore(ctx, "chat", 0, 2)
	if err != nil || len(last) != 8 || last[0].Sequence != 9 || last[0].Type != "message.user" || last[7].Sequence != 16 {
		t.Fatalf("last two turns: %d events from %v, %v", len(last), first(last), err)
	}
	older, err := store.ListTurnsBefore(ctx, "chat", 9, 1)
	if err != nil || len(older) != 4 || older[0].Sequence != 5 || older[3].Sequence != 8 {
		t.Fatalf("the turn before: %d events from %v, %v", len(older), first(older), err)
	}
	all, err := store.ListTurnsBefore(ctx, "chat", 5, 10)
	if err != nil || len(all) != 4 || all[0].Sequence != 1 {
		t.Fatalf("fewer turns than asked: %d events from %v, %v", len(all), first(all), err)
	}
	if none, err := store.ListTurnsBefore(ctx, "chat", 1, 3); err != nil || len(none) != 0 {
		t.Fatalf("nothing before the start: %d %v", len(none), err)
	}
}

func first[T any](items []T) any {
	if len(items) == 0 {
		return nil
	}
	return items[0]
}
