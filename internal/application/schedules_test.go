package application

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func scheduleService(t *testing.T) (*Service, *sqlite.Store, string) {
	t.Helper()
	db, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "schedules.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: &memorySecrets{values: map[secrets.Reference]string{}}, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return &fakeProvider{}, nil }})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	return s, db, workspace.ID
}

func TestSaveScheduleValidatesScopeAndCanPause(t *testing.T) {
	s, _, workspaceID := scheduleService(t)
	in := SaveScheduleInput{WorkspaceID: workspaceID, Name: "Daily review", TargetKind: "prompt", BackendID: "local", Prompt: "Review the code", Frequency: "daily", Timezone: "America/Sao_Paulo", LocalTime: "09:30", MissedPolicy: "skip", Enabled: true}
	saved, err := s.SaveSchedule(in)
	if err != nil || saved.ID == "" || saved.NextRunAt == nil || saved.NextRunAt.IsZero() {
		t.Fatalf("save: %+v %v", saved, err)
	}
	list, err := s.ListSchedules(workspaceID)
	if err != nil || len(list) != 1 || list[0].ID != saved.ID {
		t.Fatalf("list: %+v %v", list, err)
	}
	paused, err := s.SetSchedulePaused(SetSchedulePausedInput{ScheduleID: saved.ID, Paused: true, Revision: saved.Revision})
	if err != nil || paused.Enabled || paused.NextRunAt != nil {
		t.Fatalf("pause: %+v %v", paused, err)
	}
	if _, err := s.SetSchedulePaused(SetSchedulePausedInput{ScheduleID: saved.ID, Paused: false, Revision: saved.Revision}); err == nil {
		t.Fatal("stale revision should fail")
	}
	_, err = s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Bad once", TargetKind: "prompt", BackendID: "local", Prompt: "x", Frequency: "once", Timezone: "America/New_York", LocalDate: "2026-03-08", LocalTime: "02:30", MissedPolicy: "skip", Enabled: true})
	if !errors.Is(err, ErrInvalidScheduleTime) {
		t.Fatalf("nonexistent local time should explain the clock: %v", err)
	}
}

func TestScheduleRequiresExplicitConsentForCLIBackend(t *testing.T) {
	s, _, workspaceID := scheduleService(t)
	s.external["codex"] = fakeExternal{available: true}
	in := SaveScheduleInput{WorkspaceID: workspaceID, Name: "CLI review", TargetKind: "prompt", BackendID: "codex", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true}
	if _, err := s.SaveSchedule(in); err == nil {
		t.Fatal("CLI schedule should require an explicit consent")
	}
	in.AllowCLI = true
	if _, err := s.SaveSchedule(in); err != nil {
		t.Fatalf("consented CLI schedule: %v", err)
	}
}

