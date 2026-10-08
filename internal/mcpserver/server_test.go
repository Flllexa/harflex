package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/security"
	"github.com/persioflexa/harflex/internal/tools"
)

type echoTool struct{}

func (echoTool) Spec() agentcore.ToolSpec {
	return agentcore.ToolSpec{Name: "echo", Description: "echoes", Schema: json.RawMessage(`{"type":"object"}`)}
}
func (echoTool) Risk() security.Risk { return security.ReadOnly }
func (echoTool) Execute(_ context.Context, args json.RawMessage, _ agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	if strings.Contains(string(args), "fail") {
		return agentcore.ToolExecutionResult{}, &agentcore.ToolFailure{Code: "bad", Message: "it failed"}
	}
	return agentcore.ToolExecutionResult{Content: args}, nil
}

func post(t *testing.T, url, token, body string, mutate func(*http.Request)) (*http.Response, map[string]any) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if mutate != nil {
		mutate(request)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response, decoded
}

func TestServerSpeaksMCPToTheTokensOwnerOnly(t *testing.T) {
	server, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	token, release := server.Register([]tools.Tool{echoTool{}})

	if response, _ := post(t, server.URL(), "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", response.StatusCode)
	}
	if response, _ := post(t, server.URL(), token, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }); response.StatusCode != http.StatusForbidden {
		t.Fatalf("browser origin: %d", response.StatusCode)
	}
	if response, _ := post(t, server.URL(), token, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, func(r *http.Request) { r.Host = "rebind.example:80" }); response.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign host: %d", response.StatusCode)
	}

	_, initialized := post(t, server.URL(), token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`, nil)
	if result := initialized["result"].(map[string]any); result["protocolVersion"] != "2025-03-26" || result["serverInfo"].(map[string]any)["name"] != "harflex" {
		t.Fatalf("initialize: %v", initialized)
	}
	if response, _ := post(t, server.URL(), token, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil); response.StatusCode != http.StatusAccepted {
		t.Fatalf("notification: %d", response.StatusCode)
	}
	_, listed := post(t, server.URL(), token, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, nil)
	if items := listed["result"].(map[string]any)["tools"].([]any); len(items) != 1 || items[0].(map[string]any)["name"] != "echo" {
		t.Fatalf("tools/list: %v", listed)
	}
	_, called := post(t, server.URL(), token, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`, nil)
	result := called["result"].(map[string]any)
	if result["isError"] != false || result["content"].([]any)[0].(map[string]any)["text"] != `{"a":1}` {
		t.Fatalf("tools/call: %v", called)
	}
	_, failed := post(t, server.URL(), token, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"x":"fail"}}}`, nil)
	if result := failed["result"].(map[string]any); result["isError"] != true || result["content"].([]any)[0].(map[string]any)["text"] != "bad: it failed" {
		t.Fatalf("failed call: %v", failed)
	}
	_, unknown := post(t, server.URL(), token, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope"}}`, nil)
	if unknown["error"] == nil {
		t.Fatalf("unknown tool: %v", unknown)
	}

	release()
	if response, _ := post(t, server.URL(), token, `{"jsonrpc":"2.0","id":6,"method":"ping"}`, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("released token: %d", response.StatusCode)
	}
}
