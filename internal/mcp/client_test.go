package mcpclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPStdioHelper(t *testing.T) {
	if !slices.Contains(os.Args, "mcp-helper") {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "stdio-fixture", Version: "1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
		Text string `json:"text"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: input.Text}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "read_token"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: os.Getenv("MCP_TOKEN")}}}, nil, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		t.Fatal(err)
	}
}

func TestStdioClientStartsOwnsAndClosesServer(t *testing.T) {
	cfg := Config{Transport: "stdio", Command: os.Args[0], Args: []string{"-test.run=TestMCPStdioHelper", "--", "mcp-helper"}, WorkspacePath: t.TempDir()}
	tools, err := Discover(t.Context(), cfg)
	if err != nil || len(tools) != 2 {
		t.Fatalf("stdio discovery: %+v %v", tools, err)
	}
	result, err := Call(t.Context(), cfg, "echo", []byte(`{"text":"stdio works"}`))
	if err != nil || result.Text != "stdio works" {
		t.Fatalf("stdio call: %+v %v", result, err)
	}
}

func TestStdioTokenReachesOnlyAuthorizedChildEnvironment(t *testing.T) {
	t.Setenv("MCP_TOKEN", "inherited-token-must-not-be-used")
	cfg := Config{Transport: "stdio", Command: os.Args[0], Args: []string{"-test.run=TestMCPStdioHelper", "--", "mcp-helper"}, WorkspacePath: t.TempDir(), TokenEnvVar: "MCP_TOKEN", Token: "synthetic-stdio-secret"}
	result, err := Call(t.Context(), cfg, "read_token", []byte(`{}`))
	if err != nil || result.Text != cfg.Token {
		t.Fatalf("stdio child did not receive its configured credential: %+v %v", result, err)
	}
}

func TestStdioTokenEnvironmentNameValidation(t *testing.T) {
	base := Config{Transport: "stdio", Command: os.Args[0], WorkspacePath: t.TempDir(), Token: "synthetic-token"}
	for _, name := range []string{"", "1TOKEN", "TOKEN-NAME", "TOKEN=NAME", "PATH", "home", "NO_COLOR", "MCP TOKEN"} {
		cfg := base
		cfg.TokenEnvVar = name
		if err := Validate(cfg); err != ErrInvalidConfig {
			t.Fatalf("unsafe token env name %q accepted: %v", name, err)
		}
	}
	base.TokenEnvVar = "MCP_TOKEN"
	if err := Validate(base); err != nil {
		t.Fatalf("valid token env name rejected: %v", err)
	}
	base.Transport, base.URL, base.Command = "http", "http://127.0.0.1:3333/mcp", ""
	if err := Validate(base); err != ErrInvalidConfig {
		t.Fatalf("stdio env name accepted for HTTP transport: %v", err)
	}
}

func TestStdioEnvPreservesMixedCasePathWithoutOpeningAllowlist(t *testing.T) {
	t.Setenv("Path", "synthetic-child-path")
	t.Setenv("UNRELATED_SECRET", "synthetic-parent-only")
	env := stdioEnv()
	if !slices.Contains(env, "Path=synthetic-child-path") {
		t.Fatal("mixed-case Path was removed from stdio child environment")
	}
	if slices.Contains(env, "UNRELATED_SECRET=synthetic-parent-only") {
		t.Fatal("stdio child inherited an unapproved variable")
	}
}

func TestStdioEnvFiltersSimulatedWindowsPath(t *testing.T) {
	env := stdioEnvFrom([]string{
		"Path=C:\\Windows\\System32",
		"SystemRoot=C:\\Windows",
		"UNRELATED_SECRET=synthetic-parent-only",
		"NO_COLOR=0",
		"ſystemRoot=unexpected-unicode-alias",
	})
	if !slices.Contains(env, "Path=C:\\Windows\\System32") || !slices.Contains(env, "SystemRoot=C:\\Windows") {
		t.Fatalf("Windows child environment lost allowed keys: %v", env)
	}
	if slices.Contains(env, "UNRELATED_SECRET=synthetic-parent-only") || slices.Contains(env, "NO_COLOR=0") || slices.Contains(env, "ſystemRoot=unexpected-unicode-alias") {
		t.Fatalf("Windows child environment admitted unapproved keys: %v", env)
	}
	if !slices.Contains(env, "NO_COLOR=1") {
		t.Fatalf("child color policy missing: %v", env)
	}
}

func TestHTTPClientDiscoversAndCallsRealMCPTool(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo text"}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
		Text string `json:"text"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: input.Text}}}, nil, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	defer httpServer.Close()
	cfg := Config{Transport: "http", URL: httpServer.URL, WorkspacePath: t.TempDir()}
	tools, err := Discover(t.Context(), cfg)
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" || len(tools[0].Schema) == 0 {
		t.Fatalf("discover: %+v %v", tools, err)
	}
	result, err := Call(t.Context(), cfg, "echo", []byte(`{"text":"hello MCP"}`))
	if err != nil || result.Text != "hello MCP" || result.IsError {
		t.Fatalf("call: %+v %v", result, err)
	}
}

func TestHTTPRejectsCredentialsInEndpointURL(t *testing.T) {
	workspace := t.TempDir()
	for _, endpoint := range []string{"http://127.0.0.1:3333/mcp?token=secret", "https://user:secret@example.com/mcp", "http://example.com/mcp"} {
		if err := Validate(Config{Transport: "http", URL: endpoint, WorkspacePath: workspace}); err == nil {
			t.Fatalf("unsafe MCP endpoint accepted: %q", endpoint)
		}
	}
}

