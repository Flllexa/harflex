package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/repositories"
)

func TestErrorCodesAreStableAndHideDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{safe("prompt", agentcore.ErrSessionBusy), "session_busy"},
		{safe("prompt", externalagent.ErrSessionBusy), "session_busy"},
		{safe("approve", agentcore.ErrApprovalNotFound), "approval_not_found"},
		{ErrSessionNotFound, "session_not_found"},
		{ErrPipelineGitRequired, "pipeline_git_required"},
		{ErrPipelineGitDirty, "pipeline_git_dirty"},
		{ErrPipelineGitBaselineUnavailable, "pipeline_git_baseline_unavailable"},
		{ErrPipelineCodeSnapshotUnavailable, "pipeline_code_snapshot_unavailable"},
		{ErrSDDCLIReadIsolationUnavailable, "sdd_cli_read_isolation_unavailable"},
		{ErrPipelineDesignModelRequired, "pipeline_design_model_required"},
		{ErrPipelineDesignExecutorUnavailable, "pipeline_design_executor_unavailable"},
		{externalagent.ErrCodexDocumentProtocol, "pipeline_design_invalid_response"},
		{repositories.ErrExecutionCopyConfirmationRequired, "execution_copy_confirmation_required"},
		{repositories.ErrExecutionSourceDrift, "pipeline_source_drift"},
		{repositories.ErrExecutionApplyConflict, "pipeline_code_apply_conflict"},
		{fmt.Errorf("wrapped: %w", ErrInvalidInput), "invalid_input"},
		{context.Canceled, "cancelled"},
		{safe("prompt", errors.New("secret-canary-123")), "internal"},
	} {
		if got := ErrorCode(tc.err); got != tc.code {
			t.Fatalf("%v: code=%s want %s", tc.err, got, tc.code)
		}
		var payload map[string]string
		if err := json.Unmarshal(MarshalError(tc.err), &payload); err != nil || len(payload) != 1 || payload["code"] != tc.code {
			t.Fatalf("payload=%v err=%v", payload, err)
		}
	}
}

func TestRunResultDoesNotExposeToolFailureCause(t *testing.T) {
	cause := errors.New("api-key-supersecret")
	ended := agentcore.NewRunError("tool_failed", cause)
	result, err := runResult("prompt", ended)
	if err != nil || result.Status != RunFailed || result.Reason != "tool_failed" || !errors.Is(ended, cause) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	data, err := json.Marshal(result)
	if err != nil || strings.Contains(string(data), cause.Error()) || strings.Contains(ended.Error(), cause.Error()) {
		t.Fatalf("unsafe outcome=%s err=%v", data, err)
	}
}

func TestRunResultSeparatesTerminalRunsFromRefusedCalls(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want RunResultDTO
		code string
	}{
		{"completed", nil, RunResultDTO{Status: RunCompleted}, ""},
		{"cancelled", agentcore.NewRunError("cancelled", context.Canceled), RunResultDTO{Status: RunCancelled}, ""},
		{"failed", agentcore.NewRunError("turn_limit", errors.New("secret-canary-456")), RunResultDTO{Status: RunFailed, Reason: "turn_limit"}, ""},
		{"backend changed", agentcore.NewRunError("execution_failed", ErrBackendChanged), RunResultDTO{Status: RunFailed, Reason: "execution_failed", Code: "backend_changed"}, ""},
		{"busy", agentcore.ErrSessionBusy, RunResultDTO{}, "session_busy"},
		{"journal fault", errors.New("secret-canary-789"), RunResultDTO{}, "internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runResult("prompt", tc.err)
			if got.Status != tc.want.Status || got.Reason != tc.want.Reason || got.Code != tc.want.Code || got.Approval != nil {
				t.Fatalf("result=%#v", got)
			}
			if (err != nil) != (tc.code != "") || (err != nil && (ErrorCode(err) != tc.code || err.Error() != "prompt: operation failed")) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	required := &agentcore.ApprovalRequiredError{Approval: agentcore.ApprovalRequest{ID: "a", ToolCallID: "c", Name: "write", Risk: "write", Arguments: json.RawMessage(`{"path":"x"}`)}}
	got, err := runResult("prompt", required)
	if err != nil || got.Status != RunAwaitingApproval || !reflect.DeepEqual(*got.Approval, ApprovalDTO{ID: "a", ToolCallID: "c", Name: "write", Risk: "write", Arguments: json.RawMessage(`{"path":"x"}`)}) || string(got.Approval.Arguments) != `{"path":"x"}` {
		t.Fatalf("result=%#v err=%v", got, err)
	}
	required.Approval.Arguments[0] = '!'
	if string(got.Approval.Arguments) != `{"path":"x"}` {
		t.Fatal("approval DTO shares argument buffer")
	}
}
