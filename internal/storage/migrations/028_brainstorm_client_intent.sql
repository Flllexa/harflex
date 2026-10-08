-- The application computes this public intent hash before catalog validation.
-- Empty legacy values cannot authorize the application's offline replay path.
ALTER TABLE pipeline_brainstorm_runs ADD COLUMN start_client_intent_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE pipeline_brainstorm_requests ADD COLUMN client_intent_hash TEXT NOT NULL DEFAULT '';
CREATE TRIGGER brainstorm_immutable_start_intent BEFORE UPDATE OF start_client_intent_hash ON pipeline_brainstorm_runs
BEGIN SELECT RAISE(ABORT, 'immutable brainstorm start intent'); END;
CREATE TRIGGER brainstorm_immutable_request BEFORE UPDATE ON pipeline_brainstorm_requests
BEGIN SELECT RAISE(ABORT, 'immutable brainstorm request'); END;
