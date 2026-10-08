package catalog

import (
	"encoding/json"
	"github.com/persioflexa/harflex/internal/sdd"
	"time"
)

type AuthoringStageRequest struct {
	PipelineID       string
	Stage            sdd.Stage
	RequestID        string
	PipelineRevision int64
	StageRevision    int64
	DiscoveryVersion int64
	ArtifactVersion  int64
}

type BeginAuthoringStageRequest struct {
	Ref              AuthoringStageRequest
	Selection        ModelSelection
	ModelMode        string
	EffortMode       string
	PreferenceSource string
	// PreferenceRevision fences admission against a concurrent phase preference update.
	PreferenceRevision   int64
	EstimatedInputTokens int64
	ClientIntentHash     string
	// Nonempty feedback is mandatory for revision, never an artifact edit.
	Feedback string
}

// AuthoringStageModelPreference is mutable future-attempt configuration.
// Selection is populated only for an explicit model override; its credential
// identity remains the existing internal digest, never a credential value.
type AuthoringStageModelPreference struct {
	PipelineID     string
	Stage          sdd.Stage
	ModelMode      string
	EffortMode     string
	ExplicitEffort string
	Selection      ModelSelection
	Revision       int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type AuthoringStageInput struct {
	DiscoveryVersion        int64
	DiscoveryContent        string
	DiscoveryHash           string
	BrainstormRunID         string
	SynthesisVersion        int
	Synthesis               *BrainstormSynthesisContent
	DiscoveryBypassReason   string
	SpecVersion             int64
	SpecContent             json.RawMessage `json:",omitempty"`
	SpecHash                string
	SpecBypassReason        string
	PreviousArtifactVersion int64
	PreviousContent         json.RawMessage `json:",omitempty"`
	Feedback                string
}

type AuthoringStageAttempt struct {
	ID                   string
	PipelineID           string
	Stage                sdd.Stage
	RequestID            string
	PayloadHash          string
	ArtifactVersion      int64
	Status               string
	SessionID            string
	ErrorCode            string
	CancellationState    string `json:",omitempty"`
	Input                AuthoringStageInput
	Selection            ModelSelection
	ModelMode            string
	EffortMode           string
	PreferenceSource     string
	PreferenceRevision   int64
	ReservedInputTokens  int64
	ReservedOutputTokens int64
	Usage                *BrainstormUsage
	ResultHash           string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type AuthoringStageArtifact struct {
	Version         int64
	Content         json.RawMessage
	ContentHash     string
	Author          string
	AttemptID       string
	SourceSessionID string
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type AuthoringStageAction struct {
	RequestID              string
	Action                 string
	Actor                  string
	ArtifactVersion        int64
	Feedback               string
	Reason                 string
	AttemptID              string
	ResultStageRevision    int64
	ResultPipelineRevision int64
	CreatedAt              time.Time
}

type AuthoringStageRun struct {
	PipelineID            string
	Stage                 sdd.Stage
	PipelineRevision      int64
	DiscoveryVersion      int64
	Revision              int64
	State                 string
	CancellationPending   bool
	CancellationAttemptID string
	ArtifactVersion       int64
	AttemptCount          int
	InputBudgetRemaining  int64
	OutputBudgetRemaining int64
	Attempts              []AuthoringStageAttempt
	Artifacts             []AuthoringStageArtifact
	Actions               []AuthoringStageAction
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type AuthoringStageCommandReceipt struct {
	Stage                  sdd.Stage
	Action                 string
	ClientIntentHash       string
	AttemptID              string
	ResultStageRevision    int64
	ResultPipelineRevision int64
}

type CompleteAuthoringStageRequest struct {
	Ref       AuthoringStageRequest
	AttemptID string
	SessionID string
	Content   json.RawMessage
	Usage     *BrainstormUsage
}
type FailAuthoringStageRequest struct {
	Ref       AuthoringStageRequest
	AttemptID string
	ErrorCode string
}
type AuthoringStageDecisionRequest struct {
	Ref       AuthoringStageRequest
	Reason    string
	AttemptID string
}

type AuthoringStageStop struct {
	PipelineID string
	Stage      sdd.Stage
	AttemptID  string
	SessionID  string
}
