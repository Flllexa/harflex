package sqlite

import (
	"errors"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"reflect"
	"strings"
	"testing"
	"time"
)

const validSpecDocument = `{"summary":"Scope","requirements":["Required"],"nonGoals":["Excluded"],"acceptanceCriteria":[{"id":"AC-1","criterion":"Observable acceptance"}]}`
const validPlanDocument = `{"summary":"Implementation","tasks":[{"id":"T-1","title":"Implement","files":["internal/example.go"],"steps":["Add behavior"],"tests":["Test behavior"],"dependsOn":[]}],"risks":["Integration"]}`

func authoringStageFixture(t *testing.T) (*Store, string, catalog.BrainstormRun) {
	t.Helper()
	store, path, brain, _ := brainstormFixture(t)
	if _, err := store.SkipBrainstormQuestions(t.Context(), catalog.SkipBrainstormQuestionsRequest{BrainstormRequest: brainstormRef(t, store, brain.ID, "stage_bypass_0001"), Reason: "Discovery is sufficient"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfirmDiscoveryAfterSkip(t.Context(), brainstormRef(t, store, brain.ID, "stage_confirm_001")); err != nil {
		t.Fatal(err)
	}
	return store, path, brain
}

func stageRef(t *testing.T, store *Store, pipelineID string, stage sdd.Stage, requestID string) catalog.AuthoringStageRequest {
	t.Helper()
	run, err := store.GetAuthoringStage(t.Context(), pipelineID, stage)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.AuthoringStageRequest{PipelineID: pipelineID, Stage: stage, RequestID: requestID, PipelineRevision: run.PipelineRevision, StageRevision: run.Revision, DiscoveryVersion: run.DiscoveryVersion, ArtifactVersion: run.ArtifactVersion}
}

func TestAuthoringStageAdmissionRequiresDiscoveryGate(t *testing.T) {
	store, _, brain, _ := brainstormFixture(t)
	choice := brainstormChoice()
	choice.MaxOutputTokens = 4096
	in := catalog.BeginAuthoringStageRequest{Ref: catalog.AuthoringStageRequest{PipelineID: brain.PipelineID, Stage: sdd.Spec, RequestID: "stage_generate_001", PipelineRevision: 2, DiscoveryVersion: 1}, Selection: choice, ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("a", 64)}
	if _, admitted, err := store.BeginAuthoringStage(t.Context(), in); err == nil || admitted {
		t.Fatal("Discovery gate bypassed")
	}
}

func TestAuthoringStagePreviewAllowsExplicitlySkippedBrainstormWithoutRun(t *testing.T) {
	store, _, pipeline := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), pipeline, "Discovery without brainstorming"); err != nil {
		t.Fatal(err)
	}
	const status = `{"discovery":"completed","spec":"active","plan":"pending","code":"pending","eval":"pending"}`
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='spec',stage_status=?,discovery_frozen_version=1 WHERE id=?`, status, pipeline.ID); err != nil {
		t.Fatal(err)
	}
	ref := stageRef(t, store, pipeline.ID, sdd.Spec, "skip_brain_preview_01")
	input, err := store.PreviewAuthoringStageInput(t.Context(), ref, "")
	if err != nil {
		t.Fatalf("preview after skipped Brainstorm: %v", err)
	}
	if input.DiscoveryVersion != 1 || input.DiscoveryContent != "Discovery without brainstorming" || input.BrainstormRunID != "" || input.Synthesis != nil || input.DiscoveryBypassReason != "Brainstorm skipped" {
		t.Fatalf("unexpected no-Brainstorm input: %+v", input)
	}
}

func TestAuthoringStagePreviewUsesCurrentDiscoveryAfterSkipWithPriorBrainstormHistory(t *testing.T) {
	store, _, brain, _ := brainstormFixture(t)
	pipeline, err := store.GetPipeline(t.Context(), brain.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReviseAuthoringDiscovery(t.Context(), brain.PipelineID, pipeline.Revision, 1, "Current Discovery version two", "Current title", "Current objective"); err != nil {
		t.Fatal(err)
	}
	pipeline, err = store.GetPipeline(t.Context(), brain.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FreezeDiscovery(t.Context(), pipeline.ID, pipeline.Revision, 2); err != nil {
		t.Fatal(err)
	}
	const status = `{"discovery":"completed","spec":"active","plan":"pending","code":"pending","eval":"pending"}`
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='spec',stage_status=? WHERE id=?`, status, pipeline.ID); err != nil {
		t.Fatal(err)
	}
	ref := stageRef(t, store, pipeline.ID, sdd.Spec, "skip_brain_history_01")
	input, err := store.PreviewAuthoringStageInput(t.Context(), ref, "")
	if err != nil {
		t.Fatalf("preview after current Discovery skip: %v", err)
	}
	if input.DiscoveryVersion != 2 || input.DiscoveryContent != "Current Discovery version two" || input.BrainstormRunID != "" || input.Synthesis != nil || input.DiscoveryBypassReason != "Brainstorm skipped" {
		t.Fatalf("stale Brainstorm history contaminated current skipped Discovery: %+v", input)
	}
}

