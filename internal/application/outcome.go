package application

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/repositories"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
	"github.com/persioflexa/harflex/internal/terminal"
)

// Run statuses returned by Prompt and Approve. A run that reached a terminal
// state is a successful call; errors are reserved for calls the backend refused.
const (
	RunCompleted        = "completed"
	RunAwaitingApproval = "awaiting_approval"
	RunCancelled        = "cancelled"
	RunFailed           = "failed"
)

const maxPromptBytes = 1024 * 1024

func runResult(operation string, err error) (RunResultDTO, error) {
	var required *agentcore.ApprovalRequiredError
	var ended *agentcore.RunError
	switch {
	case err == nil:
		return RunResultDTO{Status: RunCompleted}, nil
	case errors.As(err, &required):
		a := required.Approval
		return RunResultDTO{Status: RunAwaitingApproval, Approval: &ApprovalDTO{ID: a.ID, ToolCallID: a.ToolCallID, Name: a.Name, Risk: a.Risk, Arguments: append(json.RawMessage(nil), a.Arguments...)}}, nil
	case errors.As(err, &ended) && ended.Reason == RunCancelled:
		return RunResultDTO{Status: RunCancelled}, nil
	case errors.As(err, &ended):
		result := RunResultDTO{Status: RunFailed, Reason: ended.Reason}
		if code := ErrorCode(ended); code != codeInternal {
			result.Code = code
		}
		return result, nil
	}
	return RunResultDTO{}, safe(operation, err)
}

const codeInternal = "internal"

