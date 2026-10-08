package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
)

func TestJournalRedactsKnownCredentialBeforePersistenceAndEmission(t *testing.T) {
	s, db, _, session := sessionSetup(t, &fakeProvider{})
	key := profileInput().APIKey
	var emitted []EventDTO
	SetEmitter(s, func(_ string, data any) { emitted = append(emitted, data.(EventDTO)) })
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "prompt contains " + key}); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListAfter(t.Context(), session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if strings.Contains(string(item.Data), key) {
			t.Fatal("credential persisted in event payload")
		}
	}
	for _, item := range emitted {
		if strings.Contains(string(item.Data), key) {
			t.Fatal("credential emitted in event payload")
		}
	}
	if len(emitted) != len(items) {
		t.Fatal("persisted/emitted journal differs")
	}
}

type credentialEchoProvider struct {
	key     func(context.Context) (string, error)
	started chan string
	release <-chan struct{}
}

func (*credentialEchoProvider) ID() string { return "credential-echo" }
func (*credentialEchoProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true}
}
func (p *credentialEchoProvider) Stream(ctx context.Context, _ agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	events := make(chan agentcore.StreamEvent, 1)
	failures := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(failures)
		key, err := p.key(ctx)
		if err != nil {
			failures <- err
			return
		}
		if p.started != nil {
			p.started <- key
		}
		if p.release != nil {
			select {
			case <-p.release:
			case <-ctx.Done():
				failures <- ctx.Err()
				return
			}
		}
		events <- agentcore.StreamEvent{Type: "text_delta", Delta: key}
	}()
	return events, failures
}

