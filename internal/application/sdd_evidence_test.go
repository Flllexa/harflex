package application

import (
	"errors"
	"fmt"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
)

type sddEvidenceEvent struct {
	typ  string
	data any
}

func TestSDDEvidenceReadsEntireJournalAndLatestCumulativeUsage(t *testing.T) {
	s, db, _ := setup(t)
	defer Shutdown(s)
	appendEvent := func(typ string, data any) {
		t.Helper()
		if _, err := db.Append(t.Context(), "evidence", "agent_session", typ, data); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("run.started", struct{}{})
	appendEvent("message.user", agentcore.Message{Role: agentcore.RoleUser, Content: "prompt"})
	// Exceed a normal page: neither a fixed first nor a fixed last page is enough.
	for i := 0; i < 1100; i++ {
		appendEvent("assistant.delta", map[string]string{"text": "x"})
	}
	appendEvent("usage.recorded", agentcore.Usage{InputTokens: 100, OutputTokens: 5})
	appendEvent("usage.recorded", agentcore.Usage{InputTokens: 100, OutputTokens: 7})
	appendEvent("usage.recorded", agentcore.Usage{InputTokens: 100, OutputTokens: 7})
	appendEvent("usage.recorded", map[string]int64{"inputTokens": 100})
	appendEvent("usage.recorded", agentcore.Usage{InputTokens: -1, OutputTokens: 8})
	appendEvent("usage.recorded", agentcore.Usage{InputTokens: 200, OutputTokens: -1})
	appendEvent("message.assistant", agentcore.Message{Role: agentcore.RoleAssistant, Content: `{"question":"What is needed?"}`})
	appendEvent("run.completed", map[string]string{"reason": ""})
	got, err := s.readSDDBrainstormEvidence(t.Context(), "evidence", "question")
	if err != nil || got.Question != "What is needed?" || got.Synthesis != nil || got.Usage == nil || *got.Usage != (catalog.BrainstormUsage{InputTokens: 100, OutputTokens: 7}) {
		t.Fatalf("evidence=%+v err=%v", got, err)
	}
}

func TestSDDEvidenceMissingIncompleteOrNegativeUsageIsUnknown(t *testing.T) {
	for _, usage := range []any{nil, map[string]int64{"inputTokens": 100}, map[string]int64{"outputTokens": 7}, map[string]any{"inputTokens": nil, "outputTokens": 7}, map[string]int64{},
		map[string]int64{"inputTokens": -1, "outputTokens": 7}, map[string]int64{"inputTokens": 100, "outputTokens": -1}} {
		t.Run(fmt.Sprintf("%v", usage), func(t *testing.T) {
			s, db, _ := setup(t)
			defer Shutdown(s)
			events := []sddEvidenceEvent{{"run.started", struct{}{}}, {"message.user", agentcore.Message{Role: agentcore.RoleUser, Content: "prompt"}}}
			if usage != nil {
				events = append(events, sddEvidenceEvent{"usage.recorded", usage})
			}
			events = append(events, sddEvidenceEvent{"message.assistant", agentcore.Message{Role: agentcore.RoleAssistant, Content: `{"scope":"Confirmed scope","decisions":["Chosen"],"openQuestions":[]}`}}, sddEvidenceEvent{"run.completed", struct{}{}})
			for _, event := range events {
				if _, err := db.Append(t.Context(), "evidence", "agent_session", event.typ, event.data); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.readSDDBrainstormEvidence(t.Context(), "evidence", "synthesis")
			if err != nil || got.Usage != nil || got.Question != "" || got.Synthesis == nil || got.Synthesis.Scope != "Confirmed scope" {
				t.Fatalf("evidence=%+v err=%v", got, err)
			}
		})
	}
}

func TestSDDEvidenceRejectsAnythingButSingleSuccessfulOutput(t *testing.T) {
	for _, scenario := range []string{"no_terminal", "failed", "cancelled", "two_assistants", "two_runs", "tool_call", "tool_event", "empty", "wrong_role", "bad_json", "bad_schema", "malformed_usage", "usage_after_assistant", "assistant_after_terminal", "no_user", "unknown_kind"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, _ := setup(t)
			defer Shutdown(s)
			kind := "question"
			message := agentcore.Message{Role: agentcore.RoleAssistant, Content: `{"question":"What is needed?"}`}
			prefix := []sddEvidenceEvent{{"run.started", struct{}{}}, {"message.user", agentcore.Message{Role: agentcore.RoleUser, Content: "prompt"}}}
			suffix := []sddEvidenceEvent{{"run.completed", struct{}{}}}
			switch scenario {
			case "no_terminal":
				suffix = nil
			case "failed":
				suffix = []sddEvidenceEvent{{"run.failed", map[string]string{"reason": "execution_failed"}}}
			case "cancelled":
				suffix = []sddEvidenceEvent{{"run.cancelled", struct{}{}}}
			case "two_assistants":
				prefix = append(prefix, sddEvidenceEvent{"message.assistant", message})
			case "two_runs":
				prefix = append(prefix, sddEvidenceEvent{"run.started", struct{}{}})
			case "tool_call":
				message.ToolCalls = []agentcore.ToolCall{{ID: "forbidden", Name: "read"}}
			case "tool_event":
				prefix = append(prefix, sddEvidenceEvent{"tool.denied", map[string]string{"reason": "forbidden"}})
			case "empty":
				message.Content = ""
			case "wrong_role":
				message.Role = agentcore.RoleUser
			case "bad_json":
				message.Content = "not json"
			case "bad_schema":
				message.Content = `{"question":"What is needed?","extra":"no"}`
			case "malformed_usage":
				prefix = append(prefix, sddEvidenceEvent{"usage.recorded", map[string]string{"inputTokens": "bad"}})
			case "usage_after_assistant":
				suffix = append([]sddEvidenceEvent{{"usage.recorded", agentcore.Usage{InputTokens: 100, OutputTokens: 7}}}, suffix...)
			case "assistant_after_terminal":
				prefix = append(prefix, sddEvidenceEvent{"run.completed", struct{}{}})
			case "no_user":
				prefix = prefix[:1]
			case "unknown_kind":
				kind = "spec"
			}
			events := append(prefix, sddEvidenceEvent{"message.assistant", message})
			events = append(events, suffix...)
			for _, event := range events {
				if _, err := db.Append(t.Context(), "evidence", "agent_session", event.typ, event.data); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.readSDDBrainstormEvidence(t.Context(), "evidence", kind); !errors.Is(err, errInvalidSDDOutput) {
				t.Fatalf("invalid evidence accepted: %v", err)
			}
		})
	}
}
