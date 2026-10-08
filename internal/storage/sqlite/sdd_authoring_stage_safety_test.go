package sqlite

import (
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

func stageAdmission(t *testing.T, store *Store, pipelineID string, stage sdd.Stage, key string) catalog.BeginAuthoringStageRequest {
	t.Helper()
	choice := brainstormChoice()
	choice.MaxOutputTokens = 4096
	return catalog.BeginAuthoringStageRequest{Ref: stageRef(t, store, pipelineID, stage, key), Selection: choice, ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", PreferenceRevision: 0, EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("c", 64)}
}

func TestAuthoringStageConcurrentAdmissionAndConflictingKeys(t *testing.T) {
	store, path, brain := authoringStageFixture(t)
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, "concurrent_stage_01")
	type result struct {
		a        catalog.AuthoringStageAttempt
		admitted bool
		err      error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, db := range []*Store{store, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, admitted, err := db.BeginAuthoringStage(t.Context(), in)
			results <- result{a, admitted, err}
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	id := ""
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.admitted {
			count++
		}
		if id != "" && id != r.a.ID {
			t.Fatal("two attempts created")
		}
		id = r.a.ID
	}
	if count != 1 {
		t.Fatalf("admitted %d", count)
	}
	run, err := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil || run.AttemptCount != 1 || run.InputBudgetRemaining != sdd.AuthoringInputBudget-4096 {
		t.Fatalf("duplicate reservation %+v %v", run, err)
	}
	for _, change := range []func(*catalog.BeginAuthoringStageRequest){func(v *catalog.BeginAuthoringStageRequest) { v.Selection.ModelID = "other" }, func(v *catalog.BeginAuthoringStageRequest) { v.Ref.PipelineRevision++ }, func(v *catalog.BeginAuthoringStageRequest) { v.ClientIntentHash = strings.Repeat("d", 64) }} {
		changed := in
		change(&changed)
		if _, admitted, err := store.BeginAuthoringStage(t.Context(), changed); !errors.Is(err, ErrPipelineConflict) || admitted {
			t.Fatalf("changed replay %v %v", admitted, err)
		}
	}
}

func TestAuthoringStageAdmissionFailuresRollback(t *testing.T) {
	for _, scenario := range []string{"stale_pipeline", "stale_stage", "stale_discovery", "stale_artifact", "receipt_failure", "event_failure", "unknown_model", "output_cap", "context"} {
		t.Run(scenario, func(t *testing.T) {
			store, _, brain := authoringStageFixture(t)
			in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, "reject_admission_01")
			switch scenario {
			case "stale_pipeline":
				in.Ref.PipelineRevision++
			case "stale_stage":
				in.Ref.StageRevision++
			case "stale_discovery":
				in.Ref.DiscoveryVersion++
			case "stale_artifact":
				in.Ref.ArtifactVersion++
			case "receipt_failure":
				_, _ = store.DB().Exec(`CREATE TRIGGER fail_stage_receipt BEFORE INSERT ON pipeline_authoring_requests BEGIN SELECT RAISE(ABORT,'injected'); END`)
			case "event_failure":
				_, _ = store.DB().Exec(`CREATE TRIGGER fail_stage_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'injected'); END`)
			case "unknown_model":
				in.Selection.Source = "generic_manual"
			case "output_cap":
				in.Selection.MaxOutputTokens = 4097
			case "context":
				in.Selection.ContextLength = 4096
			}
			before, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
			pBefore, _ := store.GetPipeline(t.Context(), brain.PipelineID)
			if _, admitted, err := store.BeginAuthoringStage(t.Context(), in); err == nil || admitted {
				t.Fatal("invalid admission accepted")
			}
			after, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
			pAfter, _ := store.GetPipeline(t.Context(), brain.PipelineID)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(pBefore, pAfter) {
				t.Fatal("failed admission changed durable state")
			}
		})
	}
}

func TestAuthoringStageSixAttemptsNeverRefundBudget(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	for i := 0; i < 6; i++ {
		in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, fmt.Sprintf("bounded_attempt_%02d", i))
		a, admitted, err := store.BeginAuthoringStage(t.Context(), in)
		if err != nil || !admitted {
			t.Fatalf("attempt %d: %v %v", i, admitted, err)
		}
		ref := stageRef(t, store, brain.PipelineID, sdd.Spec, fmt.Sprintf("failure_receipt_%02d", i))
		run, err := store.FailAuthoringStage(t.Context(), catalog.FailAuthoringStageRequest{Ref: ref, AttemptID: a.ID, ErrorCode: "provider_failed"})
		if err != nil || run.AttemptCount != i+1 || run.InputBudgetRemaining != sdd.AuthoringInputBudget-int64(i+1)*4096 || run.OutputBudgetRemaining != sdd.AuthoringOutputBudget-int64(i+1)*4096 || len(run.Artifacts) != 0 {
			t.Fatalf("reservation refunded: %+v %v", run, err)
		}
	}
	in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, "seventh_attempt_01")
	if _, admitted, err := store.BeginAuthoringStage(t.Context(), in); err == nil || admitted {
		t.Fatal("seventh call admitted")
	}
}

