package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

// approvedPipeline walks a pipeline through the real Code and QA gates and returns it at the pull request stage.
func approvedPipeline(t *testing.T) (*Store, string, catalog.PipelineRun) {
	t.Helper()
	store, path, run := authoringFixture(t)
	run.Kind = "legacy"
	run.Current = sdd.Code
	run.Status = map[sdd.Stage]sdd.Status{sdd.Discovery: sdd.Completed, sdd.Spec: sdd.Completed, sdd.Plan: sdd.Completed, sdd.Code: sdd.Active, sdd.Eval: sdd.Pending, sdd.PRs: sdd.Pending}
	if err := store.CreatePipeline(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	session := func(id, role string) {
		t.Helper()
		if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: id, WorkspaceID: run.WorkspaceID, BackendID: "local", Status: "completed", CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}, nil, "", nil, "agent_session", nil); err != nil {
			t.Fatal(err)
		}
		if err := store.LinkPipelineSession(t.Context(), catalog.PipelineSession{ID: "link-" + id, PipelineID: run.ID, SessionID: id, Role: role, CreatedAt: run.CreatedAt}); err != nil {
			t.Fatal(err)
		}
	}
	decide := func(stage sdd.Stage, content, sessionID string) catalog.PipelineRun {
		t.Helper()
		current, err := store.GetPipeline(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SavePipelineExecutionArtifact(t.Context(), run.ID, stage, content, sessionID, current.Revision); err != nil {
			t.Fatal(err)
		}
		pending, err := store.GetPipeline(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(content))
		approved, err := store.DecidePipelineExecutionArtifact(t.Context(), catalog.PipelineExecutionReviewRequest{PipelineID: run.ID, RequestID: fmt.Sprintf("approve_%s_000001", stage), Stage: stage,
			ArtifactVersion: 1, ArtifactDigest: hex.EncodeToString(digest[:]), Decision: "approve", ExpectedRevision: pending.Revision})
		if err != nil {
			t.Fatal(err)
		}
		return approved
	}
	session("coder-session", "coder")
	if approved := decide(sdd.Code, "diff --git a/index.html b/index.html\n+<h1>TODO</h1>\n", "coder-session"); approved.Current != sdd.Eval {
		t.Fatalf("Code approval: %+v", approved)
	}
	session("qa-session", "evaluator")
	approved := decide(sdd.Eval, `{"passed":true,"findings":[],"criteria":[{"criterion":"one page","evidence":"TODO"}]}`, "qa-session")
	return store, path, approved
}

func TestApprovingQAHandsThePipelineToThePullRequestStage(t *testing.T) {
	_, _, run := approvedPipeline(t)
	if run.Current != sdd.PRs || run.Status[sdd.Eval] != sdd.Completed || run.Status[sdd.PRs] != sdd.Active {
		t.Fatalf("after QA approval: current=%q status=%v", run.Current, run.Status)
	}
}

