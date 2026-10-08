package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/automation"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

var ErrScheduleNotFound = errors.New("schedule not found")
var ErrScheduleJobNotFound = errors.New("schedule job not found")
var ErrScheduleCancelUnconfirmed = errors.New("schedule child cancellation was not confirmed")
var ErrInvalidScheduleTime = errors.New("schedule wall clock is invalid or ambiguous")
var ErrScheduleTimePassed = errors.New("schedule time has passed")

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}
func scheduleDTO(item catalog.Schedule) ScheduleDTO {
	return ScheduleDTO{ID: item.ID, WorkspaceID: item.WorkspaceID, Name: item.Name, TargetKind: item.TargetKind, WorkflowID: item.WorkflowID, BackendID: item.BackendID, Prompt: item.Prompt, Frequency: item.Frequency, Timezone: item.Timezone, LocalDate: item.LocalDate, LocalTime: item.LocalTime, MissedPolicy: item.MissedPolicy, Enabled: item.Enabled, AllowCLI: item.AllowCLI, NextRunAt: timePointer(item.NextRunAt), Revision: item.Revision, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}
func scheduleJobDTO(item catalog.ScheduleJob) ScheduleJobDTO {
	return ScheduleJobDTO{ID: item.ID, ScheduleID: item.ScheduleID, WorkspaceID: item.WorkspaceID, Trigger: item.Trigger, Status: item.Status, ErrorCode: item.ErrorCode, WorkflowRunID: item.WorkflowRunID, SessionID: item.SessionID, DueAt: item.DueAt, CreatedAt: item.CreatedAt, StartedAt: timePointer(item.StartedAt), FinishedAt: timePointer(item.FinishedAt)}
}

func (s *Service) SaveSchedule(in SaveScheduleInput) (ScheduleDTO, error) {
	if err := s.beginCall(); err != nil {
		return ScheduleDTO{}, err
	}
	defer s.endCall()
	in.Name, in.Prompt = strings.TrimSpace(in.Name), strings.TrimSpace(in.Prompt)
	if in.WorkspaceID == "" || in.Name == "" || len(in.Name) > 200 || in.BackendID == "" || (in.MissedPolicy != "skip" && in.MissedPolicy != "run_once") {
		return ScheduleDTO{}, ErrInvalidInput
	}
	if _, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID); errors.Is(err, sql.ErrNoRows) {
		return ScheduleDTO{}, ErrWorkspaceNotFound
	} else if err != nil {
		return ScheduleDTO{}, safe("get schedule workspace", err)
	}
	switch in.TargetKind {
	case "prompt":
		if in.Prompt == "" || len(in.Prompt) > maxPromptBytes || in.WorkflowID != "" {
			return ScheduleDTO{}, ErrInvalidInput
		}
	case "workflow":
		if in.Prompt != "" || in.WorkflowID == "" {
			return ScheduleDTO{}, ErrInvalidInput
		}
		workflow, err := s.store.GetWorkflow(s.ctx, in.WorkflowID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && workflow.WorkspaceID != in.WorkspaceID) {
			return ScheduleDTO{}, ErrWorkflowNotFound
		}
		if err != nil {
			return ScheduleDTO{}, safe("get schedule workflow", err)
		}
	default:
		return ScheduleDTO{}, ErrInvalidInput
	}
	available := false
	for _, backend := range s.ListBackends() {
		if backend.ID != in.BackendID {
			continue
		}
		if !backend.Available {
			return ScheduleDTO{}, ErrBackendNotFound
		}
		if backend.Kind == "cli" && !in.AllowCLI {
			return ScheduleDTO{}, ErrInvalidInput
		}
		if backend.Kind != "cli" && in.AllowCLI {
			return ScheduleDTO{}, ErrInvalidInput
		}
		available = true
		break
	}
	if !available {
		return ScheduleDTO{}, ErrBackendNotFound
	}
	now := time.Now().UTC()
	rule := automation.Rule{Frequency: in.Frequency, Timezone: in.Timezone, LocalDate: in.LocalDate, LocalTime: in.LocalTime}
	next, err := automation.NextAfter(rule, now)
	if err != nil {
		return ScheduleDTO{}, ErrInvalidScheduleTime
	}
	if in.Enabled && next.IsZero() {
		return ScheduleDTO{}, ErrScheduleTimePassed
	}
	if !in.Enabled {
		next = time.Time{}
	}
	item := catalog.Schedule{ID: in.ID, WorkspaceID: in.WorkspaceID, Name: in.Name, TargetKind: in.TargetKind, WorkflowID: in.WorkflowID, BackendID: in.BackendID, Prompt: in.Prompt, Frequency: in.Frequency, Timezone: in.Timezone, LocalDate: in.LocalDate, LocalTime: in.LocalTime, MissedPolicy: in.MissedPolicy, AllowCLI: in.AllowCLI, Enabled: in.Enabled, NextRunAt: next, Revision: 1, CreatedAt: now, UpdatedAt: now}
	expected := int64(0)
	if in.ID == "" {
		item.ID = id.New()
	} else {
		previous, err := s.store.GetSchedule(s.ctx, in.ID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && previous.WorkspaceID != in.WorkspaceID) {
			return ScheduleDTO{}, ErrScheduleNotFound
		}
		if err != nil {
			return ScheduleDTO{}, safe("get schedule", err)
		}
		if in.Revision != previous.Revision {
			return ScheduleDTO{}, sqlite.ErrScheduleConflict
		}
		item.CreatedAt, item.Revision, expected = previous.CreatedAt, previous.Revision+1, previous.Revision
	}
	if err := s.store.SaveSchedule(s.ctx, item, expected); err != nil {
		return ScheduleDTO{}, safe("save schedule", err)
	}
	s.emitScheduleChange(item.WorkspaceID)
	s.wakeScheduler()
	return scheduleDTO(item), nil
}

