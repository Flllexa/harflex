package application

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestCoderReportReadsTheClosingStatus(t *testing.T) {
	for name, test := range map[string]struct {
		text, state, summary string
		questions            []string
	}{
		"done":                  {"Feito.\n```harflex-code\n{\"status\":\"done\",\"summary\":\"Exportação pronta.\"}\n```", "done", "Exportação pronta.", nil},
		"questions":             {"Preciso saber:\n```harflex-code\n{\"status\":\"questions\",\"questions\":[\"Qual bucket?\",\" \",\"Qual TTL?\"]}\n```", "questions", "", []string{"Qual bucket?", "Qual TTL?"}},
		"no block":              {"Criei IMPLEMENTATION_STATUS.md com as pendências.", "unfinished", "", nil},
		"broken block":          {"```harflex-code\n{status: done}\n```", "unfinished", "", nil},
		"questions with none":   {"```harflex-code\n{\"status\":\"questions\",\"questions\":[]}\n```", "unfinished", "", nil},
		"the last block counts": {"```harflex-code\n{\"status\":\"questions\",\"questions\":[\"a\"]}\n```\nRespondido.\n```harflex-code\n{\"status\":\"done\"}\n```", "done", "", nil},
	} {
		state, summary, questions, message := coderReport(test.text)
		if state != test.state || summary != test.summary || strings.Join(questions, "|") != strings.Join(test.questions, "|") || strings.Contains(message, "harflex-code") {
			t.Fatalf("%s: %q %q %v %q", name, state, summary, questions, message)
		}
	}
}

// coderAt opens a work at Code with an API Coder that changed a file in its private copy.
func coderAt(t *testing.T) (*Service, *sqlite.Store, PipelineDTO, PipelineSessionDTO) {
	t.Helper()
	s, db, _ := setup(t)
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar", Objective: "CSV"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"discovery", "spec", "brainstorm", "plan"} {
		if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: stage, Content: stage + " do CSV"}); err != nil {
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
		t.Fatal(err)
	}
	snapshot, err := s.executionSnapshotForLink(links[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot.Root, "export.go"), []byte("package export\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return s, db, run, coder
}

func say(t *testing.T, db *sqlite.Store, sessionID, text string) {
	t.Helper()
	for _, event := range []struct {
		kind string
		data any
	}{{"run.started", map[string]string{}}, {"message.assistant", agentcore.Message{Role: agentcore.RoleAssistant, Content: text}}, {"run.completed", map[string]string{"reason": ""}}} {
		if _, err := db.Append(t.Context(), sessionID, "agent_session", event.kind, event.data); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCodeGoesToReviewOnlyWhenTheCoderSaysItFinished(t *testing.T) {
	s, db, run, coder := coderAt(t)
	if !strings.Contains(coder.Prompt, "harflex-code") || !strings.Contains(coder.Prompt, "Não crie arquivos de status") {
		t.Fatal("the Coder is not told how to say it finished or to ask")
	}
	if status, err := s.GetPipelineCoderStatus(run.ID); err != nil || status.State != "none" && status.State != "working" && status.SessionID != coder.Session.ID {
		t.Fatalf("before any run: %+v %v", status, err)
	}
	// It stopped partway, writing notes instead of code: nothing to review yet.
	say(t, db, coder.Session.ID, "Criei NEXT_ACTIONS.md com as confirmações pendentes.")
	if _, err := s.CompletePipelineCode(run.ID); !errors.Is(err, ErrPipelineCodeNotFinished) {
		t.Fatalf("a Coder that did not finish went to review: %v", err)
	}
	if status, _ := s.GetPipelineCoderStatus(run.ID); status.State != "unfinished" || !status.CanReply || !strings.Contains(status.Message, "NEXT_ACTIONS") {
		t.Fatalf("unfinished: %+v", status)
	}
	// It asks: the person answers in the same conversation, which still takes messages.
	say(t, db, coder.Session.ID, "Antes de seguir:\n```harflex-code\n{\"status\":\"questions\",\"questions\":[\"Qual TTL?\"]}\n```")
	status, err := s.GetPipelineCoderStatus(run.ID)
	if err != nil || status.State != "questions" || strings.Join(status.Questions, "|") != "Qual TTL?" || !status.CanReply {
		t.Fatalf("questions: %+v %v", status, err)
	}
	if _, err := s.CompletePipelineCode(run.ID); !errors.Is(err, ErrPipelineCodeNotFinished) {
		t.Fatalf("a Coder with open questions went to review: %v", err)
	}
	reply, err := s.ReplyToPipelineCoder(ReplyToPipelineCoderInput{PipelineID: run.ID, Text: "90 dias"})
	if err != nil || reply.Session.ID != coder.Session.ID || !strings.Contains(reply.Prompt, "90 dias") {
		t.Fatalf("reply: %+v %v", reply, err)
	}
	if _, err := s.ReplyToPipelineCoder(ReplyToPipelineCoderInput{PipelineID: run.ID, Text: "  "}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("an empty answer: %v", err)
	}
	say(t, db, coder.Session.ID, "Implementei com TTL de 90 dias.\n```harflex-code\n{\"status\":\"done\",\"summary\":\"CSV pronto.\"}\n```")
	if status, _ := s.GetPipelineCoderStatus(run.ID); status.State != "done" || status.Summary != "CSV pronto." {
		t.Fatalf("done: %+v", status)
	}
	verified, err := s.CompletePipelineCode(run.ID)
	if err != nil || verified.StageStatus["code"] != "waiting_user" {
		t.Fatalf("a finished Coder's change was not offered for review: %+v %v", verified, err)
	}
	if ErrorCode(ErrPipelineCodeNotFinished) != "pipeline_code_not_finished" {
		t.Fatal("the error needs a stable code")
	}
}
