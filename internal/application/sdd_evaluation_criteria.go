package application

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

func digestEvaluationCriteriaContent(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func evaluationCriteriaDigest(snapshot catalog.PipelineEvaluationCriteriaSnapshot) (string, error) {
	snapshot.Digest = ""
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func evaluationCriteriaText(snapshot catalog.PipelineEvaluationCriteriaSnapshot) string {
	if snapshot.SourceStage == sdd.Spec || snapshot.SynthesisContent == "" {
		return snapshot.SourceContent
	}
	return snapshot.SourceContent + "\nSíntese de brainstorming aprovada:\n" + snapshot.SynthesisContent
}

func (s *Service) pipelineEvaluationCriteria(run catalog.PipelineRun) (catalog.PipelineEvaluationCriteriaSnapshot, error) {
	discovery, ok := run.Artifacts[sdd.Discovery]
	if !ok || strings.TrimSpace(discovery.Content) == "" || (run.Status[sdd.Discovery] != sdd.Completed && run.Status[sdd.Discovery] != sdd.Skipped) {
		return catalog.PipelineEvaluationCriteriaSnapshot{}, sdd.ErrEvidenceRequired
	}
	discoveryVersion := discovery.Version
	if run.DiscoveryFrozenVersion > 0 {
		if discovery.Version != run.DiscoveryFrozenVersion {
			return catalog.PipelineEvaluationCriteriaSnapshot{}, ErrPipelineEvaluationCriteriaChanged
		}
		discoveryVersion = run.DiscoveryFrozenVersion
	}
	snapshot := catalog.PipelineEvaluationCriteriaSnapshot{DiscoveryVersion: discoveryVersion}
	if run.Status[sdd.Spec] == sdd.Completed {
		spec, exists := run.Artifacts[sdd.Spec]
		if !exists || strings.TrimSpace(spec.Content) == "" || spec.Version < 1 {
			return catalog.PipelineEvaluationCriteriaSnapshot{}, sdd.ErrEvidenceRequired
		}
		snapshot.SourceStage = sdd.Spec
		snapshot.SourceVersion = spec.Version
		snapshot.SourceContent = spec.Content
		snapshot.SourceDigest = digestEvaluationCriteriaContent(spec.Content)
	} else if run.Status[sdd.Spec] == sdd.Skipped {
		snapshot.SourceStage = sdd.Discovery
		snapshot.SourceVersion = discovery.Version
		snapshot.SourceContent = discovery.Content
		snapshot.SourceDigest = digestEvaluationCriteriaContent(discovery.Content)
		reason, err := s.pipelineStageBypassReason(run, sdd.Spec)
		if err != nil {
			return catalog.PipelineEvaluationCriteriaSnapshot{}, err
		}
		snapshot.Bypasses = append(snapshot.Bypasses, catalog.PipelineEvaluationBypass{Stage: string(sdd.Spec), Reason: reason})
	} else {
		return catalog.PipelineEvaluationCriteriaSnapshot{}, sdd.ErrEvidenceRequired
	}
	if run.Status[sdd.Discovery] == sdd.Skipped {
		reason, err := s.pipelineStageBypassReason(run, sdd.Discovery)
		if err != nil {
			return catalog.PipelineEvaluationCriteriaSnapshot{}, err
		}
		snapshot.Bypasses = append(snapshot.Bypasses, catalog.PipelineEvaluationBypass{Stage: string(sdd.Discovery), Reason: reason})
	}
	brainstorms, err := s.store.ListBrainstormingByPipeline(s.ctx, run.ID, 0)
	if err != nil {
		return catalog.PipelineEvaluationCriteriaSnapshot{}, safe("read approved brainstorming synthesis", err)
	}
	for _, brainstorm := range brainstorms {
		if brainstorm.DiscoveryVersion != discoveryVersion || brainstorm.DiscoveryContent != discovery.Content || brainstorm.State != "approved" {
			continue
		}
		snapshot.SynthesisRunID = brainstorm.ID
		for _, synthesis := range brainstorm.Syntheses {
			if synthesis.Version != brainstorm.SynthesisVersion || synthesis.DiscoveryVersion != discoveryVersion || synthesis.Status != "approved" {
				continue
			}
			encoded, err := json.Marshal(synthesis.Content)
			if err != nil {
				return catalog.PipelineEvaluationCriteriaSnapshot{}, fmt.Errorf("encode approved brainstorming synthesis: %w", err)
			}
			snapshot.SynthesisVersion = synthesis.Version
			snapshot.SynthesisContent = string(encoded)
			snapshot.SynthesisDigest = digestEvaluationCriteriaContent(snapshot.SynthesisContent)
			break
		}
		if snapshot.SynthesisVersion == 0 {
			for index := len(brainstorm.HumanActions) - 1; index >= 0; index-- {
				action := brainstorm.HumanActions[index]
				if action.Kind == "bypass" {
					snapshot.Bypasses = append(snapshot.Bypasses, catalog.PipelineEvaluationBypass{Stage: "brainstorming", Reason: action.Reason})
					break
				}
			}
		}
		break
	}
	snapshot.Digest, err = evaluationCriteriaDigest(snapshot)
	if err != nil {
		return catalog.PipelineEvaluationCriteriaSnapshot{}, fmt.Errorf("hash evaluation criteria snapshot: %w", err)
	}
	return snapshot, nil
}

func (s *Service) pipelineStageBypassReason(run catalog.PipelineRun, stage sdd.Stage) (string, error) {
	if run.Kind != "ai_authoring" {
		return s.store.GetPipelineStageSkipReason(s.ctx, run.ID, stage)
	}
	stageRun, err := s.store.GetAuthoringStage(s.ctx, run.ID, stage)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", safe("read SDD bypass reason", err)
	}
	for index := len(stageRun.Actions) - 1; index >= 0; index-- {
		if stageRun.Actions[index].Action == "skip" {
			return stageRun.Actions[index].Reason, nil
		}
	}
	return "", nil
}
