package sqlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func TestPipelineDesignMigrationCreatesDurableWorkspaceTables(t *testing.T) {
	store, _, _ := authoringFixture(t)
	for _, table := range []string{"pipeline_design_workspaces", "pipeline_design_documents", "pipeline_design_versions", "pipeline_design_messages", "pipeline_design_attempts", "pipeline_design_commands"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("durable design table %s is missing: %v", table, err)
		}
	}
}

func designFixture(t *testing.T) (*Store, string, catalog.PipelineDesignWorkspace) {
	t.Helper()
	store, path, run := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), run, "Criar um TODO com localStorage"); err != nil {
		t.Fatal(err)
	}
	workspace, err := store.OpenPipelineDesign(t.Context(), run.ID, run.Revision, nil)
	if err != nil {
		t.Fatal(err)
	}
	return store, path, workspace
}

func TestPipelineDesignFencesLegacyAuthoringPipelineWriters(t *testing.T) {
	for _, action := range []string{"revise", "freeze", "brainstorm", "authoring_stage"} {
		t.Run(action, func(t *testing.T) {
			store, _, workspace := designFixture(t)
			before, err := store.GetPipeline(t.Context(), workspace.PipelineID)
			if err != nil {
				t.Fatal(err)
			}
			var writeErr error
			switch action {
			case "revise":
				writeErr = store.ReviseAuthoringDiscovery(t.Context(), before.ID, before.Revision, 1, "External edit", "New title", "New objective")
			case "freeze":
				writeErr = store.FreezeDiscovery(t.Context(), before.ID, before.Revision, 1)
			case "brainstorm":
				tx, err := store.DB().BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				writeErr = brainstormPipelineCAS(t.Context(), tx, before.ID, before.Revision, 1, time.Now().UTC())
			case "authoring_stage":
				if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='spec',discovery_frozen_version=1 WHERE id=?`, before.ID); err != nil {
					t.Fatal(err)
				}
				before, err = store.GetPipeline(t.Context(), before.ID)
				if err != nil {
					t.Fatal(err)
				}
				tx, err := store.DB().BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				writeErr = authoringPipelineCAS(t.Context(), tx, before, catalog.AuthoringStageRequest{PipelineID: before.ID, Stage: sdd.Spec, PipelineRevision: before.Revision, DiscoveryVersion: 1}, sdd.Completed, sdd.Plan, time.Now().UTC())
			}
			if !errors.Is(writeErr, ErrPipelineConflict) {
				t.Fatalf("legacy %s bypassed document workspace: %v", action, writeErr)
			}
			if action == "revise" || action == "freeze" {
				after, err := store.GetPipeline(t.Context(), before.ID)
				if err != nil || after.Revision != before.Revision || after.Artifacts[sdd.Discovery].Content != before.Artifacts[sdd.Discovery].Content {
					t.Fatalf("blocked writer changed pipeline: %+v %v", after, err)
				}
			}
		})
	}
}

func designRef(workspace catalog.PipelineDesignWorkspace, requestID string) catalog.PipelineDesignRef {
	return catalog.PipelineDesignRef{PipelineID: workspace.PipelineID, RequestID: requestID, PipelineRevision: workspace.PipelineRevision, DesignRevision: workspace.Revision}
}

func editDesign(t *testing.T, store *Store, workspace catalog.PipelineDesignWorkspace, stage sdd.Stage, content string) catalog.PipelineDesignWorkspace {
	t.Helper()
	updated, err := store.EditPipelineDesignDocument(t.Context(), catalog.PipelineDesignEditRequest{Ref: designRef(workspace, fmt.Sprintf("edit_%s_%d", stage, workspace.Revision)), Stage: stage, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestPipelineDesignOpeningImportsWithoutChangingPipelineOrReplaying(t *testing.T) {
	store, path, workspace := designFixture(t)
	if workspace.Revision != 1 || workspace.PipelineRevision != 1 || workspace.State != "ready" || workspace.ActiveAttemptID != "" || len(workspace.Attempts) != 0 {
		t.Fatalf("opening changed execution state: %+v", workspace)
	}
	if workspace.Documents[sdd.Discovery].Content != "Criar um TODO com localStorage" || len(workspace.Messages) != 1 || workspace.Messages[0].Role != "user" {
		t.Fatalf("Discovery and its original message were not imported: %+v", workspace)
	}
	reopened, err := store.OpenPipelineDesign(t.Context(), workspace.PipelineID, workspace.PipelineRevision, map[sdd.Stage]catalog.PipelineDesignDocumentInput{sdd.Discovery: {Content: "Do not overwrite"}})
	if err != nil || reopened.Documents[sdd.Discovery].Content != workspace.Documents[sdd.Discovery].Content || len(reopened.Messages) != 1 || reopened.Revision != workspace.Revision {
		t.Fatalf("reopening mutated the design: %+v %v", reopened, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reloaded, err := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || reloaded.Revision != 1 || len(reloaded.Messages) != 1 || len(reloaded.Attempts) != 0 || len(reloaded.Versions[sdd.Discovery]) != 1 {
		t.Fatalf("durable opening did not survive restart: %+v %v", reloaded, err)
	}
}

func TestPipelineDesignManualEditsPreserveHistoryAndMarkDependenciesStale(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC v1")
	workspace = editDesign(t, store, workspace, sdd.Plan, "Plan v1")
	request := catalog.PipelineDesignEditRequest{Ref: designRef(workspace, "manual_discovery_01"), Stage: sdd.Discovery, Content: "Discovery revisado"}
	updated, err := store.EditPipelineDesignDocument(t.Context(), request)
	if err != nil || !updated.Documents[sdd.Spec].Stale || !updated.Documents[sdd.Plan].Stale || updated.Documents[sdd.Discovery].Stale || updated.Documents[sdd.Discovery].Author != "user" {
		t.Fatalf("manual Discovery did not invalidate dependencies: %+v %v", updated, err)
	}
	replayed, err := store.EditPipelineDesignDocument(t.Context(), request)
	if err != nil || replayed.Revision != updated.Revision || len(replayed.Versions[sdd.Discovery]) != 2 {
		t.Fatalf("manual retry duplicated a version: %+v %v", replayed, err)
	}
	changed := request
	changed.Content = "Reused request with different text"
	if _, err := store.EditPipelineDesignDocument(t.Context(), changed); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("changed command was not rejected: %v", err)
	}
	updated = editDesign(t, store, updated, sdd.Spec, updated.Documents[sdd.Spec].Content)
	if updated.Documents[sdd.Spec].Stale || !updated.Documents[sdd.Plan].Stale {
		t.Fatal("explicit manual review did not acknowledge current Discovery")
	}
	restored, err := store.RestorePipelineDesignDocument(t.Context(), catalog.PipelineDesignRestoreRequest{Ref: designRef(updated, "restore_discovery_01"), Stage: sdd.Discovery, Version: 1})
	if err != nil || restored.Documents[sdd.Discovery].Version != 3 || restored.Documents[sdd.Discovery].Content != workspace.Documents[sdd.Discovery].Content || len(restored.Versions[sdd.Discovery]) != 3 {
		t.Fatalf("restore lost history or overwrote current version: %+v %v", restored, err)
	}
	if restored.Versions[sdd.Discovery][2].Reason != "restored" || restored.Versions[sdd.Discovery][2].RestoredFromVersion != 1 {
		t.Fatal("restore provenance was not recorded")
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_design_versions SET content='tampered' WHERE pipeline_id=?`, workspace.PipelineID); err == nil {
		t.Fatal("immutable version history accepted a mutation")
	}
}

func beginDesign(t *testing.T, store *Store, workspace catalog.PipelineDesignWorkspace, requestID, target string) (catalog.PipelineDesignWorkspace, catalog.PipelineDesignAttempt, catalog.PipelineDesignAttemptRequest) {
	t.Helper()
	in := catalog.PipelineDesignAttemptRequest{Ref: designRef(workspace, requestID), Message: "Preparar os documentos", Target: target, Selections: map[sdd.Stage]catalog.ModelSelection{}}
	for _, stage := range []sdd.Stage{sdd.Discovery, sdd.Spec, sdd.Plan} {
		in.Selections[stage] = catalog.ModelSelection{BackendID: "local", ModelID: "test-model", Source: "test", CatalogRevision: "catalog-v1", LocalRevision: "local-v1", WorkspacePath: "/project", CheckedAt: time.Now().UTC(), MaxOutputTokens: 4096}
	}
	in.IntentHash, _ = in.Hash()
	updated, attempt, admitted, err := store.BeginPipelineDesignAttempt(t.Context(), in)
	if err != nil || !admitted {
		t.Fatalf("design attempt not admitted: admitted=%v err=%v", admitted, err)
	}
	return updated, attempt, in
}

func linkDesignSession(t *testing.T, store *Store, workspace catalog.PipelineDesignWorkspace, attempt catalog.PipelineDesignAttempt, stage sdd.Stage, sessionID string) catalog.PipelineDesignWorkspace {
	t.Helper()
	selection := attempt.Selections[stage]
	selection.SessionID = sessionID
	if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: sessionID, WorkspaceID: workspace.WorkspaceID, BackendID: "local", Status: "running", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil, "", nil, "design_documents", &selection); err != nil {
		t.Fatal(err)
	}
	updated, err := store.LinkPipelineDesignSession(t.Context(), workspace.PipelineID, attempt.ID, stage, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestPipelineDesignAttemptsReplayWithoutDuplicateInferenceOrSources(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace, attempt, request := beginDesign(t, store, workspace, "generate_documents_01", "all")
	if attempt.SourceRevision != workspace.Revision || attempt.SourcePipelineRevision != workspace.PipelineRevision || len(workspace.Messages) != 2 {
		t.Fatalf("attempt lacks admitted source snapshot: %+v %+v", workspace, attempt)
	}
	replay, replayAttempt, admitted, err := store.BeginPipelineDesignAttempt(t.Context(), request)
	if err != nil || admitted || replayAttempt.ID != attempt.ID || len(replay.Attempts) != 1 || len(replay.Messages) != 2 {
		t.Fatalf("retry duplicated durable intent: %+v %+v %v %v", replay, replayAttempt, admitted, err)
	}
	changed := request
	changed.Message = "Changed request text"
	if _, _, _, err := store.BeginPipelineDesignAttempt(t.Context(), changed); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("changed attempt retry was accepted: %v", err)
	}
	workspace = linkDesignSession(t, store, workspace, attempt, sdd.Spec, "design_spec_session")
	workspace = linkDesignSession(t, store, workspace, attempt, sdd.Plan, "design_plan_session")
	phase, err := store.SetPipelineDesignPhase(t.Context(), workspace.PipelineID, attempt.ID, sdd.Plan)
	if err != nil || phase.Revision != attempt.SourceRevision || phase.PipelineRevision != attempt.SourcePipelineRevision || phase.Phase != sdd.Plan {
		t.Fatalf("progress metadata invalidated source revision: %+v %v", phase, err)
	}
	result := catalog.PipelineDesignCompleteRequest{PipelineID: workspace.PipelineID, AttemptID: attempt.ID, SourceRevision: attempt.SourceRevision, Summary: "SPEC e Plan preparados", Documents: map[sdd.Stage]catalog.PipelineDesignDocumentInput{
		sdd.Spec: {Content: "SPEC gerada", Author: "ai", SourceSessionID: "design_spec_session"},
		sdd.Plan: {Content: "Plan gerado", Author: "ai", SourceSessionID: "design_plan_session"},
	}}
	completed, err := store.CompletePipelineDesignAttempt(t.Context(), result)
	if err != nil || completed.State != "ready" || completed.ActiveAttemptID != "" || completed.Documents[sdd.Spec].SourceSessionID != "design_spec_session" || completed.Documents[sdd.Spec].Selection.ModelID != "test-model" || len(completed.Messages) != 3 {
		t.Fatalf("atomic generation lacks durable provenance: %+v %v", completed, err)
	}
	again, err := store.CompletePipelineDesignAttempt(t.Context(), result)
	if err != nil || again.Revision != completed.Revision || len(again.Versions[sdd.Spec]) != 1 {
		t.Fatalf("result retry duplicated output: %+v %v", again, err)
	}
	result.Documents[sdd.Spec] = catalog.PipelineDesignDocumentInput{Content: "Conflicting late result", Author: "ai", SourceSessionID: "design_spec_session"}
	if _, err := store.CompletePipelineDesignAttempt(t.Context(), result); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("changed terminal output was accepted: %v", err)
	}
}

func TestPipelineDesignRejectsUnlinkedAndOlderGeneratedOutput(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC atual")
	workspace, attempt, _ := beginDesign(t, store, workspace, "generation_conflict_01", "plan")
	result := catalog.PipelineDesignCompleteRequest{PipelineID: workspace.PipelineID, AttemptID: attempt.ID, SourceRevision: attempt.SourceRevision, Documents: map[sdd.Stage]catalog.PipelineDesignDocumentInput{sdd.Plan: {Content: "Generated result", Author: "ai", SourceSessionID: "invented-session"}}}
	if _, err := store.CompletePipelineDesignAttempt(t.Context(), result); err == nil {
		t.Fatal("output with invented source session was published")
	}
	workspace = linkDesignSession(t, store, workspace, attempt, sdd.Plan, "design_old_plan_session")
	workspace = editDesign(t, store, workspace, sdd.Discovery, "Discovery changed while the model ran")
	result.Documents[sdd.Plan] = catalog.PipelineDesignDocumentInput{Content: "Generated old result", Author: "ai", SourceSessionID: "design_old_plan_session"}
	if _, err := store.CompletePipelineDesignAttempt(t.Context(), result); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("old source result was accepted: %v", err)
	}
	readback, err := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || readback.Documents[sdd.Plan].Version != 0 || readback.Documents[sdd.Discovery].Content != workspace.Documents[sdd.Discovery].Content {
		t.Fatalf("older output changed current docs: %+v %v", readback, err)
	}
}

func TestPipelineDesignCancellationBlocksNewOwnerUntilSettlement(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC atual")
	workspace = editDesign(t, store, workspace, sdd.Plan, "Plan anterior")
	workspace, attempt, _ := beginDesign(t, store, workspace, "generation_cancel_01", "plan")
	pending, err := store.RequestPipelineDesignCancellation(t.Context(), workspace.PipelineID, attempt.ID)
	if err != nil || pending.State != "cancellation_pending" || pending.ActiveAttemptID != attempt.ID {
		t.Fatalf("cancellation released its owner too early: %+v %v", pending, err)
	}
	next := catalog.PipelineDesignAttemptRequest{Ref: designRef(pending, "new_owner_too_soon"), Message: "Run again", Target: "plan"}
	next.IntentHash, _ = next.Hash()
	if _, _, admitted, err := store.BeginPipelineDesignAttempt(t.Context(), next); err == nil || admitted {
		t.Fatal("new owner admitted before executor join")
	}
	settled, err := store.FailPipelineDesignAttempt(t.Context(), catalog.PipelineDesignFailRequest{PipelineID: workspace.PipelineID, AttemptID: attempt.ID, Status: "cancelled", ErrorCode: "cancelled"})
	if err != nil || settled.State != "paused" || settled.ActiveAttemptID != "" || settled.Documents[sdd.Plan].Content != "Plan anterior" {
		t.Fatalf("settled cancellation lost documents: %+v %v", settled, err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_design_attempts SET status='completed' WHERE id=?`, attempt.ID); err == nil {
		t.Fatal("terminal cancellation accepted a late status change")
	}
}

func TestPipelineDesignFencesLegacyArtifactStatusAndTransition(t *testing.T) {
	for _, action := range []string{"artifact", "status", "transition"} {
		t.Run(action, func(t *testing.T) {
			store, _, workspace := designFixture(t)
			before, _ := store.GetPipeline(t.Context(), workspace.PipelineID)
			var err error
			switch action {
			case "artifact":
				err = store.SavePipelineArtifact(t.Context(), before.ID, sdd.Discovery, "legacy overwrite", before.Revision)
			case "status":
				tx, beginErr := store.DB().BeginTx(t.Context(), nil)
				if beginErr != nil {
					t.Fatal(beginErr)
				}
				err = setDiscoveryStatus(t.Context(), tx, before.ID, sdd.Completed)
				_ = tx.Rollback()
			case "transition":
				err = store.TransitionPipeline(t.Context(), before.ID, sdd.Discovery, sdd.Flow{Current: sdd.Spec, Status: before.Status}, "complete", "", before.Revision)
			}
			if !errors.Is(err, ErrPipelineConflict) {
				t.Fatalf("legacy %s escaped design ownership: %v", action, err)
			}
			after, _ := store.GetPipeline(t.Context(), before.ID)
			if after.Revision != before.Revision || after.Current != before.Current || !reflect.DeepEqual(after.Status, before.Status) || !reflect.DeepEqual(after.Artifacts, before.Artifacts) {
				t.Fatal("blocked legacy writer changed the aggregate")
			}
		})
	}
}

func TestPipelineDesignOpenBlocksRunningLegacyBrainstorm(t *testing.T) {
	store, _, run, _ := brainstormFixture(t)
	brainstormAdmit(t, store, run.ID, "question", "legacy_active_question")
	pipeline, _ := store.GetPipeline(t.Context(), run.PipelineID)
	if _, err := store.OpenPipelineDesign(t.Context(), pipeline.ID, pipeline.Revision, nil); !errors.Is(err, sdd.ErrAuthoringCancellationPending) {
		t.Fatalf("designer took an active brainstorm owner: %v", err)
	}
	var count int
	_ = store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pipeline_design_workspaces WHERE pipeline_id=?`, pipeline.ID).Scan(&count)
	if count != 0 {
		t.Fatal("rejected OpenDesign created a workspace")
	}
}

func legacyDesignAuthoringAttempt(t *testing.T, linked bool) (*Store, catalog.PipelineRun, catalog.AuthoringStageAttempt, catalog.AuthoringStageRequest) {
	t.Helper()
	store, _, brain := authoringStageFixture(t)
	ref := stageRef(t, store, brain.PipelineID, sdd.Spec, "legacy_active_spec")
	attempt, _, err := store.BeginAuthoringStage(t.Context(), catalog.BeginAuthoringStageRequest{Ref: ref, Selection: brainstormChoice(), ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", EstimatedInputTokens: 2000, ClientIntentHash: strings.Repeat("e", 64)})
	if err != nil {
		t.Fatal(err)
	}
	ref = stageRef(t, store, brain.PipelineID, sdd.Spec, "legacy_settle_spec")
	pipeline, _ := store.GetPipeline(t.Context(), brain.PipelineID)
	if linked {
		session := catalog.SessionRecord{ID: "legacy_design_session", WorkspaceID: pipeline.WorkspaceID, BackendID: "profile", BackendRevision: "revision", Mode: "sdd_readonly", Status: "ready", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := store.CreateAuthoringStageSession(t.Context(), ref, attempt, session); err != nil {
			t.Fatal(err)
		}
		attempt.SessionID = session.ID
	}
	return store, pipeline, attempt, ref
}

func TestPipelineDesignOpenBlocksLegacyAuthoringAndPendingStop(t *testing.T) {
	store, pipeline, attempt, ref := legacyDesignAuthoringAttempt(t, true)
	if _, err := store.OpenPipelineDesign(t.Context(), pipeline.ID, pipeline.Revision, nil); !errors.Is(err, sdd.ErrAuthoringCancellationPending) {
		t.Fatalf("designer took running SPEC: %v", err)
	}
	paused, err := store.CancelAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: ref, AttemptID: attempt.ID})
	if err != nil || !paused.CancellationPending {
		t.Fatalf("legacy cancellation did not reserve join: %+v %v", paused, err)
	}
	pipeline, _ = store.GetPipeline(t.Context(), pipeline.ID)
	if _, err := store.OpenPipelineDesign(t.Context(), pipeline.ID, pipeline.Revision, nil); !errors.Is(err, sdd.ErrAuthoringCancellationPending) {
		t.Fatalf("designer took an unconfirmed stop: %v", err)
	}
	if _, err := store.ConfirmAuthoringStageStop(t.Context(), pipeline.ID, sdd.Spec, attempt.ID, "joined"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenPipelineDesign(t.Context(), pipeline.ID, pipeline.Revision, nil); err != nil {
		t.Fatalf("confirmed legacy stop blocked import: %v", err)
	}
}

func TestPipelineDesignOpenImportsReadyAndWaitingLegacyDraft(t *testing.T) {
	for _, state := range []string{"ready", "waiting_user"} {
		t.Run(state, func(t *testing.T) {
			var store *Store
			var pipeline catalog.PipelineRun
			if state == "ready" {
				var brain catalog.BrainstormRun
				store, _, brain = authoringStageFixture(t)
				pipeline, _ = store.GetPipeline(t.Context(), brain.PipelineID)
			} else {
				var attempt catalog.AuthoringStageAttempt
				var ref catalog.AuthoringStageRequest
				store, pipeline, attempt, ref = legacyDesignAuthoringAttempt(t, true)
				if _, err := store.CompleteAuthoringStage(t.Context(), catalog.CompleteAuthoringStageRequest{Ref: ref, AttemptID: attempt.ID, SessionID: attempt.SessionID, Content: []byte(validSpecDocument)}); err != nil {
					t.Fatal(err)
				}
				pipeline, _ = store.GetPipeline(t.Context(), pipeline.ID)
			}
			workspace, err := store.OpenPipelineDesign(t.Context(), pipeline.ID, pipeline.Revision, nil)
			if err != nil || workspace.PipelineRevision != pipeline.Revision || workspace.Documents[sdd.Discovery].Version == 0 {
				t.Fatalf("settled legacy draft could not import: %+v %v", workspace, err)
			}
			if state == "waiting_user" && workspace.Documents[sdd.Spec].Content != validSpecDocument {
				t.Fatalf("waiting SPEC was lost: %q", workspace.Documents[sdd.Spec].Content)
			}
		})
	}
}

// Simulate a workspace created by the previous version while a legacy worker
// still owned its attempt. New OpenDesign admission must never create this case.
func forceLegacyDesignOwnership(t *testing.T, store *Store, pipeline catalog.PipelineRun) catalog.PipelineDesignWorkspace {
	t.Helper()
	tx, err := store.beginPipelineDesignTx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO pipeline_design_workspaces(pipeline_id,pipeline_revision,revision,state,created_at,updated_at) VALUES(?,?,1,'ready',?,?)`, pipeline.ID, pipeline.Revision, formatCatalogTime(now), formatCatalogTime(now)); err != nil {
		t.Fatal(err)
	}
	artifact := pipeline.Artifacts[sdd.Discovery]
	document := catalog.PipelineDesignDocument{Stage: sdd.Discovery, Version: 1, Content: artifact.Content, ContentDigest: catalog.PipelineDesignContentDigest(artifact.Content), Author: "user", UpdatedAt: now}
	if err := writePipelineDesignVersion(t.Context(), tx, pipeline.ID, document, "import", 0, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	workspace, err := store.GetPipelineDesign(t.Context(), pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}

func TestPipelineDesignLegacyBrainstormSettlementPreservesNewOwner(t *testing.T) {
	for _, action := range []string{"cancel", "fail", "recover"} {
		t.Run(action, func(t *testing.T) {
			store, _, run, _ := brainstormFixture(t)
			attempt := brainstormAdmit(t, store, run.ID, "question", "legacy_settlement_question")
			ref := brainstormRef(t, store, run.ID, "legacy_settlement_action")
			pipeline, _ := store.GetPipeline(t.Context(), run.PipelineID)
			workspace := forceLegacyDesignOwnership(t, store, pipeline)
			workspace = editDesign(t, store, workspace, sdd.Discovery, "Designer owns the revised Discovery")
			pipeline, _ = store.GetPipeline(t.Context(), run.PipelineID)
			var err error
			switch action {
			case "cancel":
				_, err = store.CancelBrainstormAttempt(t.Context(), catalog.CancelBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: attempt.ID})
			case "fail":
				_, err = store.FailBrainstormAttempt(t.Context(), catalog.FailBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: attempt.ID, ErrorCode: "provider_failed"})
			case "recover":
				err = store.InterruptRunningBrainstormAttempts(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			after, _ := store.GetPipeline(t.Context(), pipeline.ID)
			design, _ := store.GetPipelineDesign(t.Context(), pipeline.ID)
			settled, _ := store.GetBrainstorming(t.Context(), run.ID)
			if after.Revision != pipeline.Revision || !reflect.DeepEqual(after.Status, pipeline.Status) || design.Revision != workspace.Revision || design.PipelineRevision != workspace.PipelineRevision || settled.State != "paused" || settled.Attempts[0].Status == "running" {
				t.Fatalf("legacy settlement changed design or left owner active: pipeline=%d design=%d state=%s", after.Revision, design.Revision, settled.State)
			}
		})
	}
}

func TestPipelineDesignLegacyAuthoringSettlementPreservesNewOwner(t *testing.T) {
	for _, action := range []string{"cancel", "fail", "recover"} {
		t.Run(action, func(t *testing.T) {
			store, pipeline, attempt, ref := legacyDesignAuthoringAttempt(t, true)
			workspace := forceLegacyDesignOwnership(t, store, pipeline)
			workspace = editDesign(t, store, workspace, sdd.Discovery, "Designer owns the revised Discovery")
			pipeline, _ = store.GetPipeline(t.Context(), pipeline.ID)
			var err error
			switch action {
			case "cancel":
				_, err = store.CancelAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: ref, AttemptID: attempt.ID})
			case "fail":
				_, err = store.FailAuthoringStage(t.Context(), catalog.FailAuthoringStageRequest{Ref: ref, AttemptID: attempt.ID, ErrorCode: "provider_failed"})
			case "recover":
				err = store.InterruptRunningAuthoringStages(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			after, _ := store.GetPipeline(t.Context(), pipeline.ID)
			design, _ := store.GetPipelineDesign(t.Context(), pipeline.ID)
			settled, _ := store.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
			if after.Revision != pipeline.Revision || !reflect.DeepEqual(after.Status, pipeline.Status) || design.Revision != workspace.Revision || design.PipelineRevision != workspace.PipelineRevision || !settled.CancellationPending || settled.Attempts[0].Status == "running" {
				t.Fatal("legacy authoring settlement changed design or lost its pending stop")
			}
			if _, err := store.ConfirmAuthoringStageStop(t.Context(), pipeline.ID, sdd.Spec, attempt.ID, "joined"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPipelineDesignPlanRequiresCurrentSpecInStore(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC original")
	workspace = editDesign(t, store, workspace, sdd.Plan, "Plan original")
	workspace = editDesign(t, store, workspace, sdd.Discovery, "Discovery changed")
	in := catalog.PipelineDesignAttemptRequest{Ref: designRef(workspace, "plan_from_stale_spec"), Message: "Update only Plan", Target: "plan", Selections: map[sdd.Stage]catalog.ModelSelection{sdd.Plan: {BackendID: "local", ModelID: "test-model"}}}
	in.IntentHash, _ = in.Hash()
	if _, _, admitted, err := store.BeginPipelineDesignAttempt(t.Context(), in); !errors.Is(err, ErrPipelineDesignSpecStale) || admitted {
		t.Fatalf("stale SPEC admitted Plan: %v %v", admitted, err)
	}
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC current")
	workspace, attempt, _ := beginDesign(t, store, workspace, "plan_admitted_current_spec", "plan")
	workspace = linkDesignSession(t, store, workspace, attempt, sdd.Plan, "plan_stale_result_session")
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_design_documents SET stale=1 WHERE pipeline_id=? AND stage='spec'`, workspace.PipelineID); err != nil {
		t.Fatal(err)
	}
	result := catalog.PipelineDesignCompleteRequest{PipelineID: workspace.PipelineID, AttemptID: attempt.ID, SourceRevision: attempt.SourceRevision, Documents: map[sdd.Stage]catalog.PipelineDesignDocumentInput{sdd.Plan: {Content: "Must not publish", Author: "ai", SourceSessionID: "plan_stale_result_session"}}}
	if _, err := store.CompletePipelineDesignAttempt(t.Context(), result); !errors.Is(err, ErrPipelineDesignSpecStale) {
		t.Fatalf("stale SPEC was laundered by Plan result: %v", err)
	}
	readback, _ := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if !readback.Documents[sdd.Plan].Stale || readback.Documents[sdd.Plan].Content != "Plan original" {
		t.Fatal("rejected Plan changed the current document")
	}
}

func TestPipelineDesignOpenSerializesWithLegacyRevision(t *testing.T) {
	store, path, pipeline := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), pipeline, "Initial Discovery"); err != nil {
		t.Fatal(err)
	}
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := store.OpenPipelineDesign(t.Context(), pipeline.ID, pipeline.Revision, nil)
		results <- err
	}()
	go func() {
		<-start
		results <- other.ReviseAuthoringDiscovery(t.Context(), pipeline.ID, pipeline.Revision, 1, "Legacy revision", "Title", "Objective")
	}()
	close(start)
	first, second := <-results, <-results
	if (first == nil) == (second == nil) || first != nil && !errors.Is(first, ErrPipelineConflict) || second != nil && !errors.Is(second, ErrPipelineConflict) {
		t.Fatalf("writers were not serialized: first=%v second=%v", first, second)
	}
	run, _ := store.GetPipeline(t.Context(), pipeline.ID)
	workspace, designErr := store.GetPipelineDesign(t.Context(), pipeline.ID)
	if designErr == nil && (workspace.PipelineRevision != run.Revision || workspace.Documents[sdd.Discovery].Content != "Initial Discovery") {
		t.Fatal("legacy writer drifted the newly opened workspace")
	}
	if designErr != nil && (run.Revision != 2 || run.Artifacts[sdd.Discovery].Content != "Legacy revision") {
		t.Fatal("legacy winner lost its committed revision")
	}
}

