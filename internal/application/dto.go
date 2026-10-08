package application

import (
	"encoding/json"
	"github.com/persioflexa/harflex/internal/agentcore"
	"time"
)

type WorkspaceDTO struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Profile string `json:"profile"`
}
type WorkspaceSummaryDTO struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Profile   string `json:"profile"`
	Available bool   `json:"available"`
	Archived  bool   `json:"archived"`
}
type SetWorkspaceArchivedInput struct {
	WorkspaceID string `json:"workspaceId"`
	Archived    bool   `json:"archived"`
}
type SaveLocalChannelInput struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Folder      string `json:"folder"`
}
type LocalChannelDTO struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	Name        string    `json:"name"`
	Folder      string    `json:"folder"`
	Status      string    `json:"status"`
	LastError   string    `json:"lastError"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
type ChannelMessageDTO struct {
	ID        string    `json:"id"`
	ChannelID string    `json:"channelId"`
	Direction string    `json:"direction"`
	FileName  string    `json:"fileName"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	ErrorCode string    `json:"errorCode"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type ChannelImportDTO struct {
	Channel  LocalChannelDTO `json:"channel"`
	Imported int             `json:"imported"`
	Failed   int             `json:"failed"`
}
type SendChannelMessageInput struct {
	ChannelID string `json:"channelId"`
	RequestID string `json:"requestId"`
	Content   string `json:"content"`
}
type RetryChannelMessageInput struct {
	ChannelID string `json:"channelId"`
	RequestID string `json:"requestId"`
}
type SetWorkspaceProfileInput struct {
	WorkspaceID string `json:"workspaceId"`
	Profile     string `json:"profile"`
	// ConfirmFullAccess must be true to save the full_access profile.
	ConfirmFullAccess bool `json:"confirmFullAccess"`
}
type ProviderProfileDTO struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	ProviderType    string    `json:"providerType"`
	BaseURL         string    `json:"baseUrl"`
	Model           string    `json:"model"`
	HasCredential   bool      `json:"hasCredential"`
	EndpointBlocked bool      `json:"endpointBlocked"`
	UpdatedAt       time.Time `json:"updatedAt"`
}
type SettingsDTO struct {
	DefaultBackendID      string `json:"defaultBackendId"`
	DefaultModelBackendID string `json:"defaultModelBackendId"`
	DefaultModelID        string `json:"defaultModelId"`
}
type SaveSettingsInput struct {
	DefaultBackendID      string `json:"defaultBackendId"`
	DefaultModelBackendID string `json:"defaultModelBackendId"`
	DefaultModelID        string `json:"defaultModelId"`
}
type ListLogEventsInput struct {
	WorkspaceID string `json:"workspaceId"`
	Type        string `json:"type"`
	BeforeID    int64  `json:"beforeId"`
	Limit       int    `json:"limit"`
}
type LogEntryDTO struct {
	Cursor      int64     `json:"cursor"`
	ID          string    `json:"id"`
	SessionID   string    `json:"sessionId"`
	WorkspaceID string    `json:"workspaceId"`
	Sequence    int64     `json:"sequence"`
	Type        string    `json:"type"`
	CreatedAt   time.Time `json:"createdAt"`
}
type CredentialProbeDTO struct {
	Status  string `json:"status"`
	Checked int    `json:"checked"`
	Missing int    `json:"missing"`
}
type RepositoryDTO struct {
	IsRepository bool     `json:"isRepository"`
	Root         string   `json:"root"`
	Branch       string   `json:"branch"`
	Files        []string `json:"files"`
	StagedDiff   string   `json:"stagedDiff"`
	UnstagedDiff string   `json:"unstagedDiff"`
	Truncated    bool     `json:"truncated"`
}
type CreatePipelineInput struct {
	WorkspaceID string `json:"workspaceId"`
	Title       string `json:"title"`
	Objective   string `json:"objective"`
}
type CreateAuthoringPipelineInput struct {
	WorkspaceID string `json:"workspaceId"`
	RequestID   string `json:"requestId"`
	Discovery   string `json:"discovery"`
}
type DeriveAuthoringPipelineInput struct {
	ParentPipelineID string `json:"parentPipelineId"`
	RequestID        string `json:"requestId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Discovery        string `json:"discovery"`
}
type ReviseAuthoringDiscoveryInput struct {
	PipelineID       string `json:"pipelineId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	ExpectedVersion  int64  `json:"expectedVersion"`
	Discovery        string `json:"discovery"`
}
type SavePipelineArtifactInput struct {
	PipelineID string `json:"pipelineId"`
	Stage      string `json:"stage"`
	Content    string `json:"content"`
}
type DecidePipelineExecutionArtifactInput struct {
	PipelineID       string `json:"pipelineId"`
	RequestID        string `json:"requestId"`
	Stage            string `json:"stage"`
	ArtifactVersion  int64  `json:"artifactVersion"`
	ArtifactDigest   string `json:"artifactDigest"`
	Decision         string `json:"decision"`
	Feedback         string `json:"feedback"`
	PipelineRevision int64  `json:"pipelineRevision"`
}
type ReopenPipelineCodeReviewInput struct {
	PipelineID       string `json:"pipelineId"`
	ArtifactVersion  int64  `json:"artifactVersion"`
	ArtifactDigest   string `json:"artifactDigest"`
	PipelineRevision int64  `json:"pipelineRevision"`
}
type SkipPipelineStageInput struct {
	PipelineID string `json:"pipelineId"`
	Reason     string `json:"reason"`
}
type PipelineArtifactDTO struct {
	Stage           string    `json:"stage"`
	Version         int64     `json:"version"`
	Content         string    `json:"content"`
	Author          string    `json:"author"`
	SourceSessionID string    `json:"sourceSessionId"`
	ContentDigest   string    `json:"contentDigest,omitempty"`
	ReviewStatus    string    `json:"reviewStatus,omitempty"`
	ReviewActor     string    `json:"reviewActor,omitempty"`
	ReviewFeedback  string    `json:"reviewFeedback,omitempty"`
	ReviewedAt      time.Time `json:"reviewedAt,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt"`
}
type PipelineArchivedArtifactDTO struct {
	Stage           string    `json:"stage"`
	Version         int64     `json:"version"`
	Content         string    `json:"content"`
	Author          string    `json:"author"`
	SourceSessionID string    `json:"sourceSessionId"`
	ContentDigest   string    `json:"contentDigest"`
	Reason          string    `json:"reason"`
	CreatedAt       time.Time `json:"createdAt"`
}
type PipelineExecutionReviewDTO struct {
	Stage           string    `json:"stage"`
	Version         int64     `json:"version"`
	Content         string    `json:"content"`
	ContentDigest   string    `json:"contentDigest"`
	SourceSessionID string    `json:"sourceSessionId"`
	Decision        string    `json:"decision"`
	Actor           string    `json:"actor"`
	Feedback        string    `json:"feedback"`
	CreatedAt       time.Time `json:"createdAt"`
}
type PipelineDTO struct {
	ID                       string                         `json:"id"`
	WorkspaceID              string                         `json:"workspaceId"`
	Kind                     string                         `json:"kind"`
	PreparationExperience    string                         `json:"preparationExperience,omitempty"`
	DerivedFromPipelineID    string                         `json:"derivedFromPipelineId"`
	DiscoveryFrozenVersion   int64                          `json:"discoveryFrozenVersion"`
	Title                    string                         `json:"title"`
	Objective                string                         `json:"objective"`
	CurrentStage             string                         `json:"currentStage"`
	StageStatus              map[string]string              `json:"stageStatus"`
	Revision                 int64                          `json:"revision"`
	Artifacts                map[string]PipelineArtifactDTO `json:"artifacts"`
	ArchivedArtifacts        []PipelineArchivedArtifactDTO  `json:"archivedArtifacts,omitempty"`
	ExecutionReviews         []PipelineExecutionReviewDTO   `json:"executionReviews,omitempty"`
	CodeAppliedAt            *time.Time                     `json:"codeAppliedAt,omitempty"`
	CodePatchPending         bool                           `json:"codePatchPending,omitempty"` // an isolated Code run's approved patch has not reached the project folder yet
	CodeReviewRecoveryStatus string                         `json:"codeReviewRecoveryStatus,omitempty"`
	CodeReviewRecoveryReason string                         `json:"codeReviewRecoveryReason,omitempty"`
	CreatedAt                time.Time                      `json:"createdAt"`
	UpdatedAt                time.Time                      `json:"updatedAt"`
}
type CreatePipelineSessionInput struct {
	PipelineID           string                  `json:"pipelineId"`
	BackendID            string                  `json:"backendId"`
	Role                 string                  `json:"role"`
	Selection            *APIModelSelectionInput `json:"selection,omitempty"`
	ConfirmWorkspaceCopy bool                    `json:"confirmWorkspaceCopy,omitempty"`
	// ContinuePreviousCopy keeps a Coder fix round in the previous Coder's private copy.
	ContinuePreviousCopy bool `json:"continuePreviousCopy,omitempty"`
}
type GetPipelineForSessionInput struct {
	SessionID   string `json:"sessionId"`
	WorkspaceID string `json:"workspaceId"`
}
type PipelineSessionDTO struct {
	Session SessionDTO `json:"session"`
	Prompt  string     `json:"prompt"`
	Role    string     `json:"role"`
}
type PipelineCodeCopyPreviewDTO struct {
	IsGit         bool     `json:"isGit"`
	FileCount     int      `json:"fileCount"`
	TotalBytes    int64    `json:"totalBytes"`
	ExcludedPaths []string `json:"excludedPaths"`
	UnsafePaths   []string `json:"unsafePaths"`
}
type BackendDTO struct {
	ID                    string                 `json:"id"`
	Name                  string                 `json:"name"`
	Kind                  string                 `json:"kind"`
	Available             bool                   `json:"available"`
	ProfessionalAvailable bool                   `json:"professionalAvailable,omitempty"`
	Capabilities          agentcore.Capabilities `json:"capabilities"`
}
type SaveProviderProfileInput struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	ProviderType    string `json:"providerType"`
	BaseURL         string `json:"baseUrl"`
	Model           string `json:"model"`
	APIKey          string `json:"apiKey"`
	ClearCredential bool   `json:"clearCredential"`
}
type SessionDTO struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	BackendID   string    `json:"backendId"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Resumable   bool      `json:"resumable"`
	// Purpose separates user conversations ("chat") from the sessions the SDD creates for
	// itself ("preparation", "code", "evaluation"). Title previews a chat's first user message
	// (filled only by ListSessions, for the most recent chats) and names internal sessions.
	Purpose string `json:"purpose,omitempty"`
	Title   string `json:"title,omitempty"`
	// Pinned keeps the conversation at the top of the Casual sidebar (filled by ListChatProjects).
	Pinned bool `json:"pinned,omitempty"`
}
type ListSessionsInput struct {
	WorkspaceID string `json:"workspaceId"`
}
type OpenSessionInput struct {
	SessionID   string `json:"sessionId"`
	WorkspaceID string `json:"workspaceId"`
}
type CreateSessionInput struct {
	WorkspaceID     string `json:"workspaceId"`
	BackendID       string `json:"backendId"`
	AgentID         string `json:"agentId"`
	Mode            string `json:"mode"`
	BypassReason    string `json:"bypassReason"`
	ModelID         string `json:"modelId"`
	ReasoningEffort string `json:"reasoningEffort"`
	CatalogRevision string `json:"catalogRevision"`
}
type CreateDirectSessionInput struct {
	WorkspaceID     string `json:"workspaceId"`
	BackendID       string `json:"backendId"`
	AgentID         string `json:"agentId"`
	Reason          string `json:"reason"`
	ModelID         string `json:"modelId"`
	ReasoningEffort string `json:"reasoningEffort"`
	CatalogRevision string `json:"catalogRevision"`
}
type SaveAgentInput struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Instructions string   `json:"instructions"`
	BackendID    string   `json:"backendId"`
	AllowedTools []string `json:"allowedTools"`
}
type AgentDTO struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Instructions string    `json:"instructions"`
	BackendID    string    `json:"backendId"`
	AllowedTools []string  `json:"allowedTools"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
type DelegateToAgentInput struct {
	ParentSessionID string `json:"parentSessionId"`
	AgentID         string `json:"agentId"`
	RequestID       string `json:"requestId"`
	Prompt          string `json:"prompt"`
}
type DelegationDTO struct {
	ID              string    `json:"id"`
	ParentSessionID string    `json:"parentSessionId"`
	ChildSessionID  string    `json:"childSessionId"`
	AgentID         string    `json:"agentId"`
	TaskPrompt      string    `json:"taskPrompt"`
	PromptCount     int       `json:"promptCount"`
	PromptLimit     int       `json:"promptLimit"`
	TimeoutSeconds  int       `json:"timeoutSeconds"`
	Depth           int       `json:"depth"`
	CreatedAt       time.Time `json:"createdAt"`
	Status          string    `json:"status"`
	Result          string    `json:"result"`
	ErrorCode       string    `json:"errorCode"`
}
type DelegatedSessionDTO struct {
	Session SessionDTO `json:"session"`
	Prompt  string     `json:"prompt"`
	Depth   int        `json:"depth"`
	Created bool       `json:"created"`
}
type WorkflowStepInput struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}
type SaveWorkflowInput struct {
	ID          string              `json:"id"`
	WorkspaceID string              `json:"workspaceId"`
	Name        string              `json:"name"`
	Steps       []WorkflowStepInput `json:"steps"`
}
type StartWorkflowInput struct {
	WorkflowID string `json:"workflowId"`
	BackendID  string `json:"backendId"`
}
type ResumeWorkflowRunInput struct {
	RunID             string `json:"runId"`
	ReviewedSessionID string `json:"reviewedSessionId"`
	Choice            string `json:"choice"`
}
type WorkflowDTO struct {
	ID          string              `json:"id"`
	WorkspaceID string              `json:"workspaceId"`
	Name        string              `json:"name"`
	Steps       []WorkflowStepInput `json:"steps"`
	Revision    int64               `json:"revision"`
	CreatedAt   time.Time           `json:"createdAt"`
	UpdatedAt   time.Time           `json:"updatedAt"`
}
type WorkflowRunDTO struct {
	ID            string              `json:"id"`
	WorkflowID    string              `json:"workflowId"`
	WorkspaceID   string              `json:"workspaceId"`
	BackendID     string              `json:"backendId"`
	Steps         []WorkflowStepInput `json:"steps"`
	CurrentStep   int                 `json:"currentStep"`
	Status        string              `json:"status"`
	LastSessionID string              `json:"lastSessionId"`
	CreatedAt     time.Time           `json:"createdAt"`
	UpdatedAt     time.Time           `json:"updatedAt"`
}
type SaveScheduleInput struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspaceId"`
	Name         string `json:"name"`
	TargetKind   string `json:"targetKind"`
	WorkflowID   string `json:"workflowId"`
	BackendID    string `json:"backendId"`
	Prompt       string `json:"prompt"`
	Frequency    string `json:"frequency"`
	Timezone     string `json:"timezone"`
	LocalDate    string `json:"localDate"`
	LocalTime    string `json:"localTime"`
	MissedPolicy string `json:"missedPolicy"`
	Enabled      bool   `json:"enabled"`
	AllowCLI     bool   `json:"allowCli"`
	Revision     int64  `json:"revision"`
}
type ScheduleDTO struct {
	ID           string     `json:"id"`
	WorkspaceID  string     `json:"workspaceId"`
	Name         string     `json:"name"`
	TargetKind   string     `json:"targetKind"`
	WorkflowID   string     `json:"workflowId"`
	BackendID    string     `json:"backendId"`
	Prompt       string     `json:"prompt"`
	Frequency    string     `json:"frequency"`
	Timezone     string     `json:"timezone"`
	LocalDate    string     `json:"localDate"`
	LocalTime    string     `json:"localTime"`
	MissedPolicy string     `json:"missedPolicy"`
	Enabled      bool       `json:"enabled"`
	AllowCLI     bool       `json:"allowCli"`
	NextRunAt    *time.Time `json:"nextRunAt"`
	Revision     int64      `json:"revision"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}
type SetSchedulePausedInput struct {
	ScheduleID string `json:"scheduleId"`
	Paused     bool   `json:"paused"`
	Revision   int64  `json:"revision"`
}
type ScheduleJobDTO struct {
	ID            string     `json:"id"`
	ScheduleID    string     `json:"scheduleId"`
	WorkspaceID   string     `json:"workspaceId"`
	Trigger       string     `json:"trigger"`
	Status        string     `json:"status"`
	ErrorCode     string     `json:"errorCode"`
	WorkflowRunID string     `json:"workflowRunId"`
	SessionID     string     `json:"sessionId"`
	DueAt         time.Time  `json:"dueAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	StartedAt     *time.Time `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`
}
type ScheduleChangeDTO struct {
	WorkspaceID string `json:"workspaceId"`
}
type SaveSkillInput struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Enabled     bool   `json:"enabled"`
}
type ImportSkillInput struct {
	WorkspaceID string `json:"workspaceId"`
	Path        string `json:"path"`
	Enabled     bool   `json:"enabled"`
}
type SkillDTO struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	Enabled     bool      `json:"enabled"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
type ImportKnowledgeInput struct {
	WorkspaceID string `json:"workspaceId"`
	Path        string `json:"path"`
}
type KnowledgeDocumentInput struct {
	WorkspaceID string `json:"workspaceId"`
	DocumentID  string `json:"documentId"`
}
type SearchKnowledgeInput struct {
	WorkspaceID string `json:"workspaceId"`
	DocumentID  string `json:"documentId"`
	Query       string `json:"query"`
	Limit       int    `json:"limit"`
}
type KnowledgeDocumentDTO struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspaceId"`
	Path             string    `json:"path"`
	SourceSize       int64     `json:"sourceSize"`
	SourceModifiedAt time.Time `json:"sourceModifiedAt"`
	IndexedAt        time.Time `json:"indexedAt"`
	ChunkCount       int       `json:"chunkCount"`
}
type KnowledgeHitDTO struct {
	DocumentID       string    `json:"documentId"`
	Path             string    `json:"path"`
	LineStart        int       `json:"lineStart"`
	Snippet          string    `json:"snippet"`
	SourceModifiedAt time.Time `json:"sourceModifiedAt"`
	IndexedAt        time.Time `json:"indexedAt"`
}

