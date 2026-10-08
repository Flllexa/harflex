CREATE TABLE session_model_selections (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    backend_id TEXT NOT NULL CHECK(length(backend_id) BETWEEN 1 AND 128),
    model_id TEXT NOT NULL CHECK(length(model_id) BETWEEN 1 AND 512),
    reasoning_effort TEXT NOT NULL DEFAULT '' CHECK(length(reasoning_effort) <= 64),
    catalog_revision TEXT NOT NULL CHECK(length(catalog_revision) BETWEEN 1 AND 128),
    local_revision TEXT NOT NULL CHECK(length(local_revision) BETWEEN 1 AND 128),
    source TEXT NOT NULL CHECK(length(source) BETWEEN 1 AND 64),
    executable_path TEXT NOT NULL DEFAULT '',
    executable_version TEXT NOT NULL DEFAULT '',
    workspace_path TEXT NOT NULL,
    checked_at TEXT NOT NULL
);
