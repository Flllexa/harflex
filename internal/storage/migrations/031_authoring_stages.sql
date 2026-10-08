CREATE TABLE pipeline_authoring_stages (
 pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
 stage TEXT NOT NULL CHECK(stage IN ('spec','plan')),
 revision INTEGER NOT NULL CHECK(revision>=0),
 state TEXT NOT NULL CHECK(state IN ('ready','running','waiting_user','paused','approved','skipped')),
 artifact_version INTEGER NOT NULL DEFAULT 0 CHECK(artifact_version BETWEEN 0 AND 3),
 attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 6),
 input_budget_remaining INTEGER NOT NULL DEFAULT 1572864 CHECK(input_budget_remaining BETWEEN 0 AND 1572864),
 output_budget_remaining INTEGER NOT NULL DEFAULT 24576 CHECK(output_budget_remaining BETWEEN 0 AND 24576),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 PRIMARY KEY(pipeline_id,stage)
);
CREATE TABLE pipeline_authoring_attempts (
 id TEXT PRIMARY KEY,
 pipeline_id TEXT NOT NULL,
 stage TEXT NOT NULL,
 request_id TEXT NOT NULL,
 payload_hash TEXT NOT NULL,
 artifact_version INTEGER NOT NULL CHECK(artifact_version BETWEEN 1 AND 3),
 status TEXT NOT NULL CHECK(status IN ('running','completed','failed','interrupted')),
 session_id TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '' CHECK(error_code IN ('','provider_failed','invalid_output','output_overflow','budget_overrun','timeout','cancelled','interrupted')),
 input_snapshot BLOB NOT NULL, selection_snapshot BLOB NOT NULL,
 reserved_input_tokens INTEGER NOT NULL CHECK(reserved_input_tokens BETWEEN 1 AND 262144),
 reserved_output_tokens INTEGER NOT NULL CHECK(reserved_output_tokens BETWEEN 1 AND 4096),
 actual_usage BLOB, result_hash TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 FOREIGN KEY(pipeline_id,stage) REFERENCES pipeline_authoring_stages(pipeline_id,stage) ON DELETE RESTRICT,
 UNIQUE(pipeline_id,request_id)
);
CREATE UNIQUE INDEX authoring_one_running ON pipeline_authoring_attempts(pipeline_id,stage) WHERE status='running';
CREATE UNIQUE INDEX authoring_unique_session ON pipeline_authoring_attempts(session_id) WHERE session_id!='';
CREATE TABLE pipeline_authoring_artifacts (
 pipeline_id TEXT NOT NULL,stage TEXT NOT NULL,version INTEGER NOT NULL CHECK(version BETWEEN 1 AND 3),
 content BLOB NOT NULL,content_hash TEXT NOT NULL,
 author TEXT NOT NULL CHECK(author='ai'),
 attempt_id TEXT NOT NULL UNIQUE REFERENCES pipeline_authoring_attempts(id) ON DELETE RESTRICT,
 source_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE RESTRICT,
 status TEXT NOT NULL CHECK(status IN ('waiting_user','approved','rejected','bypassed')),
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 PRIMARY KEY(pipeline_id,stage,version),
 FOREIGN KEY(pipeline_id,stage) REFERENCES pipeline_authoring_stages(pipeline_id,stage) ON DELETE RESTRICT
);
CREATE TABLE pipeline_authoring_requests (
 pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
 request_id TEXT NOT NULL,stage TEXT NOT NULL CHECK(stage IN ('spec','plan')),
 action TEXT NOT NULL CHECK(action IN ('start','revision','approve','skip','cancel')),
 payload_hash TEXT NOT NULL,client_intent_hash TEXT NOT NULL,
 attempt_id TEXT NOT NULL DEFAULT '',artifact_version INTEGER NOT NULL,
 actor TEXT NOT NULL CHECK(actor='local_user'),feedback TEXT NOT NULL DEFAULT '',reason TEXT NOT NULL DEFAULT '',
 result_stage_revision INTEGER NOT NULL,result_pipeline_revision INTEGER NOT NULL,result_snapshot BLOB NOT NULL,
 created_at TEXT NOT NULL,PRIMARY KEY(pipeline_id,request_id)
);
CREATE TRIGGER authoring_immutable_attempt BEFORE UPDATE OF id,pipeline_id,stage,request_id,payload_hash,artifact_version,input_snapshot,selection_snapshot,reserved_input_tokens,reserved_output_tokens,created_at ON pipeline_authoring_attempts
BEGIN SELECT RAISE(ABORT,'immutable authoring attempt'); END;
CREATE TRIGGER authoring_immutable_artifact BEFORE UPDATE OF pipeline_id,stage,version,content,content_hash,author,attempt_id,source_session_id,created_at ON pipeline_authoring_artifacts
BEGIN SELECT RAISE(ABORT,'immutable authoring artifact'); END;
CREATE TRIGGER authoring_terminal_artifact BEFORE UPDATE OF status ON pipeline_authoring_artifacts WHEN OLD.status!='waiting_user' OR NEW.status NOT IN ('approved','rejected','bypassed')
BEGIN SELECT RAISE(ABORT,'terminal authoring artifact'); END;
CREATE TRIGGER authoring_immutable_request BEFORE UPDATE ON pipeline_authoring_requests
BEGIN SELECT RAISE(ABORT,'immutable authoring request'); END;
CREATE TRIGGER authoring_no_delete_request BEFORE DELETE ON pipeline_authoring_requests
BEGIN SELECT RAISE(ABORT,'immutable authoring request'); END;
CREATE TRIGGER authoring_no_delete_artifact BEFORE DELETE ON pipeline_authoring_artifacts
BEGIN SELECT RAISE(ABORT,'immutable authoring artifact'); END;
CREATE TRIGGER authoring_no_delete_attempt BEFORE DELETE ON pipeline_authoring_attempts
BEGIN SELECT RAISE(ABORT,'immutable authoring attempt'); END;
CREATE TRIGGER authoring_terminal_attempt BEFORE UPDATE ON pipeline_authoring_attempts WHEN OLD.status!='running'
BEGIN SELECT RAISE(ABORT,'terminal authoring attempt'); END;
CREATE TRIGGER authoring_session_once BEFORE UPDATE OF session_id ON pipeline_authoring_attempts WHEN OLD.session_id!='' OR NEW.session_id=''
BEGIN SELECT RAISE(ABORT,'immutable authoring session link'); END;
