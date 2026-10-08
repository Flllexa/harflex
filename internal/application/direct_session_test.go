package application

import (
	"bytes"
	"errors"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func TestDirectSessionRequiresAndPersistsRedactedSDDBypassReason(t *testing.T) {
	s, db, vault := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty bypass reason accepted: %v", err)
	}
	secret := profileInput().APIKey
	session, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "Conversa livre; " + secret})
	if err != nil {
		t.Fatal(err)
	}
	events, err := db.ListAfter(t.Context(), session.ID, 0)
	if err != nil || len(events) != 1 || events[0].Type != "sdd.bypassed" || !bytes.Contains(events[0].Data, []byte("Conversa livre")) || bytes.Contains(events[0].Data, []byte(secret)) {
		t.Fatalf("bypass reason missing or leaked credential: %+v %v", events, err)
	}
	Shutdown(s)
	reopened := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return &fakeProvider{}, nil }})
	if _, err := reopened.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: workspace.ID}); err != nil {
		t.Fatalf("bypassed direct session did not reopen: %v", err)
	}
}
