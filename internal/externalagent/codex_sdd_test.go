package externalagent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/events"
)

func setRequestField(t *testing.T, request *Request, name string, value any) {
	t.Helper()
	field := reflect.ValueOf(request).Elem().FieldByName(name)
	if !field.IsValid() || !field.CanSet() {
		t.Fatalf("Request.%s is required for bounded SDD execution", name)
	}
	valueOf := reflect.ValueOf(value)
	if !valueOf.Type().AssignableTo(field.Type()) {
		t.Fatalf("Request.%s has type %s, cannot assign %s", name, field.Type(), valueOf.Type())
	}
	field.Set(valueOf)
}

func TestCodexSDDRequestUsesReadOnlyEphemeralInvocation(t *testing.T) {
	request := Request{CWD: "/registered/workspace", Model: "gpt-6-sol", ReasoningEffort: "high", Prompt: "Return JSON"}
	setRequestField(t, &request, "Sandbox", "read-only")
	setRequestField(t, &request, "IgnoreUserConfig", true)
	setRequestField(t, &request, "Ephemeral", true)
	args, _ := NewCodex("/opt/homebrew/bin/codex").(*cliAdapter).build(request)
	joined := strings.Join(args, " ")
	for _, want := range []string{"--sandbox read-only", "-C /registered/workspace", "--ignore-user-config", "--ephemeral", "--model gpt-6-sol", "model_reasoning_effort=high"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Codex SDD args missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "danger-full-access") || strings.Contains(joined, "workspace-write") {
		t.Fatalf("SDD request escaped its read-only sandbox: %v", args)
	}
}

func TestCodexIsolatedCodePolicyAllowsBoundedWorkspaceWrite(t *testing.T) {
	request := Request{CWD: t.TempDir(), Prompt: "Implement the approved plan", Sandbox: "workspace-write", IgnoreUserConfig: true, ApproveForMe: true, Policy: "sdd_code", MaxAssistantOutputBytes: 64 * 1024}
	if err := validateRequest(request, "codex", true); err != nil {
		t.Fatalf("isolated Code request rejected: %v", err)
	}
	args, _ := NewCodex("/opt/homebrew/bin/codex").(*cliAdapter).build(request)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--approve-for-me") || !strings.Contains(joined, "--ignore-user-config") || strings.Contains(joined, "--sandbox") {
		t.Fatalf("Codex Code request lost its sandbox policy: %v", args)
	}
	request.Policy = ""
	if err := validateRequest(request, "codex", true); err == nil {
		t.Fatal("bounded workspace-write was accepted without the isolated Code policy")
	}
	request.Policy = "sdd_code"
	request.ApproveForMe = false
	if err := validateRequest(request, "codex", true); err == nil {
		t.Fatal("isolated Code was accepted without a noninteractive approval policy")
	}
}

type outputLimitRecorder struct {
	events []struct {
		eventType string
		data      any
	}
}

func (r *outputLimitRecorder) Append(_ context.Context, _, _, eventType string, data any) (events.Event, error) {
	r.events = append(r.events, struct {
		eventType string
		data      any
	}{eventType, data})
	return events.Event{Type: eventType}, nil
}

type outputLimitAdapter struct{ text string }

func (a outputLimitAdapter) ID() string { return "codex" }
func (a outputLimitAdapter) Detect() Detection {
	return Detection{Available: true, Path: "/opt/homebrew/bin/codex", Version: "test"}
}
func (a outputLimitAdapter) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true}
}
func (a outputLimitAdapter) Run(context.Context, Request) (<-chan Event, <-chan error) {
	output := make(chan Event, 1)
	output <- Event{Type: "assistant.message", Text: a.text, Raw: json.RawMessage(`{"type":"item.completed"}`)}
	close(output)
	errors := make(chan error)
	close(errors)
	return output, errors
}

type rawSnapshotLimitAdapter struct{ raw json.RawMessage }

func (a rawSnapshotLimitAdapter) ID() string { return "codex" }
func (a rawSnapshotLimitAdapter) Detect() Detection {
	return Detection{Available: true, Path: "/opt/homebrew/bin/codex", Version: "test"}
}
func (a rawSnapshotLimitAdapter) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, Resumable: true}
}
func (a rawSnapshotLimitAdapter) Run(context.Context, Request) (<-chan Event, <-chan error) {
	output := make(chan Event, 1)
	output <- Event{Type: "external.raw", Raw: a.raw, SessionID: "thread_synthetic"}
	close(output)
	errors := make(chan error)
	close(errors)
	return output, errors
}

func TestExternalSessionRejectsAssistantOutputBeforeJournalingOverLimit(t *testing.T) {
	request := Request{CWD: t.TempDir(), Sandbox: "read-only", IgnoreUserConfig: true, Ephemeral: true}
	setRequestField(t, &request, "MaxAssistantOutputBytes", 8)
	recorder := &outputLimitRecorder{}
	session := NewSession("sdd-session", outputLimitAdapter{text: "123456789"}, recorder, request)
	err := session.Prompt(t.Context(), "Generate a small document")
	if err == nil {
		t.Fatal("oversized CLI assistant response was accepted")
	}
	for _, event := range recorder.events {
		if event.eventType == "external.event" {
			if assistant, ok := event.data.(Event); ok && assistant.Type == "assistant.message" {
				t.Fatal("oversized assistant text was written to the durable journal")
			}
		}
	}
}

func TestExternalSessionRejectsOversizedCodexSnapshotBeforeJournaling(t *testing.T) {
	request := Request{CWD: t.TempDir(), Sandbox: "read-only", IgnoreUserConfig: true, Ephemeral: true}
	setRequestField(t, &request, "MaxAssistantOutputBytes", 8)
	raw := json.RawMessage(`{"type":"item.updated","item":{"id":"item_1","type":"agent_message","text":"123456789"}}`)
	recorder := &outputLimitRecorder{}
	session := NewSession("sdd-session", rawSnapshotLimitAdapter{raw: raw}, recorder, request)
	err := session.Prompt(t.Context(), "Generate a small document")
	if err == nil {
		t.Fatal("oversized Codex snapshot was accepted")
	}
	for _, event := range recorder.events {
		if event.eventType == "external.event" {
			if recorded, ok := event.data.(Event); ok && recorded.Type == "external.raw" {
				t.Fatal("oversized Codex text snapshot was written to the durable journal")
			}
		}
	}
}
