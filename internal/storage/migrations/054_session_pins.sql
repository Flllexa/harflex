-- Conversations the person pinned to the top of the Casual sidebar.
CREATE TABLE IF NOT EXISTS session_pins (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    pinned_at TEXT NOT NULL
);
