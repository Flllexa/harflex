package application

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

// workFolderPipeline takes a pipeline through one Coder round and a QA that fails, so a fix round follows.
func workFolderPipeline(t *testing.T, provider *qaLabProvider) (*Service, string, string) {
	t.Helper()
	_, db, vault := setup(t)
	cache, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }, ExecutionCacheRoot: cache})
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
	for _, stage := range []string{"discovery", "spec", "brainstorm", "plan"} {
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
	if result, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil || result.Status != RunCompleted {
		t.Fatalf("coder: %+v %v", result, err)
	}
	if _, err := coderSaidDone(t, s, run.ID).CompletePipelineCode(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.decideLatest(run.ID, "code", "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPipelineQA(StartPipelineQAInput{PipelineID: run.ID, Evaluator: PipelineRoleChoice{BackendID: "local"}}); err != nil {
		t.Fatal(err)
	}
	waitQALoop(t, s, run.ID)
	return s, run.ID, cache
}

// folderNames lists a directory, waiting briefly for removals that finish in the background.
func folderNames(t *testing.T, dir string, want int) []string {
	t.Helper()
	var names []string
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		names = names[:0]
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		if len(names) == want || time.Now().After(deadline) {
			return names
		}
	}
}

func TestAPipelineWorksInOneFolderThatKeepsOnlyTheLatestQALab(t *testing.T) {
	s, pipelineID, cache := workFolderPipeline(t, &qaLabProvider{})
	folder := filepath.Join(cache, "pipeline-"+pipelineID)
	coders, _ := s.store.ListPipelineSessions(t.Context(), pipelineID, "coder")
	code, err := s.executionSnapshotForLink(coders[0])
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(code.Root) != folder {
		t.Fatalf("the Code copy is not in the pipeline's folder: %s", code.Root)
	}
	evaluators, _ := s.store.ListPipelineSessions(t.Context(), pipelineID, "evaluator")
	firstLab := qaLabRoot(code.Root, evaluators[0].ID)

	if _, err := s.FixPipelineFindings(FixPipelineFindingsInput{PipelineID: pipelineID, Findings: []string{"O CSV sai sem cabeçalho"}, Coder: PipelineRoleChoice{BackendID: "local"}, Evaluator: PipelineRoleChoice{BackendID: "local"}}); err != nil {
		t.Fatal(err)
	}
	if state := waitQALoop(t, s, pipelineID); state.Phase != "done" {
		t.Fatalf("fix loop: %+v", state)
	}
	if names := folderNames(t, cache, 1); len(names) != 1 || names[0] != "pipeline-"+pipelineID {
		t.Fatalf("the cache holds more than the pipeline's folder: %v", names)
	}
	evaluators, _ = s.store.ListPipelineSessions(t.Context(), pipelineID, "evaluator")
	want := []string{filepath.Base(code.Root), filepath.Base(qaLabRoot(code.Root, evaluators[0].ID))}
	sort.Strings(want)
	if names := folderNames(t, folder, 2); strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("the pipeline's folder holds %v, want the Code copy and the latest lab %v", names, want)
	}
	if _, err := os.Stat(firstLab); !os.IsNotExist(err) {
		t.Fatalf("the first QA lab is still there: %v", err)
	}
}

func TestAcceptedPullRequestsRemoveTheWorkFolderAndKeepTheConversations(t *testing.T) {
	s, pipelineID, cache := workFolderPipeline(t, &qaLabProvider{passFirst: true})
	if err := s.decideLatest(pipelineID, "eval", "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyPipelineCode(pipelineID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: mustPipeline(t, s, pipelineID).WorkspaceID, Profile: "full_access", ConfirmFullAccess: true}); err != nil {
		t.Fatal(err)
	}
	publisher, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: pipelineID, BackendID: "local", Role: "publisher"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: publisher.Session.ID, Text: publisher.Prompt}); err != nil || result.Status != RunCompleted {
		t.Fatalf("publisher: %+v %v", result, err)
	}
	register := harflexToolNamed(t, s.pullRequestTools(pipelineID, publisher.Session.ID), "harflex_register_pull_request")
	if _, err := register.Execute(t.Context(), json.RawMessage(`{"url":"https://github.com/acme/todo/pull/7","title":"Exportar CSV","branch":"harflex/exportar-csv"}`), nil); err != nil {
		t.Fatal(err)
	}
	coders, _ := s.store.ListPipelineSessions(t.Context(), pipelineID, "coder")
	if names := folderNames(t, filepath.Join(cache, "pipeline-"+pipelineID), 2); len(names) != 2 {
		t.Fatalf("before the PRs are accepted the folder holds the copy and the lab: %v", names)
	}
	report := harflexToolNamed(t, s.pullRequestTools(pipelineID, publisher.Session.ID), "harflex_pull_request_status")
	if _, err := report.Execute(t.Context(), json.RawMessage(`{"url":"https://github.com/acme/todo/pull/7","state":"merged","summary":"Mergeado"}`), nil); err != nil {
		t.Fatal(err)
	}
	// What the watch does after its check: every PR merged ends the stage.
	s.finishPullRequestsWhenDone(pipelineID)
	var run PipelineDTO
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if run, _ = s.GetPipeline(pipelineID); run.StageStatus["prs"] == "completed" {
			break
		}
	}
	if run.StageStatus["prs"] != "completed" {
		t.Fatalf("the PRs stage did not end: %+v", run.StageStatus)
	}
	if names := folderNames(t, cache, 0); len(names) != 0 {
		t.Fatalf("the work folder survived the accepted PRs: %v", names)
	}
	// The conversation is history in the catalog: it still reads, without its folder.
	events, err := s.ListEvents(ListEventsInput{SessionID: coders[0].SessionID})
	if err != nil || len(events) == 0 {
		t.Fatalf("the Coder's conversation is gone with the folder: %d %v", len(events), err)
	}
	s.mu.Lock()
	delete(s.sessions, coders[0].SessionID)
	s.mu.Unlock()
	if _, err := s.OpenSession(OpenSessionInput{SessionID: coders[0].SessionID, WorkspaceID: run.WorkspaceID}); err != nil {
		t.Fatalf("the Coder's conversation no longer opens: %v", err)
	}
}

