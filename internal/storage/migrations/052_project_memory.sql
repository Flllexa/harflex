-- What the Harflex knows about a project before anyone describes it: technologies, domains, repositories and services,
-- written by the AI from the project's own documents and manifests, and editable by the person. The document phases
-- read it as context. One row per project; sources lists the files that were read.
CREATE TABLE IF NOT EXISTS project_memories (
    workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('reading','ready','failed','needs_model')),
    content TEXT NOT NULL DEFAULT '' CHECK (length(content) <= 65536),
    sources TEXT NOT NULL DEFAULT '[]',
    backend_id TEXT NOT NULL DEFAULT '',
    model_id TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    edited INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
