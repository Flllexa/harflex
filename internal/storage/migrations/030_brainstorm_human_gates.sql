-- Zero/NULL on older receipts means unknown; never infer historical revisions.
ALTER TABLE pipeline_brainstorm_requests ADD COLUMN result_run_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pipeline_brainstorm_requests ADD COLUMN result_pipeline_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pipeline_brainstorm_requests ADD COLUMN actor TEXT NOT NULL DEFAULT '';
ALTER TABLE pipeline_brainstorm_requests ADD COLUMN result_snapshot BLOB;

CREATE TABLE pipeline_brainstorm_human_receipts (
 run_id TEXT NOT NULL,
 request_id TEXT NOT NULL,
 actor TEXT NOT NULL CHECK (actor = 'local_user'),
 synthesis_version INTEGER NOT NULL,
 feedback TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '',
 choice TEXT NOT NULL DEFAULT '',
 result_revision INTEGER NOT NULL CHECK (result_revision > 0),
 result_pipeline_revision INTEGER NOT NULL CHECK (result_pipeline_revision > 0),
 created_at TEXT NOT NULL,
 PRIMARY KEY (run_id, request_id),
 FOREIGN KEY (run_id, request_id) REFERENCES pipeline_brainstorm_requests(run_id, request_id) ON DELETE RESTRICT
);
CREATE TRIGGER brainstorm_immutable_human_receipt BEFORE UPDATE ON pipeline_brainstorm_human_receipts
BEGIN SELECT RAISE(ABORT, 'immutable human receipt'); END;
