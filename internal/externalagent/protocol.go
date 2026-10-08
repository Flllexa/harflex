package externalagent

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Each Run owns its normalizer; adapters may be shared by concurrent sessions.
type eventNormalizer func([]byte) Event

type protocolObject map[string]json.RawMessage

func rawProtocolEvent(line []byte) (Event, protocolObject) {
	if !utf8.Valid(line) || !json.Valid(line) {
		return Event{Type: "external.stdout", Text: strings.ToValidUTF8(string(line), "�")}, nil
	}
	e := Event{Type: "external.raw", Raw: bytes.Clone(line)}
	var obj protocolObject
	if len(line) > maxLine || json.Unmarshal(line, &obj) != nil {
		return e, nil
	}
	return e, obj
}

func (o protocolObject) string(field string) string {
	var value string
	_ = json.Unmarshal(o[field], &value)
	return value
}

func (o protocolObject) object(field string) protocolObject {
	var value protocolObject
	_ = json.Unmarshal(o[field], &value)
	return value
}

func (o protocolObject) text() (string, bool) {
	var value *string
	if json.Unmarshal(o["text"], &value) != nil || value == nil || len(*value) > maxLine || !utf8.ValidString(*value) {
		return "", false
	}
	return *value, true
}

func validProtocolID(values ...string) string {
	for _, value := range values {
		if ValidSessionID(value) {
			return value
		}
	}
	return ""
}

func newCodexNormalizer() eventNormalizer {
	var sessionID string
	return func(line []byte) Event {
		e, obj := rawProtocolEvent(line)
		if id := validProtocolID(obj.string("thread_id")); id != "" {
			sessionID = id
		}
		e.SessionID = sessionID
		// Codex exec JSONL emits complete agent text in item.completed.
		// https://learn.chatgpt.com/docs/non-interactive-mode#make-output-machine-readable
		item := obj.object("item")
		if obj.string("type") == "item.completed" && item.string("type") == "agent_message" {
			if value, ok := item.text(); ok {
				id := validProtocolID(item.string("id"))
				if value == "" && id == "" {
					return e
				}
				e.Type, e.Text = "assistant.message", value
				e.MessageID = id
				if e.MessageID != "" {
					e.Mode = "replace"
				}
			}
		}
		return e
	}
}

func newOpenCodeNormalizer() eventNormalizer {
	var sessionID string
	snapshots := make(map[string]string)
	return func(line []byte) Event {
		e, obj := rawProtocolEvent(line)
		part := obj.object("part")
		if id := validProtocolID(obj.string("sessionID"), part.string("sessionID")); id != "" {
			sessionID = id
		}
		e.SessionID = sessionID
		// The run command emits completed text-part snapshots, not SDK deltas.
		// https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/cli/cmd/run.ts
		if obj.string("type") != "text" || part.string("type") != "text" {
			return e
		}
		value, ok := part.text()
		if !ok {
			return e
		}
		id := validProtocolID(part.string("id"), part.string("messageID"))
		if id == "" {
			// Without identity the complete snapshot is still a separate message.
			if value != "" {
				e.Type, e.Text = "assistant.message", value
			}
			return e
		}
		key := sessionID + "/" + id
		previous, exists := snapshots[key]
		if exists && previous == value {
			return e
		}
		// JSONL order is authoritative. A later shorter or empty snapshot can
		// retract earlier output; its length does not establish its age.
		e.Type, e.Text, e.MessageID, e.Mode = "assistant.message", value, id, "replace"
		// Preserve the provider's complete snapshot. Synthesized suffix deltas
		// could split a credential across separately redacted journal records.
		snapshots[key] = value
		return e
	}
}
