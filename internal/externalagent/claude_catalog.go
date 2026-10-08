package externalagent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/modelcatalog"
)

// Claude Code has no command that lists models. Its aliases always point at the newest model of each family,
// so the catalog offers them instead of full names that would go stale.
var claudeCatalogModels = []struct{ id, name string }{
	{"fable", "Fable (mais recente)"},
	{"opus", "Opus (mais recente)"},
	{"sonnet", "Sonnet (mais recente)"},
	{"haiku", "Haiku (mais recente)"},
}

func claudeCatalogResult(status modelcatalog.Status, code string) modelcatalog.Result {
	return modelcatalog.Result{BackendID: "claude", Source: "claude_cli", Destination: "Claude Code CLI", Status: status,
		ErrorCode: code, CheckedAt: time.Now().UTC(), Models: []modelcatalog.Model{}}
}

func (a *cliAdapter) queryClaudeModels(parent context.Context, cwd string) (modelcatalog.Result, error) {
	if parent == nil {
		return claudeCatalogResult(modelcatalog.StatusFailed, "catalog_workspace_unavailable"), nil
	}
	if parent.Err() != nil {
		return claudeCatalogResult(modelcatalog.StatusInterrupted, "catalog_cancelled"), nil
	}
	if !filepath.IsAbs(cwd) {
		return claudeCatalogResult(modelcatalog.StatusFailed, "catalog_workspace_unavailable"), nil
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return claudeCatalogResult(modelcatalog.StatusFailed, "catalog_workspace_unavailable"), nil
	}
	path, err := a.executable()
	if err != nil {
		return claudeCatalogResult(modelcatalog.StatusFailed, "catalog_cli_unavailable"), nil
	}
	loggedIn, err := claudeLoggedIn(parent, path, cwd)
	if parent.Err() != nil {
		return claudeCatalogResult(modelcatalog.StatusInterrupted, "catalog_cancelled"), nil
	}
	if err != nil {
		return claudeCatalogResult(modelcatalog.StatusFailed, "catalog_unavailable"), nil
	}
	if !loggedIn {
		return claudeCatalogResult(modelcatalog.StatusFailed, "catalog_not_logged_in"), nil
	}
	result := claudeCatalogResult(modelcatalog.StatusComplete, "")
	for _, model := range claudeCatalogModels {
		result.Models = append(result.Models, modelcatalog.Model{ID: model.id, DisplayName: model.name, BackendID: "claude", Source: "claude_cli", Availability: "listed",
			SupportedReasoningEfforts: append([]string(nil), claudeReasoningEfforts...)})
	}
	result.Complete = true
	return result, nil
}

// claudeLoggedIn asks the CLI whether it has credentials, without sending a prompt.
func claudeLoggedIn(parent context.Context, path, cwd string) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := exec.Command(path, "auth", "status")
	cmd.Dir = cwd
	cmd.Env = buildEnvironment("claude", os.Environ(), runtime.GOOS)
	cmd.Stdin = strings.NewReader("")
	cmd.WaitDelay = 250 * time.Millisecond
	output := &limitedBuffer{limit: 64 * 1024, onLimit: cancel}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	owned, err := ownCommand(cmd)
	if err != nil {
		return false, err
	}
	defer owned.close()
	if err := owned.start(); err != nil {
		return false, err
	}
	// The command exits non-zero when nobody is logged in; the JSON still says so.
	waitErr := owned.wait(ctx)
	if ctx.Err() != nil || output.truncated {
		return false, context.Cause(ctx)
	}
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if json.Unmarshal([]byte(output.String()), &status) != nil || status.LoggedIn == nil {
		if waitErr != nil {
			return false, waitErr
		}
		return false, io.ErrUnexpectedEOF
	}
	return *status.LoggedIn, nil
}
