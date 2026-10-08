package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/repositories"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestCreatePipelineSessionRetainsStageModelSelection(t *testing.T) {
	request := map[string]any{
		"pipelineId": "pipeline-1",
		"backendId":  "codex",
		"role":       "coder",
		"selection": map[string]any{
			"executor": "codex_cli", "backendId": "codex", "profileId": "", "modelId": "model-stage",
			"catalogRevision": "catalog-stage", "source": "codex_app_server", "destination": "",
		},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var input CreatePipelineSessionInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	selection, ok := decoded["selection"].(map[string]any)
	if !ok || selection["modelId"] != "model-stage" || selection["catalogRevision"] != "catalog-stage" {
		t.Fatalf("pipeline session dropped its stage model selection: %s", encoded)
	}
}

func TestPreviewPipelineCodeWorkspaceReportsPrivateCopyScope(t *testing.T) {
	s, _, _ := setup(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "metadata"), []byte("excluded"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewPipelineCodeWorkspace(workspace.ID)
	if err != nil || preview.IsGit || preview.FileCount != 1 || preview.TotalBytes != int64(len("package main\n")) || len(preview.ExcludedPaths) != 1 || preview.ExcludedPaths[0] != ".git" {
		t.Fatalf("Code copy preview: %+v %v", preview, err)
	}
	if _, err := os.Stat(filepath.Join(root, "main.go")); err != nil {
		t.Fatalf("preview modified source: %v", err)
	}
}

func TestPreviewPipelineCodeWorkspaceEmptyListsSerializeAsArrays(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewPipelineCodeWorkspace(workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"excludedPaths", "unsafePaths"} {
		values, ok := decoded[key].([]any)
		if !ok || len(values) != 0 {
			t.Fatalf("%s must be an empty JSON array for the frontend contract: %s", key, raw)
		}
	}
}

func TestLegacyPipelineCoderSessionWithoutSnapshotReopensReadOnly(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Legacy", Objective: "Inspect legacy Code"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := catalog.SessionRecord{ID: "legacy-code-session", WorkspaceID: workspace.ID, BackendID: "local", Status: "ready", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateSessionWithSnapshots(t.Context(), session, nil, "", nil, "agent_session", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkPipelineSession(t.Context(), catalog.PipelineSession{ID: "legacy-code-link", PipelineID: pipeline.ID, SessionID: session.ID, Role: "coder", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	opened, err := s.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: workspace.ID})
	if err != nil || opened.Resumable {
		t.Fatalf("legacy direct-workspace Code resumed after upgrade: %+v %v", opened, err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "change project files"}); !errors.Is(err, externalagent.ErrNotResumable) {
		t.Fatalf("legacy Code did not remain read-only: %v", err)
	}
}

func TestEvaluationRejectionReturnsToCodeWithoutARoundLimit(t *testing.T) {
	_, db, vault := setup(t)
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ExecutionCacheRoot: t.TempDir()})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Revisar", Objective: "Implementar e avaliar"})
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: "discovery", Content: "Discovery completo dos critérios do TODO"})
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.AdvancePipeline(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	completeCode := func(round int) {
		t.Helper()
		coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true})
		if err != nil {
			t.Fatal(err)
		}
		links, err := db.ListPipelineSessions(t.Context(), run.ID, "coder")
		if err != nil || len(links) == 0 {
			t.Fatalf("read Coder execution snapshot: %+v %v", links, err)
		}
		execution, err := s.executionSnapshotForLink(links[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(execution.Root, fmt.Sprintf("round-%d.txt", round)), []byte(fmt.Sprintf("round %d change\n", round)), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, event := range []struct {
			kind string
			data any
		}{
			{"tool.completed", map[string]any{"toolCallId": "tool", "name": "edit", "content": map[string]string{"text": "ok"}, "details": map[string]string{"diff": "+round change"}}},
			{"run.completed", map[string]string{"reason": ""}},
		} {
			if _, err := db.Append(t.Context(), coder.Session.ID, "agent_session", event.kind, event.data); err != nil {
				t.Fatal(err)
			}
		}
		run, err = s.CompletePipelineCode(run.ID)
		if err != nil || run.CurrentStage != "code" || run.StageStatus["code"] != "waiting_user" {
			t.Fatalf("code round %d: %+v %v", round, run, err)
		}
		run = decidePipelineStageReviewForTest(t, s, run, "code", "approve", "")
	}
	for round := 1; round <= 5; round++ {
		completeCode(round)
		evaluator, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "evaluator"})
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range []struct {
			kind string
			data any
		}{
			{"message.assistant", map[string]string{"role": "assistant", "content": `{"passed":false,"findings":["Critério não atendido"]}`}},
			{"run.completed", map[string]string{"reason": ""}},
		} {
			if _, err := db.Append(t.Context(), evaluator.Session.ID, "agent_session", event.kind, event.data); err != nil {
				t.Fatal(err)
			}
		}
		run, err = s.CompletePipelineEvaluation(run.ID)
		if err != nil || run.CurrentStage != "eval" || run.StageStatus["eval"] != "waiting_user" {
			t.Fatalf("evaluation round %d: %+v %v", round, run, err)
		}
		run = decidePipelineStageReviewForTest(t, s, run, "eval", "request_revision", "Critério não atendido; revisar o Código.")
		if run.CurrentStage != "code" || run.StageStatus["eval"] != "failed" {
			t.Fatalf("evaluation revision round %d: %+v", round, run)
		}
	}
	// QA has no round limit: a sixth run is admitted like the first.
	completeCode(6)
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "evaluator"}); err != nil {
		t.Fatalf("evaluation after five rounds refused: %v", err)
	}
}

