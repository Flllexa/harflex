package application

import (
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func TestCoordinatorInstructionsNameTheWorkAndHowToReadIt(t *testing.T) {
	_, db, vault := setup(t)
	provider := &fakeProvider{}
	factory := func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: factory})
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	if got := s.coordinatorInstructions("no-such-session"); got != "" {
		t.Fatalf("a chat that coordinates nothing got instructions: %q", got)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar faturas", Objective: "CSV"})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "Conversa do trabalho"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.coordinatorInstructions(chat.ID); got != "" {
		t.Fatalf("an unlinked chat got instructions: %q", got)
	}
	if err := s.store.SetPipelineCoordinator(s.ctx, run.ID, chat.ID); err != nil {
		t.Fatal(err)
	}
	note := s.coordinatorInstructions(chat.ID)
	if !strings.Contains(note, `"Exportar faturas"`) || !strings.Contains(note, "harflex_get_pipeline") || !strings.Contains(note, run.ID) {
		t.Fatalf("the note does not name the work or how to read it: %s", note)
	}
	list, err := s.ListWorkCoordinators(workspace.ID)
	if err != nil || len(list) != 1 || list[0].SessionID != chat.ID || list[0].PipelineID != run.ID {
		t.Fatalf("list work coordinators: %+v %v", list, err)
	}
	found, err := s.GetPipelineForSession(GetPipelineForSessionInput{SessionID: chat.ID, WorkspaceID: workspace.ID})
	if err != nil || found.ID != run.ID {
		t.Fatalf("the coordinating chat does not resolve to its work: %+v %v", found, err)
	}
}

func TestEnsureWorkChatsGivesEachWorkOneChatAndOnlyOnce(t *testing.T) {
	_, db, vault := setup(t)
	provider := &fakeProvider{}
	factory := func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: factory})
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar faturas", Objective: "CSV"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Relatório mensal", Objective: "PDF"})
	if err != nil {
		t.Fatal(err)
	}
	// No default backend yet: nothing is created and nothing fails.
	if result, err := s.EnsureWorkChats(workspace.ID); err != nil || result.Created != 0 || len(result.Coordinators) != 0 {
		t.Fatalf("without a default backend: %+v %v", result, err)
	}
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "local"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.EnsureWorkChats(workspace.ID)
	if err != nil || result.Created != 2 || len(result.Coordinators) != 2 {
		t.Fatalf("first pass: %+v %v", result, err)
	}
	seen := map[string]string{}
	for _, item := range result.Coordinators {
		seen[item.PipelineID] = item.SessionID
	}
	if seen[first.ID] == "" || seen[second.ID] == "" || seen[first.ID] == seen[second.ID] {
		t.Fatalf("each work needs its own chat: %v", seen)
	}
	again, err := s.EnsureWorkChats(workspace.ID)
	if err != nil || again.Created != 0 || len(again.Coordinators) != 2 {
		t.Fatalf("a second pass must create nothing: %+v %v", again, err)
	}
	if note := s.coordinatorInstructions(seen[first.ID]); !strings.Contains(note, first.ID) {
		t.Fatalf("the chat does not know its work: %s", note)
	}
}

func TestEnsureWorkChatsWorksWithACLIDefaultAndNoModelChosen(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "claude", version: "2.1.0"}
	s.external[stub.id] = stub
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar faturas", Objective: "CSV"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "claude"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.EnsureWorkChats(workspace.ID)
	if err != nil || result.Created != 1 || len(result.Coordinators) != 1 || result.Coordinators[0].PipelineID != run.ID {
		t.Fatalf("a CLI default with no model chosen should still give the work a chat: %+v %v", result, err)
	}
}
