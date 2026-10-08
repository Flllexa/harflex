package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func pendingApprovalID(t *testing.T, s *Service, sessionID string) string {
	t.Helper()
	events, err := s.ListEvents(ListEventsInput{SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != "approval.requested" {
			continue
		}
		var payload struct {
			ApprovalID string `json:"approvalId"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		return payload.ApprovalID
	}
	t.Fatal("approval was not requested")
	return ""
}

func TestWorkspaceDowngradeRequiresApprovalInExistingRunner(t *testing.T) {
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "downgrade-write", Name: "write", Arguments: json.RawMessage(`{"path":"downgrade.txt","content":"effect"}`)}}
	s, db, _, original := sessionSetup(t, provider)
	workspace, err := db.GetWorkspace(t.Context(), original.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "trusted_workspace"}); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "ask"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Write the file"})
	if err != nil || result.Status != RunAwaitingApproval || result.Approval == nil {
		t.Fatalf("downgraded runner executed without approval: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "downgrade.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("write occurred before approval: %v", err)
	}
	approved, err := s.Approve(ApprovalInput{SessionID: session.ID, ApprovalID: result.Approval.ID, Allow: true})
	if err != nil || approved.Status != RunCompleted {
		t.Fatalf("approved write: %+v %v", approved, err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "downgrade.txt")); err != nil {
		t.Fatalf("approved write absent: %v", err)
	}
	continued, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Continue"})
	if err != nil || continued.Status != RunCompleted {
		t.Fatalf("approval corrupted later history: %+v %v", continued, err)
	}
}

func TestCancelWaitingWorkflowRejectsApprovalBeforeTerminal(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "workflow-write", Name: "write", Arguments: json.RawMessage(`{"path":"cancelled.txt","content":"effect"}`)}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspaceID, Name: "Write", Steps: []WorkflowStepInput{{Name: "Write", Prompt: "Write the file"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.StartWorkflow(StartWorkflowInput{WorkflowID: definition.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := s.RunWorkflowStep(run.ID)
	if err != nil || waiting.Status != "waiting_user" {
		t.Fatalf("waiting: %+v %v", waiting, err)
	}
	approvalID := pendingApprovalID(t, s, waiting.LastSessionID)
	cancelled, err := s.CancelWorkflowRun(run.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	record, err := db.GetSession(t.Context(), waiting.LastSessionID)
	if err != nil || (record.Status != "cancelled" && record.Status != "failed") {
		t.Fatalf("approval session still live: %+v %v", record, err)
	}
	if _, err := s.Approve(ApprovalInput{SessionID: waiting.LastSessionID, ApprovalID: approvalID, Allow: true}); !errors.Is(err, agentcore.ErrApprovalNotFound) {
		t.Fatalf("cancelled workflow approval remained executable: %v", err)
	}
	workspace, err := db.GetWorkspace(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "cancelled.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled write occurred: %v", err)
	}
	reloaded := NewService(t.Context(), Dependencies{Store: db, Secrets: s.secrets, External: map[string]ExternalBackend{}, ProviderFactory: s.providerFactory})
	if _, err := reloaded.OpenSession(OpenSessionInput{SessionID: waiting.LastSessionID, WorkspaceID: workspaceID}); err != nil {
		t.Fatalf("cancelled approval corrupted replay: %v", err)
	}
}

func TestRecancelLegacyCancelledWorkflowRevokesOrphanedApproval(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "orphaned-write", Name: "write", Arguments: json.RawMessage(`{"path":"orphaned.txt","content":"effect"}`)}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspaceID, Name: "Legacy", Steps: []WorkflowStepInput{{Name: "Write", Prompt: "Write the file"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.StartWorkflow(StartWorkflowInput{WorkflowID: definition.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := s.RunWorkflowStep(run.ID)
	if err != nil || waiting.Status != "waiting_user" {
		t.Fatalf("waiting: %+v %v", waiting, err)
	}
	approvalID := pendingApprovalID(t, s, waiting.LastSessionID)
	stale, err := db.GetWorkflowRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale.Status, stale.UpdatedAt = "cancelled", time.Now().UTC()
	if err := db.UpdateWorkflowRun(t.Context(), stale, "waiting_user", "workflow.cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelWorkflowRun(run.ID); err != nil {
		t.Fatalf("recancel: %v", err)
	}
	if _, err := s.Approve(ApprovalInput{SessionID: waiting.LastSessionID, ApprovalID: approvalID, Allow: true}); !errors.Is(err, agentcore.ErrApprovalNotFound) {
		t.Fatalf("orphaned approval remained live: %v", err)
	}
}

func TestCancelWaitingScheduleJobReachesTerminalAndRejectsApproval(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "scheduled-write", Name: "write", Arguments: json.RawMessage(`{"path":"scheduled.txt","content":"effect"}`)}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	schedule, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Approval", TargetKind: "prompt", BackendID: "local", Prompt: "Write the file", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.RunScheduleNow(schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.tickSchedules(t.Context(), now, now); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.GetScheduleJob(job.ID)
	if err != nil || waiting.Status != "waiting_user" {
		t.Fatalf("waiting: %+v %v", waiting, err)
	}
	approvalID := pendingApprovalID(t, s, waiting.SessionID)
	if _, err := s.CancelScheduleJob(job.ID); err != nil {
		t.Fatalf("cancel waiting job: %v", err)
	}
	if err := s.tickSchedules(t.Context(), now.Add(time.Second), now); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.GetScheduleJob(job.ID)
	if err != nil || terminal.Status != "cancelled" {
		t.Fatalf("job did not finish cancellation: %+v %v", terminal, err)
	}
	if _, err := s.Approve(ApprovalInput{SessionID: waiting.SessionID, ApprovalID: approvalID, Allow: true}); !errors.Is(err, agentcore.ErrApprovalNotFound) {
		t.Fatalf("scheduled approval remained executable: %v", err)
	}
	workspace, err := db.GetWorkspace(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "scheduled.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-cancel schedule write occurred: %v", err)
	}
}

func TestCancelWaitingWorkflowScheduleReachesTerminal(t *testing.T) {
	s, _, workspaceID := scheduleService(t)
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "scheduled-workflow-write", Name: "write", Arguments: json.RawMessage(`{"path":"scheduled-workflow.txt","content":"effect"}`)}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspaceID, Name: "Approval", Steps: []WorkflowStepInput{{Name: "Write", Prompt: "Write the file"}}})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Workflow approval", TargetKind: "workflow", WorkflowID: definition.ID, BackendID: "local", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.RunScheduleNow(schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.tickSchedules(t.Context(), now, now); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.GetScheduleJob(job.ID)
	if err != nil || waiting.Status != "waiting_user" || waiting.WorkflowRunID == "" {
		t.Fatalf("waiting workflow job: %+v %v", waiting, err)
	}
	approvalID := pendingApprovalID(t, s, waiting.SessionID)
	if _, err := s.CancelScheduleJob(job.ID); err != nil {
		t.Fatalf("cancel waiting workflow job: %v", err)
	}
	if err := s.tickSchedules(t.Context(), now.Add(time.Second), now); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.GetScheduleJob(job.ID)
	if err != nil || terminal.Status != "cancelled" {
		t.Fatalf("workflow job cancellation stalled: %+v %v", terminal, err)
	}
	if _, err := s.Approve(ApprovalInput{SessionID: waiting.SessionID, ApprovalID: approvalID, Allow: true}); !errors.Is(err, agentcore.ErrApprovalNotFound) {
		t.Fatalf("cancelled workflow job approval remained live: %v", err)
	}
}

type pausedWorkflowTransitionStore struct {
	Store
	started chan struct{}
	release chan struct{}
}

func (p *pausedWorkflowTransitionStore) UpdateWorkflowRun(ctx context.Context, item catalog.WorkflowRun, expectedStatus, eventType string) error {
	err := p.Store.UpdateWorkflowRun(ctx, item, expectedStatus, eventType)
	if err == nil && expectedStatus == "ready" && item.Status == "running" {
		close(p.started)
		<-p.release
	}
	return err
}

func TestCancelBetweenWorkflowCASAndPromptPreventsToolEffect(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "late-write", Name: "write", Arguments: json.RawMessage(`{"path":"late.txt","content":"effect"}`)}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspaceID, Profile: "trusted_workspace"}); err != nil {
		t.Fatal(err)
	}
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspaceID, Name: "Write", Steps: []WorkflowStepInput{{Name: "Write", Prompt: "Write the file"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.StartWorkflow(StartWorkflowInput{WorkflowID: definition.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	gate := &pausedWorkflowTransitionStore{Store: s.store, started: make(chan struct{}), release: make(chan struct{})}
	s.store = gate
	done := make(chan struct{})
	var step WorkflowRunDTO
	var stepErr error
	go func() { defer close(done); step, stepErr = s.RunWorkflowStep(run.ID) }()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("workflow did not reach CAS")
	}
	cancelled, err := s.CancelWorkflowRun(run.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	close(gate.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("workflow step did not stop")
	}
	if stepErr != nil || step.Status != "cancelled" {
		t.Fatalf("step did not observe cancellation: %+v %v", step, stepErr)
	}
	workspace, err := db.GetWorkspace(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "late.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-cancel write occurred: %v", err)
	}
	reloaded := NewService(t.Context(), Dependencies{Store: db, Secrets: s.secrets, External: map[string]ExternalBackend{}, ProviderFactory: s.providerFactory})
	if _, err := reloaded.OpenSession(OpenSessionInput{SessionID: step.LastSessionID, WorkspaceID: workspaceID}); err != nil {
		t.Fatalf("pre-prompt cancellation corrupted replay: %v", err)
	}
}

func pausedWorkflow(t *testing.T) (*Service, catalog.WorkflowRun) {
	t.Helper()
	s, db, workspaceID := scheduleService(t)
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "unknown-call", Name: "unknown", Arguments: json.RawMessage(`{}`)}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspaceID, Name: "Recover", Steps: []WorkflowStepInput{{Name: "Risky step", Prompt: "Use a tool"}}})
	if err != nil {
		t.Fatal(err)
	}
	started, err := s.StartWorkflow(StartWorkflowInput{WorkflowID: definition.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	paused, _ := s.RunWorkflowStep(started.ID)
	if paused.Status != "paused" || paused.LastSessionID == "" {
		t.Fatalf("expected a failed step: %+v", paused)
	}
	run, err := db.GetWorkflowRun(t.Context(), paused.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, run
}

func TestPausedWorkflowRequiresReviewedSessionAndExplicitRetry(t *testing.T) {
	s, paused := pausedWorkflow(t)
	if _, err := s.ResumeWorkflowRun(ResumeWorkflowRunInput{RunID: paused.ID, ReviewedSessionID: "another-session", Choice: "retry"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("stale review accepted: %v", err)
	}
	ready, err := s.ResumeWorkflowRun(ResumeWorkflowRunInput{RunID: paused.ID, ReviewedSessionID: paused.LastSessionID, Choice: "retry"})
	if err != nil || ready.Status != "ready" || ready.CurrentStep != 0 || ready.LastSessionID != paused.LastSessionID {
		t.Fatalf("retry choice: %+v %v", ready, err)
	}
	if _, err := s.ResumeWorkflowRun(ResumeWorkflowRunInput{RunID: paused.ID, ReviewedSessionID: paused.LastSessionID, Choice: "retry"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("repeated recovery accepted: %v", err)
	}
	completed, err := s.RunWorkflowStep(paused.ID)
	if err != nil || completed.Status != "completed" || completed.CurrentStep != 1 || completed.LastSessionID == paused.LastSessionID {
		t.Fatalf("retry did not create a fresh step: %+v %v", completed, err)
	}
}

func TestPausedWorkflowCanSkipAfterReviewWithoutExecuting(t *testing.T) {
	s, paused := pausedWorkflow(t)
	completed, err := s.ResumeWorkflowRun(ResumeWorkflowRunInput{RunID: paused.ID, ReviewedSessionID: paused.LastSessionID, Choice: "skip"})
	if err != nil || completed.Status != "completed" || completed.CurrentStep != 1 || completed.LastSessionID != paused.LastSessionID {
		t.Fatalf("skip choice: %+v %v", completed, err)
	}
	events, err := s.store.ListAfter(t.Context(), paused.ID, 0)
	if err != nil || events[len(events)-1].Type != "workflow.step.skipped" {
		t.Fatalf("skip was not audited: %+v %v", events, err)
	}
}
