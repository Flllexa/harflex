package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/persioflexa/harflex/internal/agentcore"
	mcpclient "github.com/persioflexa/harflex/internal/mcp"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func TestApplicationMCPStdioHelper(t *testing.T) {
	if !slices.Contains(os.Args, "application-mcp-helper") {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "stdio-fixture", Version: "1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "read_token"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: os.Getenv("MCP_TOKEN")}}}, nil, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		t.Fatal(err)
	}
}

func TestStdioCredentialEnvNamePersistsWithoutSecretAndRedactsToolResult(t *testing.T) {
	const token = "synthetic-stdio-keyring-secret"
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in := SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Local authenticated MCP", Transport: "stdio", Command: os.Args[0], Args: []string{"-test.run=TestApplicationMCPStdioHelper", "--", "application-mcp-helper"}, Token: token}
	if err := json.Unmarshal([]byte(`{"tokenEnvVar":"MCP_TOKEN"}`), &in); err != nil {
		t.Fatal(err)
	}
	configured, err := s.SaveMCPServer(in)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(configured)
	if err != nil || !bytes.Contains(encoded, []byte(`"tokenEnvVar":"MCP_TOKEN"`)) || bytes.Contains(encoded, []byte(token)) {
		t.Fatalf("stdio credential environment name missing or secret in DTO: %s %v", encoded, err)
	}
	var storedName string
	var storedArgs []byte
	var storedURL string
	if err := db.DB().QueryRow("SELECT token_env_var,args,endpoint_url FROM mcp_servers WHERE id=?", configured.ID).Scan(&storedName, &storedArgs, &storedURL); err != nil || storedName != "MCP_TOKEN" || bytes.Contains(storedArgs, []byte(token)) || strings.Contains(storedURL, token) {
		t.Fatalf("stdio credential was not isolated from SQLite config: %q %v", storedName, err)
	}
	connected, err := s.ConnectMCPServer(configured.ID)
	if err != nil || len(connected.Tools) != 1 || connected.Tools[0].Name != "read_token" {
		t.Fatalf("stdio discovery: %+v %v", connected, err)
	}
	item, err := db.GetMCPServer(t.Context(), configured.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.mcpConfig(t.Context(), item)
	if err != nil {
		t.Fatal(err)
	}
	response, err := mcpclient.Call(t.Context(), cfg, "read_token", []byte(`{}`))
	if err != nil || response.Text != token {
		t.Fatalf("stdio child did not receive keyring token: %+v %v", response, err)
	}
	cfg.Token = ""

	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "stdio-token-call", Name: connected.Tools[0].AgentName, Arguments: []byte(`{}`)}}
	s.providerFactory = func(c openai.Config) (agentcore.Provider, error) { provider.key = c.APIKey; return provider, nil }
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Read child token"})
	if err != nil || result.Status != RunAwaitingApproval {
		t.Fatalf("stdio call skipped approval: %+v %v", result, err)
	}
	if _, err := s.Approve(ApprovalInput{SessionID: session.ID, ApprovalID: result.Approval.ID, Allow: true}); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListEvents(ListEventsInput{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(events)
	if err != nil || bytes.Contains(encoded, []byte(token)) {
		t.Fatal("stdio credential leaked to journal replay", err)
	}
	destination := filepath.Join(t.TempDir(), "stdio-audit.jsonl")
	if _, err := s.ExportAudit(ExportAuditInput{SessionID: session.ID, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	audit, err := os.ReadFile(destination)
	if err != nil || bytes.Contains(audit, []byte(token)) {
		t.Fatal("stdio credential leaked to audit export", err)
	}
}

func TestMCPDiscoveryRejectsCredentialEchoBeforePublishingTools(t *testing.T) {
	for _, field := range []string{"name", "description", "schema"} {
		t.Run(field, func(t *testing.T) {
			const token = "synthetic<credential-echo>&123"
			tool := &mcp.Tool{Name: "echo", Description: "safe", InputSchema: map[string]any{"type": "object"}}
			switch field {
			case "name":
				tool.Name = "echo_" + token
			case "description":
				tool.Description = "Echo " + token
			case "schema":
				tool.InputSchema = map[string]any{"type": "object", "description": token}
			}
			server := mcp.NewServer(&mcp.Implementation{Name: "credential-echo", Version: "1.0"}, nil)
			server.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			})
			transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+token {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				transport.ServeHTTP(w, r)
			}))
			defer httpServer.Close()

			s, db, _ := setup(t)
			workspace, err := s.OpenWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			configured, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Echo", Transport: "http", URL: httpServer.URL, Token: token})
			if err != nil {
				t.Fatal(err)
			}
			connected, err := s.ConnectMCPServer(configured.ID)
			if !errors.Is(err, ErrMCPConnectionFailed) || strings.Contains(err.Error(), token) {
				t.Fatalf("credential echo was not safely rejected: %+v %v", connected, err)
			}
			encoded, err := json.Marshal(connected)
			if err != nil || bytes.Contains(encoded, []byte(token)) {
				t.Fatal("credential echoed in connection DTO", err)
			}
			servers, err := s.ListMCPServers(workspace.ID)
			if err != nil || len(servers) != 1 || servers[0].Enabled || len(servers[0].Tools) != 0 {
				t.Fatalf("contaminated discovery published tools: %+v %v", servers, err)
			}
			encoded, err = json.Marshal(servers)
			if err != nil || bytes.Contains(encoded, []byte(token)) {
				t.Fatal("credential echoed in listed DTO", err)
			}
			var storedTools []byte
			if err := db.DB().QueryRow("SELECT tools FROM mcp_servers WHERE id=?", configured.ID).Scan(&storedTools); err != nil || !bytes.Equal(storedTools, []byte("[]")) {
				t.Fatalf("contaminated discovery reached SQLite: %s %v", storedTools, err)
			}
			var eventCount int
			if err := db.DB().QueryRow("SELECT COUNT(*) FROM events").Scan(&eventCount); err != nil || eventCount != 0 {
				t.Fatalf("discovery wrote journal events: %d %v", eventCount, err)
			}
		})
	}
}

