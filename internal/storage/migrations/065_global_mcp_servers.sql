-- MCP servers, skills and workflows are global now: what each project set up serves every project. The same MCP
-- server set up in more than one project (same transport, address, command and arguments) keeps its most recent
-- setup, whose credential is the one in use; workspace_id stays as the project it was set up in.
DELETE FROM mcp_servers WHERE id IN (
  SELECT id FROM (
    SELECT id, row_number() OVER (PARTITION BY transport, endpoint_url, command_path, CAST(args AS TEXT) ORDER BY updated_at DESC, id) AS position
    FROM mcp_servers
  ) WHERE position > 1
);
