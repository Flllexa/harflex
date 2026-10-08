CREATE TABLE pipeline_archived_artifacts (
    pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    stage TEXT NOT NULL CHECK (stage IN ('code', 'eval')),
    version INTEGER NOT NULL CHECK (version > 0),
    content TEXT NOT NULL,
    author TEXT NOT NULL,
    source_session_id TEXT NOT NULL,
    content_digest TEXT NOT NULL CHECK (length(content_digest) = 64),
    reason TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (pipeline_id, stage, version)
);

CREATE INDEX pipeline_archived_artifacts_by_stage
    ON pipeline_archived_artifacts(pipeline_id, stage, version DESC);
