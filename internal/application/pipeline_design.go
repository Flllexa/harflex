package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

type pipelineDesignOwner struct {
	pipelineID string
	cancel     context.CancelFunc
	done       chan struct{}
}

func validPipelineDesignRef(ref PipelineDesignRefInput) bool {
	return validSelectionText(ref.PipelineID, 128) && authoringRequestID.MatchString(ref.RequestID) && ref.PipelineRevision > 0 && ref.DesignRevision > 0
}
func designStage(stage string) bool {
	return stage == "discovery" || stage == "spec" || stage == "plan"
}

func (s *Service) OpenPipelineDesign(pipelineID string) (PipelineDesignDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDesignDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(pipelineID, 128) {
		return PipelineDesignDTO{}, ErrInvalidInput
	}
	workspace, err := s.openPipelineDesign(s.ctx, pipelineID)
	if err != nil {
		return PipelineDesignDTO{}, err
	}
	return pipelineDesignDTO(workspace), nil
}

func (s *Service) openPipelineDesign(ctx context.Context, pipelineID string) (catalog.PipelineDesignWorkspace, error) {
	if existing, err := s.store.GetPipelineDesign(ctx, pipelineID); err == nil {
		if existing.State == "running" || existing.State == "cancellation_pending" {
			for _, attempt := range existing.Attempts {
				if attempt.ID == existing.ActiveAttemptID && pipelineDesignOwnerDead(attempt.OwnerPID) {
					return s.store.FailPipelineDesignAttempt(ctx, catalog.PipelineDesignFailRequest{PipelineID: pipelineID, AttemptID: attempt.ID, Status: "interrupted", ErrorCode: "owner_stopped"})
				}
			}
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return catalog.PipelineDesignWorkspace{}, safe("read draft workspace", err)
	}
	run, err := s.loadPipeline(pipelineID)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	if run.Kind != "ai_authoring" {
		return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidTransition
	}
	inherited, err := s.inheritedPipelineDesignDocuments(ctx, run)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	initial := map[sdd.Stage]catalog.PipelineDesignDocumentInput{}
	for _, stage := range designStages() {
		artifact := run.Artifacts[stage]
		content, source, author := artifact.Content, artifact.SourceSessionID, artifact.Author
		if stage != sdd.Discovery && content == "" && inherited[stage].Content != "" {
			initial[stage] = inherited[stage]
			continue
		}
		if stage != sdd.Discovery && content == "" {
			staged, stageErr := s.store.GetAuthoringStage(ctx, pipelineID, stage)
			if stageErr == nil {
				if staged.State == "running" || staged.State == "cancellation_pending" {
					return catalog.PipelineDesignWorkspace{}, sdd.ErrAuthoringCancellationPending
				}
				for _, draft := range staged.Artifacts {
					if draft.Version == staged.ArtifactVersion {
						content, source, author = string(draft.Content), draft.SourceSessionID, "ai"
					}
				}
			} else if !errors.Is(stageErr, sql.ErrNoRows) && !errors.Is(stageErr, sdd.ErrInvalidTransition) {
				return catalog.PipelineDesignWorkspace{}, safe("import document draft", stageErr)
			}
		}
		if content == "" {
			continue
		}
		content = designMarkdown(stage, content)
		if len(content) > catalog.MaxPipelineDesignDocumentBytes || !utf8.ValidString(content) {
			return catalog.PipelineDesignWorkspace{}, ErrInvalidInput
		}
		if stage == sdd.Discovery || author != "ai" || source == "" {
			author, source = "user", ""
		}
		input := catalog.PipelineDesignDocumentInput{Content: content, Author: author, SourceSessionID: source}
		if source != "" {
			if selection, selectionErr := s.store.GetSessionModelSelection(ctx, source); selectionErr == nil {
				input.Selection = selection
			}
		}
		initial[stage] = input
	}
	workspace, err := s.store.OpenPipelineDesign(ctx, pipelineID, run.Revision, initial)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, safe("open draft workspace", err)
	}
	return workspace, nil
}

