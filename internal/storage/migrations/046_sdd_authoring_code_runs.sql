-- Renumbered from 037. Catalogs that ran the AI-authored Code line before the merge already
-- hold these objects with the same definitions, so every statement is idempotent.
CREATE TABLE IF NOT EXISTS pipeline_authoring_code_runs (
 id TEXT PRIMARY KEY NOT NULL,
 pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
 preparation_id TEXT NOT NULL REFERENCES pipeline_authoring_code_copy_attempts(id) ON DELETE RESTRICT,
 preparation_request_id TEXT NOT NULL,
 request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
 intent_hash TEXT NOT NULL CHECK(length(intent_hash)=64),
 workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
 pipeline_revision INTEGER NOT NULL CHECK(pipeline_revision>0),
 plan_stage_revision INTEGER NOT NULL CHECK(plan_stage_revision>=0),
 plan_artifact_version INTEGER NOT NULL CHECK(plan_artifact_version>=0),
 code_preference_revision INTEGER NOT NULL CHECK(code_preference_revision>=0),
 code_selection_hash TEXT NOT NULL CHECK(length(code_selection_hash)=64),
 manifest_hash TEXT NOT NULL CHECK(length(manifest_hash)=64),
 plan_source_hash TEXT NOT NULL CHECK(length(plan_source_hash)=64),
 private_path TEXT NOT NULL CHECK(length(private_path)>0),
 preference_snapshot_json BLOB NOT NULL CHECK(json_valid(preference_snapshot_json)),
 manifest_snapshot_json BLOB NOT NULL CHECK(json_valid(manifest_snapshot_json)),
 max_prompt_bytes INTEGER NOT NULL CHECK(max_prompt_bytes>0),
 max_output_tokens INTEGER NOT NULL CHECK(max_output_tokens BETWEEN 1 AND 24576),
 max_output_tokens_per_turn INTEGER NOT NULL CHECK(max_output_tokens_per_turn=4096),
 max_turns INTEGER NOT NULL CHECK(max_turns>0),
 max_tool_calls INTEGER NOT NULL CHECK(max_tool_calls>0),
 timeout_millis INTEGER NOT NULL CHECK(timeout_millis>0),
 session_id TEXT REFERENCES sessions(id) ON DELETE RESTRICT,
 status TEXT NOT NULL CHECK(status IN ('running','cancelling','completed','failed','cancelled','interrupted')),
 error_code TEXT NOT NULL DEFAULT '',
 usage_json BLOB CHECK(usage_json IS NULL OR json_valid(usage_json)),
 result_manifest_json BLOB CHECK(result_manifest_json IS NULL OR json_valid(result_manifest_json)),
 source_manifest_json BLOB CHECK(source_manifest_json IS NULL OR json_valid(source_manifest_json)),
 changes_json BLOB CHECK(changes_json IS NULL OR json_valid(changes_json)),
 patch_json BLOB CHECK(patch_json IS NULL OR (json_valid(patch_json) AND length(patch_json)<=67108864)),
 patch_hash TEXT CHECK(patch_hash IS NULL OR length(patch_hash)=64),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 CHECK((patch_json IS NULL)=(patch_hash IS NULL)),
 UNIQUE(pipeline_id,request_id),
 UNIQUE(preparation_id)
);

CREATE INDEX IF NOT EXISTS pipeline_authoring_code_runs_pipeline
 ON pipeline_authoring_code_runs(pipeline_id,created_at,id);
CREATE UNIQUE INDEX IF NOT EXISTS pipeline_authoring_code_runs_one_active
 ON pipeline_authoring_code_runs(pipeline_id)
 WHERE status IN ('running','cancelling');

