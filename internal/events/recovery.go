package events

// ToolClosure records the known outcome of an interrupted public tool call.
type ToolClosure struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	ErrorCode  string `json:"errorCode"`
	Error      string `json:"error"`
}
