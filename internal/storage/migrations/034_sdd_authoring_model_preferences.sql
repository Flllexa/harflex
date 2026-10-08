CREATE TABLE pipeline_authoring_model_preferences (
 pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
 stage TEXT NOT NULL CHECK(stage IN ('spec','plan')),
 model_mode TEXT NOT NULL CHECK(model_mode IN ('inherit','override')),
 effort_mode TEXT NOT NULL CHECK(effort_mode IN ('inherit','automatic','explicit')),
 explicit_effort TEXT NOT NULL DEFAULT '' CHECK(length(explicit_effort)<=64),
 model_selection_snapshot BLOB,
 revision INTEGER NOT NULL CHECK(revision>0),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY(pipeline_id,stage),
 CHECK((model_mode='inherit' AND model_selection_snapshot IS NULL) OR
       (model_mode='override' AND model_selection_snapshot IS NOT NULL)),
 CHECK((effort_mode='explicit' AND explicit_effort!='') OR (effort_mode!='explicit' AND explicit_effort=''))
);

CREATE TRIGGER authoring_model_preference_revision BEFORE UPDATE ON pipeline_authoring_model_preferences
WHEN NEW.revision != OLD.revision + 1
BEGIN SELECT RAISE(ABORT,'invalid authoring model preference revision'); END;
CREATE TRIGGER authoring_model_preference_no_delete BEFORE DELETE ON pipeline_authoring_model_preferences
BEGIN SELECT RAISE(ABORT,'immutable authoring model preference history'); END;

ALTER TABLE pipeline_authoring_attempts
 ADD COLUMN model_mode TEXT NOT NULL DEFAULT 'inherit' CHECK(model_mode IN ('inherit','override'));
ALTER TABLE pipeline_authoring_attempts
 ADD COLUMN effort_mode TEXT NOT NULL DEFAULT 'inherit' CHECK(effort_mode IN ('inherit','automatic','explicit'));
ALTER TABLE pipeline_authoring_attempts
 ADD COLUMN preference_source TEXT NOT NULL DEFAULT '' CHECK(preference_source IN ('','brainstorm','global_default','phase_override'));
ALTER TABLE pipeline_authoring_attempts
 ADD COLUMN preference_revision INTEGER NOT NULL DEFAULT 0 CHECK(preference_revision>=0);

DROP TRIGGER authoring_immutable_attempt;
CREATE TRIGGER authoring_immutable_attempt BEFORE UPDATE OF
 id,pipeline_id,stage,request_id,payload_hash,artifact_version,input_snapshot,selection_snapshot,
 reserved_input_tokens,reserved_output_tokens,model_mode,effort_mode,preference_source,preference_revision,created_at
 ON pipeline_authoring_attempts
BEGIN SELECT RAISE(ABORT,'immutable authoring attempt'); END;
