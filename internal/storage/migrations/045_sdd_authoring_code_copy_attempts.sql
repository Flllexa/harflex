-- Renumbered from 036. Catalogs that ran the AI-authored Code line before the merge already
-- hold these objects with the same definitions, so every statement is idempotent.
CREATE TABLE IF NOT EXISTS pipeline_authoring_code_copy_attempts (
 id TEXT PRIMARY KEY NOT NULL,
 pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
 workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
 request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
 intent_hash TEXT NOT NULL CHECK(length(intent_hash)=64),
 pipeline_revision INTEGER NOT NULL CHECK(pipeline_revision>0),
 code_preference_revision INTEGER NOT NULL CHECK(code_preference_revision>=0),
 source_path TEXT NOT NULL CHECK(length(source_path)>0),
 private_path TEXT NOT NULL CHECK(length(private_path)>0),
 manifest_hash TEXT NOT NULL CHECK(length(manifest_hash)=64),
 preference_snapshot_json BLOB NOT NULL CHECK(json_valid(preference_snapshot_json)),
 manifest_snapshot_json BLOB NOT NULL CHECK(json_valid(manifest_snapshot_json)),
 status TEXT NOT NULL CHECK(status IN ('preparing','prepared','interrupted','stale','failed')),
 error_code TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(pipeline_id,request_id)
);

CREATE INDEX IF NOT EXISTS pipeline_authoring_code_copy_attempts_pipeline
 ON pipeline_authoring_code_copy_attempts(pipeline_id,created_at,id);
CREATE UNIQUE INDEX IF NOT EXISTS pipeline_authoring_code_copy_attempts_one_active
 ON pipeline_authoring_code_copy_attempts(pipeline_id)
 WHERE status='preparing';

CREATE TRIGGER IF NOT EXISTS authoring_code_copy_attempts_immutable BEFORE UPDATE OF
 id,pipeline_id,workspace_id,request_id,intent_hash,pipeline_revision,code_preference_revision,
 source_path,private_path,manifest_hash,preference_snapshot_json,manifest_snapshot_json,created_at
 ON pipeline_authoring_code_copy_attempts
 WHEN NEW.id IS NOT OLD.id OR NEW.pipeline_id IS NOT OLD.pipeline_id OR NEW.workspace_id IS NOT OLD.workspace_id OR
      NEW.request_id IS NOT OLD.request_id OR NEW.intent_hash IS NOT OLD.intent_hash OR
      NEW.pipeline_revision IS NOT OLD.pipeline_revision OR NEW.code_preference_revision IS NOT OLD.code_preference_revision OR
      NEW.source_path IS NOT OLD.source_path OR NEW.private_path IS NOT OLD.private_path OR
      NEW.manifest_hash IS NOT OLD.manifest_hash OR NEW.preference_snapshot_json IS NOT OLD.preference_snapshot_json OR
      NEW.manifest_snapshot_json IS NOT OLD.manifest_snapshot_json OR NEW.created_at IS NOT OLD.created_at
BEGIN SELECT RAISE(ABORT,'immutable authoring Code copy receipt'); END;

CREATE TRIGGER IF NOT EXISTS authoring_code_copy_attempts_status_transition BEFORE UPDATE OF status
 ON pipeline_authoring_code_copy_attempts
 WHEN NEW.status != OLD.status AND NOT (
   OLD.status='preparing' AND NEW.status IN ('prepared','interrupted','stale','failed')
 )
BEGIN SELECT RAISE(ABORT,'invalid authoring Code copy status transition'); END;

CREATE TRIGGER IF NOT EXISTS authoring_code_copy_attempts_no_delete BEFORE DELETE ON pipeline_authoring_code_copy_attempts
BEGIN SELECT RAISE(ABORT,'immutable authoring Code copy receipt'); END;
