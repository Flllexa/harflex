package application

import (
	"testing"

	"github.com/persioflexa/harflex/internal/secrets"
)

func TestCredentialProbeReportsReadabilityWithoutExposingValues(t *testing.T) {
	s, db, vault := setup(t)
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	result, err := s.ProbeCredentialStore("")
	if err != nil || result.Status != "ready" || result.Checked != 1 || result.Missing != 0 {
		t.Fatalf("unexpected credential probe: %+v %v", result, err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Docs", Transport: "http", URL: "http://127.0.0.1:3333/mcp", Token: "synthetic-mcp-token"}); err != nil {
		t.Fatal(err)
	}
	result, err = s.ProbeCredentialStore(workspace.ID)
	if err != nil || result.Status != "ready" || result.Checked != 2 || result.Missing != 0 {
		t.Fatalf("MCP credential omitted from probe: %+v %v", result, err)
	}
	profile, err := db.GetProviderProfile(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Delete(t.Context(), secrets.Reference{Provider: profile.CredentialProvider, Account: profile.CredentialAccount}); err != nil {
		t.Fatal(err)
	}
	result, err = s.ProbeCredentialStore("")
	if err != nil || result.Status != "degraded" || result.Checked != 1 || result.Missing != 1 {
		t.Fatalf("missing credential not detected: %+v %v", result, err)
	}
}
