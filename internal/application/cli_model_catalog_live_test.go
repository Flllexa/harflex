package application

import (
	"os"
	"testing"

	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/modelcatalog"
)

// Opt-in readback of the installed Codex CLI. It prints only status/count,
// never model IDs, credentials, path or raw process output.
func TestLiveCodexApplicationModelCatalog(t *testing.T) {
	if os.Getenv("HARFLEX_LIVE_CODEX_CATALOG") != "1" {
		t.Skip("set HARFLEX_LIVE_CODEX_CATALOG=1 for a local read-only check")
	}
	s, _, _ := setup(t)
	s.external["codex"] = externalagent.NewCodex("")
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryCLIModelCatalog(t.Context(), CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: "codex"})
	if err != nil || result.Status != modelcatalog.StatusComplete || !result.Complete || result.ProfileRevision == "" || len(result.Models) == 0 {
		t.Fatalf("application Codex catalog status=%s complete=%t count=%d code=%s err=%v", result.Status, result.Complete, len(result.Models), result.ErrorCode, err)
	}
	t.Logf("application Codex catalog readback: %d models, source=%s", len(result.Models), result.Source)
}