func (s *Service) inheritedPipelineDesignDocuments(ctx context.Context, run catalog.PipelineRun) (map[sdd.Stage]catalog.PipelineDesignDocumentInput, error) {
	result := map[sdd.Stage]catalog.PipelineDesignDocumentInput{}
	if run.DerivedFromPipelineID == "" {
		return result, nil
	}
	parent, err := s.loadPipeline(run.DerivedFromPipelineID)
	if err != nil || parent.WorkspaceID != run.WorkspaceID {
		return nil, ErrInvalidInput
	}
	design, err := s.store.GetPipelineDesign(ctx, parent.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, safe("read parent documents", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		design.Documents = map[sdd.Stage]catalog.PipelineDesignDocument{}
		for _, stage := range designStages() {
			artifact := parent.Artifacts[stage]
			content := designMarkdown(stage, artifact.Content)
			document := catalog.PipelineDesignDocument{Stage: stage, Content: content, ContentDigest: catalog.PipelineDesignContentDigest(content), Author: artifact.Author, SourceSessionID: artifact.SourceSessionID}
			document.SourceDigest = catalog.PipelineDesignSourceDigest(stage, design.Documents)
			design.Documents[stage] = document
		}
	}
	for _, stage := range []sdd.Stage{sdd.Spec, sdd.Plan} {
		document := design.Documents[stage]
		if document.Content == "" {
			continue
		}
		result[stage] = catalog.PipelineDesignDocumentInput{Content: document.Content, Author: document.Author, SourceSessionID: document.SourceSessionID, SourceDigest: document.SourceDigest, Selection: document.Selection}
	}
	return result, nil
}

func designMarkdown(stage sdd.Stage, content string) string {
	if stage == sdd.Spec {
		var doc sdd.SpecDocument
		if json.Unmarshal([]byte(content), &doc) == nil && doc.Summary != "" && len(doc.Requirements) > 0 {
			var text strings.Builder
			fmt.Fprintf(&text, "# SPEC\n\n%s\n\n## Requisitos\n", doc.Summary)
			for _, item := range doc.Requirements {
				fmt.Fprintf(&text, "- %s\n", item)
			}
			if len(doc.NonGoals) > 0 {
				text.WriteString("\n## Fora do escopo\n")
				for _, item := range doc.NonGoals {
					fmt.Fprintf(&text, "- %s\n", item)
				}
			}
			text.WriteString("\n## Critérios de aceite\n")
			for _, item := range doc.AcceptanceCriteria {
				fmt.Fprintf(&text, "- **%s:** %s\n", item.ID, item.Criterion)
			}
			return strings.TrimSpace(text.String())
		}
	}
	if stage == sdd.Plan {
		var doc sdd.PlanDocument
		if json.Unmarshal([]byte(content), &doc) == nil && doc.Summary != "" && len(doc.Tasks) > 0 {
			var text strings.Builder
			fmt.Fprintf(&text, "# Plan\n\n%s\n", doc.Summary)
			for _, task := range doc.Tasks {
				fmt.Fprintf(&text, "\n## %s\n", task.Title)
				if len(task.Files) > 0 {
					fmt.Fprintf(&text, "\nArquivos: %s\n", strings.Join(task.Files, ", "))
				}
				for _, step := range task.Steps {
					fmt.Fprintf(&text, "- %s\n", step)
				}
				if len(task.Tests) > 0 {
					text.WriteString("\nValidação:\n")
					for _, test := range task.Tests {
						fmt.Fprintf(&text, "- %s\n", test)
					}
				}
			}
			if len(doc.Risks) > 0 {
				text.WriteString("\n## Riscos\n")
				for _, risk := range doc.Risks {
					fmt.Fprintf(&text, "- %s\n", risk)
				}
			}
			return strings.TrimSpace(text.String())
		}
	}
	return content
}

func (s *Service) EditPipelineDesignDocument(in EditPipelineDesignDocumentInput) (PipelineDesignDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDesignDTO{}, err
	}
	defer s.endCall()
	if !validPipelineDesignRef(in.Ref) || !designStage(in.Stage) || strings.TrimSpace(in.Content) == "" || len(in.Content) > catalog.MaxPipelineDesignDocumentBytes || !utf8.ValidString(in.Content) || strings.ContainsRune(in.Content, 0) {
		return PipelineDesignDTO{}, ErrInvalidInput
	}
	workspace, err := s.store.EditPipelineDesignDocument(s.ctx, catalog.PipelineDesignEditRequest{Ref: in.Ref.request(), Stage: sdd.Stage(in.Stage), Content: in.Content})
	if err != nil {
		return PipelineDesignDTO{}, safe("save document draft", err)
	}
	return pipelineDesignDTO(workspace), nil
}
func (s *Service) RestorePipelineDesignDocument(in RestorePipelineDesignDocumentInput) (PipelineDesignDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDesignDTO{}, err
	}
	defer s.endCall()
	if !validPipelineDesignRef(in.Ref) || !designStage(in.Stage) || in.Version < 1 {
		return PipelineDesignDTO{}, ErrInvalidInput
	}
	workspace, err := s.store.RestorePipelineDesignDocument(s.ctx, catalog.PipelineDesignRestoreRequest{Ref: in.Ref.request(), Stage: sdd.Stage(in.Stage), Version: in.Version})
	if err != nil {
		return PipelineDesignDTO{}, safe("restore document version", err)
	}
	return pipelineDesignDTO(workspace), nil
}
func (s *Service) ApprovePipelineDesign(in ApprovePipelineDesignInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	if !validPipelineDesignRef(in.Ref) || len(in.Digests) != 3 {
		return PipelineDTO{}, ErrInvalidInput
	}
	digests := map[sdd.Stage]string{}
	for stage, digest := range in.Digests {
		if !designStage(stage) || len(digest) != 64 {
			return PipelineDTO{}, ErrInvalidInput
		}
		digests[sdd.Stage(stage)] = digest
	}
	run, err := s.store.ApprovePipelineDesign(s.ctx, catalog.PipelineDesignApproveRequest{Ref: in.Ref.request(), Digests: digests})
	if err != nil {
		return PipelineDTO{}, safe("approve document snapshot", err)
	}
	return s.pipelineDTOWithCodeApplyStatus(run)
}

