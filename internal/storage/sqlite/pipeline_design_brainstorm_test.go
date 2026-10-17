package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

const brainstormDocumentForTest = `{"flow":"flowchart TD\n A-->B","design":null,"noDesignReason":"Só backend.","notes":""}`

func TestEditingSpecMakesBrainstormAndPlanStale(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC v1")
	workspace = editDesign(t, store, workspace, sdd.Brainstorm, brainstormDocumentForTest)
	workspace = editDesign(t, store, workspace, sdd.Plan, "Plan v1")
	for _, stage := range []sdd.Stage{sdd.Spec, sdd.Brainstorm, sdd.Plan} {
		if workspace.Documents[stage].Stale {
			t.Fatalf("%s written in order must be current: %+v", stage, workspace.Documents[stage])
		}
	}
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC v2")
	if workspace.Documents[sdd.Spec].Stale || !workspace.Documents[sdd.Brainstorm].Stale || !workspace.Documents[sdd.Plan].Stale {
		t.Fatalf("editing SPEC must make Brainstorm and Plan stale: %+v", workspace.Documents)
	}
	workspace = editDesign(t, store, workspace, sdd.Discovery, "Discovery v2")
	if !workspace.Documents[sdd.Spec].Stale || !workspace.Documents[sdd.Brainstorm].Stale || !workspace.Documents[sdd.Plan].Stale {
		t.Fatalf("editing Discovery must make every later document stale: %+v", workspace.Documents)
	}
}

func TestEditingBrainstormMakesPlanStale(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC v1")
	workspace = editDesign(t, store, workspace, sdd.Brainstorm, brainstormDocumentForTest)
	workspace = editDesign(t, store, workspace, sdd.Plan, "Plan v1")
	before := workspace.Documents[sdd.Plan].SourceDigest
	workspace = editDesign(t, store, workspace, sdd.Brainstorm, strings.Replace(brainstormDocumentForTest, "TD", "LR", 1))
	if workspace.Documents[sdd.Spec].Stale || workspace.Documents[sdd.Brainstorm].Stale || !workspace.Documents[sdd.Plan].Stale {
		t.Fatalf("editing Brainstorm must make only Plan stale: %+v", workspace.Documents)
	}
	workspace = editDesign(t, store, workspace, sdd.Plan, "Plan v2")
	if workspace.Documents[sdd.Plan].Stale || workspace.Documents[sdd.Plan].SourceDigest == before {
		t.Fatal("Plan's source digest must follow the Brainstorm it was written from")
	}
}

func TestBrainstormWrittenOverAStaleSpecIsStale(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC v1")
	workspace = editDesign(t, store, workspace, sdd.Discovery, "Discovery v2")
	workspace = editDesign(t, store, workspace, sdd.Brainstorm, brainstormDocumentForTest)
	if !workspace.Documents[sdd.Brainstorm].Stale {
		t.Fatal("a Brainstorm written from a stale SPEC must be stale too")
	}
	workspace = editDesign(t, store, workspace, sdd.Brainstorm, strings.Repeat(" ", 100*1024)+brainstormDocumentForTest)
	if workspace.Documents[sdd.Brainstorm].Version != 2 {
		t.Fatal("a Brainstorm larger than the generic 64 KB limit must be accepted")
	}
}