type SaveKnowledgeEmbeddingProfileInput struct {
	WorkspaceID string `json:"workspaceId"`
	Kind        string `json:"kind"`
	BaseURL     string `json:"baseUrl"`
	Model       string `json:"model"`
}
type IndexKnowledgeVectorsInput struct {
	WorkspaceID         string `json:"workspaceId"`
	ExpectedFingerprint string `json:"expectedFingerprint"`
}
type KnowledgeEmbeddingProfileDTO struct {
	Configured  bool   `json:"configured"`
	Kind        string `json:"kind"`
	BaseURL     string `json:"baseUrl"`
	Model       string `json:"model"`
	Fingerprint string `json:"fingerprint"`
	Progress    struct {
		Total     int `json:"total"`
		Indexed   int `json:"indexed"`
		Dimension int `json:"dimension"`
	} `json:"progress"`
}
type KnowledgeSearchDTO struct {
	Mode string            `json:"mode"`
	Hits []KnowledgeHitDTO `json:"hits"`
}
type SaveMCPServerInput struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspaceId"`
	Name        string   `json:"name"`
	Transport   string   `json:"transport"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	URL         string   `json:"url"`
	TokenEnvVar string   `json:"tokenEnvVar"`
	// AuthScheme is "bearer" (also the meaning of "") or "basic", where Token is "user:secret".
	AuthScheme string `json:"authScheme"`
	Token      string `json:"token"`
}
type MCPToolDTO struct {
	Name        string          `json:"name"`
	AgentName   string          `json:"agentName"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}
