package application

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

// codeConversationFixture is a pipeline at its Code stage with the Coder's conversation open in an isolated copy.
func codeConversationFixture(t *testing.T) (*Service, PipelineDTO, PipelineSessionDTO, WorkspaceDTO, string) {
	t.Helper()
	s, db, _ := setup(t)
	provider := &fakeProvider{}
	s.providerFactory = func(c openai.Config) (agentcore.Provider, error) { provider.key = c.APIKey; return provider, nil }
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "TODO", Objective: "Criar um TODO com evidência revisável"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"discovery", "spec", "plan"} {
		if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: stage, Content: "Requisitos e evidências do TODO"}); err != nil {
			t.Fatal(err)
		}
		if run, err = s.AdvancePipeline(run.ID); err != nil {
			t.Fatal(err)
		}
	}
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	links, err := db.ListPipelineSessions(t.Context(), run.ID, "coder")
	if err != nil || len(links) != 1 {
		t.Fatalf("read Coder link: %+v %v", links, err)
	}
	snapshot, err := s.executionSnapshotForLink(links[0])
	if err != nil {
		t.Fatal(err)
	}
	return s, run, coder, workspace, snapshot.Root
}

// The Coder's conversation is not locked when its run ends: the person can keep refining the Code, in the same
// conversation, until they verify it. Verifying is what closes it.
func TestACodeConversationStaysOpenAfterARunUntilTheCodeIsVerified(t *testing.T) {
	s, run, coder, workspace, root := codeConversationFixture(t)
	if !coder.Session.Resumable {
		t.Fatalf("a fresh Coder conversation must accept its first message: %+v", coder.Session)
	}
	first, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt})
	if err != nil || first.Status != RunCompleted {
		t.Fatalf("first run: %+v %v", first, err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>TODO</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	open, err := s.OpenSession(OpenSessionInput{SessionID: coder.Session.ID, WorkspaceID: workspace.ID})
	if err != nil || !open.Resumable {
		t.Fatalf("a Coder conversation whose run ended is locked: %+v %v", open, err)
	}
	second, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "Ajuste o título para ficar em maiúsculas."})
	if err != nil || second.Status != RunCompleted {
		t.Fatalf("a follow-up in the Coder conversation was refused: %+v %v", second, err)
	}

	verified, err := s.CompletePipelineCode(run.ID)
	if err != nil || verified.StageStatus["code"] != "waiting_user" {
		t.Fatalf("verifying the Code: %+v %v", verified, err)
	}
	closed, err := s.OpenSession(OpenSessionInput{SessionID: coder.Session.ID, WorkspaceID: workspace.ID})
	if err != nil || closed.Resumable {
		t.Fatalf("a verified Code conversation must close: %+v %v", closed, err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "Mais uma mudança"}); ErrorCode(err) != "pipeline_code_session_closed" {
		t.Fatalf("a verified Code conversation took another message: %v", err)
	}
}

// The app restarting between the run and the follow-up changes nothing: the new process lists the Coder's conversation as
// open, takes the follow-up, and verifying afterwards still works.
func TestACodeConversationTakesAFollowUpAfterTheAppRestarts(t *testing.T) {
	s, run, coder, workspace, root := codeConversationFixture(t)
	if first, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil || first.Status != RunCompleted {
		t.Fatalf("first run: %+v %v", first, err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>TODO</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(t.Context(), Dependencies{Store: s.store, Secrets: s.secrets, External: map[string]ExternalBackend{}, ProviderFactory: s.providerFactory, ExecutionCacheRoot: s.executionCacheRoot})
	open, err := restarted.OpenSession(OpenSessionInput{SessionID: coder.Session.ID, WorkspaceID: workspace.ID})
	if err != nil || !open.Resumable {
		t.Fatalf("the Coder conversation did not survive a restart as open: %+v %v", open, err)
	}
	if second, err := restarted.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "Ajuste o título para ficar em maiúsculas."}); err != nil || second.Status != RunCompleted {
		t.Fatalf("a follow-up after the restart was refused: %+v %v", second, err)
	}
	verified, err := restarted.CompletePipelineCode(run.ID)
	if err != nil || verified.StageStatus["code"] != "waiting_user" {
		t.Fatalf("verifying after the restart: %+v %v", verified, err)
	}
}

// QA is one verdict: a conversation that has not run can start it (the screen has to be able to send the QA
// request), and one that has run is not continued, so the verdict stays the last thing it said.
func TestAQAConversationCanStartButNotBeContinuedAfterItsVerdict(t *testing.T) {
	s, run, coder, workspace, root := codeConversationFixture(t)
	if first, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil || first.Status != RunCompleted {
		t.Fatalf("Coder run: %+v %v", first, err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>TODO</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.CompletePipelineCode(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	approved := decidePipelineStageReviewForTest(t, s, waiting, "code", "approve", "")
	qa, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: approved.ID, BackendID: "local", Role: "evaluator"})
	if err != nil {
		t.Fatal(err)
	}
	if !qa.Session.Resumable {
		t.Fatalf("a fresh QA conversation reports it cannot start, so the screen would never send its request: %+v", qa.Session)
	}
	if result, err := s.Prompt(PromptInput{SessionID: qa.Session.ID, Text: qa.Prompt}); err != nil || result.Status != RunCompleted {
		t.Fatalf("QA run: %+v %v", result, err)
	}
	finished, err := s.OpenSession(OpenSessionInput{SessionID: qa.Session.ID, WorkspaceID: workspace.ID})
	if err != nil || finished.Resumable {
		t.Fatalf("a QA conversation that gave its verdict must not be continued: %+v %v", finished, err)
	}
}