func TestAuthoringPipelineExecutesAndEvaluatesApprovedPlan(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "authoring-code-eval-0001", Discovery: "Build one index.html TODO with localStorage"})
	if err != nil {
		t.Fatal(err)
	}
	const status = `{"discovery":"completed","spec":"completed","plan":"completed","code":"active","eval":"pending"}`
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='code',stage_status=?,revision=revision+1 WHERE id=?`, status, run.ID); err != nil {
		t.Fatal(err)
	}
	for stage, content := range map[string]string{
		"spec": "The SPEC requires a single index.html and tasks that can be added.",
		"plan": "Implement addTask and verify it in the browser.",
	} {
		if _, err := db.DB().ExecContext(t.Context(), `INSERT INTO pipeline_artifacts (pipeline_id,stage,version,content,author,source_session_id,updated_at) VALUES (?,?,1,?,'ai','',?)`, run.ID, stage, content, run.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}

	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil || coder.Session.ID == "" || !strings.Contains(coder.Prompt, "The SPEC requires a single index.html") {
		t.Fatalf("authoring Coder session: %+v %v", coder, err)
	}
	coderLinks, err := db.ListPipelineSessions(t.Context(), run.ID, "coder")
	if err != nil || len(coderLinks) != 1 {
		t.Fatalf("read isolated Coder link: %+v %v", coderLinks, err)
	}
	codeSnapshot, err := s.executionSnapshotForLink(coderLinks[0])
	if err != nil || codeSnapshot.Root == workspace.Path || codeSnapshot.SourceRoot != workspace.Path {
		t.Fatalf("Coder did not get an isolated root: %+v %v", codeSnapshot, err)
	}
	if err := os.WriteFile(filepath.Join(codeSnapshot.Root, "main.js"), []byte("function addTask() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, event := range []struct {
		kind string
		data any
	}{
		{"tool.completed", map[string]any{"toolCallId": "tool-1", "name": "edit", "content": map[string]string{"text": "ok"}, "details": map[string]string{"diff": "+function addTask() {}"}}},
		{"run.completed", map[string]string{"reason": ""}},
	} {
		if _, err := db.Append(t.Context(), coder.Session.ID, "agent_session", event.kind, event.data); err != nil {
			t.Fatal(err)
		}
	}
	run, err = s.CompletePipelineCode(run.ID)
	if err != nil || run.Kind != "ai_authoring" || run.CurrentStage != "code" || run.StageStatus["code"] != "waiting_user" || run.Artifacts["code"].Author != "ai" || run.Artifacts["code"].SourceSessionID != coder.Session.ID ||
		!strings.Contains(run.Artifacts["code"].Content, "# Harflex isolated Code provenance") || !strings.Contains(run.Artifacts["code"].Content, "file=\"main.js\" action=added") || !strings.Contains(run.Artifacts["code"].Content, "after=") {
		t.Fatalf("authoring Code completion: %+v %v", run, err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "main.js")); !os.IsNotExist(err) {
		t.Fatalf("Code wrote into the source workspace before Eval: %v", err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "continue editing after Code verification"}); ErrorCode(err) != "pipeline_code_session_closed" {
		t.Fatalf("verified Code session remained writable after its gate: %v", err)
	}
	if _, err := s.ApplyPipelineCode(run.ID); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("Code patch was applicable before human review: %v", err)
	}
	run = decidePipelineStageReviewForTest(t, s, run, "code", "approve", "")
	if run.CurrentStage != "eval" || run.StageStatus["code"] != "completed" {
		t.Fatalf("Code approval did not admit Eval: %+v", run)
	}

	evaluator, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "evaluator"})
	if err != nil || !strings.Contains(evaluator.Prompt, "function addTask") {
		t.Fatalf("authoring Evaluator session: %+v %v", evaluator, err)
	}
	for _, event := range []struct {
		kind string
		data any
	}{
		{"message.assistant", map[string]string{"role": "assistant", "content": `{"passed":true,"findings":[],"criteria":[{"criterion":"The SPEC requires a single index.html","evidence":"function addTask"}]}`}},
		{"run.completed", map[string]string{"reason": ""}},
	} {
		if _, err := db.Append(t.Context(), evaluator.Session.ID, "agent_session", event.kind, event.data); err != nil {
			t.Fatal(err)
		}
	}
	run, err = s.CompletePipelineEvaluation(run.ID)
	if err != nil || run.Kind != "ai_authoring" || run.CurrentStage != "eval" || run.StageStatus["eval"] != "waiting_user" || run.Artifacts["eval"].SourceSessionID != evaluator.Session.ID {
		t.Fatalf("authoring Eval completion: %+v %v", run, err)
	}
	if _, err := s.ApplyPipelineCode(run.ID); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("Code patch was applicable before human Eval approval: %v", err)
	}
	run = decidePipelineStageReviewForTest(t, s, run, "eval", "approve", "")
	if run.CurrentStage != "prs" || run.StageStatus["eval"] != "completed" || run.StageStatus["prs"] != "active" {
		t.Fatalf("Eval approval did not hand the pipeline to the pull request stage: %+v", run)
	}
	// The agent works on the project folder, so it does not start before the approved patch is there.
	if !run.CodePatchPending || run.CodeAppliedAt != nil {
		t.Fatalf("the pipeline does not say its approved patch is still to be applied: pending=%v applied=%v", run.CodePatchPending, run.CodeAppliedAt)
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "publisher"}); !errors.Is(err, ErrPipelineCodeNotApplied) {
		t.Fatalf("pull requests started before the patch was applied: %v", err)
	}
	run, err = s.ApplyPipelineCode(run.ID)
	if err != nil || run.CodeAppliedAt == nil || run.CodePatchPending || run.CurrentStage != "prs" {
		t.Fatalf("approved patch was not applied with readback: %+v %v", run, err)
	}
	if reread, err := s.GetPipelineForSession(GetPipelineForSessionInput{SessionID: coder.Session.ID, WorkspaceID: workspace.ID}); err != nil || reread.CodeAppliedAt == nil || reread.CodePatchPending {
		t.Fatalf("the pipeline read from a session lost its apply status: %+v %v", reread, err)
	}
	if contents, err := os.ReadFile(filepath.Join(workspace.Path, "main.js")); err != nil || !strings.Contains(string(contents), "function addTask") {
		t.Fatalf("approved Code patch did not reach the source workspace: %q %v", contents, err)
	}
	appliedLinks, err := db.ListPipelineSessions(t.Context(), run.ID, "coder")
	if err != nil || len(appliedLinks) != 1 || appliedLinks[0].ExecutionAppliedAt.IsZero() {
		t.Fatalf("patch application readback was not persisted: %+v %v", appliedLinks, err)
	}
	if repeated, err := s.ApplyPipelineCode(run.ID); err != nil || repeated.CodeAppliedAt == nil {
		t.Fatalf("patch application was not idempotent: %+v %v", repeated, err)
	}

	// Pull requests: an ordinary chat in the project folder, linked to the pipeline, whose final answer is the stage's evidence.
	if _, err := s.FinishPipelinePRs(FinishPipelinePRsInput{PipelineID: run.ID, Outcome: "completed"}); !errors.Is(err, sdd.ErrEvidenceRequired) {
		t.Fatalf("pull requests completed without any agent work: %v", err)
	}
	publisher, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "publisher"})
	if err != nil || publisher.Role != "publisher" || publisher.Session.ID == "" ||
		!strings.Contains(publisher.Prompt, "Implement addTask and verify it in the browser.") || !strings.Contains(publisher.Prompt, "The SPEC requires a single index.html") {
		t.Fatalf("pull request session: %+v %v", publisher, err)
	}
	if record, err := db.GetSession(t.Context(), publisher.Session.ID); err != nil || record.Mode != "" || record.WorkspaceID != workspace.ID {
		t.Fatalf("the pull request chat must be an ordinary session of the project: %+v %v", record, err)
	}
	if linked, err := s.GetPipelineForSession(GetPipelineForSessionInput{SessionID: publisher.Session.ID, WorkspaceID: workspace.ID}); err != nil || linked.ID != run.ID {
		t.Fatalf("the pull request chat does not find its pipeline: %+v %v", linked, err)
	}
	listed, err := s.ListSessions(ListSessionsInput{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range listed {
		if item.ID == publisher.Session.ID {
			found = true
			if item.Purpose != "chat" || item.Title != "PRs · "+run.Title {
				t.Fatalf("the pull request chat is listed as %q/%q; it must stay an ordinary chat named for its work", item.Purpose, item.Title)
			}
		}
	}
	if !found {
		t.Fatal("the pull request chat is missing from the project's sessions, so it could not be reopened for the fixes a review asks for")
	}
	if activity, err := s.GetPipelineStageActivity(run.ID, "prs"); err != nil || activity.SessionID != publisher.Session.ID {
		t.Fatalf("pull request stage activity: %+v %v", activity, err)
	}
	if _, err := s.FinishPipelinePRs(FinishPipelinePRsInput{PipelineID: run.ID, Outcome: "completed"}); !errors.Is(err, sdd.ErrEvidenceRequired) {
		t.Fatalf("pull requests completed before the agent finished: %v", err)
	}
	const report = "## Pull requests\n- https://github.com/acme/todo/pull/12 · harflex/add-task · main"
	for _, event := range []struct {
		kind string
		data any
	}{
		{"run.started", map[string]string{}},
		{"message.assistant", map[string]string{"role": "assistant", "content": report}},
		{"run.completed", map[string]string{"reason": ""}},
	} {
		if _, err := db.Append(t.Context(), publisher.Session.ID, "agent_session", event.kind, event.data); err != nil {
			t.Fatal(err)
		}
	}
	run, err = s.FinishPipelinePRs(FinishPipelinePRsInput{PipelineID: run.ID, Outcome: "completed"})
	if err != nil || run.CurrentStage != "" || run.StageStatus["prs"] != "completed" || run.Artifacts["prs"].Content != report ||
		run.Artifacts["prs"].Author != "ai" || run.Artifacts["prs"].SourceSessionID != publisher.Session.ID || run.CodeAppliedAt == nil {
		t.Fatalf("pull request completion: %+v %v", run, err)
	}
	if _, err := s.FinishPipelinePRs(FinishPipelinePRsInput{PipelineID: run.ID, Outcome: "completed"}); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("a finished pipeline accepted a second pull request completion: %v", err)
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "publisher"}); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("a finished pipeline accepted a new pull request session: %v", err)
	}
}

// Pipelines saved before the stage existed finished at QA with no status for it. They can still open their pull
// requests, and skipping ends the stage without pretending it ran.
func TestPullRequestStageSkipsAndServesPipelinesThatFinishedBeforeItExisted(t *testing.T) {
	created := 0
	setupFinished := func(t *testing.T, s *Service, db *sqlite.Store, workspaceID, current, status string) string {
		t.Helper()
		created++
		run, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspaceID, RequestID: fmt.Sprintf("pull-request-stage-%04d", created), Discovery: "Export invoices"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage=?,stage_status=?,revision=revision+1 WHERE id=?`, current, status, run.ID); err != nil {
			t.Fatal(err)
		}
		return run.ID
	}
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	finishedBefore := setupFinished(t, s, db, workspace.ID, "", `{"discovery":"completed","spec":"completed","plan":"completed","code":"completed","eval":"completed"}`)
	skipped, err := s.FinishPipelinePRs(FinishPipelinePRsInput{PipelineID: finishedBefore, Outcome: "skipped", Reason: "  Vou abrir o PR à mão  "})
	if err != nil || skipped.CurrentStage != "" || skipped.StageStatus["prs"] != "skipped" || skipped.Artifacts["prs"].Content != "" {
		t.Fatalf("skipping on a pipeline from before the stage: %+v %v", skipped, err)
	}
	var reason, action string
	if err := db.DB().QueryRowContext(t.Context(), `SELECT action, reason FROM pipeline_transitions WHERE pipeline_id=? AND stage='prs'`, finishedBefore).Scan(&action, &reason); err != nil || action != "skip" || reason != "Vou abrir o PR à mão" {
		t.Fatalf("skip was not recorded: action=%q reason=%q %v", action, reason, err)
	}
	if _, err := s.FinishPipelinePRs(FinishPipelinePRsInput{PipelineID: finishedBefore, Outcome: "skipped"}); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("a skipped stage was skipped again: %v", err)
	}

	active := setupFinished(t, s, db, workspace.ID, "prs", `{"discovery":"completed","spec":"completed","plan":"completed","code":"completed","eval":"completed","prs":"active"}`)
	if run, err := s.FinishPipelinePRs(FinishPipelinePRsInput{PipelineID: active, Outcome: "skipped"}); err != nil || run.CurrentStage != "" || run.StageStatus["prs"] != "skipped" {
		t.Fatalf("skipping the current stage: %+v %v", run, err)
	}

	// A legacy pipeline cannot write the stage's evidence by hand or advance past it without the agent's report.
	manual, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Manual", Objective: "Fluxo legado"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='prs',stage_status=?,revision=revision+1 WHERE id=?`, `{"discovery":"completed","spec":"completed","plan":"completed","code":"completed","eval":"completed","prs":"active"}`, manual.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: manual.ID, Stage: "prs", Content: "escrito à mão"}); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("the pull request evidence was written by hand: %v", err)
	}
	if _, err := s.AdvancePipeline(manual.ID); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("a legacy pipeline advanced past the pull request stage without its report: %v", err)
	}

	beforeQA := setupFinished(t, s, db, workspace.ID, "eval", `{"discovery":"completed","spec":"completed","plan":"completed","code":"completed","eval":"waiting_user","prs":"pending"}`)
	if _, err := s.FinishPipelinePRs(FinishPipelinePRsInput{PipelineID: beforeQA, Outcome: "skipped"}); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("pull requests skipped before QA approved: %v", err)
	}
	if _, err := s.FinishPipelinePRs(FinishPipelinePRsInput{PipelineID: active, Outcome: "maybe"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown outcome accepted: %v", err)
	}
}

