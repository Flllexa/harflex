-- A local test: the Code applied to the project's own folder for the person to try before approving, with what undo needs.
CREATE TABLE pipeline_local_tests (
  id TEXT PRIMARY KEY,
  pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL,
  artifact_version INTEGER NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  backup_path TEXT NOT NULL,
  applied TEXT NOT NULL CHECK(json_valid(applied)),
  services TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(services)),
  status TEXT NOT NULL CHECK(status IN ('applied','undone')),
  created_at TEXT NOT NULL,
  undone_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX pipeline_local_tests_one_per_workspace ON pipeline_local_tests(workspace_id) WHERE status = 'applied';
CREATE INDEX pipeline_local_tests_by_pipeline ON pipeline_local_tests(pipeline_id, created_at);
