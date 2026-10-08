-- A project the person archived stays in the catalog with everything it owns; it only leaves the Projects list.
-- Empty means active. Opening the same folder again clears it.
ALTER TABLE workspaces ADD COLUMN archived_at TEXT NOT NULL DEFAULT '';