func TestPipelineDesignApprovalPublishesExactCurrentHashesWithoutExecutingCode(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC manual")
	workspace = editDesign(t, store, workspace, sdd.Plan, "Plan manual")
	request := catalog.PipelineDesignApproveRequest{Ref: designRef(workspace, "approve_design_01"), Digests: map[sdd.Stage]string{}}
	for _, stage := range []sdd.Stage{sdd.Discovery, sdd.Spec, sdd.Plan} {
		request.Digests[stage] = workspace.Documents[stage].ContentDigest
	}
	wrong := request
	wrong.Digests = map[sdd.Stage]string{sdd.Discovery: request.Digests[sdd.Discovery], sdd.Spec: strings.Repeat("a", 64), sdd.Plan: request.Digests[sdd.Plan]}
	if _, err := store.ApprovePipelineDesign(t.Context(), wrong); err == nil {
		t.Fatal("approval accepted mismatched SPEC hash")
	}
	run, err := store.ApprovePipelineDesign(t.Context(), request)
	if err != nil || run.Current != sdd.Code || run.Status[sdd.Code] != sdd.Active || run.Status[sdd.Eval] != sdd.Pending || len(run.Artifacts) != 3 || run.DiscoveryFrozenVersion != run.Artifacts[sdd.Discovery].Version {
		t.Fatalf("approval did not advance only to Code: %+v %v", run, err)
	}
	for _, stage := range []sdd.Stage{sdd.Discovery, sdd.Spec, sdd.Plan} {
		artifact := run.Artifacts[stage]
		if artifact.Content != workspace.Documents[stage].Content || artifact.Author != "user" || artifact.SourceSessionID != "" || run.Status[stage] != sdd.Completed {
			t.Fatalf("manual approval provenance lost for %s: %+v", stage, artifact)
		}
	}
	again, err := store.ApprovePipelineDesign(t.Context(), request)
	if err != nil || again.Revision != run.Revision {
		t.Fatalf("approval retry changed pipeline again: %+v %v", again, err)
	}
	readback, err := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || readback.State != "approved" || !readback.NeedsDerivation {
		t.Fatalf("approved documents still editable in original pipeline: %+v %v", readback, err)
	}
	if _, err := store.EditPipelineDesignDocument(t.Context(), catalog.PipelineDesignEditRequest{Ref: designRef(readback, "edit_after_code"), Stage: sdd.Plan, Content: "Mutation after Code"}); err == nil {
		t.Fatal("documents in Code accepted an in-place mutation")
	}
}

