package application

import (
	"context"
	"encoding/json"
	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/embeddings"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/knowledge"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/secrets"
	"time"
)

type Store interface {
	agentcore.Journal
	ListAfterLimit(context.Context, string, int64, int) ([]events.Event, error)
	WalkAfter(context.Context, string, int64, int, int, func(events.Event) error) error
	StreamDataBytes(context.Context, string) (int64, error)
	RepairInterruptedRun(context.Context, string, events.RecoveryRequest) ([]events.Event, error)
	GetWorkspace(context.Context, string) (catalog.Workspace, error)
	ListWorkspaces(context.Context) ([]catalog.Workspace, error)
	SetWorkspaceProfile(context.Context, string, string) error
	SetWorkspaceArchived(context.Context, string, bool) error
	GetSettings(context.Context) (catalog.AppSettings, error)
	SaveSettings(context.Context, catalog.AppSettings) error
	ListLogEvents(context.Context, string, string, int64, int) ([]catalog.LogEntry, error)
	CreatePipeline(context.Context, catalog.PipelineRun) error
	CreateAuthoringPipeline(context.Context, catalog.PipelineRun, string) error
	DeriveAuthoringPipeline(context.Context, string, int64, catalog.PipelineRun, string) error
	GetAuthoringPipelineByRequest(context.Context, string, string, string) (catalog.PipelineRun, error)
	ReviseAuthoringDiscovery(context.Context, string, int64, int64, string, string, string) error
	FreezeDiscovery(context.Context, string, int64, int64) error
	StartBrainstorming(context.Context, catalog.StartBrainstormingRequest) (catalog.BrainstormRun, error)
	GetBrainstorming(context.Context, string) (catalog.BrainstormRun, error)
	GetBrainstormingByStartRequest(context.Context, string, string) (catalog.BrainstormRun, error)
	GetBrainstormingByPipeline(context.Context, string, int64) (catalog.BrainstormRun, error)
	ListBrainstormingByPipeline(context.Context, string, int) ([]catalog.BrainstormRun, error)
	GetBrainstormCommand(context.Context, string, string) (catalog.BrainstormCommandReceipt, error)
	BeginBrainstormQuestion(context.Context, catalog.BeginBrainstormAttemptRequest) (catalog.BrainstormAttempt, bool, error)
	QuestionsSufficient(context.Context, catalog.GenerateBrainstormSynthesisRequest) (catalog.BrainstormAttempt, bool, error)
	FinishAndGenerateSynthesis(context.Context, catalog.GenerateBrainstormSynthesisRequest) (catalog.BrainstormAttempt, bool, error)
	CreateBrainstormSession(context.Context, catalog.BrainstormRequest, catalog.BrainstormAttempt, catalog.SessionRecord) error
	ValidateBrainstormSession(context.Context, catalog.BrainstormRequest, string, string) (catalog.BrainstormAttempt, error)
	CompleteBrainstormAttempt(context.Context, catalog.CompleteBrainstormAttemptRequest) (catalog.BrainstormRun, error)
	AnswerBrainstormQuestion(context.Context, catalog.AnswerBrainstormRequest) (catalog.BrainstormRun, error)
	ApproveBrainstormSynthesis(context.Context, catalog.ApproveBrainstormSynthesisRequest) (catalog.BrainstormRun, error)
	CancelBrainstormAttempt(context.Context, catalog.CancelBrainstormAttemptRequest) (catalog.BrainstormRun, error)
	ResumePausedBrainstorm(context.Context, catalog.BrainstormRequest) (catalog.BrainstormRun, error)
	RequestBrainstormRevision(context.Context, catalog.RequestBrainstormRevisionRequest) (catalog.BrainstormRun, error)
	SkipBrainstormQuestions(context.Context, catalog.SkipBrainstormQuestionsRequest) (catalog.BrainstormRun, error)
	ConfirmDiscoveryAfterSkip(context.Context, catalog.BrainstormRequest) (catalog.BrainstormRun, error)
	FailBrainstormAttempt(context.Context, catalog.FailBrainstormAttemptRequest) (catalog.BrainstormRun, error)
	InterruptRunningBrainstormAttempts(context.Context) error
	GetAuthoringStageModelPreference(context.Context, string, sdd.Stage) (catalog.AuthoringStageModelPreference, error)
	SaveAuthoringStageModelPreference(context.Context, catalog.AuthoringStageModelPreference, int64) (catalog.AuthoringStageModelPreference, error)
	GetAuthoringCodeCopyAttempt(context.Context, string, string) (catalog.AuthoringCodeCopyAttempt, error)
	BeginAuthoringCodeCopyAttempt(context.Context, catalog.AuthoringCodeCopyAttempt) (catalog.AuthoringCodeCopyAttempt, bool, error)
	CompleteAuthoringCodeCopyAttempt(context.Context, string, string, string, string) (catalog.AuthoringCodeCopyAttempt, error)
	FailAuthoringCodeCopyAttempt(context.Context, string, string) error
	InterruptPreparingAuthoringCodeCopyAttempts(context.Context) error
	ListAuthoringCodeCopyAttempts(context.Context, string) ([]catalog.AuthoringCodeCopyAttempt, error)
	GetAuthoringCodeRun(context.Context, string, string) (catalog.AuthoringCodeRun, error)
	GetAuthoringCodeRunByID(context.Context, string) (catalog.AuthoringCodeRun, error)
	ListAuthoringCodeRuns(context.Context, string) ([]catalog.AuthoringCodeRun, error)
	GetAuthoringCodeDecision(context.Context, string, string) (catalog.AuthoringCodeDecision, error)
	DecideAuthoringCode(context.Context, catalog.AuthoringCodeDecisionRequest) (catalog.AuthoringCodeDecision, error)
	BeginAuthoringCodeRun(context.Context, catalog.AuthoringCodeRun) (catalog.AuthoringCodeRun, bool, error)
	CreateAuthoringCodeSession(context.Context, string, catalog.SessionRecord, catalog.ModelSelection) error
	RequestAuthoringCodeRunCancel(context.Context, string) (catalog.AuthoringCodeRun, error)
	FinishAuthoringCodeRun(context.Context, string, string, string, *catalog.BrainstormUsage, *catalog.AuthoringCodeCopyManifest, *catalog.AuthoringCodeCopyManifest, []catalog.CodeFileChange, []byte, string) (catalog.AuthoringCodeRun, error)
	InterruptRunningAuthoringCodeRuns(context.Context) error
	GetAuthoringStage(context.Context, string, sdd.Stage) (catalog.AuthoringStageRun, error)
	PreviewAuthoringStageInput(context.Context, catalog.AuthoringStageRequest, string) (catalog.AuthoringStageInput, error)
	GetAuthoringStageCommand(context.Context, string, string) (catalog.AuthoringStageCommandReceipt, error)
	BeginAuthoringStage(context.Context, catalog.BeginAuthoringStageRequest) (catalog.AuthoringStageAttempt, bool, error)
	ReviseAuthoringStage(context.Context, catalog.BeginAuthoringStageRequest) (catalog.AuthoringStageAttempt, bool, error)
	CreateAuthoringStageSession(context.Context, catalog.AuthoringStageRequest, catalog.AuthoringStageAttempt, catalog.SessionRecord) error
	ValidateAuthoringStageSession(context.Context, catalog.AuthoringStageRequest, string, string) (catalog.AuthoringStageAttempt, error)
	CompleteAuthoringStage(context.Context, catalog.CompleteAuthoringStageRequest) (catalog.AuthoringStageRun, error)
	FailAuthoringStage(context.Context, catalog.FailAuthoringStageRequest) (catalog.AuthoringStageRun, error)
	ApproveAuthoringStage(context.Context, catalog.AuthoringStageDecisionRequest) (catalog.AuthoringStageRun, error)
	SkipAuthoringStage(context.Context, catalog.AuthoringStageDecisionRequest) (catalog.AuthoringStageRun, error)
	CancelAuthoringStage(context.Context, catalog.AuthoringStageDecisionRequest) (catalog.AuthoringStageRun, error)
	ConfirmAuthoringStageStop(context.Context, string, sdd.Stage, string, string) (catalog.AuthoringStageRun, error)
	ListPendingAuthoringStops(context.Context) ([]catalog.AuthoringStageStop, error)
	InterruptRunningAuthoringStages(context.Context) error
	GetPipeline(context.Context, string) (catalog.PipelineRun, error)
	OpenPipelineDesign(context.Context, string, int64, map[sdd.Stage]catalog.PipelineDesignDocumentInput) (catalog.PipelineDesignWorkspace, error)
	GetPipelineDesign(context.Context, string) (catalog.PipelineDesignWorkspace, error)
	BeginPipelineDesignAttempt(context.Context, catalog.PipelineDesignAttemptRequest) (catalog.PipelineDesignWorkspace, catalog.PipelineDesignAttempt, bool, error)
	SetPipelineDesignPhase(context.Context, string, string, sdd.Stage) (catalog.PipelineDesignWorkspace, error)
	LinkPipelineDesignSession(context.Context, string, string, sdd.Stage, string) (catalog.PipelineDesignWorkspace, error)
	CompletePipelineDesignAttempt(context.Context, catalog.PipelineDesignCompleteRequest) (catalog.PipelineDesignWorkspace, error)
	FailPipelineDesignAttempt(context.Context, catalog.PipelineDesignFailRequest) (catalog.PipelineDesignWorkspace, error)
	RequestPipelineDesignCancellation(context.Context, string, string) (catalog.PipelineDesignWorkspace, error)
	EditPipelineDesignDocument(context.Context, catalog.PipelineDesignEditRequest) (catalog.PipelineDesignWorkspace, error)
	RestorePipelineDesignDocument(context.Context, catalog.PipelineDesignRestoreRequest) (catalog.PipelineDesignWorkspace, error)
	ApprovePipelineDesign(context.Context, catalog.PipelineDesignApproveRequest) (catalog.PipelineRun, error)
	RecoverPipelineDesignAttempts(context.Context, time.Time, []string) (int, error)
	ListPipelines(context.Context, string) ([]catalog.PipelineRun, error)
	SavePipelineArtifact(context.Context, string, sdd.Stage, string, int64) error
	SavePipelineExecutionArtifact(context.Context, string, sdd.Stage, string, string, int64) error
	TransitionPipeline(context.Context, string, sdd.Stage, sdd.Flow, string, string, int64) error
	DecidePipelineExecutionArtifact(context.Context, catalog.PipelineExecutionReviewRequest) (catalog.PipelineRun, error)
	ReopenPipelineCodeReview(context.Context, catalog.PipelineExecutionReviewRecoveryRequest) (catalog.PipelineRun, error)
	GetPipelineExecutionReviewResult(context.Context, string, string, string) (catalog.PipelineRun, bool, error)
	GetPipelineStageSkipReason(context.Context, string, sdd.Stage) (string, error)
	LinkPipelineSession(context.Context, catalog.PipelineSession) error
	ListPipelineSessions(context.Context, string, string) ([]catalog.PipelineSession, error)
	GetPipelineIDForSession(context.Context, string) (string, error)
	GetPipelineIDForPRSession(context.Context, string) (string, error)
	SetPipelineCoordinator(ctx context.Context, pipelineID, sessionID string) error
	GetPipelineIDForCoordinator(context.Context, string) (string, error)
	ReplacePipelineCoordinator(ctx context.Context, pipelineID, previous, next string) error
	ListWorkCoordinators(ctx context.Context, workspaceID string) ([]catalog.WorkCoordinator, error)
	SavePipelinePullRequest(context.Context, catalog.PipelinePullRequest) (catalog.PipelinePullRequest, error)
	GetPipelinePullRequest(context.Context, string) (catalog.PipelinePullRequest, error)
	ListPipelinePullRequests(context.Context, string) ([]catalog.PipelinePullRequest, error)
	ListWatchedPullRequests(context.Context) ([]catalog.PipelinePullRequest, error)
	LinkPipelinePRSession(context.Context, catalog.PipelinePRSession) error
	ListPipelinePRSessions(context.Context, string) ([]catalog.PipelinePRSession, error)
	ListWorkspaceStageExecutors(context.Context, string) ([]catalog.StageExecutor, error)
	GetWorkspaceStageExecutor(context.Context, string, sdd.Stage) (catalog.StageExecutor, error)
	SaveWorkspaceStageExecutor(context.Context, catalog.StageExecutor) error
	DeleteWorkspaceStageExecutor(context.Context, string, sdd.Stage) error
	GetProjectMemory(context.Context, string) (catalog.ProjectMemory, error)
	ListSessionPins(context.Context) (map[string]time.Time, error)
	SetSessionPinned(context.Context, string, bool) error
	SaveProjectMemory(context.Context, catalog.ProjectMemory) error
	FinishPipelinePRs(context.Context, catalog.PipelinePRsFinish) error
	MarkPipelineCodeApplied(context.Context, string, string, int64, time.Time) error
	UpsertAgent(context.Context, catalog.Agent) error
	GetAgent(context.Context, string) (catalog.Agent, error)
	ListAgents(context.Context) ([]catalog.Agent, error)
	GetSessionAgentSnapshot(context.Context, string) (catalog.AgentSnapshot, error)
	UpsertSessionWithAgentSnapshot(context.Context, catalog.SessionRecord, catalog.AgentSnapshot) error
	CreateDelegatedSessionWithSnapshots(context.Context, catalog.SessionRecord, *catalog.AgentSnapshot, string, string, catalog.Delegation, json.RawMessage, int) (bool, error)
	GetDelegationByRequestID(context.Context, string, string) (catalog.Delegation, error)
	ReserveDelegationPrompt(context.Context, string, int) (int, bool, bool, error)
	ReleaseDelegationPrompt(context.Context, string) error
	ListDelegations(context.Context, string) ([]catalog.Delegation, error)
	GetParentDelegation(context.Context, string) (catalog.Delegation, error)
	GetDelegationOutcome(context.Context, string) (catalog.DelegationOutcome, error)
	GetDelegationDepth(context.Context, string) (int, error)
	SaveWorkflow(context.Context, catalog.Workflow) error
	GetWorkflow(context.Context, string) (catalog.Workflow, error)
	ListWorkflows(context.Context, string) ([]catalog.Workflow, error)
	CreateWorkflowRun(context.Context, catalog.WorkflowRun) error
	GetWorkflowRun(context.Context, string) (catalog.WorkflowRun, error)
	ListWorkflowRuns(context.Context, string) ([]catalog.WorkflowRun, error)
	UpdateWorkflowRun(context.Context, catalog.WorkflowRun, string, string) error
	SaveSchedule(context.Context, catalog.Schedule, int64) error
	GetSchedule(context.Context, string) (catalog.Schedule, error)
	ListSchedules(context.Context, string) ([]catalog.Schedule, error)
	ListDueSchedules(context.Context, time.Time) ([]catalog.Schedule, error)
	ClaimDueSchedule(context.Context, string, time.Time, time.Time, time.Time, string) (catalog.ScheduleJob, error)
	CreateManualScheduleJob(context.Context, string, time.Time) (catalog.ScheduleJob, error)
	GetScheduleJob(context.Context, string) (catalog.ScheduleJob, error)
	ListScheduleJobs(context.Context, string) ([]catalog.ScheduleJob, error)
	ListQueuedScheduleJobs(context.Context) ([]catalog.ScheduleJob, error)
	ListWaitingScheduleJobs(context.Context, string) ([]catalog.ScheduleJob, error)
	UpdateScheduleJob(context.Context, catalog.ScheduleJob, string) error
	InterruptRunningScheduleJobs(context.Context, time.Time) error
	UpsertSkill(context.Context, catalog.Skill) error
	GetSkill(context.Context, string) (catalog.Skill, error)
	ListSkills(context.Context, string) ([]catalog.Skill, error)
	ReplaceKnowledge(context.Context, catalog.KnowledgeDocument, []knowledge.Chunk) (catalog.KnowledgeDocument, error)
	GetKnowledge(context.Context, string, string) (catalog.KnowledgeDocument, error)
	ListKnowledge(context.Context, string) ([]catalog.KnowledgeDocument, error)
	SearchKnowledge(context.Context, string, string, string, int) ([]catalog.KnowledgeHit, error)
	RemoveKnowledge(context.Context, string, string) error
	SaveEmbeddingProfile(context.Context, string, embeddings.Profile) error
	GetEmbeddingProfile(context.Context, string) (embeddings.Profile, error)
	PendingVectorChunks(context.Context, string, string, int) ([]knowledge.VectorChunk, error)
	SaveVectorBatch(context.Context, string, string, []knowledge.VectorWrite) error
	VectorProgress(context.Context, string, string) (knowledge.VectorProgress, error)
	SearchVectors(context.Context, string, string, string, []float64, int) ([]catalog.KnowledgeHit, error)
	SaveLocalChannel(context.Context, catalog.LocalChannel) error
	GetLocalChannel(context.Context, string) (catalog.LocalChannel, error)
	GetLocalChannelByFolder(context.Context, string, string) (catalog.LocalChannel, error)
	ListLocalChannels(context.Context, string) ([]catalog.LocalChannel, error)
	SetLocalChannelStatus(context.Context, string, string, string) (catalog.LocalChannel, error)
	AddIncomingChannelMessage(context.Context, catalog.ChannelMessage) (bool, error)
	ReserveOutgoingChannelMessage(context.Context, catalog.ChannelMessage) (catalog.ChannelMessage, error)
	GetOutgoingChannelMessage(context.Context, string, string) (catalog.ChannelMessage, error)
	SetChannelMessageStatus(context.Context, string, string, string, string) (catalog.ChannelMessage, error)
	ListChannelMessages(context.Context, string) ([]catalog.ChannelMessage, error)
	CreateSessionWithSnapshots(context.Context, catalog.SessionRecord, *catalog.AgentSnapshot, string, json.RawMessage, string, *catalog.ModelSelection) error
	GetSessionModelSelection(context.Context, string) (catalog.ModelSelection, error)
	GetSessionSkillSnapshot(context.Context, string) (string, error)
	UpsertMCPServer(context.Context, catalog.MCPServer) error
	PublishMCPServer(context.Context, catalog.MCPServer) error
	GetMCPServer(context.Context, string) (catalog.MCPServer, error)
	ListMCPServers(context.Context, string) ([]catalog.MCPServer, error)
	AddCredentialReference(context.Context, catalog.CredentialReference) error
	UpsertWorkspace(context.Context, catalog.Workspace) error
	GetProviderProfile(context.Context, string) (catalog.ProviderProfile, error)
	UpsertProviderProfile(context.Context, catalog.ProviderProfile) error
	PublishProviderProfile(context.Context, catalog.ProviderProfile) error
	GetOpenRouterManagementCredential(context.Context, string) (catalog.OpenRouterManagementCredential, error)
	PublishOpenRouterManagementCredential(context.Context, catalog.OpenRouterManagementCredential) error
	ClearOpenRouterManagementCredential(context.Context, string) error
	ListCredentialReferences(context.Context) ([]catalog.CredentialReference, error)
	ListProviderProfiles(context.Context) ([]catalog.ProviderProfile, error)
	GetSession(context.Context, string) (catalog.SessionRecord, error)
	UpsertSession(context.Context, catalog.SessionRecord) error
	ListAllSessions(context.Context) ([]catalog.SessionRecord, error)
	ListSessions(context.Context, string) ([]catalog.SessionRecord, error)
}
type ExternalBackend interface{ externalagent.Adapter }
type AuditExporter interface {
	Export(context.Context, ExportAuditInput) (string, error)
}
type Dependencies struct {
	Store                  Store
	Secrets                secrets.Store
	External               map[string]ExternalBackend
	ProviderFactory        func(openai.Config) (agentcore.Provider, error)
	Audit                  AuditExporter
	Emit                   func(string, any)
	ExecutionCacheRoot     string
	PrivateWorkspaceParent string
}