func TestModelCredentialCallbackWorksWithAuthenticatedMCPAndRedactsBothKeys(t *testing.T) {
	const mcpKey = "synthetic-mcp-session-secret"
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+mcpKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		transport.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	s, db, _ := setup(t)
	s.providerFactory = func(cfg openai.Config) (agentcore.Provider, error) {
		return &credentialEchoProvider{key: cfg.APIKey}, nil
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configured, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: workspace.ID, Name: "Authenticated", Transport: "http", URL: httpServer.URL, Token: mcpKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConnectMCPServer(configured.ID); err != nil {
		t.Fatal(err)
	}
	profile := profileInput()
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	var emitted []EventDTO
	SetEmitter(s, func(_ string, data any) { emitted = append(emitted, data.(EventDTO)) })
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: mcpKey + " " + profile.APIKey})
	if err != nil || result.Status != RunCompleted {
		t.Fatalf("provider APIKey callback failed with MCP redaction: %+v %v", result, err)
	}
	events, err := db.ListAfter(t.Context(), session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if bytes.Contains(event.Data, []byte(mcpKey)) || bytes.Contains(event.Data, []byte(profile.APIKey)) {
			t.Fatal("credential persisted in session event")
		}
	}
	for _, event := range emitted {
		if bytes.Contains(event.Data, []byte(mcpKey)) || bytes.Contains(event.Data, []byte(profile.APIKey)) {
			t.Fatal("credential emitted in session event")
		}
	}
	destination := filepath.Join(t.TempDir(), "mcp-session-audit.jsonl")
	if _, err := s.ExportAudit(ExportAuditInput{SessionID: session.ID, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	audit, err := os.ReadFile(destination)
	if err != nil || bytes.Contains(audit, []byte(mcpKey)) || bytes.Contains(audit, []byte(profile.APIKey)) {
		t.Fatal("credential leaked to audit export", err)
	}
}

func TestConcurrentRotationKeepsCredentialAndJournalSnapshotTogether(t *testing.T) {
	s, db, _ := setup(t)
	firstStarted, release := make(chan string, 1), make(chan struct{})
	calls := 0
	s.providerFactory = func(c openai.Config) (agentcore.Provider, error) {
		calls++
		p := &credentialEchoProvider{key: c.APIKey}
		if calls == 1 {
			p.started, p.release = firstStarted, release
		}
		return p, nil
	}
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in := profileInput()
	in.APIKey = "synthetic-version-one"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	old, err := s.CreateSession(CreateSessionInput{WorkspaceID: w.ID, BackendID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	oldResult := make(chan error, 1)
	go func() { _, err := s.Prompt(PromptInput{SessionID: old.ID, Text: in.APIKey}); oldResult <- err }()
	if got := <-firstStarted; got != in.APIKey {
		t.Fatal("wrong first credential")
	}
	firstKey := in.APIKey
	in.APIKey = "synthetic-version-two"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.CreateSession(CreateSessionInput{WorkspaceID: w.ID, BackendID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if _, err := s.Prompt(PromptInput{SessionID: fresh.ID, Text: in.APIKey}); err != nil {
		t.Fatal(err)
	}
	if err := <-oldResult; err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range []string{old.ID, fresh.ID} {
		items, err := db.ListAfter(t.Context(), sessionID, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range items {
			if bytes.Contains(event.Data, []byte(firstKey)) || bytes.Contains(event.Data, []byte(in.APIKey)) {
				t.Fatal("rotation mixed journal and credential snapshots")
			}
		}
	}
}

func TestRedactedJournalSurvivesDeletedHistoricalKeyAndShutdownClearsBuffers(t *testing.T) {
	s, db, vault, session := sessionSetup(t, &fakeProvider{})
	profile, err := db.GetProviderProfile(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	journal := s.journals[session.ID]
	keyBuffer := journal.redactor.patterns[0]
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: profileInput().APIKey}); err != nil {
		t.Fatal(err)
	}
	if err := vault.Delete(t.Context(), secrets.Reference{Provider: profile.CredentialProvider, Account: profile.CredentialAccount}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "deleted.jsonl")
	if _, err := s.ExportAudit(ExportAuditInput{SessionID: session.ID, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || bytes.Contains(data, []byte(profileInput().APIKey)) {
		t.Fatal("deleted key invalidated pre-persistence redaction", err)
	}
	if _, err := db.DB().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	var dbPath string
	var number int
	var name string
	if err := db.DB().QueryRow("PRAGMA database_list").Scan(&number, &name, &dbPath); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(dbPath)
	if err != nil || bytes.Contains(data, []byte(profileInput().APIKey)) {
		t.Fatal("credential found in raw SQLite file", err)
	}
	Shutdown(s)
	if !bytes.Equal(keyBuffer, make([]byte, len(keyBuffer))) || len(s.sessions) != 0 || len(s.journals) != 0 {
		t.Fatal("shutdown retained credential buffers")
	}
}

func TestJournalInterleavedFlushCannotReconstructKnownCredential(t *testing.T) {
	for _, boundary := range []string{"usage.recorded", "message.assistant", "tool.started", "run.cancelled", "run.failed"} {
		t.Run(boundary, func(t *testing.T) {
			s, db, _ := setup(t)
			key := "synthetic-interleaved-key"
			journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte(key))}
			t.Cleanup(journal.Close)
			for _, event := range []struct {
				typ  string
				data any
			}{
				{"assistant.delta", map[string]string{"delta": key[:8]}},
				{boundary, map[string]string{"status": "done"}},
				{"assistant.delta", map[string]string{"delta": key[8:]}},
				{"run.completed", map[string]string{}},
			} {
				if _, err := journal.Append(t.Context(), "interleaved", "agent_session", event.typ, event.data); err != nil {
					t.Fatal(err)
				}
			}
			items, err := db.ListAfter(t.Context(), "interleaved", 0)
			if err != nil {
				t.Fatal(err)
			}
			combined := ""
			for _, event := range items {
				if event.Type == "assistant.delta" {
					var data map[string]string
					if err := json.Unmarshal(event.Data, &data); err != nil {
						t.Fatal(err)
					}
					combined += data["delta"]
				}
			}
			if strings.Contains(combined, key) {
				t.Fatal("interleaved flush reconstructed credential")
			}
		})
	}
}

func TestApprovalResponseRedactsCredentialWithoutChangingToolExecution(t *testing.T) {
	key := profileInput().APIKey
	arguments, err := json.Marshal(map[string]string{"path": "output.txt", "content": key})
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "call", Name: "write", Arguments: arguments}}
	s, db, _, session := sessionSetup(t, p)
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "write"})
	if err != nil || result.Approval == nil {
		t.Fatal("approval missing", err)
	}
	if strings.Contains(string(result.Approval.Arguments), key) {
		t.Fatal("approval response leaked credential")
	}
	if _, err := s.Approve(ApprovalInput{SessionID: session.ID, ApprovalID: result.Approval.ID, Allow: true}); err != nil {
		t.Fatal(err)
	}
	w, err := db.GetWorkspace(t.Context(), session.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(w.Path, "output.txt"))
	if err != nil || string(data) != key {
		t.Fatal("redaction changed authorized tool input", err)
	}
}

func TestJournalRedactsEveryPayloadStringPreservingStructure(t *testing.T) {
	s, db, _ := setup(t)
	key := "synthetic-\"-秘密-key"
	journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte(key))}
	t.Cleanup(journal.Close)
	var emitted EventDTO
	SetEmitter(s, func(_ string, data any) { emitted = data.(EventDTO) })
	for _, typ := range []string{"message.user", "message.assistant", "tool.finished", "external.event"} {
		payload := map[string]any{"text": key, "nested": []any{key, map[string]any{"diff": key, "details": key, "number": json.Number("9007199254740993"), "bool": true, "nil": nil}}}
		event, err := journal.Append(t.Context(), "semantic", "agent_session", typ, payload)
		if err != nil {
			t.Fatal(err)
		}
		if event.Type != typ || !bytes.Equal(event.Data, emitted.Data) {
			t.Fatal("journal/emitter mismatch")
		}
		var got map[string]any
		decoder := json.NewDecoder(bytes.NewReader(event.Data))
		decoder.UseNumber()
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		nested := got["nested"].([]any)
		details := nested[1].(map[string]any)
		if got["text"] != "[REDACTED]" || nested[0] != "[REDACTED]" || details["diff"] != "[REDACTED]" || details["details"] != "[REDACTED]" || details["number"] != json.Number("9007199254740993") || details["bool"] != true || details["nil"] != nil {
			t.Fatal("semantic payload not preserved")
		}
		if payload["text"] != key {
			t.Fatal("caller payload mutated")
		}
	}
}

