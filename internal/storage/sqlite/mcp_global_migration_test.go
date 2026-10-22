package sqlite

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

// MCP servers became global: the same server set up in two projects keeps only its most recent setup.
func TestOpenKeepsOneMCPServerPerAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, id := range []string{"playground", "dify"} {
		if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: id, Path: t.TempDir(), Profile: "ask", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	servers := []catalog.MCPServer{
		{ID: "old-bitbucket", WorkspaceID: "playground", Name: "Bitbucket", Transport: "http", URL: "https://mcp.atlassian.com/v2/mcp", CreatedAt: now.Add(-11 * 24 * time.Hour), UpdatedAt: now.Add(-11 * 24 * time.Hour)},
		{ID: "new-bitbucket", WorkspaceID: "dify", Name: "Bitbucket", Transport: "http", URL: "https://mcp.atlassian.com/v2/mcp", CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: now.Add(-24 * time.Hour)},
		{ID: "docs", WorkspaceID: "playground", Name: "Docs", Transport: "http", URL: "https://docs.example.test/mcp", CreatedAt: now, UpdatedAt: now},
	}
	for _, server := range servers {
		server.Tools, server.Args = []catalog.MCPTool{}, []string{}
		if err := store.UpsertMCPServer(t.Context(), server); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE version=65`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	listed, err := upgraded.ListMCPServers(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, server := range listed {
		ids[server.ID] = true
	}
	if len(listed) != 2 || !ids["new-bitbucket"] || !ids["docs"] {
		t.Fatalf("servers after the upgrade: %+v", listed)
	}
}
