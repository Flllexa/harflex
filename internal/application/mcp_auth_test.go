package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

// basicServer is an MCP endpoint that only answers a request carrying exactly the expected Authorization header.
func basicServer(t *testing.T, wantHeader string, handler mcp.ToolHandler, toolName string) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "bitbucket-fixture", Version: "1.0"}, nil)
	server.AddTool(&mcp.Tool{Name: toolName, Description: "Open a pull request", InputSchema: map[string]any{"type": "object"}}, handler)
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != wantHeader {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		transport.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	return httpServer
}

func okTool(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
}

func TestMCPServerWithBasicCredentialConnectsAndNeverStoresTheSecretInTheCatalog(t *testing.T) {
	const credential = "ana@example.com:atlassian-api-token-canary"
	httpServer := basicServer(t, "Basic "+base64.StdEncoding.EncodeToString([]byte(credential)), okTool, "bitbucketPullRequest")
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configured, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Bitbucket", Transport: "http", URL: httpServer.URL, AuthScheme: "basic", Token: credential})
	if err != nil {
		t.Fatal(err)
	}
	if configured.AuthScheme != "basic" || !configured.HasCredential {
		t.Fatalf("saved server = %+v", configured)
	}
	encoded, err := json.Marshal(configured)
	if err != nil || bytes.Contains(encoded, []byte("atlassian-api-token-canary")) || bytes.Contains(encoded, []byte("ana@example.com")) {
		t.Fatalf("the credential reached the DTO: %s %v", encoded, err)
	}
	var scheme, row string
	if err := db.DB().QueryRow(`SELECT auth_scheme, name||command_path||args||endpoint_url||token_env_var||credential_account FROM mcp_servers WHERE id=?`, configured.ID).Scan(&scheme, &row); err != nil || scheme != "basic" || strings.Contains(row, "atlassian-api-token-canary") {
		t.Fatalf("catalog row: scheme=%q err=%v (secret in row: %v)", scheme, err, strings.Contains(row, "atlassian-api-token-canary"))
	}

	connected, err := s.ConnectMCPServer(configured.ID)
	if err != nil || !connected.Enabled || len(connected.Tools) != 1 || connected.AuthScheme != "basic" {
		t.Fatalf("connect with the basic credential: %+v %v", connected, err)
	}

	// Editing the name keeps both the stored credential and the way it is presented.
	renamed, err := s.SaveMCPServer(SaveMCPServerInput{ID: configured.ID, WorkspaceID: workspace.ID, Name: "Bitbucket (equipe)", Transport: "http", URL: httpServer.URL})
	if err != nil || renamed.AuthScheme != "basic" || !renamed.HasCredential {
		t.Fatalf("edit without a new credential: %+v %v", renamed, err)
	}
	if again, err := s.ConnectMCPServer(configured.ID); err != nil || len(again.Tools) != 1 {
		t.Fatalf("reconnect after the edit: %+v %v", again, err)
	}
}

func TestMCPServerRejectsAnInconsistentAuthScheme(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	http := func(scheme, token string) SaveMCPServerInput {
		return SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Remote", Transport: "http", URL: "https://mcp.example.test/mcp", AuthScheme: scheme, Token: token}
	}
	for name, in := range map[string]SaveMCPServerInput{
		"basic without a credential":      http("basic", ""),
		"basic without user and secret":   http("basic", "just-a-token"),
		"basic with an empty secret":      http("basic", "ana@example.com:"),
		"unknown scheme":                  http("digest", "ana@example.com:token"),
		"basic for a local process":       {WorkspaceID: workspace.ID, Name: "Local", Transport: "stdio", Command: "/bin/echo", TokenEnvVar: "MCP_TOKEN", AuthScheme: "basic", Token: "ana@example.com:token"},
		"basic with a newline in secret":  http("basic", "ana@example.com:to\nken"),
		"a secret that is also the name":  {WorkspaceID: workspace.ID, Name: "ana@example.com:token", Transport: "http", URL: "https://mcp.example.test/mcp", AuthScheme: "basic", Token: "ana@example.com:token"},
		"a secret echoed in the endpoint": {WorkspaceID: workspace.ID, Name: "Remote", Transport: "http", URL: "https://mcp.example.test/ana@example.com:token", AuthScheme: "basic", Token: "ana@example.com:token"},
	} {
		if _, err := s.SaveMCPServer(in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s was accepted: %v", name, err)
		}
	}
	var count int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM mcp_servers`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("a rejected server was stored: %d %v", count, err)
	}
	// A server saved without naming a scheme is a bearer server, as every server was before the choice existed.
	saved, err := s.SaveMCPServer(http("", "ghp_synthetic"))
	if err != nil || saved.AuthScheme != "bearer" {
		t.Fatalf("default scheme: %+v %v", saved, err)
	}
}

// What an MCP server answers when it refuses a call is exactly what the model needs to try again, so a refusal
// is a tool failure the run survives and the model reads, not the end of the run.
func TestMCPServerRefusingACallLetsTheRunGoOn(t *testing.T) {
	refuse := func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "A pull request already exists for branch harflex/csv-export"}}}, nil
	}
	httpServer := basicServer(t, "Bearer ghp_synthetic", refuse, "create_pull_request")
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configured, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "GitHub", Transport: "http", URL: httpServer.URL, Token: "ghp_synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	connected, err := s.ConnectMCPServer(configured.ID)
	if err != nil || len(connected.Tools) != 1 {
		t.Fatalf("connect: %+v %v", connected, err)
	}
	// Full access is what lets an unattended agent call a network tool; it is the point of the setting.
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "full_access", ConfirmFullAccess: true}); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "pr-call", Name: connected.Tools[0].AgentName, Arguments: json.RawMessage(`{"title":"CSV"}`)}}
	s.providerFactory = func(c openai.Config) (agentcore.Provider, error) { provider.key = c.APIKey; return provider, nil }
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Open the pull request"})
	if err != nil || result.Status != RunCompleted || result.Approval != nil {
		t.Fatalf("a refused MCP call ended the run or asked for approval: %+v %v", result, err)
	}
	got := toolMessages(provider.request.Messages)
	if len(got) != 1 || !strings.Contains(got[0], `"error":"mcp_error"`) || !strings.Contains(got[0], "A pull request already exists for branch harflex/csv-export") {
		t.Fatalf("the model must read the server's answer, got %q", got)
	}
	events, err := s.ListEvents(ListEventsInput{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	var failed struct {
		ErrorCode   string `json:"errorCode"`
		Recoverable bool   `json:"recoverable"`
	}
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
		if event.Type == "tool.failed" {
			if err := json.Unmarshal(event.Data, &failed); err != nil {
				t.Fatal(err)
			}
		}
	}
	if failed.ErrorCode != "mcp_error" || !failed.Recoverable || types[len(types)-1] != "run.completed" {
		t.Fatalf("failed event = %+v, events = %v", failed, types)
	}
	var credentialRows int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE data LIKE '%ghp_synthetic%'`).Scan(&credentialRows); err != nil || credentialRows != 0 {
		t.Fatalf("the credential reached the journal: %d %v", credentialRows, err)
	}
}

