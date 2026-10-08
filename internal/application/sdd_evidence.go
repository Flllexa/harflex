package application

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/externalagent"
)

type sddBrainstormEvidence struct {
	Question  string
	Synthesis *catalog.BrainstormSynthesisContent
	Usage     *catalog.BrainstormUsage
}

// Accept evidence only from one successful, tool-less internal run. walkEvents
// traverses the entire journal with sequence, event-count, and byte bounds.
// This does not replace the caller's successful RunResult or settlement CAS.
func (s *Service) readSDDBrainstormEvidence(ctx context.Context, sessionID, kind string) (sddBrainstormEvidence, error) {
	if kind != "question" && kind != "synthesis" {
		return sddBrainstormEvidence{}, errInvalidSDDOutput
	}
	content, usage, err := s.readSDDReadOnlyEvidence(ctx, sessionID)
	if err != nil {
		return sddBrainstormEvidence{}, err
	}
	result := sddBrainstormEvidence{Usage: usage}
	if kind == "question" {
		result.Question, err = parseSDDQuestionOutput(content)
	} else {
		var synthesis catalog.BrainstormSynthesisContent
		synthesis, err = parseSDDSynthesisOutput(content)
		result.Synthesis = &synthesis
	}
	if err != nil {
		return sddBrainstormEvidence{}, err
	}
	return result, nil
}

