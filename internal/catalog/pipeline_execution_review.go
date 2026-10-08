package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/persioflexa/harflex/internal/sdd"
)

type PipelineExecutionReviewRequest struct {
	PipelineID       string
	RequestID        string
	Stage            sdd.Stage
	ArtifactVersion  int64
	ArtifactDigest   string
	Decision         string
	Feedback         string
	ExpectedRevision int64
}

type PipelineExecutionReviewRecoveryRequest struct {
	PipelineID       string
	ArtifactVersion  int64
	ArtifactDigest   string
	ExpectedRevision int64
}

type PipelineExecutionReview struct {
	PipelineID      string
	RequestID       string
	Stage           sdd.Stage
	ArtifactVersion int64
	ArtifactDigest  string
	ArtifactContent string
	SourceSessionID string
	Decision        string
	Actor           string
	Feedback        string
	ResultRevision  int64
	CreatedAt       time.Time
}

type PipelineEvaluationCriteriaSnapshot struct {
	SourceStage      sdd.Stage                  `json:"sourceStage"`
	SourceVersion    int64                      `json:"sourceVersion"`
	SourceDigest     string                     `json:"sourceDigest"`
	DiscoveryVersion int64                      `json:"discoveryVersion"`
	SynthesisRunID   string                     `json:"synthesisRunId,omitempty"`
	SynthesisVersion int                        `json:"synthesisVersion,omitempty"`
	SynthesisDigest  string                     `json:"synthesisDigest,omitempty"`
	SynthesisContent string                     `json:"synthesisContent,omitempty"`
	Bypasses         []PipelineEvaluationBypass `json:"bypasses,omitempty"`
	SourceContent    string                     `json:"sourceContent"`
	Digest           string                     `json:"digest"`
}

type PipelineEvaluationBypass struct {
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}

func (in PipelineExecutionReviewRequest) Hash() (string, error) {
	encoded, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
