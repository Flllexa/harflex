package application

import (
	"os"
	"path/filepath"
	"testing"

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
	if err := s.claimQALoop(approved.ID, QALoopDTO{Phase: "evaluating", Round: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "Corrija o rodapé"}); err != nil {
		t.Fatal(err)
	}
	loop, err := s.GetPipelineQALoop(approved.ID)
	if err != nil || loop.Running || loop.Phase != "stopped" {
		t.Fatalf("QA kept running on the old version: %+v %v", loop, err)
	}
}
