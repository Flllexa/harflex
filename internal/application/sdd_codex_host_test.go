package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

type hostCodexStub struct {
	cliCatalogStub
	requests []externalagent.DocumentRequest
	answers  []string
}

func (*hostCodexStub) DocumentContractSupported(externalagent.Detection) bool { return true }

type unprovenDocumentCLI struct{ cliCatalogStub }

func (*unprovenDocumentCLI) GenerateDocument(context.Context, externalagent.DocumentRequest, func(externalagent.Event)) (externalagent.DocumentResult, error) {
	return externalagent.DocumentResult{}, externalagent.ErrCodexDocumentContractUnavailable
}

func TestCodexHostRejectsDocumentGeneratorWithoutExplicitContractCapability(t *testing.T) {
	s, _, run, proven, _ := codexHostCodeFixture(t, "trusted_workspace")
	s.external["codex"] = &unprovenDocumentCLI{cliCatalogStub: proven.cliCatalogStub}
	for _, backend := range s.ListBackends() {
		if backend.ID == "codex" && backend.ProfessionalAvailable {
			t.Fatal("unproven document contract was advertised")
		}
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "codex", Role: "coder", ConfirmWorkspaceCopy: true}); err == nil {
		t.Fatal("unproven document contract admitted Code")
	}
}

func (stub *hostCodexStub) GenerateDocument(_ context.Context, request externalagent.DocumentRequest, _ func(externalagent.Event)) (externalagent.DocumentResult, error) {
	index := len(stub.requests)
	stub.requests = append(stub.requests, request)
	return externalagent.DocumentResult{Text: stub.answers[index], UsageKnown: true}, nil
}

func codexHostCodeFixture(t *testing.T, profile string, answers ...string) (*Service, *sqlite.Store, PipelineDTO, *hostCodexStub, string) {
	t.Helper()
	return hostCodeFixture(t, "codex", profile, answers...)
}

// hostCodeFixture prepares a pipeline at Code whose phases run on a CLI that works as a model only.
func hostCodeFixture(t *testing.T, backendID, profile string, answers ...string) (*Service, *sqlite.Store, PipelineDTO, *hostCodexStub, string) {
	t.Helper()
	s, db, _ := setup(t)
	s.executionCacheRoot = t.TempDir()
	root := t.TempDir()
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: profile}); err != nil {
		t.Fatal(err)
	}
	stub := &hostCodexStub{cliCatalogStub: cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: backendID, version: "0.157.0", efforts: []string{"low"}}, answers: answers}
	s.external[backendID] = stub
	if err := db.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: backendID, DefaultModelBackendID: backendID, DefaultModelID: "provider/model-exact"}); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "TODO", Objective: "Persistir em localStorage"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: "discovery", Content: "As tarefas persistem em localStorage."}); err != nil {
		t.Fatal(err)
	}
	if run, err = s.AdvancePipeline(run.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID}); err != nil {
			t.Fatal(err)
		}
	}
	return s, db, run, stub, root
}

func TestHostCLICompletesCodeEvalAndApplyThroughGuardedTools(t *testing.T) {
	for _, backendID := range []string{"codex", "claude"} {
		t.Run(backendID, func(t *testing.T) { hostCLICompletesCodeEvalAndApply(t, backendID) })
	}
}

