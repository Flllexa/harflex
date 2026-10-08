package application

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

type AuthoringStageRefInput struct {
	PipelineID       string `json:"pipelineId"`
	Stage            string `json:"stage"`
	RequestID        string `json:"requestId"`
	PipelineRevision int64  `json:"pipelineRevision"`
	StageRevision    int64  `json:"stageRevision"`
	DiscoveryVersion int64  `json:"discoveryVersion"`
	ArtifactVersion  int64  `json:"artifactVersion"`
}

func (r AuthoringStageRefInput) request() catalog.AuthoringStageRequest {
	return catalog.AuthoringStageRequest{PipelineID: r.PipelineID, Stage: sdd.Stage(r.Stage), RequestID: r.RequestID, PipelineRevision: r.PipelineRevision, StageRevision: r.StageRevision, DiscoveryVersion: r.DiscoveryVersion, ArtifactVersion: r.ArtifactVersion}
}

type GetAuthoringStageInput struct {
	PipelineID string `json:"pipelineId"`
	Stage      string `json:"stage"`
}
type AuthoringModelConsentBindingInput struct {
	BackendID       string `json:"backendId"`
	ModelID         string `json:"modelId"`
	CatalogRevision string `json:"catalogRevision"`
	Source          string `json:"source"`
	Destination     string `json:"destination"`
}
type GenerateAuthoringStageInput struct {
	Ref               AuthoringStageRefInput             `json:"ref"`
	Selection         APIModelSelectionInput             `json:"selection"`
	Feedback          string                             `json:"feedback"`
	ConfirmUnfiltered bool                               `json:"confirmUnfiltered"`
	ConfirmJITLoad    bool                               `json:"confirmJitLoad"`
	ConsentBinding    *AuthoringModelConsentBindingInput `json:"consentBinding,omitempty"`
}
type AuthoringStageDecisionInput struct {
	Ref       AuthoringStageRefInput `json:"ref"`
	Reason    string                 `json:"reason"`
	AttemptID string                 `json:"attemptId"`
}

