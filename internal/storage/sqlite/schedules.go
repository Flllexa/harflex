package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
)

var ErrScheduleConflict = errors.New("schedule changed concurrently")
var ErrScheduleJobConflict = errors.New("schedule job changed concurrently")

func optionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatCatalogTime(value)
}
func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

const scheduleColumns = "id,workspace_id,name,target_kind,workflow_id,backend_id,prompt,frequency,timezone,local_date,local_time,missed_policy,allow_cli,enabled,next_run_at,revision,created_at,updated_at"

func scanSchedule(row rowScanner) (catalog.Schedule, error) {
	var item catalog.Schedule
	var enabled, allowCLI int
	var next, created, updated string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.TargetKind, &item.WorkflowID, &item.BackendID, &item.Prompt, &item.Frequency, &item.Timezone, &item.LocalDate, &item.LocalTime, &item.MissedPolicy, &allowCLI, &enabled, &next, &item.Revision, &created, &updated); err != nil {
		return item, err
	}
	item.AllowCLI = allowCLI == 1
	item.Enabled = enabled == 1
	var err error
	if item.NextRunAt, err = parseOptionalTime(next); err != nil {
		return item, fmt.Errorf("parse schedule next run: %w", err)
	}
	if item.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return item, fmt.Errorf("parse schedule creation: %w", err)
	}
	if item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return item, fmt.Errorf("parse schedule update: %w", err)
	}
	return item, nil
}

func (s *Store) SaveSchedule(ctx context.Context, item catalog.Schedule, expectedRevision int64) error {
	var result sql.Result
	var err error
	if expectedRevision == 0 {
		result, err = s.db.ExecContext(ctx, `INSERT INTO schedules(id,workspace_id,name,target_kind,workflow_id,backend_id,prompt,frequency,timezone,local_date,local_time,missed_policy,allow_cli,enabled,next_run_at,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.WorkspaceID, item.Name, item.TargetKind, item.WorkflowID, item.BackendID, item.Prompt, item.Frequency, item.Timezone, item.LocalDate, item.LocalTime, item.MissedPolicy, item.AllowCLI, item.Enabled, optionalTime(item.NextRunAt), item.Revision, formatCatalogTime(item.CreatedAt), formatCatalogTime(item.UpdatedAt))
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE schedules SET name=?,target_kind=?,workflow_id=?,backend_id=?,prompt=?,frequency=?,timezone=?,local_date=?,local_time=?,missed_policy=?,allow_cli=?,enabled=?,next_run_at=?,revision=revision+1,updated_at=? WHERE id=? AND workspace_id=? AND revision=?`, item.Name, item.TargetKind, item.WorkflowID, item.BackendID, item.Prompt, item.Frequency, item.Timezone, item.LocalDate, item.LocalTime, item.MissedPolicy, item.AllowCLI, item.Enabled, optionalTime(item.NextRunAt), formatCatalogTime(item.UpdatedAt), item.ID, item.WorkspaceID, expectedRevision)
	}
	if err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read schedule write: %w", err)
	}
	if changed != 1 {
		return ErrScheduleConflict
	}
	return nil
}

