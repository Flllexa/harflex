CREATE TABLE pipeline_design_workspaces (
 pipeline_id TEXT PRIMARY KEY REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
 pipeline_revision INTEGER NOT NULL CHECK(pipeline_revision>0),
 revision INTEGER NOT NULL CHECK(revision>0),
 state TEXT NOT NULL CHECK(state IN ('ready','running','paused','approved','cancellation_pending')),
 phase TEXT NOT NULL DEFAULT '' CHECK(phase IN ('','discovery','spec','plan')),
 active_attempt_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);

CREATE TABLE pipeline_design_versions (
 pipeline_id TEXT NOT NULL REFERENCES pipeline_design_workspaces(pipeline_id) ON DELETE RESTRICT,
 stage TEXT NOT NULL CHECK(stage IN ('discovery','spec','plan')),
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
 target TEXT NOT NULL CHECK(target IN ('all','discovery','spec','plan')),
 status TEXT NOT NULL CHECK(status IN ('running','cancellation_pending','completed','failed','cancelled','interrupted','stale')),
 phase TEXT NOT NULL CHECK(phase IN ('discovery','spec','plan')),
 source_revision INTEGER NOT NULL CHECK(source_revision>0),
 source_pipeline_revision INTEGER NOT NULL CHECK(source_pipeline_revision>0),
 input_snapshot BLOB NOT NULL, selection_snapshot BLOB NOT NULL,
 error_code TEXT NOT NULL DEFAULT '', result_hash TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(pipeline_id,request_id)
);
CREATE UNIQUE INDEX pipeline_design_single_owner ON pipeline_design_attempts(pipeline_id) WHERE status IN ('running','cancellation_pending');

CREATE TABLE pipeline_design_attempt_sessions (
 attempt_id TEXT NOT NULL REFERENCES pipeline_design_attempts(id) ON DELETE RESTRICT,
 stage TEXT NOT NULL CHECK(stage IN ('discovery','spec','plan')),
 session_id TEXT NOT NULL UNIQUE REFERENCES sessions(id) ON DELETE RESTRICT,
 created_at TEXT NOT NULL,
 PRIMARY KEY(attempt_id,stage)
);

CREATE TABLE pipeline_design_messages (
 id TEXT PRIMARY KEY,
 pipeline_id TEXT NOT NULL REFERENCES pipeline_design_workspaces(pipeline_id) ON DELETE RESTRICT,
 role TEXT NOT NULL CHECK(role IN ('user','assistant','system')),
 content TEXT NOT NULL, target TEXT NOT NULL CHECK(target IN ('all','discovery','spec','plan')),
 attempt_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE INDEX pipeline_design_message_order ON pipeline_design_messages(pipeline_id,created_at);

CREATE TABLE pipeline_design_commands (
 pipeline_id TEXT NOT NULL REFERENCES pipeline_design_workspaces(pipeline_id) ON DELETE RESTRICT,
 request_id TEXT NOT NULL, action TEXT NOT NULL CHECK(action IN ('prepare','edit','restore','approve')),
 intent_hash TEXT NOT NULL CHECK(length(intent_hash)=64),
 attempt_id TEXT NOT NULL DEFAULT '', result_snapshot BLOB NOT NULL,
 created_at TEXT NOT NULL,
 PRIMARY KEY(pipeline_id,request_id)
);

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
