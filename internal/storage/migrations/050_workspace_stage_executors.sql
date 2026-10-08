-- Who works on each phase of a project's pipelines: a provider profile or a CLI backend, and optionally a model.
-- A phase with no row uses the default of Settings. The model is an identifier only; the catalog is asked again when
-- the phase runs, so a model that left it is noticed then.
CREATE TABLE IF NOT EXISTS workspace_stage_executors (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    stage TEXT NOT NULL CHECK (stage IN ('discovery','spec','plan','code','eval','prs')),
    backend_id TEXT NOT NULL CHECK (length(backend_id) BETWEEN 1 AND 128),
    model_id TEXT NOT NULL DEFAULT '' CHECK (length(model_id) <= 512),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (workspace_id, stage)
);
