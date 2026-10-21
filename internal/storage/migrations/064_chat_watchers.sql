-- A chat watcher: a check Harflex runs in the background for a conversation (a command until it exits 0, or a
-- timer) and that brings the conversation back with a message when it fires.
CREATE TABLE chat_watchers (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL,
  title TEXT NOT NULL,
  command TEXT NOT NULL DEFAULT '',
  interval_seconds INTEGER NOT NULL DEFAULT 0,
  deadline_at TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('watching','fired','expired','failed','stopped')),
  reason TEXT NOT NULL DEFAULT '',
  last_output TEXT NOT NULL DEFAULT '',
  last_exit_code INTEGER NOT NULL DEFAULT 0,
  checks INTEGER NOT NULL DEFAULT 0,
  failures INTEGER NOT NULL DEFAULT 0,
  last_check_at TEXT NOT NULL DEFAULT '',
  next_check_at TEXT NOT NULL,
  delivered INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  ended_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX chat_watchers_due ON chat_watchers(status, next_check_at);
CREATE INDEX chat_watchers_workspace ON chat_watchers(workspace_id, created_at DESC);
CREATE INDEX chat_watchers_session ON chat_watchers(session_id, status);
