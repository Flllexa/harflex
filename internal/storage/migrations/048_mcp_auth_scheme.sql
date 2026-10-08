-- How an HTTP MCP server wants its stored credential presented: 'bearer' (the token itself, what every
-- server saved so far used) or 'basic' ("user:secret", for example an Atlassian e-mail and API token).
ALTER TABLE mcp_servers ADD COLUMN auth_scheme TEXT NOT NULL DEFAULT 'bearer' CHECK (auth_scheme IN ('bearer','basic'));
