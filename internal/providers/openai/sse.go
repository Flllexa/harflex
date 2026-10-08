package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
)

const maxSSESize = 1 << 20

type sseReader struct{ reader *bufio.Reader }

func (s *sseReader) next() ([]byte, error) {
	var data []byte
	eventSize := 0
	hasData := false
	for {
		var line []byte
		for {
			part, err := s.reader.ReadSlice('\n')
			if len(line)+len(part) > maxSSESize {
				return nil, errors.New("SSE line size limit exceeded")
			}
			line = append(line, part...)
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil, io.ErrUnexpectedEOF
				}
				return nil, fmt.Errorf("read SSE response: %w", safeCause{err})
			}
			break
		}
		if !utf8.Valid(line) {
			return nil, errors.New("SSE invalid UTF-8")
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) == 0 {
			if hasData {
				return data, nil
			}
			eventSize = 0
			continue
		}
		eventSize += len(line) + 1
		if eventSize > maxSSESize {
			return nil, errors.New("SSE event size limit exceeded")
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte{':'})
		if !bytes.Equal(field, []byte("data")) {
			continue
		}
		value = bytes.TrimPrefix(value, []byte{' '})
		if hasData {
			data = append(data, '\n')
		}
		data = append(data, value...)
		hasData = true
	}
}

type chunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     *int64 `json:"prompt_tokens"`
		CompletionTokens *int64 `json:"completion_tokens"`
	} `json:"usage"`
}

type pendingCall struct{ id, name, args strings.Builder }

func consumeSSE(body io.Reader, send func(agentcore.StreamEvent) error) error {
	reader := sseReader{reader: bufio.NewReaderSize(body, 4096)}
	calls := make(map[int]*pendingCall)
	totalCallSize := 0
	emitted := false
	flush := func() error {
		if emitted {
			return nil
		}
		indices := make([]int, 0, len(calls))
		for index := range calls {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		for _, index := range indices {
			call := calls[index]
			if !objectJSON([]byte(call.args.String())) {
				return errors.New("invalid tool arguments: expected JSON object")
			}
			if call.id.Len() == 0 || call.name.Len() == 0 {
				return errors.New("invalid incomplete tool call")
			}
		}
		for _, index := range indices {
			call := calls[index]
			event := agentcore.StreamEvent{Type: "tool_call", ToolCall: &agentcore.ToolCall{ID: call.id.String(), Name: call.name.String(), Arguments: json.RawMessage(call.args.String())}}
			if err := send(event); err != nil {
				return err
			}
		}
		emitted = true
		return nil
	}
	for {
		data, err := reader.next()
		if err != nil {
			return err
		}
		if string(data) == "[DONE]" {
			return flush()
		}
		var incoming chunk
		if err := json.Unmarshal(data, &incoming); err != nil {
			return errors.New("invalid SSE JSON event")
		}
		if incoming.Choices == nil && incoming.Usage == nil {
			return errors.New("invalid SSE chunk: missing choices or usage")
		}
		for _, choice := range incoming.Choices {
			if choice.Delta.Content != "" {
				if err := send(agentcore.StreamEvent{Type: "text_delta", Delta: choice.Delta.Content}); err != nil {
					return err
				}
			}
			for _, delta := range choice.Delta.ToolCalls {
				if delta.Index < 0 || emitted {
					return errors.New("invalid tool call sequence")
				}
				call := calls[delta.Index]
				totalCallSize += len(delta.ID) + len(delta.Function.Name) + len(delta.Function.Arguments)
				if totalCallSize > maxSSESize || (call == nil && len(calls) >= 1024) {
					return errors.New("tool call accumulation size limit exceeded")
				}
				if call == nil {
					call = &pendingCall{}
					calls[delta.Index] = call
				}
				call.id.WriteString(delta.ID)
				call.name.WriteString(delta.Function.Name)
				call.args.WriteString(delta.Function.Arguments)
			}
			if choice.FinishReason == "tool_calls" {
				if err := flush(); err != nil {
					return fmt.Errorf("complete tool calls: %w", err)
				}
			}
		}
		if incoming.Usage != nil && incoming.Usage.PromptTokens != nil && incoming.Usage.CompletionTokens != nil && *incoming.Usage.PromptTokens >= 0 && *incoming.Usage.CompletionTokens >= 0 {
			if err := send(agentcore.StreamEvent{Type: "usage", Usage: &agentcore.Usage{InputTokens: *incoming.Usage.PromptTokens, OutputTokens: *incoming.Usage.CompletionTokens}}); err != nil {
				return err
			}
		}
	}
}
