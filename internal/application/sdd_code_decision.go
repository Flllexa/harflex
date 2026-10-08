package application

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

type DecideAuthoringCodeInput struct {
	PipelineID         string `json:"pipelineId"`
	PipelineRevision   int64  `json:"pipelineRevision"`
	RunID              string `json:"runId"`
	RequestID          string `json:"requestId"`
	Action             string `json:"action"`
	PatchHash          string `json:"patchHash"`
	BaselineHash       string `json:"baselineHash"`
	SourceManifestHash string `json:"sourceManifestHash"`
	ResultManifestHash string `json:"resultManifestHash"`
	Feedback           string `json:"feedback"`
}

type AuthoringCodeDecisionDTO struct {
	PipelineID             string `json:"pipelineId"`
	PipelineRevision       int64  `json:"pipelineRevision"`
	RunID                  string `json:"runId"`
	RequestID              string `json:"requestId"`
	Action                 string `json:"action"`
	PatchHash              string `json:"patchHash"`
	BaselineHash           string `json:"baselineHash"`
	SourceManifestHash     string `json:"sourceManifestHash"`
	ResultManifestHash     string `json:"resultManifestHash"`
	Feedback               string `json:"feedback"`
	Actor                  string `json:"actor"`
	ResultPipelineRevision int64  `json:"resultPipelineRevision"`
	CreatedAt              string `json:"createdAt"`
}

type authoringCodeDecisionIntent struct {
	PipelineID         string `json:"pipelineId"`
	PipelineRevision   int64  `json:"pipelineRevision"`
	RunID              string `json:"runId"`
	RequestID          string `json:"requestId"`
	Action             string `json:"action"`
	PatchHash          string `json:"patchHash"`
	BaselineHash       string `json:"baselineHash"`
	SourceManifestHash string `json:"sourceManifestHash"`
	ResultManifestHash string `json:"resultManifestHash"`
	Feedback           string `json:"feedback"`
}

func (s *Service) DecideAuthoringCode(in DecideAuthoringCodeInput) (AuthoringCodeDecisionDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringCodeDecisionDTO{}, err
	}
	defer s.endCall()
	if !validDecideAuthoringCodeInput(in) {
		return AuthoringCodeDecisionDTO{}, ErrInvalidInput
	}
	intentHash, err := authoringCodeDecisionIntentHash(in)
	if err != nil {
		return AuthoringCodeDecisionDTO{}, err
	}
	request := catalog.AuthoringCodeDecisionRequest{
		PipelineID: in.PipelineID, PipelineRevision: in.PipelineRevision, RunID: in.RunID,
		RequestID: in.RequestID, Action: in.Action, PatchHash: in.PatchHash,
		BaselineHash: in.BaselineHash, SourceManifestHash: in.SourceManifestHash,
		ResultManifestHash: in.ResultManifestHash, Feedback: in.Feedback, IntentHash: intentHash,
	}
	if prior, readErr := s.store.GetAuthoringCodeDecision(s.ctx, in.PipelineID, in.RequestID); readErr == nil {
		if prior.IntentHash != intentHash {
			return AuthoringCodeDecisionDTO{}, sqlite.ErrPipelineConflict
		}
		return authoringCodeDecisionDTO(prior), nil
	} else if !errors.Is(readErr, sql.ErrNoRows) {
		return AuthoringCodeDecisionDTO{}, safe("read prior authoring Code decision", readErr)
	}
	pipeline, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return AuthoringCodeDecisionDTO{}, err
	}
	if pipeline.Current != sdd.Code || pipeline.Status[sdd.Code] != sdd.Active || pipeline.Revision != in.PipelineRevision {
		return AuthoringCodeDecisionDTO{}, sqlite.ErrPipelineConflict
	}
	run, err := s.store.GetAuthoringCodeRunByID(s.ctx, in.RunID)
	if errors.Is(err, sql.ErrNoRows) || err == sqlite.ErrAuthoringCodeRunNotFound {
		return AuthoringCodeDecisionDTO{}, sqlite.ErrPipelineConflict
	}
	if err != nil {
		return AuthoringCodeDecisionDTO{}, safe("read authoring Code decision run", err)
	}
	if run.PipelineID != in.PipelineID || run.PipelineRevision != in.PipelineRevision || run.Status != "completed" ||
		run.PatchHash != in.PatchHash || run.ManifestHash != in.BaselineHash || run.SourceManifest == nil ||
		run.ResultManifest == nil || run.SourceManifest.Hash != in.SourceManifestHash || run.ResultManifest.Hash != in.ResultManifestHash {
		return AuthoringCodeDecisionDTO{}, sqlite.ErrPipelineConflict
	}
	runs, err := s.store.ListAuthoringCodeRuns(s.ctx, in.PipelineID)
	if err != nil {
		return AuthoringCodeDecisionDTO{}, safe("read current authoring Code run", err)
	}
	if len(runs) == 0 || runs[len(runs)-1].ID != run.ID {
		return AuthoringCodeDecisionDTO{}, sqlite.ErrPipelineConflict
	}
	if err := s.verifyCurrentAuthoringCodeEvidence(run); err != nil {
		return AuthoringCodeDecisionDTO{}, ErrAuthoringCodePatchUnavailable
	}
	decision, err := s.store.DecideAuthoringCode(s.ctx, request)
	if err != nil {
		return AuthoringCodeDecisionDTO{}, safe("persist authoring Code decision", err)
	}
	if decision.IntentHash != intentHash {
		return AuthoringCodeDecisionDTO{}, sqlite.ErrPipelineConflict
	}
	return authoringCodeDecisionDTO(decision), nil
}

