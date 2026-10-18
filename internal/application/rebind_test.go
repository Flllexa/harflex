package application

import (
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

// Editing the provider in Settings used to leave every chat of it read-only; it now goes on with the same model.
func TestEditedProfileKeepsTheConversation(t *testing.T) {
	provider := &fakeProvider{}
	s, db, vault, session := sessionSetup(t, provider)
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "primeiro"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	edited := profileInput()
	edited.Name = "Local (editado)"
	if _, err := s.SaveProviderProfile(edited); err != nil {
		t.Fatal(err)
	}
	next := &fakeProvider{}
	reopened := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(c openai.Config) (agentcore.Provider, error) { next.key = c.APIKey; return next, nil }})
	opened, err := reopened.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
	if err != nil || !opened.Resumable {
		t.Fatalf("the chat closed after its provider was edited: %+v %v", opened, err)
	}
	if result, err := reopened.Prompt(PromptInput{SessionID: session.ID, Text: "segundo"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("the chat did not go on: %+v %v", result, err)
	}
	if got := len(next.request.Messages); got < 3 {
		t.Fatalf("the history did not come along: %d messages", got)
	}
}

func TestAProviderEditedWhileTheChatIsOpenDoesNotStopIt(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "primeiro"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	edited := profileInput()
	edited.Name = "Local (editado)"
	if _, err := s.SaveProviderProfile(edited); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "segundo"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("the open chat stopped when its provider was edited: %+v %v", result, err)
	}
}

func TestDeletedProviderLeavesTheChatReadOnly(t *testing.T) {
	s, db, _, session := sessionSetup(t, &fakeProvider{})
	if _, err := db.DB().Exec(`DELETE FROM provider_profiles WHERE id='local'`); err != nil {
		t.Skipf("cannot remove the profile in this schema: %v", err)
	}
	opened, err := s.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
	if err == nil && opened.Resumable {
		t.Fatal("a chat whose provider is gone claims it can go on")
	}
}
