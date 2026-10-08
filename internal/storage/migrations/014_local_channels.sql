CREATE TABLE local_channels (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    name TEXT NOT NULL,
    folder TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('ready','error')),
    last_error TEXT NOT NULL DEFAULT '',
    inbox_error TEXT NOT NULL DEFAULT '',
    outbox_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(workspace_id, folder)
);
CREATE INDEX local_channels_workspace ON local_channels(workspace_id, updated_at DESC);

CREATE TABLE channel_messages (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES local_channels(id),
    request_id TEXT NOT NULL DEFAULT '',
    direction TEXT NOT NULL CHECK(direction IN ('incoming','outgoing')),
    file_name TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    content TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('received','pending','sent','failed')),
    error_code TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(channel_id, direction, file_name, content_hash)
);
CREATE UNIQUE INDEX channel_outgoing_request ON channel_messages(channel_id, request_id) WHERE request_id != '';
CREATE INDEX channel_messages_history ON channel_messages(channel_id, created_at DESC, id);

CREATE TABLE channel_events (
    id INTEGER PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES local_channels(id),
    message_id TEXT REFERENCES channel_messages(id),
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
