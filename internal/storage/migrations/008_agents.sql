CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    instructions TEXT NOT NULL,
    backend_id TEXT NOT NULL,
    allowed_tools BLOB NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX agents_updated ON agents(updated_at DESC, id);

CREATE TABLE session_agent_snapshots (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    instructions TEXT NOT NULL,
    allowed_tools BLOB NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE session_delegations (
    id TEXT PRIMARY KEY,
    parent_session_id TEXT NOT NULL REFERENCES sessions(id),
    child_session_id TEXT NOT NULL UNIQUE REFERENCES sessions(id),
    agent_id TEXT NOT NULL,
    depth INTEGER NOT NULL CHECK (depth BETWEEN 1 AND 3),
    created_at TEXT NOT NULL
);
CREATE INDEX session_delegations_parent ON session_delegations(parent_session_id, created_at);