func TestPipelineDesignRejectsPipelineChangesOutsideWorkspace(t *testing.T) {
	store, _, workspace := designFixture(t)
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET revision=revision+1 WHERE id=?`, workspace.PipelineID); err != nil {
		t.Fatal(err)
	}
	request := catalog.PipelineDesignEditRequest{Ref: designRef(workspace, "stale_pipeline_edit"), Stage: sdd.Discovery, Content: "Outdated command"}
	if _, err := store.EditPipelineDesignDocument(t.Context(), request); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("outside pipeline mutation was not rejected: %v", err)
	}
}

func TestPipelineDesignApprovalPreservesGeneratedSessionsAndHumanDiscovery(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace, attempt, _ := beginDesign(t, store, workspace, "generate_all_for_approval", "discovery")
	for _, stage := range pipelineDesignStages {
		workspace = linkDesignSession(t, store, workspace, attempt, stage, "session_"+string(stage))
	}
	documents := map[sdd.Stage]catalog.PipelineDesignDocumentInput{}
	for _, stage := range pipelineDesignStages {
		documents[stage] = catalog.PipelineDesignDocumentInput{Content: "Updated " + string(stage), Author: "ai", SourceSessionID: "session_" + string(stage)}
	}
	workspace, err := store.CompletePipelineDesignAttempt(t.Context(), catalog.PipelineDesignCompleteRequest{PipelineID: workspace.PipelineID, AttemptID: attempt.ID, SourceRevision: attempt.SourceRevision, Documents: documents})
	if err != nil {
		t.Fatal(err)
	}
	request := catalog.PipelineDesignApproveRequest{Ref: designRef(workspace, "approve_generated_documents"), Digests: map[sdd.Stage]string{}}
	for _, stage := range pipelineDesignStages {
		request.Digests[stage] = workspace.Documents[stage].ContentDigest
	}
	run, err := store.ApprovePipelineDesign(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if run.DiscoveryFrozenVersion != run.Artifacts[sdd.Discovery].Version || run.Artifacts[sdd.Discovery].Author != "user" || run.Artifacts[sdd.Discovery].SourceSessionID != "" {
		t.Fatalf("Discovery does not record the actual human-approved artifact: %+v", run)
	}
	for _, stage := range []sdd.Stage{sdd.Spec, sdd.Plan} {
		artifact := run.Artifacts[stage]
		if artifact.Author != "ai" || artifact.SourceSessionID != "session_"+string(stage) || catalog.PipelineDesignContentDigest(artifact.Content) != request.Digests[stage] {
			t.Fatalf("approved generated %s lost source evidence: %+v", stage, artifact)
		}
	}
}

func TestPipelineDesignApprovalRejectsEmptyStaleOrRunningDocuments(t *testing.T) {
	for _, invalidState := range []string{"empty", "stale", "running"} {
		t.Run(invalidState, func(t *testing.T) {
			store, _, workspace := designFixture(t)
			if invalidState != "empty" {
				workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC")
				workspace = editDesign(t, store, workspace, sdd.Plan, "Plan")
			}
			if invalidState == "stale" {
				workspace = editDesign(t, store, workspace, sdd.Discovery, "New Discovery")
			}
			if invalidState == "running" {
				workspace, _, _ = beginDesign(t, store, workspace, "prepare_during_approval", "all")
			}
			request := catalog.PipelineDesignApproveRequest{Ref: designRef(workspace, "invalid_approval"), Digests: map[sdd.Stage]string{}}
			for _, stage := range pipelineDesignStages {
				request.Digests[stage] = workspace.Documents[stage].ContentDigest
			}
			if _, err := store.ApprovePipelineDesign(t.Context(), request); err == nil {
				t.Fatalf("approval accepted %s documents", invalidState)
			}
			run, err := store.GetPipeline(t.Context(), workspace.PipelineID)
			if err != nil || run.Revision != workspace.PipelineRevision || run.Current == sdd.Code || len(run.Artifacts) != 1 {
				t.Fatalf("invalid approval partially published artifacts: %+v %v", run, err)
			}
		})
	}
}

func TestPipelineDesignRecoveryIsPassiveAndProtectsKnownLiveOwners(t *testing.T) {
	store, path, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "Previous SPEC")
	workspace, attempt, request := beginDesign(t, store, workspace, "recover_design_01", "all")
	cutoff := time.Now().UTC().Add(time.Second)
	if recovered, err := store.RecoverPipelineDesignAttempts(t.Context(), cutoff, []string{attempt.ID}); err != nil || recovered != 0 {
		t.Fatalf("live attempt was recovered: count=%d err=%v", recovered, err)
	}
	if recovered, err := store.RecoverPipelineDesignAttempts(t.Context(), attempt.CreatedAt, nil); err != nil || recovered != 0 {
		t.Fatalf("cutoff was ignored: count=%d err=%v", recovered, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	readback, err := reopened.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || readback.ActiveAttemptID != attempt.ID || readback.Attempts[0].Status != "running" {
		t.Fatalf("read/reopen replayed or recovered execution implicitly: %+v %v", readback, err)
	}
	if recovered, err := reopened.RecoverPipelineDesignAttempts(t.Context(), cutoff, nil); err != nil || recovered != 1 {
		t.Fatalf("abandoned attempt not checkpointed: count=%d err=%v", recovered, err)
	}
	readback, err = reopened.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || readback.State != "paused" || readback.ActiveAttemptID != "" || readback.Attempts[0].Status != "interrupted" || readback.Documents[sdd.Spec].Content != "Previous SPEC" || len(readback.Messages) != 2 {
		t.Fatalf("passive recovery lost durable work: %+v %v", readback, err)
	}
	_, oldAttempt, admitted, err := reopened.BeginPipelineDesignAttempt(t.Context(), request)
	if err != nil || admitted || oldAttempt.Status != "interrupted" || oldAttempt.ID != attempt.ID {
		t.Fatalf("retry of interrupted intent reexecuted the model: %+v %v %v", oldAttempt, admitted, err)
	}
	if recovered, err := reopened.RecoverPipelineDesignAttempts(t.Context(), cutoff, nil); err != nil || recovered != 0 {
		t.Fatalf("recovery was not idempotent: count=%d err=%v", recovered, err)
	}
	beginDesign(t, reopened, readback, "explicit_resume_new_intent", "all")
}

func TestPipelineDesignRejectsWrongSessionSelectionAndAtomicOutput(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace, attempt, _ := beginDesign(t, store, workspace, "bad_model_source", "all")
	selection := attempt.Selections[sdd.Spec]
	selection.SessionID, selection.ModelID = "wrong_model_session", "different-model"
	if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: selection.SessionID, WorkspaceID: workspace.WorkspaceID, BackendID: selection.BackendID, Status: "running", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil, "", nil, "design_documents", &selection); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPipelineDesignSession(t.Context(), workspace.PipelineID, attempt.ID, sdd.Spec, selection.SessionID); err == nil {
		t.Fatal("session with a different model was accepted as source")
	}
	workspace = linkDesignSession(t, store, workspace, attempt, sdd.Spec, "correct_spec_session")
	workspace = linkDesignSession(t, store, workspace, attempt, sdd.Plan, "correct_plan_session")
	result := catalog.PipelineDesignCompleteRequest{PipelineID: workspace.PipelineID, AttemptID: attempt.ID, SourceRevision: attempt.SourceRevision, Documents: map[sdd.Stage]catalog.PipelineDesignDocumentInput{
		sdd.Spec: {Content: "Valid SPEC", Author: "ai", SourceSessionID: "correct_spec_session"},
		sdd.Plan: {Content: "Invalid Plan source", Author: "ai", SourceSessionID: "correct_spec_session"},
	}}
	if _, err := store.CompletePipelineDesignAttempt(t.Context(), result); err == nil {
		t.Fatal("mismatched Plan session published an atomic result")
	}
	readback, err := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || readback.Documents[sdd.Spec].Version != 0 || readback.Documents[sdd.Plan].Version != 0 || readback.Revision != workspace.Revision {
		t.Fatalf("invalid output partially wrote the valid SPEC: %+v %v", readback, err)
	}
}

func TestPipelineDesignConcurrentManualCommandsHaveOneWinner(t *testing.T) {
	store, _, workspace := designFixture(t)
	var group sync.WaitGroup
	errorsFound := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, err := store.EditPipelineDesignDocument(t.Context(), catalog.PipelineDesignEditRequest{Ref: designRef(workspace, fmt.Sprintf("concurrent_edit_%d", i)), Stage: sdd.Discovery, Content: fmt.Sprintf("Discovery from command %d", i)})
			errorsFound <- err
		}(i)
	}
	group.Wait()
	close(errorsFound)
	winners, conflicts := 0, 0
	for err := range errorsFound {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrPipelineConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected competing-command error: %v", err)
		}
	}
	readback, err := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || winners != 1 || conflicts != 1 || readback.Documents[sdd.Discovery].Version != 2 || readback.Revision != 2 || readback.PipelineRevision != 2 {
		t.Fatalf("CAS admitted multiple winners: winners=%d conflicts=%d readback=%+v err=%v", winners, conflicts, readback, err)
	}
}

func TestPipelineDesignTransactionRollsBackDocumentAndCommandOnWriteFailure(t *testing.T) {
	store, _, workspace := designFixture(t)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER fail_design_version BEFORE INSERT ON pipeline_design_versions WHEN NEW.reason='manual' BEGIN SELECT RAISE(ABORT,'injected write failure'); END`); err != nil {
		t.Fatal(err)
	}
	request := catalog.PipelineDesignEditRequest{Ref: designRef(workspace, "edit_transaction_failure"), Stage: sdd.Discovery, Content: "Do not persist"}
	if _, err := store.EditPipelineDesignDocument(t.Context(), request); err == nil {
		t.Fatal("injected version failure was ignored")
	}
	readback, err := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	var commands int
	if countErr := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pipeline_design_commands WHERE pipeline_id=?`, workspace.PipelineID).Scan(&commands); countErr != nil {
		t.Fatal(countErr)
	}
	if err != nil || readback.Revision != workspace.Revision || readback.PipelineRevision != workspace.PipelineRevision || readback.Documents[sdd.Discovery].Content != workspace.Documents[sdd.Discovery].Content || commands != 0 {
		t.Fatalf("failed transaction left partial changes: %+v %v commands=%d", readback, err, commands)
	}
}

func TestPipelineDesignCodePipelineCanBeOpenedButRequiresDerivation(t *testing.T) {
	store, _, run := authoringFixture(t)
	run.Kind, run.Current = "legacy", sdd.Eval
	run.Status = map[sdd.Stage]sdd.Status{sdd.Discovery: sdd.Completed, sdd.Spec: sdd.Completed, sdd.Plan: sdd.Completed, sdd.Code: sdd.Completed, sdd.Eval: sdd.Active}
	if err := store.CreatePipeline(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	for stage, content := range map[sdd.Stage]string{sdd.Discovery: "Discovery original", sdd.Spec: "SPEC original", sdd.Plan: "Plan original", sdd.Code: "Code evidence", sdd.Eval: "Eval evidence"} {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO pipeline_artifacts(pipeline_id,stage,version,content,author,source_session_id,updated_at) VALUES(?,?,1,?,'legacy/manual','',?)`, run.ID, stage, content, formatCatalogTime(run.CreatedAt)); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := store.OpenPipelineDesign(t.Context(), run.ID, run.Revision, nil)
	if err != nil || !workspace.NeedsDerivation || workspace.Documents[sdd.Spec].Content != "SPEC original" || workspace.Documents[sdd.Plan].Content != "Plan original" {
		t.Fatalf("old Code/Eval pipeline is not accessible: %+v %v", workspace, err)
	}
	if _, err := store.EditPipelineDesignDocument(t.Context(), catalog.PipelineDesignEditRequest{Ref: designRef(workspace, "mutate_execution_history"), Stage: sdd.Discovery, Content: "Mutation"}); !errors.Is(err, ErrPipelineDesignDerivationRequired) {
		t.Fatalf("execution evidence allowed an in-place document mutation: %v", err)
	}
	readback, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil || readback.Revision != run.Revision || readback.Current != sdd.Eval || readback.Artifacts[sdd.Code].Content != "Code evidence" || readback.Artifacts[sdd.Eval].Content != "Eval evidence" {
		t.Fatalf("opening design overwrote execution evidence: %+v %v", readback, err)
	}
}

