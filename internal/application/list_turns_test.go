package application

import (
	"errors"
	"testing"
)

func TestAConversationOpensOnItsLastTurns(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.ListEvents(ListEventsInput{SessionID: chat.ID})
	if err != nil {
		t.Fatal(err)
	}
	for turn := 0; turn < 3; turn++ {
		for _, kind := range []string{"message.user", "run.started", "run.completed"} {
			if _, err := db.Append(t.Context(), chat.ID, "agent_session", kind, map[string]string{"content": "x"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	last, err := s.ListEventsBefore(ListEventsBeforeInput{SessionID: chat.ID, Turns: 1})
	if err != nil || len(last) != 3 || last[0].Type != "message.user" || last[0].Sequence != int64(len(base))+7 {
		t.Fatalf("last turn: %+v %v", last, err)
	}
	older, err := s.ListEventsBefore(ListEventsBeforeInput{SessionID: chat.ID, BeforeSequence: last[0].Sequence, Turns: 1})
	if err != nil || len(older) != 3 || older[2].Sequence != last[0].Sequence-1 {
		t.Fatalf("the turn before: %+v %v", older, err)
	}
	if _, err := s.ListEventsBefore(ListEventsBeforeInput{SessionID: chat.ID, Turns: 0}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("no turns asked: %v", err)
	}
	if _, err := s.ListEventsBefore(ListEventsBeforeInput{SessionID: "missing", Turns: 1}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown conversation: %v", err)
	}
}
