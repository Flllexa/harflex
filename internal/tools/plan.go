package tools

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/security"
)

// PlanToolName is the tool an agent calls to show the person its plan; the activity panel draws it.
const PlanToolName = "update_plan"

const (
	maxPlanSteps    = 20
	maxPlanStepText = 300
)

// NewPlanTool lets the agent publish and update its plan of work. It changes nothing: the plan travels in the call's
// arguments, which the journal keeps and the activity panel draws as a flow.
func NewPlanTool() Tool { return planTool{} }

type planTool struct{}

func (planTool) Spec() agentcore.ToolSpec {
	return agentcore.ToolSpec{
		Name: PlanToolName,
		Description: "Show the person your plan for this task as a short list of steps, each pending, in_progress or completed. " +
			"Call it before starting work that takes several steps, and again whenever a step starts or finishes, sending the whole list each time. " +
			"Keep exactly one step in_progress while working. Write the steps in the person's language, as short actions (for example: 'Ler o módulo de baixas', 'Ajustar a validação', 'Rodar os testes'). It changes nothing in the project.",
		Schema: json.RawMessage(`{"type":"object","properties":{"explanation":{"type":"string","description":"Optional one-line note about what changed in the plan."},"plan":{"type":"array","maxItems":20,"items":{"type":"object","properties":{"step":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed"]}},"required":["step","status"],"additionalProperties":false}}},"required":["plan"],"additionalProperties":false}`),
	}
}

func (planTool) Risk() security.Risk { return security.ReadOnly }

func (planTool) Execute(ctx context.Context, args json.RawMessage, _ agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	var in struct {
		Explanation string `json:"explanation"`
		Plan        []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return agentcore.ToolExecutionResult{}, toolError(PlanToolName, nil, badArguments("plan must be a list of steps with step and status"))
	}
	if len(in.Plan) == 0 || len(in.Plan) > maxPlanSteps {
		return agentcore.ToolExecutionResult{}, toolError(PlanToolName, nil, badArguments("plan needs between 1 and %d steps", maxPlanSteps))
	}
	active, done := 0, 0
	for _, item := range in.Plan {
		step := strings.TrimSpace(item.Step)
		if step == "" || utf8.RuneCountInString(step) > maxPlanStepText {
			return agentcore.ToolExecutionResult{}, toolError(PlanToolName, nil, badArguments("each step needs a short text (up to %d characters)", maxPlanStepText))
		}
		switch item.Status {
		case "in_progress":
			active++
		case "completed":
			done++
		case "pending":
		default:
			return agentcore.ToolExecutionResult{}, toolError(PlanToolName, nil, badArguments("status must be pending, in_progress or completed"))
		}
	}
	if active > 1 {
		return agentcore.ToolExecutionResult{}, toolError(PlanToolName, nil, badArguments("keep at most one step in_progress"))
	}
	content, _ := json.Marshal(map[string]any{"ok": true, "steps": len(in.Plan), "completed": done})
	return agentcore.ToolExecutionResult{Content: content}, nil
}
