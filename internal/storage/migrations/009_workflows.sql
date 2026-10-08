CREATE TABLE workflows (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    name TEXT NOT NULL,
    steps BLOB NOT NULL,
    revision INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX workflows_workspace_updated ON workflows(workspace_id, updated_at DESC);

CREATE TABLE workflow_runs (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL REFERENCES workflows(id),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    backend_id TEXT NOT NULL,
    steps BLOB NOT NULL,
    current_step INTEGER NOT NULL,
    status TEXT NOT NULL,
    last_session_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX workflow_runs_workspace_updated ON workflow_runs(workspace_id, updated_at DESC);