func TestPipelineDesignAdmissionRequiresAllDependentModelSelections(t *testing.T) {
	for _, target := range []string{"all", "discovery", "spec", "plan"} {
		t.Run(target, func(t *testing.T) {
			store, _, workspace := designFixture(t)
			in := catalog.PipelineDesignAttemptRequest{Ref: designRef(workspace, "missing_model_selection"), Message: "Prepare", Target: target, Selections: map[sdd.Stage]catalog.ModelSelection{
				sdd.Discovery: {BackendID: "local", ModelID: "test-model"},
			}}
			in.IntentHash, _ = in.Hash()
			if _, _, admitted, err := store.BeginPipelineDesignAttempt(t.Context(), in); err == nil || admitted {
				t.Fatalf("target %s admitted without its dependent model snapshots", target)
			}
			readback, err := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
			if err != nil || readback.Revision != workspace.Revision || len(readback.Attempts) != 0 || len(readback.Messages) != 1 {
				t.Fatalf("invalid admission persisted an incomplete owner: %+v %v", readback, err)
			}
		})
	}
}

func TestPipelineDesignSnapshotsPreserveCredentialIdentityWithoutExposingIt(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC atual")
	request := catalog.PipelineDesignAttemptRequest{Ref: designRef(workspace, "credential_snapshot_01"), Message: "Prepare", Target: "plan", Selections: map[sdd.Stage]catalog.ModelSelection{
		sdd.Plan: {BackendID: "local", ModelID: "test-model", Source: "test", CatalogRevision: "catalog-v1", LocalRevision: "local-v1", WorkspacePath: "/project", CheckedAt: time.Now().UTC(), CredentialIdentity: strings.Repeat("a", 64)},
	}}
	request.IntentHash, _ = request.Hash()
	workspace, attempt, admitted, err := store.BeginPipelineDesignAttempt(t.Context(), request)
	if err != nil || !admitted {
		t.Fatal(err)
	}
	workspace, err = store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || workspace.Attempts[0].Selections[sdd.Plan].CredentialIdentity != request.Selections[sdd.Plan].CredentialIdentity {
		t.Fatalf("credential fingerprint was dropped from execution snapshot: %+v %v", workspace.Attempts, err)
	}
	workspace = linkDesignSession(t, store, workspace, attempt, sdd.Plan, "credential_plan_session")
	workspace, err = store.CompletePipelineDesignAttempt(t.Context(), catalog.PipelineDesignCompleteRequest{PipelineID: workspace.PipelineID, AttemptID: attempt.ID, SourceRevision: attempt.SourceRevision, Documents: map[sdd.Stage]catalog.PipelineDesignDocumentInput{sdd.Plan: {Content: "Plan", Author: "ai", SourceSessionID: "credential_plan_session"}}})
	if err != nil || workspace.Documents[sdd.Plan].Selection.CredentialIdentity != request.Selections[sdd.Plan].CredentialIdentity {
		t.Fatalf("credential fingerprint was dropped from provenance: %+v %v", workspace.Documents[sdd.Plan], err)
	}
	encoded, err := json.Marshal(workspace)
	if err != nil || strings.Contains(string(encoded), request.Selections[sdd.Plan].CredentialIdentity) {
		t.Fatalf("internal credential reference leaked through catalog DTO: %s %v", encoded, err)
	}
}

