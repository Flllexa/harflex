package application

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "harflex-protocol-helper.exe" {
		if len(os.Args) == 2 && os.Args[1] == "--version" {
			fmt.Println("synthetic protocol helper 1.0")
		} else {
			data, err := os.ReadFile(filepath.Join(filepath.Dir(os.Args[0]), "protocol.jsonl"))
			if err != nil {
				syscall.Exit(2)
			}
			if _, err := os.Stdout.Write(data); err != nil {
				syscall.Exit(3)
			}
		}
		// The helper only emits synthetic fixture bytes. Skip the race runtime's
		// shutdown delay; the parent retains race checks over the real integration.
		syscall.Exit(0)
	}
	os.Exit(m.Run())
}

func protocolProcess(t *testing.T, fixture string) string {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "harflex-protocol-helper.exe")
	if err := os.Link(source, target); err != nil {
		input, err := os.Open(source)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("copy protocol helper: %v / %v", copyErr, closeErr)
		}
	}
	data, err := os.ReadFile(filepath.Join("..", "externalagent", "testdata", fixture+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "protocol.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestCLIProtocolPersistsThroughApplicationJournalAndSQLiteReopen(t *testing.T) {
	for _, tc := range []struct {
		adapter, fixture, externalID string
		factory                      func(string) externalagent.Adapter
		wantMessages                 map[string]string
	}{
		{"codex", "codex-exec", "thread_synthetic", externalagent.NewCodex, map[string]string{"item_1": "Resposta do Codex.\n"}},
		{"opencode", "opencode-run", "ses_synthetic", externalagent.NewOpenCode, map[string]string{"part_1": "", "part_2": "Segunda parte."}},
	} {
		t.Run(tc.adapter, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "protocol.db")
			db, err := sqlite.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			// A separate connection must see each emitted event already committed.
			observer, err := sqlite.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = observer.Close() })
			vault := &memorySecrets{values: map[secrets.Reference]string{}}
			adapter := tc.factory(protocolProcess(t, tc.fixture))
			service := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{tc.adapter: adapter}})
			t.Cleanup(func() { Shutdown(service) })
			workspace, err := service.OpenWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			session, err := service.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: tc.adapter})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := service.sessions[session.ID].(*externalagent.Session); !ok || service.journals[session.ID].store != db {
				t.Fatal("integration bypassed the external Session/application journal")
			}
			var emitted []EventDTO
			SetEmitter(service, func(_ string, payload any) {
				event := payload.(EventDTO)
				stored, err := observer.ListAfter(t.Context(), session.ID, event.Sequence-1)
				if err != nil || len(stored) != 1 || !reflect.DeepEqual(eventDTO(stored[0]), event) {
					t.Errorf("emission preceded commit or changed persisted data: event=%+v stored=%+v err=%v", event, stored, err)
				}
				emitted = append(emitted, event)
			})
			result, err := service.Prompt(PromptInput{SessionID: session.ID, Text: "Pergunta"})
			if err != nil || result.Status != RunCompleted {
				t.Fatalf("protocol run: result=%+v err=%v", result, err)
			}
			stored, err := db.ListAfter(t.Context(), session.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(stored) < 4 || stored[0].Type != "external.run.started" || stored[1].Type != "message.user" || stored[len(stored)-1].Type != "external.run.completed" {
				t.Fatalf("missing durable lifecycle: %+v", stored)
			}
			var prompt agentcore.Message
			if err := json.Unmarshal(stored[1].Data, &prompt); err != nil || prompt.Role != agentcore.RoleUser || prompt.Content != "Pergunta" {
				t.Fatalf("durable prompt=%+v err=%v", prompt, err)
			}
			var actual []any
			messages := make(map[string]string)
			if len(emitted) != len(stored) {
				t.Fatalf("persisted=%d emitted=%d", len(stored), len(emitted))
			}
			for index, event := range stored {
				if event.ID == "" || event.StreamID != session.ID || event.Sequence != int64(index+1) || !reflect.DeepEqual(eventDTO(event), emitted[index]) {
					t.Fatalf("journal/emitter mismatch at %d: %+v / %+v", index, event, emitted[index])
				}
				if event.Type != "external.event" {
					continue
				}
				var data any
				if err := json.Unmarshal(event.Data, &data); err != nil {
					t.Fatal(err)
				}
				actual = append(actual, data)
				var normalized externalagent.Event
				if err := json.Unmarshal(event.Data, &normalized); err != nil {
					t.Fatal(err)
				}
				if normalized.SessionID != tc.externalID {
					t.Fatalf("lost external session ID: %+v", normalized)
				}
				if normalized.Type == "assistant.message" {
					if normalized.Mode != "replace" || normalized.MessageID == "" {
						t.Fatalf("invalid persisted text identity/mode: %+v", normalized)
					}
					messages[normalized.MessageID] = normalized.Text
				}
			}
			if !reflect.DeepEqual(messages, tc.wantMessages) {
				t.Fatalf("persisted conversation=%q want=%q", messages, tc.wantMessages)
			}
			// These same exact persisted payloads are projected by frontend tests.
			golden, err := os.ReadFile(filepath.Join("..", "externalagent", "testdata", tc.adapter+"-events.json"))
			if err != nil {
				t.Fatal(err)
			}
			var expected []any
			if err := json.Unmarshal(golden, &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("SQLite payloads differ from frontend fixture: got=%+v want=%+v", actual, expected)
			}
			if vault.gets != 0 || vault.deletes != 0 || len(vault.values) != 0 {
				t.Fatal("CLI integration touched credentials")
			}
			Shutdown(service)
			if err := observer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sqlite.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			var recovered []events.Event
			err = reopened.WalkAfter(t.Context(), session.ID, 0, 100, 1<<20, func(event events.Event) error {
				recovered = append(recovered, event)
				return nil
			})
			if err != nil || !reflect.DeepEqual(recovered, stored) {
				t.Fatalf("durable replay changed after close/reopen: recovered=%+v err=%v", recovered, err)
			}
			record, err := reopened.GetSession(t.Context(), session.ID)
			if err != nil || record.Status != "completed" {
				t.Fatalf("durable session catalog=%+v err=%v", record, err)
			}
		})
	}
}