func TestPullRequestStageLinksItsChatAndEndsWithTheAgentsReport(t *testing.T) {
	store, path, run := approvedPipeline(t)
	chat := func(id string) {
		t.Helper()
		if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: id, WorkspaceID: run.WorkspaceID, BackendID: "local", Status: "completed", CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}, nil, "", nil, "agent_session", nil); err != nil {
			t.Fatal(err)
		}
	}
	chat("pr-chat-1")
	if err := store.LinkPipelinePRSession(t.Context(), catalog.PipelinePRSession{ID: "pr-link-1", PipelineID: run.ID, SessionID: "pr-chat-1", CreatedAt: run.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if found, err := store.GetPipelineIDForPRSession(t.Context(), "pr-chat-1"); err != nil || found != run.ID {
		t.Fatalf("pipeline of the PR chat = %q, %v", found, err)
	}
	if _, err := store.GetPipelineIDForSession(t.Context(), "pr-chat-1"); err == nil {
		t.Fatal("the PR chat leaked into the Code and QA session links")
	}

	current, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	finish := catalog.PipelinePRsFinish{PipelineID: run.ID, ExpectedRevision: current.Revision, Outcome: "completed", SessionID: "pr-chat-1", Report: "## PRs\n- https://github.com/acme/app/pull/7"}
	for name, bad := range map[string]catalog.PipelinePRsFinish{
		"a stale revision":          {PipelineID: finish.PipelineID, ExpectedRevision: current.Revision - 1, Outcome: "completed", SessionID: finish.SessionID, Report: finish.Report},
		"a blank report":            {PipelineID: finish.PipelineID, ExpectedRevision: current.Revision, Outcome: "completed", SessionID: finish.SessionID, Report: "  \n"},
		"a chat of another work":    {PipelineID: finish.PipelineID, ExpectedRevision: current.Revision, Outcome: "completed", SessionID: "coder-session", Report: finish.Report},
		"an outcome nobody defined": {PipelineID: finish.PipelineID, ExpectedRevision: current.Revision, Outcome: "failed", SessionID: finish.SessionID, Report: finish.Report},
	} {
		if err := store.FinishPipelinePRs(t.Context(), bad); err == nil {
			t.Errorf("%s ended the stage", name)
		}
	}
	untouched, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil || untouched.Current != sdd.PRs || untouched.Revision != current.Revision || untouched.Artifacts[sdd.PRs].Content != "" {
		t.Fatalf("a refused finish changed the pipeline: %+v %v", untouched, err)
	}

	if err := store.FinishPipelinePRs(t.Context(), finish); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	done, err := reopened.GetPipeline(t.Context(), run.ID)
	if err != nil || done.Current != "" || done.Status[sdd.PRs] != sdd.Completed || done.Revision != current.Revision+1 ||
		done.Artifacts[sdd.PRs].Content != finish.Report || done.Artifacts[sdd.PRs].Author != "ai" || done.Artifacts[sdd.PRs].SourceSessionID != "pr-chat-1" {
		t.Fatalf("pipeline after the pull request stage, reopened: %+v %v", done, err)
	}
	links, err := reopened.ListPipelinePRSessions(t.Context(), run.ID)
	if err != nil || len(links) != 1 || links[0].SessionID != "pr-chat-1" {
		t.Fatalf("links after reopening: %+v %v", links, err)
	}
	// Nothing else may be linked or finished once the stage ended.
	chat2 := "pr-chat-late"
	if err := reopened.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: chat2, WorkspaceID: run.WorkspaceID, BackendID: "local", Status: "ready", CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}, nil, "", nil, "agent_session", nil); err != nil {
		t.Fatal(err)
	}
	if err := reopened.LinkPipelinePRSession(t.Context(), catalog.PipelinePRSession{PipelineID: run.ID, SessionID: chat2, CreatedAt: run.CreatedAt}); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("a finished pipeline took another PR chat: %v", err)
	}
	if err := reopened.FinishPipelinePRs(t.Context(), catalog.PipelinePRsFinish{PipelineID: run.ID, ExpectedRevision: done.Revision, Outcome: "skipped"}); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("a finished pipeline was finished again: %v", err)
	}
}

func TestPullRequestStageCapsItsChats(t *testing.T) {
	store, _, run := approvedPipeline(t)
	for index := range MaxPipelinePRSessions + 1 {
		id := fmt.Sprintf("pr-chat-%d", index)
		if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: id, WorkspaceID: run.WorkspaceID, BackendID: "local", Status: "ready", CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}, nil, "", nil, "agent_session", nil); err != nil {
			t.Fatal(err)
		}
		err := store.LinkPipelinePRSession(t.Context(), catalog.PipelinePRSession{PipelineID: run.ID, SessionID: id, CreatedAt: run.CreatedAt})
		if index < MaxPipelinePRSessions && err != nil {
			t.Fatalf("chat %d refused: %v", index, err)
		}
		if index == MaxPipelinePRSessions && !errors.Is(err, ErrPipelineConflict) {
			t.Fatalf("chat beyond the limit accepted: %v", err)
		}
	}
}

func TestPullRequestStageIsClosedUntilQAIsApproved(t *testing.T) {
	store, _, run := authoringFixture(t)
	run.Kind = "legacy"
	run.Current = sdd.Eval
	run.Status = map[sdd.Stage]sdd.Status{sdd.Discovery: sdd.Completed, sdd.Spec: sdd.Completed, sdd.Plan: sdd.Completed, sdd.Code: sdd.Completed, sdd.Eval: sdd.Active, sdd.PRs: sdd.Pending}
	if err := store.CreatePipeline(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPipelinePRs(t.Context(), catalog.PipelinePRsFinish{PipelineID: run.ID, ExpectedRevision: run.Revision, Outcome: "skipped"}); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("pull requests skipped before QA approved: %v", err)
	}
	if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: "too-early", WorkspaceID: run.WorkspaceID, BackendID: "local", Status: "ready", CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}, nil, "", nil, "agent_session", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.LinkPipelinePRSession(t.Context(), catalog.PipelinePRSession{PipelineID: run.ID, SessionID: "too-early", CreatedAt: run.CreatedAt}); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("a PR chat was linked before QA approved: %v", err)
	}
}