func TestAuthoringStageDraftRequiresLinkedSessionAndHumanApproval(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	choice := brainstormChoice()
	choice.MaxOutputTokens = 4096
	in := catalog.BeginAuthoringStageRequest{Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "stage_generate_001"), Selection: choice, ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("a", 64)}
	attempt, admitted, err := store.BeginAuthoringStage(t.Context(), in)
	if err != nil || !admitted {
		t.Fatalf("admission %v %v", admitted, err)
	}
	if attempt.Input.DiscoveryContent != "Immutable initial Discovery" || attempt.Input.DiscoveryBypassReason != "Discovery is sufficient" || attempt.Input.DiscoveryVersion != 1 {
		t.Fatalf("input provenance: %+v", attempt.Input)
	}
	ref := stageRef(t, store, brain.PipelineID, sdd.Spec, "complete_spec_001")
	complete := catalog.CompleteAuthoringStageRequest{Ref: ref, AttemptID: attempt.ID, SessionID: "stage_session_01", Content: []byte(validSpecDocument)}
	if _, err := store.CompleteAuthoringStage(t.Context(), complete); err == nil {
		t.Fatal("unlinked model result published")
	}
	linkStageAttempt(t, store, ref, attempt, complete.SessionID)
	draft, err := store.CompleteAuthoringStage(t.Context(), complete)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.GetPipeline(t.Context(), brain.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.State != "waiting_user" || draft.ArtifactVersion != 1 || draft.Artifacts[0].Author != "ai" || draft.Artifacts[0].SourceSessionID != complete.SessionID || p.Current != sdd.Spec || p.Status[sdd.Spec] != sdd.WaitingUser {
		t.Fatalf("draft crossed gate: %+v %+v", draft, p)
	}
	approval := catalog.AuthoringStageDecisionRequest{Ref: stageRef(t, store, p.ID, sdd.Spec, "approve_spec_001")}
	approved, err := store.ApproveAuthoringStage(t.Context(), approval)
	if err != nil {
		t.Fatal(err)
	}
	p, err = store.GetPipeline(t.Context(), p.ID)
	if err != nil || p.Current != sdd.Plan || p.Status[sdd.Spec] != sdd.Completed || approved.State != "approved" {
		t.Fatalf("approval: %+v %+v %v", approved, p, err)
	}
	plan := catalog.BeginAuthoringStageRequest{Ref: stageRef(t, store, p.ID, sdd.Plan, "generate_plan_001"), Selection: choice, ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("b", 64)}
	pa, admitted, err := store.BeginAuthoringStage(t.Context(), plan)
	if err != nil || !admitted || pa.Input.SpecVersion != 1 || string(pa.Input.SpecContent) != validSpecDocument || pa.Input.SpecHash == "" {
		t.Fatalf("Plan input: %+v %v %v", pa, admitted, err)
	}
	replayed, err := store.ApproveAuthoringStage(t.Context(), approval)
	if err != nil || !reflect.DeepEqual(approved, replayed) {
		t.Fatalf("approval receipt changed: %+v %v", replayed, err)
	}
}

