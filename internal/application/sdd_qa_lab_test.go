package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

// qaLabProvider plays the Coder and the QA: each role first calls one tool, then answers.
type qaLabProvider struct {
	mu        sync.Mutex
	coderRuns int
	qaRuns    int
	qaTools   []string
	// proseFirst makes the first QA end in prose, as a long session sometimes does.
	proseFirst bool
	reminded   int
	// passFirst makes the first QA pass, for tests that go on to the pull requests.
	passFirst bool
	// failRounds makes that many QA runs fail, each with a new failure, before one passes (default 1).
	failRounds int
	watched    []string
}

func (*qaLabProvider) ID() string { return "qa-lab" }
func (*qaLabProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}

func (p *qaLabProvider) Stream(_ context.Context, r agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	events := make(chan agentcore.StreamEvent, 1)
	errs := make(chan error, 1)
	qa := false
	for _, message := range r.Messages {
		qa = qa || strings.Contains(message.Content, "Você é o QA desta entrega")
	}
	last := r.Messages[len(r.Messages)-1]
	if last.Role == agentcore.RoleUser && strings.Contains(last.Content, "Vigia da revisão do pull request") {
		p.watched = append(p.watched, last.Content)
		args, _ := json.Marshal(map[string]any{"url": "https://github.com/acme/todo/pull/7", "state": "merged", "handledComments": 2, "summary": "Corrigi os dois apontamentos e o PR foi mergeado"})
		events <- agentcore.StreamEvent{Type: "tool_call", ToolCall: &agentcore.ToolCall{ID: "watch-status", Name: "harflex_pull_request_status", Arguments: args}}
		close(events)
		close(errs)
		return events, errs
	}
	if last.Role == agentcore.RoleUser && strings.Contains(last.Content, "Você terminou sem o relatório") {
		p.reminded++
		events <- agentcore.StreamEvent{Type: "text_delta", Delta: `{"passed":false,"checks":[{"name":"Testes","kind":"unit","command":"npm test","status":"failed","summary":"falhou"}],"findings":["O CSV sai sem cabeçalho"],"improvements":[],"criteria":[]}`}
		close(events)
		close(errs)
		return events, errs
	}
	switch {
	case last.Role == agentcore.RoleTool && qa && p.proseFirst && p.qaRuns == 1:
		events <- agentcore.StreamEvent{Type: "text_delta", Delta: "Verificações concluídas; todos os critérios verificáveis passaram."}
	case last.Role == agentcore.RoleTool && qa:
		reply := `{"passed":false,"checks":[{"name":"Testes","kind":"unit","command":"go test ./...","status":"failed","summary":"falta o cabeçalho"}],"findings":["O CSV sai sem cabeçalho"],"improvements":["Nomear as colunas em português"],"criteria":[]}`
		if p.qaRuns > 1 && p.qaRuns <= p.failRounds {
			reply = `{"passed":false,"checks":[{"name":"E2E","kind":"e2e","command":"node e2e.cjs","status":"failed","summary":"expirou"}],"findings":["O E2E expira na rodada ` + strconv.Itoa(p.qaRuns) + `"],"improvements":[],"criteria":[]}`
		} else if p.qaRuns > 1 || p.passFirst {
			reply = `{"passed":true,"checks":[{"name":"Testes","kind":"unit","command":"go test ./...","status":"passed","summary":"ok"}],"findings":[],"improvements":[],"criteria":[{"criterion":"spec critérios","evidence":"ExportarCSV"}]}`
		}
		events <- agentcore.StreamEvent{Type: "text_delta", Delta: reply}
	case last.Role == agentcore.RoleTool:
		events <- agentcore.StreamEvent{Type: "text_delta", Delta: "Pronto."}
	case qa:
		p.qaRuns++
		p.qaTools = p.qaTools[:0]
		for _, tool := range r.Tools {
			p.qaTools = append(p.qaTools, tool.Name)
		}
		args, _ := json.Marshal(map[string]string{"command": "touch qa-only.txt"})
		events <- agentcore.StreamEvent{Type: "tool_call", ToolCall: &agentcore.ToolCall{ID: "qa-run", Name: "bash", Arguments: args}}
	default:
		p.coderRuns++
		content := "package export\nfunc ExportarCSV() {}\n"
		if p.coderRuns > 1 {
			content = "package export\n// cabeçalho: nome,valor\nfunc ExportarCSV() {}\n" + strings.Repeat("// ajuste\n", p.coderRuns-2)
		}
		args, _ := json.Marshal(map[string]string{"path": "export.go", "content": content})
		events <- agentcore.StreamEvent{Type: "tool_call", ToolCall: &agentcore.ToolCall{ID: "code-write", Name: "write", Arguments: args}}
	}
	close(events)
	close(errs)
	return events, errs
}

