package application

import (
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func brainstormWithScreens(htmlSizes ...int) BrainstormContent {
	content := BrainstormContent{Flow: "flowchart TD\n  A[Abrir contratos] --> B[Filtrar por mês]", Notes: "## Decisões", Design: &BrainstormDesign{}}
	for index, size := range htmlSizes {
		name := []string{"Lista", "Detalhe", "Exportar"}[index]
		page := "<!doctype html><html><body><h1>" + name + "</h1>" + strings.Repeat("x", size) + "</body></html>"
		content.Design.Screens = append(content.Design.Screens, BrainstormScreen{Name: name, Purpose: "Tela de " + name, HTML: page})
	}
	return content
}

func TestCoderPromptCarriesApprovedFlowAndScreens(t *testing.T) {
	brainstorm := brainstormWithScreens(100)
	run := catalog.PipelineRun{Objective: "Contratos", Artifacts: map[sdd.Stage]catalog.PipelineArtifact{
		sdd.Spec:       {Content: "# SPEC"},
		sdd.Brainstorm: {Content: brainstorm.Canonical()},
		sdd.Plan:       {Content: "# Plan"},
		sdd.Code:       {Content: "diff"},
	}}
	coder := pipelinePrompt(run, "coder", "api", nil)
	if !strings.Contains(coder, "Abrir contratos") || !strings.Contains(coder, "<h1>Lista</h1>") || !strings.Contains(coder, "Tela de Lista") {
		t.Fatalf("Coder must see the approved flow and screens:\n%s", coder)
	}
	if strings.Contains(coder, `"noDesignReason"`) {
		t.Fatal("Coder must get the Brainstorm as readable context, not its stored JSON")
	}
	criteria := &catalog.PipelineEvaluationCriteriaSnapshot{SourceStage: sdd.Spec, SourceVersion: 1, SourceDigest: strings.Repeat("a", 64), SourceContent: "# SPEC", Digest: strings.Repeat("b", 64)}
	qa := pipelinePrompt(run, "evaluator", "api", criteria)
	if !strings.Contains(qa, "Abrir contratos") || strings.Contains(qa, "<!doctype") || !strings.Contains(qa, "Tela de Lista") {
		t.Fatalf("QA must see the flow and the screens by name, without HTML:\n%s", qa)
	}
	run.Artifacts[sdd.Brainstorm] = catalog.PipelineArtifact{}
	if strings.Contains(pipelinePrompt(run, "coder", "api", nil), "Brainstorm") {
		t.Fatal("work without a Brainstorm must not mention one")
	}
}

func TestBrainstormContextRespectsBudget(t *testing.T) {
	content := brainstormWithScreens(90*1024, 90*1024, 90*1024).Canonical()
	context := brainstormExecutionContext(content, 200*1024)
	for _, name := range []string{"<h1>Lista</h1>", "<h1>Detalhe</h1>"} {
		if !strings.Contains(context, name) {
			t.Fatalf("screen %s must fit in full", name)
		}
	}
	if strings.Contains(context, "<h1>Exportar</h1>") || !strings.Contains(context, "- Exportar: Tela de Exportar") {
		t.Fatal("the screen past the budget must come by name and purpose only")
	}
	if len(context) > 200*1024+4096 {
		t.Fatalf("context is %d bytes, over the budget", len(context))
	}
	if brainstormExecutionContext("", 200*1024) != "" || brainstormExecutionContext("not json", 200*1024) != "" {
		t.Fatal("no Brainstorm gives no context")
	}
}