func TestAuthoringStageThreeDraftsAndStableHistory(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	run := completedSpec(t, store, brain.PipelineID)
	for version := 2; version <= 3; version++ {
		in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, fmt.Sprintf("revise_version_%02d", version))
		in.Feedback = "Clarify acceptance"
		a, admitted, err := store.ReviseAuthoringStage(t.Context(), in)
		if err != nil || !admitted {
			t.Fatalf("revision: %v %v", admitted, err)
		}
		ref := stageRef(t, store, brain.PipelineID, sdd.Spec, fmt.Sprintf("complete_version_%d", version))
		session := fmt.Sprintf("version_session_%d", version)
		linkStageAttempt(t, store, ref, a, session)
		run, err = store.CompleteAuthoringStage(t.Context(), catalog.CompleteAuthoringStageRequest{Ref: ref, AttemptID: a.ID, SessionID: session, Content: []byte(validSpecDocument)})
		if err != nil {
			t.Fatal(err)
		}
	}
	in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, "fourth_version_001")
	in.Feedback = "One more"
	if _, admitted, err := store.ReviseAuthoringStage(t.Context(), in); err == nil || admitted {
		t.Fatal("fourth draft admitted")
	}
	after, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if !reflect.DeepEqual(run, after) || len(after.Artifacts) != 3 || after.Artifacts[0].Status != "rejected" || after.Artifacts[2].Status != "waiting_user" {
		t.Fatal("history changed on limit")
	}
}

func TestAuthoringStageOutputUsageAndCancelledPublication(t *testing.T) {
	for _, scenario := range []string{"unknown_usage", "input_overrun", "output_overrun", "invalid_document", "cancelled", "stale", "artifact_failure", "event_failure"} {
		t.Run(scenario, func(t *testing.T) {
			store, _, brain := authoringStageFixture(t)
			in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, "settlement_stage_01")
			a, _, err := store.BeginAuthoringStage(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			ref := stageRef(t, store, brain.PipelineID, sdd.Spec, "settle_stage_0001")
			linkStageAttempt(t, store, ref, a, "settlement_session")
			complete := catalog.CompleteAuthoringStageRequest{Ref: ref, AttemptID: a.ID, SessionID: "settlement_session", Content: []byte(validSpecDocument)}
			switch scenario {
			case "input_overrun":
				complete.Usage = &catalog.BrainstormUsage{InputTokens: a.ReservedInputTokens + 1, OutputTokens: 10}
			case "output_overrun":
				complete.Usage = &catalog.BrainstormUsage{InputTokens: 10, OutputTokens: a.ReservedOutputTokens + 1}
			case "invalid_document":
				complete.Content = []byte(`{"summary":"partial"}`)
			case "cancelled":
				if _, err := store.CancelAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: ref, AttemptID: a.ID}); err != nil {
					t.Fatal(err)
				}
			case "stale":
				complete.Ref.StageRevision++
			case "artifact_failure":
				_, _ = store.DB().Exec(`CREATE TRIGGER fail_stage_artifact BEFORE INSERT ON pipeline_authoring_artifacts BEGIN SELECT RAISE(ABORT,'injected'); END`)
			case "event_failure":
				_, _ = store.DB().Exec(`CREATE TRIGGER fail_result_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'injected'); END`)
			}
			before, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
			_, err = store.CompleteAuthoringStage(t.Context(), complete)
			after, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
			if scenario == "unknown_usage" {
				if err != nil || after.State != "waiting_user" || after.Attempts[0].Usage != nil || after.InputBudgetRemaining != before.InputBudgetRemaining {
					t.Fatalf("unknown usage: %+v %v", after, err)
				}
			} else if scenario == "input_overrun" || scenario == "output_overrun" {
				if !errors.Is(err, sdd.ErrAuthoringBudgetExceeded) || after.State != "paused" || after.Attempts[0].ErrorCode != "budget_overrun" || len(after.Artifacts) != 0 {
					t.Fatalf("overrun: %+v %v", after, err)
				}
				if _, err := store.CompleteAuthoringStage(t.Context(), complete); !errors.Is(err, sdd.ErrAuthoringBudgetExceeded) {
					t.Fatal("overrun replay changed outcome")
				}
			} else if err == nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("invalid publication changed state: %v", err)
			}
		})
	}
}

