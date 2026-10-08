package application

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func appendCodexEvidence(t *testing.T, db *sqlite.Store, sessionID string, assistant externalagent.Event) {
	t.Helper()
	for _, item := range []struct {
		typ  string
		data any
	}{
		{"external.run.started", map[string]string{"adapter": "codex"}},
		{"message.user", agentcore.Message{Role: agentcore.RoleUser, Content: "Generate a clarification question"}},
		{"external.event", externalagent.Event{Type: "external.raw", SessionID: "thread_synthetic", Raw: json.RawMessage(`{"type":"thread.started","thread_id":"thread_synthetic"}`)}},
		{"external.session.bound", map[string]string{"sessionId": "thread_synthetic"}},
		{"external.event", externalagent.Event{Type: "external.raw", SessionID: "thread_synthetic", Raw: json.RawMessage(`{"type":"turn.started"}`)}},
		{"external.event", assistant},
		{"external.event", externalagent.Event{Type: "external.raw", SessionID: "thread_synthetic", Raw: json.RawMessage(`{"type":"turn.completed","usage":{"input_tokens":12,"output_tokens":8}}`)}},
		{"external.run.completed", map[string]string{"adapter": "codex"}},
	} {
		if _, err := db.Append(t.Context(), sessionID, "external_session", item.typ, item.data); err != nil {
			t.Fatal(err)
		}
	}
}

func cliEvidenceFixture(t *testing.T) (*Service, *sqlite.Store, string) {
	t.Helper()
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "codex-sdd-evidence"
	now := time.Now().UTC()
	if err := db.UpsertSession(t.Context(), catalog.SessionRecord{ID: sessionID, WorkspaceID: workspace.ID, BackendID: "codex", Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Shutdown(s) })
	return s, db, sessionID
}

func TestSDDEvidenceAcceptsOneCompletedCodexAssistantMessage(t *testing.T) {
	s, db, sessionID := cliEvidenceFixture(t)
	answer := `{"question":"Qual recurso é prioridade?"}`
	appendCodexEvidence(t, db, sessionID, externalagent.Event{Type: "assistant.message", Text: answer, MessageID: "item_1", Mode: "replace", SessionID: "thread_synthetic", Raw: json.RawMessage(`{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"{\"question\":\"Qual recurso é prioridade?\"}"}}`)})
	evidence, err := s.readSDDBrainstormEvidence(t.Context(), sessionID, "question")
	if err != nil || evidence.Question != "Qual recurso é prioridade?" {
		t.Fatalf("Codex assistant evidence: %+v %v", evidence, err)
	}
}

func TestSDDEvidenceRejectsCodexToolEvents(t *testing.T) {
	s, db, sessionID := cliEvidenceFixture(t)
	appendCodexEvidence(t, db, sessionID, externalagent.Event{Type: "external.raw", SessionID: "thread_synthetic", Raw: json.RawMessage(`{"type":"item.completed","item":{"type":"command_execution","text":"not allowed"}}`)})
	if _, _, err := s.readSDDReadOnlyEvidence(t.Context(), sessionID); err != errInvalidSDDOutput {
		t.Fatalf("Codex tool event accepted as SDD evidence: %v", err)
	}
}
