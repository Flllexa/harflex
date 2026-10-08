package externalagent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/modelcatalog"
)

// Opt-in only: reads the installed OpenCode model list in --pure mode without
// printing model IDs, credentials, or process output.
func TestLiveOpenCodeModelCatalog(t *testing.T) {
	if os.Getenv("HARFLEX_LIVE_OPENCODE_CATALOG") != "1" {
		t.Skip("set HARFLEX_LIVE_OPENCODE_CATALOG=1 for local safe catalog readback")
	}
	nativePath := os.Getenv("HARFLEX_LIVE_OPENCODE_NATIVE")
	if nativePath == "" {
		t.Skip("set HARFLEX_LIVE_OPENCODE_NATIVE only after an isolated positive plugin-control attestation")
	}
	a := NewOpenCode(nativePath)
	cataloger := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	result, err := cataloger.QueryModels(ctx, t.TempDir())
	if err != nil || !result.Complete || (result.Status != modelcatalog.StatusComplete && result.Status != modelcatalog.StatusEmpty) {
		t.Fatalf("catalog status=%s complete=%t code=%s err=%v", result.Status, result.Complete, result.ErrorCode, err)
	}
	t.Logf("OpenCode pure catalog readback: %d models, source=%s", len(result.Models), result.Source)
}
