-- The largest context a conversation keeps before it compacts; 0 means the window of each model.
ALTER TABLE app_settings ADD COLUMN max_context_tokens INTEGER NOT NULL DEFAULT 0;
