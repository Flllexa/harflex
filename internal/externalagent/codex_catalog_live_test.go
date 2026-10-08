package externalagent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/modelcatalog"
)

// Opt-in only: exercises the installed Codex app-server and current account
// without printing model names, credentials, or protocol frames.
func TestLiveCodexModelCatalog(t *testing.T) {
	if os.Getenv("HARFLEX_LIVE_CODEX_CATALOG") != "1" {
		t.Skip("set HARFLEX_LIVE_CODEX_CATALOG=1 for local app-server readback")
	}
	a := NewCodex("")
	cataloger := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	result, err := cataloger.QueryModels(ctx, t.TempDir())
	if err != nil || !result.Complete || (result.Status != modelcatalog.StatusComplete && result.Status != modelcatalog.StatusEmpty) {
		t.Fatalf("catalog status=%s complete=%t code=%s err=%v", result.Status, result.Complete, result.ErrorCode, err)
	}
	t.Logf("Codex catalog readback: %d models, source=%s", len(result.Models), result.Source)
}
