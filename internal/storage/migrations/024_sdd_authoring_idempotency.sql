ALTER TABLE pipeline_runs ADD COLUMN creation_request_id TEXT NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN creation_request_hash TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX pipeline_runs_ai_authoring_request
    ON pipeline_runs (workspace_id, COALESCE(derived_from_pipeline_id, ''), creation_request_id)
    WHERE kind = 'ai_authoring' AND creation_request_id <> '';