func TestContaminatedMCPRediscoveryDisablesPreviouslyEnabledServer(t *testing.T) {
	const token = "synthetic-reconnect-secret"
	serverWith := func(description string) *mcp.Server {
		server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1.0"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: description}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
		return server
	}
	clean := serverWith("safe")
	contaminated := serverWith(token)
	var unsafe atomic.Bool
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		if unsafe.Load() {
			return contaminated
		}
		return clean
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})
	httpServer := httptest.NewServer(transport)
	defer httpServer.Close()
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configured, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Reconnect", Transport: "http", URL: httpServer.URL, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	connected, err := s.ConnectMCPServer(configured.ID)
	if err != nil || !connected.Enabled || len(connected.Tools) != 1 {
		t.Fatalf("clean discovery: %+v %v", connected, err)
	}
	unsafe.Store(true)
	if _, err := s.ConnectMCPServer(configured.ID); !errors.Is(err, ErrMCPConnectionFailed) {
		t.Fatalf("contaminated rediscovery was accepted: %v", err)
	}
	stored, err := db.GetMCPServer(t.Context(), configured.ID)
	if err != nil || stored.Enabled || len(stored.Tools) != 0 {
		t.Fatalf("previous tools remained enabled after unsafe discovery: %+v %v", stored, err)
	}
}

func TestMCPConfigRejectsCredentialInPersistedArgsOrURL(t *testing.T) {
	const token = "synthetic-config-secret"
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stdio := SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Local", Transport: "stdio", Command: os.Args[0], TokenEnvVar: "MCP_TOKEN", Token: token}
	for _, in := range []SaveMCPServerInput{
		{WorkspaceID: workspace.ID, Name: "HTTP", Transport: "http", URL: "http://127.0.0.1:3333/" + token, Token: token},
		{WorkspaceID: workspace.ID, Name: "HTTP encoded", Transport: "http", URL: "http://127.0.0.1:3333/synthetic%2Dconfig%2Dsecret", Token: token},
		{WorkspaceID: workspace.ID, Name: "Local", Transport: "stdio", Command: os.Args[0], Args: []string{"--token=" + token}, TokenEnvVar: "MCP_TOKEN", Token: token},
		{WorkspaceID: workspace.ID, Name: "Local", Transport: "stdio", Command: os.Args[0], TokenEnvVar: "MCP_TOKEN", Token: "MCP_TOKEN"},
	} {
		if _, err := s.SaveMCPServer(in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("credential-bearing MCP config accepted: %v", err)
		}
	}
	configured, err := s.SaveMCPServer(stdio)
	if err != nil {
		t.Fatal(err)
	}
	stdio.ID = configured.ID
	stdio.Token = ""
	stdio.Args = []string{"--token=" + token}
	if _, err := s.SaveMCPServer(stdio); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("existing key was copied into process args: %v", err)
	}
	var count int
	if err := db.DB().QueryRow("SELECT COUNT(*) FROM mcp_servers").Scan(&count); err != nil || count != 1 {
		t.Fatalf("invalid config persisted: %d %v", count, err)
	}
}