func linkStageAttempt(t *testing.T, store *Store, ref catalog.AuthoringStageRequest, a catalog.AuthoringStageAttempt, sessionID string) {
	t.Helper()
	p, err := store.GetPipeline(t.Context(), ref.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: sessionID, WorkspaceID: p.WorkspaceID, BackendID: a.Selection.BackendID, BackendRevision: a.Selection.CatalogRevision, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateAuthoringStageSession(t.Context(), ref, a, record); err != nil {
		t.Fatal(err)
	}
	selection, err := store.GetSessionModelSelection(t.Context(), sessionID)
	if err != nil || selection.ModelID != a.Selection.ModelID || selection.MaxOutputTokens != a.Selection.MaxOutputTokens || selection.CredentialIdentity != a.Selection.CredentialIdentity {
		t.Fatalf("session provenance %+v %v", selection, err)
	}
}

func completedSpec(t *testing.T, store *Store, pipelineID string) catalog.AuthoringStageRun {
	t.Helper()
	choice := brainstormChoice()
	choice.MaxOutputTokens = 4096
	in := catalog.BeginAuthoringStageRequest{Ref: stageRef(t, store, pipelineID, sdd.Spec, "generate_spec_001"), Selection: choice, ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("a", 64)}
	a, admitted, err := store.BeginAuthoringStage(t.Context(), in)
	if err != nil || !admitted {
		t.Fatalf("admit: %v %v", admitted, err)
	}
	ref := stageRef(t, store, pipelineID, sdd.Spec, "complete_spec_001")
	linkStageAttempt(t, store, ref, a, "spec_session_001")
	run, err := store.CompleteAuthoringStage(t.Context(), catalog.CompleteAuthoringStageRequest{Ref: ref, AttemptID: a.ID, SessionID: "spec_session_001", Content: []byte(validSpecDocument)})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestAuthoringStageRevisionIsAtomicAndVersioned(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	completedSpec(t, store, brain.PipelineID)
	choice := brainstormChoice()
	choice.MaxOutputTokens = 1024
	in := catalog.BeginAuthoringStageRequest{Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "revise_spec_0001"), Selection: choice, ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("b", 64), Feedback: "Make acceptance measurable"}
	a, admitted, err := store.ReviseAuthoringStage(t.Context(), in)
	if err != nil || !admitted || a.ArtifactVersion != 2 || a.Input.PreviousArtifactVersion != 1 || string(a.Input.PreviousContent) != validSpecDocument || a.Input.Feedback != in.Feedback {
		t.Fatalf("revision: %+v %v %v", a, admitted, err)
	}
	run, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if run.Artifacts[0].Status != "rejected" || run.Artifacts[0].Version != 1 {
		t.Fatal("old version changed")
	}
	if _, err := store.ApproveAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "approve_old_spec_1")}); err == nil {
		t.Fatal("approved stale draft")
	}
	ref := stageRef(t, store, brain.PipelineID, sdd.Spec, "complete_spec_002")
	linkStageAttempt(t, store, ref, a, "spec_session_002")
	run, err = store.CompleteAuthoringStage(t.Context(), catalog.CompleteAuthoringStageRequest{Ref: ref, AttemptID: a.ID, SessionID: "spec_session_002", Content: []byte(strings.Replace(validSpecDocument, "Observable acceptance", "Measured acceptance", 1))})
	if err != nil || run.ArtifactVersion != 2 || len(run.Artifacts) != 2 || run.State != "waiting_user" {
		t.Fatalf("new draft %+v %v", run, err)
	}
	if replay, admitted, err := store.ReviseAuthoringStage(t.Context(), in); err != nil || admitted || replay.ID != a.ID {
		t.Fatalf("revision replay %v %v", admitted, err)
	}
	in.Feedback = "Different feedback"
	if _, admitted, err := store.ReviseAuthoringStage(t.Context(), in); !errors.Is(err, ErrPipelineConflict) || admitted {
		t.Fatal("different revision reused key")
	}
}