func TestJournalStreamingRedactionEveryByteSplit(t *testing.T) {
	for _, key := range []string{"synthetic-stream-key", "á秘密🔑", "aaa"} {
		for split := 1; split < len(key); split++ {
			t.Run(strconv.Itoa(len(key))+"/"+strconv.Itoa(split), func(t *testing.T) {
				s, db, _ := setup(t)
				journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte(key))}
				t.Cleanup(journal.Close)
				var emitted []events.Event
				SetEmitter(s, func(_ string, data any) {
					d := data.(EventDTO)
					emitted = append(emitted, events.Event{Type: d.Type, Data: d.Data, Sequence: d.Sequence})
				})
				for _, chunk := range []string{"prefix " + key[:split], key[split:] + " suffix"} {
					if _, err := journal.Append(t.Context(), "split", "agent_session", "assistant.delta", map[string]string{"delta": chunk}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := journal.Append(t.Context(), "split", "agent_session", "run.completed", map[string]string{"reason": "done"}); err != nil {
					t.Fatal(err)
				}
				stored, err := db.ListAfter(t.Context(), "split", 0)
				if err != nil {
					t.Fatal(err)
				}
				for _, list := range [][]events.Event{stored, emitted} {
					var combined strings.Builder
					for i, event := range list {
						if event.Sequence != int64(i+1) {
							t.Fatal("noncontiguous journal")
						}
						if event.Type == "assistant.delta" {
							var p struct {
								Delta string `json:"delta"`
							}
							if err := json.Unmarshal(event.Data, &p); err != nil {
								t.Fatal(err)
							}
							combined.WriteString(p.Delta)
						}
					}
					if combined.String() != "prefix [REDACTED] suffix" || !utf8.ValidString(combined.String()) {
						t.Fatal("fragmented credential escaped or text changed")
					}
					if list[len(list)-1].Type != "run.completed" {
						t.Fatal("pending delta written after terminal")
					}
				}
			})
		}
	}
}

