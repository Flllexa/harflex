ALTER TABLE sessions ADD COLUMN mode TEXT NOT NULL DEFAULT '';

CREATE TABLE pipeline_sessions (
    id TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL UNIQUE REFERENCES sessions(id),
    role TEXT NOT NULL CHECK (role IN ('coder', 'evaluator')),
    baseline_git_hash TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX pipeline_sessions_by_role ON pipeline_sessions(pipeline_id, role, created_at DESC);
