-- Renumbered from 035 when the AI-authored Code line was merged with the line that already
-- used 035-043. Rebuilding the table is safe to repeat: a catalog that already widened it
-- gets an identical copy, and a catalog still on the spec/plan shape gets the widened one.
DROP TRIGGER authoring_model_preference_revision;
DROP TRIGGER authoring_model_preference_no_delete;

CREATE TABLE pipeline_authoring_model_preferences_next (
 pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
 stage TEXT NOT NULL CHECK(stage IN ('spec','plan','code','eval')),
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

INSERT INTO pipeline_authoring_model_preferences_next (
 pipeline_id,stage,model_mode,effort_mode,explicit_effort,
 model_selection_snapshot,revision,created_at,updated_at
)
SELECT pipeline_id,stage,model_mode,effort_mode,explicit_effort,
 model_selection_snapshot,revision,created_at,updated_at
FROM pipeline_authoring_model_preferences;

DROP TABLE pipeline_authoring_model_preferences;
ALTER TABLE pipeline_authoring_model_preferences_next RENAME TO pipeline_authoring_model_preferences;

CREATE TRIGGER authoring_model_preference_revision BEFORE UPDATE ON pipeline_authoring_model_preferences
WHEN NEW.revision != OLD.revision + 1
BEGIN SELECT RAISE(ABORT,'invalid authoring model preference revision'); END;
CREATE TRIGGER authoring_model_preference_no_delete BEFORE DELETE ON pipeline_authoring_model_preferences
BEGIN SELECT RAISE(ABORT,'immutable authoring model preference history'); END;
