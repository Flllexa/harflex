package externalagent

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// Synthetic protocol-shaped records; never captured credentials or provider output.
//
//go:embed testdata/*.jsonl
var protocolFixtures embed.FS

func TestProtocolProcessNormalizesCanonicalOutput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		factory   func(string) Adapter
		want      []string
		sessionID string
	}{
		{"codex", NewCodex, []string{"Resposta do Codex.\n"}, "thread_synthetic"},
		{"opencode", NewOpenCode, []string{"Olá", "Olá mundo", "Olá", "Segunda parte."}, "ses_synthetic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.factory(helper(t, tc.name+"-protocol"))
			es, err := collect(t, t.Context(), a, Request{CWD: t.TempDir(), Prompt: "Pergunta"})
			if err != nil {
				t.Fatal(err)
			}
			var texts []string
			for _, e := range es {
				if e.Text != "" {
					texts = append(texts, e.Text)
				}
				if !json.Valid(e.Raw) {
					t.Fatalf("invalid raw: %+v", e)
				}
			}
			if !reflect.DeepEqual(texts, tc.want) || es[0].SessionID != tc.sessionID {
				t.Fatalf("texts=%q session=%q want=%q / %q", texts, es[0].SessionID, tc.want, tc.sessionID)
			}
		})
	}
}

func TestProtocolSessionPersistsFixtureAndResumesSameConversation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		factory func(string) Adapter
	}{{"codex", NewCodex}, {"opencode", NewOpenCode}} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recording{}
			s := NewSession("session", tc.factory(helper(t, tc.name+"-protocol")), r, Request{CWD: t.TempDir()})
			if err := s.Prompt(t.Context(), "Pergunta"); err != nil {
				t.Fatal(err)
			}
			if r.types[0] != "external.run.started" || r.types[1] != "message.user" || r.types[len(r.types)-1] != "external.run.completed" {
				t.Fatal(r.types)
			}
			var actual []json.RawMessage
			for i, typ := range r.types {
				if typ != "external.event" {
					continue
				}
				e := r.data[i].(Event)
				if bytes.Contains(e.Raw, []byte(`"args"`)) {
					continue
				}
				b, err := json.Marshal(e)
				if err != nil {
					t.Fatal(err)
				}
				actual = append(actual, b)
			}
			want, err := os.ReadFile("testdata/" + tc.name + "-events.json")
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			var gotData, wantData any
			if err := json.Unmarshal(b, &gotData); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(want, &wantData); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotData, wantData) {
				t.Fatalf("persisted events differ:\n%s\nwant:\n%s", b, want)
			}
			before := len(r.types)
			err = s.Prompt(t.Context(), "Segunda pergunta")
			if err != nil {
				t.Fatal(err)
			}
			var args []string
			for i := before; i < len(r.types); i++ {
				if r.types[i] == "external.event" {
					var echo struct{ Args []string }
					_ = json.Unmarshal(r.data[i].(Event).Raw, &echo)
					if echo.Args != nil {
						args = echo.Args
					}
				}
			}
			resumeArgs := strings.Join(args, " ")
			if tc.name == "codex" {
				if !strings.Contains(resumeArgs, "--sandbox workspace-write") || !strings.Contains(resumeArgs, "-C "+s.request.CWD+" --skip-git-repo-check resume thread_synthetic -") {
					t.Fatalf("Codex resume args=%q", args)
				}
			} else if !strings.Contains(resumeArgs, "--session ses_synthetic") {
				t.Fatalf("OpenCode resume args=%q", args)
			}
		})
	}
}

func TestProtocolSessionRecordsExternalThreadBindingOnlyOnce(t *testing.T) {
	recorder := &recording{}
	session := NewSession("session", NewCodex(helper(t, "codex-protocol")), recorder, Request{CWD: t.TempDir()})
	if err := session.Prompt(t.Context(), "Pergunta"); err != nil {
		t.Fatal(err)
	}
	bindings := 0
	for _, typ := range recorder.types {
		if typ == "external.session.bound" {
			bindings++
		}
	}
	if bindings != 1 {
		t.Fatalf("journal recorded %d session bindings, want exactly one: %v", bindings, recorder.types)
	}
}

func TestProtocolConcurrentRunsIsolateSnapshots(t *testing.T) {
	cwd := t.TempDir()
	for _, tc := range []struct {
		adapter Adapter
		want    []string
	}{
		{NewCodex(helper(t, "codex-protocol")), []string{"Resposta do Codex.\n"}},
		{NewOpenCode(helper(t, "opencode-protocol")), []string{"Olá", "Olá mundo", "Olá", "Segunda parte."}},
	} {
		for i := range 3 {
			t.Run(tc.adapter.ID()+"/"+string(rune('a'+i)), func(t *testing.T) {
				t.Parallel()
				es, err := collect(t, context.Background(), tc.adapter, Request{CWD: cwd, Prompt: "Pergunta"})
				if err != nil {
					t.Fatal(err)
				}
				var texts []string
				for _, e := range es {
					if e.Text != "" {
						texts = append(texts, e.Text)
					}
				}
				if !reflect.DeepEqual(texts, tc.want) {
					t.Fatal(texts)
				}
			})
		}
	}
}

