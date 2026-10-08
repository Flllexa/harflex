package application

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func toolMessages(messages []agentcore.Message) []string {
	var contents []string
	for _, message := range messages {
		if message.Role == agentcore.RoleTool {
			contents = append(contents, message.Content)
		}
	}
	return contents
}

// The Coder reads a file the task is about to create. That is a question with an answer, not the end of
// the run, and the answer must be what the model reads again after the session is reopened.
func TestReadingAFileThatDoesNotExistYetLetsTheRunGoOnAndSurvivesReopening(t *testing.T) {
	p := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "read-call", Name: "read", Arguments: json.RawMessage(`{"path":"index.html","offset":1,"limit":300}`)}}
	first, db, vault, session := sessionSetup(t, p)
	workspace, err := db.GetWorkspace(t.Context(), session.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := first.Prompt(PromptInput{session.ID, "create the todo page in index.html"})
	if err != nil || result.Status != RunCompleted {
		t.Fatalf("a missing file must not end the run: %+v %v", result, err)
	}
	want := `{"error":"not_found","message":"\"index.html\" does not exist"}`
	if got := toolMessages(p.request.Messages); len(got) != 1 || got[0] != want {
		t.Fatalf("the model must read the failure as the tool's result, got %q", got)
	}

	events, err := first.ListEvents(ListEventsInput{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	var failed struct {
		ErrorCode   string `json:"errorCode"`
		Error       string `json:"error"`
		Recoverable bool   `json:"recoverable"`
	}
	for _, event := range events {
		types = append(types, event.Type)
		if strings.Contains(string(event.Data), workspace.Path) {
			t.Fatalf("an event carries the private path of the project: %s", event.Data)
		}
		if event.Type == "tool.failed" {
			if err := json.Unmarshal(event.Data, &failed); err != nil {
				t.Fatal(err)
			}
		}
	}
	if failed.ErrorCode != "not_found" || failed.Error != `"index.html" does not exist` || !failed.Recoverable {
		t.Fatalf("failed event = %+v", failed)
	}
	if types[len(types)-1] != "run.completed" {
		t.Fatalf("the run must end completed, events = %v", types)
	}

	Shutdown(first)
	next := &fakeProvider{}
	second := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(c openai.Config) (agentcore.Provider, error) { next.key = c.APIKey; return next, nil }})
	if err := second.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := second.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID}); err != nil {
		t.Fatal(err)
	}
	if result, err := second.Prompt(PromptInput{session.ID, "and now?"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("reopened session: %+v %v", result, err)
	}
	if got := toolMessages(next.request.Messages); len(got) != 1 || got[0] != want {
		t.Fatalf("the reopened history must hold the very same tool message, got %q", got)
	}
}

// An error nobody classified still ends the run and shows nothing but a safe code.
func TestAnUnclassifiedToolErrorStillEndsTheRun(t *testing.T) {
	// A call to a tool that does not exist is rejected by the session itself, before any tool runs.
	p := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "ghost-call", Name: "does_not_exist", Arguments: json.RawMessage(`{}`)}}
	s, _, _, session := sessionSetup(t, p)
	result, err := s.Prompt(PromptInput{session.ID, "use a tool that is not there"})
	if err == nil && result.Status == RunCompleted {
		t.Fatalf("an unknown tool must not complete the run: %+v", result)
	}
	events, listErr := s.ListEvents(ListEventsInput{SessionID: session.ID})
	if listErr != nil {
		t.Fatal(listErr)
	}
	for _, event := range events {
		if strings.Contains(string(event.Data), `"recoverable"`) {
			t.Fatalf("a failure nobody classified was marked recoverable: %s", event.Data)
		}
	}
}
