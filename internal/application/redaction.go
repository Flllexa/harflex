package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

var errRedactionUnavailable = errors.New("credential redaction unavailable")

// Owned by one eventJournal, whose mutex serializes redaction and disposal.
// Only credentials authorized for this backend are retained for the session.
type sensitiveRedactor struct {
	patterns  [][]byte
	assistant textBuffer
	tools     map[string]*toolTextBuffer
	aliases   *publicAliases
	closed    bool
}

type textBuffer struct{ pending []byte }
type toolTextBuffer struct {
	textBuffer
	stream string
}

func newSensitiveRedactor(keys ...[]byte) *sensitiveRedactor {
	r := &sensitiveRedactor{aliases: newPublicAliases()}
	for _, key := range keys {
		if len(key) > 0 {
			r.patterns = append(r.patterns, bytes.Clone(key))
		}
	}
	return r
}

func (r *sensitiveRedactor) close() {
	for _, key := range r.patterns {
		clear(key)
	}
	clear(r.assistant.pending)
	for _, stream := range r.tools {
		clear(stream.pending)
	}
	r.aliases.close()
	r.patterns, r.assistant.pending, r.tools, r.closed = nil, nil, nil, true
}

func (r *sensitiveRedactor) payload(data any, streaming bool) (json.RawMessage, error) {
	typ := ""
	if streaming {
		typ = "assistant.delta"
	}
	return r.eventPayload(data, typ)
}

func (r *sensitiveRedactor) eventPayload(data any, typ string) (json.RawMessage, error) {
	if r.closed {
		return nil, errRedactionUnavailable
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, errRedactionUnavailable
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errRedactionUnavailable
	}
	before, err := json.Marshal(value)
	if err != nil {
		return nil, errRedactionUnavailable
	}
	defer clear(before)
	r.aliases.transform(typ, value, r.patterns)
	streaming := typ == "assistant.delta" || typ == "tool.updated"
	if streaming {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, errRedactionUnavailable
		}
		field := "delta"
		buffer := &r.assistant
		if typ == "tool.updated" {
			field = "text"
			call, ok := object["toolCallId"].(string)
			if !ok || call == "" {
				return nil, errRedactionUnavailable
			}
			stream, ok := object["stream"].(string)
			if !ok {
				return nil, errRedactionUnavailable
			}
			if r.tools == nil {
				r.tools = make(map[string]*toolTextBuffer)
			}
			if r.tools[call] == nil {
				r.tools[call] = &toolTextBuffer{}
			}
			if !safeLiveEnum(typ, "stream", stream) {
				stream = string(redactAuditBytes([]byte(stream), r.patterns))
			}
			r.tools[call].stream = stream
			buffer = &r.tools[call].textBuffer
		}
		delta, ok := object[field].(string)
		if !ok {
			return nil, errRedactionUnavailable
		}
		// Preserve a byte-fragmented UTF-8 rune until the next delta completes it.
		// JSON encoding would otherwise replace each incomplete half separately.
		switch original := data.(type) {
		case map[string]string:
			delta = original[field]
		case map[string]any:
			if text, ok := original[field].(string); ok {
				delta = text
			}
		}
		object[field] = buffer.delta(delta, r.patterns)
	}
	value = redactLiveEventValue(value, typ, r.patterns, r.aliases)
	result, err := json.Marshal(value)
	if err != nil {
		return nil, errRedactionUnavailable
	}
	if !streaming && bytes.Equal(before, result) {
		return bytes.Clone(raw), nil
	}
	return result, nil
}

// Retain enough bytes for either a credential prefix or an incomplete UTF-8
// rune, even when no credential is configured. Fully observed overlapping
// matches extend the safe prefix because their bytes will be replaced together.
func (b *textBuffer) delta(text string, patterns [][]byte) string {
	input := make([]byte, 0, len(b.pending)+len(text))
	input = append(input, b.pending...)
	input = append(input, text...)
	clear(b.pending)
	defer clear(input)
	tail := utf8.UTFMax - 1
	for _, pattern := range patterns {
		tail = max(tail, len(pattern)-1)
	}
	cut := max(0, len(input)-tail)
	for cut > 0 && cut < len(input) && !utf8.RuneStart(input[cut]) {
		cut--
	}
	for changed := true; changed; {
		changed = false
		for _, pattern := range patterns {
			for offset := 0; offset < len(input); {
				index := bytes.Index(input[offset:], pattern)
				if index < 0 {
					break
				}
				start, end := offset+index, offset+index+len(pattern)
				offset = start + 1
				if start < cut && end > cut {
					cut, changed = end, true
					for cut < len(input) && !utf8.RuneStart(input[cut]) {
						cut++
					}
				}
			}
		}
	}
	b.pending = bytes.Clone(input[cut:])
	return string(redactAuditBytes(input[:cut], patterns))
}

func (b *textBuffer) flush(patterns [][]byte) string {
	// A boundary can be followed by another delta (usage, tools, or a new run).
	// Conceal unfinished credential prefixes instead of publishing a fragment
	// that a later delta could complete after this pending buffer is cleared.
	cut := len(b.pending)
	for _, pattern := range patterns {
		for size := 1; size < len(pattern) && size <= len(b.pending); size++ {
			if bytes.Equal(b.pending[len(b.pending)-size:], pattern[:size]) {
				cut = min(cut, len(b.pending)-size)
			}
		}
	}
	for cut > 0 && cut < len(b.pending) && !utf8.RuneStart(b.pending[cut]) {
		cut--
	}
	text := string(redactAuditBytes(b.pending[:cut], patterns))
	if cut < len(b.pending) {
		text += redactionMarker(patterns)
	}
	clear(b.pending)
	b.pending = nil
	return text
}