func (s *Service) ListSchedules(workspaceID string) ([]ScheduleDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	items, err := s.store.ListSchedules(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("list schedules", err)
	}
	result := make([]ScheduleDTO, 0, len(items))
	for _, item := range items {
		result = append(result, scheduleDTO(item))
	}
	return result, nil
}

func (s *Service) SetSchedulePaused(in SetSchedulePausedInput) (ScheduleDTO, error) {
	if err := s.beginCall(); err != nil {
		return ScheduleDTO{}, err
	}
	defer s.endCall()
	if in.ScheduleID == "" || in.Revision < 1 {
		return ScheduleDTO{}, ErrInvalidInput
	}
	item, err := s.store.GetSchedule(s.ctx, in.ScheduleID)
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduleDTO{}, ErrScheduleNotFound
	}
	if err != nil {
		return ScheduleDTO{}, safe("get schedule", err)
	}
	if item.Revision != in.Revision {
		return ScheduleDTO{}, sqlite.ErrScheduleConflict
	}
	now := time.Now().UTC()
	next := time.Time{}
	if !in.Paused {
		next, err = automation.NextAfter(automation.Rule{Frequency: item.Frequency, Timezone: item.Timezone, LocalDate: item.LocalDate, LocalTime: item.LocalTime}, now)
		if err != nil {
			return ScheduleDTO{}, ErrInvalidScheduleTime
		}
		if next.IsZero() {
			return ScheduleDTO{}, ErrScheduleTimePassed
		}
	}
	item.Enabled, item.NextRunAt, item.Revision, item.UpdatedAt = !in.Paused, next, item.Revision+1, now
	if err := s.store.SaveSchedule(s.ctx, item, in.Revision); err != nil {
		return ScheduleDTO{}, safe("pause schedule", err)
	}
	s.emitScheduleChange(item.WorkspaceID)
	s.wakeScheduler()
	return scheduleDTO(item), nil
}

func (s *Service) ListScheduleJobs(workspaceID string) ([]ScheduleJobDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	items, err := s.store.ListScheduleJobs(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("list schedule jobs", err)
	}
	result := make([]ScheduleJobDTO, 0, len(items))
	for _, item := range items {
		result = append(result, scheduleJobDTO(item))
	}
	return result, nil
}

