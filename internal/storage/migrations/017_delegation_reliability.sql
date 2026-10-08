ALTER TABLE session_delegations ADD COLUMN task_prompt TEXT NOT NULL DEFAULT '';
ALTER TABLE session_delegations ADD COLUMN task_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE session_delegations ADD COLUMN prompt_count INTEGER NOT NULL DEFAULT 0 CHECK(prompt_count >= 0);
CREATE UNIQUE INDEX session_delegations_task ON session_delegations(parent_session_id, agent_id, task_hash) WHERE task_hash != '';
