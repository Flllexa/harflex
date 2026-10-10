package application

import (
	"errors"
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
	if !strings.Contains(note, "Exportar faturas") || !strings.Contains(note, "harflex_get_pipeline") || !strings.Contains(note, run.ID) {
		t.Fatalf("the note does not name the work or how to read it: %s", note)
	}
	// It carries every phase and where the work stopped, so the chat can answer without searching the project.
	for _, want := range []string{"Discovery: ", "SPEC: ", "Plan: ", "Code: ", "QA: ", "PRs: ", "<- current phase", "Where it stands: it stopped at"} {
		if !strings.Contains(note, want) {
			t.Fatalf("the note lacks %q: %s", want, note)
		}
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

func TestWorkChatMovesToTheConversationThatContinuesIt(t *testing.T) {
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
	other, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar faturas", Objective: "CSV"})
	if err != nil {
		t.Fatal(err)
	}
	newChat := func(workspaceID string) SessionDTO {
		chat, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspaceID, BackendID: "local", Reason: "Conversa"})
		if err != nil {
			t.Fatal(err)
		}
		return chat
	}
	first, second, loose, elsewhere := newChat(workspace.ID), newChat(workspace.ID), newChat(workspace.ID), newChat(other.ID)
	if err := s.store.SetPipelineCoordinator(s.ctx, run.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	// A loose chat does not take over a work, and a conversation of another project cannot.
	if moved, err := s.ContinueWorkChat(loose.ID, second.ID); err != nil || moved {
		t.Fatalf("a chat that coordinates nothing moved something: %v %v", moved, err)
	}
	if _, err := s.ContinueWorkChat(first.ID, elsewhere.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("another project's conversation took the work: %v", err)
	}
	if _, err := s.ContinueWorkChat(first.ID, first.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("moving a chat onto itself: %v", err)
	}
	moved, err := s.ContinueWorkChat(first.ID, second.ID)
	if err != nil || !moved {
		t.Fatalf("continuing the work chat: %v %v", moved, err)
	}
	list, err := s.ListWorkCoordinators(workspace.ID)
	if err != nil || len(list) != 1 || list[0].SessionID != second.ID || len(list[0].PreviousSessionIDs) != 1 || list[0].PreviousSessionIDs[0] != first.ID {
		t.Fatalf("the work must point at the new chat and remember the old one: %+v %v", list, err)
	}
	if note := s.coordinatorInstructions(second.ID); !strings.Contains(note, run.ID) {
		t.Fatalf("the new chat does not know the work: %s", note)
	}
	if note := s.coordinatorInstructions(first.ID); note != "" {
		t.Fatalf("the old chat still coordinates: %s", note)
	}
	// Moving again from the old chat changes nothing: it is no longer the work's chat.
	if again, err := s.ContinueWorkChat(first.ID, loose.ID); err != nil || again {
		t.Fatalf("a replaced chat took the work back: %v %v", again, err)
	}
	third := newChat(workspace.ID)
	if moved, err := s.ContinueWorkChat(second.ID, third.ID); err != nil || !moved {
		t.Fatalf("a second move: %v %v", moved, err)
	}
	if list, _ = s.ListWorkCoordinators(workspace.ID); len(list[0].PreviousSessionIDs) != 2 {
		t.Fatalf("the history keeps every earlier chat: %+v", list)
	}
}
