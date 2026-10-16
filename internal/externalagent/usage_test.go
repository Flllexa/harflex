package externalagent

import (
	"encoding/json"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
)

func TestRunUsageReadsTurnTotalsOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want agentcore.Usage
		ok   bool
	}{
		{"claude result counts cache", `{"type":"result","subtype":"success","usage":{"input_tokens":10,"output_tokens":4,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000}}`, agentcore.Usage{InputTokens: 1110, OutputTokens: 4}, true},
		{"codex turn", `{"type":"turn.completed","usage":{"input_tokens":12,"cached_input_tokens":6,"output_tokens":8}}`, agentcore.Usage{InputTokens: 12, OutputTokens: 8}, true},
		{"claude per message usage is ignored", `{"type":"assistant","message":{"usage":{"input_tokens":3,"output_tokens":2}}}`, agentcore.Usage{}, false},
		{"missing output", `{"type":"result","usage":{"input_tokens":3}}`, agentcore.Usage{}, false},
		{"negative", `{"type":"turn.completed","usage":{"input_tokens":-1,"output_tokens":2}}`, agentcore.Usage{}, false},
		{"no usage", `{"type":"turn.completed"}`, agentcore.Usage{}, false},
		{"not json", `nope`, agentcore.Usage{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RunUsage(json.RawMessage(tc.raw))
			if ok != tc.ok || got != tc.want {
				t.Fatalf("RunUsage = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestSessionRecordsCLITurnUsage(t *testing.T) {
	r := &recording{}
	s := NewSession("session", NewCodex(helper(t, "codex-protocol")), r, Request{CWD: t.TempDir()})
	if err := s.Prompt(t.Context(), "Pergunta"); err != nil {
		t.Fatal(err)
	}
	var usages []agentcore.Usage
	for i, typ := range r.types {
		if typ == "usage.recorded" {
			usages = append(usages, r.data[i].(agentcore.Usage))
		}
	}
	if len(usages) != 1 || usages[0] != (agentcore.Usage{InputTokens: 12, OutputTokens: 8}) {
		t.Fatalf("usage events = %+v (types %v)", usages, r.types)
	}
}
