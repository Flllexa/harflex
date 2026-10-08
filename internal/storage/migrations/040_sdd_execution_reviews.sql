ALTER TABLE pipeline_sessions ADD COLUMN evaluation_criteria TEXT NOT NULL DEFAULT '';

CREATE TABLE pipeline_execution_reviews (
    pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    request_id TEXT NOT NULL,
    stage TEXT NOT NULL CHECK (stage IN ('code', 'eval')),
    artifact_version INTEGER NOT NULL CHECK (artifact_version > 0),
    artifact_digest TEXT NOT NULL CHECK (length(artifact_digest) = 64),
    artifact_content TEXT NOT NULL,
    source_session_id TEXT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('approve', 'request_revision')),
    actor TEXT NOT NULL CHECK (actor = 'local_user'),
    feedback TEXT NOT NULL DEFAULT '',
    request_hash TEXT NOT NULL CHECK (length(request_hash) = 64),
    result_revision INTEGER NOT NULL CHECK (result_revision > 0),
    result_snapshot BLOB NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (pipeline_id, request_id),
    UNIQUE (pipeline_id, stage, artifact_version)
);

CREATE INDEX pipeline_execution_reviews_by_stage
    ON pipeline_execution_reviews(pipeline_id, stage, artifact_version DESC);
