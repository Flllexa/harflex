package application

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

// localTestPipeline is a Git project whose pipeline waits on the Code review: the Coder changed README.md and added
// novo.go in its copy; the project's folder is untouched.
func localTestPipeline(t *testing.T) (*Service, *sqlite.Store, PipelineDTO, WorkspaceDTO, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	for name, content := range map[string]string{"README.md": "baseline\n", ".gitignore": ".env\nnode_modules/\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "initial"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	s, db, _ := setup(t)
	provider := &fakeProvider{}
	s.providerFactory = func(c openai.Config) (agentcore.Provider, error) { provider.key = c.APIKey; return provider, nil }
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	run := localTestPipelineIn(t, s, db, workspace, "Criar código")
	return s, db, run, workspace, root
}

func localTestPipelineIn(t *testing.T, s *Service, db *sqlite.Store, workspace WorkspaceDTO, title string) PipelineDTO {
	t.Helper()
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: title, Objective: "Criar novo.go"})
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID}); err != nil {
			t.Fatal(err)
		}
	}
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder"})
	if err != nil {
		t.Fatal(err)
	}
	coderWrites(t, s, db, run.ID, coder.Session.ID, "tracked change\n")
	if run, err = coderSaidDone(t, s, run.ID).CompletePipelineCode(run.ID); err != nil || run.StageStatus["code"] != "waiting_user" {
		t.Fatalf("Code review: %+v %v", run, err)
	}
	return run
}

func coderWrites(t *testing.T, s *Service, db *sqlite.Store, pipelineID, sessionID, readme string) {
	t.Helper()
	link, err := s.latestPipelineSession(pipelineID, "coder")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.executionSnapshotForLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execution.Root, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execution.Root, "novo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Append(t.Context(), sessionID, "agent_session", "run.completed", map[string]string{"reason": ""}); err != nil {
		t.Fatal(err)
	}
}

func startLocalTest(t *testing.T, s *Service, run PipelineDTO) PipelineSessionDTO {
	t.Helper()
	tester, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: testerRole})
	if err != nil {
		t.Fatalf("start the local test: %v", err)
	}
	return tester
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return "<absent>"
	}
	return string(data)
}

func TestLocalTestAppliesTheCodeAndStartsTheTester(t *testing.T) {
	s, db, run, _, root := localTestPipeline(t)
	tester := startLocalTest(t, s, run)
	if record, err := db.GetSession(t.Context(), tester.Session.ID); err != nil || record.Mode != localTestMode {
		t.Fatalf("tester session: %+v %v", record, err)
	}
	if fileText(t, filepath.Join(root, "README.md")) != "tracked change\n" || fileText(t, filepath.Join(root, "novo.go")) != "package main\n" {
		t.Fatal("the Code was not applied to the project's folder")
	}
	if !strings.Contains(tester.Prompt, "harflex-local") || tester.Role != testerRole {
		t.Fatalf("tester prompt: %+v", tester)
	}
	test, err := s.GetPipelineLocalTest(run.ID)
	if err != nil || test.Status != "applied" || test.ArtifactVersion != run.Artifacts["code"].Version || test.SessionID != tester.Session.ID {
		t.Fatalf("local test: %+v %v", test, err)
	}
}

func TestUndoRestoresTheFolderAndEndsTheTest(t *testing.T) {
	s, _, run, _, root := localTestPipeline(t)
	startLocalTest(t, s, run)
	test, err := s.UndoPipelineLocalTest(UndoPipelineLocalTestInput{PipelineID: run.ID})
	if err != nil || test.Status != "none" || test.Conflict != nil {
		t.Fatalf("undo: %+v %v", test, err)
	}
	if fileText(t, filepath.Join(root, "README.md")) != "baseline\n" || fileText(t, filepath.Join(root, "novo.go")) != "<absent>" {
		t.Fatal("the folder is not back")
	}
}

func TestASecondLocalTestInTheProjectIsRefused(t *testing.T) {
	s, db, run, workspace, _ := localTestPipeline(t)
	startLocalTest(t, s, run)
	// A second pipeline of the same project gets to its Code review on a copy made before the first test.
	if _, err := s.UndoPipelineLocalTest(UndoPipelineLocalTestInput{PipelineID: run.ID}); err != nil {
		t.Fatal(err)
	}
	other := localTestPipelineIn(t, s, db, workspace, "Outro trabalho")
	startLocalTest(t, s, run)
	_, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: other.ID, BackendID: "local", Role: testerRole})
	if ErrorCode(err) != "local_test_busy" {
		t.Fatalf("a second local test in the project: %v", err)
	}
	if view, err := s.GetPipelineLocalTest(other.ID); err != nil || view.HeldBy != run.Title {
		t.Fatalf("the other pipeline does not say who holds the project: %+v %v", view, err)
	}
}