var errorCodes = []struct {
	err  error
	code string
}{
	{ErrInvalidInput, "invalid_input"},
	{ErrWorkspaceNotFound, "workspace_not_found"},
	{ErrBackendNotFound, "backend_not_found"},
	{ErrProviderEndpointBlocked, "provider_endpoint_blocked"},
	{ErrBackendChanged, "backend_changed"},
	{ErrSessionNotFound, "session_not_found"},
	{ErrSessionCorrupt, "session_corrupt"},
	{ErrHistoryTooLarge, "history_too_large"},
	{events.ErrStreamBudgetExceeded, "history_too_large"},
	{externalagent.ErrNotResumable, "session_not_resumable"},
	{ErrNoActiveRun, "no_active_run"},
	{ErrApprovalUnsupported, "approval_unsupported"},
	{ErrAuditUnavailable, "audit_unavailable"},
	{repositories.ErrGitUnavailable, "git_unavailable"},
	{repositories.ErrNotRepository, "not_repository"},
	{repositories.ErrWorktreeNotFound, "worktree_not_found"},
	{repositories.ErrWorktreeNotDeletable, "worktree_not_deletable"},
	{repositories.ErrWorktreeSaveBlocked, "worktree_save_blocked"},
	{repositories.ErrWorktreeRemoveFailed, "worktree_remove_failed"},
	{ErrPipelineNotFound, "pipeline_not_found"},
	{ErrBrainstormNotFound, "brainstorm_not_found"},
	{ErrPipelineRequestConflict, "pipeline_request_conflict"},
	{sqlite.ErrPipelineConflict, "pipeline_conflict"},
	{sqlite.ErrHistoricalReceiptUnavailable, "historical_receipt_unavailable"},
	{sdd.ErrEvidenceRequired, "evidence_required"},
	{sdd.ErrInvalidTransition, "invalid_transition"},
	{sdd.ErrBrainstormBudgetExceeded, "brainstorm_budget_exceeded"},
	{sdd.ErrAuthoringBudgetExceeded, "authoring_budget_exceeded"},
	{sdd.ErrAuthoringCancellationPending, "authoring_cancellation_pending"},
	{errSDDPromptTooLarge, "brainstorm_prompt_too_large"},
	{ErrUntrackedEvidenceRequiresStage, "untracked_evidence_requires_stage"},
	{ErrPipelineGitRequired, "pipeline_git_required"},
	{ErrPipelineGitDirty, "pipeline_git_dirty"},
	{ErrPipelineGitBaselineUnavailable, "pipeline_git_baseline_unavailable"},
	{ErrPipelineCodeSnapshotUnavailable, "pipeline_code_snapshot_unavailable"},
	{ErrPipelineCodeSessionClosed, "pipeline_code_session_closed"},
	{ErrPipelineCodeNotApplied, "pipeline_code_not_applied"},
	{ErrPullRequestWatchNeedsFullAccess, "pull_request_watch_needs_full_access"},
	{ErrPullRequestNotFound, "pull_request_not_found"},
	{ErrPipelineQABusy, "pipeline_qa_busy"},
	{ErrPipelinePRsBackendUnsupported, "pipeline_prs_backend_unsupported"},
	{ErrStageExecutorUnsupported, "stage_executor_unsupported"},
	{ErrPipelineDesignPhaseExecutor, "pipeline_design_phase_executor_unusable"},
	{ErrPipelineEvaluationCriteriaChanged, "pipeline_evaluation_criteria_changed"},
	{ErrSDDCLIReadIsolationUnavailable, "sdd_cli_read_isolation_unavailable"},
	{ErrPipelineDesignModelRequired, "pipeline_design_model_required"},
	{ErrPipelineDesignSpecStale, "pipeline_design_spec_stale"},
	{sqlite.ErrPipelineDesignSpecStale, "pipeline_design_spec_stale"},
	{ErrPipelineDesignExecutorUnavailable, "pipeline_design_executor_unavailable"},
	{sqlite.ErrPipelineDesignDerivationRequired, "pipeline_design_derivation_required"},
	{externalagent.ErrCodexDocumentContractUnavailable, "pipeline_design_executor_unavailable"},
	{externalagent.ErrCodexDocumentProtocol, "pipeline_design_invalid_response"},
	{externalagent.ErrClaudeDocumentContractUnavailable, "pipeline_design_executor_unavailable"},
	{externalagent.ErrClaudeDocumentProtocol, "pipeline_design_invalid_response"},
	{externalagent.ErrClaudeNotLoggedIn, "claude_not_logged_in"},
	{ErrTerminalNotFound, "terminal_not_found"},
	{terminal.ErrUnsupported, "terminal_unsupported"},
	{externalagent.ErrDocumentProviderInput, "pipeline_design_invalid_request"},
	{externalagent.ErrDocumentProviderOutput, "pipeline_design_invalid_response"},
	{repositories.ErrExecutionCopyConfirmationRequired, "execution_copy_confirmation_required"},
	{repositories.ErrExecutionSnapshotUnsafePath, "pipeline_execution_unsafe_path"},
	{repositories.ErrExecutionSnapshotTooLarge, "pipeline_execution_snapshot_too_large"},
	{repositories.ErrExecutionEvidenceTooLarge, "pipeline_execution_evidence_too_large"},
	{repositories.ErrExecutionSourceDrift, "pipeline_source_drift"},
	{repositories.ErrExecutionApplyConflict, "pipeline_code_apply_conflict"},
	{repositories.ErrExecutionNoChanges, "evidence_required"},
	{ErrAgentNotFound, "agent_not_found"},
	{ErrDelegationLimit, "delegation_limit"},
	{ErrDelegationBudgetExceeded, "delegation_budget_exceeded"},
	{ErrDelegationRequestConflict, "delegation_request_conflict"},
	{ErrWorkflowNotFound, "workflow_not_found"},
	{ErrWorkflowRunNotFound, "workflow_run_not_found"},
	{sqlite.ErrWorkflowConflict, "workflow_conflict"},
	{ErrScheduleNotFound, "schedule_not_found"},
	{ErrScheduleJobNotFound, "schedule_job_not_found"},
	{ErrScheduleCancelUnconfirmed, "schedule_cancel_unconfirmed"},
	{ErrWorkflowCancelUnconfirmed, "workflow_cancel_unconfirmed"},
	{ErrInvalidScheduleTime, "schedule_time_invalid"},
	{ErrScheduleTimePassed, "schedule_time_passed"},
	{sqlite.ErrScheduleConflict, "schedule_conflict"},
	{sqlite.ErrScheduleJobConflict, "schedule_job_conflict"},
	{ErrSkillNotFound, "skill_not_found"},
	{ErrSkillImportFailed, "skill_import_failed"},
	{ErrKnowledgeNotFound, "knowledge_not_found"},
	{ErrKnowledgeSourceUnavailable, "knowledge_source_unavailable"},
	{ErrKnowledgeSourceChanged, "knowledge_source_changed"},
	{ErrEmbeddingUnavailable, "local_embedding_unavailable"},
	{ErrEmbeddingProfileChanged, "local_embedding_profile_changed"},
	{ErrChannelNotFound, "channel_not_found"},
	{ErrChannelFolderUnavailable, "channel_folder_unavailable"},
	{ErrChannelInboxLimit, "channel_inbox_limit"},
	{ErrChannelSourceUnavailable, "channel_source_unavailable"},
	{ErrChannelSourceChanged, "channel_source_changed"},
	{ErrChannelMessageInvalid, "channel_message_invalid"},
	{ErrChannelOutboxConflict, "channel_outbox_conflict"},
	{ErrChannelWriteFailed, "channel_write_failed"},
	{ErrChannelRequestConflict, "channel_request_conflict"},
	{ErrChannelConfigLocked, "channel_config_locked"},
	{ErrMCPServerNotFound, "mcp_server_not_found"},
	{ErrMCPConnectionFailed, "mcp_connection_failed"},
	{agentcore.ErrSessionBusy, "session_busy"},
	{externalagent.ErrSessionBusy, "session_busy"},
	{agentcore.ErrApprovalNotFound, "approval_not_found"},
	{context.Canceled, "cancelled"},
	{context.DeadlineExceeded, "cancelled"},
}

// ErrorCode classifies an error into a stable code the frontend can branch on.
func ErrorCode(err error) string {
	for _, known := range errorCodes {
		if errors.Is(err, known.err) {
			return known.code
		}
	}
	return codeInternal
}

// MarshalError controls only the Wails error cause payload. Wails separately
// serializes the outer error message; callers must still sanitize that message
// with safe before returning adapter diagnostics across the bridge.
func MarshalError(err error) []byte {
	payload := map[string]string{"code": ErrorCode(err)}
	// A document phase that could not start on the executor chosen for it says which phase it was.
	var phase phaseExecutorError
	if errors.As(err, &phase) {
		payload["stage"] = string(phase.stage)
	}
	data, _ := json.Marshal(payload)
	return data
}
