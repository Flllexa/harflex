package agentcore

import (
	"bytes"
	"encoding/json"
	"unicode"
	"unicode/utf8"
)

const (
	maxToolFailureCodeBytes    = 40
	maxToolFailureMessageBytes = 400
)

// ToolFailure is a failure the model can act on: a file that is not there, a malformed argument, a
// command that exited with a status. A tool returns it instead of a plain error when the model can
// reasonably correct course. The tool writes Code and Message itself, from facts it already holds and
// never from an underlying error, so no secret or private path rides along into the journal or the model.
// The run goes on and the model reads the failure as the tool's result. Any other error from a tool still
// ends the run, and only a generic, safe code is recorded for it.
type ToolFailure struct {
	// Code is a lower-case identifier such as not_found or exit_status.
	Code string
	// Message is one short sentence about what went wrong, safe to show to the model and the person.
	Message string
	cause   error
}

// Error keeps the underlying text for logs and tests that read it; the session journals only Code and
// Message, never this.
func (e *ToolFailure) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	return "tool failure " + e.Code
}

// Unwrap exposes what the tool saw, to callers in code (errors.Is). The session never journals it.
func (e *ToolFailure) Unwrap() error { return e.cause }

// WithCause records the underlying error for code that inspects it; it is not part of Code or Message.
func (e *ToolFailure) WithCause(cause error) *ToolFailure {
	e.cause = cause
	return e
}

func (e *ToolFailure) valid() bool {
	if e == nil || e.Code == "" || len(e.Code) > maxToolFailureCodeBytes || e.Message == "" || len(e.Message) > maxToolFailureMessageBytes || !utf8.ValidString(e.Message) {
		return false
	}
	for index, r := range e.Code {
		if !(r >= 'a' && r <= 'z' || (index > 0 && (r >= '0' && r <= '9' || r == '_'))) {
			return false
		}
	}
	return !containsControl(e.Message)
}

func containsControl(text string) bool {
	for _, r := range text {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// soleToolFailure finds a ToolFailure that is the only cause behind err. A failure joined with another
// error, or hidden behind several causes, does not qualify: something unexpected also went wrong and the
// run must not go on as if only the expected part had happened.
func soleToolFailure(err error) (*ToolFailure, bool) {
	for err != nil {
		if failure, ok := err.(*ToolFailure); ok {
			return failure, true
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil, false
		}
		err = wrapped.Unwrap()
	}
	return nil, false
}

// ToolFailureResponse is what the model reads after a ToolFailure. Both the live run and a reopened
// session build it from the journaled code, message and partial result, so the history is the same.
func ToolFailureResponse(code, message string, content json.RawMessage) string {
	response := map[string]any{"error": code, "message": message}
	if trimmed := bytes.TrimSpace(content); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && json.Valid(trimmed) {
		response["result"] = json.RawMessage(trimmed)
	}
	encoded, _ := json.Marshal(response)
	return string(encoded)
}