func TestAuthoringStageHumanFailuresRollbackAndPreserveLegacy(t *testing.T) {
	for _, scenario := range []string{"stale_version", "stale_pipeline", "stale_stage", "receipt_failure", "event_failure"} {
		t.Run(scenario, func(t *testing.T) {
			store, _, brain := authoringStageFixture(t)
			completedSpec(t, store, brain.PipelineID)
			in := catalog.AuthoringStageDecisionRequest{Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "reject_approval_01")}
			switch scenario {
			case "stale_version":
				in.Ref.ArtifactVersion++
			case "stale_pipeline":
				in.Ref.PipelineRevision++
			case "stale_stage":
				in.Ref.StageRevision++
			case "receipt_failure":
				_, _ = store.DB().Exec(`CREATE TRIGGER fail_human_receipt BEFORE INSERT ON pipeline_authoring_requests BEGIN SELECT RAISE(ABORT,'injected'); END`)
			case "event_failure":
				_, _ = store.DB().Exec(`CREATE TRIGGER fail_human_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'injected'); END`)
			}
			before, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
			pBefore, _ := store.GetPipeline(t.Context(), brain.PipelineID)
			if _, err := store.ApproveAuthoringStage(t.Context(), in); err == nil {
				t.Fatal("invalid approval accepted")
			}
			after, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
			pAfter, _ := store.GetPipeline(t.Context(), brain.PipelineID)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(pBefore, pAfter) {
				t.Fatal("failed approval changed evidence")
			}
			if err := store.SavePipelineArtifact(t.Context(), brain.PipelineID, sdd.Spec, "manual overwrite", pBefore.Revision); err == nil {
				t.Fatal("manual authoring overwrite")
			}
		})
	}
}

func TestAuthoringStageEvidenceCannotBeMutatedOrDeleted(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	run := completedSpec(t, store, brain.PipelineID)
	for _, statement := range []string{
		`UPDATE pipeline_authoring_attempts SET selection_snapshot='{}'`,
		`UPDATE pipeline_authoring_artifacts SET content='{}'`,
		`UPDATE pipeline_authoring_requests SET client_intent_hash='changed'`,
		`DELETE FROM pipeline_authoring_requests`,
		`DELETE FROM pipeline_authoring_artifacts`,
	} {
		if _, err := store.DB().Exec(statement); err == nil {
			t.Fatalf("mutable evidence: %s", statement)
		}
	}
	after, _ := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if !reflect.DeepEqual(run, after) {
		t.Fatal("historical evidence changed")
	}
}

func TestAuthoringStageRejectsDiscoverySnapshotDrift(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	if _, err := store.DB().Exec(`UPDATE pipeline_artifacts SET content='Different content at the same version' WHERE pipeline_id=? AND stage='discovery'`, brain.PipelineID); err != nil {
		t.Fatal(err)
	}
	in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, "drifted_source_001")
	if _, admitted, err := store.BeginAuthoringStage(t.Context(), in); err == nil || admitted {
		t.Fatal("unapproved Discovery content admitted")
	}
}

func TestAuthoringStageCancellationSurvivesDiscoverySnapshotDrift(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, "cancel_drift_start_1")
	attempt, admitted, err := store.BeginAuthoringStage(t.Context(), in)
	if err != nil || !admitted {
		t.Fatalf("admission: %v %v", admitted, err)
	}
	ref := stageRef(t, store, brain.PipelineID, sdd.Spec, "cancel_drift_00001")
	linkStageAttempt(t, store, ref, attempt, "cancel_drift_session")
	if _, err := store.DB().Exec(`UPDATE pipeline_artifacts SET content='Discovery diverged after admission' WHERE pipeline_id=? AND stage='discovery'`, brain.PipelineID); err != nil {
		t.Fatal(err)
	}
	ref = stageRef(t, store, brain.PipelineID, sdd.Spec, "cancel_drift_00001")
	paused, err := store.CancelAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: ref, AttemptID: attempt.ID})
	if err != nil {
		t.Fatalf("source drift must not prevent cancellation: %v", err)
	}
	if paused.State != "cancellation_pending" || !paused.CancellationPending || paused.Attempts[0].Status != "interrupted" || paused.Attempts[0].ErrorCode != "cancelled" || paused.PipelineRevision != ref.PipelineRevision+1 || paused.Revision != ref.StageRevision+1 {
		t.Fatalf("cancellation fence not persisted: %+v", paused)
	}
	current := stageRef(t, store, brain.PipelineID, sdd.Spec, "late_drift_result_1")
	for _, completionRef := range []catalog.AuthoringStageRequest{ref, current} {
		if _, err := store.CompleteAuthoringStage(t.Context(), catalog.CompleteAuthoringStageRequest{Ref: completionRef, AttemptID: attempt.ID, SessionID: "cancel_drift_session", Content: []byte(validSpecDocument)}); !errors.Is(err, ErrPipelineConflict) {
			t.Fatalf("late completion must be rejected: %v", err)
		}
	}
	after, err := store.GetAuthoringStage(t.Context(), brain.PipelineID, sdd.Spec)
	if err != nil || !reflect.DeepEqual(paused, after) {
		t.Fatalf("late completion changed cancellation: %+v %v", after, err)
	}
}

