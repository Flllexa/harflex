package application

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestListLogEventsFiltersPaginatesAndDoesNotExposePayload(t *testing.T) {
	s, db, _ := setup(t)
	now := time.Now().UTC()
	for _, item := range []catalog.Workspace{{ID: "one", Path: t.TempDir(), Profile: "ask", CreatedAt: now}, {ID: "two", Path: t.TempDir(), Profile: "ask", CreatedAt: now}} {
		if err := db.UpsertWorkspace(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []catalog.SessionRecord{{ID: "s1", WorkspaceID: "one", BackendID: "local", CreatedAt: now, UpdatedAt: now}, {ID: "s2", WorkspaceID: "two", BackendID: "local", CreatedAt: now, UpdatedAt: now}} {
		if err := db.UpsertSession(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []struct{ session, kind string }{{"s1", "run.started"}, {"s2", "run.failed"}, {"s1", "run.completed"}} {
		if _, err := db.Append(t.Context(), step.session, "agent_session", step.kind, map[string]string{"private": "secret-canary-log"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ListLogEvents(ListLogEventsInput{WorkspaceID: "one", Limit: 1})
	if err != nil || len(page) != 1 || page[0].Type != "run.completed" || page[0].SessionID != "s1" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	older, err := s.ListLogEvents(ListLogEventsInput{WorkspaceID: "one", BeforeID: page[0].Cursor, Limit: 10})
	if err != nil || len(older) != 1 || older[0].Type != "run.started" {
		t.Fatalf("older page: %+v %v", older, err)
	}
	filtered, err := s.ListLogEvents(ListLogEventsInput{Type: "run.failed", Limit: 10})
	if err != nil || len(filtered) != 1 || filtered[0].SessionID != "s2" {
		t.Fatalf("type filter: %+v %v", filtered, err)
	}
	encoded, _ := json.Marshal(append(append(page, older...), filtered...))
	if strings.Contains(string(encoded), "secret-canary-log") || strings.Contains(string(encoded), "private") {
		t.Fatal("log index exposed event payload")
	}
}