CREATE TRIGGER IF NOT EXISTS authoring_code_runs_immutable BEFORE UPDATE OF
 id,pipeline_id,preparation_id,preparation_request_id,request_id,intent_hash,workspace_id,
 pipeline_revision,plan_stage_revision,plan_artifact_version,code_preference_revision,
 code_selection_hash,manifest_hash,plan_source_hash,private_path,preference_snapshot_json,
 manifest_snapshot_json,max_prompt_bytes,max_output_tokens,max_output_tokens_per_turn,max_turns,max_tool_calls,
 timeout_millis,created_at
 ON pipeline_authoring_code_runs
 WHEN NEW.id IS NOT OLD.id OR NEW.pipeline_id IS NOT OLD.pipeline_id OR NEW.preparation_id IS NOT OLD.preparation_id OR
      NEW.preparation_request_id IS NOT OLD.preparation_request_id OR NEW.request_id IS NOT OLD.request_id OR
      NEW.intent_hash IS NOT OLD.intent_hash OR NEW.workspace_id IS NOT OLD.workspace_id OR
      NEW.pipeline_revision IS NOT OLD.pipeline_revision OR NEW.plan_stage_revision IS NOT OLD.plan_stage_revision OR
      NEW.plan_artifact_version IS NOT OLD.plan_artifact_version OR NEW.code_preference_revision IS NOT OLD.code_preference_revision OR
      NEW.code_selection_hash IS NOT OLD.code_selection_hash OR NEW.manifest_hash IS NOT OLD.manifest_hash OR
      NEW.plan_source_hash IS NOT OLD.plan_source_hash OR NEW.private_path IS NOT OLD.private_path OR
      NEW.preference_snapshot_json IS NOT OLD.preference_snapshot_json OR NEW.manifest_snapshot_json IS NOT OLD.manifest_snapshot_json OR
      NEW.max_prompt_bytes IS NOT OLD.max_prompt_bytes OR NEW.max_output_tokens IS NOT OLD.max_output_tokens OR
      NEW.max_output_tokens_per_turn IS NOT OLD.max_output_tokens_per_turn OR
      NEW.max_turns IS NOT OLD.max_turns OR NEW.max_tool_calls IS NOT OLD.max_tool_calls OR
      NEW.timeout_millis IS NOT OLD.timeout_millis OR NEW.created_at IS NOT OLD.created_at
BEGIN SELECT RAISE(ABORT,'immutable authoring Code run snapshot'); END;

CREATE TRIGGER IF NOT EXISTS authoring_code_runs_status_transition BEFORE UPDATE OF status
 ON pipeline_authoring_code_runs
 WHEN NEW.status != OLD.status AND NOT (
   OLD.status='running' AND NEW.status IN ('cancelling','completed','failed','cancelled','interrupted') OR
   OLD.status='cancelling' AND NEW.status IN ('completed','failed','cancelled','interrupted')
 )
BEGIN SELECT RAISE(ABORT,'invalid authoring Code run status transition'); END;

CREATE TRIGGER IF NOT EXISTS authoring_code_runs_terminal_evidence BEFORE UPDATE OF
 session_id,error_code,usage_json,result_manifest_json,source_manifest_json,changes_json,patch_json,patch_hash,updated_at
 ON pipeline_authoring_code_runs
 WHEN OLD.status IN ('completed','failed','cancelled','interrupted') AND (
      NEW.session_id IS NOT OLD.session_id OR NEW.error_code IS NOT OLD.error_code OR
      NEW.usage_json IS NOT OLD.usage_json OR NEW.result_manifest_json IS NOT OLD.result_manifest_json OR
      NEW.source_manifest_json IS NOT OLD.source_manifest_json OR NEW.changes_json IS NOT OLD.changes_json OR
      NEW.patch_json IS NOT OLD.patch_json OR NEW.patch_hash IS NOT OLD.patch_hash OR NEW.updated_at IS NOT OLD.updated_at
 )
BEGIN SELECT RAISE(ABORT,'terminal authoring Code evidence is immutable'); END;

CREATE TRIGGER IF NOT EXISTS authoring_code_runs_completed_patch BEFORE UPDATE OF
 status,result_manifest_json,source_manifest_json,patch_json,patch_hash
 ON pipeline_authoring_code_runs
 WHEN NEW.status='completed' AND (
      NEW.result_manifest_json IS NULL OR NEW.source_manifest_json IS NULL OR
      NEW.patch_json IS NULL OR NEW.patch_hash IS NULL OR
      json_extract(NEW.source_manifest_json,'$.hash') IS NOT NEW.manifest_hash OR
      json_extract(NEW.patch_json,'$.baselineHash') IS NOT NEW.manifest_hash OR
      json_extract(NEW.patch_json,'$.sourceManifestHash') IS NOT NEW.manifest_hash OR
      json_extract(NEW.patch_json,'$.resultManifestHash') IS NOT json_extract(NEW.result_manifest_json,'$.hash')
 )
BEGIN SELECT RAISE(ABORT,'completed authoring Code run requires bound patch evidence'); END;

CREATE TRIGGER IF NOT EXISTS authoring_code_runs_no_delete BEFORE DELETE ON pipeline_authoring_code_runs
BEGIN SELECT RAISE(ABORT,'immutable authoring Code run'); END;
