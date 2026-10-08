package catalog

import "time"

type Schedule struct {
	ID, WorkspaceID, Name                                   string
	TargetKind, WorkflowID, BackendID, Prompt               string
	Frequency, Timezone, LocalDate, LocalTime, MissedPolicy string
	AllowCLI                                                bool
	Enabled                                                 bool
	NextRunAt                                               time.Time
	Revision                                                int64
	CreatedAt, UpdatedAt                                    time.Time
}

type ScheduleJob struct {
	ID, ScheduleID, WorkspaceID                          string
	TargetKind, WorkflowID, BackendID, Prompt            string
	Trigger, Status, ErrorCode, WorkflowRunID, SessionID string
	DueAt, CreatedAt, StartedAt, FinishedAt              time.Time
}
