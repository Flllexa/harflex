package application

import (
	"testing"
)

func TestSessionDTOReportsTheEffectiveContextWindow(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	opened, err := s.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
	if err != nil || opened.ContextWindow != 200000 {
		t.Fatalf("default window = %d %v", opened.ContextWindow, err)
	}
	limit := 100000
	if _, err := s.SaveSettings(SaveSettingsInput{MaxContextTokens: &limit}); err != nil {
		t.Fatal(err)
	}
	opened, err = s.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
	if err != nil || opened.ContextWindow != 100000 {
		t.Fatalf("window with the Settings maximum = %d %v", opened.ContextWindow, err)
	}
}

func TestCompactSessionOnDemand(t *testing.T) {
	provider := &fakeProvider{output: "Resumo: tudo certo."}
	s, db, _, session := sessionSetup(t, provider)
	for _, text := range []string{"primeiro", "segundo"} {
		if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CompactSession(CompactSessionInput{SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	stored, err := db.ListAfter(t.Context(), session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range stored {
		found = found || event.Type == "context.compacted"
	}
	if !found {
		t.Fatal("no compaction was journaled")
	}
}