type MCPServerDTO struct {
	ID            string       `json:"id"`
	WorkspaceID   string       `json:"workspaceId"`
	Name          string       `json:"name"`
	Transport     string       `json:"transport"`
	Command       string       `json:"command"`
	Args          []string     `json:"args"`
	URL           string       `json:"url"`
	TokenEnvVar   string       `json:"tokenEnvVar"`
	AuthScheme    string       `json:"authScheme"`
	Enabled       bool         `json:"enabled"`
	Tools         []MCPToolDTO `json:"tools"`
	HasCredential bool         `json:"hasCredential"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}
type PromptInput struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}
type ApprovalInput struct {
	SessionID  string `json:"sessionId"`
	ApprovalID string `json:"approvalId"`
	Allow      bool   `json:"allow"`
}
type ApprovalDTO struct {
	ID         string          `json:"approvalId"`
	ToolCallID string          `json:"toolCallId"`
	Name       string          `json:"name"`
	Risk       string          `json:"risk"`
	Arguments  json.RawMessage `json:"arguments"`
}
type RunResultDTO struct {
	Status   string       `json:"status"`
	Reason   string       `json:"reason,omitempty"`
	Code     string       `json:"code,omitempty"`
	Approval *ApprovalDTO `json:"approval,omitempty"`
}
type ListEventsInput struct {
	SessionID     string `json:"sessionId"`
	AfterSequence int64  `json:"afterSequence"`
	Limit         int    `json:"limit"`
}
type ExportAuditInput struct {
	SessionID   string `json:"sessionId"`
	Destination string `json:"destination"`
}
type EventDTO struct {
	ID        string          `json:"id"`
	StreamID  string          `json:"streamId"`
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"createdAt"`
}
