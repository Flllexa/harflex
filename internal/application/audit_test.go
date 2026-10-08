package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/secrets"
)

func auditItems() []events.Event {
	return []events.Event{{ID: "one", StreamID: "session", Sequence: 1, Type: "message.user", Data: json.RawMessage(`{"text":"first"}`), CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, {ID: "two", StreamID: "session", Sequence: 2, Type: "run.completed", Data: json.RawMessage(`{}`), CreatedAt: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)}}
}

func TestExportJSONLAtomicOrderedAndPrivate(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(dest, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	items := auditItems()
	called := 0
	if err := ExportJSONL(t.Context(), items, dest, func(line []byte) []byte {
		old, err := os.ReadFile(dest)
		if err != nil || string(old) != "old" {
			t.Error("destination replaced before export completed")
		}
		called++
		return bytes.ReplaceAll(line, []byte("first"), []byte("redacted"))
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if called != 2 || len(lines) != 2 || !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("not ordered JSONL", string(data))
	}
	for i, line := range lines {
		var event events.Event
		if err := json.Unmarshal(line, &event); err != nil || event.Sequence != int64(i+1) {
			t.Fatal(string(line), err)
		}
	}
	if bytes.Contains(data, []byte("first")) || !bytes.Contains(data, []byte("redacted")) {
		t.Fatal("redactor not applied")
	}
	if info, err := os.Stat(dest); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		t.Fatal(info, err)
	}
	assertNoAuditTemp(t, filepath.Dir(dest))
}

func assertNoAuditTemp(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".harflex-audit-*"))
	if err != nil || len(matches) != 0 {
		t.Fatal("temporary export leaked", matches, err)
	}
}

func TestExportJSONLFailureLeavesDestinationUntouched(t *testing.T) {
	for _, failure := range []string{"cancel-before", "cancel-during", "invalid-redactor", "invalid-event", "rename"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "audit.jsonl")
			if err := os.WriteFile(dest, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			items := auditItems()
			var redact func([]byte) []byte
			switch failure {
			case "cancel-before":
				cancel()
			case "cancel-during":
				redact = func(b []byte) []byte { cancel(); return b }
			case "invalid-redactor":
				redact = func([]byte) []byte { return []byte("sensitive invalid JSON") }
			case "invalid-event":
				items[1].Data = json.RawMessage(`{"broken`)
			case "rename":
				redact = func(b []byte) []byte {
					if _, err := os.Stat(dest + ".saved"); os.IsNotExist(err) {
						if err := os.Rename(dest, dest+".saved"); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(dest, 0700); err != nil {
							t.Fatal(err)
						}
					}
					return b
				}
			}
			err := ExportJSONL(ctx, items, dest, redact)
			if err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatal("unsafe successful export", err)
			}
			if strings.HasPrefix(failure, "cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			readPath := dest
			if failure == "rename" {
				readPath += ".saved"
			}
			data, readErr := os.ReadFile(readPath)
			if readErr != nil || string(data) != "old" {
				t.Fatal("original lost", string(data), readErr)
			}
			assertNoAuditTemp(t, dir)
		})
	}
}