func TestMCPToolCallHonorsCancellation(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "slow-fixture", Version: "1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "wait"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := Call(ctx, Config{Transport: "http", URL: httpServer.URL, WorkspacePath: t.TempDir()}, "wait", []byte(`{}`))
	if err == nil || time.Since(started) > 2*time.Second {
		t.Fatalf("MCP call did not stop promptly on cancellation: %v", err)
	}
}

func TestMCPDiscoveryFailsInsteadOfSilentlyTruncatingTools(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "many-tools", Version: "1.0"}, nil)
	for index := 0; index < 257; index++ {
		mcp.AddTool(server, &mcp.Tool{Name: fmt.Sprintf("tool_%03d", index)}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	}
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	defer httpServer.Close()
	if tools, err := Discover(t.Context(), Config{Transport: "http", URL: httpServer.URL, WorkspacePath: t.TempDir()}); err == nil {
		t.Fatalf("oversized MCP catalog silently truncated to %d tools", len(tools))
	}
}

// headerSpy serves a real MCP endpoint and remembers the Authorization header of every request.
func headerSpy(t *testing.T) (string, func() []string) {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "auth-fixture", Version: "1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo text"}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
		Text string `json:"text"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: input.Text}}}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	var mu sync.Mutex
	var seen []string
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	return httpServer.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func TestHTTPCredentialIsSentInTheSchemeTheServerExpects(t *testing.T) {
	for _, tt := range []struct {
		name, scheme, token, want string
	}{
		{"default is a bearer token", "", "ghp_synthetic", "Bearer ghp_synthetic"},
		{"explicit bearer", AuthBearer, "ghp_synthetic", "Bearer ghp_synthetic"},
		{"basic encodes user and secret", AuthBasic, "ana@example.com:atlassian-api-token", "Basic " + base64.StdEncoding.EncodeToString([]byte("ana@example.com:atlassian-api-token"))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			url, headers := headerSpy(t)
			cfg := Config{Transport: "http", URL: url, WorkspacePath: t.TempDir(), AuthScheme: tt.scheme, Token: tt.token}
			if tools, err := Discover(t.Context(), cfg); err != nil || len(tools) != 1 {
				t.Fatalf("discover: %+v %v", tools, err)
			}
			if result, err := Call(t.Context(), cfg, "echo", []byte(`{"text":"ok"}`)); err != nil || result.Text != "ok" {
				t.Fatalf("call: %+v %v", result, err)
			}
			seen := headers()
			if len(seen) == 0 {
				t.Fatal("the server saw no request")
			}
			for _, header := range seen {
				if header != tt.want {
					t.Fatalf("Authorization = %q, want %q", header, tt.want)
				}
			}
		})
	}
}

func TestHTTPWithoutACredentialSendsNoAuthorization(t *testing.T) {
	url, headers := headerSpy(t)
	if _, err := Discover(t.Context(), Config{Transport: "http", URL: url, WorkspacePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for _, header := range headers() {
		if header != "" {
			t.Fatalf("an unauthenticated server received %q", header)
		}
	}
}

func TestBasicCredentialMustBeUserAndSecret(t *testing.T) {
	workspace := t.TempDir()
	base := Config{Transport: "http", URL: "https://mcp.example.test/v2/mcp", WorkspacePath: workspace, AuthScheme: AuthBasic}
	for _, token := range []string{"", "no-colon", ":only-secret", "only-user:", "ana@example.com:tok\nen", "ana@example.com:tok\x00en"} {
		cfg := base
		cfg.Token = token
		if err := Validate(cfg); err != ErrInvalidConfig {
			t.Fatalf("basic credential %q accepted: %v", token, err)
		}
	}
	cfg := base
	cfg.Token = "ana@example.com:atlassian-api-token"
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid basic credential rejected: %v", err)
	}
	// A secret may itself contain colons; only the first one separates the user.
	cfg.Token = "ana@example.com:part:two"
	if err := Validate(cfg); err != nil {
		t.Fatalf("secret with a colon rejected: %v", err)
	}
}

func TestAuthSchemeIsOnlyForHTTPAndOnlyKnownValues(t *testing.T) {
	workspace := t.TempDir()
	for _, scheme := range []string{"digest", "BASIC", "none", "bearer "} {
		cfg := Config{Transport: "http", URL: "https://mcp.example.test/mcp", WorkspacePath: workspace, AuthScheme: scheme, Token: "ana@example.com:token"}
		if err := Validate(cfg); err != ErrInvalidConfig {
			t.Fatalf("auth scheme %q accepted: %v", scheme, err)
		}
	}
	stdio := Config{Transport: "stdio", Command: os.Args[0], WorkspacePath: workspace, AuthScheme: AuthBasic}
	if err := Validate(stdio); err != ErrInvalidConfig {
		t.Fatalf("basic scheme accepted for a local process: %v", err)
	}
	stdio.AuthScheme = AuthBearer
	if err := Validate(stdio); err != nil {
		t.Fatalf("the stored default scheme must stay valid for a local process: %v", err)
	}
	if strings.Contains(authorization(Config{AuthScheme: AuthBasic, Token: "ana@example.com:secret"}), "secret") {
		t.Fatal("the Basic header carries the secret in clear text")
	}
}
