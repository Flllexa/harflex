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