func TestExportJSONLValidatesDestinationAndCompactsRedactorOutput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "parent-file")
	os.WriteFile(file, nil, 0600)
	for _, dest := range []string{"", " ", dir, filepath.Join(dir, "missing", "file"), filepath.Join(file, "child")} {
		if err := ExportJSONL(t.Context(), auditItems(), dest, nil); err == nil {
			t.Fatalf("accepted invalid destination %q", dest)
		}
	}
	dest := filepath.Join(dir, "multiline.jsonl")
	if err := ExportJSONL(t.Context(), auditItems(), dest, func(b []byte) []byte {
		var out bytes.Buffer
		if err := json.Indent(&out, b, "", "  "); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	if bytes.Count(data, []byte("\n")) != 2 {
		t.Fatal("redactor broke JSONL framing")
	}
	empty := filepath.Join(dir, "empty.jsonl")
	if err := ExportJSONL(t.Context(), nil, empty, nil); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(empty)
	if len(data) != 0 {
		t.Fatal(string(data))
	}
}

func TestServiceAuditPaginatesAndRedactsActiveCredentials(t *testing.T) {
	s, db, _ := setup(t)
	persistedSession(t, s, db, "session")
	key := `test-quote"\-secret` // A synthetic value verifies JSON escaping.
	input := profileInput()
	input.APIKey = key
	if _, err := s.SaveProviderProfile(input); err != nil {
		t.Fatal(err)
	}
	for i := range 1005 {
		if _, err := db.Append(t.Context(), "session", "agent_session", "message.user", map[string]any{"index": i, "text": key}); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(t.TempDir(), "audit.jsonl")
	path, err := s.ExportAudit(ExportAuditInput{SessionID: "session", Destination: dest})
	if err != nil || path != dest {
		t.Fatal(path, err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(lines) != 1005 {
		t.Fatal("history truncated", len(lines))
	}
	for i, line := range lines {
		var event events.Event
		if err := json.Unmarshal(line, &event); err != nil || event.Sequence != int64(i+1) {
			t.Fatal("unordered history", i, err)
		}
		var value map[string]any
		if err := json.Unmarshal(event.Data, &value); err != nil || value["text"] == key {
			t.Fatal("secret not redacted")
		}
	}
}

type auditSecretsFailure struct{ secrets.Store }

func (auditSecretsFailure) Get(context.Context, secrets.Reference) (string, error) {
	return "", errors.New("private-vault-diagnostic")
}

func TestServiceAuditSecretFailureAndMissingReferences(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing", false: "failure"}[missing], func(t *testing.T) {
			s, db, v := setup(t)
			persistedSession(t, s, db, "session", "run.completed")
			if _, err := s.SaveProviderProfile(profileInput()); err != nil {
				t.Fatal(err)
			}
			v.values = map[secrets.Reference]string{}
			if !missing {
				s.secrets = auditSecretsFailure{v}
			}
			dest := filepath.Join(t.TempDir(), "audit.jsonl")
			os.WriteFile(dest, []byte("old"), 0600)
			_, err := s.ExportAudit(ExportAuditInput{SessionID: "session", Destination: dest})
			if missing && err != nil {
				t.Fatal(err)
			}
			if !missing {
				data, _ := os.ReadFile(dest)
				if err == nil || strings.Contains(err.Error(), "private-vault") || string(data) != "old" {
					t.Fatal(err, string(data))
				}
			}
		})
	}
}

func TestServiceAuditNoSecretsAndLifecycle(t *testing.T) {
	s, db, _ := setup(t)
	persistedSession(t, s, db, "session", "run.completed")
	dest := filepath.Join(t.TempDir(), "audit.jsonl")
	if _, err := s.ExportAudit(ExportAuditInput{SessionID: "missing", Destination: dest}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal(err)
	}
	if _, err := s.ExportAudit(ExportAuditInput{SessionID: "session", Destination: dest}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(dest)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s.ctx = ctx
	if _, err := s.ExportAudit(ExportAuditInput{SessionID: "session", Destination: dest}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(dest)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("cancelled export replaced file")
	}
}

func TestAuditRedactionUnionsOverlapsAndPreservesUTF8(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		patterns          [][]byte
	}{
		{"overlap", "prefix abcdefgh suffix", "prefix [REDACTED] suffix", [][]byte{[]byte("abcd"), []byte("cdef"), []byte("efgh")}},
		{"self-overlap", "aaaaa", "[REDACTED]", [][]byte{[]byte("aaa")}},
		{"short", "x/Ω", "[REDACTED]/Ω", [][]byte{[]byte("x")}},
		{"cut-utf8", "á秘密z", "á[REDACTED]z", [][]byte{[]byte("秘密")[1:5]}},
		{"identity", "plain Ω", "plain Ω", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte(tc.input)
			got := redactAuditBytes(input, tc.patterns)
			if string(got) != tc.want || !utf8.Valid(got) {
				t.Fatalf("redaction %q want %q", got, tc.want)
			}
			if len(got) > 0 {
				got[0] = '!'
			}
			if string(input) != tc.input {
				t.Fatal("redactor mutated input")
			}
		})
	}
}

func TestRedactionMarkerCannotReintroduceKnownCredential(t *testing.T) {
	for _, key := range []string{"[REDACTED]", "REDACTED", "E"} {
		if got := redactAuditBytes([]byte("before "+key+" after"), [][]byte{[]byte(key)}); bytes.Contains(got, []byte(key)) {
			t.Fatal("redaction marker reintroduced known credential")
		}
	}
}

