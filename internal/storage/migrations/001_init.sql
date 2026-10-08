CREATE TABLE streams (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE events (
    id TEXT PRIMARY KEY,
    stream_id TEXT NOT NULL REFERENCES streams(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    type TEXT NOT NULL,
    data BLOB NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (stream_id, sequence)
);

CREATE INDEX events_stream_sequence ON events(stream_id, sequence);

CREATE TABLE workspaces (
    id TEXT PRIMARY KEY,
    path TEXT NOT NULL UNIQUE,
    profile TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE provider_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    base_url TEXT NOT NULL,
    model TEXT NOT NULL,
    credential_provider TEXT NOT NULL,
    credential_account TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    backend_id TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
