package application

import (
	"path/filepath"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestWorkflowExecutesPersistedStepsAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.db")
	db, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	vault := &memorySecrets{values: map[secrets.Reference]string{}}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return &fakeProvider{}, nil }})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspace.ID, Name: "Revisar e documentar", Steps: []WorkflowStepInput{{Name: "Revisar", Prompt: "Leia o código"}, {Name: "Documentar", Prompt: "Atualize a documentação"}}})
	if err != nil || len(definition.Steps) != 2 {
		t.Fatalf("save workflow: %+v %v", definition, err)
	}
	run, err := s.StartWorkflow(StartWorkflowInput{WorkflowID: definition.ID, BackendID: "local"})
	if err != nil || run.Status != "ready" || run.CurrentStep != 0 {
		t.Fatalf("start run: %+v %v", run, err)
	}
	run, err = s.RunWorkflowStep(run.ID)
	if err != nil || run.Status != "ready" || run.CurrentStep != 1 || run.LastSessionID == "" {
		t.Fatalf("first step: %+v %v", run, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s = NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return &fakeProvider{}, nil }})
	loaded, err := s.GetWorkflowRun(run.ID)
	if err != nil || loaded.CurrentStep != 1 || loaded.Status != "ready" {
		t.Fatalf("reopened run: %+v %v", loaded, err)
	}
	run, err = s.RunWorkflowStep(run.ID)
	if err != nil || run.Status != "completed" || run.CurrentStep != 2 {
		t.Fatalf("final step: %+v %v", run, err)
	}
	events, err := db.ListAfter(t.Context(), run.ID, 0)
	if err != nil || len(events) < 5 || events[len(events)-1].Type != "workflow.completed" {
		t.Fatalf("workflow journal: %+v %v", events, err)
	}
}
