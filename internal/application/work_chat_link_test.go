package application

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
)

func TestTheFirstMessageOfANewChatOpensItsWorkAndOnlyOnce(t *testing.T) {
	provider := &fakeProvider{output: "Entendi."}
	s, _, _, session := sessionSetup(t, provider)
	var mu sync.Mutex
	var works []map[string]string
	SetEmitter(s, func(name string, payload any) {
		if name == "harflex:pipeline" {
			mu.Lock()
			works = append(works, payload.(map[string]string))
			mu.Unlock()
		}
	})
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Quero exportar as faturas em CSV\nCom filtro por data."}); err != nil {
		t.Fatal(err)
	}
	pipelines, err := s.ListPipelines(session.WorkspaceID)
	if err != nil || len(pipelines) != 1 {
		t.Fatalf("pipelines: %+v %v", pipelines, err)
	}
	work := pipelines[0]
	if work.Kind != "ai_authoring" || work.CurrentStage != "discovery" || work.Title != "Quero exportar as faturas em CSV" || !strings.Contains(work.Objective, "Com filtro por data.") {
		t.Fatalf("the chat's work: %+v", work)
	}
	if found, err := s.GetPipelineForSession(GetPipelineForSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID}); err != nil || found.ID != work.ID {
		t.Fatalf("the chat does not coordinate its work: %+v %v", found, err)
	}
	mu.Lock()
	announced := len(works) == 1 && works[0]["pipelineId"] == work.ID && works[0]["sessionId"] == session.ID && works[0]["reason"] == "chat"
	mu.Unlock()
	if !announced {
		t.Fatalf("the work was not announced with its chat: %+v", works)
	}
	// The conversation learns at once which work it coordinates.
	if !strings.Contains(provider.request.Messages[0].Content, work.ID) {
		t.Fatal("the first turn did not know its work")
	}
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "E também PDF"}); err != nil {
		t.Fatal(err)
	}
	if pipelines, _ = s.ListPipelines(session.WorkspaceID); len(pipelines) != 1 {
		t.Fatalf("a second message opened another work: %d", len(pipelines))
	}
}

func TestAContinuedChatOpensItsWorkWithTheRequestOnly(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{output: "Ok."})
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Ajuste o rodapé" + continuationMarker + " (referência)\nConversa antiga com segredos de contexto"}); err != nil {
		t.Fatal(err)
	}
	pipelines, _ := s.ListPipelines(session.WorkspaceID)
	if len(pipelines) != 1 || strings.Contains(pipelines[0].Objective, "Conversa antiga") || pipelines[0].Title != "Ajuste o rodapé" {
		t.Fatalf("the carried context went into the Discovery: %+v", pipelines)
	}
}

func TestEveryWorkCreatedOutsideAChatGetsItsChat(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: session.BackendID}); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: session.WorkspaceID, RequestID: randomRequestID(), Discovery: "# Relatório mensal\n\nGerar PDF."})
	if err != nil {
		t.Fatal(err)
	}
	coordinators, err := s.ListWorkCoordinators(session.WorkspaceID)
	if err != nil || len(coordinators) != 1 || coordinators[0].PipelineID != created.ID || coordinators[0].SessionID == "" {
		t.Fatalf("the work has no chat: %+v %v", coordinators, err)
	}
	// That chat is the work's: its first message does not open a second work.
	if _, err := s.Prompt(PromptInput{SessionID: coordinators[0].SessionID, Text: "Onde paramos?"}); err != nil {
		t.Fatal(err)
	}
	if pipelines, _ := s.ListPipelines(session.WorkspaceID); len(pipelines) != 1 {
		t.Fatalf("the work's chat opened another work: %d", len(pipelines))
	}
}

func TestTheAgentWritesTheDiscoveryOfItsChatsWorkInsteadOfOpeningAnother(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{output: "Vamos lá."})
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Quero um relatório"}); err != nil {
		t.Fatal(err)
	}
	pipelines, _ := s.ListPipelines(session.WorkspaceID)
	if len(pipelines) != 1 {
		t.Fatalf("pipelines: %d", len(pipelines))
	}
	tool := harflexToolNamed(t, s.harflexTools(session.WorkspaceID, session.ID), harflexCreatePipelineTool)
	result, err := tool.Execute(t.Context(), json.RawMessage(`{"discovery":"# Relatório mensal em PDF\n\nO financeiro precisa do relatório mensal de faturas em PDF, com totais por cliente."}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Content), `"updated":true`) {
		t.Fatalf("result: %s", result.Content)
	}
	after, _ := s.ListPipelines(session.WorkspaceID)
	if len(after) != 1 || after[0].ID != pipelines[0].ID || after[0].Title != "Relatório mensal em PDF" || !strings.Contains(after[0].Objective, "totais por cliente") {
		t.Fatalf("the chat's work was not updated: %+v", after)
	}
}

func TestAWorkPastDiscoveryAsksForANewChat(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{output: "Ok."})
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: session.WorkspaceID, Title: "Exportar", Objective: "CSV"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: "discovery", Content: "Exportar CSV"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvancePipeline(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetPipelineCoordinator(s.ctx, run.ID, session.ID); err != nil {
		t.Fatal(err)
	}
	tool := harflexToolNamed(t, s.harflexTools(session.WorkspaceID, session.ID), harflexCreatePipelineTool)
	_, err = tool.Execute(t.Context(), json.RawMessage(`{"discovery":"# Outra coisa\n\nUm trabalho completamente diferente do primeiro."}`), nil)
	var failure *agentcore.ToolFailure
	if !errors.As(err, &failure) || failure.Code != "work_past_discovery" || !strings.Contains(failure.Message, "new chat") {
		t.Fatalf("a work past Discovery took another Discovery: %v", err)
	}
	if pipelines, _ := s.ListPipelines(session.WorkspaceID); len(pipelines) != 1 {
		t.Fatalf("another work was opened from the same chat: %d", len(pipelines))
	}
}
