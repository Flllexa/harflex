package application

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/repositories"
	"github.com/persioflexa/harflex/internal/sdd"
)

var ErrPipelineNotFound = errors.New("pipeline not found")

func pipelineDTO(run catalog.PipelineRun) PipelineDTO {
	status := make(map[string]string, len(run.Status))
	for stage, value := range run.Status {
		status[string(stage)] = string(value)
	}
	artifacts := make(map[string]PipelineArtifactDTO, len(run.Artifacts))
	for stage, artifact := range run.Artifacts {
		item := PipelineArtifactDTO{Stage: string(artifact.Stage), Version: artifact.Version, Content: artifact.Content, Author: artifact.Author, SourceSessionID: artifact.SourceSessionID, UpdatedAt: artifact.UpdatedAt}
		if stage == sdd.Code || stage == sdd.Eval {
			digest := sha256.Sum256([]byte(artifact.Content))
			item.ContentDigest = hex.EncodeToString(digest[:])
			for _, review := range run.ExecutionReviews {
				if review.Stage == stage && review.ArtifactVersion == artifact.Version {
					item.ReviewStatus = review.Decision
					item.ReviewActor = review.Actor
					item.ReviewFeedback = review.Feedback
					item.ReviewedAt = review.CreatedAt
				}
			}
			if item.ReviewStatus == "" && run.Status[stage] == sdd.WaitingUser {
				item.ReviewStatus = "waiting_user"
			}
			if item.ReviewStatus == "" && artifact.Author == "ai" && artifact.SourceSessionID != "" &&
				(run.Status[stage] == sdd.Completed || run.Status[stage] == sdd.Failed) {
				item.ReviewStatus = "legacy_unreviewed"
			}
			if stage == sdd.Eval && pipelineEvalArtifactStale(run, artifact) {
				item.ReviewStatus = "stale"
			}
		}
		artifacts[string(stage)] = item
	}
	reviews := make([]PipelineExecutionReviewDTO, 0, len(run.ExecutionReviews))
	for _, review := range run.ExecutionReviews {
		reviews = append(reviews, PipelineExecutionReviewDTO{Stage: string(review.Stage), Version: review.ArtifactVersion, Content: review.ArtifactContent, ContentDigest: review.ArtifactDigest,
			SourceSessionID: review.SourceSessionID, Decision: review.Decision, Actor: review.Actor, Feedback: review.Feedback, CreatedAt: review.CreatedAt})
	}
	archivedArtifacts := make([]PipelineArchivedArtifactDTO, 0, len(run.ArchivedArtifacts))
	for _, artifact := range run.ArchivedArtifacts {
		archivedArtifacts = append(archivedArtifacts, PipelineArchivedArtifactDTO{Stage: string(artifact.Stage), Version: artifact.Version, Content: artifact.Content, Author: artifact.Author,
			SourceSessionID: artifact.SourceSessionID, ContentDigest: artifact.ContentDigest, Reason: artifact.Reason, CreatedAt: artifact.CreatedAt})
	}
	experience := ""
	if run.Kind == "ai_authoring" {
		experience = "conversational"
	}
	return PipelineDTO{ID: run.ID, WorkspaceID: run.WorkspaceID, Kind: run.Kind, PreparationExperience: experience, DerivedFromPipelineID: run.DerivedFromPipelineID, DiscoveryFrozenVersion: run.DiscoveryFrozenVersion, Title: run.Title, Objective: run.Objective, CurrentStage: string(run.Current), StageStatus: status, Revision: run.Revision, Artifacts: artifacts, ArchivedArtifacts: archivedArtifacts, ExecutionReviews: reviews, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}
}

func pipelineEvalArtifactStale(run catalog.PipelineRun, artifact catalog.PipelineArtifact) bool {
	if artifact.Author != "ai" || artifact.SourceSessionID == "" {
		return false
	}
	if run.Status[sdd.Eval] == sdd.Pending || run.Status[sdd.Eval] == sdd.Active {
		return true
	}
	for _, review := range run.ExecutionReviews {
		if review.Stage == sdd.Code && review.CreatedAt.After(artifact.UpdatedAt) {
			return true
		}
	}
	return false
}