func TestAuthoringStageSkipCancellationAndRecovery(t *testing.T) {
	store, path, brain := authoringStageFixture(t)
	skip := catalog.AuthoringStageDecisionRequest{Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "skip_spec_000001"), Reason: "Use approved Discovery directly"}
	if _, err := store.SkipAuthoringStage(t.Context(), skip); err != nil {
		t.Fatal(err)
	}
	choice := brainstormChoice()
	choice.MaxOutputTokens = 4096
	in := catalog.BeginAuthoringStageRequest{Ref: stageRef(t, store, brain.PipelineID, sdd.Plan, "generate_plan_001"), Selection: choice, ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("c", 64)}
	a, _, err := store.BeginAuthoringStage(t.Context(), in)
	if err != nil || a.Input.SpecBypassReason != skip.Reason || a.Input.SpecVersion != 0 || len(a.Input.SpecContent) != 0 {
		t.Fatalf("bypass evidence: %+v %v", a, err)
	}
	cancel := catalog.AuthoringStageDecisionRequest{Ref: stageRef(t, store, brain.PipelineID, sdd.Plan, "cancel_plan_0001"), AttemptID: a.ID}
	paused, err := store.CancelAuthoringStage(t.Context(), cancel)
	if err != nil || paused.State != "paused" || paused.Attempts[0].ErrorCode != "cancelled" {
		t.Fatalf("cancel: %+v %v", paused, err)
	}
	in.Ref = stageRef(t, store, brain.PipelineID, sdd.Plan, "generate_plan_002")
	a, _, err = store.BeginAuthoringStage(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.InterruptRunningAuthoringStages(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, err := other.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Plan)
	if err != nil || run.State != "paused" || run.Attempts[1].Status != "interrupted" || run.AttemptCount != 2 || len(run.Artifacts) != 0 {
		t.Fatalf("recovery: %+v %v", run, err)
	}
	if replay, admitted, err := other.BeginAuthoringStage(t.Context(), in); err != nil || admitted || replay.ID != a.ID {
		t.Fatal("restart replay authorized inference")
	}
}

func TestAuthoringStagePreviewMatchesAdmittedInputWithoutMutation(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, "preview_generate_01")
	before, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	input, err := store.PreviewAuthoringStageInput(t.Context(), in.Ref, "")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preview changed state")
	}
	a, _, err := store.BeginAuthoringStage(t.Context(), in)
	if err != nil || !reflect.DeepEqual(input, a.Input) {
		t.Fatalf("preview drift: %+v %+v %v", input, a.Input, err)
	}
	if _, err := store.PreviewAuthoringStageInput(t.Context(), in.Ref, ""); !errors.Is(err, ErrPipelineConflict) {
		t.Fatal("stale preview accepted")
	}
	ref := stageRef(t, store, brain.PipelineID, sdd.Spec, "preview_complete_1")
	linkStageAttempt(t, store, ref, a, "preview_session_01")
	if _, err := store.CompleteAuthoringStage(t.Context(), catalog.CompleteAuthoringStageRequest{Ref: ref, AttemptID: a.ID, SessionID: "preview_session_01", Content: []byte(validSpecDocument)}); err != nil {
		t.Fatal(err)
	}
	revision := stageAdmission(t, store, brain.PipelineID, sdd.Spec, "preview_revision_1")
	revision.Feedback = "Clarify the criterion"
	input, err = store.PreviewAuthoringStageInput(t.Context(), revision.Ref, revision.Feedback)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err = store.ReviseAuthoringStage(t.Context(), revision)
	if err != nil || !reflect.DeepEqual(input, a.Input) {
		t.Fatalf("revision preview drift: %+v %+v %v", input, a.Input, err)
	}
}
