package catalog

import (
	"encoding/json"
	"time"
)

type PipelineSession struct {
	ID                    string
	PipelineID            string
	SessionID             string
	Role                  string
	BaselineGitHash       string
	BaselineGitFiles      []string
	BaselineGitFileHashes map[string]string
	ExecutionSnapshot     json.RawMessage
	ExecutionAppliedAt    time.Time
	EvaluationCriteria    json.RawMessage
	CreatedAt             time.Time
}

// PipelineSessionHead is a pipeline link without its baseline and snapshot, which hold the project's whole file
// list: enough to tell which conversation is a pipeline's latest Coder or QA and whether it ran in a private copy.
type PipelineSessionHead struct {
	ID                   string
	SessionID            string
	Role                 string
	HasExecutionSnapshot bool
	CreatedAt            time.Time
}
