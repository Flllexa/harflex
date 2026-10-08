-- A project the person chose not to have read keeps that answer ('declined'), so opening it again does not ask again.
-- SQLite cannot change a CHECK, so the table is rebuilt with the same rows.
CREATE TABLE project_memories_next (
    workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('reading','ready','failed','needs_model','declined')),
    content TEXT NOT NULL DEFAULT '' CHECK (length(content) <= 65536),
    sources TEXT NOT NULL DEFAULT '[]',
    backend_id TEXT NOT NULL DEFAULT '',
    model_id TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    edited INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
INSERT INTO project_memories_next SELECT workspace_id, status, content, sources, backend_id, model_id, error_code, edited, updated_at FROM project_memories;
DROP TABLE project_memories;
ALTER TABLE project_memories_next RENAME TO project_memories;