func TestAuthoringStagePlanApprovalStopsAtCodeGate(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	completedSpec(t, store, brain.PipelineID)
	if _, err := store.ApproveAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: stageRef(t, store, brain.PipelineID, sdd.Spec, "approve_spec_plan_1")}); err != nil {
		t.Fatal(err)
	}
	in := stageAdmission(t, store, brain.PipelineID, sdd.Plan, "generate_full_plan")
	a, _, err := store.BeginAuthoringStage(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	ref := stageRef(t, store, brain.PipelineID, sdd.Plan, "complete_full_plan")
	linkStageAttempt(t, store, ref, a, "full_plan_session")
	draft, err := store.CompleteAuthoringStage(t.Context(), catalog.CompleteAuthoringStageRequest{Ref: ref, AttemptID: a.ID, SessionID: "full_plan_session", Content: []byte(validPlanDocument)})
	if err != nil || draft.State != "waiting_user" {
		t.Fatalf("Plan draft: %+v %v", draft, err)
	}
	p, err := store.GetPipeline(t.Context(), brain.PipelineID)
	if err != nil || p.Current != sdd.Plan || p.Status[sdd.Code] != sdd.Pending {
		t.Fatal("Plan completion started Code")
	}
	if _, err := store.ApproveAuthoringStage(t.Context(), catalog.AuthoringStageDecisionRequest{Ref: stageRef(t, store, brain.PipelineID, sdd.Plan, "approve_full_plan")}); err != nil {
		t.Fatal(err)
	}
	p, err = store.GetPipeline(t.Context(), brain.PipelineID)
	if err != nil || p.Current != sdd.Code || p.Status[sdd.Plan] != sdd.Completed {
		t.Fatal("Plan gate did not advance")
	}
	if _, err := store.GetAuthoringStage(t.Context(), p.ID, sdd.Code); err == nil {
		t.Fatal("SPEC/Plan contract admitted Code")
	}
	sessions, err := store.ListPipelineSessions(t.Context(), p.ID, "")
	if err != nil || len(sessions) != 0 {
		t.Fatal("Code session auto-created")
	}
}

func TestAuthoringStageExpiredAttemptCannotPublish(t *testing.T) {
	store, _, brain := authoringStageFixture(t)
	in := stageAdmission(t, store, brain.PipelineID, sdd.Spec, "expired_stage_001")
	a, _, err := store.BeginAuthoringStage(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	ref := stageRef(t, store, brain.PipelineID, sdd.Spec, "expire_result_001")
	linkStageAttempt(t, store, ref, a, "expired_stage_session")
	// Move only this isolated fixture's creation time; production snapshots are immutable.
	if _, err := store.DB().Exec(`DROP TRIGGER authoring_immutable_attempt`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE pipeline_authoring_attempts SET created_at=? WHERE id=?`, formatCatalogTime(time.Now().Add(-91*time.Second)), a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateAuthoringStageSession(t.Context(), ref, a.ID, "expired_stage_session"); err == nil {
		t.Fatal("expired attempt executable")
	}
	if _, err := store.CompleteAuthoringStage(t.Context(), catalog.CompleteAuthoringStageRequest{Ref: ref, AttemptID: a.ID, SessionID: "expired_stage_session", Content: []byte(validSpecDocument)}); err == nil {
		t.Fatal("expired output published")
	}
	run, err := store.FailAuthoringStage(t.Context(), catalog.FailAuthoringStageRequest{Ref: ref, AttemptID: a.ID, ErrorCode: "timeout"})
	if err != nil || run.State != "cancellation_pending" || !run.CancellationPending || len(run.Artifacts) != 0 || run.Attempts[0].ErrorCode != "timeout" {
		t.Fatalf("timeout settlement: %+v %v", run, err)
	}
}
