ALTER TABLE workspaces ADD COLUMN last_opened_at TEXT NOT NULL DEFAULT '';
UPDATE workspaces SET last_opened_at = created_at WHERE last_opened_at = '';
CREATE INDEX workspaces_last_opened ON workspaces(last_opened_at DESC, id);