func (s *Service) GetScheduleJob(jobID string) (ScheduleJobDTO, error) {
	if err := s.beginCall(); err != nil {
		return ScheduleJobDTO{}, err
	}
	defer s.endCall()
	if jobID == "" {
		return ScheduleJobDTO{}, ErrInvalidInput
	}
	item, err := s.store.GetScheduleJob(s.ctx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduleJobDTO{}, ErrScheduleJobNotFound
	}
	if err != nil {
		return ScheduleJobDTO{}, safe("get schedule job", err)
	}
	return scheduleJobDTO(item), nil
}

func (s *Service) RunScheduleNow(scheduleID string) (ScheduleJobDTO, error) {
	if err := s.beginCall(); err != nil {
		return ScheduleJobDTO{}, err
	}
	defer s.endCall()
	if scheduleID == "" {
		return ScheduleJobDTO{}, ErrInvalidInput
	}
	item, err := s.store.CreateManualScheduleJob(s.ctx, scheduleID, time.Now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduleJobDTO{}, ErrScheduleNotFound
	}
	if err != nil {
		return ScheduleJobDTO{}, safe("queue schedule job", err)
	}
	s.emitScheduleChange(item.WorkspaceID)
	s.wakeScheduler()
	return scheduleJobDTO(item), nil
}

func (s *Service) CancelScheduleJob(jobID string) (ScheduleJobDTO, error) {
	if err := s.beginCall(); err != nil {
		return ScheduleJobDTO{}, err
	}
	defer s.endCall()
	if jobID == "" {
		return ScheduleJobDTO{}, ErrInvalidInput
	}
	for attempt := 0; attempt < 3; attempt++ {
		job, err := s.store.GetScheduleJob(s.ctx, jobID)
		if errors.Is(err, sql.ErrNoRows) {
			return ScheduleJobDTO{}, ErrScheduleJobNotFound
		}
		if err != nil {
			return ScheduleJobDTO{}, safe("get schedule job", err)
		}
		if job.Status == "completed" || job.Status == "failed" || job.Status == "skipped" || job.Status == "cancelled" || job.Status == "interrupted" {
			s.scheduleMu.Lock()
			delete(s.scheduleCanceled, jobID)
			s.scheduleMu.Unlock()
			return scheduleJobDTO(job), nil
		}
		if job.Status == "queued" {
			job.Status, job.ErrorCode, job.FinishedAt = "cancelled", "cancelled", time.Now().UTC()
			if err := s.store.UpdateScheduleJob(s.ctx, job, "queued"); errors.Is(err, sqlite.ErrScheduleJobConflict) {
				continue
			} else if err != nil {
				return ScheduleJobDTO{}, safe("cancel queued schedule job", err)
			}
			s.emitScheduleChange(job.WorkspaceID)
			return scheduleJobDTO(job), nil
		}
		if job.Status != "cancel_requested" {
			previous := job.Status
			job.Status, job.ErrorCode = "cancel_requested", ""
			if err := s.store.UpdateScheduleJob(s.ctx, job, previous); errors.Is(err, sqlite.ErrScheduleJobConflict) {
				continue
			} else if err != nil {
				return ScheduleJobDTO{}, safe("request schedule cancellation", err)
			}
			s.emitScheduleChange(job.WorkspaceID)
		}
		if err := s.requestScheduleChildCancel(job); err != nil {
			s.recordScheduleCancelFailure(job.ID)
			return ScheduleJobDTO{}, safe("cancel schedule child", errors.Join(ErrScheduleCancelUnconfirmed, err))
		}
		s.scheduleMu.Lock()
		s.scheduleCanceled[jobID] = true
		s.scheduleMu.Unlock()
		s.wakeScheduler()
		latest, err := s.store.GetScheduleJob(s.ctx, jobID)
		if err != nil {
			return ScheduleJobDTO{}, safe("read schedule cancellation", err)
		}
		return scheduleJobDTO(latest), nil
	}
	return ScheduleJobDTO{}, sqlite.ErrScheduleJobConflict
}

