package application

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

type memoryCLIStub struct {
	cliCatalogStub
	requests []externalagent.DocumentRequest
}

func (*memoryCLIStub) DocumentContractSupported(externalagent.Detection) bool { return true }

func (stub *memoryCLIStub) GenerateDocument(_ context.Context, request externalagent.DocumentRequest, _ func(externalagent.Event)) (externalagent.DocumentResult, error) {
	stub.requests = append(stub.requests, request)
	return externalagent.DocumentResult{Text: `{"document":"## Visão geral\nCobrança.\n\n## Tecnologias\n- Go 1.26","reply":"Li o projeto."}`, ThreadID: "memory-thread", UsageKnown: true}, nil
}

func readingService(t *testing.T) (*Service, *sqlite.Store, *memoryCLIStub) {
	t.Helper()
	db, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	stub := &memoryCLIStub{cliCatalogStub: cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "claude", version: "2.1.286 (Claude Code)", efforts: []string{"low"}}}
	s := NewService(t.Context(), Dependencies{Store: db, External: map[string]ExternalBackend{"claude": stub}, ExecutionCacheRoot: t.TempDir()})
	return s, db, stub
}

func waitForMemory(t *testing.T, s *Service, workspaceID string) ProjectMemoryDTO {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		memory, err := s.GetProjectMemory(workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if memory.Status != "" && memory.Status != "reading" {
			return memory
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the project memory never finished reading")
	return ProjectMemoryDTO{}
}

func TestAProjectIsReadIntoMemoryOnlyWhenThePersonAsks(t *testing.T) {
	s, db, stub := readingService(t)
	if err := db.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: "claude", DefaultModelBackendID: "claude", DefaultModelID: "provider/model-exact"}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Cobrança\nServiço de boletos em Go."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=segredo"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	// Adding the project only asks; nothing is read until the person says yes.
	if memory, err := s.GetProjectMemory(workspace.ID); err != nil || memory.Status != "" || len(stub.requests) != 0 {
		t.Fatalf("adding a project must not read it: %+v %v (%d calls)", memory, err, len(stub.requests))
	}
	if _, err := s.RefreshProjectMemory(workspace.ID); err != nil {
		t.Fatal(err)
	}
	memory := waitForMemory(t, s, workspace.ID)
	if memory.Status != "ready" || !strings.Contains(memory.Content, "## Tecnologias") || !slices.Equal(memory.Sources, []string{"README.md"}) || memory.BackendID != "claude" || memory.Edited {
		t.Fatalf("memory = %+v", memory)
	}
	if len(stub.requests) != 1 || !strings.Contains(stub.requests[0].Prompt, "Serviço de boletos em Go.") || strings.Contains(stub.requests[0].Prompt, "segredo") ||
		!strings.Contains(stub.requests[0].SystemPrompt, "## Repositórios") || stub.requests[0].CWD == root {
		t.Fatalf("the read must carry the documents and no secrets: %+v", stub.requests)
	}
	// Opening the same project again does not read it again.
	if _, err := s.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if len(stub.requests) != 1 {
		t.Fatalf("reopening read the project again: %d calls", len(stub.requests))
	}

	edited, err := s.SaveProjectMemory(SaveProjectMemoryInput{WorkspaceID: workspace.ID, Content: "## Visão geral\nCobrança e conciliação."})
	if err != nil || !edited.Edited || edited.Status != "ready" || edited.Content != "## Visão geral\nCobrança e conciliação." {
		t.Fatalf("edit = %+v, %v", edited, err)
	}
	if got := s.projectMemoryContext(t.Context(), workspace.ID); got != edited.Content {
		t.Fatalf("the phases must read the edited memory, got %q", got)
	}
	if _, err := s.SaveProjectMemory(SaveProjectMemoryInput{WorkspaceID: workspace.ID, Content: strings.Repeat("x", catalog.MaxProjectMemoryBytes+1)}); err == nil {
		t.Fatal("an oversized memory must be refused")
	}
}

func TestProjectMemoryWaitsForAModelAndKeepsWhatItKnewOnFailure(t *testing.T) {
	s, db, _ := readingService(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshProjectMemory(workspace.ID); err != nil {
		t.Fatal(err)
	}
	if memory := waitForMemory(t, s, workspace.ID); memory.Status != "needs_model" {
		t.Fatalf("with no AI chosen the read waits for one, got %+v", memory)
	}
	if err := db.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: "claude", DefaultModelBackendID: "claude", DefaultModelID: "provider/model-exact"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshProjectMemory(workspace.ID); err != nil {
		t.Fatal(err)
	}
	if memory := waitForMemory(t, s, workspace.ID); memory.Status != "ready" {
		t.Fatalf("refresh = %+v", memory)
	}
	// A later failed read (the model left the catalog) keeps the content it had.
	if err := db.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: "claude", DefaultModelBackendID: "claude", DefaultModelID: "gone-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshProjectMemory(workspace.ID); err != nil {
		t.Fatal(err)
	}
	memory := waitForMemory(t, s, workspace.ID)
	if memory.Status != "ready" || memory.ErrorCode == "" || !strings.Contains(memory.Content, "## Visão geral") {
		t.Fatalf("a failed read must keep the memory and say why: %+v", memory)
	}
}

func TestDocumentPhasesReceiveTheProjectMemory(t *testing.T) {
	prompt, err := pipelineDesignPrompt(sdd.Spec, "Prepare a SPEC.", map[sdd.Stage]catalog.PipelineDesignDocument{}, nil, "## Tecnologias\n- Java 21")
	if err != nil || !strings.Contains(prompt, `"projectMemory":"## Tecnologias\n- Java 21"`) {
		t.Fatalf("prompt = %s, %v", prompt, err)
	}
	if !strings.Contains(designSystemPrompt, "projectMemory") {
		t.Fatal("the phases must be told what projectMemory is")
	}
	if prompt, _ := pipelineDesignPrompt(sdd.Spec, "x", map[sdd.Stage]catalog.PipelineDesignDocument{}, nil, ""); strings.Contains(prompt, "projectMemory") {
		t.Fatal("no memory, no field")
	}
}

func TestConversationsStartWithTheProjectMemory(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveProjectMemory(t.Context(), catalog.ProjectMemory{WorkspaceID: workspace.ID, Status: "ready", Content: "## Domínios\n- `baixas`: write-off de contratos.", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0", efforts: []string{"low"}, responses: []string{"ok"}}
	s.external[stub.id] = stub
	listed, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: stub.id})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: stub.id, ModelID: "provider/model-exact", CatalogRevision: listed.ProfileRevision})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot, err := db.GetSessionSkillSnapshot(t.Context(), session.ID); err != nil || !strings.Contains(snapshot, "`baixas`: write-off de contratos.") {
		t.Fatalf("the conversation must keep the memory it started with: %q %v", snapshot, err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "o que as baixas fazem?"}); err != nil {
		t.Fatal(err)
	}
	if len(stub.runs) != 1 || !strings.Contains(stub.runs[0].SystemPrompt, "Memória do projeto") || !strings.Contains(stub.runs[0].SystemPrompt, "write-off de contratos") {
		t.Fatalf("the CLI must receive the memory: %+v", stub.runs)
	}
	if got := projectMemoryInstructions("  "); got != "" {
		t.Fatalf("no memory, no instructions: %q", got)
	}
}

func TestDecliningTheReadIsRememberedAndKeepsAnExistingMemory(t *testing.T) {
	s, db, stub := readingService(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	declined, err := s.DeclineProjectMemory(workspace.ID)
	if err != nil || declined.Status != "declined" || len(stub.requests) != 0 {
		t.Fatalf("decline = %+v, %v", declined, err)
	}
	if memory, _ := s.GetProjectMemory(workspace.ID); memory.Status != "declined" {
		t.Fatalf("the answer must be kept: %+v", memory)
	}
	// Changing their mind later reads it normally.
	if err := db.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: "claude", DefaultModelBackendID: "claude", DefaultModelID: "provider/model-exact"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshProjectMemory(workspace.ID); err != nil {
		t.Fatal(err)
	}
	if memory := waitForMemory(t, s, workspace.ID); memory.Status != "ready" {
		t.Fatalf("read after declining = %+v", memory)
	}
	if kept, err := s.DeclineProjectMemory(workspace.ID); err != nil || kept.Status != "ready" {
		t.Fatalf("declining must not throw away a memory: %+v %v", kept, err)
	}
}