func (s *Service) pipelineDTOWithCodeApplyStatus(run catalog.PipelineRun) (PipelineDTO, error) {
	dto := pipelineDTO(run)
	code := run.Artifacts[sdd.Code]
	if code.SourceSessionID == "" {
		return dto, nil
	}
	links, err := s.store.ListPipelineSessions(s.ctx, run.ID, "coder")
	if err != nil {
		return PipelineDTO{}, safe("read Code recovery state", err)
	}
	var source *catalog.PipelineSession
	for index := range links {
		if links[index].SessionID == code.SourceSessionID {
			source = &links[index]
			break
		}
	}
	if source != nil && !source.ExecutionAppliedAt.IsZero() {
		appliedAt := source.ExecutionAppliedAt
		dto.CodeAppliedAt = &appliedAt
	}
	// Code runs from before isolation wrote straight into the project: there is nothing left to apply for them.
	dto.CodePatchPending = source != nil && len(source.ExecutionSnapshot) > 0 && source.ExecutionAppliedAt.IsZero()
	if recoveryStatusNeeded(run) {
		dto.CodeReviewRecoveryStatus, dto.CodeReviewRecoveryReason = s.pipelineCodeReviewRecoveryStatus(run, source)
	}
	return dto, nil
}

func recoveryStatusNeeded(run catalog.PipelineRun) bool {
	return run.Status[sdd.Code] == sdd.Completed && run.Artifacts[sdd.Code].Author == "ai" &&
		run.Artifacts[sdd.Code].Content != "" && run.Artifacts[sdd.Code].SourceSessionID != "" &&
		(run.Current == sdd.Eval || run.Current == "") && !pipelineArtifactApproved(run, sdd.Code)
}

func (s *Service) pipelineCodeReviewRecoveryStatus(run catalog.PipelineRun, link *catalog.PipelineSession) (string, string) {
	if link == nil {
		return "snapshot_unavailable", "A sessão original de Code não está mais vinculada. Crie uma nova execução a partir do Discovery salvo."
	}
	snapshot, err := s.executionSnapshotForLink(*link)
	if err != nil {
		return "snapshot_unavailable", "O snapshot isolado desta versão não está disponível. Crie uma nova execução a partir do Discovery salvo."
	}
	if _, err := s.executionRootForSession(link.SessionID); err != nil {
		return "snapshot_unavailable", "A pasta isolada desta sessão não pode ser verificada. Crie uma nova execução a partir do Discovery salvo."
	}
	if !link.ExecutionAppliedAt.IsZero() {
		return "already_applied", "Esta versão de Code já foi aplicada ao workspace. Crie uma nova execução a partir do Discovery salvo para revisar outro patch."
	}
	applied, err := repositories.ExecutionSnapshotApplied(s.ctx, snapshot)
	if err != nil {
		return "snapshot_unavailable", "Não foi possível confirmar o estado do workspace. Crie uma nova execução a partir do Discovery salvo."
	}
	if applied {
		return "already_applied", "Esta versão de Code já está aplicada ao workspace. Crie uma nova execução a partir do Discovery salvo para revisar outro patch."
	}
	evidence, _, err := pipelineCodeEvidence(s.ctx, snapshot, 1024*1024)
	if err != nil || evidence != run.Artifacts[sdd.Code].Content {
		return "source_drift", "O snapshot ou a fonte desta versão mudou. Crie uma nova execução a partir do Discovery salvo para obter uma revisão íntegra."
	}
	return "available", ""
}

func (s *Service) loadPipeline(id string) (catalog.PipelineRun, error) {
	if id == "" {
		return catalog.PipelineRun{}, ErrInvalidInput
	}
	run, err := s.store.GetPipeline(s.ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.PipelineRun{}, ErrPipelineNotFound
	}
	if err != nil {
		return catalog.PipelineRun{}, safe("get pipeline", err)
	}
	return run, nil
}