func TestTheStartupSweepRemovesCopiesAPipelineNoLongerUses(t *testing.T) {
	s, pipelineID, cache := workFolderPipeline(t, &qaLabProvider{passFirst: true})
	coders, _ := s.store.ListPipelineSessions(t.Context(), pipelineID, "coder")
	evaluators, _ := s.store.ListPipelineSessions(t.Context(), pipelineID, "evaluator")
	code, _ := s.executionSnapshotForLink(coders[0])
	latestLab := qaLabRoot(code.Root, evaluators[0].ID)
	// What earlier versions left behind: an older QA lab of this work, beside the copies, and a removal cut short.
	stale := catalog.PipelineSession{ID: "stale-lab", PipelineID: pipelineID, SessionID: evaluators[0].SessionID + "-old", Role: "evaluator", ExecutionSnapshot: coders[0].ExecutionSnapshot, CreatedAt: evaluators[0].CreatedAt.Add(-time.Hour)}
	old, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: mustPipeline(t, s, pipelineID).WorkspaceID, BackendID: "local", Reason: "QA antigo"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SessionID = old.ID
	if err := s.store.LinkPipelineSession(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	staleLab := qaLabRoot(code.Root, stale.ID)
	unknown := filepath.Join(cache, "sdd-code-unknown")
	leftover := filepath.Join(cache, ".removing-123")
	for _, dir := range []string{staleLab, unknown, filepath.Join(leftover, "node_modules")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A dependency cache keeps its folders read-only; the removal still gets through.
	readOnly := filepath.Join(staleLab, "go", "pkg", "mod")
	if err := os.MkdirAll(readOnly, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readOnly, "go.mod"), []byte("module x\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}

	s.SweepPipelineCopies()

	for _, gone := range []string{staleLab, leftover} {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(gone); os.IsNotExist(err) {
				break
			}
		}
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s survived the sweep: %v", filepath.Base(gone), err)
		}
	}
	for _, kept := range []string{code.Root, latestLab, unknown} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("the sweep removed %s: %v", filepath.Base(kept), err)
		}
	}
	if names := folderNames(t, cache, 2); strings.Join(names, ",") != "pipeline-"+pipelineID+",sdd-code-unknown" {
		t.Fatalf("cache after the sweep: %v", names)
	}
}

func mustPipeline(t *testing.T, s *Service, pipelineID string) PipelineDTO {
	t.Helper()
	run, err := s.GetPipeline(pipelineID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestApplyRebuildsACodeCopyTheSystemCleared(t *testing.T) {
	s, pipelineID, _ := workFolderPipeline(t, &qaLabProvider{passFirst: true})
	if err := s.decideLatest(pipelineID, "eval", "approve", ""); err != nil {
		t.Fatal(err)
	}
	coders, _ := s.store.ListPipelineSessions(t.Context(), pipelineID, "coder")
	code, err := s.executionSnapshotForLink(coders[0])
	if err != nil {
		t.Fatal(err)
	}
	// macOS empties app caches when the disk runs low; the approved change is still saved with the pipeline.
	if err := os.RemoveAll(code.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyPipelineCode(pipelineID); err != nil {
		t.Fatalf("apply after the copy vanished: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(code.SourceRoot, "export.go"))
	if err != nil || !strings.Contains(string(body), "ExportarCSV") {
		t.Fatalf("the approved change did not reach the project: %q %v", body, err)
	}
}