func TestMCPServerDTOUsesEmptyArgsArray(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configured, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "HTTP", Transport: "http", URL: "http://127.0.0.1:3333/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(configured)
	if err != nil || !bytes.Contains(encoded, []byte(`"args":[]`)) {
		t.Fatalf("MCP DTO did not satisfy frontend array contract: %s %v", encoded, err)
	}
}

func TestMCPFailedConnectionKeepsServerDisabled(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := httpServer.URL
	httpServer.Close()
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configured, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Offline", Transport: "http", URL: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConnectMCPServer(configured.ID); !errors.Is(err, ErrMCPConnectionFailed) {
		t.Fatalf("expected sanitized connection failure, got %v", err)
	}
	servers, err := s.ListMCPServers(workspace.ID)
	if err != nil || len(servers) != 1 || servers[0].Enabled {
		t.Fatalf("failed connection enabled MCP tools: %+v %v", servers, err)
	}
}

func TestMCPToolRequiresExplicitConnectionAndAgentApproval(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1.0"}, nil)
	var calls atomic.Int32
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo text"}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
		Text string `json:"text"`
	}) (*mcp.CallToolResult, any, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: input.Text}}}, nil, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	defer httpServer.Close()
	s, _, _ := setup(t)
	const token = "mcp-top-secret-test-value"
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configured, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Local Echo", Transport: "http", URL: httpServer.URL, Token: token})
	if err != nil || configured.Enabled {
		t.Fatalf("configured without consent: %+v %v", configured, err)
	}
	connected, err := s.ConnectMCPServer(configured.ID)
	if err != nil || !connected.Enabled || len(connected.Tools) != 1 || connected.Tools[0].Name != "echo" {
		t.Fatalf("discovery: %+v %v", connected, err)
	}
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "mcp-call", Name: connected.Tools[0].AgentName, Arguments: []byte(`{"text":"mcp-top-secret-test-value"}`)}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Use echo"})
	if err != nil || result.Status != RunAwaitingApproval || calls.Load() != 0 {
		t.Fatalf("MCP called before approval: %+v %v", result, err)
	}
	if _, err := s.Approve(ApprovalInput{SessionID: session.ID, ApprovalID: result.Approval.ID, Allow: true}); err != nil || calls.Load() != 1 {
		t.Fatalf("approved MCP call: %d %v", calls.Load(), err)
	}
	events, err := s.ListEvents(ListEventsInput{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("MCP credential leaked into replay events")
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "MCP specialist", Instructions: "Use echo", BackendID: "local", AllowedTools: []string{connected.Tools[0].AgentName}})
	if err != nil {
		t.Fatal(err)
	}
	agentSession, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local", AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	provider.toolCall = &agentcore.ToolCall{ID: "mcp-agent-call", Name: connected.Tools[0].AgentName, Arguments: []byte(`{"text":"agent hello"}`)}
	agentResult, err := s.Prompt(PromptInput{SessionID: agentSession.ID, Text: "Echo again"})
	if err != nil || agentResult.Status != RunAwaitingApproval || calls.Load() != 1 {
		t.Fatalf("agent MCP tool not gated: %+v %v", agentResult, err)
	}
	if _, err := s.Approve(ApprovalInput{SessionID: agentSession.ID, ApprovalID: agentResult.Approval.ID, Allow: true}); err != nil || calls.Load() != 2 {
		t.Fatalf("agent MCP tool did not execute after approval: %d %v", calls.Load(), err)
	}
	destination := filepath.Join(t.TempDir(), "mcp-audit.jsonl")
	if _, err := s.ExportAudit(ExportAuditInput{SessionID: session.ID, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	audit, err := os.ReadFile(destination)
	if err != nil || strings.Contains(string(audit), token) {
		t.Fatal("MCP credential leaked into audit export", err)
	}
}
