package sqlite

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestSchedulesPersistAndClaimDueAtMostOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedules.db")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: "workspace", Path: t.TempDir(), Profile: "ask", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	item := catalog.Schedule{ID: "schedule", WorkspaceID: "workspace", Name: "Daily review", TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: "11:59", MissedPolicy: "run_once", Enabled: true, NextRunAt: due, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveSchedule(t.Context(), item, 0); err != nil {
		t.Fatal(err)
	}
	dueItems, err := store.ListDueSchedules(t.Context(), due.Add(time.Nanosecond))
	if err != nil || len(dueItems) != 1 {
		t.Fatalf("due at fractional now: %+v %v", dueItems, err)
	}
	next := due.Add(24 * time.Hour)
	job, err := store.ClaimDueSchedule(t.Context(), item.ID, due, next, now, "queued")
	if err != nil || job.Status != "queued" || job.Prompt != "Review" {
		t.Fatalf("claim: %+v %v", job, err)
	}
	events, err := store.ListAfter(t.Context(), job.ID, 0)
	if err != nil || len(events) != 1 || events[0].Type != "schedule.queued" {
		t.Fatalf("claim journal: %+v %v", events, err)
	}
	if _, err := store.ClaimDueSchedule(t.Context(), item.ID, due, next, now, "queued"); !errors.Is(err, ErrScheduleConflict) {
		t.Fatalf("duplicate claim: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	loaded, err := store.GetSchedule(t.Context(), item.ID)
	if err != nil || !loaded.NextRunAt.Equal(next) {
		t.Fatalf("reopened schedule: %+v %v", loaded, err)
	}
	jobs, err := store.ListScheduleJobs(t.Context(), item.WorkspaceID)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("reopened jobs: %+v %v", jobs, err)
	}
}

func TestRunningJobsAreInterruptedAfterRestart(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: "workspace", Path: t.TempDir(), Profile: "ask", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	item := catalog.Schedule{ID: "schedule", WorkspaceID: "workspace", Name: "Once", TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "once", Timezone: "UTC", LocalTime: "12:01", LocalDate: "2026-09-26", MissedPolicy: "skip", Enabled: true, NextRunAt: now.Add(time.Minute), Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveSchedule(t.Context(), item, 0); err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateManualScheduleJob(t.Context(), item.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManualScheduleJob(t.Context(), item.ID, now.Add(time.Second)); !errors.Is(err, ErrScheduleJobConflict) {
		t.Fatalf("duplicate active manual job: %v", err)
	}
	job.Status, job.StartedAt = "running", now
	if err := store.UpdateScheduleJob(t.Context(), job, "queued"); err != nil {
		t.Fatal(err)
	}
	job.Status = "cancel_requested"
	if err := store.UpdateScheduleJob(t.Context(), job, "running"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManualScheduleJob(t.Context(), item.ID, now.Add(2*time.Second)); !errors.Is(err, ErrScheduleJobConflict) {
		t.Fatalf("cancellation request must remain active: %v", err)
	}
	approvalSchedule := item
	approvalSchedule.ID = "schedule-approval"
	if err := store.SaveSchedule(t.Context(), approvalSchedule, 0); err != nil {
		t.Fatal(err)
	}
	approvalJob, err := store.CreateManualScheduleJob(t.Context(), approvalSchedule.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	approvalJob.Status, approvalJob.StartedAt = "running", now
	if err := store.UpdateScheduleJob(t.Context(), approvalJob, "queued"); err != nil {
		t.Fatal(err)
	}
	approvalJob.Status = "waiting_user"
	if err := store.UpdateScheduleJob(t.Context(), approvalJob, "running"); err != nil {
		t.Fatal(err)
	}
	waiting, err := store.ListWaitingScheduleJobs(t.Context(), "")
	if err != nil || len(waiting) != 2 {
		t.Fatalf("waiting jobs: %+v %v", waiting, err)
	}
	if err := store.InterruptRunningScheduleJobs(t.Context(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetScheduleJob(t.Context(), job.ID)
	if err != nil || loaded.Status != "interrupted" || loaded.ErrorCode != "app_restart" {
		t.Fatalf("recovered job: %+v %v", loaded, err)
	}
	approvalLoaded, err := store.GetScheduleJob(t.Context(), approvalJob.ID)
	if err != nil || approvalLoaded.Status != "interrupted" || approvalLoaded.ErrorCode != "app_restart" {
		t.Fatalf("stale approval job: %+v %v", approvalLoaded, err)
	}
	events, err := store.ListAfter(t.Context(), approvalJob.ID, 0)
	if err != nil || len(events) != 4 || events[len(events)-1].Type != "schedule.interrupted" {
		t.Fatalf("recovery journal: %+v %v", events, err)
	}
}

func TestWaitingScheduleJobsCanBeScannedWithoutStarvation(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "waiting.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: "workspace", Path: t.TempDir(), Profile: "ask", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 33; i++ {
		item := catalog.Schedule{ID: fmt.Sprintf("schedule-%02d", i), WorkspaceID: "workspace", Name: "Pending", TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: false, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.SaveSchedule(t.Context(), item, 0); err != nil {
			t.Fatal(err)
		}
		job, err := store.CreateManualScheduleJob(t.Context(), item.ID, now)
		if err != nil {
			t.Fatal(err)
		}
		job.Status = "waiting_user"
		if err := store.UpdateScheduleJob(t.Context(), job, "queued"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListWaitingScheduleJobs(t.Context(), "")
	if err != nil || len(first) != 32 {
		t.Fatalf("first page: %d %v", len(first), err)
	}
	second, err := store.ListWaitingScheduleJobs(t.Context(), first[len(first)-1].ID)
	if err != nil || len(second) != 1 {
		t.Fatalf("second page: %d %v", len(second), err)
	}
}

func TestOneTimeScheduleHasNoSecondAutomaticClaim(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "once.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	due := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: "workspace", Path: t.TempDir(), Profile: "ask", CreatedAt: due.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	item := catalog.Schedule{ID: "once", WorkspaceID: "workspace", Name: "Once", TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "once", Timezone: "UTC", LocalDate: "2026-09-27", LocalTime: "12:00", MissedPolicy: "run_once", Enabled: true, NextRunAt: due, Revision: 1, CreatedAt: due.Add(-time.Hour), UpdatedAt: due.Add(-time.Hour)}
	if err := store.SaveSchedule(t.Context(), item, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimDueSchedule(t.Context(), item.ID, due, time.Time{}, due, "queued"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetSchedule(t.Context(), item.ID)
	if err != nil || loaded.Enabled || !loaded.NextRunAt.IsZero() {
		t.Fatalf("one-time schedule still active: %+v %v", loaded, err)
	}
	if _, err := store.ClaimDueSchedule(t.Context(), item.ID, due, time.Time{}, due, "queued"); !errors.Is(err, ErrScheduleConflict) {
		t.Fatalf("second claim: %v", err)
	}
}