func waitQALoop(t *testing.T, s *Service, pipelineID string) QALoopDTO {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state, err := s.GetPipelineQALoop(pipelineID)
		if err != nil {
			t.Fatal(err)
		}
		if !state.Running && state.Phase != "" {
			return state
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the QA loop did not finish")
	return QALoopDTO{}
}

func TestQALabRunsChecksInItsOwnCopyAndFixesInTheSameCodeCopyUntilItPasses(t *testing.T) {
	_, db, vault := setup(t)
	provider := &qaLabProvider{}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }, ExecutionCacheRoot: t.TempDir()})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar faturas", Objective: "Exportar em CSV"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"discovery", "spec", "plan"} {
		if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: stage, Content: stage + " critérios e contexto"}); err != nil {
			t.Fatal(err)
		}
		if run, err = s.AdvancePipeline(run.ID); err != nil {
			t.Fatal(err)
		}
	}
	local := PipelineRoleChoice{BackendID: "local"}
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil || result.Status != RunCompleted {
		t.Fatalf("coder: %+v %v", result, err)
	}
	if _, err := s.CompletePipelineCode(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.decideLatest(run.ID, "code", "approve", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := s.StartPipelineQA(StartPipelineQAInput{PipelineID: run.ID, Evaluator: local}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPipelineQA(StartPipelineQAInput{PipelineID: run.ID, Evaluator: local}); err == nil {
		state, _ := s.GetPipelineQALoop(run.ID)
		if state.Running {
			t.Fatal("a second QA run started while the first runs")
		}
	}
	first := waitQALoop(t, s, run.ID)
	if first.Phase != "waiting" {
		t.Fatalf("first QA: %+v", first)
	}
	if strings.Join(provider.qaTools, ",") == "" || !strings.Contains(strings.Join(provider.qaTools, ","), "bash") || strings.Contains(strings.Join(provider.qaTools, ","), "write") {
		t.Fatalf("QA tools: %v", provider.qaTools)
	}
	current, err := s.GetPipeline(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var report pipelineEvaluationArtifact
	if err := json.Unmarshal([]byte(current.Artifacts["eval"].Content), &report); err != nil || report.Passed || len(report.Checks) != 1 || report.Checks[0].Status != "failed" || len(report.Improvements) != 1 {
		t.Fatalf("QA report: %+v %v", report, err)
	}
	coderLinks, _ := db.ListPipelineSessions(t.Context(), run.ID, "coder")
	evaluatorLinks, _ := db.ListPipelineSessions(t.Context(), run.ID, "evaluator")
	codeCopy, err := s.executionSnapshotForLink(coderLinks[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(qaLabRoot(codeCopy.Root, evaluatorLinks[0].ID), "qa-only.txt")); err != nil {
		t.Fatalf("the QA command did not run in its lab: %v", err)
	}
	if _, err := os.Stat(filepath.Join(codeCopy.Root, "qa-only.txt")); !os.IsNotExist(err) {
		t.Fatal("what QA did reached the Code copy")
	}

	if _, err := s.FixPipelineFindings(FixPipelineFindingsInput{PipelineID: run.ID, Findings: report.Findings, Coder: local, Evaluator: local}); err != nil {
		t.Fatal(err)
	}
	done := waitQALoop(t, s, run.ID)
	if done.Phase != "done" {
		t.Fatalf("fix loop: %+v", done)
	}
	current, _ = s.GetPipeline(run.ID)
	if err := json.Unmarshal([]byte(current.Artifacts["eval"].Content), &report); err != nil || !report.Passed {
		t.Fatalf("final QA: %+v %v", report, err)
	}
	if !strings.Contains(current.Artifacts["code"].Content, "cabeçalho") || !strings.Contains(current.Artifacts["code"].Content, "ExportarCSV") {
		t.Fatalf("the fix round lost earlier work or did not reach the evidence: %s", current.Artifacts["code"].Content)
	}
	coderLinks, _ = db.ListPipelineSessions(t.Context(), run.ID, "coder")
	if len(coderLinks) != 2 {
		t.Fatalf("coder rounds: %d", len(coderLinks))
	}
	second, err := s.executionSnapshotForLink(coderLinks[0])
	if err != nil {
		t.Fatal(err)
	}
	if second.Root != codeCopy.Root {
		t.Fatal("the fix round did not continue in the same private copy")
	}
}

func TestQALoopFixesANewFailureByItselfUntilQAPasses(t *testing.T) {
	_, db, vault := setup(t)
	provider := &qaLabProvider{failRounds: 2}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }, ExecutionCacheRoot: t.TempDir()})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar faturas", Objective: "Exportar em CSV"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"discovery", "spec", "plan"} {
		if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: stage, Content: stage + " critérios e contexto"}); err != nil {
			t.Fatal(err)
		}
		if run, err = s.AdvancePipeline(run.ID); err != nil {
			t.Fatal(err)
		}
	}
	local := PipelineRoleChoice{BackendID: "local"}
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil || result.Status != RunCompleted {
		t.Fatalf("coder: %+v %v", result, err)
	}
	if _, err := s.CompletePipelineCode(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.decideLatest(run.ID, "code", "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPipelineQA(StartPipelineQAInput{PipelineID: run.ID, Evaluator: local}); err != nil {
		t.Fatal(err)
	}
	if first := waitQALoop(t, s, run.ID); first.Phase != "waiting" {
		t.Fatalf("first QA: %+v", first)
	}
	if _, err := s.FixPipelineFindings(FixPipelineFindingsInput{PipelineID: run.ID, Findings: []string{"O CSV sai sem cabeçalho"}, Coder: local, Evaluator: local}); err != nil {
		t.Fatal(err)
	}
	// The second QA finds a new failure; the loop sends it to the Coder by itself and the third QA passes.
	if done := waitQALoop(t, s, run.ID); done.Phase != "done" {
		t.Fatalf("the loop did not fix the new failure by itself: %+v", done)
	}
	if _, err := s.ResumePipelineFixes(ResumePipelineFixesInput{PipelineID: run.ID, Coder: local, Evaluator: local}); err == nil {
		t.Fatal("fixes resumed with QA already passed")
	}
	if done := waitQALoop(t, s, run.ID); done.Phase != "done" {
		t.Fatalf("the loop did not fix the new failure by itself: %+v", done)
	}
	coderLinks, _ := db.ListPipelineSessions(t.Context(), run.ID, "coder")
	if len(coderLinks) != 3 || provider.qaRuns != 3 {
		t.Fatalf("coder rounds %d, QA runs %d", len(coderLinks), provider.qaRuns)
	}
}