func TestBrainstormRetryRelinksItsSessionOnce(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace = editDesign(t, store, workspace, sdd.Spec, "SPEC v1")
	workspace, attempt, _ := beginDesign(t, store, workspace, "prepare_brainstorm_relink_01", "brainstorm")
	workspace = linkDesignSession(t, store, workspace, attempt, sdd.Brainstorm, "brainstorm-first")
	session := func(id string) {
		selection := attempt.Selections[sdd.Brainstorm]
		selection.SessionID = id
		if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: id, WorkspaceID: workspace.WorkspaceID, BackendID: "local", Status: "running", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil, "", nil, "design_documents", &selection); err != nil {
			t.Fatal(err)
		}
	}
	session("brainstorm-retry")
	if _, err := store.RelinkPipelineDesignSession(t.Context(), workspace.PipelineID, attempt.ID, sdd.Brainstorm, "someone-else", "brainstorm-retry"); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("a retry must name the session it replaces: %v", err)
	}
	relinked, err := store.RelinkPipelineDesignSession(t.Context(), workspace.PipelineID, attempt.ID, sdd.Brainstorm, "brainstorm-first", "brainstorm-retry")
	if err != nil || relinked.Attempts[0].SessionIDs[sdd.Brainstorm] != "brainstorm-retry" {
		t.Fatalf("retry link = %+v, %v", relinked.Attempts, err)
	}
	if _, err := store.RelinkPipelineDesignSession(t.Context(), workspace.PipelineID, attempt.ID, sdd.Brainstorm, "brainstorm-first", "brainstorm-retry"); err != nil {
		t.Fatalf("the same retry link must replay: %v", err)
	}
	session("brainstorm-third")
	if _, err := store.RelinkPipelineDesignSession(t.Context(), workspace.PipelineID, attempt.ID, sdd.Brainstorm, "brainstorm-retry", "brainstorm-third"); err == nil {
		t.Fatal("Brainstorm gets one corrective retry, not more")
	}
	reloaded, err := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || reloaded.Attempts[0].SessionIDs[sdd.Brainstorm] != "brainstorm-retry" {
		t.Fatalf("the retry link must be the one loaded: %+v %v", reloaded.Attempts, err)
	}
	completed, err := store.CompletePipelineDesignAttempt(t.Context(), catalog.PipelineDesignCompleteRequest{PipelineID: workspace.PipelineID, AttemptID: attempt.ID, SourceRevision: attempt.SourceRevision,
		Documents: map[sdd.Stage]catalog.PipelineDesignDocumentInput{sdd.Brainstorm: {Content: brainstormDocumentForTest, Author: "ai", SourceSessionID: "brainstorm-retry"}}, Summary: "Pronto."})
	if err != nil || completed.Documents[sdd.Brainstorm].SourceSessionID != "brainstorm-retry" {
		t.Fatalf("completing from the retry session = %+v, %v", completed.Documents[sdd.Brainstorm], err)
	}
}

func TestOnlyBrainstormGetsARetryLink(t *testing.T) {
	store, _, workspace := designFixture(t)
	workspace, attempt, _ := beginDesign(t, store, workspace, "prepare_spec_relink_01", "spec")
	workspace = linkDesignSession(t, store, workspace, attempt, sdd.Spec, "spec-first")
	selection := attempt.Selections[sdd.Spec]
	selection.SessionID = "spec-retry"
	if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: "spec-retry", WorkspaceID: workspace.WorkspaceID, BackendID: "local", Status: "running", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil, "", nil, "design_documents", &selection); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RelinkPipelineDesignSession(t.Context(), workspace.PipelineID, attempt.ID, sdd.Spec, "spec-first", "spec-retry"); err == nil {
		t.Fatal("only Brainstorm answers are retried")
	}
}

func designApproval(workspace catalog.PipelineDesignWorkspace, requestID string, stages ...sdd.Stage) catalog.PipelineDesignApproveRequest {
	request := catalog.PipelineDesignApproveRequest{Ref: designRef(workspace, requestID), Digests: map[sdd.Stage]string{}}
	for _, stage := range stages {
		request.Digests[stage] = workspace.Documents[stage].ContentDigest
	}
	return request
}

// approveBrainstormGate approves the flow and design and returns the work and its design as they stand after it.
func approveBrainstormGate(t *testing.T, store *Store, workspace catalog.PipelineDesignWorkspace) (catalog.PipelineRun, catalog.PipelineDesignWorkspace) {
	t.Helper()
	run, err := store.ApprovePipelineBrainstorm(t.Context(), designApproval(workspace, fmt.Sprintf("approve_brainstorm_%d", workspace.Revision), sdd.Discovery, sdd.Spec, sdd.Brainstorm))
	if err != nil || run.Current != sdd.Plan || run.Status[sdd.Brainstorm] != sdd.Completed || run.Status[sdd.Plan] != sdd.Active {
		t.Fatalf("flow and design approval = %+v, %v", run, err)
	}
	updated, err := store.GetPipelineDesign(t.Context(), workspace.PipelineID)
	if err != nil || updated.State != "ready" || updated.NeedsDerivation {
		t.Fatalf("the design must stay editable for the Plan: %+v %v", updated, err)
	}
	return run, updated
}
