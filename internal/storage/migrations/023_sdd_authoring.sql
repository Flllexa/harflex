ALTER TABLE pipeline_runs ADD COLUMN kind TEXT NOT NULL DEFAULT 'legacy' CHECK (kind IN ('legacy', 'ai_authoring'));
ALTER TABLE pipeline_runs ADD COLUMN derived_from_pipeline_id TEXT REFERENCES pipeline_runs(id) ON DELETE RESTRICT;
ALTER TABLE pipeline_runs ADD COLUMN discovery_frozen_version INTEGER NOT NULL DEFAULT 0 CHECK (discovery_frozen_version >= 0);

ALTER TABLE pipeline_artifacts ADD COLUMN author TEXT NOT NULL DEFAULT 'legacy/manual' CHECK (author IN ('legacy/manual', 'user', 'ai'));
ALTER TABLE pipeline_artifacts ADD COLUMN source_session_id TEXT NOT NULL DEFAULT '';
