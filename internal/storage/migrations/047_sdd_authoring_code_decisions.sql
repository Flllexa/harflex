-- Renumbered from 038. Idempotent like 045 and 046.
CREATE TABLE IF NOT EXISTS pipeline_authoring_code_decisions (
 pipeline_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE RESTRICT,
 request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
 run_id TEXT NOT NULL REFERENCES pipeline_authoring_code_runs(id) ON DELETE RESTRICT,
 pipeline_revision INTEGER NOT NULL CHECK(pipeline_revision>0),
 action TEXT NOT NULL CHECK(action IN ('approve','request_revision')),
 patch_hash TEXT NOT NULL CHECK(length(patch_hash)=64 AND patch_hash NOT GLOB '*[^0-9a-f]*'),
 baseline_hash TEXT NOT NULL CHECK(length(baseline_hash)=64 AND baseline_hash NOT GLOB '*[^0-9a-f]*'),
 source_manifest_hash TEXT NOT NULL CHECK(length(source_manifest_hash)=64 AND source_manifest_hash NOT GLOB '*[^0-9a-f]*'),
 result_manifest_hash TEXT NOT NULL CHECK(length(result_manifest_hash)=64 AND result_manifest_hash NOT GLOB '*[^0-9a-f]*'),
 feedback TEXT NOT NULL DEFAULT '' CHECK(length(feedback)<=16384),
 intent_hash TEXT NOT NULL CHECK(length(intent_hash)=64 AND intent_hash NOT GLOB '*[^0-9a-f]*'),
 actor TEXT NOT NULL CHECK(actor='local_user'),
 result_pipeline_revision INTEGER NOT NULL CHECK(result_pipeline_revision=pipeline_revision+1),
 created_at TEXT NOT NULL,
 PRIMARY KEY(pipeline_id,request_id),
 UNIQUE(run_id),
 CHECK((action='approve' AND feedback='') OR
       (action='request_revision' AND length(trim(feedback))>0))
);

CREATE INDEX IF NOT EXISTS pipeline_authoring_code_decisions_pipeline
 ON pipeline_authoring_code_decisions(pipeline_id,created_at,request_id);

CREATE TRIGGER IF NOT EXISTS authoring_code_decisions_binding BEFORE INSERT ON pipeline_authoring_code_decisions
 WHEN NOT EXISTS (
   SELECT 1 FROM pipeline_authoring_code_runs r
   JOIN pipeline_runs p ON p.id=r.pipeline_id
   WHERE r.id=NEW.run_id AND r.pipeline_id=NEW.pipeline_id AND r.status='completed'
     AND r.pipeline_revision=NEW.pipeline_revision AND r.patch_hash=NEW.patch_hash
     AND r.manifest_hash=NEW.baseline_hash
     AND json_extract(r.source_manifest_json,'$.hash')=NEW.source_manifest_hash
     AND json_extract(r.result_manifest_json,'$.hash')=NEW.result_manifest_hash
     AND p.kind='ai_authoring' AND p.current_stage='code'
     AND p.revision=NEW.pipeline_revision
     AND json_extract(p.stage_status,'$.code')='active'
 )
BEGIN SELECT RAISE(ABORT,'authoring Code decision snapshot is stale or incomplete'); END;

CREATE TRIGGER IF NOT EXISTS authoring_code_decisions_immutable BEFORE UPDATE ON pipeline_authoring_code_decisions
BEGIN SELECT RAISE(ABORT,'immutable authoring Code decision'); END;

CREATE TRIGGER IF NOT EXISTS authoring_code_decisions_no_delete BEFORE DELETE ON pipeline_authoring_code_decisions
BEGIN SELECT RAISE(ABORT,'immutable authoring Code decision'); END;
