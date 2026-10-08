package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/repositories"
	"github.com/persioflexa/harflex/internal/sdd"
)

const maxPipelineReviewFeedbackBytes = 8192

func pipelineArtifactDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func pipelineArtifactApproved(run catalog.PipelineRun, stage sdd.Stage) bool {
	artifact := run.Artifacts[stage]
	digest := pipelineArtifactDigest(artifact.Content)
	for _, review := range run.ExecutionReviews {
		if review.Stage == stage && review.ArtifactVersion == artifact.Version && review.ArtifactDigest == digest && review.Decision == "approve" {
			return true
		}
	}
	return false
}

func pipelineArtifactRevisionRequested(run catalog.PipelineRun, stage sdd.Stage, sessionID string) bool {
	artifact := run.Artifacts[stage]
	for _, review := range run.ExecutionReviews {
		if review.Stage == stage && review.ArtifactVersion == artifact.Version && review.SourceSessionID == sessionID && review.Decision == "request_revision" {
			return true
		}
	}
	return false
}

func (s *Service) DecidePipelineExecutionArtifact(in DecidePipelineExecutionArtifactInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	stage := sdd.Stage(in.Stage)
	if !authoringRequestID.MatchString(in.RequestID) || (stage != sdd.Code && stage != sdd.Eval) || in.ArtifactVersion < 1 ||
		len(in.ArtifactDigest) != 64 || !validSelectionText(in.ArtifactDigest, 64) || (in.Decision != "approve" && in.Decision != "request_revision") ||
		len(in.Feedback) > maxPipelineReviewFeedbackBytes || !utf8.ValidString(in.Feedback) ||
		(in.Decision == "approve" && in.Feedback != "") || (in.Decision == "request_revision" && strings.TrimSpace(in.Feedback) == "") {
		return PipelineDTO{}, ErrInvalidInput
	}
	request := catalog.PipelineExecutionReviewRequest{PipelineID: in.PipelineID, RequestID: in.RequestID, Stage: stage, ArtifactVersion: in.ArtifactVersion,
		ArtifactDigest: in.ArtifactDigest, Decision: in.Decision, Feedback: in.Feedback, ExpectedRevision: in.PipelineRevision}
	requestHash, err := request.Hash()
	if err != nil {
		return PipelineDTO{}, safe("hash pipeline review intent", err)
	}
	if replay, found, err := s.store.GetPipelineExecutionReviewResult(s.ctx, in.PipelineID, in.RequestID, requestHash); err != nil {
		return PipelineDTO{}, err
	} else if found {
		return s.pipelineDTOWithCodeApplyStatus(replay)
	}
	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	artifact := run.Artifacts[stage]
	if run.Revision != in.PipelineRevision || run.Current != stage || run.Status[stage] != sdd.WaitingUser ||
		artifact.Version != in.ArtifactVersion || artifact.Content == "" || artifact.SourceSessionID == "" || artifact.Author != "ai" ||
		pipelineArtifactDigest(artifact.Content) != in.ArtifactDigest {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	role := "coder"
	if stage == sdd.Code {
		link, err := s.latestPipelineSession(run.ID, role)
		if err != nil || link.SessionID != artifact.SourceSessionID {
			return PipelineDTO{}, ErrPipelineCodeSnapshotUnavailable
		}
		snapshot, err := s.executionSnapshotForLink(link)
		if err != nil {
			return PipelineDTO{}, err
		}
		evidence, _, err := pipelineCodeEvidence(s.ctx, snapshot, 1024*1024)
		if err != nil || evidence != artifact.Content {
			return PipelineDTO{}, repositories.ErrExecutionSourceDrift
		}
	} else {
		role = "evaluator"
		link, err := s.latestPipelineSession(run.ID, role)
		if err != nil || link.SessionID != artifact.SourceSessionID {
			return PipelineDTO{}, ErrPipelineCodeSnapshotUnavailable
		}
		criteria, err := s.pipelineEvaluationCriteria(run)
		var admitted catalog.PipelineEvaluationCriteriaSnapshot
		if err != nil || len(link.EvaluationCriteria) == 0 || json.Unmarshal(link.EvaluationCriteria, &admitted) != nil || admitted.Digest != criteria.Digest {
			return PipelineDTO{}, ErrPipelineEvaluationCriteriaChanged
		}
	}
	updated, err := s.store.DecidePipelineExecutionArtifact(s.ctx, request)
	if err != nil {
		return PipelineDTO{}, safe("record pipeline execution review", err)
	}
	return s.pipelineDTOWithCodeApplyStatus(updated)
}

func (s *Service) ReopenPipelineCodeReview(in ReopenPipelineCodeReviewInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	if in.PipelineID == "" || in.ArtifactVersion < 1 || in.PipelineRevision < 1 || len(in.ArtifactDigest) != 64 || !validSelectionText(in.ArtifactDigest, 64) {
		return PipelineDTO{}, ErrInvalidInput
	}
	if _, err := hex.DecodeString(in.ArtifactDigest); err != nil {
		return PipelineDTO{}, ErrInvalidInput
	}
	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	artifact := run.Artifacts[sdd.Code]
	if artifact.Version != in.ArtifactVersion || artifact.Author != "ai" || artifact.Content == "" || artifact.SourceSessionID == "" || pipelineArtifactDigest(artifact.Content) != in.ArtifactDigest {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	if !(run.Current == sdd.Code && run.Status[sdd.Code] == sdd.WaitingUser) {
		link, err := s.latestPipelineSession(run.ID, "coder")
		if err != nil || link.SessionID != artifact.SourceSessionID {
			return PipelineDTO{}, ErrPipelineCodeSnapshotUnavailable
		}
		status, _ := s.pipelineCodeReviewRecoveryStatus(run, &link)
		switch status {
		case "available":
		case "already_applied":
			return PipelineDTO{}, repositories.ErrExecutionApplyConflict
		case "source_drift":
			return PipelineDTO{}, repositories.ErrExecutionSourceDrift
		default:
			return PipelineDTO{}, ErrPipelineCodeSnapshotUnavailable
		}
	}
	updated, err := s.store.ReopenPipelineCodeReview(s.ctx, catalog.PipelineExecutionReviewRecoveryRequest{PipelineID: in.PipelineID, ArtifactVersion: in.ArtifactVersion,
		ArtifactDigest: in.ArtifactDigest, ExpectedRevision: in.PipelineRevision})
	if err != nil {
		return PipelineDTO{}, safe("reopen Code review", err)
	}
	return s.pipelineDTOWithCodeApplyStatus(updated)
}