func TestJournalStreamingPreservesUTF8WithShortOrNoCredential(t *testing.T) {
	for _, key := range []string{"x", ""} {
		for _, text := range []string{"á", "秘", "🔑"} {
			for split := 1; split < len(text); split++ {
				t.Run("key-"+key+"/"+text+"/"+strconv.Itoa(split), func(t *testing.T) {
					s, db, _ := setup(t)
					journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte(key))}
					t.Cleanup(journal.Close)
					var emitted []events.Event
					SetEmitter(s, func(_ string, payload any) {
						dto := payload.(EventDTO)
						emitted = append(emitted, events.Event{Type: dto.Type, Data: dto.Data, Sequence: dto.Sequence})
					})
					for _, chunk := range []string{text[:split], text[split:]} {
						if _, err := journal.Append(t.Context(), "utf8", "agent_session", "assistant.delta", map[string]string{"delta": chunk}); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := journal.Append(t.Context(), "utf8", "agent_session", "run.completed", map[string]string{}); err != nil {
						t.Fatal(err)
					}
					stored, err := db.ListAfter(t.Context(), "utf8", 0)
					if err != nil {
						t.Fatal(err)
					}
					for _, list := range [][]events.Event{stored, emitted} {
						var combined strings.Builder
						for index, event := range list {
							if event.Sequence != int64(index+1) {
								t.Fatal("noncontiguous journal")
							}
							if event.Type == "assistant.delta" {
								var payload struct {
									Delta string `json:"delta"`
								}
								if err := json.Unmarshal(event.Data, &payload); err != nil {
									t.Fatal(err)
								}
								if !utf8.ValidString(payload.Delta) {
									t.Fatal("invalid UTF-8 delta")
								}
								combined.WriteString(payload.Delta)
							}
						}
						if combined.String() != text {
							t.Fatalf("fragmented rune changed: got %q, want %q", combined.String(), text)
						}
						if list[len(list)-1].Type != "run.completed" {
							t.Fatal("delta flushed after terminal")
						}
					}
				})
			}
		}
	}
}

func TestJournalRedactorFailureAndCloseNeverPersistOriginal(t *testing.T) {
	s, db, _ := setup(t)
	journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte("synthetic-close-key"))}
	emitted := 0
	SetEmitter(s, func(string, any) { emitted++ })
	if _, err := journal.Append(t.Context(), "closed", "agent_session", "message.user", make(chan string)); err == nil {
		t.Fatal("unsupported data accepted")
	}
	journal.Close()
	if _, err := journal.Append(context.Background(), "closed", "agent_session", "message.user", "synthetic-close-key"); err == nil {
		t.Fatal("closed redactor accepted original")
	}
	items, err := db.ListAfter(t.Context(), "closed", 0)
	if err != nil || len(items) != 0 || emitted != 0 {
		t.Fatal("failed redaction persisted or emitted", err)
	}
}

func TestAuditRetainsPublishedCredentialHistoryAfterRotation(t *testing.T) {
	s, db, _ := setup(t)
	persistedSession(t, s, db, "rotation")
	in := profileInput()
	in.APIKey = "synthetic-history-first"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	first := in.APIKey
	in.APIKey = "synthetic-history-second"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Append(t.Context(), "rotation", "agent_session", "message.user", map[string]string{"text": first + " " + in.APIKey}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "history.jsonl")
	if _, err := s.ExportAudit(ExportAuditInput{SessionID: "rotation", Destination: destination}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), first) || strings.Contains(string(data), in.APIKey) {
		t.Fatal("rotation removed historical redaction coverage")
	}
	var count int
	if err := db.DB().QueryRow("SELECT COUNT(*) FROM credential_references").Scan(&count); err != nil || count != 2 {
		t.Fatal("published inventory missing", count, err)
	}
	var payload struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.Data["text"] != "[REDACTED] [REDACTED]" {
		t.Fatal("invalid semantic redaction", err)
	}
}
