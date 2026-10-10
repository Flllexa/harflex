-- The chats that coordinated a work before the current one (a conversation that moved to another model keeps the work's chat).
ALTER TABLE pipeline_runs ADD COLUMN coordinator_previous TEXT NOT NULL DEFAULT '[]';
