package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

type aliasApprovalRunner struct {
	journal   *eventJournal
	approvals int
}

func (*aliasApprovalRunner) Cancel() bool { return false }
func (r *aliasApprovalRunner) Prompt(ctx context.Context, _ string) error {
	call := agentcore.ToolCall{ID: "CALL-A", Name: "write", Arguments: json.RawMessage(`{"text":"A","number":9007199254740993}`)}
	if _, err := r.journal.Append(ctx, "aliases", "agent_session", "message.assistant", agentcore.Message{Role: agentcore.RoleAssistant, ToolCalls: []agentcore.ToolCall{call}}); err != nil {
		return err
	}
	request := agentcore.ApprovalRequest{ID: "APPROVAL-A", ToolCallID: call.ID, Name: call.Name, Risk: "write", Arguments: call.Arguments}
	if _, err := r.journal.Append(ctx, "aliases", "agent_session", "approval.requested", request); err != nil {
		return err
	}
	return &agentcore.ApprovalRequiredError{Approval: request}
}
func (r *aliasApprovalRunner) Approve(ctx context.Context, actual string, allow bool) error {
	if actual != "APPROVAL-A" || allow {
		return errors.New("approval alias was not translated")
	}
	r.approvals++
	for _, item := range []struct {
		typ  string
		data any
	}{
		{"approval.denied", map[string]string{"approvalId": actual, "toolCallId": "CALL-A"}},
		{"tool.skipped", map[string]string{"toolCallId": "CALL-A", "name": "write", "reason": "approval_denied"}},
		{"run.failed", map[string]string{"reason": "approval_denied"}},
	} {
		if _, err := r.journal.Append(ctx, "aliases", "agent_session", item.typ, item.data); err != nil {
			return err
		}
	}
	return agentcore.NewRunError("approval_denied", errors.New("denied"))
}

func TestPublicAliasesProtectIDsAndResolveApprovalOncePerOccurrence(t *testing.T) {
	s, db, _ := setup(t)
	journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte("A"))}
	runner := &aliasApprovalRunner{journal: journal}
	s.sessions["aliases"], s.journals["aliases"] = runner, journal
	var emitted []EventDTO
	SetEmitter(s, func(_ string, data any) { emitted = append(emitted, data.(EventDTO)) })
	seenApproval, seenTool := map[string]bool{}, map[string]bool{}
	for occurrence := range 2 {
		result, err := s.Prompt(PromptInput{SessionID: "aliases", Text: "write"})
		if err != nil || result.Approval == nil {
			t.Fatal("approval missing", err)
		}
		a := result.Approval
		if !strings.HasPrefix(a.ID, "approval-") || !strings.HasPrefix(a.ToolCallID, "tool-") || strings.Contains(a.ID, "A") || strings.Contains(a.ToolCallID, "A") {
			t.Fatal("operational IDs were redacted instead of aliased")
		}
		if seenApproval[a.ID] || seenTool[a.ToolCallID] {
			t.Fatal("reused internal IDs collided across occurrences")
		}
		seenApproval[a.ID], seenTool[a.ToolCallID] = true, true
		if _, err := s.Approve(ApprovalInput{SessionID: "aliases", ApprovalID: "APPROVAL-A", Allow: false}); !errors.Is(err, agentcore.ErrApprovalNotFound) {
			t.Fatal("actual ID accepted across public boundary", err)
		}
		if denied, err := s.Approve(ApprovalInput{SessionID: "aliases", ApprovalID: a.ID, Allow: false}); err != nil || denied.Reason != "approval_denied" {
			t.Fatal("public approval failed", err)
		}
		if _, err := s.Approve(ApprovalInput{SessionID: "aliases", ApprovalID: a.ID, Allow: false}); !errors.Is(err, agentcore.ErrApprovalNotFound) {
			t.Fatal("consumed alias was reused", err)
		}
		if runner.approvals != occurrence+1 {
			t.Fatal("approval executed more than once")
		}
	}
	stored, err := db.ListAfter(t.Context(), "aliases", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(emitted) {
		t.Fatal("DB/emitter diverged")
	}
	var currentTool, currentApproval string
	for i, event := range stored {
		if string(event.Data) != string(emitted[i].Data) || strings.Contains(string(event.Data), "A") {
			t.Fatal("internal ID/key persisted or emitted")
		}
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		switch event.Type {
		case "message.assistant":
			currentTool = data["toolCalls"].([]any)[0].(map[string]any)["id"].(string)
		case "approval.requested":
			currentApproval = data["approvalId"].(string)
			if data["toolCallId"] != currentTool || !seenApproval[currentApproval] {
				t.Fatal("approval/tool aliases lost correlation")
			}
		case "approval.denied":
			if data["approvalId"] != currentApproval || data["toolCallId"] != currentTool {
				t.Fatal("denial lost alias correlation")
			}
		case "tool.skipped":
			if data["toolCallId"] != currentTool {
				t.Fatal("tool terminal lost alias correlation")
			}
		}
	}
	Shutdown(s)
}