func TestAuthoringPipelineBlocksCodexCLIWithoutReadIsolation(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cli := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0"}
	s.external[cli.id] = cli
	run, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "codex-authoring-code-0001", Discovery: "Create a one-file TODO with localStorage"})
	if err != nil {
		t.Fatal(err)
	}
	const status = `{"discovery":"completed","spec":"completed","plan":"completed","code":"active","eval":"pending"}`
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='code',stage_status=?,revision=revision+1 WHERE id=?`, status, run.ID); err != nil {
		t.Fatal(err)
	}
	for stage, content := range map[string]string{
		"spec": "The SPEC requires a single index.html and the app can add tasks.",
		"plan": "Implement addTask and verify it in a browser.",
	} {
		if _, err := db.DB().ExecContext(t.Context(), `INSERT INTO pipeline_artifacts (pipeline_id,stage,version,content,author,source_session_id,updated_at) VALUES (?,?,1,?,'ai','',?)`, run.ID, stage, content, run.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "codex", Role: "coder"}); !errors.Is(err, ErrSDDCLIReadIsolationUnavailable) {
		t.Fatalf("Professional Code admitted an unconfined Codex CLI: %v", err)
	}
	var sessions int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE workspace_id=?`, workspace.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("unsafe Codex Code created %d sessions: %v", sessions, err)
	}
	if len(cli.runs) != 0 {
		t.Fatalf("unsafe Codex Code executed %d CLI runs", len(cli.runs))
	}
}

