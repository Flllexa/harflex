package externalagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/modelcatalog"
)

func runCodexCatalogHelper(mode string) {
	if os.Getenv("HARFLEX_OTHER_API_KEY") != "" {
		fmt.Fprintln(os.Stderr, "catalog inherited unrelated credential")
		return
	}
	if len(os.Args) < 3 || os.Args[1] != "app-server" || os.Args[2] != "--stdio" {
		fmt.Fprintln(os.Stderr, "wrong invocation")
		return
	}
	initialized := false
	acknowledged := false
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Cursor        *string `json:"cursor"`
				Limit         int     `json:"limit"`
				IncludeHidden bool    `json:"includeHidden"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			fmt.Fprintln(os.Stdout, `{"id":0,"error":{"code":-1}}`)
			return
		}
		switch request.Method {
		case "initialize":
			initialized = true
			fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{}}\n", request.ID)
		case "initialized":
			acknowledged = initialized
		case "model/list":
			if !acknowledged || request.Params.Limit < 1 || request.Params.IncludeHidden {
				fmt.Fprintf(os.Stdout, "{\"id\":%d,\"error\":{\"code\":-2}}\n", request.ID)
				return
			}
			if mode == "codex-catalog-hang" {
				time.Sleep(time.Minute)
				return
			}
			if mode == "codex-catalog-request" {
				fmt.Fprintln(os.Stdout, `{"id":77,"method":"tool/requestUserInput","params":{"question":"unsafe"}}`)
				time.Sleep(time.Minute)
				return
			}
			if mode == "codex-catalog-large" {
				fmt.Fprintln(os.Stdout, strings.Repeat("x", int(modelcatalog.MaxBytes)+2))
				return
			}
			if request.Params.Cursor == nil {
				fmt.Fprintln(os.Stdout, `{"method":"account/rateLimits/updated","params":{}}`)
				fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{\"data\":[{\"id\":\"catalog-a\",\"model\":\"runtime-a\",\"displayName\":\"Model A\",\"hidden\":false,\"defaultReasoningEffort\":\"high\",\"supportedReasoningEfforts\":[{\"reasoningEffort\":\"low\",\"description\":\"fast\"},{\"reasoningEffort\":\"high\",\"description\":\"deep\"}]}],\"nextCursor\":\"next\"}}\n", request.ID)
			} else if *request.Params.Cursor == "next" {
				fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{\"data\":[{\"id\":\"catalog-b\",\"model\":\"runtime-b\",\"displayName\":\"Model B\",\"hidden\":false,\"defaultReasoningEffort\":\"medium\",\"supportedReasoningEfforts\":[{\"reasoningEffort\":\"medium\",\"description\":\"default\"}]}],\"nextCursor\":null}}\n", request.ID)
			} else {
				fmt.Fprintf(os.Stdout, "{\"id\":%d,\"error\":{\"code\":-3}}\n", request.ID)
			}
		case "config/read":
			if mode == "codex-catalog-context-missing" {
				fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{}}\n", request.ID)
			} else {
				fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{\"config\":{\"model_provider\":\"openai\"},\"origins\":{}}}\n", request.ID)
			}
		case "account/read":
			if mode == "codex-catalog-no-account" {
				fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{\"account\":null,\"requiresOpenaiAuth\":false}}\n", request.ID)
			} else {
				fmt.Fprintf(os.Stdout, "{\"id\":%d,\"result\":{\"account\":{\"type\":\"chatgpt\",\"email\":\"synthetic@example.test\"},\"requiresOpenaiAuth\":true}}\n", request.ID)
			}
		default:
			fmt.Fprintln(os.Stderr, "unexpected method "+request.Method)
			return
		}
	}
}

func TestCodexCatalogUsesRuntimeModelSlugAndEfforts(t *testing.T) {
	t.Setenv("HARFLEX_OTHER_API_KEY", "sensitive-canary")
	a := NewCodex(helper(t, "codex-catalog-paged"))
	cataloger, ok := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	})
	if !ok {
		t.Fatal("Codex adapter does not expose catalog")
	}
	result, err := cataloger.QueryModels(t.Context(), t.TempDir())
	if err != nil || result.Status != modelcatalog.StatusComplete || !result.Complete || len(result.Models) != 2 {
		t.Fatal(result, err)
	}
	if result.Models[0].ID != "runtime-a" || result.Models[1].ID != "runtime-b" ||
		result.Models[0].DefaultReasoningEffort != "high" || strings.Join(result.Models[0].SupportedReasoningEfforts, ",") != "low,high" ||
		result.Source != "codex_app_server" || !result.AccountFiltered || result.ProfileRevision == "" {
		t.Fatal(result)
	}
}

func TestCodexCatalogFailsClosedWhenEffectiveContextCannotBeRead(t *testing.T) {
	for _, mode := range []string{"codex-catalog-context-missing", "codex-catalog-no-account"} {
		t.Run(mode, func(t *testing.T) {
			a := NewCodex(helper(t, mode))
			result, err := a.(interface {
				QueryModels(context.Context, string) (modelcatalog.Result, error)
			}).QueryModels(t.Context(), t.TempDir())
			if err != nil || result.Complete || result.Status != modelcatalog.StatusFailed || result.ErrorCode != "catalog_context_unverified" || len(result.Models) != 0 {
				t.Fatal(result, err)
			}
		})
	}
}

func TestCodexCatalogCancellationAndUnexpectedServerRequest(t *testing.T) {
	for _, mode := range []string{"codex-catalog-hang", "codex-catalog-request"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			a := NewCodex(helper(t, mode))
			cataloger, ok := a.(interface {
				QueryModels(context.Context, string) (modelcatalog.Result, error)
			})
			if !ok {
				t.Fatal("Codex adapter does not expose catalog")
			}
			result, err := cataloger.QueryModels(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "codex-catalog-hang" && result.Status != modelcatalog.StatusInterrupted {
				t.Fatal(result)
			}
			if mode == "codex-catalog-request" && (result.Status != modelcatalog.StatusFailed || result.Complete) {
				t.Fatal(result)
			}
		})
	}
}

func TestCodexCatalogPageTimeoutRemainsInterruptedAfterProcessCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	a := NewCodex(helper(t, "codex-catalog-hang"))
	cataloger := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	})
	result, err := cataloger.QueryModels(ctx, t.TempDir())
	if err != nil || result.Status != modelcatalog.StatusInterrupted || result.Complete || result.ErrorCode != "catalog_cancelled" {
		t.Fatal(result, err)
	}
}

func TestCodexCatalogCancelledBeforeAdmissionDoesNotResolveExecutable(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a := NewCodex(filepath.Join(t.TempDir(), "missing-codex"))
	cataloger := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	})
	result, err := cataloger.QueryModels(ctx, t.TempDir())
	if err != nil || result.Status != modelcatalog.StatusInterrupted || result.ErrorCode != "catalog_cancelled" {
		t.Fatal(result, err)
	}
}

func TestCodexCatalogBoundsOneOversizedProtocolFrame(t *testing.T) {
	a := NewCodex(helper(t, "codex-catalog-large"))
	cataloger := a.(interface {
		QueryModels(context.Context, string) (modelcatalog.Result, error)
	})
	result, err := cataloger.QueryModels(t.Context(), t.TempDir())
	if err != nil || result.Status != modelcatalog.StatusPartial || result.ErrorCode != "catalog_limit" || result.Complete {
		t.Fatal(result, err)
	}
}

func TestCodexCatalogRejectsMalformedAndOversizedPageMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want error
	}{
		{"missing data", `{}`, modelcatalog.ErrUnavailable},
		{"invalid cursor", `{"data":[],"nextCursor":"bad\nvalue"}`, modelcatalog.ErrUnavailable},
		{"missing runtime slug", `{"data":[{"id":"catalog-only","displayName":"Only"}]}`, modelcatalog.ErrUnavailable},
		{"invalid runtime slug", `{"data":[{"id":"catalog","model":"-unsafe","displayName":"Unsafe"}]}`, modelcatalog.ErrUnavailable},
		{"reasoning budget", `{"data":[{"id":"catalog","model":"runtime","supportedReasoningEfforts":[` + strings.Repeat(`{"reasoningEffort":"low"},`, 32) + `{"reasoningEffort":"high"}]}]}`, modelcatalog.ErrLimit},
		{"model budget", `{"data":[` + strings.Repeat(`{"id":"catalog","model":"runtime"},`, 1000) + `{"id":"last","model":"last"}]}`, modelcatalog.ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeCodexModelPage(json.RawMessage(tc.body), int64(len(tc.body)))
			if !errors.Is(err, tc.want) {
				t.Fatal(err, tc.want)
			}
		})
	}
}

func TestCodexCatalogOmitsHiddenModelAndUnverifiedDefaultEffort(t *testing.T) {
	body := `{"data":[{"id":"hidden","model":"hidden-runtime","hidden":true},{"id":"shown","model":"shown-runtime","displayName":"Shown","defaultReasoningEffort":"ultra","supportedReasoningEfforts":[{"reasoningEffort":"low"}]}]}`
	page, err := decodeCodexModelPage(json.RawMessage(body), int64(len(body)))
	if err != nil || len(page.Models) != 1 || page.Models[0].ID != "shown-runtime" ||
		page.Models[0].DefaultReasoningEffort != "" || strings.Join(page.Models[0].SupportedReasoningEfforts, ",") != "low" {
		t.Fatal(page, err)
	}
}