func TestOnlyPublicAliasesAreExemptFromLiveRedaction(t *testing.T) {
	s, db, _ := setup(t)
	journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte("a"))}
	t.Cleanup(journal.Close)
	event, err := journal.Append(t.Context(), "structure", "agent_session", "tool.completed", map[string]any{"toolCallId": "call-a", "name": "bash", "type": "data", "content": map[string]any{"id": "a", "type": "a", "number": json.Number("9007199254740993")}})
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data["name"].(string), "a") || strings.Contains(data["type"].(string), "a") {
		t.Fatal("untrusted name/type bypassed content redaction")
	}
	if strings.Contains(data["toolCallId"].(string), "a") {
		t.Fatal("alias contains known credential")
	}
	content := data["content"].(map[string]any)
	if content["id"] == "a" || content["type"] == "a" {
		t.Fatal("content fields incorrectly treated as structural IDs")
	}
}

func TestUnknownProviderToolNameCannotPublishCredential(t *testing.T) {
	key := profileInput().APIKey
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "call", Name: key, Arguments: json.RawMessage(`{}`)}}
	s, db, _, session := sessionSetup(t, provider)
	var emitted []EventDTO
	SetEmitter(s, func(_ string, data any) { emitted = append(emitted, data.(EventDTO)) })
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "run tool"})
	if err != nil || result.Status != RunFailed || result.Reason != "unknown_tool" || result.Approval != nil {
		t.Fatal("unknown tool did not fail safely", err)
	}
	stored, err := db.ListAfter(t.Context(), session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(emitted) {
		t.Fatal("journal/emitter mismatch")
	}
	for i, event := range stored {
		if strings.Contains(string(event.Data), key) || strings.Contains(string(emitted[i].Data), key) {
			t.Fatal("provider tool name leaked credential")
		}
	}
	if _, err := db.DB().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	var number int
	var name, path string
	if err := db.DB().QueryRow("PRAGMA database_list").Scan(&number, &name, &path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), key) {
		t.Fatal("provider tool name leaked into raw SQLite", err)
	}
}

func TestApprovalDTOAndEventRedactUntrustedName(t *testing.T) {
	s, db, _ := setup(t)
	key := "synthetic-untrusted-tool-name"
	journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte(key))}
	s.journals["approval-name"] = journal
	t.Cleanup(journal.Close)
	request := agentcore.ApprovalRequest{ID: "approval", ToolCallID: "call", Name: key, Risk: "write", Arguments: json.RawMessage(`{}`)}
	event, err := journal.Append(t.Context(), "approval-name", "agent_session", "approval.requested", request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.sessionResult("approval-name", "prompt", &agentcore.ApprovalRequiredError{Approval: request})
	if err != nil || result.Approval == nil {
		t.Fatal(err)
	}
	if strings.Contains(string(event.Data), key) || strings.Contains(result.Approval.Name, key) {
		t.Fatal("approval name bypassed redaction")
	}
	var data map[string]any
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	if result.Approval.ID != data["approvalId"] || result.Approval.ToolCallID != data["toolCallId"] {
		t.Fatal("name redaction changed public alias correlation")
	}
}

func TestLiveUnknownPayloadStringsDoNotReceiveStructuralExemptions(t *testing.T) {
	s, db, _ := setup(t)
	key := "synthetic-unknown-payload"
	journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte(key))}
	t.Cleanup(journal.Close)
	for _, event := range []struct {
		typ  string
		data any
	}{
		{"message.assistant", map[string]any{"id": key, "role": key, "toolCalls": []any{key, []any{key}}}},
		{"tool.updated", map[string]string{"toolCallId": "call", "stream": key, "text": "pending"}},
		{"run.completed", map[string]string{}},
	} {
		if _, err := journal.Append(t.Context(), "unknown", "agent_session", event.typ, event.data); err != nil {
			t.Fatal(err)
		}
	}
	items, err := db.ListAfter(t.Context(), "unknown", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range items {
		if strings.Contains(string(event.Data), key) {
			t.Fatal("unknown payload text bypassed redaction")
		}
	}
}

func TestRealSessionAliasesShortCredentialApprovalWithoutChangingExecution(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "deny", true: "allow"}[allow], func(t *testing.T) {
			s, _, _ := setup(t)
			provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "CALL-A", Name: "write", Arguments: json.RawMessage(`{"path":"result.txt","content":"A"}`)}}
			s.providerFactory = func(c openai.Config) (agentcore.Provider, error) { provider.key = c.APIKey; return provider, nil }
			root := t.TempDir()
			workspace, err := s.OpenWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}
			input := profileInput()
			input.APIKey = "A"
			if _, err := s.SaveProviderProfile(input); err != nil {
				t.Fatal(err)
			}
			session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: input.ID})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "write"})
			if err != nil || result.Approval == nil {
				t.Fatal(err)
			}
			if result.Approval.Name != "write" || strings.Contains(result.Approval.ID, "A") || strings.Contains(result.Approval.ToolCallID, "A") {
				t.Fatal("short credential broke public approval")
			}
			if _, err := s.Approve(ApprovalInput{SessionID: session.ID, ApprovalID: result.Approval.ID, Allow: allow}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Approve(ApprovalInput{SessionID: session.ID, ApprovalID: result.Approval.ID, Allow: allow}); !errors.Is(err, agentcore.ErrApprovalNotFound) {
				t.Fatal("reused approval", err)
			}
			data, err := os.ReadFile(filepath.Join(root, "result.txt"))
			if allow && (err != nil || string(data) != "A") {
				t.Fatal("public alias changed authorized tool execution", err)
			}
			if !allow && !os.IsNotExist(err) {
				t.Fatal("denied tool executed")
			}
		})
	}
}
