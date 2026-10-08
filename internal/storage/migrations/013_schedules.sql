CREATE TABLE schedules (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    name TEXT NOT NULL,
    target_kind TEXT NOT NULL CHECK(target_kind IN ('workflow','prompt')),
    workflow_id TEXT NOT NULL DEFAULT '',
    backend_id TEXT NOT NULL,
    prompt TEXT NOT NULL DEFAULT '',
    frequency TEXT NOT NULL CHECK(frequency IN ('daily','once')),
    timezone TEXT NOT NULL,
    local_date TEXT NOT NULL DEFAULT '',
    local_time TEXT NOT NULL,
    missed_policy TEXT NOT NULL CHECK(missed_policy IN ('skip','run_once')),
    allow_cli INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL,
    next_run_at TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX schedules_due ON schedules(enabled, next_run_at);
CREATE INDEX schedules_workspace ON schedules(workspace_id, updated_at DESC);

CREATE TABLE schedule_jobs (
    id TEXT PRIMARY KEY,
    schedule_id TEXT NOT NULL REFERENCES schedules(id),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    target_kind TEXT NOT NULL,
    workflow_id TEXT NOT NULL DEFAULT '',
    backend_id TEXT NOT NULL,
    prompt TEXT NOT NULL DEFAULT '',
    trigger TEXT NOT NULL CHECK(trigger IN ('scheduled','manual')),
    status TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    workflow_run_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    due_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    started_at TEXT NOT NULL DEFAULT '',
    finished_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX schedule_jobs_unique_due ON schedule_jobs(schedule_id,due_at) WHERE trigger='scheduled';
CREATE INDEX schedule_jobs_workspace_created ON schedule_jobs(workspace_id,created_at DESC);