func (s *Service) requestScheduleChildCancel(job catalog.ScheduleJob) error {
	if job.WorkflowRunID != "" {
		run, err := s.store.GetWorkflowRun(s.ctx, job.WorkflowRunID)
		if err != nil {
			return err
		}
		if run.Status == "completed" {
			return ErrScheduleCancelUnconfirmed
		}
		cancelled, err := s.CancelWorkflowRun(run.ID)
		if err != nil {
			return err
		}
		if cancelled.Status != "cancelled" {
			return ErrScheduleCancelUnconfirmed
		}
		return nil
	}
	if job.SessionID != "" {
		runner, err := s.runner(job.SessionID)
		if err != nil {
			return err
		}
		if _, ok := runner.(interface{ Abort(context.Context) error }); ok {
			return s.abortWorkflowSession(job.SessionID)
		}
		if runner.Cancel() {
			return nil
		}
		return ErrNoActiveRun
	}
	return nil
}

func (s *Service) recordScheduleCancelFailure(jobID string) {
	job, err := s.store.GetScheduleJob(s.ctx, jobID)
	if err != nil || job.Status != "cancel_requested" {
		return
	}
	job.ErrorCode = "cancel_failed"
	if s.store.UpdateScheduleJob(s.ctx, job, "cancel_requested") == nil {
		s.emitScheduleChange(job.WorkspaceID)
	}
}

func RecoverScheduleJobs(s *Service) error {
	return safe("recover schedule jobs", s.store.InterruptRunningScheduleJobs(s.ctx, time.Now().UTC()))
}