func (s *Service) CancelPipelineDesign(in CancelPipelineDesignInput) (PipelineDesignDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDesignDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(in.PipelineID, 128) || !validSelectionText(in.AttemptID, 128) {
		return PipelineDesignDTO{}, ErrInvalidInput
	}
	s.authoringAdmissionGate.Lock()
	s.mu.RLock()
	owner := s.designOwners[in.AttemptID]
	s.mu.RUnlock()
	if owner != nil && owner.pipelineID == in.PipelineID {
		owner.cancel()
	}
	workspace, requestErr := s.store.RequestPipelineDesignCancellation(s.ctx, in.PipelineID, in.AttemptID)
	if requestErr == nil && workspace.CurrentPipelineRevision != workspace.PipelineRevision {
		requestErr = sqlite.ErrPipelineConflict
	}
	s.authoringAdmissionGate.Unlock()
	if owner != nil && owner.pipelineID == in.PipelineID {
		select {
		case <-owner.done:
		case <-time.After(2 * time.Second):
			if requestErr != nil {
				return PipelineDesignDTO{}, safe("cancel document preparation", requestErr)
			}
			return pipelineDesignDTO(workspace), nil
		case <-s.ctx.Done():
			if requestErr != nil {
				return PipelineDesignDTO{}, safe("cancel document preparation", requestErr)
			}
			return pipelineDesignDTO(workspace), nil
		}
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		defer cancel()
		if settled, readErr := s.store.GetPipelineDesign(readCtx, in.PipelineID); readErr == nil {
			workspace = settled
		}
	}
	if requestErr != nil {
		return pipelineDesignDTO(workspace), safe("cancel document preparation", requestErr)
	}
	return pipelineDesignDTO(workspace), nil
}
