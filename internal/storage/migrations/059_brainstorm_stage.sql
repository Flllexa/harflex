-- The Brainstorm stage sits between SPEC and Plan: the preparation workspace gets a fourth document kind and the per-phase
-- executors a seventh stage. SQLite cannot change a CHECK, so these tables are rebuilt with the same rows. Foreign keys
-- stay on during migrations, so the old tables are renamed first (their children follow the rename), the new ones are
-- created and filled parents first, the old ones dropped children first, and only then the indexes and triggers are
-- created again (an old trigger or index still holds its name until its table is dropped).
ALTER TABLE pipeline_design_workspaces RENAME TO pipeline_design_workspaces_old;
ALTER TABLE pipeline_design_versions RENAME TO pipeline_design_versions_old;
ALTER TABLE pipeline_design_documents RENAME TO pipeline_design_documents_old;
ALTER TABLE pipeline_design_attempts RENAME TO pipeline_design_attempts_old;
ALTER TABLE pipeline_design_attempt_sessions RENAME TO pipeline_design_attempt_sessions_old;
ALTER TABLE pipeline_design_messages RENAME TO pipeline_design_messages_old;
ALTER TABLE pipeline_design_commands RENAME TO pipeline_design_commands_old;

CREATE TABLE pipeline_design_workspaces (
 pipeline_id TEXT PRIMARY KEY REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
 pipeline_revision INTEGER NOT NULL CHECK(pipeline_revision>0),
 revision INTEGER NOT NULL CHECK(revision>0),
 state TEXT NOT NULL CHECK(state IN ('ready','running','paused','approved','cancellation_pending')),
 phase TEXT NOT NULL DEFAULT '' CHECK(phase IN ('','discovery','spec','brainstorm','plan')),
 active_attempt_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);

CREATE TABLE pipeline_design_versions (
 pipeline_id TEXT NOT NULL REFERENCES pipeline_design_workspaces(pipeline_id) ON DELETE RESTRICT,
 stage TEXT NOT NULL CHECK(stage IN ('discovery','spec','brainstorm','plan')),
 version INTEGER NOT NULL CHECK(version>0),
 content TEXT NOT NULL, content_digest TEXT NOT NULL CHECK(length(content_digest)=64),
 author TEXT NOT NULL CHECK(author IN ('user','ai','legacy/manual')),
 source_session_id TEXT NOT NULL DEFAULT '', source_digest TEXT NOT NULL DEFAULT '',
 selection_snapshot BLOB NOT NULL, attempt_id TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL CHECK(reason IN ('import','generated','manual','restored')),
 restored_from_version INTEGER NOT NULL DEFAULT 0 CHECK(restored_from_version>=0),
 created_at TEXT NOT NULL,
 PRIMARY KEY(pipeline_id,stage,version)
);

CREATE TABLE pipeline_design_documents (
 pipeline_id TEXT NOT NULL, stage TEXT NOT NULL,
 current_version INTEGER NOT NULL, stale INTEGER NOT NULL DEFAULT 0 CHECK(stale IN (0,1)),
 updated_at TEXT NOT NULL,
 PRIMARY KEY(pipeline_id,stage),
 FOREIGN KEY(pipeline_id,stage,current_version) REFERENCES pipeline_design_versions(pipeline_id,stage,version) ON DELETE RESTRICT
);

CREATE TABLE pipeline_design_attempts (
 id TEXT PRIMARY KEY,
 pipeline_id TEXT NOT NULL REFERENCES pipeline_design_workspaces(pipeline_id) ON DELETE RESTRICT,
 request_id TEXT NOT NULL, intent_hash TEXT NOT NULL CHECK(length(intent_hash)=64),
 owner_pid INTEGER NOT NULL DEFAULT 0 CHECK(owner_pid>=0),
 target TEXT NOT NULL CHECK(target IN ('all','discovery','spec','brainstorm','plan')),
 status TEXT NOT NULL CHECK(status IN ('running','cancellation_pending','completed','failed','cancelled','interrupted','stale')),
 phase TEXT NOT NULL CHECK(phase IN ('discovery','spec','brainstorm','plan')),
 source_revision INTEGER NOT NULL CHECK(source_revision>0),
 source_pipeline_revision INTEGER NOT NULL CHECK(source_pipeline_revision>0),
 input_snapshot BLOB NOT NULL, selection_snapshot BLOB NOT NULL,
 error_code TEXT NOT NULL DEFAULT '', result_hash TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(pipeline_id,request_id)
);

CREATE TABLE pipeline_design_attempt_sessions (
 attempt_id TEXT NOT NULL REFERENCES pipeline_design_attempts(id) ON DELETE RESTRICT,
 stage TEXT NOT NULL CHECK(stage IN ('discovery','spec','brainstorm','plan')),
 session_id TEXT NOT NULL UNIQUE REFERENCES sessions(id) ON DELETE RESTRICT,
 created_at TEXT NOT NULL,
 PRIMARY KEY(attempt_id,stage)
);

CREATE TABLE pipeline_design_messages (
 id TEXT PRIMARY KEY,
 pipeline_id TEXT NOT NULL REFERENCES pipeline_design_workspaces(pipeline_id) ON DELETE RESTRICT,
 role TEXT NOT NULL CHECK(role IN ('user','assistant','system')),
 content TEXT NOT NULL, target TEXT NOT NULL CHECK(target IN ('all','discovery','spec','brainstorm','plan')),
 attempt_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);

