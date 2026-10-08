CREATE TABLE session_model_selections_new (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    backend_id TEXT NOT NULL CHECK(length(backend_id) BETWEEN 1 AND 128),
    model_id TEXT NOT NULL CHECK(length(model_id) BETWEEN 1 AND 512),
    reasoning_effort TEXT NOT NULL DEFAULT '' CHECK(length(reasoning_effort) <= 64),
    catalog_revision TEXT NOT NULL CHECK(length(catalog_revision) BETWEEN 1 AND 128),
    local_revision TEXT NOT NULL DEFAULT '' CHECK(length(local_revision) <= 128),
    source TEXT NOT NULL CHECK(length(source) BETWEEN 1 AND 64),
    executable_path TEXT NOT NULL DEFAULT '',
    executable_version TEXT NOT NULL DEFAULT '',
    workspace_path TEXT NOT NULL,
    checked_at TEXT NOT NULL,
    destination TEXT NOT NULL DEFAULT '',
    credential_identity TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    confirm_unverified_manual INTEGER NOT NULL DEFAULT 0 CHECK(confirm_unverified_manual IN (0,1)),
    confirm_unfiltered INTEGER NOT NULL DEFAULT 0 CHECK(confirm_unfiltered IN (0,1)),
    confirm_jit_load INTEGER NOT NULL DEFAULT 0 CHECK(confirm_jit_load IN (0,1)),
    max_output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(max_output_tokens BETWEEN 0 AND 32768)
);

INSERT INTO session_model_selections_new(session_id,backend_id,model_id,reasoning_effort,catalog_revision,local_revision,source,executable_path,executable_version,workspace_path,checked_at)
SELECT session_id,backend_id,model_id,reasoning_effort,catalog_revision,local_revision,source,executable_path,executable_version,workspace_path,checked_at FROM session_model_selections;

DROP TABLE session_model_selections;
ALTER TABLE session_model_selections_new RENAME TO session_model_selections;