func (s *Store) GetSchedule(ctx context.Context, scheduleID string) (catalog.Schedule, error) {
	item, err := scanSchedule(s.db.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id=?", scheduleID))
	if err != nil {
		return item, fmt.Errorf("get schedule: %w", err)
	}
	return item, nil
}

func (s *Store) ListSchedules(ctx context.Context, workspaceID string) ([]catalog.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE workspace_id=? ORDER BY updated_at DESC,id", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.Schedule, 0)
	for rows.Next() {
		item, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan schedule: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListDueSchedules(ctx context.Context, now time.Time) ([]catalog.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE enabled=1 AND next_run_at!='' AND next_run_at<=? ORDER BY next_run_at,id LIMIT 32", formatCatalogTime(now))
	if err != nil {
		return nil, fmt.Errorf("list due schedules: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.Schedule, 0)
	for rows.Next() {
		item, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan due schedule: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const jobColumns = "id,schedule_id,workspace_id,target_kind,workflow_id,backend_id,prompt,trigger,status,error_code,workflow_run_id,session_id,due_at,created_at,started_at,finished_at"

func scanScheduleJob(row rowScanner) (catalog.ScheduleJob, error) {
	var item catalog.ScheduleJob
	var due, created, started, finished string
	if err := row.Scan(&item.ID, &item.ScheduleID, &item.WorkspaceID, &item.TargetKind, &item.WorkflowID, &item.BackendID, &item.Prompt, &item.Trigger, &item.Status, &item.ErrorCode, &item.WorkflowRunID, &item.SessionID, &due, &created, &started, &finished); err != nil {
		return item, err
	}
	var err error
	if item.DueAt, err = time.Parse(time.RFC3339Nano, due); err != nil {
		return item, fmt.Errorf("parse job due: %w", err)
	}
	if item.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return item, fmt.Errorf("parse job creation: %w", err)
	}
	if item.StartedAt, err = parseOptionalTime(started); err != nil {
		return item, fmt.Errorf("parse job start: %w", err)
	}
	if item.FinishedAt, err = parseOptionalTime(finished); err != nil {
		return item, fmt.Errorf("parse job finish: %w", err)
	}
	return item, nil
}

func insertScheduleJob(ctx context.Context, tx *sql.Tx, job catalog.ScheduleJob) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO schedule_jobs(id,schedule_id,workspace_id,target_kind,workflow_id,backend_id,prompt,trigger,status,error_code,workflow_run_id,session_id,due_at,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, job.ID, job.ScheduleID, job.WorkspaceID, job.TargetKind, job.WorkflowID, job.BackendID, job.Prompt, job.Trigger, job.Status, job.ErrorCode, job.WorkflowRunID, job.SessionID, formatCatalogTime(job.DueAt), formatCatalogTime(job.CreatedAt), optionalTime(job.StartedAt), optionalTime(job.FinishedAt))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO streams(id,kind,created_at) VALUES(?,?,?)", job.ID, "schedule_job", formatCatalogTime(job.CreatedAt)); err != nil {
		return fmt.Errorf("create schedule job stream: %w", err)
	}
	return appendRunEvent(ctx, tx, job.ID, "schedule."+job.Status, map[string]any{"scheduleId": job.ScheduleID, "status": job.Status, "errorCode": job.ErrorCode}, job.CreatedAt)
}

func newScheduleJob(schedule catalog.Schedule, due, now time.Time, trigger, status string) catalog.ScheduleJob {
	return catalog.ScheduleJob{ID: id.New(), ScheduleID: schedule.ID, WorkspaceID: schedule.WorkspaceID, TargetKind: schedule.TargetKind, WorkflowID: schedule.WorkflowID, BackendID: schedule.BackendID, Prompt: schedule.Prompt, Trigger: trigger, Status: status, DueAt: due, CreatedAt: now}
}

func (s *Store) ClaimDueSchedule(ctx context.Context, scheduleID string, due, next, now time.Time, status string) (catalog.ScheduleJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return catalog.ScheduleJob{}, fmt.Errorf("begin due claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	schedule, err := scanSchedule(tx.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id=?", scheduleID))
	if err != nil {
		return catalog.ScheduleJob{}, fmt.Errorf("get due schedule: %w", err)
	}
	if !schedule.Enabled || !schedule.NextRunAt.Equal(due) {
		return catalog.ScheduleJob{}, ErrScheduleConflict
	}
	skipReason := "missed_execution"
	if status == "queued" {
		var active bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schedule_jobs WHERE schedule_id=? AND status IN ('queued','running','waiting_user','cancel_requested'))", scheduleID).Scan(&active); err != nil {
			return catalog.ScheduleJob{}, fmt.Errorf("check active schedule job: %w", err)
		}
		if active {
			status, skipReason = "skipped", "previous_job_active"
		}
	}
	result, err := tx.ExecContext(ctx, "UPDATE schedules SET next_run_at=?,enabled=?,revision=revision+1,updated_at=? WHERE id=? AND enabled=1 AND next_run_at=? AND revision=?", optionalTime(next), !next.IsZero(), formatCatalogTime(now), scheduleID, optionalTime(due), schedule.Revision)
	if err != nil {
		return catalog.ScheduleJob{}, fmt.Errorf("advance schedule: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return catalog.ScheduleJob{}, err
	}
	if changed != 1 {
		return catalog.ScheduleJob{}, ErrScheduleConflict
	}
	job := newScheduleJob(schedule, due, now, "scheduled", status)
	if status == "skipped" {
		job.ErrorCode, job.FinishedAt = skipReason, now
	}
	if err := insertScheduleJob(ctx, tx, job); err != nil {
		return catalog.ScheduleJob{}, fmt.Errorf("insert due job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return catalog.ScheduleJob{}, fmt.Errorf("commit due claim: %w", err)
	}
	return job, nil
}

func (s *Store) CreateManualScheduleJob(ctx context.Context, scheduleID string, now time.Time) (catalog.ScheduleJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return catalog.ScheduleJob{}, fmt.Errorf("begin manual job: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	schedule, err := scanSchedule(tx.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id=?", scheduleID))
	if err != nil {
		return catalog.ScheduleJob{}, fmt.Errorf("get manual schedule: %w", err)
	}
	var active bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schedule_jobs WHERE schedule_id=? AND status IN ('queued','running','waiting_user','cancel_requested'))", scheduleID).Scan(&active); err != nil {
		return catalog.ScheduleJob{}, fmt.Errorf("check active schedule job: %w", err)
	}
	if active {
		return catalog.ScheduleJob{}, ErrScheduleJobConflict
	}
	job := newScheduleJob(schedule, now, now, "manual", "queued")
	if err := insertScheduleJob(ctx, tx, job); err != nil {
		return catalog.ScheduleJob{}, fmt.Errorf("insert manual job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return catalog.ScheduleJob{}, fmt.Errorf("commit manual job: %w", err)
	}
	return job, nil
}

func (s *Store) GetScheduleJob(ctx context.Context, jobID string) (catalog.ScheduleJob, error) {
	job, err := scanScheduleJob(s.db.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM schedule_jobs WHERE id=?", jobID))
	if err != nil {
		return job, fmt.Errorf("get schedule job: %w", err)
	}
	return job, nil
}

func (s *Store) ListScheduleJobs(ctx context.Context, workspaceID string) ([]catalog.ScheduleJob, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+jobColumns+" FROM schedule_jobs WHERE workspace_id=? ORDER BY created_at DESC,id DESC LIMIT 200", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list schedule jobs: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.ScheduleJob, 0)
	for rows.Next() {
		item, err := scanScheduleJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan schedule job: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListQueuedScheduleJobs(ctx context.Context) ([]catalog.ScheduleJob, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+jobColumns+" FROM schedule_jobs WHERE status='queued' ORDER BY created_at,id LIMIT 32")
	if err != nil {
		return nil, fmt.Errorf("list queued jobs: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.ScheduleJob, 0)
	for rows.Next() {
		item, err := scanScheduleJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan queued job: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListWaitingScheduleJobs(ctx context.Context, afterID string) ([]catalog.ScheduleJob, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+jobColumns+" FROM schedule_jobs WHERE status IN ('waiting_user','cancel_requested') AND id>? ORDER BY id LIMIT 32", afterID)
	if err != nil {
		return nil, fmt.Errorf("list waiting schedule jobs: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.ScheduleJob, 0)
	for rows.Next() {
		item, err := scanScheduleJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan waiting schedule job: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateScheduleJob(ctx context.Context, job catalog.ScheduleJob, expectedStatus string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schedule job transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, "UPDATE schedule_jobs SET status=?,error_code=?,workflow_run_id=?,session_id=?,started_at=?,finished_at=? WHERE id=? AND status=?", job.Status, job.ErrorCode, job.WorkflowRunID, job.SessionID, optionalTime(job.StartedAt), optionalTime(job.FinishedAt), job.ID, expectedStatus)
	if err != nil {
		return fmt.Errorf("update schedule job: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrScheduleJobConflict
	}
	eventType := "schedule." + job.Status
	if expectedStatus == job.Status {
		eventType = "schedule.linked"
	}
	if err := appendRunEvent(ctx, tx, job.ID, eventType, map[string]any{"status": job.Status, "errorCode": job.ErrorCode, "workflowRunId": job.WorkflowRunID, "sessionId": job.SessionID}, time.Now().UTC()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schedule job transition: %w", err)
	}
	return nil
}

func (s *Store) InterruptRunningScheduleJobs(ctx context.Context, now time.Time) error {
	for {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin schedule recovery: %w", err)
		}
		rows, err := tx.QueryContext(ctx, "SELECT id,status FROM schedule_jobs WHERE status IN ('running','waiting_user','cancel_requested') ORDER BY id LIMIT 100")
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("list unfinished schedule jobs: %w", err)
		}
		type pending struct{ id, status string }
		items := make([]pending, 0, 100)
		for rows.Next() {
			var item pending
			if err := rows.Scan(&item.id, &item.status); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return fmt.Errorf("scan unfinished job: %w", err)
			}
			items = append(items, item)
		}
		readErr := rows.Err()
		_ = rows.Close()
		if readErr != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read unfinished jobs: %w", readErr)
		}
		for _, item := range items {
			if _, err := tx.ExecContext(ctx, "UPDATE schedule_jobs SET status='interrupted',error_code='app_restart',finished_at=? WHERE id=? AND status=?", formatCatalogTime(now), item.id, item.status); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("interrupt job: %w", err)
			}
			if err := appendRunEvent(ctx, tx, item.id, "schedule.interrupted", map[string]any{"status": "interrupted", "errorCode": "app_restart"}, now); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit schedule recovery: %w", err)
		}
		if len(items) < 100 {
			return nil
		}
	}
}
