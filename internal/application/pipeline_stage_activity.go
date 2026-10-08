package application

import (
	"bytes"
	"database/sql"
	"errors"
	"time"

	"github.com/persioflexa/harflex/internal/sdd"
)

type PipelineStageActivityDTO struct {
	PipelineID    string    `json:"pipelineId"`
	WorkspaceID   string    `json:"workspaceId"`
	Stage         string    `json:"stage"`
	Status        string    `json:"status"`
	Phase         string    `json:"phase"`
	SessionID     string    `json:"sessionId"`
	ModelID       string    `json:"modelId"`
	AttemptStatus string    `json:"attemptStatus,omitempty"`
	ErrorCode     string    `json:"errorCode,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Stage inspection reads durable ownership and never admits model inference.
func (s *Service) GetPipelineStageActivity(pipelineID, stage string) (PipelineStageActivityDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineStageActivityDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(pipelineID, 128) || !designStage(stage) && stage != "code" && stage != "eval" && stage != "prs" {
		return PipelineStageActivityDTO{}, ErrInvalidInput
	}
	run, err := s.loadPipeline(pipelineID)
	if err != nil {
		return PipelineStageActivityDTO{}, err
	}
	result := PipelineStageActivityDTO{PipelineID: run.ID, WorkspaceID: run.WorkspaceID, Stage: stage, Status: string(run.Status[sdd.Stage(stage)]), UpdatedAt: run.UpdatedAt}
	if result.Status == "" {
		result.Status = "pending"
	}
	if designStage(stage) {
		design, err := s.store.GetPipelineDesign(s.ctx, run.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, safe("read document activity", err)
		}
		if err == nil {
			result.Phase = string(design.Phase)
			for index := len(design.Attempts) - 1; index >= 0; index-- {
				attempt := design.Attempts[index]
				if _, selected := attempt.Selections[sdd.Stage(stage)]; selected {
					result.SessionID = attempt.SessionIDs[sdd.Stage(stage)]
					result.AttemptStatus, result.ErrorCode = attempt.Status, attempt.ErrorCode
					break
				}
			}
			if result.SessionID == "" && result.AttemptStatus == "" {
				result.SessionID = design.Documents[sdd.Stage(stage)].SourceSessionID
			}
			if result.SessionID == "" && design.Documents[sdd.Stage(stage)].Version > 0 {
				result.Status = "ready"
			}
			if design.Documents[sdd.Stage(stage)].Stale {
				result.Status = "stale"
			}
		}
	} else if stage == "prs" {
		links, err := s.store.ListPipelinePRSessions(s.ctx, run.ID)
		if err != nil {
			return result, safe("read pull request activity", err)
		}
		if len(links) > 0 {
			result.SessionID = links[len(links)-1].SessionID
		}
	} else {
		role := "coder"
		if stage == "eval" {
			role = "evaluator"
		}
		link, err := s.latestPipelineSession(run.ID, role)
		if err == nil {
			obsolete := run.Status[sdd.Stage(stage)] == sdd.Active && run.Artifacts[sdd.Stage(stage)].SourceSessionID == link.SessionID
			if stage == "eval" {
				if coder, coderErr := s.latestPipelineSession(run.ID, "coder"); coderErr == nil && !bytes.Equal(coder.ExecutionSnapshot, link.ExecutionSnapshot) {
					obsolete = true
				}
			}
			if !obsolete {
				result.SessionID = link.SessionID
			}
		} else if !errors.Is(err, sdd.ErrEvidenceRequired) {
			return result, err
		}
	}
	if result.SessionID == "" {
		if result.Status == "active" {
			result.Status = "ready"
		}
		if result.AttemptStatus == "running" || result.AttemptStatus == "cancellation_pending" {
			result.Status = result.AttemptStatus
		}
		if result.ErrorCode != "" {
			result.Status = result.AttemptStatus
		}
		return result, nil
	}
	record, err := s.store.GetSession(s.ctx, result.SessionID)
	if err != nil || record.WorkspaceID != run.WorkspaceID {
		return result, ErrSessionNotFound
	}
	if result.Status != "stale" && result.Status != "waiting_user" {
		result.Status = record.Status
	}
	if result.AttemptStatus == "failed" || result.AttemptStatus == "stale" || result.AttemptStatus == "interrupted" || result.AttemptStatus == "cancelled" || result.AttemptStatus == "cancellation_pending" {
		result.Status = result.AttemptStatus
	}
	result.UpdatedAt = record.UpdatedAt
	if selection, err := s.store.GetSessionModelSelection(s.ctx, record.ID); err == nil {
		result.ModelID = selection.ModelID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, safe("read activity model", err)
	}
	return result, nil
}