func TestLocalTestRefusesAFolderWithChanges(t *testing.T) {
	s, _, run, _, root := localTestPipeline(t)
	if err := os.WriteFile(filepath.Join(root, "notas.md"), []byte("minhas\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: testerRole})
	if ErrorCode(err) != "local_test_source_dirty" || !strings.Contains(string(MarshalError(err)), "notas.md") {
		t.Fatalf("a folder with changes: %v %s", err, MarshalError(err))
	}
	if fileText(t, filepath.Join(root, "README.md")) != "baseline\n" {
		t.Fatal("a folder with changes was written to")
	}
}

func TestDecideUndoesTheLocalTestFirst(t *testing.T) {
	s, _, run, _, root := localTestPipeline(t)
	startLocalTest(t, s, run)
	approved := decidePipelineStageReviewForTest(t, s, run, "code", "approve", "")
	if approved.CurrentStage != "eval" || fileText(t, filepath.Join(root, "README.md")) != "baseline\n" {
		t.Fatalf("approve with a local test: stage=%s readme=%q", approved.CurrentStage, fileText(t, filepath.Join(root, "README.md")))
	}
	if test, err := s.GetPipelineLocalTest(run.ID); err != nil || test.Status != "none" {
		t.Fatalf("the test is still applied: %+v %v", test, err)
	}
}

func TestDecideWithAnEditedLocalTestStopsWithTheConflict(t *testing.T) {
	s, _, run, _, root := localTestPipeline(t)
	startLocalTest(t, s, run)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("my edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := run.Artifacts["code"]
	_, err := s.DecidePipelineExecutionArtifact(DecidePipelineExecutionArtifactInput{PipelineID: run.ID, RequestID: "review_code_conflict", Stage: "code",
		ArtifactVersion: artifact.Version, ArtifactDigest: artifact.ContentDigest, Decision: "approve", PipelineRevision: run.Revision})
	if ErrorCode(err) != "local_test_conflict" {
		t.Fatalf("approve over an edit: %v", err)
	}
	if current, err := s.GetPipeline(run.ID); err != nil || current.StageStatus["code"] != "waiting_user" || fileText(t, filepath.Join(root, "README.md")) != "my edit\n" {
		t.Fatalf("the decision went through or the edit was lost: %+v %v", current.StageStatus, err)
	}
	view, err := s.GetPipelineLocalTest(run.ID)
	if err != nil || view.Conflict == nil || len(view.Conflict.Edited) != 1 || view.Conflict.Edited[0] != "README.md" {
		t.Fatalf("the card does not show the conflict: %+v %v", view, err)
	}
	if view, err := s.UndoPipelineLocalTest(UndoPipelineLocalTestInput{PipelineID: run.ID, Discard: true}); err != nil || view.Status != "none" {
		t.Fatalf("discarding undo: %+v %v", view, err)
	}
	decidePipelineStageReviewForTest(t, s, run, "code", "approve", "")
}

func TestLocalTestSurvivesARestartWithoutItsServices(t *testing.T) {
	s, db, run, _, root := localTestPipeline(t)
	tester := startLocalTest(t, s, run)
	if err := db.AddPipelineLocalTestService(t.Context(), localTestIDForTest(t, s, run.ID), catalogService("web", "term-gone")); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(t.Context(), Dependencies{Store: s.store, Secrets: s.secrets, External: map[string]ExternalBackend{}, ProviderFactory: s.providerFactory, ExecutionCacheRoot: s.executionCacheRoot})
	view, err := restarted.GetPipelineLocalTest(run.ID)
	if err != nil || view.Status != "applied" || !view.ServicesLost || view.SessionID != tester.Session.ID {
		t.Fatalf("after a restart: %+v %v", view, err)
	}
	if _, err := restarted.UndoPipelineLocalTest(UndoPipelineLocalTestInput{PipelineID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if fileText(t, filepath.Join(root, "README.md")) != "baseline\n" {
		t.Fatal("undo after a restart did not restore the folder")
	}
}

func TestANewCodeVersionUndoesTheOlderTest(t *testing.T) {
	s, db, run, _, root := localTestPipeline(t)
	startLocalTest(t, s, run)
	link, err := s.latestPipelineSession(run.ID, "coder")
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: link.SessionID, Text: "Mais uma mudança"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("the Coder's follow-up: %+v %v", result, err)
	}
	coderWrites(t, s, db, run.ID, link.SessionID, "second version\n")
	next, err := coderSaidDone(t, s, run.ID).CompletePipelineCode(run.ID)
	if err != nil || next.StageStatus["code"] != "waiting_user" || next.Artifacts["code"].Version <= run.Artifacts["code"].Version {
		t.Fatalf("the new version: %+v %v", next, err)
	}
	if fileText(t, filepath.Join(root, "README.md")) != "baseline\n" {
		t.Fatal("the old version stayed applied under the new one")
	}
	if view, err := s.GetPipelineLocalTest(run.ID); err != nil || view.Status != "none" {
		t.Fatalf("the old test is still applied: %+v %v", view, err)
	}
}

func localTestIDForTest(t *testing.T, s *Service, pipelineID string) string {
	t.Helper()
	test, err := s.store.LatestPipelineLocalTest(t.Context(), pipelineID)
	if err != nil {
		t.Fatal(err)
	}
	return test.ID
}

func catalogService(name, terminalID string) catalog.LocalTestService {
	return catalog.LocalTestService{Name: name, TerminalID: terminalID, Command: "npm run dev"}
}

// The tester is a conversation like any other: it takes its first message, and it reopens after the app restarts.
func TestTheTesterConversationRunsAndReopens(t *testing.T) {
	s, _, run, workspace, _ := localTestPipeline(t)
	tester := startLocalTest(t, s, run)
	if result, err := s.Prompt(PromptInput{SessionID: tester.Session.ID, Text: tester.Prompt}); err != nil || result.Status != RunCompleted {
		t.Fatalf("the tester's first message: %+v %v", result, err)
	}
	restarted := NewService(t.Context(), Dependencies{Store: s.store, Secrets: s.secrets, External: map[string]ExternalBackend{}, ProviderFactory: s.providerFactory, ExecutionCacheRoot: s.executionCacheRoot})
	opened, err := restarted.OpenSession(OpenSessionInput{SessionID: tester.Session.ID, WorkspaceID: workspace.ID})
	if err != nil || !opened.Resumable {
		t.Fatalf("the tester after a restart: %+v %v", opened, err)
	}
	if result, err := restarted.Prompt(PromptInput{SessionID: tester.Session.ID, Text: "Suba os serviços de novo"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("a message to the tester after a restart: %+v %v", result, err)
	}
	if _, err := restarted.UndoPipelineLocalTest(UndoPipelineLocalTestInput{PipelineID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Prompt(PromptInput{SessionID: tester.Session.ID, Text: "mais"}); ErrorCode(err) != "local_test_finished" {
		t.Fatalf("a message after the undo: %v", err)
	}
}

// Over MCP nothing asks before a tool runs, so the Claude Code tester would run commands in the person's folder
// without approval: it is offered only when the project is on Full access.
func TestClaudeTesterNeedsFullAccess(t *testing.T) {
	s, _, run, _, root := localTestPipeline(t)
	s.external["claude"] = fakeExternal{available: true}
	_, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "claude", Role: testerRole})
	if ErrorCode(err) != "local_test_needs_full_access" {
		t.Fatalf("a Claude tester on a project without Full access: %v", err)
	}
	if fileText(t, filepath.Join(root, "README.md")) != "baseline\n" {
		t.Fatal("the refused test still applied the Code")
	}
}

// Talking to the Coder starts a new version: the test of the old one is undone first, and changes made during it
// stop the message so the card can ask.
func TestTalkingToTheCoderEndsTheTestFirst(t *testing.T) {
	s, _, run, _, root := localTestPipeline(t)
	startLocalTest(t, s, run)
	link, err := s.latestPipelineSession(run.ID, "coder")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("my edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: link.SessionID, Text: "Mais uma mudança"}); ErrorCode(err) != "local_test_conflict" {
		t.Fatalf("a message to the Coder over a changed test: %v", err)
	}
	if current, err := s.GetPipeline(run.ID); err != nil || current.StageStatus["code"] != "waiting_user" {
		t.Fatalf("the Code left its review while the test stays: %+v %v", current.StageStatus, err)
	}
	if _, err := s.UndoPipelineLocalTest(UndoPipelineLocalTestInput{PipelineID: run.ID, Discard: true}); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: link.SessionID, Text: "Mais uma mudança"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("the Coder after the undo: %+v %v", result, err)
	}
}

// While one pipeline's test is applied, another pipeline of the project does not copy the folder: the copy would
// take the untested change in as its starting point.
func TestAnotherPipelineCannotCopyAHeldProject(t *testing.T) {
	s, _, run, workspace, _ := localTestPipeline(t)
	other, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Outro", Objective: "Outra coisa"})
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if other, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: other.ID}); err != nil {
			t.Fatal(err)
		}
	}
	startLocalTest(t, s, run)
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: other.ID, BackendID: "local", Role: "coder"}); ErrorCode(err) != "local_test_busy" {
		t.Fatalf("a Coder copy of a project under test: %v", err)
	}
}

// Two undos at once (Desfazer, then Aprovar before it returned) never lose an original.
func TestConcurrentUndosNeverLoseAFile(t *testing.T) {
	s, _, run, _, root := localTestPipeline(t)
	startLocalTest(t, s, run)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := s.UndoPipelineLocalTest(UndoPipelineLocalTestInput{PipelineID: run.ID})
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("an undo failed: %v", err)
		}
	}
	if fileText(t, filepath.Join(root, "README.md")) != "baseline\n" || fileText(t, filepath.Join(root, "novo.go")) != "<absent>" {
		t.Fatal("the folder is not back after two undos at once")
	}
}
