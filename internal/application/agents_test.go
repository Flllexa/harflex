package application

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestAgentSnapshotControlsSessionInstructionsAndToolsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.db")
	db, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	vault := &memorySecrets{values: map[secrets.Reference]string{}}
	provider := &fakeProvider{}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Analista", Description: "Lê código", Instructions: "Leia antes de responder", BackendID: "local", AllowedTools: []string{"read", "grep"}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, AgentID: agent.ID})
	if err != nil || session.BackendID != "local" {
		t.Fatalf("agent session: %+v %v", session, err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Olá"}); err != nil {
		t.Fatal(err)
	}
	if len(provider.request.Messages) == 0 || provider.request.Messages[0].Role != agentcore.RoleSystem || !strings.Contains(provider.request.Messages[0].Content, "Leia antes") {
		t.Fatalf("agent instructions missing: %+v", provider.request.Messages)
	}
	// The plan and Harflex tools stay beside the agent's own list.
	names := []string{}
	for _, tool := range provider.request.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "grep,harflex_create_pipeline,harflex_get_pipeline,harflex_list_pipelines,read,update_plan" {
		t.Fatalf("unexpected tools: %+v", provider.request.Tools)
	}
	if _, err := s.SaveAgent(SaveAgentInput{ID: agent.ID, Name: "Analista", Description: "Atualizado", Instructions: "INSTRUÇÃO NOVA", BackendID: "local", AllowedTools: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	Shutdown(s)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	provider = &fakeProvider{}
	s = NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }})
	if _, err := s.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Continue"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(provider.request.Messages[0].Content, "INSTRUÇÃO NOVA") || len(provider.request.Tools) != 6 {
		t.Fatalf("session snapshot changed after edit: %+v %+v", provider.request.Messages, provider.request.Tools)
	}
}
