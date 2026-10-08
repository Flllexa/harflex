package sdd

import (
	"errors"
	"testing"
)

func TestApproveDiscoveryRequiresWaitingUser(t *testing.T) {
	flow := NewFlow()
	if _, err := ApproveDiscovery(flow); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("active Discovery approved: %v", err)
	}
	flow.Status[Discovery] = WaitingUser
	next, err := ApproveDiscovery(flow)
	if err != nil || next.Current != Spec || next.Status[Discovery] != Completed || flow.Status[Discovery] != WaitingUser {
		t.Fatalf("approval %+v %v", next, err)
	}
}

func TestFlowRequiresArtifactsAndNeverTreatsBypassAsCompletion(t *testing.T) {
	flow := NewFlow()
	if flow.Current != Discovery || flow.Status[Discovery] != Active || flow.Status[Spec] != Pending {
		t.Fatal(flow)
	}
	if _, err := Advance(flow, false); !errors.Is(err, ErrEvidenceRequired) {
		t.Fatalf("missing discovery artifact admitted: %v", err)
	}
	flow, _ = Advance(flow, true)
	if flow.Current != Spec || flow.Status[Discovery] != Completed {
		t.Fatal(flow)
	}
	flow, _ = Skip(flow)
	if flow.Current != Plan || flow.Status[Spec] != Skipped || flow.Status[Spec] == Completed {
		t.Fatal("bypass became completion", flow)
	}
	flow, _ = Advance(flow, true)
	if flow.Current != Code || flow.Status[Plan] != Completed {
		t.Fatal(flow)
	}
	if _, err := Advance(flow, false); !errors.Is(err, ErrEvidenceRequired) {
		t.Fatalf("code completed without execution evidence: %v", err)
	}
	if _, err := Skip(flow); !errors.Is(err, ErrEvidenceRequired) {
		t.Fatalf("code bypassed as successful implementation: %v", err)
	}
}

func TestEvaluatorFailureReturnsToCoderWithoutClaimingAcceptance(t *testing.T) {
	flow := NewFlow()
	for range 4 {
		var err error
		flow, err = Advance(flow, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	if flow.Current != Eval {
		t.Fatal(flow)
	}
	flow, err := Revise(flow)
	if err != nil || flow.Current != Code || flow.Status[Eval] != Failed || flow.Status[Code] != Active {
		t.Fatalf("rejected code was accepted: %+v %v", flow, err)
	}
	flow, _ = Advance(flow, true)
	flow, _ = Advance(flow, true)
	if flow.Current != PRs || flow.Status[Eval] != Completed || flow.Status[PRs] != Active {
		t.Fatal("passing evaluation did not hand over to the pull request stage", flow)
	}
	flow, err = Advance(flow, true)
	if err != nil || flow.Current != "" || flow.Status[PRs] != Completed {
		t.Fatalf("pull requests did not complete the pipeline: %+v %v", flow, err)
	}
}

func TestThePullRequestStageComesLastAndMaySkipButNeverFakesCompletion(t *testing.T) {
	if Stages[len(Stages)-1] != PRs || Stages[len(Stages)-2] != Eval {
		t.Fatalf("stage order = %v", Stages)
	}
	flow := NewFlow()
	if flow.Status[PRs] != Pending || len(flow.Status) != len(Stages) {
		t.Fatalf("new flow status = %v", flow.Status)
	}
	flow.Current, flow.Status[Eval], flow.Status[PRs] = PRs, Completed, Active
	skipped, err := Skip(flow)
	if err != nil || skipped.Current != "" || skipped.Status[PRs] != Skipped || skipped.Status[PRs] == Completed {
		t.Fatalf("skipping pull requests: %+v %v", skipped, err)
	}
	if flow.Status[PRs] != Active {
		t.Fatal("skip changed the flow it was given")
	}
	if _, err := Revise(flow); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("pull requests sent the pipeline back to Code: %v", err)
	}
}