func TestPipelineDesignImportsExistingAIDocumentsWithTheirPersistedModel(t *testing.T) {
	store, _, run := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), run, "Discovery original"); err != nil {
		t.Fatal(err)
	}
	selection := catalog.ModelSelection{SessionID: "imported_model_session", BackendID: "local", ModelID: "historical-model", Source: "test", CatalogRevision: "catalog-v1", LocalRevision: "local-v1", WorkspacePath: "/project", CheckedAt: time.Now().UTC()}
	if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: selection.SessionID, WorkspaceID: run.WorkspaceID, BackendID: selection.BackendID, Status: "completed", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil, "", nil, "design_documents", &selection); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []sdd.Stage{sdd.Spec, sdd.Plan} {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO pipeline_artifacts(pipeline_id,stage,version,content,author,source_session_id,updated_at) VALUES(?,?,2,?,'ai',?,?)`, run.ID, stage, "Imported "+string(stage), selection.SessionID, formatCatalogTime(run.CreatedAt)); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := store.OpenPipelineDesign(t.Context(), run.ID, run.Revision, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []sdd.Stage{sdd.Spec, sdd.Plan} {
		document := workspace.Documents[stage]
		if document.Author != "ai" || document.SourceSessionID != selection.SessionID || document.Selection.ModelID != selection.ModelID || len(workspace.Versions[stage]) != 1 || workspace.Versions[stage][0].Reason != "import" {
			t.Fatalf("imported %s lost persisted model provenance: %+v", stage, document)
		}
	}
	if len(workspace.Attempts) != 0 || len(workspace.Messages) != 1 {
		t.Fatal("opening imported documents inferred or added duplicate messages")
	}
}

func TestPipelineDesignConcurrentStoresHaveOneWinnerAndTypedConflicts(t *testing.T) {
	first, path, workspace := designFixture(t)
	second, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	stores := []*Store{first, second}
	start := make(chan struct{})
	results := make(chan error, 8)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			<-start
			_, err := stores[i%2].EditPipelineDesignDocument(t.Context(), catalog.PipelineDesignEditRequest{Ref: designRef(workspace, fmt.Sprintf("separate_store_edit_%d", i)), Stage: sdd.Discovery, Content: fmt.Sprintf("Discovery from store command %d", i)})
			results <- err
		}(i)
	}
	close(start)
	group.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrPipelineConflict) {
			conflicts++
		} else {
			t.Fatalf("separate Store connections returned an untyped snapshot error: %v", err)
		}
	}
	if winners != 1 || conflicts != 7 {
		t.Fatalf("separate Store connections admitted multiple winners: winners=%d conflicts=%d", winners, conflicts)
	}
}

func TestPipelineDesignOwnerPIDRoundTripsWithoutChangingIntentOrDTO(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC atual")
	request := catalog.PipelineDesignAttemptRequest{Ref: designRef(workspace, "owner_pid_roundtrip"), Message: "Prepare", Target: "plan", OwnerPID: 135791, Selections: map[sdd.Stage]catalog.ModelSelection{sdd.Plan: {BackendID: "local", ModelID: "test-model"}}}
	request.IntentHash, _ = request.Hash()
	changedOwner := request
	changedOwner.OwnerPID = 246802
	changedHash, _ := changedOwner.Hash()
	if changedHash != request.IntentHash {
		t.Fatal("execution owner changed the durable user intent")
	}
	workspace, attempt, admitted, err := store.BeginPipelineDesignAttempt(t.Context(), request)
	if err != nil || !admitted || attempt.OwnerPID != request.OwnerPID {
		t.Fatalf("owner PID was not admitted durably: %+v %v %v", attempt, admitted, err)
	}
	readback, err := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || readback.Attempts[0].OwnerPID != request.OwnerPID {
		t.Fatalf("owner PID lost on readback: %+v %v", readback.Attempts, err)
	}
	_, replay, admitted, err := store.BeginPipelineDesignAttempt(t.Context(), changedOwner)
	if err != nil || admitted || replay.OwnerPID != request.OwnerPID || replay.ID != attempt.ID {
		t.Fatalf("retry replaced the original executor owner: %+v %v %v", replay, admitted, err)
	}
	encoded, err := json.Marshal(readback)
	if err != nil || strings.Contains(string(encoded), "ownerPid") || strings.Contains(string(encoded), "OwnerPID") || strings.Contains(string(encoded), "135791") {
		t.Fatalf("internal owner identity appeared in catalog DTO: %s %v", encoded, err)
	}
}