func hostCLICompletesCodeEvalAndApply(t *testing.T, backendID string) {
	codeText := "<html><script>localStorage.setItem('todo','[]')</script></html>\n"
	write, _ := json.Marshal(map[string]any{"reply": "Vou criar o HTML.", "toolCalls": []any{map[string]any{"name": "write", "arguments": map[string]string{"path": "index.html", "content": codeText}}}})
	evaluation, _ := json.Marshal(map[string]any{"reply": `{"passed":true,"findings":[],"criteria":[{"criterion":"localStorage","evidence":"localStorage"}]}`, "toolCalls": []any{}})
	s, db, run, stub, root := hostCodeFixture(t, backendID, "trusted_workspace", string(write), `{"reply":"HTML criado.","toolCalls":[]}`, string(evaluation))
	for _, backend := range s.ListBackends() {
		if backend.ID == backendID && !backend.ProfessionalAvailable {
			t.Fatal("safe CLI is absent from Professional backends")
		}
	}
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: backendID, Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil || result.Status != "completed" {
		t.Fatalf("Code=%+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); !os.IsNotExist(err) {
		t.Fatalf("Code changed source before approval: %v", err)
	}
	links, err := db.ListPipelineSessions(t.Context(), run.ID, "coder")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.executionSnapshotForLink(links[0])
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(snapshot.Root, "index.html")); err != nil || string(content) != codeText {
		t.Fatalf("isolated HTML=%q %v", content, err)
	}
	if run, err = s.CompletePipelineCode(run.ID); err != nil {
		t.Fatal(err)
	}
	run = decidePipelineStageReviewForTest(t, s, run, "code", "approve", "")
	evaluator, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: backendID, Role: "evaluator"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: evaluator.Session.ID, Text: evaluator.Prompt}); err != nil || result.Status != "completed" {
		t.Fatalf("Eval=%+v %v", result, err)
	}
	if run, err = s.CompletePipelineEvaluation(run.ID); err != nil {
		t.Fatal(err)
	}
	run = decidePipelineStageReviewForTest(t, s, run, "eval", "approve", "")
	if run, err = s.ApplyPipelineCode(run.ID); err != nil || run.CodeAppliedAt == nil {
		t.Fatalf("Apply=%+v %v", run, err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "index.html")); err != nil || string(content) != codeText {
		t.Fatalf("applied HTML=%q %v", content, err)
	}
	if len(stub.runs) != 0 || len(stub.requests) != 3 {
		t.Fatalf("unsafe CLI runs=%d document calls=%d", len(stub.runs), len(stub.requests))
	}
	for _, request := range stub.requests {
		if request.CWD == root || request.CWD == snapshot.Root || request.Model != "provider/model-exact" || len(request.OutputSchema) == 0 {
			t.Fatalf("host tools leaked their roots to the CLI runner: %+v", request)
		}
	}
	if strings.Contains(stub.requests[2].Prompt, `"name":"write"`) {
		t.Fatal("Eval exposed write tools")
	}
}

func TestCodexHostDeniesPathEscapeAndKeepsSourceUnchanged(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "protected.txt")
	if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	answer, _ := json.Marshal(map[string]any{"reply": "", "toolCalls": []any{map[string]any{"name": "write", "arguments": map[string]string{"path": outside, "content": "changed"}}}})
	s, _, run, stub, _ := codexHostCodeFixture(t, "trusted_workspace", string(answer))
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "codex", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt})
	if err != nil || result.Status != "failed" {
		t.Fatalf("path escape=%+v %v", result, err)
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "unchanged" {
		t.Fatalf("outside file=%q %v", content, err)
	}
	if len(stub.requests) != 1 || len(stub.runs) != 0 {
		t.Fatal("path escape continued inference or direct CLI")
	}
}

func TestCodexHostWritesInThePrivateCopyWithoutAskingAndNeverTouchesTheProject(t *testing.T) {
	s, _, run, _, root := codexHostCodeFixture(t, "ask", `{"reply":"Vou escrever o arquivo.","toolCalls":[{"name":"write","arguments":{"path":"index.html","content":"localStorage"}}]}`, `{"reply":"Arquivo pronto.","toolCalls":[]}`)
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "codex", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	// The private copy needs no per-write approval, even when the project asks for each action.
	result, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt})
	if err != nil || result.Status != "completed" || result.Approval != nil {
		t.Fatalf("private copy write=%+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); !os.IsNotExist(err) {
		t.Fatal("the Coder wrote to the project folder")
	}
}
