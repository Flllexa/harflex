-- Pull requests the PRs conversation opened, and the review watch that keeps fixing their comments.
CREATE TABLE IF NOT EXISTS pipeline_pull_requests (
    id TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'open',
    watch INTEGER NOT NULL DEFAULT 0,
    last_checked_at TEXT NOT NULL DEFAULT '',
    timeline TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (pipeline_id, url)
);
CREATE INDEX IF NOT EXISTS pipeline_pull_requests_watch ON pipeline_pull_requests (watch, state);
