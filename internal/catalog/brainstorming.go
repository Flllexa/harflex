package catalog

import "time"

// BrainstormRequest carries both aggregate compare-and-swap boundaries.
type BrainstormRequest struct {
	RunID            string
	RequestID        string
	PipelineRevision int64
	RunRevision      int64
	DiscoveryVersion int64
}

type StartBrainstormingRequest struct {
	PipelineID       string
	RequestID        string
	PipelineRevision int64
	DiscoveryVersion int64
	// Selection must be prepared and validated by the application catalog boundary.
	Selection        ModelSelection
	ClientIntentHash string `json:"ClientIntentHash,omitempty"`
}

type BeginBrainstormAttemptRequest struct {
	BrainstormRequest
	Kind      string
	Selection ModelSelection
	// EstimatedInputTokens includes the complete provider prompt and framing.
	EstimatedInputTokens int64
	ClientIntentHash     string `json:"ClientIntentHash,omitempty"`
}

// GenerateBrainstormSynthesisRequest is admitted only by an explicit human command.
type GenerateBrainstormSynthesisRequest struct {
	BrainstormRequest
	Selection            ModelSelection
	EstimatedInputTokens int64
	ClientIntentHash     string `json:"ClientIntentHash,omitempty"`
}

// BrainstormCommandReceipt supports offline application replay before catalog access.
type BrainstormCommandReceipt struct {
	Action                 string
	ClientIntentHash       string
	AttemptID              string
	ResultRunRevision      int64
	ResultPipelineRevision int64
	Actor                  string
}

type BrainstormUsage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	// Nil means unknown (N/A), never free.
	CostUSD *float64 `json:"costUsd"`
}

type BrainstormSynthesisContent struct {
	Scope         string   `json:"scope"`
	Decisions     []string `json:"decisions"`
	OpenQuestions []string `json:"openQuestions"`
}

type CompleteBrainstormAttemptRequest struct {
	BrainstormRequest
	AttemptID string
	SessionID string
	Question  string
	Synthesis *BrainstormSynthesisContent
	Usage     *BrainstormUsage
}

type AnswerBrainstormRequest struct {
	BrainstormRequest
	QuestionID       string
	Answer           string
	ClientIntentHash string `json:"ClientIntentHash,omitempty"`
}

type FailBrainstormAttemptRequest struct {
	BrainstormRequest
	AttemptID string
	// ErrorCode is an allowlisted classification, never a provider message.
	ErrorCode string
}

type BrainstormRun struct {
	ID         string
	PipelineID string
	// PipelineRevision is populated by aggregate readback in the same snapshot.
	PipelineRevision      int64
	DiscoveryVersion      int64
	StartRequestID        string
	StartPayloadHash      string
	StartClientIntentHash string
	DiscoveryContent      string
	DiscoveryHash         string
	Selection             ModelSelection
	State                 string
	Revision              int64
	QuestionCount         int
	CurrentQuestionID     string
	SynthesisVersion      int
	AttemptCount          int
	InputBudgetRemaining  int64
	OutputBudgetRemaining int64
	ActiveDuration        time.Duration
	CreatedAt             time.Time
	UpdatedAt             time.Time
	Attempts              []BrainstormAttempt
	Turns                 []BrainstormTurn
	Syntheses             []BrainstormSynthesis
	HumanActions          []BrainstormHumanAction
}

type BrainstormAttempt struct {
	ID                   string
	RunID                string
	DiscoveryVersion     int64
	SynthesisVersion     int
	RequestID            string
	Kind                 string
	PayloadHash          string
	Status               string
	SessionID            string
	ErrorCode            string
	Selection            ModelSelection
	ReservedInputTokens  int64
	ReservedOutputTokens int64
	Usage                *BrainstormUsage
	ResultHash           string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type BrainstormTurn struct {
	Number          int
	QuestionID      string
	Question        string
	Answer          string
	SourceSessionID string
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type BrainstormSynthesis struct {
	Version          int
	DiscoveryVersion int64
	Content          BrainstormSynthesisContent
	SourceSessionID  string
	Status           string
	CreatedAt        time.Time
}
