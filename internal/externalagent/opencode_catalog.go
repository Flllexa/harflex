package externalagent

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/persioflexa/harflex/internal/modelcatalog"
)

func openCodeCatalogResult(status modelcatalog.Status, code string) modelcatalog.Result {
	return modelcatalog.Result{BackendID: "opencode", Source: "opencode_cli", Destination: "OpenCode CLI", Status: status,
		ErrorCode: code, CheckedAt: time.Now().UTC(), Models: []modelcatalog.Model{}}
}

func (a *cliAdapter) queryOpenCodeModels(parent context.Context, cwd string) (modelcatalog.Result, error) {
	if a.path == "" {
		return openCodeCatalogResult(modelcatalog.StatusUnsupported, "catalog_attestation_required"), nil
	}
	if parent == nil {
		return openCodeCatalogResult(modelcatalog.StatusFailed, "catalog_workspace_unavailable"), nil
	}
	if parent.Err() != nil {
		return openCodeCatalogResult(modelcatalog.StatusInterrupted, "catalog_cancelled"), nil
	}
	if !filepath.IsAbs(cwd) {
		return openCodeCatalogResult(modelcatalog.StatusFailed, "catalog_workspace_unavailable"), nil
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return openCodeCatalogResult(modelcatalog.StatusFailed, "catalog_workspace_unavailable"), nil
	}
	path, err := a.executable()
	if err != nil {
		return openCodeCatalogResult(modelcatalog.StatusFailed, "catalog_cli_unavailable"), nil
	}
	if err := verifyOpenCodePure(parent, path); err != nil {
		if parent.Err() != nil {
			return openCodeCatalogResult(modelcatalog.StatusInterrupted, "catalog_cancelled"), nil
		}
		return openCodeCatalogResult(modelcatalog.StatusUnsupported, "catalog_pure_unavailable"), nil
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := exec.Command(path, "--pure", "models")
	cmd.Dir = cwd
	cmd.Env = buildEnvironment("opencode", os.Environ(), runtime.GOOS)
	cmd.Stdin = strings.NewReader("")
	cmd.WaitDelay = 250 * time.Millisecond
	output := &limitedBuffer{limit: int(modelcatalog.MaxBytes), onLimit: cancel}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	owned, err := ownCommand(cmd)
	if err != nil {
		return openCodeCatalogResult(modelcatalog.StatusFailed, "catalog_cli_unavailable"), nil
	}
	defer owned.close()
	if err := owned.start(); err != nil {
		return openCodeCatalogResult(modelcatalog.StatusFailed, "catalog_cli_unavailable"), nil
	}
	err = owned.wait(ctx)
	if output.truncated {
		return openCodeCatalogResult(modelcatalog.StatusPartial, "catalog_limit"), nil
	}
	if ctx.Err() != nil {
		return openCodeCatalogResult(modelcatalog.StatusInterrupted, "catalog_cancelled"), nil
	}
	if err != nil {
		return openCodeCatalogResult(modelcatalog.StatusFailed, "catalog_unavailable"), nil
	}
	result := openCodeCatalogResult(modelcatalog.StatusComplete, "")
	scanner := bufio.NewScanner(strings.NewReader(output.String()))
	scanner.Buffer(make([]byte, 4096), int(modelcatalog.MaxBytes)+2)
	entries := 0
	for scanner.Scan() {
		id := strings.TrimSpace(scanner.Text())
		if id == "" {
			continue
		}
		entries++
		if entries > modelcatalog.MaxModels {
			return openCodeCatalogResult(modelcatalog.StatusPartial, "catalog_limit"), nil
		}
		separator := strings.IndexByte(id, '/')
		if separator < 1 || separator == len(id)-1 || strings.HasPrefix(id, "-") ||
			!validCatalogText(id, 512) || strings.IndexFunc(id, unicode.IsSpace) >= 0 {
			return openCodeCatalogResult(modelcatalog.StatusFailed, "catalog_unavailable"), nil
		}
		result.Models = append(result.Models, modelcatalog.Model{ID: id, DisplayName: id, BackendID: "opencode", Source: "opencode_cli", Availability: "listed"})
	}
	if scanner.Err() != nil {
		return openCodeCatalogResult(modelcatalog.StatusPartial, "catalog_limit"), nil
	}
	if len(result.Models) == 0 {
		result.Status = modelcatalog.StatusEmpty
	}
	result.Complete = true
	return result, nil
}
