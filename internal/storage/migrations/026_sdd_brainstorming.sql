CREATE TABLE pipeline_brainstorm_runs (
    id TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
    discovery_version INTEGER NOT NULL CHECK (discovery_version > 0),
    start_request_id TEXT NOT NULL,
    start_payload_hash TEXT NOT NULL,
    discovery_content TEXT NOT NULL,
    discovery_hash TEXT NOT NULL,
    selection_snapshot BLOB NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('ready','running_question','waiting_answer','ready_for_synthesis','running_synthesis','waiting_user','paused','invalidated','skipped_waiting_confirmation','approved')),
    revision INTEGER NOT NULL CHECK (revision > 0),
    question_count INTEGER NOT NULL DEFAULT 0 CHECK (question_count BETWEEN 0 AND 5),
    current_question_id TEXT NOT NULL DEFAULT '',
    synthesis_version INTEGER NOT NULL DEFAULT 0 CHECK (synthesis_version BETWEEN 0 AND 3),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 8),
    input_budget_remaining INTEGER NOT NULL DEFAULT 262144 CHECK (input_budget_remaining BETWEEN 0 AND 262144),
    output_budget_remaining INTEGER NOT NULL DEFAULT 4608 CHECK (output_budget_remaining BETWEEN 0 AND 4608),
    active_duration INTEGER NOT NULL DEFAULT 0 CHECK (active_duration BETWEEN 0 AND 600000000000),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (pipeline_id, discovery_version),
    UNIQUE (pipeline_id, start_request_id)
);

CREATE TABLE pipeline_brainstorm_attempts (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES pipeline_brainstorm_runs(id) ON DELETE RESTRICT,
    discovery_version INTEGER NOT NULL CHECK (discovery_version > 0),
    synthesis_version INTEGER NOT NULL CHECK ((kind='question' AND synthesis_version=0) OR (kind='synthesis' AND synthesis_version BETWEEN 1 AND 3)),
    request_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('question','synthesis')),
    payload_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running','completed','failed','interrupted','stale')),
    session_id TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '' CHECK (error_code IN ('','cancelled','timeout','provider_failed','invalid_output','interrupted','discovery_revised','budget_overrun','output_overflow')),
    selection_snapshot BLOB NOT NULL,
    reserved_input_tokens INTEGER NOT NULL CHECK (reserved_input_tokens > 0),
    reserved_output_tokens INTEGER NOT NULL CHECK (reserved_output_tokens IN (256,1024)),
    actual_usage BLOB,
    result_hash TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (run_id, request_id)
);
CREATE UNIQUE INDEX pipeline_brainstorm_one_running ON pipeline_brainstorm_attempts(run_id) WHERE status = 'running';

-- A shared namespace includes generation and all human actions, including future gates.
CREATE TABLE pipeline_brainstorm_requests (
    run_id TEXT NOT NULL REFERENCES pipeline_brainstorm_runs(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('question','synthesis','answer','stop','resume','revision','bypass','confirm','approve','cancel')),
    payload_hash TEXT NOT NULL,
    result_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (run_id, request_id)
);

CREATE TABLE pipeline_brainstorm_turns (
    run_id TEXT NOT NULL REFERENCES pipeline_brainstorm_runs(id) ON DELETE RESTRICT,
    number INTEGER NOT NULL CHECK (number BETWEEN 1 AND 5),
    question_id TEXT NOT NULL UNIQUE,
    question TEXT NOT NULL,
    answer TEXT NOT NULL DEFAULT '',
    source_session_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('waiting_answer','answered','skipped')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (run_id, number)
);

CREATE TABLE pipeline_brainstorm_syntheses (
    run_id TEXT NOT NULL REFERENCES pipeline_brainstorm_runs(id) ON DELETE RESTRICT,
    version INTEGER NOT NULL CHECK (version BETWEEN 1 AND 3),
    discovery_version INTEGER NOT NULL CHECK (discovery_version > 0),
    content BLOB NOT NULL,
    source_session_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('completed','approved','rejected')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (run_id, version)
);

CREATE TRIGGER brainstorm_immutable_input BEFORE UPDATE OF pipeline_id, discovery_version, start_request_id, start_payload_hash, discovery_content, discovery_hash, selection_snapshot ON pipeline_brainstorm_runs
BEGIN SELECT RAISE(ABORT, 'immutable brainstorm input'); END;
CREATE TRIGGER brainstorm_immutable_question BEFORE UPDATE OF run_id, number, question_id, question, source_session_id ON pipeline_brainstorm_turns
BEGIN SELECT RAISE(ABORT, 'immutable brainstorm question'); END;
CREATE TRIGGER brainstorm_immutable_answer BEFORE UPDATE ON pipeline_brainstorm_turns WHEN OLD.status IN ('answered','skipped')
BEGIN SELECT RAISE(ABORT, 'immutable brainstorm answer'); END;
CREATE TRIGGER brainstorm_immutable_synthesis BEFORE UPDATE OF run_id,version,discovery_version,content,source_session_id,created_at ON pipeline_brainstorm_syntheses
BEGIN SELECT RAISE(ABORT, 'immutable brainstorm synthesis'); END;
CREATE TRIGGER brainstorm_terminal_synthesis BEFORE UPDATE OF status ON pipeline_brainstorm_syntheses WHEN OLD.status != 'completed' OR NEW.status NOT IN ('approved','rejected')
BEGIN SELECT RAISE(ABORT, 'terminal brainstorm synthesis'); END;
CREATE TRIGGER brainstorm_immutable_attempt BEFORE UPDATE OF id,run_id,discovery_version,synthesis_version,request_id,kind,payload_hash,selection_snapshot,reserved_input_tokens,reserved_output_tokens,created_at ON pipeline_brainstorm_attempts
BEGIN SELECT RAISE(ABORT, 'immutable brainstorm attempt'); END;
