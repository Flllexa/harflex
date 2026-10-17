package sqlite

import (
	"strings"
	"testing"

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
