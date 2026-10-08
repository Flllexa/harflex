package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"

	"github.com/persioflexa/harflex/internal/security"
)

func TestPlanToolAcceptsAPlanAndChangesNothing(t *testing.T) {
	tool := NewPlanTool()
	if tool.Spec().Name != PlanToolName || tool.Risk() != security.ReadOnly {
		t.Fatalf("unexpected spec or risk: %s %v", tool.Spec().Name, tool.Risk())
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"plan":[{"step":"Ler o código","status":"completed"},{"step":"Ajustar","status":"in_progress"},{"step":"Testar","status":"pending"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		OK               bool
		Steps, Completed int
	}
	if err := json.Unmarshal(result.Content, &out); err != nil || !out.OK || out.Steps != 3 || out.Completed != 1 {
		t.Fatalf("unexpected result %s (%v)", result.Content, err)
	}
}

func TestPlanToolRejectsBadPlans(t *testing.T) {
	for name, args := range map[string]string{
		"empty":      `{"plan":[]}`,
		"blank step": `{"plan":[{"step":"  ","status":"pending"}]}`,
		"bad status": `{"plan":[{"step":"a","status":"doing"}]}`,
		"two active": `{"plan":[{"step":"a","status":"in_progress"},{"step":"b","status":"in_progress"}]}`,
		"not a plan": `{"plan":"a"}`,
	} {
		if _, err := NewPlanTool().Execute(context.Background(), json.RawMessage(args), nil); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestPlanToolArgumentErrorsAreRecoverable(t *testing.T) {
	_, err := NewPlanTool().Execute(context.Background(), json.RawMessage(`{"plan":[]}`), nil)
	var failure *agentcore.ToolFailure
	if !errors.As(err, &failure) || failure.Code != "invalid_arguments" {
		t.Fatalf("expected a recoverable failure, got %v", err)
	}
}
