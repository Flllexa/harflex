package application

import (
	"reflect"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func TestStagesForAllAfterSpecStartsAtBrainstorm(t *testing.T) {
	workspace := func(written ...sdd.Stage) catalog.PipelineDesignWorkspace {
		documents := map[sdd.Stage]catalog.PipelineDesignDocument{sdd.Discovery: {Content: "D"}}
		for _, stage := range written {
			documents[stage] = catalog.PipelineDesignDocument{Content: string(stage)}
		}
		return catalog.PipelineDesignWorkspace{Documents: documents}
	}
	for _, tc := range []struct {
		name, target string
		workspace    catalog.PipelineDesignWorkspace
		want         []sdd.Stage
	}{
		{"all from Discovery", "all", workspace(), []sdd.Stage{sdd.Spec, sdd.Brainstorm}},
		{"all with SPEC written", "all", workspace(sdd.Spec), []sdd.Stage{sdd.Brainstorm}},
		{"all with SPEC and Plan from before", "all", workspace(sdd.Spec, sdd.Plan), []sdd.Stage{sdd.Brainstorm}},
		{"all with everything", "all", workspace(sdd.Spec, sdd.Brainstorm, sdd.Plan), []sdd.Stage{sdd.Discovery, sdd.Spec, sdd.Brainstorm}},
		{"discovery", "discovery", workspace(sdd.Spec), []sdd.Stage{sdd.Discovery, sdd.Spec, sdd.Brainstorm}},
		{"spec", "spec", workspace(sdd.Spec), []sdd.Stage{sdd.Spec, sdd.Brainstorm}},
		{"brainstorm", "brainstorm", workspace(sdd.Spec), []sdd.Stage{sdd.Brainstorm}},
		{"plan", "plan", workspace(sdd.Spec, sdd.Brainstorm), []sdd.Stage{sdd.Plan}},
		{"unknown", "code", workspace(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pipelineDesignStagesFor(tc.target, tc.workspace); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("pipelineDesignStagesFor(%q) = %v, want %v", tc.target, got, tc.want)
			}
		})
	}
	if !designStage("brainstorm") || len(designStages()) != 4 || designStages()[2] != sdd.Brainstorm {
		t.Fatalf("design stages = %v", designStages())
	}
}