func TestPipelineCoderRejectsCLIWithoutProvenWorkspaceSandbox(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "README.md"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	s, db, _ := setup(t)
	cli := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "opencode", version: "1.14.27"}
	s.external[cli.id] = cli
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "TODO", Objective: "Criar TODO"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"discovery", "spec", "plan"} {
		if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: stage, Content: "Critérios do TODO"}); err != nil {
			t.Fatal(err)
		}
		run, err = s.AdvancePipeline(run.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: cli.id})
	if err != nil || catalog.Source != "opencode_cli" {
		t.Fatalf("query OpenCode catalog: %+v %v", catalog, err)
	}
	choice := &APIModelSelectionInput{Executor: "cli", BackendID: cli.id, ModelID: "provider/model-exact", CatalogRevision: catalog.ProfileRevision, Source: catalog.Source, CheckedAt: catalog.CheckedAt}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: cli.id, Role: "coder", Selection: choice}); !errors.Is(err, ErrSDDCLIReadIsolationUnavailable) {
		t.Fatalf("uncontained OpenCode Coder admitted: %v", err)
	}
	var sessionCount int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE workspace_id=?`, workspace.ID).Scan(&sessionCount); err != nil || sessionCount != 0 {
		t.Fatalf("uncontained CLI preflight created %d sessions: %v", sessionCount, err)
	}
}

func TestPipelineCodexCLIIsBlockedForProfessionalCodeWithoutReadIsolation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "main.go"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	s, db, _ := setup(t)
	s.external["codex"] = &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "0.157.0"}
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Alterar código", Objective: "Editar main.go"})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "codex", Role: "coder"}); !errors.Is(err, ErrSDDCLIReadIsolationUnavailable) {
		t.Fatalf("Codex CLI ran Professional Code without a read boundary: %v", err)
	}
	var sessions int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE workspace_id=?`, workspace.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("unsafe Codex Code preflight created %d sessions: %v", sessions, err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "main.go")); err != nil || string(got) != "package main\n" {
		t.Fatalf("blocked Code mutated source: %q %v", got, err)
	}
}

func TestPipelineNonGitCoderRequiresExplicitPrivateCopyConfirmation(t *testing.T) {
	root := t.TempDir()
	s, db, _ := setup(t)
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Implementar exemplo", Objective: "Criar um TODO"})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder"}); !errors.Is(err, repositories.ErrExecutionCopyConfirmationRequired) {
		t.Fatalf("non-Git workspace was copied without confirmation: %v", err)
	}
	var sessions int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE workspace_id=?`, workspace.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("failed preflight created %d sessions: %v", sessions, err)
	}
	created, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil || created.Session.ID == "" {
		t.Fatalf("confirmed non-Git private copy did not start: %+v %v", created, err)
	}
	links, err := db.ListPipelineSessions(t.Context(), run.ID, "coder")
	if err != nil || len(links) != 1 {
		t.Fatalf("confirmed non-Git copy has no session link: %+v %v", links, err)
	}
	prepared, err := s.executionSnapshotForLink(links[0])
	if err != nil || prepared.IsGit || prepared.Root == workspace.Path {
		t.Fatalf("non-Git Coder did not use its isolated copy: %+v %v", prepared, err)
	}
}

func TestPipelineCoderRejectsDirtyGitSourceBeforeCreatingSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("baseline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "README.md"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("pre-existing local edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, db, vault := setup(t)
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{"codex": fakeExternal{true}}, ExecutionCacheRoot: t.TempDir()})
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Implementar exemplo", Objective: "Criar um TODO"})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder"}); !errors.Is(err, ErrPipelineGitDirty) {
		t.Fatalf("dirty Git baseline admitted a Coder: %v", err)
	}
	var sessions int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE workspace_id=?`, workspace.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("dirty baseline preflight created %d sessions: %v", sessions, err)
	}
}

