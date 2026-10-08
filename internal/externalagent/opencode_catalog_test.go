package externalagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/modelcatalog"
)

func runOpenCodeCatalogHelper(mode string) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Fprintln(os.Stdout, "1.14.27")
		return
	}
	if config := os.Getenv("OPENCODE_CONFIG_DIR"); config != "" {
		cwd, _ := os.Getwd()
		if !openCodeProbeFixturesPresent(config, cwd) {
			return
		}
		if mode == "opencode-catalog-probe-fail" && len(os.Args) > 1 && os.Args[1] == "--pure" {
			_ = os.WriteFile(filepath.Join(cwd, "probe-executed"), []byte("unsafe"), 0600)
		}
	}
	if len(os.Args) != 3 || os.Args[1] != "--pure" || os.Args[2] != "models" ||
		os.Getenv("HARFLEX_OTHER_API_KEY") != "" || os.Getenv("OPENCODE_DISABLE_AUTOUPDATE") != "1" ||
		os.Getenv("OPENCODE_DISABLE_LSP_DOWNLOAD") != "1" || os.Getenv("OPENCODE_DISABLE_MODELS_FETCH") != "1" {
		fmt.Fprintln(os.Stderr, "unsafe OpenCode catalog invocation")
		return
	}
	switch mode {
	case "opencode-catalog-lines":
		fmt.Fprintln(os.Stdout, "openai/gpt-exact")
		fmt.Fprintln(os.Stdout, "local/qwen:latest")
	case "opencode-catalog-invalid":
		fmt.Fprintln(os.Stdout, "not-a-provider-model")
	case "opencode-catalog-many":
		for range modelcatalog.MaxModels + 1 {
			fmt.Fprintln(os.Stdout, "provider/"+strings.Repeat("x", 20))
		}
	case "opencode-catalog-overflow":
		if os.Getenv("OPENCODE_CONFIG_DIR") != "" {
			fmt.Fprintln(os.Stdout, "provider/model")
		} else {
			fmt.Fprint(os.Stdout, strings.Repeat("x", int(modelcatalog.MaxBytes)+4096))
			time.Sleep(time.Minute)
		}
	case "opencode-catalog-hang":
		time.Sleep(time.Minute)
	case "opencode-catalog-probe-fail":
		fmt.Fprintln(os.Stdout, "openai/gpt-exact")
	}
}

func openCodeProbeFixturesPresent(config, cwd string) bool {
	for _, path := range []string{
		filepath.Join(os.Getenv("HOME"), ".config", "opencode", "plugins", "harflex-global-probe.js"),
		filepath.Join(config, "plugins", "harflex-custom-probe.js"),
		filepath.Join(cwd, ".opencode", "plugins", "harflex-probe.js"),
	} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

func TestOpenCodeCatalogRejectsFailedPurePluginProbe(t *testing.T) {
	a := NewOpenCode(helper(t, "opencode-catalog-probe-fail"))
	cataloger := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	})
	result, err := cataloger.QueryModels(t.Context(), t.TempDir())
	if err != nil || result.Status != modelcatalog.StatusUnsupported || result.ErrorCode != "catalog_pure_unavailable" || result.Complete {
		t.Fatal(result, err)
	}
}

func TestOpenCodeRunRejectsFailedPurePluginProbe(t *testing.T) {
	a := NewOpenCode(helper(t, "opencode-catalog-probe-fail"))
	events, err := collect(t, t.Context(), a, Request{CWD: t.TempDir(), Prompt: "private prompt"})
	if len(events) != 0 || !errors.Is(err, ErrOpenCodePureUnavailable) {
		t.Fatalf("unsafe OpenCode run admitted: events=%d err=%v", len(events), err)
	}
}

func TestOpenCodeRejectsExecutableShellWrapperBeforeItCanReloadSecrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell wrapper fixture")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "wrapper-executed")
	path := filepath.Join(root, "opencode")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf unsafe > '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	a := NewOpenCode(path)
	if a.Detect().Available {
		t.Fatal("shell wrapper detected as a safe OpenCode executable")
	}
	if events, err := collect(t, t.Context(), a, Request{CWD: root, Prompt: "go"}); len(events) != 0 || err == nil {
		t.Fatalf("shell wrapper run admitted: %d %v", len(events), err)
	}
	result, err := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	}).QueryModels(t.Context(), root)
	if err != nil || result.ErrorCode != "catalog_cli_unavailable" {
		t.Fatal(result, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shell wrapper was executed: %v", err)
	}
}

func TestDefaultOpenCodeFailsClosedWithoutIsolatedPluginAttestation(t *testing.T) {
	a := NewOpenCode("")
	if a.Detect().Available {
		t.Fatal("unattested OpenCode advertised as available")
	}
	result, err := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	}).QueryModels(t.Context(), t.TempDir())
	if err != nil || result.Status != modelcatalog.StatusUnsupported || result.ErrorCode != "catalog_attestation_required" {
		t.Fatal(result, err)
	}
	events, err := collect(t, t.Context(), a, Request{CWD: t.TempDir(), Prompt: "go"})
	if len(events) != 0 || !errors.Is(err, ErrOpenCodePureUnavailable) {
		t.Fatal(events, err)
	}
}

func TestOpenCodeCatalogListsExactIDsWithoutInventedEffort(t *testing.T) {
	t.Setenv("HARFLEX_OTHER_API_KEY", "secret-canary")
	a := NewOpenCode(helper(t, "opencode-catalog-lines"))
	cataloger := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	})
	result, err := cataloger.QueryModels(t.Context(), t.TempDir())
	if err != nil || result.Status != modelcatalog.StatusComplete || !result.Complete || len(result.Models) != 2 ||
		result.Models[0].ID != "openai/gpt-exact" || result.Models[1].ID != "local/qwen:latest" ||
		result.Source != "opencode_cli" || len(result.Models[0].SupportedReasoningEfforts) != 0 || result.Models[0].DefaultReasoningEffort != "" {
		t.Fatal(result, err)
	}
}

func TestOpenCodeCatalogFailsClosedForInvalidOutputAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		wantStatus modelcatalog.Status
	}{
		{"opencode-catalog-invalid", modelcatalog.StatusFailed},
		{"opencode-catalog-hang", modelcatalog.StatusInterrupted},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
			defer cancel()
			a := NewOpenCode(helper(t, tc.mode))
			cataloger := a.(interface {
				QueryModels(context.Context, string) (modelcatalog.Result, error)
			})
			result, err := cataloger.QueryModels(ctx, t.TempDir())
			if err != nil || result.Status != tc.wantStatus || result.Complete {
				t.Fatal(result, err)
			}
		})
	}
}

func TestOpenCodeCatalogStopsOverflowingProcessWithoutWaitingForTimeout(t *testing.T) {
	a := NewOpenCode(helper(t, "opencode-catalog-overflow"))
	start := time.Now()
	result, err := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	}).QueryModels(t.Context(), t.TempDir())
	if err != nil || result.Status != modelcatalog.StatusPartial || result.ErrorCode != "catalog_limit" || result.Complete || time.Since(start) > 4*time.Second {
		t.Fatal(result, err, time.Since(start))
	}
}
