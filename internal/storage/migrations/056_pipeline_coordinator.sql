-- The chat that coordinates an SDD work: the conversation that created it, or the one the person opened for it.
ALTER TABLE pipeline_runs ADD COLUMN coordinator_session_id TEXT NOT NULL DEFAULT '';
CREATE INDEX pipeline_runs_by_coordinator ON pipeline_runs(coordinator_session_id) WHERE coordinator_session_id <> '';