// tickSchedules performs one bounded batch. Queued jobs are safe to resume;
// running jobs are interrupted at startup because their side effects are unknown.
func (s *Service) tickSchedules(ctx context.Context, now, openedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	due, err := s.store.ListDueSchedules(ctx, now)
	if err != nil {
		return safe("list due schedules", err)
	}
	for _, item := range due {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, err := automation.NextAfter(automation.Rule{Frequency: item.Frequency, Timezone: item.Timezone, LocalDate: item.LocalDate, LocalTime: item.LocalTime}, now)
		if err != nil {
			return safe("calculate next schedule run", err)
		}
		status := "queued"
		if item.MissedPolicy == "skip" && (item.NextRunAt.Before(openedAt) || now.Sub(item.NextRunAt) > time.Minute) {
			status = "skipped"
		}
		job, err := s.store.ClaimDueSchedule(ctx, item.ID, item.NextRunAt, next, now, status)
		if errors.Is(err, sqlite.ErrScheduleConflict) {
			continue
		}
		if err != nil {
			return safe("claim due schedule", err)
		}
		s.emitScheduleChange(job.WorkspaceID)
	}
	queued, err := s.store.ListQueuedScheduleJobs(ctx)
	if err != nil {
		return safe("list queued schedule jobs", err)
	}
	for _, job := range queued {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.runScheduleJob(ctx, job); err != nil {
			return err
		}
	}
	s.scheduleMu.Lock()
	afterID := s.scheduleWaitingCursor
	s.scheduleMu.Unlock()
	waiting, err := s.store.ListWaitingScheduleJobs(ctx, afterID)
	if err != nil {
		return safe("list waiting schedule jobs", err)
	}
	if len(waiting) == 0 && afterID != "" {
		waiting, err = s.store.ListWaitingScheduleJobs(ctx, "")
		if err != nil {
			return safe("restart waiting job scan", err)
		}
	}
	s.scheduleMu.Lock()
	if len(waiting) > 0 {
		s.scheduleWaitingCursor = waiting[len(waiting)-1].ID
	} else {
		s.scheduleWaitingCursor = ""
	}
	s.scheduleMu.Unlock()
	for _, job := range waiting {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.reconcileWaitingScheduleJob(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileWaitingScheduleJob(ctx context.Context, job catalog.ScheduleJob) error {
	if job.Status == "cancel_requested" {
		return s.reconcileRequestedCancellation(ctx, job)
	}
	if job.SessionID == "" {
		return nil
	}
	record, err := s.store.GetSession(ctx, job.SessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return safe("get schedule job session", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		job.Status, job.ErrorCode, job.FinishedAt = "failed", "session_not_found", time.Now().UTC()
		if err := s.store.UpdateScheduleJob(ctx, job, "waiting_user"); errors.Is(err, sqlite.ErrScheduleJobConflict) {
			return nil
		} else if err != nil {
			return safe("fail schedule job without session", err)
		}
		s.emitScheduleChange(job.WorkspaceID)
		return nil
	}
	if job.TargetKind == "workflow" {
		if err == nil && record.Status != "completed" && record.Status != "failed" && record.Status != "paused" && record.Status != "cancelled" {
			return nil
		}
		return s.reconcileWaitingWorkflowJob(ctx, job)
	}
	status, code := "", ""
	switch {
	case record.Status == "completed":
		status = "completed"
	case record.Status == "failed" || record.Status == "paused":
		status, code = "failed", "execution_failed"
	case record.Status == "cancelled":
		status, code = "cancelled", "cancelled"
	default:
		return nil
	}
	job.Status, job.ErrorCode, job.FinishedAt = status, code, time.Now().UTC()
	if err := s.store.UpdateScheduleJob(ctx, job, "waiting_user"); errors.Is(err, sqlite.ErrScheduleJobConflict) {
		return nil
	} else if err != nil {
		return safe("reconcile schedule job", err)
	}
	s.emitScheduleChange(job.WorkspaceID)
	return nil
}

func (s *Service) reconcileRequestedCancellation(ctx context.Context, job catalog.ScheduleJob) error {
	s.scheduleMu.Lock()
	active := s.scheduleRunning == job.ID
	s.scheduleMu.Unlock()
	if active {
		return nil
	}
	status, code := "", ""
	if job.WorkflowRunID != "" {
		run, err := s.store.GetWorkflowRun(ctx, job.WorkflowRunID)
		if errors.Is(err, sql.ErrNoRows) {
			status, code = "interrupted", "workflow_run_not_found"
		} else if err != nil {
			return safe("read cancelled workflow", err)
		} else {
			switch run.Status {
			case "cancelled":
				if job.SessionID != "" {
					record, err := s.store.GetSession(ctx, job.SessionID)
					if errors.Is(err, sql.ErrNoRows) {
						status, code = "interrupted", "session_not_found"
					} else if err != nil {
						return safe("read cancelled workflow session", err)
					} else if record.Status != "completed" && record.Status != "failed" && record.Status != "paused" && record.Status != "cancelled" {
						return nil
					}
				}
				if status == "" {
					status, code = "cancelled", "cancelled"
				}
			case "completed":
				status = "completed"
			case "paused":
				status, code = "failed", "workflow_paused"
			}
		}
	} else if job.SessionID != "" {
		record, err := s.store.GetSession(ctx, job.SessionID)
		if errors.Is(err, sql.ErrNoRows) {
			status, code = "interrupted", "session_not_found"
		} else if err != nil {
			return safe("read cancelled session", err)
		} else {
			switch record.Status {
			case "cancelled":
				status, code = "cancelled", "cancelled"
			case "completed":
				status = "completed"
			case "failed", "paused":
				status, code = "failed", "execution_failed"
			}
		}
	} else {
		status, code = "interrupted", "cancel_unconfirmed"
	}
	if status == "" {
		return nil
	}
	job.Status, job.FinishedAt = status, time.Now().UTC()
	if status == "completed" && job.ErrorCode == "cancel_failed" {
		code = "cancel_failed"
	}
	job.ErrorCode = code
	if err := s.store.UpdateScheduleJob(ctx, job, "cancel_requested"); errors.Is(err, sqlite.ErrScheduleJobConflict) {
		return nil
	} else if err != nil {
		return safe("reconcile requested cancellation", err)
	}
	s.scheduleMu.Lock()
	delete(s.scheduleCanceled, job.ID)
	s.scheduleMu.Unlock()
	s.emitScheduleChange(job.WorkspaceID)
	return nil
}

func (s *Service) reconcileWaitingWorkflowJob(ctx context.Context, job catalog.ScheduleJob) error {
	if job.WorkflowRunID == "" {
		return nil
	}
	run, err := s.GetWorkflowRun(job.WorkflowRunID)
	if err != nil {
		return safe("get waiting workflow run", err)
	}
	if run.Status == "waiting_user" || run.Status == "running" {
		run, err = s.RunWorkflowStep(run.ID)
		if err != nil {
			return safe("reconcile waiting workflow step", err)
		}
	}
	if run.Status == "waiting_user" || run.Status == "running" {
		return nil
	}
	if run.Status == "ready" {
		job.Status = "running"
		if err := s.store.UpdateScheduleJob(ctx, job, "waiting_user"); errors.Is(err, sqlite.ErrScheduleJobConflict) {
			return nil
		} else if err != nil {
			return safe("resume schedule job", err)
		}
		s.scheduleMu.Lock()
		s.scheduleRunning = job.ID
		s.scheduleMu.Unlock()
		defer func() {
			s.scheduleMu.Lock()
			if s.scheduleRunning == job.ID {
				s.scheduleRunning = ""
			}
			delete(s.scheduleCanceled, job.ID)
			s.scheduleMu.Unlock()
		}()
		s.emitScheduleChange(job.WorkspaceID)
		status, code := s.runWorkflowJobSteps(ctx, &job, run)
		job.Status, job.ErrorCode, job.FinishedAt = status, code, time.Now().UTC()
		return s.finishExecutedScheduleJob(ctx, job)
	}
	status, code := "failed", "workflow_paused"
	if run.Status == "completed" {
		status, code = "completed", ""
	}
	if run.Status == "cancelled" {
		status, code = "cancelled", "cancelled"
	}
	job.Status, job.ErrorCode, job.FinishedAt, job.SessionID = status, code, time.Now().UTC(), run.LastSessionID
	if err := s.store.UpdateScheduleJob(ctx, job, "waiting_user"); errors.Is(err, sqlite.ErrScheduleJobConflict) {
		return nil
	} else if err != nil {
		return safe("finish waiting schedule job", err)
	}
	s.emitScheduleChange(job.WorkspaceID)
	return nil
}

func (s *Service) runScheduleJob(ctx context.Context, job catalog.ScheduleJob) error {
	job.StartedAt, job.Status = time.Now().UTC(), "running"
	if err := s.store.UpdateScheduleJob(ctx, job, "queued"); errors.Is(err, sqlite.ErrScheduleJobConflict) {
		return nil
	} else if err != nil {
		return safe("start schedule job", err)
	}
	s.scheduleMu.Lock()
	s.scheduleRunning = job.ID
	s.scheduleMu.Unlock()
	defer func() {
		s.scheduleMu.Lock()
		if s.scheduleRunning == job.ID {
			s.scheduleRunning = ""
		}
		delete(s.scheduleCanceled, job.ID)
		s.scheduleMu.Unlock()
	}()
	s.emitScheduleChange(job.WorkspaceID)
	status, code := s.executeScheduleJob(ctx, &job)
	job.Status, job.ErrorCode, job.FinishedAt = status, code, time.Now().UTC()
	return s.finishExecutedScheduleJob(ctx, job)
}

func (s *Service) finishExecutedScheduleJob(ctx context.Context, job catalog.ScheduleJob) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		current, err := s.store.GetScheduleJob(writeCtx, job.ID)
		if err != nil {
			return safe("read schedule job outcome", err)
		}
		if current.Status != "running" && current.Status != "cancel_requested" {
			return nil
		}
		if current.ErrorCode == "cancel_failed" && job.Status == "completed" {
			job.ErrorCode = "cancel_failed"
		}
		if err := s.store.UpdateScheduleJob(writeCtx, job, current.Status); errors.Is(err, sqlite.ErrScheduleJobConflict) {
			continue
		} else if err != nil {
			return safe("finish schedule job", err)
		}
		s.emitScheduleChange(job.WorkspaceID)
		return nil
	}
	return sqlite.ErrScheduleJobConflict
}

func (s *Service) executeScheduleJob(ctx context.Context, job *catalog.ScheduleJob) (string, string) {
	if err := ctx.Err(); err != nil || s.scheduleJobCancelled(job.ID) {
		return "cancelled", "cancelled"
	}
	if job.TargetKind == "workflow" {
		run, err := s.StartWorkflow(StartWorkflowInput{WorkflowID: job.WorkflowID, BackendID: job.BackendID})
		if err != nil {
			return "failed", ErrorCode(err)
		}
		job.WorkflowRunID = run.ID
		if err := s.persistScheduleJobLink(ctx, *job); err != nil {
			return "failed", ErrorCode(err)
		}
		return s.runWorkflowJobSteps(ctx, job, run)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: job.WorkspaceID, BackendID: job.BackendID})
	if err != nil {
		return "failed", ErrorCode(err)
	}
	job.SessionID = session.ID
	if err := s.persistScheduleJobLink(ctx, *job); err != nil {
		return "failed", ErrorCode(err)
	}
	if err := ctx.Err(); err != nil || s.scheduleJobCancelled(job.ID) {
		return "cancelled", "cancelled"
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: job.Prompt})
	if err != nil {
		return "failed", ErrorCode(err)
	}
	switch result.Status {
	case RunCompleted:
		return "completed", ""
	case RunAwaitingApproval:
		return "waiting_user", ""
	case RunCancelled:
		return "cancelled", "cancelled"
	default:
		if result.Code != "" {
			return "failed", result.Code
		}
		return "failed", "execution_failed"
	}
}