func TestProtocolBoundariesAndRawOwnership(t *testing.T) {
	for _, factory := range []func() eventNormalizer{newCodexNormalizer, newOpenCodeNormalizer} {
		normalize := factory()
		for _, line := range []string{
			`{"type":"text","text":"ROOT_ONLY","sessionID":"../invalid"}`,
			`{"type":"text","part":{"type":"text","text":null}}`,
			`{"type":"item.completed","item":{"type":"agent_message","text":42}}`,
			`{"type":"turn.failed","error":{"message":"RAW_ONLY"}}`,
			`{"type":"error","text":"RAW_ONLY"}`,
			`{"type":"text","part":{"type":"tool","text":"RAW_ONLY"}}`,
			`{"type":"future","text":"RAW_ONLY"}`,
			`{"type":"text","part":{"type":"text","text":"` + strings.Repeat("x", maxLine) + `"}}`,
		} {
			raw := []byte(line)
			e := normalize(raw)
			if e.Text != "" || e.Type != "external.raw" || e.SessionID != "" {
				t.Fatalf("unexpected protocol projection: %+v", e)
			}
			raw[0] = 'x'
			if string(e.Raw) != line {
				t.Fatal("raw aliases source bytes")
			}
		}
		e := normalize([]byte{'b', 0xff})
		if e.Type != "external.stdout" || !utf8.ValidString(e.Text) {
			t.Fatal(e)
		}
	}
}

func TestOpenCodeMissingAndInvalidIdentity(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("x", 257), "../unsafe"} {
		b, _ := json.Marshal(map[string]any{"type": "text", "sessionID": id, "part": map[string]any{"id": id, "type": "text", "text": "answer"}})
		e := newOpenCodeNormalizer()(b)
		if e.Type != "assistant.message" || e.Text != "answer" || e.SessionID != "" || e.MessageID != "" {
			t.Fatal(e)
		}
	}
	normalize := newOpenCodeNormalizer()
	for _, line := range []string{
		`{"type":"text","part":{"messageID":"message_1","type":"text","text":"A"}}`,
		`{"type":"text","part":{"messageID":"message_1","type":"text","text":"AB"}}`,
	} {
		e := normalize([]byte(line))
		if e.MessageID != "message_1" {
			t.Fatal(e)
		}
	}
}

func TestOpenCodeSnapshotUpdatesNeverSplitText(t *testing.T) {
	normalize := newOpenCodeNormalizer()
	for _, text := range []string{"synthetic-", "synthetic-secret"} {
		line, _ := json.Marshal(map[string]any{"type": "text", "part": map[string]any{"id": "part_1", "type": "text", "text": text}})
		e := normalize(line)
		if e.Text != text || e.Mode != "replace" {
			t.Fatalf("snapshot fragmented: text=%q mode=%q", e.Text, e.Mode)
		}
	}
}

func TestOpenCodeLaterSnapshotsReplaceShorterAndEmptyText(t *testing.T) {
	normalize := newOpenCodeNormalizer()
	for index, text := range []string{"Olá mundo", "Olá", "", "Outra resposta", "Olá"} {
		line, _ := json.Marshal(map[string]any{"type": "text", "part": map[string]any{
			"id": "part_1", "type": "text", "text": text, "time": map[string]int{"end": index + 3},
		}})
		e := normalize(line)
		if e.Type != "assistant.message" || e.Mode != "replace" || e.MessageID != "part_1" || e.Text != text {
			t.Fatalf("later snapshot ignored: index=%d got=%+v want text=%q", index, e, text)
		}
		if duplicate := normalize(line); duplicate.Type != "external.raw" || duplicate.Text != "" {
			t.Fatalf("identical snapshot duplicated: %+v", duplicate)
		}
	}
}

func TestProtocolEmptyTextWithoutIdentityRemainsRaw(t *testing.T) {
	for _, tc := range []struct {
		normalize eventNormalizer
		line      string
	}{
		{newOpenCodeNormalizer(), `{"type":"text","part":{"type":"text","text":""}}`},
		{newCodexNormalizer(), `{"type":"item.completed","item":{"type":"agent_message","text":""}}`},
	} {
		if e := tc.normalize([]byte(tc.line)); e.Type != "external.raw" || e.MessageID != "" {
			t.Fatal(e)
		}
	}
}

func TestCodexThreadIDIsBoundedAndAdapterSupportsResume(t *testing.T) {
	for _, id := range []string{"../private", "-flag", strings.Repeat("x", 257)} {
		b, _ := json.Marshal(map[string]string{"type": "thread.started", "thread_id": id})
		if e := newCodexNormalizer()(b); e.SessionID != "" {
			t.Fatal(e)
		}
	}
	if !NewCodex("").Capabilities().Resumable {
		t.Fatal("Codex resume unavailable")
	}
}