func validDecideAuthoringCodeInput(in DecideAuthoringCodeInput) bool {
	if !validSelectionText(in.PipelineID, 128) || in.PipelineRevision < 1 || !validSelectionText(in.RunID, 128) ||
		!authoringRequestID.MatchString(in.RequestID) || !authoringCodeManifestHash.MatchString(in.PatchHash) ||
		!authoringCodeManifestHash.MatchString(in.BaselineHash) || !authoringCodeManifestHash.MatchString(in.SourceManifestHash) ||
		!authoringCodeManifestHash.MatchString(in.ResultManifestHash) {
		return false
	}
	switch in.Action {
	case "approve":
		return in.Feedback == ""
	case "request_revision":
		return validAuthoringFeedback(in.Feedback)
	default:
		return false
	}
}

func authoringCodeDecisionIntentHash(in DecideAuthoringCodeInput) (string, error) {
	encoded, err := json.Marshal(authoringCodeDecisionIntent{
		PipelineID: in.PipelineID, PipelineRevision: in.PipelineRevision, RunID: in.RunID,
		RequestID: in.RequestID, Action: in.Action, PatchHash: in.PatchHash,
		BaselineHash: in.BaselineHash, SourceManifestHash: in.SourceManifestHash,
		ResultManifestHash: in.ResultManifestHash, Feedback: in.Feedback,
	})
	if err != nil {
		return "", fmt.Errorf("encode authoring Code decision intent: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func authoringCodeDecisionDTO(decision catalog.AuthoringCodeDecision) AuthoringCodeDecisionDTO {
	return AuthoringCodeDecisionDTO{
		PipelineID: decision.PipelineID, PipelineRevision: decision.PipelineRevision,
		RunID: decision.RunID, RequestID: decision.RequestID, Action: decision.Action,
		PatchHash: decision.PatchHash, BaselineHash: decision.BaselineHash,
		SourceManifestHash: decision.SourceManifestHash, ResultManifestHash: decision.ResultManifestHash,
		Feedback: decision.Feedback, Actor: decision.Actor,
		ResultPipelineRevision: decision.ResultPipelineRevision,
		CreatedAt:              decision.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}
