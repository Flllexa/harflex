package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/tools"
)

func harflexToolNamed(t *testing.T, items []tools.Tool, name string) tools.Tool {
	t.Helper()
	for _, item := range items {
		if item.Spec().Name == name {
			return item
		}
	}
	t.Fatalf("tool %s missing", name)
	return nil
}

func TestHarflexToolsCreateAndReadPipelinesOfTheirOwnProjectOnly(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	s.emit = func(name string, _ any) { events = append(events, name) }
	items := s.harflexTools(workspace.ID, "")

	created, err := harflexToolNamed(t, items, harflexCreatePipelineTool).Execute(t.Context(), json.RawMessage(`{"discovery":"# Baixa parcial\n\nPermitir baixar parte de uma parcela, com testes."}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Created  bool
		Pipeline harflexPipelineSummary
	}
	if err := json.Unmarshal(created.Content, &result); err != nil || !result.Created || result.Pipeline.Title != "Baixa parcial" || result.Pipeline.CurrentPhase == "" {
		t.Fatalf("create: %s %v", created.Content, err)
	}
	if len(events) != 1 || events[0] != "harflex:pipeline" {
		t.Fatalf("the screen is told about the new pipeline: %v", events)
	}

	listed, err := harflexToolNamed(t, items, harflexListPipelinesTool).Execute(t.Context(), json.RawMessage(`{}`), nil)
	if err != nil || !strings.Contains(string(listed.Content), result.Pipeline.ID) {
		t.Fatalf("list: %s %v", listed.Content, err)
	}
	read, err := harflexToolNamed(t, items, harflexGetPipelineTool).Execute(t.Context(), json.RawMessage(`{"pipelineId":"`+result.Pipeline.ID+`","includeDocuments":true}`), nil)
	if err != nil || !strings.Contains(string(read.Content), "Permitir baixar parte") {
		t.Fatalf("get: %s %v", read.Content, err)
	}
	// The read also says every phase and where the work stopped, in plain text.
	if !strings.Contains(string(read.Content), "Where it stands") || !strings.Contains(string(read.Content), "Discovery: ") {
		t.Fatalf("get without the work status: %s", read.Content)
	}

	var failure *agentcore.ToolFailure
	_, err = harflexToolNamed(t, s.harflexTools(other.ID, ""), harflexGetPipelineTool).Execute(t.Context(), json.RawMessage(`{"pipelineId":"`+result.Pipeline.ID+`"}`), nil)
	if !errors.As(err, &failure) || failure.Code != "not_found" {
		t.Fatalf("another project's pipeline stays hidden: %v", err)
	}
	_, err = harflexToolNamed(t, items, harflexCreatePipelineTool).Execute(t.Context(), json.RawMessage(`{"discovery":"curta"}`), nil)
	if !errors.As(err, &failure) || failure.Code != "invalid_arguments" {
		t.Fatalf("a too short discovery is recoverable: %v", err)
	}
}

func TestPlatformMCPServesTheSessionsProjectTools(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	url, token, err := s.platformMCPEndpoint("session-1", workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.platformMCP.Close()
	if again, sameToken, _ := s.platformMCPEndpoint("session-1", workspace.ID); again != url || sameToken != token {
		t.Fatal("a session keeps its token")
	}
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var listed struct {
		Result struct{ Tools []struct{ Name string } }
	}
	_ = json.NewDecoder(response.Body).Decode(&listed)
	names := []string{}
	for _, item := range listed.Result.Tools {
		names = append(names, item.Name)
	}
	if strings.Join(names, ",") != "harflex_create_pipeline,harflex_list_pipelines,harflex_get_pipeline" {
		t.Fatalf("tools: %v", names)
	}
}

// conversationOnly drops the system instructions every chat now starts with.
func conversationOnly(messages []agentcore.Message) []agentcore.Message {
	for len(messages) > 0 && messages[0].Role == agentcore.RoleSystem {
		messages = messages[1:]
	}
	return messages
}
