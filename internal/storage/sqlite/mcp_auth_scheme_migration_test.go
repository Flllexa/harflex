package sqlite

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

// An installed catalog has MCP servers saved before the choice of credential scheme existed. They were all sent
// as bearer tokens, so that is what they must stay.
func TestOpenGivesEveryEarlierMCPServerTheBearerScheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: "workspace-1", Path: t.TempDir(), Profile: "ask", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := catalog.MCPServer{ID: "mcp-before-the-choice", WorkspaceID: "workspace-1", Name: "Legado", Transport: "http", URL: "https://mcp.example.test/mcp", Tools: []catalog.MCPTool{}, Args: []string{}, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertMCPServer(t.Context(), server); err != nil {
		t.Fatal(err)
	}
	// Turn the catalog back into the one that existed before the column.
	for _, statement := range []string{`ALTER TABLE mcp_servers DROP COLUMN auth_scheme`, `DELETE FROM schema_migrations WHERE version=48`} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("prepare the earlier catalog: %v (%s)", err, statement)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("a catalog from before the credential scheme must open: %v", err)
	}
	defer upgraded.Close()
	stored, err := upgraded.GetMCPServer(t.Context(), server.ID)
	if err != nil || stored.AuthScheme != "bearer" || stored.Name != "Legado" {
		t.Fatalf("earlier server after the upgrade = %+v, %v; want it kept as a bearer server", stored, err)
	}
	basic := stored
	basic.ID, basic.AuthScheme = "mcp-basic", "basic"
	if err := upgraded.UpsertMCPServer(t.Context(), basic); err != nil {
		t.Fatalf("the basic scheme must be storable: %v", err)
	}
	if got, err := upgraded.GetMCPServer(t.Context(), "mcp-basic"); err != nil || got.AuthScheme != "basic" {
		t.Fatalf("basic server round trip = %+v, %v", got, err)
	}
	if _, err := upgraded.DB().ExecContext(t.Context(), `UPDATE mcp_servers SET auth_scheme='digest' WHERE id=?`, server.ID); err == nil {
		t.Fatal("the catalog accepted a credential scheme the client cannot send")
	}
	// A caller that never names a scheme (older code paths, tests) is stored as the default instead of failing.
	unset := stored
	unset.ID, unset.AuthScheme = "mcp-unset", ""
	if err := upgraded.UpsertMCPServer(t.Context(), unset); err != nil {
		t.Fatalf("an unset scheme must fall back to the default: %v", err)
	}
	if got, err := upgraded.GetMCPServer(t.Context(), "mcp-unset"); err != nil || got.AuthScheme != "bearer" {
		t.Fatalf("unset scheme stored as %+v, %v", got, err)
	}
}