func TestAuditRedactsHistoricalIDsAndNamesWithoutChangingStoredEvents(t *testing.T) {
	s, db, _ := setup(t)
	persistedSession(t, s, db, "historical-ids")
	input := profileInput()
	input.APIKey = "synthetic-historical-secret"
	if _, err := s.SaveProviderProfile(input); err != nil {
		t.Fatal(err)
	}
	key := input.APIKey
	for _, typ := range []string{"message.assistant", "approval.requested", "tool.completed"} {
		payload := map[string]any{"id": key, "toolCallId": key, "approvalId": key, "name": key, "type": key, "number": json.Number("9007199254740993"), "toolCalls": []any{map[string]any{"id": key, "name": key, "arguments": map[string]string{"text": key}}}}
		if _, err := db.Append(t.Context(), "historical-ids", "agent_session", typ, payload); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "historical.jsonl")
	if _, err := s.ExportAudit(ExportAuditInput{SessionID: "historical-ids", Destination: destination}); err != nil {
		t.Fatal(err)
	}
	exported, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(exported, []byte(key)) {
		t.Fatal("historical ID/name bypassed audit redaction")
	}
	lines := bytes.Split(bytes.TrimSpace(exported), []byte("\n"))
	if len(lines) != 3 {
		t.Fatal("audit event count changed")
	}
	for i, line := range lines {
		var event events.Event
		if err := json.Unmarshal(line, &event); err != nil || event.Sequence != int64(i+1) || event.Type != []string{"message.assistant", "approval.requested", "tool.completed"}[i] {
			t.Fatal("audit envelope changed", err)
		}
		if !bytes.Contains(event.Data, []byte(`"number":9007199254740993`)) {
			t.Fatal("audit number changed")
		}
	}
	stored, err := db.ListAfter(t.Context(), "historical-ids", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range stored {
		if !bytes.Contains(event.Data, []byte(key)) {
			t.Fatal("audit mutated historical database")
		}
	}
}

func TestServiceAuditRedactsAlternateJSONSecretEscapes(t *testing.T) {
	s, db, _ := setup(t)
	persistedSession(t, s, db, "session")
	in := profileInput()
	in.APIKey = "synthetic-secret"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Append(t.Context(), "session", "agent_session", "message.user", json.RawMessage(`{"text":"\u0073ynthetic-secret"}`)); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "audit.jsonl")
	if _, err := s.ExportAudit(ExportAuditInput{SessionID: "session", Destination: dest}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	var event events.Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Text == in.APIKey {
		t.Fatal("alternate JSON escaping bypassed credential redaction")
	}
}

func TestServiceAuditRedactsDecodedStringsWithoutChangingJSONStructure(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		credentials      []string
	}{
		{"escape-boundary", "\nsecret", "\nsecret", []string{"nsecret"}},
		{"newline", "\nsecret", "[REDACTED]", []string{"\nsecret"}},
		{"quote", "a\"secret", "[REDACTED]", []string{"a\"secret"}},
		{"backslash", `a\secret`, "[REDACTED]", []string{`a\secret`}},
		{"tab", "a\tsecret", "[REDACTED]", []string{"a\tsecret"}},
		{"unicode", "á秘密🔑", "[REDACTED]", []string{"á秘密🔑"}},
		{"overlap", "abcdefgh", "[REDACTED]", []string{"abcd", "cdef", "efgh"}},
		{"structural-key", "type", "[REDACTED]", []string{"type"}},
		{"envelope-value", "message.user", "[REDACTED]", []string{"message.user"}},
		{"numeric-text", "1", "[REDACTED]", []string{"1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, vault := setup(t)
			persistedSession(t, s, db, "session")
			for i, credential := range tc.credentials {
				input := profileInput()
				input.ID = "profile-" + strconv.Itoa(i)
				if _, err := s.SaveProviderProfile(input); err != nil {
					t.Fatal(err)
				}
				profile, err := db.GetProviderProfile(t.Context(), input.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := vault.Put(t.Context(), secrets.Reference{Provider: profile.CredentialProvider, Account: profile.CredentialAccount}, credential); err != nil {
					t.Fatal(err)
				}
			}
			payload := map[string]any{"text": tc.text, "nested": []any{tc.text, map[string]any{"type": tc.text, "number": json.Number("9007199254740993"), "decimal": json.Number("1.25"), "flag": true, "nil": nil}}}
			before, err := db.Append(t.Context(), "session", "agent_session", "message.user", payload)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "audit.jsonl")
			if _, err := s.ExportAudit(ExportAuditInput{SessionID: "session", Destination: destination}); err != nil {
				t.Fatal("semantic redaction failed", err)
			}
			line, err := os.ReadFile(destination)
			if err != nil || !json.Valid(line) {
				t.Fatal("invalid exported JSON", err)
			}
			var after events.Event
			if err := json.Unmarshal(line, &after); err != nil {
				t.Fatal(err)
			}
			if before.ID != after.ID || before.StreamID != after.StreamID || before.Type != after.Type || before.Sequence != after.Sequence || !before.CreatedAt.Equal(after.CreatedAt) {
				t.Fatal("redaction changed the event envelope")
			}
			decoder := json.NewDecoder(bytes.NewReader(after.Data))
			decoder.UseNumber()
			var got map[string]any
			if err := decoder.Decode(&got); err != nil {
				t.Fatal(err)
			}
			nested, ok := got["nested"].([]any)
			if !ok || len(nested) != 2 {
				t.Fatal("redaction changed payload structure")
			}
			object, ok := nested[1].(map[string]any)
			if !ok {
				t.Fatal("redaction changed nested object")
			}
			if got["text"] != tc.want || nested[0] != tc.want || object["type"] != tc.want {
				t.Fatal("decoded secret was not redacted exactly")
			}
			if object["number"] != json.Number("9007199254740993") || object["decimal"] != json.Number("1.25") || object["flag"] != true || object["nil"] != nil {
				t.Fatal("redaction changed non-string values")
			}
		})
	}
}
