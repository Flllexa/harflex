package application

import (
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

type PipelineDesignRefInput struct {
	PipelineID       string `json:"pipelineId"`
	RequestID        string `json:"requestId"`
	PipelineRevision int64  `json:"pipelineRevision"`
	DesignRevision   int64  `json:"designRevision"`
}

func (ref PipelineDesignRefInput) request() catalog.PipelineDesignRef {
	return catalog.PipelineDesignRef{PipelineID: ref.PipelineID, RequestID: ref.RequestID, PipelineRevision: ref.PipelineRevision, DesignRevision: ref.DesignRevision}
}

type PreparePipelineDesignInput struct {
	Ref        PipelineDesignRefInput            `json:"ref"`
	Message    string                            `json:"message"`
	Target     string                            `json:"target"`
	Selections map[string]APIModelSelectionInput `json:"selections,omitempty"`
}
type EditPipelineDesignDocumentInput struct {
	Ref     PipelineDesignRefInput `json:"ref"`
	Stage   string                 `json:"stage"`
	Content string                 `json:"content"`
}
type RestorePipelineDesignDocumentInput struct {
	Ref     PipelineDesignRefInput `json:"ref"`
	Stage   string                 `json:"stage"`
	Version int64                  `json:"version"`
}
type CancelPipelineDesignInput struct {
	PipelineID string `json:"pipelineId"`
	AttemptID  string `json:"attemptId"`
}
type ApprovePipelineDesignInput struct {
	Ref     PipelineDesignRefInput `json:"ref"`
	Digests map[string]string      `json:"digests"`
}
type PipelineDesignDocumentDTO struct {
	Stage           string                  `json:"stage"`
	Version         int64                   `json:"version"`
	Content         string                  `json:"content"`
	ContentDigest   string                  `json:"contentDigest"`
	Author          string                  `json:"author"`
	SourceSessionID string                  `json:"sourceSessionId"`
	SourceDigest    string                  `json:"sourceDigest"`
	Selection       *BrainstormSelectionDTO `json:"selection,omitempty"`
	Stale           bool                    `json:"stale"`
	UpdatedAt       time.Time               `json:"updatedAt"`
}
type PipelineDesignVersionDTO struct {
	PipelineDesignDocumentDTO
	Reason              string    `json:"reason"`
	RestoredFromVersion int64     `json:"restoredFromVersion"`
	CreatedAt           time.Time `json:"createdAt"`
}
type PipelineDesignAttemptDTO struct {
	ID         string                            `json:"id"`
	RequestID  string                            `json:"requestId"`
	Target     string                            `json:"target"`
	Status     string                            `json:"status"`
	Phase      string                            `json:"phase"`
	Selections map[string]BrainstormSelectionDTO `json:"selections"`
	SessionIDs map[string]string                 `json:"sessionIds"`
	ErrorCode  string                            `json:"errorCode"`
	CreatedAt  time.Time                         `json:"createdAt"`
	UpdatedAt  time.Time                         `json:"updatedAt"`
}
type PipelineDesignDTO struct {
	PipelineID              string                                `json:"pipelineId"`
	WorkspaceID             string                                `json:"workspaceId"`
	PipelineRevision        int64                                 `json:"pipelineRevision"`
	CurrentPipelineRevision int64                                 `json:"currentPipelineRevision"`
	Revision                int64                                 `json:"revision"`
	State                   string                                `json:"state"`
	Phase                   string                                `json:"phase"`
	ActiveAttemptID         string                                `json:"activeAttemptId"`
	NeedsDerivation         bool                                  `json:"needsDerivation"`
	Documents               map[string]PipelineDesignDocumentDTO  `json:"documents"`
	Versions                map[string][]PipelineDesignVersionDTO `json:"versions"`
	Messages                []catalog.PipelineDesignMessage       `json:"messages"`
	Attempts                []PipelineDesignAttemptDTO            `json:"attempts"`
	CreatedAt               time.Time                             `json:"createdAt"`
	UpdatedAt               time.Time                             `json:"updatedAt"`
}

func pipelineDesignDocumentDTO(document catalog.PipelineDesignDocument) PipelineDesignDocumentDTO {
	result := PipelineDesignDocumentDTO{Stage: string(document.Stage), Version: document.Version, Content: document.Content, ContentDigest: document.ContentDigest, Author: document.Author, SourceSessionID: document.SourceSessionID, SourceDigest: document.SourceDigest, Stale: document.Stale, UpdatedAt: document.UpdatedAt}
	if document.Selection.BackendID != "" {
		choice := brainstormSelectionDTO(document.Selection)
		result.Selection = &choice
	}
	return result
}
func pipelineDesignDTO(workspace catalog.PipelineDesignWorkspace) PipelineDesignDTO {
	result := PipelineDesignDTO{PipelineID: workspace.PipelineID, WorkspaceID: workspace.WorkspaceID, PipelineRevision: workspace.PipelineRevision, CurrentPipelineRevision: workspace.CurrentPipelineRevision, Revision: workspace.Revision, State: workspace.State, Phase: string(workspace.Phase), ActiveAttemptID: workspace.ActiveAttemptID, NeedsDerivation: workspace.NeedsDerivation, Documents: map[string]PipelineDesignDocumentDTO{}, Versions: map[string][]PipelineDesignVersionDTO{}, Messages: workspace.Messages, Attempts: []PipelineDesignAttemptDTO{}, CreatedAt: workspace.CreatedAt, UpdatedAt: workspace.UpdatedAt}
	for stage, document := range workspace.Documents {
		result.Documents[string(stage)] = pipelineDesignDocumentDTO(document)
	}
	for stage, versions := range workspace.Versions {
		items := []PipelineDesignVersionDTO{}
		for _, version := range versions {
			items = append(items, PipelineDesignVersionDTO{PipelineDesignDocumentDTO: pipelineDesignDocumentDTO(version.PipelineDesignDocument), Reason: version.Reason, RestoredFromVersion: version.RestoredFromVersion, CreatedAt: version.CreatedAt})
		}
		result.Versions[string(stage)] = items
	}
	for _, attempt := range workspace.Attempts {
		item := PipelineDesignAttemptDTO{ID: attempt.ID, RequestID: attempt.RequestID, Target: attempt.Target, Status: attempt.Status, Phase: string(attempt.Phase), ErrorCode: attempt.ErrorCode, Selections: map[string]BrainstormSelectionDTO{}, SessionIDs: map[string]string{}, CreatedAt: attempt.CreatedAt, UpdatedAt: attempt.UpdatedAt}
		for stage, selection := range attempt.Selections {
			item.Selections[string(stage)] = brainstormSelectionDTO(selection)
		}
		for stage, sessionID := range attempt.SessionIDs {
			item.SessionIDs[string(stage)] = sessionID
		}
		result.Attempts = append(result.Attempts, item)
	}
	return result
}
func designStages() []sdd.Stage { return []sdd.Stage{sdd.Discovery, sdd.Spec, sdd.Plan} }
