package agentcore

import "encoding/json"

// ApprovalRequest describes the stored tool call a pending approval would run.
type ApprovalRequest struct {
	ID         string          `json:"approvalId"`
	ToolCallID string          `json:"toolCallId"`
	Name       string          `json:"name"`
	Risk       string          `json:"risk"`
	Arguments  json.RawMessage `json:"arguments"`
}

// ApprovalRequiredError is returned while a run waits for Approve. It matches
// ErrApprovalRequired with errors.Is.
type ApprovalRequiredError struct{ Approval ApprovalRequest }

func (e *ApprovalRequiredError) Error() string        { return ErrApprovalRequired.Error() }
func (e *ApprovalRequiredError) Is(target error) bool { return target == ErrApprovalRequired }

// RunError reports a run that durably reached a terminal state other than
// completion. Reason matches the reason persisted with the terminal event.
type RunError struct {
	Reason string
	cause  error
}

// NewRunError lets other runners report terminal runs with the same contract.
func NewRunError(reason string, cause error) *RunError {
	return &RunError{Reason: reason, cause: cause}
}

func (e *RunError) Error() string { return "run " + e.Reason }
func (e *RunError) Unwrap() error { return e.cause }
