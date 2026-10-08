package catalog

import (
	"time"

	"github.com/persioflexa/harflex/internal/sdd"
)

type Workspace struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Profile   string    `json:"profile"`
	CreatedAt time.Time `json:"createdAt"`
	// Archived hides the project from the Projects list; opening its folder again brings it back.
	Archived bool `json:"archived"`
}

type AppSettings struct {
	DefaultBackendID      string `json:"defaultBackendId"`
	DefaultModelBackendID string `json:"defaultModelBackendId"`
	DefaultModelID        string `json:"defaultModelId"`
}

type LogEntry struct {
	Cursor      int64     `json:"cursor"`
	ID          string    `json:"id"`
	SessionID   string    `json:"sessionId"`
	WorkspaceID string    `json:"workspaceId"`
	Sequence    int64     `json:"sequence"`
	Type        string    `json:"type"`
	CreatedAt   time.Time `json:"createdAt"`
}

type PipelineArtifact struct {
	Stage           sdd.Stage `json:"stage"`
	Version         int64     `json:"version"`
	Content         string    `json:"content"`
	Author          string    `json:"author"`
	SourceSessionID string    `json:"sourceSessionId"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type PipelineArchivedArtifact struct {
	Stage           sdd.Stage `json:"stage"`
	Version         int64     `json:"version"`
	Content         string    `json:"content"`
	Author          string    `json:"author"`
	SourceSessionID string    `json:"sourceSessionId"`
	ContentDigest   string    `json:"contentDigest"`
	Reason          string    `json:"reason"`
	CreatedAt       time.Time `json:"createdAt"`
}

type PipelineRun struct {
	ID                     string                         `json:"id"`
	WorkspaceID            string                         `json:"workspaceId"`
	Kind                   string                         `json:"kind"`
	DerivedFromPipelineID  string                         `json:"derivedFromPipelineId"`
	CreationRequestID      string                         `json:"creationRequestId"`
	CreationRequestHash    string                         `json:"creationRequestHash"`
	DiscoveryFrozenVersion int64                          `json:"discoveryFrozenVersion"`
	Title                  string                         `json:"title"`
	Objective              string                         `json:"objective"`
	Current                sdd.Stage                      `json:"currentStage"`
	Status                 map[sdd.Stage]sdd.Status       `json:"stageStatus"`
	Revision               int64                          `json:"revision"`
	Artifacts              map[sdd.Stage]PipelineArtifact `json:"artifacts"`
	ArchivedArtifacts      []PipelineArchivedArtifact     `json:"archivedArtifacts,omitempty"`
	ExecutionReviews       []PipelineExecutionReview      `json:"executionReviews,omitempty"`
	CreatedAt              time.Time                      `json:"createdAt"`
	UpdatedAt              time.Time                      `json:"updatedAt"`
}

// CredentialReference records publication, never the credential value.
type CredentialReference struct {
	ProfileID string    `json:"profileId"`
	Provider  string    `json:"provider"`
	Account   string    `json:"account"`
	CreatedAt time.Time `json:"createdAt"`
}

type ProviderProfile struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Kind               string    `json:"kind"`
	ProviderType       string    `json:"providerType"`
	BaseURL            string    `json:"baseUrl"`
	Model              string    `json:"model"`
	CredentialProvider string    `json:"credentialProvider"`
	CredentialAccount  string    `json:"credentialAccount"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type SessionRecord struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspaceId"`
	BackendID       string    `json:"backendId"`
	BackendRevision string    `json:"backendRevision"`
	Mode            string    `json:"mode"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// AuthoringCodeSessionMode marks the session that owns an AI-authored pipeline's Code run.
// It is deliberately not "sdd_code": pipeline executions already persist Coder sessions under
// that value with different tools and recovery rules, and the two must never share a code path.
const AuthoringCodeSessionMode = "sdd_authoring_code"

// ModelSelection is an immutable execution choice, not a provider credential.
// Empty ReasoningEffort means Automatic and must not become a CLI option.
type ModelSelection struct {
	SessionID                 string    `json:"sessionId"`
	BackendID                 string    `json:"backendId"`
	ModelID                   string    `json:"modelId"`
	ReasoningEffort           string    `json:"reasoningEffort"`
	SupportedReasoningEfforts []string  `json:"supportedReasoningEfforts,omitempty"`
	CatalogRevision           string    `json:"catalogRevision"`
	LocalRevision             string    `json:"localRevision"`
	Source                    string    `json:"source"`
	Destination               string    `json:"destination"`
	CredentialIdentity        string    `json:"-"`
	Status                    string    `json:"status"`
	ConfirmUnverifiedManual   bool      `json:"confirmUnverifiedManual"`
	ConfirmUnfiltered         bool      `json:"confirmUnfiltered"`
	ConfirmJITLoad            bool      `json:"confirmJitLoad"`
	MaxOutputTokens           int       `json:"maxOutputTokens"`
	MaxAssistantOutputBytes   int       `json:"maxAssistantOutputBytes,omitempty"`
	ContextLength             int       `json:"contextLength,omitempty"`
	ExecutablePath            string    `json:"executablePath"`
	ExecutableVersion         string    `json:"executableVersion"`
	WorkspacePath             string    `json:"workspacePath"`
	CheckedAt                 time.Time `json:"checkedAt"`
}