func (s *Service) readSDDReadOnlyEvidence(ctx context.Context, sessionID string) (string, *catalog.BrainstormUsage, error) {
	var started, external, user, assistant, completed, threadStarted, sessionBound, turnStarted, turnCompleted bool
	var content string
	var boundSessionID string
	var usage *catalog.BrainstormUsage
	err := s.walkEvents(ctx, sessionID, func(event events.Event) error {
		if completed || !utf8.Valid(event.Data) {
			return errInvalidSDDOutput
		}
		switch event.Type {
		case "run.started":
			if started || external {
				return errInvalidSDDOutput
			}
			started = true
		case "external.run.started":
			var item struct {
				Adapter string `json:"adapter"`
			}
			if started || json.Unmarshal(event.Data, &item) != nil || item.Adapter != "codex" {
				return errInvalidSDDOutput
			}
			started, external = true, true
		case "message.user":
			var message agentcore.Message
			if !started || user || assistant || json.Unmarshal(event.Data, &message) != nil || message.Role != agentcore.RoleUser || len(message.ToolCalls) != 0 || message.ToolCallID != "" || strings.TrimSpace(message.Content) == "" {
				return errInvalidSDDOutput
			}
			user = true
		case "external.session.bound":
			var item struct {
				SessionID string `json:"sessionId"`
			}
			if !external || !user || sessionBound || assistant || !threadStarted || json.Unmarshal(event.Data, &item) != nil || !externalagent.ValidSessionID(item.SessionID) {
				return errInvalidSDDOutput
			}
			sessionBound = true
			boundSessionID = item.SessionID
		case "external.event":
			if !external || !user || completed {
				return errInvalidSDDOutput
			}
			var item externalagent.Event
			if json.Unmarshal(event.Data, &item) != nil {
				return errInvalidSDDOutput
			}
			switch item.Type {
			case "external.raw":
				rawType, rawUsage, valid := codexSDDLifecycleEvent(item, boundSessionID, threadStarted, sessionBound, turnStarted, turnCompleted, assistant)
				if !valid {
					return errInvalidSDDOutput
				}
				switch rawType {
				case "thread.started":
					threadStarted = true
				case "turn.started":
					turnStarted = true
				case "turn.completed":
					turnCompleted = true
					if rawUsage != nil {
						usage = rawUsage
					}
				}
			case "assistant.message":
				if !threadStarted || !sessionBound || !turnStarted || turnCompleted || assistant || item.SessionID != boundSessionID || !validCodexSDDAssistantMessage(item) {
					return errInvalidSDDOutput
				}
				assistant, content = true, item.Text
			default:
				return errInvalidSDDOutput
			}
		case "assistant.delta":
			if external || !user || assistant {
				return errInvalidSDDOutput
			}
		case "usage.recorded":
			if external || !user || assistant {
				return errInvalidSDDOutput
			}
			var item struct {
				InputTokens  *int64 `json:"inputTokens"`
				OutputTokens *int64 `json:"outputTokens"`
			}
			if json.Unmarshal(event.Data, &item) != nil {
				return errInvalidSDDOutput
			}
			// A run has exactly one provider response. Providers emit cumulative
			// usage (occasionally repeated), so the last complete snapshot wins,
			// never a sum. Incomplete or negative snapshots do not replace valid
			// usage or invalidate assistant output; without valid usage it is unknown.
			if item.InputTokens != nil && item.OutputTokens != nil && *item.InputTokens >= 0 && *item.OutputTokens >= 0 {
				usage = &catalog.BrainstormUsage{InputTokens: *item.InputTokens, OutputTokens: *item.OutputTokens}
			}
		case "message.assistant":
			var message agentcore.Message
			if external || !user || assistant || json.Unmarshal(event.Data, &message) != nil || message.Role != agentcore.RoleAssistant || len(message.ToolCalls) != 0 || message.ToolCallID != "" || strings.TrimSpace(message.Content) == "" {
				return errInvalidSDDOutput
			}
			assistant, content = true, message.Content
		case "run.completed":
			if external || !assistant {
				return errInvalidSDDOutput
			}
			completed = true
		case "external.run.completed":
			var item struct {
				Adapter string `json:"adapter"`
			}
			if !external || !user || !assistant || !sessionBound || !turnCompleted || json.Unmarshal(event.Data, &item) != nil || item.Adapter != "codex" {
				return errInvalidSDDOutput
			}
			completed = true
		default:
			// This includes tool calls/results, failed/cancelled runs, additional
			// user interactions, and any unexpected execution path.
			return errInvalidSDDOutput
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	if !completed {
		return "", nil, errInvalidSDDOutput
	}
	return content, usage, nil
}

func codexSDDLifecycleEvent(event externalagent.Event, boundSessionID string, threadStarted, sessionBound, turnStarted, turnCompleted, assistant bool) (string, *catalog.BrainstormUsage, bool) {
	if event.Type != "external.raw" || !json.Valid(event.Raw) || event.SessionID == "" || !externalagent.ValidSessionID(event.SessionID) {
		return "", nil, false
	}
	var item struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Item     struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
		Usage *struct {
			InputTokens  *int64 `json:"input_tokens"`
			OutputTokens *int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(event.Raw, &item) != nil {
		return "", nil, false
	}
	switch item.Type {
	case "thread.started":
		return item.Type, nil, !threadStarted && !sessionBound && !assistant && item.ThreadID == event.SessionID
	case "turn.started":
		return item.Type, nil, threadStarted && sessionBound && event.SessionID == boundSessionID && !turnStarted && !assistant
	case "item.started", "item.updated":
		return item.Type, nil, threadStarted && sessionBound && event.SessionID == boundSessionID && turnStarted && !turnCompleted && !assistant && item.Item.ID != "" && item.Item.Type == "agent_message"
	case "turn.completed":
		if !threadStarted || !sessionBound || event.SessionID != boundSessionID || !turnStarted || turnCompleted || !assistant {
			return "", nil, false
		}
		if item.Usage == nil {
			return item.Type, nil, true
		}
		if item.Usage.InputTokens == nil || item.Usage.OutputTokens == nil || *item.Usage.InputTokens < 0 || *item.Usage.OutputTokens < 0 {
			return "", nil, false
		}
		return item.Type, &catalog.BrainstormUsage{InputTokens: *item.Usage.InputTokens, OutputTokens: *item.Usage.OutputTokens}, true
	default:
		return "", nil, false
	}
}

func validCodexSDDAssistantMessage(event externalagent.Event) bool {
	if event.Type != "assistant.message" || event.Mode != "replace" || event.MessageID == "" || event.SessionID == "" || strings.TrimSpace(event.Text) == "" {
		return false
	}
	var raw struct {
		Type string `json:"type"`
		Item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	return json.Unmarshal(event.Raw, &raw) == nil && raw.Type == "item.completed" && raw.Item.ID == event.MessageID && raw.Item.Type == "agent_message" && raw.Item.Text == event.Text
}
