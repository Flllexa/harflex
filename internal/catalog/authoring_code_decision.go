package catalog

import "time"

type AuthoringCodeDecisionRequest struct {
	PipelineID         string
	PipelineRevision   int64
	RunID              string
	RequestID          string
	Action             string
	PatchHash          string
	BaselineHash       string
	SourceManifestHash string
	ResultManifestHash string
	Feedback           string
	IntentHash         string
}

type AuthoringCodeDecision struct {
	AuthoringCodeDecisionRequest
	Actor                  string
	ResultPipelineRevision int64
	CreatedAt              time.Time
}
