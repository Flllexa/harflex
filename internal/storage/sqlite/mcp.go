package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func encodeMCP(item catalog.MCPServer) ([]byte, []byte, error) {
	args, err := json.Marshal(item.Args)
	if err != nil {
		return nil, nil, fmt.Errorf("encode MCP args: %w", err)
	}
	tools, err := json.Marshal(item.Tools)
	if err != nil {
		return nil, nil, fmt.Errorf("encode MCP tools: %w", err)
	}
	return args, tools, nil
}

// mcpAuthScheme keeps rows written before the column existed, and callers that never set it, on the bearer default.
func mcpAuthScheme(scheme string) string {
	if scheme == "" {
		return "bearer"
	}
	return scheme
}

func upsertMCP(ctx context.Context, executor catalogExecutor, item catalog.MCPServer) error {
	args, tools, err := encodeMCP(item)
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, `INSERT INTO mcp_servers(id,workspace_id,name,transport,command_path,args,endpoint_url,token_env_var,auth_scheme,enabled,tools,credential_provider,credential_account,created_at,updated_at)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,transport=excluded.transport,command_path=excluded.command_path,args=excluded.args,endpoint_url=excluded.endpoint_url,token_env_var=excluded.token_env_var,auth_scheme=excluded.auth_scheme,enabled=excluded.enabled,tools=excluded.tools,credential_provider=excluded.credential_provider,credential_account=excluded.credential_account,updated_at=excluded.updated_at`,
		item.ID, item.WorkspaceID, item.Name, item.Transport, item.Command, args, item.URL, item.TokenEnvVar, mcpAuthScheme(item.AuthScheme), item.Enabled, tools, item.CredentialProvider, item.CredentialAccount, formatCatalogTime(item.CreatedAt), formatCatalogTime(item.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upsert MCP server: %w", err)
	}
	return nil
}

func (s *Store) UpsertMCPServer(ctx context.Context, item catalog.MCPServer) error {
	return upsertMCP(ctx, s.db, item)
}

func (s *Store) PublishMCPServer(ctx context.Context, item catalog.MCPServer) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin MCP publication: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertMCP(ctx, tx, item); err != nil {
		return err
	}
	if item.CredentialProvider != "" && item.CredentialAccount != "" {
		ref := catalog.CredentialReference{ProfileID: item.ID, Provider: item.CredentialProvider, Account: item.CredentialAccount, CreatedAt: item.UpdatedAt}
		if err := addCredentialReference(ctx, tx, ref); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit MCP publication: %w", err)
	}
	return nil
}

type mcpScanner interface{ Scan(...any) error }

func scanMCP(row mcpScanner) (catalog.MCPServer, error) {
	var item catalog.MCPServer
	var args, tools []byte
	var created, updated string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.Transport, &item.Command, &args, &item.URL, &item.TokenEnvVar, &item.AuthScheme, &item.Enabled, &tools, &item.CredentialProvider, &item.CredentialAccount, &created, &updated); err != nil {
		return catalog.MCPServer{}, err
	}
	if err := json.Unmarshal(args, &item.Args); err != nil {
		return catalog.MCPServer{}, fmt.Errorf("decode MCP args: %w", err)
	}
	if err := json.Unmarshal(tools, &item.Tools); err != nil {
		return catalog.MCPServer{}, fmt.Errorf("decode MCP tools: %w", err)
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return catalog.MCPServer{}, fmt.Errorf("parse MCP created timestamp: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return catalog.MCPServer{}, fmt.Errorf("parse MCP updated timestamp: %w", err)
	}
	return item, nil
}

const mcpColumns = "id,workspace_id,name,transport,command_path,args,endpoint_url,token_env_var,auth_scheme,enabled,tools,credential_provider,credential_account,created_at,updated_at"

func (s *Store) GetMCPServer(ctx context.Context, id string) (catalog.MCPServer, error) {
	item, err := scanMCP(s.db.QueryRowContext(ctx, "SELECT "+mcpColumns+" FROM mcp_servers WHERE id=?", id))
	if err != nil {
		return catalog.MCPServer{}, fmt.Errorf("get MCP server: %w", err)
	}
	return item, nil
}

func (s *Store) ListMCPServers(ctx context.Context, workspaceID string) ([]catalog.MCPServer, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+mcpColumns+" FROM mcp_servers WHERE workspace_id=? ORDER BY updated_at DESC,id", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list MCP servers: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.MCPServer, 0)
	for rows.Next() {
		item, err := scanMCP(rows)
		if err != nil {
			return nil, fmt.Errorf("scan MCP server: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate MCP servers: %w", err)
	}
	return items, nil
}
