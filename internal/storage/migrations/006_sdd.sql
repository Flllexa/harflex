CREATE TABLE pipeline_runs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    title TEXT NOT NULL,
    objective TEXT NOT NULL,
    current_stage TEXT NOT NULL,
    stage_status BLOB NOT NULL,
    revision INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX pipeline_runs_workspace_updated ON pipeline_runs(workspace_id, updated_at DESC);

CREATE TABLE pipeline_artifacts (
    pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    stage TEXT NOT NULL,
    version INTEGER NOT NULL,
    content TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (pipeline_id, stage)
);

CREATE TABLE pipeline_transitions (
    id TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    stage TEXT NOT NULL,
    action TEXT NOT NULL,
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX pipeline_transitions_run_time ON pipeline_transitions(pipeline_id, created_at);
