package agentcore

import (
	"encoding/json"
	"errors"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/security"
	"unicode/utf8"
)

var ErrInvalidHistory = errors.New("invalid session history")

const MaxHistoryBytes = events.MaxStreamDataBytes

// RestoreSession owns a validated history, without reactivating runs or approvals.
func RestoreSession(id string, provider Provider, executor ToolExecutor, journal Journal, profile security.Profile, history []Message) (*Session, error) {
	return RestoreSessionWithLimit(id, provider, executor, journal, profile, history, 0)
}

// RestoreSessionWithLimit binds a positive output cap at construction. Zero
// preserves the legacy, uncapped request shape.
func RestoreSessionWithLimit(id string, provider Provider, executor ToolExecutor, journal Journal, profile security.Profile, history []Message, maxOutputTokens int) (*Session, error) {
	if maxOutputTokens < 0 || maxOutputTokens > 32768 {
		return nil, ErrInvalidHistory
	}
	if err := ValidateHistory(history); err != nil {
		return nil, err
	}
	s := NewSession(id, provider, executor, journal, profile)
	s.maxOutputTokens = maxOutputTokens
	s.messages = cloneMessages(history)
	return s, nil
}

func ValidateHistory(history []Message) error {
	if len(history) > 100_000 {
		return ErrInvalidHistory
	}
	total := 0
	var pending []ToolCall
	for _, message := range history {
		total += len(message.Content) + len(message.Metadata)
		// Persisted JSON escaping and credential redaction may expand fields.
		// Replay uses the durable envelope budget, not the pre-encoding input cap.
		if len(message.Content) > events.MaxDataBytes || !utf8.ValidString(message.Content) || len(message.Metadata) > events.MaxDataBytes || (len(message.Metadata) > 0 && !json.Valid(message.Metadata)) {
			return ErrInvalidHistory
		}
		if len(pending) > 0 && message.Role != RoleTool {
			return ErrInvalidHistory
		}
		switch message.Role {
		case RoleUser, RoleSystem:
			if len(message.ToolCalls) != 0 || message.ToolCallID != "" {
				return ErrInvalidHistory
			}
		case RoleAssistant:
			if message.ToolCallID != "" || len(message.ToolCalls) > maxToolCalls {
				return ErrInvalidHistory
			}
			seen := map[string]bool{}
			for _, call := range message.ToolCalls {
				// Public aliases can use a bounded Unicode fallback when a short secret
				// excludes ASCII IDs; they remain opaque provider correlation identifiers.
				if call.ID == "" || len(call.ID) > 4096 || call.Name == "" || len(call.Name) > 4096 || !utf8.ValidString(call.ID) || !utf8.ValidString(call.Name) || len(call.Arguments) > events.MaxDataBytes || !json.Valid(call.Arguments) || seen[call.ID] {
					return ErrInvalidHistory
				}
				seen[call.ID] = true
				total += len(call.ID) + len(call.Name) + len(call.Arguments)
			}
			pending = message.ToolCalls
		case RoleTool:
			if len(pending) == 0 || message.ToolCallID != pending[0].ID || len(message.ToolCalls) != 0 || !json.Valid([]byte(message.Content)) {
				return ErrInvalidHistory
			}
			pending = pending[1:]
		default:
			return ErrInvalidHistory
		}
		if total > MaxHistoryBytes {
			return ErrInvalidHistory
		}
	}
	if len(pending) != 0 {
		return ErrInvalidHistory
	}
	return nil
}
