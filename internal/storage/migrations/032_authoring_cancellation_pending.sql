CREATE TABLE pipeline_authoring_stops (
 attempt_id TEXT PRIMARY KEY REFERENCES pipeline_authoring_attempts(id) ON DELETE RESTRICT,
 pipeline_id TEXT NOT NULL, stage TEXT NOT NULL, session_id TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','confirmed')),
 proof TEXT NOT NULL DEFAULT '' CHECK(proof IN ('','joined','terminal_journal','owner_exited')),
 created_at TEXT NOT NULL, confirmed_at TEXT NOT NULL DEFAULT '',
 FOREIGN KEY(pipeline_id,stage) REFERENCES pipeline_authoring_stages(pipeline_id,stage) ON DELETE RESTRICT
);
CREATE TRIGGER authoring_stop_identity BEFORE UPDATE OF attempt_id,pipeline_id,stage,session_id,created_at ON pipeline_authoring_stops
BEGIN SELECT RAISE(ABORT,'immutable authoring stop identity'); END;
CREATE TRIGGER authoring_stop_terminal BEFORE UPDATE ON pipeline_authoring_stops WHEN OLD.state='confirmed' OR NEW.state!='confirmed'
BEGIN SELECT RAISE(ABORT,'terminal authoring stop'); END;
CREATE TRIGGER authoring_stop_no_delete BEFORE DELETE ON pipeline_authoring_stops
BEGIN SELECT RAISE(ABORT,'immutable authoring stop'); END;