func (s *Service) CreatePipeline(in CreatePipelineInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	in.Title, in.Objective = strings.TrimSpace(in.Title), strings.TrimSpace(in.Objective)
	if in.WorkspaceID == "" || len(in.Title) == 0 || len(in.Title) > 200 || len(in.Objective) == 0 || len(in.Objective) > 5000 {
		return PipelineDTO{}, ErrInvalidInput
	}
	if _, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID); errors.Is(err, sql.ErrNoRows) {
		return PipelineDTO{}, ErrWorkspaceNotFound
	} else if err != nil {
		return PipelineDTO{}, safe("get workspace", err)
	}
	flow := sdd.NewFlow()
	now := time.Now().UTC()
	run := catalog.PipelineRun{ID: id.New(), WorkspaceID: in.WorkspaceID, Kind: "legacy", Title: in.Title, Objective: in.Objective, Current: flow.Current, Status: flow.Status, Revision: 1, Artifacts: map[sdd.Stage]catalog.PipelineArtifact{}, CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreatePipeline(s.ctx, run); err != nil {
		return PipelineDTO{}, safe("create pipeline", err)
	}
	return s.pipelineDTOWithCodeApplyStatus(run)
}

func (s *Service) ListPipelines(workspaceID string) ([]PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	if _, err := s.store.GetWorkspace(s.ctx, workspaceID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkspaceNotFound
	} else if err != nil {
		return nil, safe("get workspace", err)
	}
	runs, err := s.store.ListPipelines(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("list pipelines", err)
	}
	result := make([]PipelineDTO, 0, len(runs))
	for _, run := range runs {
		result = append(result, pipelineDTO(run))
	}
	return result, nil
}

func (s *Service) GetPipeline(id string) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	run, err := s.loadPipeline(id)
	if err != nil {
		return PipelineDTO{}, err
	}
	return s.pipelineDTOWithCodeApplyStatus(run)
}

func (s *Service) SavePipelineArtifact(in SavePipelineArtifactInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	if run.Kind == "ai_authoring" {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	if in.Stage != string(run.Current) || run.Status[run.Current] != sdd.Active || strings.TrimSpace(in.Content) == "" || len(in.Content) > 1024*1024 {
		return PipelineDTO{}, ErrInvalidInput
	}
	if run.Current == sdd.PRs {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	if err := s.store.SavePipelineArtifact(s.ctx, run.ID, run.Current, in.Content, run.Revision); err != nil {
		return PipelineDTO{}, safe("save pipeline artifact", err)
	}
	updated, err := s.loadPipeline(run.ID)
	if err != nil {
		return PipelineDTO{}, err
	}
	return pipelineDTO(updated), nil
}

func (s *Service) AdvancePipeline(id string) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	run, err := s.loadPipeline(id)
	if err != nil {
		return PipelineDTO{}, err
	}
	if run.Kind == "ai_authoring" {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	if run.Current == sdd.Code || run.Current == sdd.Eval {
		return PipelineDTO{}, sdd.ErrEvidenceRequired
	}
	if run.Current == sdd.PRs {
		// The pull request stage ends through FinishPipelinePRs, which needs the agent's report.
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	artifact := run.Artifacts[run.Current]
	flow, err := sdd.Advance(sdd.Flow{Current: run.Current, Status: run.Status}, strings.TrimSpace(artifact.Content) != "")
	if err != nil {
		return PipelineDTO{}, err
	}
	if err := s.store.TransitionPipeline(s.ctx, run.ID, run.Current, flow, "advance", "", run.Revision); err != nil {
		return PipelineDTO{}, safe("advance pipeline", err)
	}
	updated, err := s.loadPipeline(run.ID)
	if err != nil {
		return PipelineDTO{}, err
	}
	return pipelineDTO(updated), nil
}

func (s *Service) SkipPipelineStage(in SkipPipelineStageInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	if len(in.Reason) > 1000 {
		return PipelineDTO{}, ErrInvalidInput
	}
	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	if run.Kind == "ai_authoring" {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	flow, err := sdd.Skip(sdd.Flow{Current: run.Current, Status: run.Status})
	if err != nil {
		return PipelineDTO{}, err
	}
	if err := s.store.TransitionPipeline(s.ctx, run.ID, run.Current, flow, "skip", strings.TrimSpace(in.Reason), run.Revision); err != nil {
		return PipelineDTO{}, safe("skip pipeline stage", err)
	}
	updated, err := s.loadPipeline(run.ID)
	if err != nil {
		return PipelineDTO{}, err
	}
	return pipelineDTO(updated), nil
}
