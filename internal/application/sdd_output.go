package application

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
)

const (
	sddQuestionOutputBytes  = 2 * 1024
	sddSynthesisOutputBytes = 64 * 1024
)

var errInvalidSDDOutput = errors.New("invalid brainstorm output")

func parseSDDQuestionOutput(output string) (string, error) {
	fields, err := parseSDDObject(output, sddQuestionOutputBytes, "question")
	if err != nil {
		return "", err
	}
	var question string
	if err := json.Unmarshal(fields["question"], &question); err != nil || strings.TrimSpace(question) == "" || len(question) > sddQuestionOutputBytes || utf8.RuneCountInString(question) > 280 || strings.Count(question, "?") != 1 || strings.IndexFunc(question, unicode.IsControl) >= 0 || strings.ContainsAny(question, "\u2028\u2029") {
		return "", errInvalidSDDOutput
	}
	return question, nil
}

func parseSDDSynthesisOutput(output string) (catalog.BrainstormSynthesisContent, error) {
	fields, err := parseSDDObject(output, sddSynthesisOutputBytes, "decisions", "openQuestions", "scope")
	if err != nil {
		return catalog.BrainstormSynthesisContent{}, err
	}
	var content catalog.BrainstormSynthesisContent
	if err := json.Unmarshal(fields["scope"], &content.Scope); err != nil || strings.TrimSpace(content.Scope) == "" ||
		json.Unmarshal(fields["decisions"], &content.Decisions) != nil || len(content.Decisions) == 0 ||
		json.Unmarshal(fields["openQuestions"], &content.OpenQuestions) != nil || content.OpenQuestions == nil {
		return catalog.BrainstormSynthesisContent{}, errInvalidSDDOutput
	}
	for _, value := range append(append([]string(nil), content.Decisions...), content.OpenQuestions...) {
		if strings.TrimSpace(value) == "" {
			return catalog.BrainstormSynthesisContent{}, errInvalidSDDOutput
		}
	}
	return content, nil
}

// A token walk detects duplicate top-level names; typed decoding alone would
// silently accept them. Nested values are constrained to strings and arrays.
func parseSDDObject(output string, maxBytes int, keys ...string) (map[string]json.RawMessage, error) {
	if len(output) == 0 || len(output) > maxBytes || !utf8.ValidString(output) {
		return nil, errInvalidSDDOutput
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errInvalidSDDOutput
	}
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
	}
	fields := make(map[string]json.RawMessage, len(keys))
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, errInvalidSDDOutput
		}
		key, ok := name.(string)
		if !ok || !allowed[key] || fields[key] != nil {
			return nil, errInvalidSDDOutput
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, errInvalidSDDOutput
		}
		fields[key] = raw
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(fields) != len(keys) {
		return nil, errInvalidSDDOutput
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errInvalidSDDOutput
	}
	return fields, nil
}
