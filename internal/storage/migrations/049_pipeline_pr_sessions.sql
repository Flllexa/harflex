-- The conversation in which the agent opens a pipeline's pull requests. It is an ordinary chat (it needs
-- shell, file and MCP tools), so it is linked here instead of widening the Code and QA session roles.
CREATE TABLE IF NOT EXISTS pipeline_pr_sessions (
    id TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL UNIQUE REFERENCES sessions(id),
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS pipeline_pr_sessions_by_pipeline ON pipeline_pr_sessions(pipeline_id, created_at DESC);
