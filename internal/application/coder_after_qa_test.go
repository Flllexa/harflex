package application

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"

	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func codeVerifiedAndApproved(t *testing.T) (*Service, PipelineDTO, PipelineSessionDTO, WorkspaceDTO, string) {
	t.Helper()
	s, run, coder, workspace, root := codeConversationFixture(t)
	if first, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil || first.Status != RunCompleted {
		t.Fatalf("Coder run: %+v %v", first, err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>TODO</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waiting, err := coderSaidDone(t, s, run.ID).CompletePipelineCode(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, decidePipelineStageReviewForTest(t, s, waiting, "code", "approve", ""), coder, workspace, root
}

func TestCoderMessageAfterQAReturnsThePipelineToCode(t *testing.T) {
	s, approved, coder, workspace, root := codeVerifiedAndApproved(t)
	if approved.CurrentStage != "eval" {
		t.Fatalf("setup: the pipeline should be at QA, got %q", approved.CurrentStage)
	}
	opened, err := s.OpenSession(OpenSessionInput{SessionID: coder.Session.ID, WorkspaceID: workspace.ID})
	if err != nil || !opened.Resumable {
		t.Fatalf("the Coder conversation closed once QA started: %+v %v", opened, err)
	}
	result, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "Troque o título para Tarefas"})
	if err != nil || result.Status != RunCompleted {
		t.Fatalf("message to the Coder after QA: %+v %v", result, err)
	}
	back, err := s.GetPipeline(approved.ID)
	if err != nil || back.CurrentStage != "code" || back.StageStatus["code"] != "active" || back.StageStatus["eval"] != "pending" {
		t.Fatalf("the pipeline did not return to Code: %+v %v", back, err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>Tarefas</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reviewed, err := coderSaidDone(t, s, approved.ID).CompletePipelineCode(approved.ID)
	if err != nil || reviewed.StageStatus["code"] != "waiting_user" || reviewed.Artifacts["code"].Version <= approved.Artifacts["code"].Version {
		t.Fatalf("the new version did not open its review: %+v %v", reviewed, err)
	}
}

func TestCoderAfterPRsStaysClosed(t *testing.T) {
	s, approved, coder, _, _ := codeVerifiedAndApproved(t)
	if _, err := s.store.(*sqlite.Store).DB().Exec(`UPDATE pipeline_runs SET current_stage='prs' WHERE id=?`, approved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "Mais uma mudança"}); ErrorCode(err) != "pipeline_code_session_closed" {
		t.Fatalf("a Coder after the PRs went out must stay closed: %v", err)
	}
}

func TestCoderMessageStopsARunningQA(t *testing.T) {
	s, approved, coder, _, _ := codeVerifiedAndApproved(t)
	gen, err := s.claimQALoop(approved.ID, QALoopDTO{Phase: "evaluating", Round: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "Corrija o rodapé"}); err != nil {
		t.Fatal(err)
	}
	loop, err := s.GetPipelineQALoop(approved.ID)
	if err != nil || loop.Running || loop.Phase != "stopped" {
		t.Fatalf("QA kept running on the old version: %+v %v", loop, err)
	}
	// The stopped loop's next step (its QA run ends cancelled) neither runs nor writes over "stopped".
	s.qaLoopFailed(approved.ID, gen, 1, "", "O QA não terminou: cancelado")
	if loop, err := s.GetPipelineQALoop(approved.ID); err != nil || loop.Phase != "stopped" {
		t.Fatalf("the stopped loop wrote over its stop: %+v %v", loop, err)
	}
}

// Talking to an earlier Coder of the same copy makes it the latest one: its next run is the one the review reads.
func TestReturningAnEarlierCoderMakesItTheLatest(t *testing.T) {
	s, approved, coder, workspace, _ := codeVerifiedAndApproved(t)
	db := s.store.(*sqlite.Store)
	later := catalog.SessionRecord{ID: "later-coder", WorkspaceID: workspace.ID, BackendID: "local", Status: "completed", Mode: "sdd_code", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.CreateSessionWithSnapshots(t.Context(), later, nil, "", nil, "agent_session", nil); err != nil {
		t.Fatal(err)
	}
	links, err := db.ListPipelineSessions(t.Context(), approved.ID, "coder")
	if err != nil || len(links) != 1 {
		t.Fatalf("links: %+v %v", links, err)
	}
	if err := db.LinkPipelineSession(t.Context(), catalog.PipelineSession{ID: "later-link", PipelineID: approved.ID, SessionID: later.ID, Role: "coder", ExecutionSnapshot: links[0].ExecutionSnapshot, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "Volte a esta versão"}); err != nil {
		t.Fatal(err)
	}
	latest, err := s.latestPipelineSession(approved.ID, "coder")
	if err != nil || latest.SessionID != coder.Session.ID {
		t.Fatalf("the returned Coder is not the latest: %+v %v", latest, err)
	}
	if active, err := s.pipelineCodeSessionActive(coder.Session.ID); err != nil || !active {
		t.Fatalf("the returned Coder is not active: %v %v", active, err)
	}
}

// The person can stop QA at any moment: the loop ends where it is, says so, and its next step neither runs nor
// writes over the stop.
func TestStopPipelineQAEndsTheLoop(t *testing.T) {
	s, approved, _, _, _ := codeVerifiedAndApproved(t)
	gen, err := s.claimQALoop(approved.ID, QALoopDTO{Phase: "qa", Round: 2})
	if err != nil {
		t.Fatal(err)
	}
	loop, err := s.StopPipelineQA(approved.ID)
	if err != nil || loop.Running || loop.Phase != "stopped" || loop.Message != "QA parado por você." {
		t.Fatalf("stopping QA: %+v %v", loop, err)
	}
	s.qaLoopFailed(approved.ID, gen, 2, "", "O QA não terminou: cancelado")
	if loop, err := s.GetPipelineQALoop(approved.ID); err != nil || loop.Phase != "stopped" {
		t.Fatalf("the stopped loop wrote over its stop: %+v %v", loop, err)
	}
	if loop, err := s.StopPipelineQA(approved.ID); err != nil || loop.Phase != "stopped" {
		t.Fatalf("stopping a loop that is not running: %+v %v", loop, err)
	}
}

// A QA run tested the Code version it was copied from: once a newer version is approved, that run's verdict is not
// the new version's, and QA has to run again.
func TestAQARunFromAnEarlierCodeVersionIsNotItsVerdict(t *testing.T) {
	s, approved, coder, _, root := codeVerifiedAndApproved(t)
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: approved.ID, BackendID: "local", Role: "evaluator"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "Mais uma correção"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>Tarefas v2</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waiting, err := coderSaidDone(t, s, approved.ID).CompletePipelineCode(approved.ID)
	if err != nil {
		t.Fatal(err)
	}
	decidePipelineStageReviewForTest(t, s, waiting, "code", "approve", "")
	if _, err := s.CompletePipelineEvaluation(approved.ID); ErrorCode(err) != "pipeline_evaluation_stale" {
		t.Fatalf("a QA run of the previous version was taken as the new one's verdict: %v", err)
	}
}