func TestPipelineIsolatedCodeEvidenceTracksNewFilesWithoutMutatingSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "README.md"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	s, db, _ := setup(t)
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Criar código", Objective: "Criar novo.go"})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder"})
	if err != nil {
		t.Fatal(err)
	}
	links, err := db.ListPipelineSessions(t.Context(), run.ID, "coder")
	if err != nil || len(links) != 1 {
		t.Fatalf("read isolated Code session: %+v %v", links, err)
	}
	execution, err := s.executionSnapshotForLink(links[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execution.Root, "README.md"), []byte("tracked change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execution.Root, "novo.go"), []byte("package main\n// arquivo novo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Append(t.Context(), coder.Session.ID, "agent_session", "tool.completed", map[string]any{
		"toolCallId": "write-readme",
		"name":       "write",
		"details":    map[string]string{"diff": "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n+tracked change"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Append(t.Context(), coder.Session.ID, "agent_session", "run.completed", map[string]string{"reason": ""}); err != nil {
		t.Fatal(err)
	}
	run, err = s.CompletePipelineCode(run.ID)
	if err != nil || run.CurrentStage != "code" || run.StageStatus["code"] != "waiting_user" || !strings.Contains(run.Artifacts["code"].Content, "arquivo novo") {
		t.Fatalf("isolated new file missing from Code evidence: %+v %v", run, err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "README.md")); err != nil || string(got) != "baseline\n" {
		t.Fatalf("Code changed original tracked file: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "novo.go")); !os.IsNotExist(err) {
		t.Fatalf("Code created a file in the original workspace: %v", err)
	}
}

func decidePipelineStageReviewForTest(t *testing.T, service *Service, run PipelineDTO, stage, decision, feedback string) PipelineDTO {
	t.Helper()
	artifact := run.Artifacts[stage]
	requestID := fmt.Sprintf("review_%s_v%d_%s", stage, artifact.Version, decision)
	updated, err := service.DecidePipelineExecutionArtifact(DecidePipelineExecutionArtifactInput{PipelineID: run.ID, RequestID: requestID, Stage: stage,
		ArtifactVersion: artifact.Version, ArtifactDigest: artifact.ContentDigest, Decision: decision, Feedback: feedback, PipelineRevision: run.Revision})
	if err != nil {
		t.Fatalf("decide %s review %s: %v", stage, decision, err)
	}
	return updated
}

func pipelineCodeReviewFixture(t *testing.T) (*Service, PipelineDTO) {
	t.Helper()
	s, db, _ := setup(t)
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
		run, err = s.AdvancePipeline(run.ID)
		if err != nil {
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
	if err := os.WriteFile(filepath.Join(snapshot.Root, "index.html"), []byte("<h1>TODO implementation</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Append(t.Context(), coder.Session.ID, "agent_session", "run.completed", map[string]string{"reason": ""}); err != nil {
		t.Fatal(err)
	}
	updated, err := s.CompletePipelineCode(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, updated
}

func TestPipelineCodeCompletionWaitsForReviewDecision(t *testing.T) {
	s, updated := pipelineCodeReviewFixture(t)
	if updated.CurrentStage != "code" || updated.StageStatus["code"] != "waiting_user" || updated.Artifacts["code"].Content == "" {
		t.Fatalf("Code advanced without a human decision: stage=%s status=%s artifact=%+v", updated.CurrentStage, updated.StageStatus["code"], updated.Artifacts["code"])
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: updated.ID, BackendID: "local", Role: "evaluator"}); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("Evaluator started before Code approval: %v", err)
	}
}

func TestPipelineCodeReviewDecisionBindsVersionHashAndOpensEval(t *testing.T) {
	s, updated := pipelineCodeReviewFixture(t)
	artifact := updated.Artifacts["code"]
	stale := DecidePipelineExecutionArtifactInput{PipelineID: updated.ID, RequestID: "approve_code_stale_01", Stage: "code", ArtifactVersion: artifact.Version,
		ArtifactDigest: strings.Repeat("0", 64), Decision: "approve", PipelineRevision: updated.Revision}
	if _, err := s.DecidePipelineExecutionArtifact(stale); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("stale Code digest was approved: %v", err)
	}
	decision := stale
	decision.RequestID = "approve_code_exact_01"
	decision.ArtifactDigest = artifact.ContentDigest
	approved, err := s.DecidePipelineExecutionArtifact(decision)
	if err != nil || approved.CurrentStage != "eval" || approved.StageStatus["code"] != "completed" || approved.StageStatus["eval"] != "active" || len(approved.ExecutionReviews) != 1 {
		t.Fatalf("exact Code approval: %+v %v", approved, err)
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: updated.ID, BackendID: "local", Role: "evaluator"}); err != nil {
		t.Fatalf("Evaluator did not start after Code approval: %v", err)
	}
	replayed, err := s.DecidePipelineExecutionArtifact(decision)
	if err != nil || replayed.CurrentStage != approved.CurrentStage || replayed.Revision != approved.Revision {
		t.Fatalf("Code approval replay was not idempotent: %+v %v", replayed, err)
	}
	conflict := decision
	conflict.Decision = "request_revision"
	conflict.Feedback = "Changed decision payload"
	if _, err := s.DecidePipelineExecutionArtifact(conflict); !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("changed request replay was admitted: %v", err)
	}
}

func TestPipelineCodeReviewRequestReturnsToCodeWithDurableFeedback(t *testing.T) {
	s, pendingReview := pipelineCodeReviewFixture(t)
	artifact := pendingReview.Artifacts["code"]
	revised, err := s.DecidePipelineExecutionArtifact(DecidePipelineExecutionArtifactInput{PipelineID: pendingReview.ID, RequestID: "revise_code_request_01", Stage: "code", ArtifactVersion: artifact.Version,
		ArtifactDigest: artifact.ContentDigest, Decision: "request_revision", Feedback: "Keep the single-file layout and add keyboard activation.", PipelineRevision: pendingReview.Revision})
	if err != nil || revised.CurrentStage != "code" || revised.StageStatus["code"] != "active" || revised.StageStatus["eval"] != "pending" {
		t.Fatalf("Code revision decision: %+v %v", revised, err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: pendingReview.Artifacts["code"].SourceSessionID, Text: "continue"}); ErrorCode(err) != "pipeline_code_session_closed" {
		t.Fatalf("rejected Code session remained writable: %v", err)
	}
	newCoder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: revised.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil || !strings.Contains(newCoder.Prompt, "Keep the single-file layout and add keyboard activation.") {
		t.Fatalf("new Coder did not receive durable human feedback: %+v %v", newCoder, err)
	}
}

func TestLegacyCodeWithoutReviewCanBeReopenedWithoutApprovingIt(t *testing.T) {
	for _, prior := range []struct {
		name       string
		current    string
		evalStatus sdd.Status
	}{
		{name: "evaluating", current: "eval", evalStatus: sdd.Active},
		{name: "already closed", current: "", evalStatus: sdd.Completed},
	} {
		t.Run(prior.name, func(t *testing.T) {
			s, waiting := pipelineCodeReviewFixture(t)
			db := s.store.(*sqlite.Store)
			status, err := json.Marshal(map[sdd.Stage]sdd.Status{sdd.Discovery: sdd.Completed, sdd.Spec: sdd.Completed, sdd.Plan: sdd.Completed, sdd.Code: sdd.Completed, sdd.Eval: prior.evalStatus})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage=?,stage_status=?,revision=revision+1 WHERE id=?`, prior.current, status, waiting.ID); err != nil {
				t.Fatal(err)
			}
			const oldEval = `{"passed":true,"findings":[],"criteria":[{"criterion":"TODO","evidence":"index.html"}]}`
			if _, err := db.DB().ExecContext(t.Context(), `INSERT INTO pipeline_artifacts(pipeline_id,stage,version,content,author,source_session_id,updated_at) VALUES(?,?,?,?,?,?,?)`, waiting.ID, sdd.Eval, 1, oldEval, "ai", "evaluator-old", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			legacy, err := s.GetPipeline(waiting.ID)
			if err != nil || legacy.CurrentStage != prior.current || legacy.Artifacts["code"].ReviewStatus != "legacy_unreviewed" {
				t.Fatalf("legacy pipeline fixture: %+v %v", legacy, err)
			}
			code := legacy.Artifacts["code"]
			input := ReopenPipelineCodeReviewInput{PipelineID: legacy.ID, ArtifactVersion: code.Version, ArtifactDigest: code.ContentDigest, PipelineRevision: legacy.Revision}
			reopened, err := s.ReopenPipelineCodeReview(input)
			if err != nil || reopened.CurrentStage != "code" || reopened.StageStatus["code"] != "waiting_user" || reopened.StageStatus["eval"] != "pending" ||
				reopened.Artifacts["eval"].Content != oldEval || reopened.Artifacts["eval"].ReviewStatus != "stale" || len(reopened.ExecutionReviews) != 0 {
				t.Fatalf("legacy Code was not reopened for explicit review: %+v %v", reopened, err)
			}
			replayed, err := s.ReopenPipelineCodeReview(input)
			if err != nil || replayed.Revision != reopened.Revision || replayed.CurrentStage != "code" {
				t.Fatalf("legacy review recovery replay was not idempotent: %+v %v", replayed, err)
			}
			stale := input
			stale.ArtifactDigest = strings.Repeat("0", 64)
			if _, err := s.ReopenPipelineCodeReview(stale); !errors.Is(err, sdd.ErrInvalidTransition) {
				t.Fatalf("recovery accepted a changed Code artifact: %v", err)
			}
		})
	}
}

func TestLegacyCodeArtifactReopensForReviewWithoutAutoApproval(t *testing.T) {
	s, terminal := pipelineCodeReviewFixture(t)
	db := s.store.(*sqlite.Store)
	status, err := json.Marshal(map[sdd.Stage]sdd.Status{sdd.Discovery: sdd.Completed, sdd.Spec: sdd.Completed, sdd.Plan: sdd.Completed, sdd.Code: sdd.Completed, sdd.Eval: sdd.Completed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='',stage_status=?,revision=revision+1 WHERE id=?`, status, terminal.ID); err != nil {
		t.Fatal(err)
	}
	const oldEval = `{"passed":true,"findings":[],"criteria":[{"criterion":"TODO","evidence":"index.html"}]}`
	if _, err := db.DB().ExecContext(t.Context(), `INSERT INTO pipeline_artifacts(pipeline_id,stage,version,content,author,source_session_id,updated_at) VALUES(?,?,?,?,?,?,?)`, terminal.ID, sdd.Eval, 1, oldEval, "ai", "evaluator-old", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.GetPipeline(terminal.ID)
	if err != nil || legacy.CurrentStage != "" || legacy.Artifacts["code"].ReviewStatus != "legacy_unreviewed" {
		t.Fatalf("legacy terminal pipeline fixture: %+v %v", legacy, err)
	}
	code := legacy.Artifacts["code"]
	input := ReopenPipelineCodeReviewInput{PipelineID: legacy.ID, ArtifactVersion: code.Version, ArtifactDigest: code.ContentDigest, PipelineRevision: legacy.Revision}
	reopened, err := s.ReopenPipelineCodeReview(input)
	if err != nil || reopened.CurrentStage != "code" || reopened.StageStatus["code"] != "waiting_user" || reopened.StageStatus["eval"] != "pending" ||
		reopened.Artifacts["eval"].Content != oldEval || reopened.Artifacts["eval"].ReviewStatus != "stale" || len(reopened.ExecutionReviews) != 0 {
		t.Fatalf("legacy terminal output was not reopened for human review: %+v %v", reopened, err)
	}
	replayed, err := s.ReopenPipelineCodeReview(input)
	if err != nil || replayed.Revision != reopened.Revision || replayed.CurrentStage != "code" {
		t.Fatalf("legacy review recovery replay was not idempotent: %+v %v", replayed, err)
	}
	stale := input
	stale.ArtifactDigest = strings.Repeat("0", 64)
	if _, err := s.ReopenPipelineCodeReview(stale); !errors.Is(err, sdd.ErrInvalidTransition) {
		t.Fatalf("recovery accepted a changed Code artifact: %v", err)
	}
}

func TestLegacyCodeRecoveryStateExplainsMissingAppliedAndDriftedSnapshots(t *testing.T) {
	for _, scenario := range []struct {
		name string
		want string
	}{
		{name: "missing snapshot", want: "snapshot_unavailable"},
		{name: "already applied", want: "already_applied"},
		{name: "source drift", want: "source_drift"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, terminal := pipelineCodeReviewFixture(t)
			db := s.store.(*sqlite.Store)
			links, err := db.ListPipelineSessions(t.Context(), terminal.ID, "coder")
			if err != nil || len(links) != 1 {
				t.Fatalf("read Code execution link: %+v %v", links, err)
			}
			snapshot, err := s.executionSnapshotForLink(links[0])
			if err != nil {
				t.Fatal(err)
			}
			workspace, err := s.store.GetWorkspace(t.Context(), terminal.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.name == "missing snapshot" {
				if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_sessions SET execution_snapshot='' WHERE pipeline_id=? AND session_id=?`, terminal.ID, links[0].SessionID); err != nil {
					t.Fatal(err)
				}
			} else {
				contents, err := os.ReadFile(filepath.Join(snapshot.Root, "index.html"))
				if err != nil {
					t.Fatal(err)
				}
				if scenario.name == "source drift" {
					contents = []byte("unrelated workspace edit\n")
				}
				if err := os.WriteFile(filepath.Join(workspace.Path, "index.html"), contents, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			status, err := json.Marshal(map[sdd.Stage]sdd.Status{sdd.Discovery: sdd.Completed, sdd.Spec: sdd.Completed, sdd.Plan: sdd.Completed, sdd.Code: sdd.Completed, sdd.Eval: sdd.Active})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='eval',stage_status=?,revision=revision+1 WHERE id=?`, status, terminal.ID); err != nil {
				t.Fatal(err)
			}
			current, err := s.GetPipeline(terminal.ID)
			if err != nil || current.CodeReviewRecoveryStatus != scenario.want || strings.TrimSpace(current.CodeReviewRecoveryReason) == "" {
				t.Fatalf("Code recovery state = %q (%q), want %q: %v", current.CodeReviewRecoveryStatus, current.CodeReviewRecoveryReason, scenario.want, err)
			}
			if scenario.name == "missing snapshot" {
				code := current.Artifacts["code"]
				_, err := s.ReopenPipelineCodeReview(ReopenPipelineCodeReviewInput{PipelineID: current.ID, ArtifactVersion: code.Version, ArtifactDigest: code.ContentDigest, PipelineRevision: current.Revision})
				if !errors.Is(err, ErrPipelineCodeSnapshotUnavailable) {
					t.Fatalf("missing snapshot reopened Code review: %v", err)
				}
			}
		})
	}
}

func TestPipelineDTOMarksApprovedEvalStaleAfterNewCodeDecision(t *testing.T) {
	evalCreated := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	codeApproved := evalCreated.Add(time.Minute)
	run := catalog.PipelineRun{
		ID: "pipeline-stale-eval", WorkspaceID: "workspace", Current: sdd.Eval, Revision: 12,
		Status: map[sdd.Stage]sdd.Status{sdd.Discovery: sdd.Completed, sdd.Spec: sdd.Completed, sdd.Plan: sdd.Completed, sdd.Code: sdd.Completed, sdd.Eval: sdd.Active},
		Artifacts: map[sdd.Stage]catalog.PipelineArtifact{
			sdd.Code: {Stage: sdd.Code, Version: 2, Content: "new approved Code", Author: "ai", SourceSessionID: "coder-new", UpdatedAt: codeApproved},
			sdd.Eval: {Stage: sdd.Eval, Version: 1, Content: `{"passed":true}`, Author: "ai", SourceSessionID: "evaluator-old", UpdatedAt: evalCreated},
		},
		ExecutionReviews: []catalog.PipelineExecutionReview{
			{Stage: sdd.Eval, ArtifactVersion: 1, Decision: "approve", Actor: "local_user", CreatedAt: evalCreated.Add(time.Second)},
			{Stage: sdd.Code, ArtifactVersion: 2, Decision: "approve", Actor: "local_user", CreatedAt: codeApproved},
		},
	}
	dto := pipelineDTO(run)
	if status := dto.Artifacts["eval"].ReviewStatus; status != "stale" {
		t.Fatalf("prior approved Eval review status = %q, want stale", status)
	}
	fresh := run
	fresh.Status = map[sdd.Stage]sdd.Status{sdd.Discovery: sdd.Completed, sdd.Spec: sdd.Completed, sdd.Plan: sdd.Completed, sdd.Code: sdd.Completed, sdd.Eval: sdd.WaitingUser}
	fresh.Artifacts = map[sdd.Stage]catalog.PipelineArtifact{}
	for stage, artifact := range run.Artifacts {
		fresh.Artifacts[stage] = artifact
	}
	newEval := fresh.Artifacts[sdd.Eval]
	newEval.UpdatedAt = codeApproved.Add(time.Second)
	fresh.Artifacts[sdd.Eval] = newEval
	fresh.ExecutionReviews = []catalog.PipelineExecutionReview{{Stage: sdd.Code, ArtifactVersion: 2, Decision: "approve", Actor: "local_user", CreatedAt: codeApproved}}
	if status := pipelineDTO(fresh).Artifacts["eval"].ReviewStatus; status != "waiting_user" {
		t.Fatalf("fresh Eval review status = %q, want waiting_user", status)
	}
}

func TestPipelineEvaluationRejectsObjectiveAsCriteriaWhenSpecWasSkipped(t *testing.T) {
	s, db, _ := setup(t)
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "TODO", Objective: "Basta criar um rascunho simples"})
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: "discovery", Content: "O TODO deve permitir adicionar, concluir e remover tarefas, filtrar estados e persistir em localStorage."})
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.AdvancePipeline(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID, Reason: "SPEC detalhada ainda não foi necessária"})
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: "plan", Content: "Implementar e validar o arquivo HTML."})
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.AdvancePipeline(run.ID)
	if err != nil {
		t.Fatal(err)
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
	if err := os.WriteFile(filepath.Join(snapshot.Root, "index.html"), []byte("<!-- TODO pronto -->\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Append(t.Context(), coder.Session.ID, "agent_session", "run.completed", map[string]string{"reason": ""}); err != nil {
		t.Fatal(err)
	}
	run, err = s.CompletePipelineCode(run.ID)
	if err != nil || run.CurrentStage != "code" || run.StageStatus["code"] != "waiting_user" {
		t.Fatalf("complete Code: %+v %v", run, err)
	}
	run = decidePipelineStageReviewForTest(t, s, run, "code", "approve", "")
	evaluator, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "evaluator"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evaluator.Prompt, "Critérios de aceite autorizados: DISCOVERY integral, versão 1") || !strings.Contains(evaluator.Prompt, "O objetivo e o título são contexto e não substituem os critérios de aceite") {
		t.Fatalf("Evaluator prompt does not identify its authorized Discovery source: %s", evaluator.Prompt)
	}
	verdict := `{"passed":true,"findings":[],"criteria":[{"criterion":"Basta criar um rascunho simples","evidence":"TODO pronto"}]}`
	if _, err := db.Append(t.Context(), evaluator.Session.ID, "agent_session", "message.assistant", map[string]string{"content": verdict}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Append(t.Context(), evaluator.Session.ID, "agent_session", "run.completed", map[string]string{"reason": ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompletePipelineEvaluation(run.ID); !errors.Is(err, sdd.ErrEvidenceRequired) {
		t.Fatalf("Evaluator accepted the objective summary after SPEC was skipped: %v", err)
	}
}

func TestPipelineAPICoderRejectsDirtySourceBeforeCreatingSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "README.md"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("pre-existing dirty change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "preexisting-untracked.txt"), []byte("baseline untracked content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Criar API", Objective: "Criar arquivo com o Coder API"})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true}); !errors.Is(err, ErrPipelineGitDirty) {
		t.Fatalf("API Coder admitted a dirty source: %v", err)
	}
	var sessions int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE workspace_id=?`, workspace.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("dirty source preflight created %d sessions: %v", sessions, err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "README.md")); err != nil || string(got) != "pre-existing dirty change\n" {
		t.Fatalf("dirty source was modified: %q %v", got, err)
	}
}

func TestPipelineAPICoderRefusesLargeDirtyGitSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	large := strings.Repeat("a", 300*1024)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(large), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "README.md"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	_, db, vault := setup(t)
	provider := &fakeProvider{}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, ProviderFactory: func(c openai.Config) (agentcore.Provider, error) { return provider, nil }, ExecutionCacheRoot: t.TempDir()})
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(strings.Repeat("b", 300*1024)), 0o600); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := repositories.Inspect(t.Context(), root); err != nil || !snapshot.Truncated {
		t.Fatalf("baseline fixture is not truncated: %+v %v", snapshot, err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Criar API", Objective: "Criar TODO"})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		run, err = s.SkipPipelineStage(SkipPipelineStageInput{PipelineID: run.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true}); ErrorCode(err) != "pipeline_git_dirty" {
		t.Fatalf("dirty Git state was accepted as an API Coder source: %v", err)
	}
}

func TestPipelineUsesCoderEvidenceAndIndependentReadOnlyEvaluator(t *testing.T) {
	_, db, vault := setup(t)
	provider := &fakeProvider{}
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
		run, err = s.AdvancePipeline(run.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil || coder.Session.ID == "" || !strings.Contains(coder.Prompt, "spec critérios") {
		t.Fatalf("coder session: %+v %v", coder, err)
	}
	coderLinks, err := db.ListPipelineSessions(t.Context(), run.ID, "coder")
	if err != nil || len(coderLinks) != 1 {
		t.Fatalf("read isolated coder session: %+v %v", coderLinks, err)
	}
	execution, err := s.executionSnapshotForLink(coderLinks[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execution.Root, "export.go"), []byte("package export\nfunc ExportarCSV() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedRun, err := s.GetPipelineForSession(GetPipelineForSessionInput{SessionID: coder.Session.ID, WorkspaceID: workspace.ID})
	if err != nil || linkedRun.ID != run.ID || linkedRun.Artifacts["spec"].Content != "spec critérios e contexto" {
		t.Fatalf("coder session lost linked SPEC: %+v %v", linkedRun, err)
	}
	otherWorkspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPipelineForSession(GetPipelineForSessionInput{SessionID: coder.Session.ID, WorkspaceID: otherWorkspace.ID}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("linked SPEC crossed workspace boundary: %v", err)
	}
	direct, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPipelineForSession(GetPipelineForSessionInput{SessionID: direct.ID, WorkspaceID: workspace.ID}); !errors.Is(err, ErrPipelineNotFound) {
		t.Fatalf("unlinked session returned a pipeline: %v", err)
	}
	for _, event := range []struct {
		kind string
		data any
	}{
		{"run.started", map[string]any{}},
		{"message.assistant", map[string]any{"role": "assistant", "content": "CODER_JUSTIFICATION_ONLY"}},
		{"tool.completed", map[string]any{"toolCallId": "tool-1", "name": "edit", "content": map[string]any{"text": "ok"}, "details": map[string]any{"diff": "+func ExportarCSV() {}"}}},
		{"run.completed", map[string]any{"reason": ""}},
	} {
		if _, err := db.Append(t.Context(), coder.Session.ID, "agent_session", event.kind, event.data); err != nil {
			t.Fatal(err)
		}
	}
	run, err = s.CompletePipelineCode(run.ID)
	if err != nil || run.CurrentStage != "code" || run.StageStatus["code"] != "waiting_user" {
		t.Fatalf("Code evidence bypassed its review gate: %+v %v", run, err)
	}
	run = decidePipelineStageReviewForTest(t, s, run, "code", "approve", "")
	evaluator, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "evaluator"})
	if err != nil || !strings.Contains(evaluator.Prompt, "+func ExportarCSV") || strings.Contains(evaluator.Prompt, "CODER_JUSTIFICATION_ONLY") {
		t.Fatalf("evaluator context: %+v %v", evaluator, err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: evaluator.Session.ID, Text: evaluator.Prompt}); err != nil {
		t.Fatal(err)
	}
	// QA runs commands in its own lab, but never edits files with the Coder's tools.
	shell := false
	for _, tool := range provider.request.Tools {
		if tool.Name == "write" || tool.Name == "edit" {
			t.Fatalf("evaluator received a file-editing tool: %s", tool.Name)
		}
		shell = shell || tool.Name == "bash"
	}
	if !shell {
		t.Fatal("the QA lab has no terminal")
	}
	if _, err := db.Append(t.Context(), evaluator.Session.ID, "agent_session", "message.assistant", map[string]any{"role": "assistant", "content": `{"passed":true,"findings":[]}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Append(t.Context(), evaluator.Session.ID, "agent_session", "run.completed", map[string]any{"reason": ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompletePipelineEvaluation(run.ID); !errors.Is(err, sdd.ErrEvidenceRequired) {
		t.Fatalf("passed without criterion/evidence matrix advanced eval: %v", err)
	}
	for _, invalid := range []string{
		`{"passed":true,"findings":[],"criteria":[{"criterion":"inventado","evidence":"ExportarCSV"}]}`,
		`{"passed":true,"findings":[],"criteria":[{"criterion":"critérios","evidence":"não existe"}]}`,
		`{"passed":true,"findings":["pendente"],"criteria":[{"criterion":"critérios","evidence":"ExportarCSV"}]}`,
		`{"passed":true,"findings":[],"criteria":[{"criterion":"critérios","evidence":"ExportarCSV"}],"unexpected":true}`,
	} {
		if _, err := db.Append(t.Context(), evaluator.Session.ID, "agent_session", "message.assistant", map[string]any{"role": "assistant", "content": invalid}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CompletePipelineEvaluation(run.ID); !errors.Is(err, sdd.ErrEvidenceRequired) {
			t.Fatalf("invalid criterion/evidence matrix advanced eval: %s %v", invalid, err)
		}
	}
	if _, err := db.Append(t.Context(), evaluator.Session.ID, "agent_session", "message.assistant", map[string]any{"role": "assistant", "content": `{"passed":true,"findings":[],"criteria":[{"criterion":"critérios","evidence":"ExportarCSV"}]}`}); err != nil {
		t.Fatal(err)
	}
	run, err = s.CompletePipelineEvaluation(run.ID)
	if err != nil || run.CurrentStage != "eval" || run.StageStatus["eval"] != "waiting_user" {
		t.Fatalf("Eval evidence bypassed its review gate: %+v %v", run, err)
	}
	run = decidePipelineStageReviewForTest(t, s, run, "eval", "approve", "")
	if run.CurrentStage != "prs" || run.StageStatus["eval"] != "completed" || run.StageStatus["prs"] != "active" {
		t.Fatalf("approved evaluation did not hand the pipeline to the pull request stage: %+v", run)
	}
}
