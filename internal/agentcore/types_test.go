package agentcore_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
)

func TestStreamEventJSONRoundTripWithToolCallArguments(t *testing.T) {
	original := agentcore.StreamEvent{
		Type: "tool_call",
		ToolCall: &agentcore.ToolCall{
			ID:        "call_123",
			Name:      "lookup_customer",
			Arguments: json.RawMessage(`{"customerId":"customer_123","includeHistory":true}`),
		},
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal stream event: %v", err)
	}

	var decoded agentcore.StreamEvent
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal stream event: %v", err)
	}

	if decoded.Type != original.Type {
		t.Fatalf("decoded type = %q, want %q", decoded.Type, original.Type)
	}
	if decoded.ToolCall == nil {
		t.Fatal("decoded tool call is nil")
	}
	if decoded.ToolCall.ID != original.ToolCall.ID {
		t.Fatalf("decoded tool call ID = %q, want %q", decoded.ToolCall.ID, original.ToolCall.ID)
	}
	if decoded.ToolCall.Name != original.ToolCall.Name {
		t.Fatalf("decoded tool call name = %q, want %q", decoded.ToolCall.Name, original.ToolCall.Name)
	}
	if !bytes.Equal(decoded.ToolCall.Arguments, original.ToolCall.Arguments) {
		t.Fatalf("decoded tool call arguments = %s, want %s", decoded.ToolCall.Arguments, original.ToolCall.Arguments)
	}
}