func TestTickSchedulesCollapsesMissedRunsAndRecordsOutcome(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(-2 * time.Hour)
	for _, tc := range []struct{ id, policy string }{{"catch-up", "run_once"}, {"skip", "skip"}} {
		item := catalog.Schedule{ID: tc.id, WorkspaceID: workspaceID, Name: tc.id, TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: due.Format("15:04"), MissedPolicy: tc.policy, Enabled: true, NextRunAt: due, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := db.SaveSchedule(t.Context(), item, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.tickSchedules(t.Context(), now, now); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListScheduleJobs(workspaceID)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	statuses := map[string]string{}
	for _, job := range jobs {
		statuses[job.ScheduleID] = job.Status
	}
	if statuses["catch-up"] != "completed" || statuses["skip"] != "skipped" {
		t.Fatal(statuses)
	}
	if err := s.tickSchedules(t.Context(), now, now); err != nil {
		t.Fatal(err)
	}
	again, _ := s.ListScheduleJobs(workspaceID)
	if len(again) != 2 {
		t.Fatalf("duplicate jobs: %+v", again)
	}
}

func TestLiveScheduleSkipsALongDelayEvenWhileAppWasOpen(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(-2 * time.Hour)
	item := catalog.Schedule{ID: "live", WorkspaceID: workspaceID, Name: "Live", TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: due.Format("15:04"), MissedPolicy: "skip", Enabled: true, NextRunAt: due, Revision: 1, CreatedAt: due, UpdatedAt: due}
	if err := db.SaveSchedule(t.Context(), item, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.tickSchedules(t.Context(), now, due.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListScheduleJobs(workspaceID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "skipped" {
		t.Fatalf("late due job: %+v %v", jobs, err)
	}
}

func TestSchedulerCancelsAndJoinsBlockedJob(t *testing.T) {
	db, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "scheduler.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	provider := &fakeProvider{block: true, started: make(chan struct{})}
	vault := &memorySecrets{values: map[secrets.Reference]string{}}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspace.ID, Name: "Manual", TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := StartScheduler(s)
	job, err := s.RunScheduleNow(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled prompt did not start")
	}
	done := make(chan struct{})
	go func() { scheduler.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not join blocked prompt")
	}
	got, err := s.GetScheduleJob(job.ID)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("cancelled job: %+v %v", got, err)
	}
}

func TestManualScheduleExecutesWorkflowAndLinksDurableRun(t *testing.T) {
	s, _, workspaceID := scheduleService(t)
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspaceID, Name: "Review then document", Steps: []WorkflowStepInput{{Name: "Review", Prompt: "Review code"}, {Name: "Document", Prompt: "Write notes"}}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Daily workflow", TargetKind: "workflow", WorkflowID: definition.ID, BackendID: "local", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.RunScheduleNow(saved.ID)
	if err != nil || job.Status != "queued" {
		t.Fatalf("queue: %+v %v", job, err)
	}
	now := time.Now().UTC()
	if err := s.tickSchedules(t.Context(), now, now); err != nil {
		t.Fatal(err)
	}
	completed, err := s.GetScheduleJob(job.ID)
	if err != nil || completed.Status != "completed" || completed.WorkflowRunID == "" || completed.SessionID == "" {
		t.Fatalf("workflow job: %+v %v", completed, err)
	}
	run, err := s.GetWorkflowRun(completed.WorkflowRunID)
	if err != nil || run.CurrentStep != 2 || run.Status != "completed" {
		t.Fatalf("workflow outcome: %+v %v", run, err)
	}
}

func TestCancellingQueuedJobDoesNotRetainCancellationState(t *testing.T) {
	s, _, workspaceID := scheduleService(t)
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Manual", TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.RunScheduleNow(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := s.CancelScheduleJob(job.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancelled: %+v %v", cancelled, err)
	}
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	if len(s.scheduleCanceled) != 0 {
		t.Fatalf("orphan cancellation flags: %+v", s.scheduleCanceled)
	}
}

type refusingScheduleRunner struct{}

func (refusingScheduleRunner) Prompt(context.Context, string) error { return nil }
func (refusingScheduleRunner) Cancel() bool                         { return false }

type acceptingScheduleRunner struct{}

func (acceptingScheduleRunner) Prompt(context.Context, string) error { return nil }
func (acceptingScheduleRunner) Cancel() bool                         { return true }

func TestFailedChildCancelRemainsNonTerminal(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspaceID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Running", TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.RunScheduleNow(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.GetScheduleJob(t.Context(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.Status, job.SessionID, job.StartedAt = "running", session.ID, time.Now().UTC()
	if err := db.UpdateScheduleJob(t.Context(), job, "queued"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.sessions[job.SessionID] = refusingScheduleRunner{}
	s.mu.Unlock()
	if _, err := s.CancelScheduleJob(job.ID); err == nil {
		t.Fatal("child refusal must be reported")
	}
	loaded, err := db.GetScheduleJob(t.Context(), job.ID)
	if err != nil || loaded.Status != "cancel_requested" || loaded.ErrorCode != "cancel_failed" {
		t.Fatalf("false cancellation success: %+v %v", loaded, err)
	}
	record, err := db.GetSession(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Status, record.UpdatedAt = "completed", time.Now().UTC()
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.tickSchedules(t.Context(), now, now); err != nil {
		t.Fatal(err)
	}
	completed, err := s.GetScheduleJob(job.ID)
	if err != nil || completed.Status != "completed" || completed.ErrorCode != "cancel_failed" {
		t.Fatalf("child completed despite cancellation failure: %+v %v", completed, err)
	}
}

func TestAcceptedChildCancelWaitsForTerminalReadback(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Running", TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.RunScheduleNow(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.GetScheduleJob(t.Context(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.Status, job.SessionID, job.StartedAt = "running", "session-accepts-cancel", time.Now().UTC()
	if err := db.UpdateScheduleJob(t.Context(), job, "queued"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.sessions[job.SessionID] = acceptingScheduleRunner{}
	s.mu.Unlock()
	pending, err := s.CancelScheduleJob(job.ID)
	if err != nil || pending.Status != "cancel_requested" || pending.FinishedAt != nil {
		t.Fatalf("cancel should remain pending: %+v %v", pending, err)
	}
}

func TestWorkflowCancellationWaitsForLinkedSessionReadback(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspaceID, Name: "Workflow", Steps: []WorkflowStepInput{{Name: "Review", Prompt: "Review"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.StartWorkflow(StartWorkflowInput{WorkflowID: definition.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspaceID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := db.GetSession(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Status, record.UpdatedAt = "running", time.Now().UTC()
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	workflowRun, err := db.GetWorkflowRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	workflowRun.Status, workflowRun.LastSessionID, workflowRun.UpdatedAt = "cancelled", session.ID, time.Now().UTC()
	if err := db.UpdateWorkflowRun(t.Context(), workflowRun, "ready", "workflow.cancelled"); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Scheduled workflow", TargetKind: "workflow", WorkflowID: definition.ID, BackendID: "local", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.RunScheduleNow(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.GetScheduleJob(t.Context(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.Status, job.WorkflowRunID, job.SessionID = "cancel_requested", run.ID, session.ID
	if err := db.UpdateScheduleJob(t.Context(), job, "queued"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.tickSchedules(t.Context(), now, now); err != nil {
		t.Fatal(err)
	}
	pending, err := s.GetScheduleJob(job.ID)
	if err != nil || pending.Status != "cancel_requested" {
		t.Fatalf("session still running: %+v %v", pending, err)
	}
	record.Status, record.UpdatedAt = "cancelled", time.Now().UTC()
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := s.tickSchedules(t.Context(), now.Add(time.Second), now); err != nil {
		t.Fatal(err)
	}
	confirmed, err := s.GetScheduleJob(job.ID)
	if err != nil || confirmed.Status != "cancelled" {
		t.Fatalf("linked session ended: %+v %v", confirmed, err)
	}
}

type failingWorkflowCancelStore struct{ Store }

func (f failingWorkflowCancelStore) UpdateWorkflowRun(context.Context, catalog.WorkflowRun, string, string) error {
	return errors.New("synthetic workflow cancel failure")
}

func TestFailedWorkflowCancelRemainsNonTerminal(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspaceID, Name: "Workflow", Steps: []WorkflowStepInput{{Name: "Review", Prompt: "Review"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.StartWorkflow(StartWorkflowInput{WorkflowID: definition.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Scheduled workflow", TargetKind: "workflow", WorkflowID: definition.ID, BackendID: "local", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.RunScheduleNow(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.GetScheduleJob(t.Context(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.Status, job.WorkflowRunID, job.StartedAt = "running", run.ID, time.Now().UTC()
	if err := db.UpdateScheduleJob(t.Context(), job, "queued"); err != nil {
		t.Fatal(err)
	}
	s.store = failingWorkflowCancelStore{Store: db}
	if _, err := s.CancelScheduleJob(job.ID); err == nil {
		t.Fatal("workflow cancellation failure must be reported")
	}
	loaded, err := db.GetScheduleJob(t.Context(), job.ID)
	if err != nil || loaded.Status != "cancel_requested" || loaded.ErrorCode != "cancel_failed" {
		t.Fatalf("false workflow cancellation: %+v %v", loaded, err)
	}
}

func TestSchedulerMaintenanceIsNotExposedAsWailsServiceMethods(t *testing.T) {
	serviceType := reflect.TypeOf(&Service{})
	for _, method := range []string{"TickSchedules", "RecoverScheduleJobs"} {
		if _, exposed := serviceType.MethodByName(method); exposed {
			t.Fatalf("maintenance method %s is exposed to the renderer", method)
		}
	}
}

func TestApprovedPromptJobReconcilesToCompleted(t *testing.T) {
	s, _, workspaceID := scheduleService(t)
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "schedule-call", Name: "write", Arguments: json.RawMessage(`{"path":"approval.txt","content":"approved"}`)}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Approval", TargetKind: "prompt", BackendID: "local", Prompt: "Write approval", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.RunScheduleNow(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.tickSchedules(t.Context(), now, now); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.GetScheduleJob(job.ID)
	if err != nil || waiting.Status != "waiting_user" || waiting.SessionID == "" {
		t.Fatalf("waiting approval: %+v %v", waiting, err)
	}
	events, err := s.ListEvents(ListEventsInput{SessionID: waiting.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	var approvalID string
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
		approvalID = payload.ApprovalID
	}
	if approvalID == "" {
		t.Fatal("approval was not journaled")
	}
	if result, err := s.Approve(ApprovalInput{SessionID: waiting.SessionID, ApprovalID: approvalID, Allow: true}); err != nil || result.Status != RunCompleted {
		t.Fatalf("approve: %+v %v", result, err)
	}
	if err := s.tickSchedules(t.Context(), now.Add(time.Second), now); err != nil {
		t.Fatal(err)
	}
	completed, err := s.GetScheduleJob(job.ID)
	if err != nil || completed.Status != "completed" {
		t.Fatalf("reconciled job: %+v %v", completed, err)
	}
}

func TestApprovedWorkflowJobContinuesRemainingSteps(t *testing.T) {
	s, _, workspaceID := scheduleService(t)
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "workflow-approval", Name: "write", Arguments: json.RawMessage(`{"path":"workflow.txt","content":"approved"}`)}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspaceID, Name: "Two steps", Steps: []WorkflowStepInput{{Name: "Approve", Prompt: "Write approved file"}, {Name: "Finish", Prompt: "Summarize"}}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Workflow approval", TargetKind: "workflow", WorkflowID: definition.ID, BackendID: "local", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.RunScheduleNow(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.tickSchedules(t.Context(), now, now); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.GetScheduleJob(job.ID)
	if err != nil || waiting.Status != "waiting_user" || waiting.SessionID == "" {
		t.Fatalf("waiting workflow: %+v %v", waiting, err)
	}
	events, err := s.ListEvents(ListEventsInput{SessionID: waiting.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	var approvalID string
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
		approvalID = payload.ApprovalID
	}
	if approvalID == "" {
		t.Fatal("workflow approval missing")
	}
	if result, err := s.Approve(ApprovalInput{SessionID: waiting.SessionID, ApprovalID: approvalID, Allow: true}); err != nil || result.Status != RunCompleted {
		t.Fatalf("approve workflow: %+v %v", result, err)
	}
	if err := s.tickSchedules(t.Context(), now.Add(time.Second), now); err != nil {
		t.Fatal(err)
	}
	completed, err := s.GetScheduleJob(job.ID)
	if err != nil || completed.Status != "completed" || completed.WorkflowRunID == "" {
		t.Fatalf("completed workflow job: %+v %v", completed, err)
	}
	run, err := s.GetWorkflowRun(completed.WorkflowRunID)
	if err != nil || run.Status != "completed" || run.CurrentStep != 2 {
		t.Fatalf("remaining step not run: %+v %v", run, err)
	}
}

func TestWaitingWorkflowJobWithMissingSessionFailsSafely(t *testing.T) {
	s, db, workspaceID := scheduleService(t)
	definition, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: workspaceID, Name: "Workflow", Steps: []WorkflowStepInput{{Name: "Review", Prompt: "Review"}}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: workspaceID, Name: "Workflow schedule", TargetKind: "workflow", WorkflowID: definition.ID, BackendID: "local", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.RunScheduleNow(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.GetScheduleJob(t.Context(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.Status, job.SessionID, job.WorkflowRunID = "waiting_user", "missing-session", "missing-run"
	if err := db.UpdateScheduleJob(t.Context(), job, "queued"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.tickSchedules(t.Context(), now, now); err != nil {
		t.Fatal(err)
	}
	failed, err := s.GetScheduleJob(job.ID)
	if err != nil || failed.Status != "failed" || failed.ErrorCode != "session_not_found" {
		t.Fatalf("missing approval session: %+v %v", failed, err)
	}
}
