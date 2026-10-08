CREATE TABLE mcp_servers (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    name TEXT NOT NULL,
    transport TEXT NOT NULL,
    command_path TEXT NOT NULL,
    args BLOB NOT NULL,
    endpoint_url TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0,1)),
    tools BLOB NOT NULL,
    credential_provider TEXT NOT NULL DEFAULT '',
    credential_account TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX mcp_servers_workspace_updated ON mcp_servers(workspace_id, updated_at DESC);
