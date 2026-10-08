ALTER TABLE session_delegations ADD COLUMN request_id TEXT NOT NULL DEFAULT '';
DROP INDEX session_delegations_task;
CREATE UNIQUE INDEX session_delegations_request ON session_delegations(parent_session_id, request_id) WHERE request_id != '';