func (s *Service) runWorkflowJobSteps(ctx context.Context, job *catalog.ScheduleJob, run WorkflowRunDTO) (string, string) {
	for run.Status == "ready" {
		if err := ctx.Err(); err != nil || s.scheduleJobCancelled(job.ID) {
			return "cancelled", "cancelled"
		}
		var err error
		run, err = s.RunWorkflowStep(run.ID)
		job.SessionID = run.LastSessionID
		if err != nil {
			return "failed", ErrorCode(err)
		}
		if err := s.persistScheduleJobLink(ctx, *job); err != nil {
			return "failed", ErrorCode(err)
		}
	}
	if run.Status == "completed" {
		return "completed", ""
	}
	if s.scheduleJobCancelled(job.ID) {
		return "cancelled", "cancelled"
	}
	switch run.Status {
	case "waiting_user":
		return "waiting_user", ""
	case "cancelled":
		return "cancelled", "cancelled"
	default:
		return "failed", "workflow_paused"
	}
}

func (s *Service) persistScheduleJobLink(ctx context.Context, job catalog.ScheduleJob) error {
	for attempt := 0; attempt < 3; attempt++ {
		current, err := s.store.GetScheduleJob(ctx, job.ID)
		if err != nil {
			return err
		}
		if current.Status != "running" && current.Status != "cancel_requested" {
			return sqlite.ErrScheduleJobConflict
		}
		job.Status, job.ErrorCode, job.FinishedAt = current.Status, current.ErrorCode, current.FinishedAt
		if err := s.store.UpdateScheduleJob(ctx, job, current.Status); errors.Is(err, sqlite.ErrScheduleJobConflict) {
			continue
		} else {
			return err
		}
	}
	return sqlite.ErrScheduleJobConflict
}

func (s *Service) scheduleJobCancelled(jobID string) bool {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	return s.scheduleCanceled[jobID]
}

func (s *Service) emitScheduleChange(workspaceID string) {
	s.mu.RLock()
	emit := s.emit
	s.mu.RUnlock()
	if emit != nil {
		emit("harflex:schedule", ScheduleChangeDTO{WorkspaceID: workspaceID})
	}
}
func (s *Service) wakeScheduler() {
	select {
	case s.scheduleWake <- struct{}{}:
	default:
	}
}
