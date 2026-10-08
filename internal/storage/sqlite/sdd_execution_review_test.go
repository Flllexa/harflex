package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func TestPipelineExecutionReviewReceiptSurvivesStoreReopen(t *testing.T) {
	store, path, run := authoringFixture(t)
	run.Kind = "legacy"
	run.Current = sdd.Code
	run.Status = map[sdd.Stage]sdd.Status{
		sdd.Discovery: sdd.Completed,
		sdd.Spec:      sdd.Completed,
		sdd.Plan:      sdd.Completed,
		sdd.Code:      sdd.Active,
		sdd.Eval:      sdd.Pending,
	}
	if err := store.CreatePipeline(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	const sessionID = "coder-review-session"
	if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: sessionID, WorkspaceID: run.WorkspaceID, BackendID: "local", Status: "completed", CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}, nil, "", nil, "agent_session", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.LinkPipelineSession(t.Context(), catalog.PipelineSession{ID: "pipeline-code-link", PipelineID: run.ID, SessionID: sessionID, Role: "coder", CreatedAt: run.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	const content = "diff --git a/index.html b/index.html\n+<h1>TODO</h1>\n"
	if err := store.SavePipelineExecutionArtifact(t.Context(), run.ID, sdd.Code, content, sessionID, run.Revision); err != nil {
		t.Fatal(err)
	}
	pending, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil || pending.Current != sdd.Code || pending.Status[sdd.Code] != sdd.WaitingUser || pending.Revision != 2 {
		t.Fatalf("Code artifact did not persist at its review gate: %+v %v", pending, err)
	}
	digest := sha256.Sum256([]byte(content))
	review := catalog.PipelineExecutionReviewRequest{PipelineID: run.ID, RequestID: "approve_code_restart_01", Stage: sdd.Code, ArtifactVersion: 1,
		ArtifactDigest: hex.EncodeToString(digest[:]), Decision: "approve", ExpectedRevision: pending.Revision}
	approved, err := store.DecidePipelineExecutionArtifact(t.Context(), review)
	if err != nil || approved.Current != sdd.Eval || approved.Status[sdd.Code] != sdd.Completed || len(approved.ExecutionReviews) != 1 {
		t.Fatalf("Code review did not persist: %+v %v", approved, err)
	}
	requestHash, err := review.Hash()
	if err != nil {
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
	readback, err := reopened.GetPipeline(t.Context(), run.ID)
	if err != nil || readback.Current != sdd.Eval || readback.Status[sdd.Code] != sdd.Completed || len(readback.ExecutionReviews) != 1 {
		t.Fatalf("review gate did not survive reopening: %+v %v", readback, err)
	}
	receipt := readback.ExecutionReviews[0]
	if receipt.RequestID != review.RequestID || receipt.Stage != sdd.Code || receipt.ArtifactVersion != 1 || receipt.ArtifactDigest != review.ArtifactDigest ||
		receipt.ArtifactContent != content || receipt.SourceSessionID != sessionID || receipt.Decision != "approve" || receipt.Actor != "local_user" {
		t.Fatalf("durable review provenance is incomplete: %+v", receipt)
	}
	replayed, found, err := reopened.GetPipelineExecutionReviewResult(t.Context(), run.ID, review.RequestID, requestHash)
	if err != nil || !found || replayed.Current != approved.Current || replayed.Revision != approved.Revision {
		t.Fatalf("idempotent review receipt did not survive restart: %+v %v found=%v", replayed, err, found)
	}
	conflict := review
	conflict.Feedback = "Changed after approval"
	conflictHash, err := conflict.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := reopened.GetPipelineExecutionReviewResult(t.Context(), run.ID, conflict.RequestID, conflictHash); !errors.Is(err, ErrPipelineConflict) || !found {
		t.Fatalf("changed replay was not rejected after restart: found=%v err=%v", found, err)
	}
}

func TestPipelineExecutionArtifactRetainsSupersededVersion(t *testing.T) {
	store, _, run := authoringFixture(t)
	run.Kind = "legacy"
	run.Current = sdd.Eval
	run.Status = map[sdd.Stage]sdd.Status{
		sdd.Discovery: sdd.Completed,
		sdd.Spec:      sdd.Completed,
		sdd.Plan:      sdd.Completed,
		sdd.Code:      sdd.Completed,
		sdd.Eval:      sdd.Active,
	}
	if err := store.CreatePipeline(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	createEvaluator := func(sessionID, linkID string) {
		t.Helper()
		if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: sessionID, WorkspaceID: run.WorkspaceID, BackendID: "local", Status: "completed", CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}, nil, "", nil, "agent_session", nil); err != nil {
			t.Fatal(err)
		}
		if err := store.LinkPipelineSession(t.Context(), catalog.PipelineSession{ID: linkID, PipelineID: run.ID, SessionID: sessionID, Role: "evaluator", CreatedAt: run.CreatedAt}); err != nil {
			t.Fatal(err)
		}
	}
	createEvaluator("evaluator-v1", "eval-link-v1")
	const first = `{"passed":false,"findings":["versão antiga"]}`
	if err := store.SavePipelineExecutionArtifact(t.Context(), run.ID, sdd.Eval, first, "evaluator-v1", run.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET revision=revision+1,current_stage='eval',stage_status='{"discovery":"completed","spec":"completed","plan":"completed","code":"completed","eval":"active"}' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	createEvaluator("evaluator-v2", "eval-link-v2")
	if err := store.SavePipelineExecutionArtifact(t.Context(), run.ID, sdd.Eval, `{"passed":true,"findings":[]}`, "evaluator-v2", run.Revision+2); err != nil {
		t.Fatal(err)
	}
	var archivedContent, digest, reason string
	var version int64
	if err := store.DB().QueryRowContext(t.Context(), `SELECT version,content,content_digest,reason FROM pipeline_archived_artifacts WHERE pipeline_id=? AND stage='eval'`, run.ID).Scan(&version, &archivedContent, &digest, &reason); err != nil {
		t.Fatalf("superseded Eval was not archived: %v", err)
	}
	wantDigest := sha256.Sum256([]byte(first))
	if version != 1 || archivedContent != first || digest != hex.EncodeToString(wantDigest[:]) || reason != "superseded" {
		t.Fatalf("archived Eval lost provenance: version=%d content=%q digest=%s reason=%q", version, archivedContent, digest, reason)
	}
	loaded, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil || len(loaded.ArchivedArtifacts) != 1 || loaded.ArchivedArtifacts[0].Content != first || loaded.Artifacts[sdd.Eval].Content == first {
		t.Fatalf("current and archived Eval versions were not returned separately: %+v %v", loaded, err)
	}
}