// A server that refuses a call may repeat what it received. The credential, in any form it travelled in, must reach
// neither the model nor the journal through that answer.
func TestMCPAnswerNeverCarriesTheCredentialBackToTheModelOrTheJournal(t *testing.T) {
	for _, tt := range []struct {
		name, scheme, credential, header string
		leaks                            []string
	}{
		{"bearer", "bearer", "ghp_leaky_synthetic_token", "Bearer ghp_leaky_synthetic_token", []string{"ghp_leaky_synthetic_token"}},
		{"basic", "basic", "ana@example.com:atlassian-leaky-token", "Basic " + base64.StdEncoding.EncodeToString([]byte("ana@example.com:atlassian-leaky-token")),
			[]string{"ana@example.com:atlassian-leaky-token", "atlassian-leaky-token", base64.StdEncoding.EncodeToString([]byte("ana@example.com:atlassian-leaky-token"))}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			echo := func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				text := "401 Bad credentials. Received " + strings.Join(tt.leaks, " | ") + " (Authorization: " + tt.header + ")"
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
			}
			httpServer := basicServer(t, tt.header, echo, "create_pull_request")
			s, db, _ := setup(t)
			workspace, err := s.OpenWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			configured, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Remote", Transport: "http", URL: httpServer.URL, AuthScheme: tt.scheme, Token: tt.credential})
			if err != nil {
				t.Fatal(err)
			}
			connected, err := s.ConnectMCPServer(configured.ID)
			if err != nil || len(connected.Tools) != 1 {
				t.Fatalf("connect: %+v %v", connected, err)
			}
			if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "full_access", ConfirmFullAccess: true}); err != nil {
				t.Fatal(err)
			}
			provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "leak-call", Name: connected.Tools[0].AgentName, Arguments: json.RawMessage(`{}`)}}
			s.providerFactory = func(c openai.Config) (agentcore.Provider, error) { provider.key = c.APIKey; return provider, nil }
			if _, err := s.SaveProviderProfile(profileInput()); err != nil {
				t.Fatal(err)
			}
			session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Open the pull request"})
			if err != nil || result.Status != RunCompleted {
				t.Fatalf("run: %+v %v", result, err)
			}
			toolMessage := strings.Join(toolMessages(provider.request.Messages), "\n")
			if !strings.Contains(toolMessage, "401 Bad credentials") || !strings.Contains(toolMessage, "[credencial removida]") {
				t.Fatalf("the model should read the refusal with the credential removed, got %q", toolMessage)
			}
			var journal []byte
			rows, err := db.DB().Query(`SELECT data FROM events WHERE stream_id=?`, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var data []byte
				if err := rows.Scan(&data); err != nil {
					t.Fatal(err)
				}
				journal = append(journal, data...)
			}
			for _, leak := range tt.leaks {
				if strings.Contains(toolMessage, leak) {
					t.Errorf("the model read the credential form %q", leak)
				}
				if bytes.Contains(journal, []byte(leak)) {
					t.Errorf("the journal stored the credential form %q", leak)
				}
			}
		})
	}
}