func TestQALoopResumesFixesThatStoppedBeforeTheCoderRound(t *testing.T) {
	_, db, vault := setup(t)
	provider := &qaLabProvider{}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }, ExecutionCacheRoot: t.TempDir()})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar faturas", Objective: "Exportar em CSV"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"discovery", "spec", "plan"} {
		if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: stage, Content: stage + " critérios e contexto"}); err != nil {
			t.Fatal(err)
		}
		if run, err = s.AdvancePipeline(run.ID); err != nil {
			t.Fatal(err)
		}
	}
	local := PipelineRoleChoice{BackendID: "local"}
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil || result.Status != RunCompleted {
		t.Fatalf("coder: %+v %v", result, err)
	}
	if _, err := s.CompletePipelineCode(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.decideLatest(run.ID, "code", "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResumePipelineFixes(ResumePipelineFixesInput{PipelineID: run.ID, Coder: local, Evaluator: local}); err == nil {
		t.Fatal("fixes resumed while QA is due")
	}
	if _, err := s.StartPipelineQA(StartPipelineQAInput{PipelineID: run.ID, Evaluator: local}); err != nil {
		t.Fatal(err)
	}
	waitQALoop(t, s, run.ID)
	// QA's failures went back to Code, and the loop stopped before the Coder round.
	if err := s.requestEvaluationRevision(run.ID, qaFixFeedback([]string{"O CSV sai sem cabeçalho"}, nil, "")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResumePipelineFixes(ResumePipelineFixesInput{PipelineID: run.ID, Coder: local, Evaluator: local}); err != nil {
		t.Fatal(err)
	}
	if done := waitQALoop(t, s, run.ID); done.Phase != "done" {
		t.Fatalf("resumed fixes: %+v", done)
	}
}

func TestQAReportRejectsAPassWithAFailedCheck(t *testing.T) {
	failed := []pipelineQACheck{{Name: "build", Kind: "build", Command: "make", Status: "failed", Summary: "quebrou"}}
	if validQAReport(true, failed, nil, nil) {
		t.Fatal("a failed check passed")
	}
	if !validQAReport(false, failed, []string{"build quebrado"}, []string{"cache"}) {
		t.Fatal("a valid failing report was rejected")
	}
	if validQAReport(false, []pipelineQACheck{{Name: "x", Kind: "smoke", Status: "passed"}}, nil, nil) {
		t.Fatal("an unknown check kind was accepted")
	}
}

func TestQALoopHandsBackOnlyWhenTheSameFailuresReturn(t *testing.T) {
	if sameFailuresKey([]string{"Botão some", "  E2E   quebra "}) != sameFailuresKey([]string{"e2e quebra", "botão some"}) {
		t.Fatal("the same failures in another order or spacing looked new")
	}
	if sameFailuresKey([]string{"Botão some"}) == sameFailuresKey([]string{"Botão some", "E2E quebra"}) {
		t.Fatal("a new failure looked like the same set")
	}
}

func TestQAEvidenceQuotedAcrossLinesIsFoundInTheDiff(t *testing.T) {
	diff := "diff --git a/app.js b/app.js\n--- a/app.js\n+++ b/app.js\n@@ -1,2 +1,4 @@\n function load() {\n-  return null\n+  if (raw === null) {\n+    return [];\n+  }\n }\n"
	code := normalizedEvidence(resultingCode(diff))
	if !strings.Contains(code, normalizedEvidence("if (raw === null) {\n      return [];\n    }")) {
		t.Fatal("evidence quoted across added lines was not found")
	}
	if !strings.Contains(code, normalizedEvidence("function load() {")) {
		t.Fatal("evidence from a context line was not found")
	}
	if strings.Contains(code, normalizedEvidence("return null")) {
		t.Fatal("a removed line counted as evidence")
	}
}

func TestQALabAsksForTheReportAgainWhenTheEvaluatorEndsInProse(t *testing.T) {
	_, db, vault := setup(t)
	provider := &qaLabProvider{proseFirst: true}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }, ExecutionCacheRoot: t.TempDir()})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar", Objective: "CSV"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"discovery", "spec", "plan"} {
		if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: stage, Content: stage + " critérios e contexto"}); err != nil {
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
	if _, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompletePipelineCode(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.decideLatest(run.ID, "code", "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPipelineQA(StartPipelineQAInput{PipelineID: run.ID, Evaluator: PipelineRoleChoice{BackendID: "local"}}); err != nil {
		t.Fatal(err)
	}
	state := waitQALoop(t, s, run.ID)
	if state.Phase != "waiting" || provider.reminded != 1 {
		t.Fatalf("the loop did not recover the report: %+v reminded=%d", state, provider.reminded)
	}
}

func TestPullRequestWatchChecksTheRecordedPRInItsConversationAndStopsWhenMerged(t *testing.T) {
	_, db, vault := setup(t)
	provider := &qaLabProvider{passFirst: true}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }, ExecutionCacheRoot: t.TempDir()})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar", Objective: "CSV"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"discovery", "spec", "plan"} {
		if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: stage, Content: stage + " critérios e contexto"}); err != nil {
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
	if _, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompletePipelineCode(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.decideLatest(run.ID, "code", "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPipelineQA(StartPipelineQAInput{PipelineID: run.ID, Evaluator: PipelineRoleChoice{BackendID: "local"}}); err != nil {
		t.Fatal(err)
	}
	if state := waitQALoop(t, s, run.ID); state.Phase != "done" {
		t.Fatalf("QA: %+v", state)
	}
	if err := s.decideLatest(run.ID, "eval", "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyPipelineCode(run.ID); err != nil {
		t.Fatal(err)
	}
	publisher, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "publisher"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(publisher.Prompt, "harflex_register_pull_request") {
		t.Fatal("the PRs prompt does not ask to record the pull requests")
	}
	register := harflexToolNamed(t, s.pullRequestTools(run.ID, publisher.Session.ID), "harflex_register_pull_request")
	if _, err := register.Execute(t.Context(), json.RawMessage(`{"url":"https://github.com/acme/todo/pull/7","title":"Exportar CSV","branch":"harflex/exportar-csv"}`), nil); err != nil {
		t.Fatal(err)
	}
	listed, err := s.ListPipelinePullRequests(run.ID)
	if err != nil || len(listed) != 1 || listed[0].State != "open" || listed[0].Watch || listed[0].Branch != "harflex/exportar-csv" {
		t.Fatalf("recorded PRs: %+v %v", listed, err)
	}
	if _, err := s.SetPullRequestWatch(SetPullRequestWatchInput{ID: listed[0].ID, Watch: true}); err != nil {
		t.Fatal(err)
	}
	// Without Acesso total nobody could approve the agent's commands, so the watch says so instead of starting.
	s.tickPullRequestWatch(time.Now().UTC())
	listed, _ = s.ListPipelinePullRequests(run.ID)
	if len(provider.watched) != 0 || listed[0].Timeline[len(listed[0].Timeline)-1].Kind != "needs_access" {
		t.Fatalf("watch without full access: %+v", listed[0].Timeline)
	}
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "full_access", ConfirmFullAccess: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckPullRequestNow(listed[0].ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		listed, _ = s.ListPipelinePullRequests(run.ID)
		if listed[0].State == "merged" && !listed[0].Checking {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(provider.watched) != 1 || !strings.Contains(provider.watched[0], "harflex/exportar-csv") || listed[0].State != "merged" || listed[0].Watch {
		t.Fatalf("watch check: watched=%d %+v", len(provider.watched), listed[0])
	}
	if last := listed[0].Timeline[len(listed[0].Timeline)-1]; last.Kind != "merged" || !strings.Contains(last.Summary, "mergeado") {
		t.Fatalf("timeline: %+v", listed[0].Timeline)
	}
}
