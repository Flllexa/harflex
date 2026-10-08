package catalog

import "time"

type BrainstormHumanAction struct {
	RequestID        string    `json:"requestId"`
	Kind             string    `json:"kind"`
	Actor            string    `json:"actor"`
	SynthesisVersion int       `json:"synthesisVersion"`
	Feedback         string    `json:"feedback"`
	Reason           string    `json:"reason"`
	Choice           string    `json:"choice"`
	ResultRevision   int64     `json:"resultRevision"`
	PipelineRevision int64     `json:"pipelineRevision"`
	CreatedAt        time.Time `json:"createdAt"`
}

type ApproveBrainstormSynthesisRequest struct {
	BrainstormRequest
	SynthesisVersion int
}

type RequestBrainstormRevisionRequest struct {
	BrainstormRequest
	SynthesisVersion int
	Choice           string
	Feedback         string
}

type SkipBrainstormQuestionsRequest struct {
	BrainstormRequest
	Reason string
}

type CancelBrainstormAttemptRequest struct {
	BrainstormRequest
	AttemptID string
}