type AuthoringStageSourceDTO struct {
	DiscoveryVersion        int64  `json:"discoveryVersion"`
	DiscoveryHash           string `json:"discoveryHash"`
	BrainstormRunID         string `json:"brainstormRunId"`
	SynthesisVersion        int    `json:"synthesisVersion"`
	DiscoveryBypassReason   string `json:"discoveryBypassReason"`
	SpecVersion             int64  `json:"specVersion"`
	SpecHash                string `json:"specHash"`
	SpecBypassReason        string `json:"specBypassReason"`
	PreviousArtifactVersion int64  `json:"previousArtifactVersion"`
	Feedback                string `json:"feedback"`
}
type AuthoringStageAttemptDTO struct {
	ID                   string                   `json:"id"`
	RequestID            string                   `json:"requestId"`
	ArtifactVersion      int64                    `json:"artifactVersion"`
	Status               string                   `json:"status"`
	SessionID            string                   `json:"sessionId"`
	ErrorCode            string                   `json:"errorCode"`
	CancellationState    string                   `json:"cancellationState"`
	ModelMode            string                   `json:"modelMode"`
	EffortMode           string                   `json:"effortMode"`
	PreferenceSource     string                   `json:"preferenceSource"`
	PreferenceRevision   int64                    `json:"preferenceRevision"`
	Source               AuthoringStageSourceDTO  `json:"source"`
	Selection            BrainstormSelectionDTO   `json:"selection"`
	ReservedInputTokens  int64                    `json:"reservedInputTokens"`
	ReservedOutputTokens int64                    `json:"reservedOutputTokens"`
	Usage                *catalog.BrainstormUsage `json:"usage"`
	CreatedAt            time.Time                `json:"createdAt"`
	UpdatedAt            time.Time                `json:"updatedAt"`
}
type AuthoringStageArtifactDTO struct {
	Version         int64           `json:"version"`
	Content         json.RawMessage `json:"content"`
	ContentHash     string          `json:"contentHash"`
	Author          string          `json:"author"`
	AttemptID       string          `json:"attemptId"`
	SourceSessionID string          `json:"sourceSessionId"`
	Status          string          `json:"status"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}
type AuthoringStageActionDTO struct {
	RequestID              string    `json:"requestId"`
	Action                 string    `json:"action"`
	Actor                  string    `json:"actor"`
	ArtifactVersion        int64     `json:"artifactVersion"`
	Feedback               string    `json:"feedback"`
	Reason                 string    `json:"reason"`
	AttemptID              string    `json:"attemptId"`
	ResultStageRevision    int64     `json:"resultStageRevision"`
	ResultPipelineRevision int64     `json:"resultPipelineRevision"`
	CreatedAt              time.Time `json:"createdAt"`
}
type AuthoringStageDTO struct {
	PipelineID            string                      `json:"pipelineId"`
	Stage                 string                      `json:"stage"`
	PipelineRevision      int64                       `json:"pipelineRevision"`
	DiscoveryVersion      int64                       `json:"discoveryVersion"`
	Revision              int64                       `json:"revision"`
	State                 string                      `json:"state"`
	CancellationPending   bool                        `json:"cancellationPending"`
	CancellationAttemptID string                      `json:"cancellationAttemptId"`
	ArtifactVersion       int64                       `json:"artifactVersion"`
	AttemptCount          int                         `json:"attemptCount"`
	InputBudgetRemaining  int64                       `json:"inputBudgetRemaining"`
	OutputBudgetRemaining int64                       `json:"outputBudgetRemaining"`
	Attempts              []AuthoringStageAttemptDTO  `json:"attempts"`
	Artifacts             []AuthoringStageArtifactDTO `json:"artifacts"`
	Actions               []AuthoringStageActionDTO   `json:"actions"`
	CreatedAt             time.Time                   `json:"createdAt"`
	UpdatedAt             time.Time                   `json:"updatedAt"`
}

func authoringStageDTO(r catalog.AuthoringStageRun) AuthoringStageDTO {
	out := AuthoringStageDTO{PipelineID: r.PipelineID, Stage: string(r.Stage), PipelineRevision: r.PipelineRevision, DiscoveryVersion: r.DiscoveryVersion, Revision: r.Revision, State: r.State, ArtifactVersion: r.ArtifactVersion, AttemptCount: r.AttemptCount, InputBudgetRemaining: r.InputBudgetRemaining, OutputBudgetRemaining: r.OutputBudgetRemaining, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Attempts: make([]AuthoringStageAttemptDTO, 0, len(r.Attempts)), Artifacts: make([]AuthoringStageArtifactDTO, 0, len(r.Artifacts)), Actions: make([]AuthoringStageActionDTO, 0, len(r.Actions))}
	out.CancellationPending, out.CancellationAttemptID = r.CancellationPending, r.CancellationAttemptID
	for _, a := range r.Attempts {
		source := AuthoringStageSourceDTO{DiscoveryVersion: a.Input.DiscoveryVersion, DiscoveryHash: a.Input.DiscoveryHash, BrainstormRunID: a.Input.BrainstormRunID, SynthesisVersion: a.Input.SynthesisVersion, DiscoveryBypassReason: a.Input.DiscoveryBypassReason, SpecVersion: a.Input.SpecVersion, SpecHash: a.Input.SpecHash, SpecBypassReason: a.Input.SpecBypassReason, PreviousArtifactVersion: a.Input.PreviousArtifactVersion, Feedback: a.Input.Feedback}
		out.Attempts = append(out.Attempts, AuthoringStageAttemptDTO{ID: a.ID, RequestID: a.RequestID, ArtifactVersion: a.ArtifactVersion, Status: a.Status, SessionID: a.SessionID, ErrorCode: a.ErrorCode, ModelMode: a.ModelMode, EffortMode: a.EffortMode, PreferenceSource: a.PreferenceSource, PreferenceRevision: a.PreferenceRevision, Source: source, Selection: brainstormSelectionDTO(a.Selection), ReservedInputTokens: a.ReservedInputTokens, ReservedOutputTokens: a.ReservedOutputTokens, Usage: a.Usage, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt})
		out.Attempts[len(out.Attempts)-1].CancellationState = a.CancellationState
	}
	for _, a := range r.Artifacts {
		out.Artifacts = append(out.Artifacts, AuthoringStageArtifactDTO{Version: a.Version, Content: append(json.RawMessage(nil), a.Content...), ContentHash: a.ContentHash, Author: a.Author, AttemptID: a.AttemptID, SourceSessionID: a.SourceSessionID, Status: a.Status, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt})
	}
	for _, a := range r.Actions {
		out.Actions = append(out.Actions, AuthoringStageActionDTO{RequestID: a.RequestID, Action: a.Action, Actor: a.Actor, ArtifactVersion: a.ArtifactVersion, Feedback: a.Feedback, Reason: a.Reason, AttemptID: a.AttemptID, ResultStageRevision: a.ResultStageRevision, ResultPipelineRevision: a.ResultPipelineRevision, CreatedAt: a.CreatedAt})
	}
	return out
}

func validAuthoringStageName(stage string) bool { return stage == "spec" || stage == "plan" }
func validAuthoringStageRef(r AuthoringStageRefInput) bool {
	return validSelectionText(r.PipelineID, 128) && validAuthoringStageName(r.Stage) && authoringRequestID.MatchString(r.RequestID) && r.PipelineRevision > 0 && r.StageRevision >= 0 && r.DiscoveryVersion > 0 && r.ArtifactVersion >= 0 && r.ArtifactVersion <= sdd.MaxAuthoringDrafts
}
func validAuthoringFeedback(text string) bool {
	return strings.TrimSpace(text) != "" && len(text) <= 16*1024 && utf8.ValidString(text) && !strings.ContainsRune(text, 0)
}

func (s *Service) GetAuthoringStage(in GetAuthoringStageInput) (AuthoringStageDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringStageDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(in.PipelineID, 128) || !validAuthoringStageName(in.Stage) {
		return AuthoringStageDTO{}, ErrInvalidInput
	}
	run, err := s.store.GetAuthoringStage(s.ctx, in.PipelineID, sdd.Stage(in.Stage))
	if err != nil {
		return AuthoringStageDTO{}, safe("read authoring stage", err)
	}
	return authoringStageDTO(run), nil
}