CREATE TABLE pipeline_design_commands (
 pipeline_id TEXT NOT NULL REFERENCES pipeline_design_workspaces(pipeline_id) ON DELETE RESTRICT,
 request_id TEXT NOT NULL, action TEXT NOT NULL CHECK(action IN ('prepare','edit','restore','approve')),
 intent_hash TEXT NOT NULL CHECK(length(intent_hash)=64),
 attempt_id TEXT NOT NULL DEFAULT '', result_snapshot BLOB NOT NULL,
 created_at TEXT NOT NULL,
 PRIMARY KEY(pipeline_id,request_id)
);

INSERT INTO pipeline_design_workspaces SELECT * FROM pipeline_design_workspaces_old;
INSERT INTO pipeline_design_versions SELECT * FROM pipeline_design_versions_old;
INSERT INTO pipeline_design_documents SELECT * FROM pipeline_design_documents_old;
INSERT INTO pipeline_design_attempts SELECT * FROM pipeline_design_attempts_old;
INSERT INTO pipeline_design_attempt_sessions SELECT * FROM pipeline_design_attempt_sessions_old;
INSERT INTO pipeline_design_messages SELECT * FROM pipeline_design_messages_old;
INSERT INTO pipeline_design_commands SELECT * FROM pipeline_design_commands_old;

DROP TABLE pipeline_design_documents_old;
DROP TABLE pipeline_design_attempt_sessions_old;
DROP TABLE pipeline_design_commands_old;
DROP TABLE pipeline_design_messages_old;
DROP TABLE pipeline_design_attempts_old;
DROP TABLE pipeline_design_versions_old;
DROP TABLE pipeline_design_workspaces_old;

CREATE UNIQUE INDEX pipeline_design_single_owner ON pipeline_design_attempts(pipeline_id) WHERE status IN ('running','cancellation_pending');
CREATE INDEX pipeline_design_message_order ON pipeline_design_messages(pipeline_id,created_at);

CREATE TRIGGER pipeline_design_version_immutable BEFORE UPDATE ON pipeline_design_versions
BEGIN SELECT RAISE(ABORT,'immutable design version'); END;
CREATE TRIGGER pipeline_design_version_no_delete BEFORE DELETE ON pipeline_design_versions
BEGIN SELECT RAISE(ABORT,'immutable design version'); END;
CREATE TRIGGER pipeline_design_message_immutable BEFORE UPDATE ON pipeline_design_messages
BEGIN SELECT RAISE(ABORT,'immutable design message'); END;
CREATE TRIGGER pipeline_design_message_no_delete BEFORE DELETE ON pipeline_design_messages
BEGIN SELECT RAISE(ABORT,'immutable design message'); END;
CREATE TRIGGER pipeline_design_command_immutable BEFORE UPDATE ON pipeline_design_commands
BEGIN SELECT RAISE(ABORT,'immutable design command'); END;
CREATE TRIGGER pipeline_design_command_no_delete BEFORE DELETE ON pipeline_design_commands
BEGIN SELECT RAISE(ABORT,'immutable design command'); END;
CREATE TRIGGER pipeline_design_attempt_identity BEFORE UPDATE OF id,pipeline_id,request_id,intent_hash,owner_pid,target,source_revision,source_pipeline_revision,input_snapshot,selection_snapshot,created_at ON pipeline_design_attempts
BEGIN SELECT RAISE(ABORT,'immutable design attempt'); END;
CREATE TRIGGER pipeline_design_attempt_terminal BEFORE UPDATE ON pipeline_design_attempts WHEN OLD.status NOT IN ('running','cancellation_pending')
BEGIN SELECT RAISE(ABORT,'terminal design attempt'); END;
CREATE TRIGGER pipeline_design_attempt_no_delete BEFORE DELETE ON pipeline_design_attempts
BEGIN SELECT RAISE(ABORT,'immutable design attempt'); END;
CREATE TRIGGER pipeline_design_session_immutable BEFORE UPDATE ON pipeline_design_attempt_sessions
BEGIN SELECT RAISE(ABORT,'immutable design session link'); END;
CREATE TRIGGER pipeline_design_session_no_delete BEFORE DELETE ON pipeline_design_attempt_sessions
BEGIN SELECT RAISE(ABORT,'immutable design session link'); END;

CREATE TABLE workspace_stage_executors_next (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    stage TEXT NOT NULL CHECK (stage IN ('discovery','spec','brainstorm','plan','code','eval','prs')),
    backend_id TEXT NOT NULL CHECK (length(backend_id) BETWEEN 1 AND 128),
    model_id TEXT NOT NULL DEFAULT '' CHECK (length(model_id) <= 512),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (workspace_id, stage)
);
INSERT INTO workspace_stage_executors_next SELECT workspace_id, stage, backend_id, model_id, updated_at FROM workspace_stage_executors;
DROP TABLE workspace_stage_executors;
ALTER TABLE workspace_stage_executors_next RENAME TO workspace_stage_executors;

-- Pipelines already past SPEC were prepared without the stage: it is skipped, with the reason on record. The others go
-- through it when they get there.
INSERT INTO pipeline_transitions (id, pipeline_id, stage, action, actor, reason, created_at)
SELECT lower(hex(randomblob(16))), id, 'brainstorm', 'skip', 'system', 'a etapa não existia quando este trabalho foi preparado',
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM pipeline_runs
WHERE current_stage IN ('plan', 'code', 'eval', 'prs', '')
  AND json_extract(CAST(stage_status AS TEXT), '$.brainstorm') IS NULL;
UPDATE pipeline_runs
SET stage_status = CAST(json_set(CAST(stage_status AS TEXT), '$.brainstorm',
        CASE WHEN current_stage IN ('plan', 'code', 'eval', 'prs', '') THEN 'skipped' ELSE 'pending' END) AS BLOB)
WHERE json_extract(CAST(stage_status AS TEXT), '$.brainstorm') IS NULL;
