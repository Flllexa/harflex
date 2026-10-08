package application

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestListSessionsTitlesConversationsAndLabelsInternalSessions(t *testing.T) {
	s, db, _, chat := sessionSetup(t, &fakeProvider{})
	if _, err := s.Prompt(PromptInput{chat.ID, "  Revise o\n\n módulo   de login\t" + strings.Repeat("palavra ", 40)}); err != nil {
		t.Fatal(err)
	}
	untouched, err := s.CreateSession(CreateSessionInput{WorkspaceID: chat.WorkspaceID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Minute)
	for id, mode := range map[string]string{"authoring": "sdd_readonly", "coder": catalog.AuthoringCodeSessionMode} {
		record := catalog.SessionRecord{ID: id, WorkspaceID: chat.WorkspaceID, BackendID: chat.BackendID, Mode: mode, Status: "completed", CreatedAt: now, UpdatedAt: now}
		if err := db.UpsertSession(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Append(t.Context(), id, "agent_session", "message.user", map[string]any{"content": "internal prompt that must not become a title"}); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := s.ListSessions(ListSessionsInput{WorkspaceID: chat.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]SessionDTO{}
	for _, item := range listed {
		byID[item.ID] = item
	}
	if len(byID) != 4 {
		t.Fatalf("sessions: %+v", listed)
	}
	title := byID[chat.ID].Title
	if byID[chat.ID].Purpose != "chat" || !strings.HasPrefix(title, "Revise o módulo de login palavra") || strings.ContainsAny(title, "\n\t") || len([]rune(title)) != sessionTitleRunes || !strings.HasSuffix(title, "…") {
		t.Fatalf("chat title was not a bounded single line: %q (%s)", title, byID[chat.ID].Purpose)
	}
	if byID[untouched.ID].Purpose != "chat" || byID[untouched.ID].Title != "" {
		t.Fatalf("a session without messages must stay untitled: %+v", byID[untouched.ID])
	}
	if byID["authoring"].Purpose != "preparation" || byID["authoring"].Title != "Preparação de documentos" ||
		byID["coder"].Purpose != "code" || byID["coder"].Title != "Execução de Code (SDD autoral)" {
		t.Fatalf("internal sessions must carry their purpose label, never a message preview: %+v %+v", byID["authoring"], byID["coder"])
	}
	opened, err := s.OpenSession(OpenSessionInput{SessionID: chat.ID, WorkspaceID: chat.WorkspaceID})
	if err != nil || opened.Purpose != "chat" || opened.Title != "" {
		t.Fatalf("OpenSession reports purpose but does not scan history: %+v %v", opened, err)
	}
}

func TestSessionTitleKeepsRedactionAndShortText(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	if _, err := s.Prompt(PromptInput{session.ID, "chave " + profileInput().APIKey + " curta"}); err != nil {
		t.Fatal(err)
	}
	if title := s.sessionTitle(session.ID); title != "chave [REDACTED] curta" {
		t.Fatalf("title must come from the redacted journal: %q", title)
	}
}

// A conversation continued from one that could not be resumed starts with the person's words and carries the old
// conversation's context after a marker line. The chat is named after the words.
func TestSessionTitleStopsAtTheContextOfAContinuedConversation(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	if _, err := s.Prompt(PromptInput{session.ID, "Corrija o título do PR" + continuationMarker + " (referência)\nPrimeiro pedido nela:\nAbra o pull request"}); err != nil {
		t.Fatal(err)
	}
	if title := s.sessionTitle(session.ID); title != "Corrija o título do PR" {
		t.Fatalf("the title must stop where the carried context starts: %q", title)
	}

	// A message that begins with the marker has no words of its own to name the chat after: it is kept whole.
	other, err := s.CreateSession(CreateSessionInput{WorkspaceID: session.WorkspaceID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{other.ID, continuationMarker + " em um texto qualquer"}); err != nil {
		t.Fatal(err)
	}
	if title := s.sessionTitle(other.ID); !strings.HasPrefix(title, "--- Contexto da conversa anterior") {
		t.Fatalf("a message that starts with the marker is titled by itself: %q", title)
	}
}

// The marker is a protocol between this side and the screen (frontend/src/features/workbench/continuation.ts): both are
// held to the same file, so changing one without the other fails a test on each.
func TestContinuationMarkerIsTheOneTheScreenSends(t *testing.T) {
	raw, err := os.ReadFile("testdata/continuation.json")
	if err != nil {
		t.Fatal(err)
	}
	var shared struct {
		Marker string `json:"marker"`
	}
	if err := json.Unmarshal(raw, &shared); err != nil {
		t.Fatal(err)
	}
	if shared.Marker != continuationMarker {
		t.Fatalf("the screen sends %q and the title stops at %q", shared.Marker, continuationMarker)
	}
}
