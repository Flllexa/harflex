import { Dialogs, Events } from '@wailsio/runtime'
import { Service } from '../../bindings/github.com/persioflexa/harflex/internal/application'
import { invalidEventDiagnostic, parse, type Backend, type TerminalOutput } from './backend'
import { t } from '../i18n'

// The only module that touches generated bindings; every DTO is validated here.
export const wailsBackend: Backend = {
  openWorkspace: async path => parse.workspace(await Service.OpenWorkspace(path)),
  listWorkspaces: async () => parse.workspaces(await Service.ListWorkspaces()),
  setWorkspaceArchived: async (workspaceId, archived) => parse.workspaceSummary(await Service.SetWorkspaceArchived({ workspaceId, archived })),
  setWorkspaceProfile: async (workspaceId, profile, options) => parse.workspace(await Service.SetWorkspaceProfile({ workspaceId, profile, confirmFullAccess: options?.confirmFullAccess === true })),
  listProviderProfiles: async () => parse.providerProfiles(await Service.ListProviderProfiles()),
  queryHTTPModelCatalog: async (query, signal) => {
    if (signal?.aborted) throw new DOMException(t('Consulta cancelada.'), 'AbortError')
    const call = Service.QueryHTTPModelCatalog(query)
    return parse.modelCatalog(await (signal ? call.cancelOn(signal) : call))
  },
  queryCLIModelCatalog: async (query, signal) => {
    if (signal?.aborted) throw new DOMException(t('Consulta cancelada.'), 'AbortError')
    const call = Service.QueryCLIModelCatalog(query)
    return parse.modelCatalog(await (signal ? call.cancelOn(signal) : call))
  },
  getOpenRouterManagementKeyStatus: async profileId => parse.openRouterManagementKeyStatus(await Service.GetOpenRouterManagementKeyStatus(profileId)),
  saveOpenRouterManagementKey: async (profileId, key) => parse.openRouterManagementKeyStatus(await Service.SaveOpenRouterManagementKey(profileId, key)),
  clearOpenRouterManagementKey: async profileId => { await Service.ClearOpenRouterManagementKey(profileId) },
  probeCredentialStore: async workspaceId => parse.credentialProbe(await Service.ProbeCredentialStore(workspaceId)),
  getSettings: async () => parse.settings(await Service.GetSettings()),
  saveSettings: async input => parse.settings(await Service.SaveSettings(input)),
  listLogEvents: async query => parse.logEntries(await Service.ListLogEvents(query)),
  inspectRepository: async workspaceId => parse.repository(await Service.InspectRepository(workspaceId)),
  listChatProjects: async perProject => parse.chatProjects(await Service.ListChatProjects({ perProject: perProject ?? 0 })),
  setSessionPinned: async (sessionId, pinned) => { await Service.SetSessionPinned({ sessionId, pinned }) },
  revealWorkspace: async workspaceId => { await Service.RevealWorkspace(workspaceId) },
  startTerminal: async (workspaceId, cols, rows) => {
    const value = await Service.StartTerminal({ workspaceId, cols, rows })
    if (!value || typeof value.id !== 'string' || !value.id) throw new Error('invalid terminal')
    return { id: value.id, shell: String(value.shell ?? ''), path: String(value.path ?? '') }
  },
  writeTerminal: async (id, data) => { await Service.WriteTerminal({ id, data }) },
  resizeTerminal: async (id, cols, rows) => { await Service.ResizeTerminal({ id, cols, rows }) },
  closeTerminal: async id => { await Service.CloseTerminal(id) },
  onTerminalOutput: listener => Events.On('harflex:terminal', event => {
    const value = event.data as Partial<TerminalOutput> | undefined
    if (value && typeof value.id === 'string') listener({ id: value.id, data: typeof value.data === 'string' ? value.data : undefined, exited: value.exited === true, code: typeof value.code === 'number' ? value.code : undefined })
  }),
  getProjectMemory: async workspaceId => parse.projectMemory(await Service.GetProjectMemory(workspaceId)),
  declineProjectMemory: async workspaceId => parse.projectMemory(await Service.DeclineProjectMemory(workspaceId)),
  refreshProjectMemory: async workspaceId => parse.projectMemory(await Service.RefreshProjectMemory(workspaceId)),
  saveProjectMemory: async (workspaceId, content) => parse.projectMemory(await Service.SaveProjectMemory({ workspaceId, content })),
  listAllWorktrees: async () => parse.worktreeGroups(await Service.ListAllWorktrees()),
  listWorktrees: async workspaceId => parse.worktreeList(await Service.ListWorktrees(workspaceId)),
  deleteWorktree: async input => parse.deleteWorktreeResult(await Service.DeleteWorktree(input)),
  pruneWorktrees: async workspaceId => parse.worktreeList(await Service.PruneWorktrees(workspaceId)),
  prepareWorktreeSave: async input => parse.worktreeSavePlan(await Service.PrepareWorktreeSave(input)),
  createPipeline: async input => parse.pipeline(await Service.CreatePipeline(input)),
  createAuthoringPipeline: async input => parse.authoringPipeline(await Service.CreateAuthoringPipeline(input)),
  openPipelineDesign: async pipelineId => parse.pipelineDesign(await Service.OpenPipelineDesign(pipelineId)),
  preparePipelineDesign: async input => parse.pipelineDesign(await Service.PreparePipelineDesign(input)),
  editPipelineDesignDocument: async input => parse.pipelineDesign(await Service.EditPipelineDesignDocument(input)),
  restorePipelineDesignDocument: async input => parse.pipelineDesign(await Service.RestorePipelineDesignDocument(input)),
  cancelPipelineDesign: async input => parse.pipelineDesign(await Service.CancelPipelineDesign(input)),
  approvePipelineDesign: async input => parse.pipeline(await Service.ApprovePipelineDesign(input)),
  deriveAuthoringPipeline: async input => parse.authoringPipeline(await Service.DeriveAuthoringPipeline(input)),
  reviseAuthoringDiscovery: async input => parse.authoringPipeline(await Service.ReviseAuthoringDiscovery(input)),
  startBrainstorming: async input => parse.brainstorm(await Service.StartBrainstorming(input)),
  getBrainstorming: async input => parse.brainstorm(await Service.GetBrainstorming(input)),
  listBrainstorming: async input => parse.brainstorms(await Service.ListBrainstorming(input)),
  answerBrainstormQuestion: async input => parse.brainstorm(await Service.AnswerBrainstormQuestion(input)),
  generateBrainstormQuestion: async input => parse.brainstorm(await Service.GenerateBrainstormQuestion(input)),
  questionsSufficient: async input => parse.brainstorm(await Service.QuestionsSufficient(input)),
  finishAndGenerateSynthesis: async input => parse.brainstorm(await Service.FinishAndGenerateSynthesis(input)),
  approveBrainstormSynthesis: async input => parse.brainstorm(await Service.ApproveBrainstormSynthesis(input)),
  requestBrainstormRevision: async input => parse.brainstorm(await Service.RequestBrainstormRevision(input)),
  skipBrainstormQuestions: async input => parse.brainstorm(await Service.SkipBrainstormQuestions(input)),
  confirmDiscoveryAfterSkip: async input => parse.brainstorm(await Service.ConfirmDiscoveryAfterSkip(input)),
  cancelBrainstormAttempt: async input => parse.brainstorm(await Service.CancelBrainstormAttempt(input)),
  resumePausedBrainstorm: async input => parse.brainstorm(await Service.ResumePausedBrainstorm(input)),
  getAuthoringStage: async input => parse.authoringStage(await Service.GetAuthoringStage(input)),
  listStageExecutors: async workspaceId => parse.stageExecutors(await Service.ListStageExecutors(workspaceId)),
  saveStageExecutor: async input => parse.stageExecutor(await Service.SaveStageExecutor(input)),
  clearStageExecutor: async (workspaceId, stage) => { await Service.ClearStageExecutor(workspaceId, stage) },
  getAuthoringStageModelPreference: async input => parse.authoringStageModelPreference(await Service.GetAuthoringStageModelPreference(input)),
  saveAuthoringStageModelPreference: async input => parse.authoringStageModelPreference(await Service.SaveAuthoringStageModelPreference(input)),
  generateAuthoringStage: async input => parse.authoringStage(await Service.GenerateAuthoringStage(input)),
  requestAuthoringStageRevision: async input => parse.authoringStage(await Service.RequestAuthoringStageRevision(input)),
  approveAuthoringStage: async input => parse.authoringStage(await Service.ApproveAuthoringStage(input)),
  skipAuthoringStage: async input => parse.authoringStage(await Service.SkipAuthoringStage(input)),
  cancelAuthoringStage: async input => parse.authoringStage(await Service.CancelAuthoringStage(input)),
  startAuthoringCode: async input => {
    const run = parse.authoringCodeRun(await Service.StartAuthoringCode(input))
    if (run.pipelineId !== input.pipelineId || run.preparationId !== input.preparationId || run.preparationRequestId !== input.preparationRequestId ||
      run.requestId !== input.requestId || run.pipelineRevision !== input.expectedPipelineRevision || run.planStageRevision !== input.expectedPlanStageRevision ||
      run.planArtifactVersion !== input.expectedPlanArtifactVersion || run.codePreferenceRevision !== input.expectedCodePreferenceRevision ||
      run.codeSelectionHash !== input.expectedCodeSelectionHash || run.manifestHash !== input.expectedManifestHash) {
      throw new Error('Authoring Code run does not match its admission request')
    }
    return run
  },
  cancelAuthoringCode: async input => {
    const run = parse.authoringCodeRun(await Service.CancelAuthoringCode(input))
    if (run.pipelineId !== input.pipelineId || run.id !== input.attemptId) throw new Error('Authoring Code cancellation returned another run')
    return run
  },
  getAuthoringCodeRuns: async input => {
    const history = parse.authoringCodeRuns(await Service.GetAuthoringCodeRuns(input))
    if (history.pipelineId !== input.pipelineId) throw new Error('Authoring Code history belongs to another pipeline')
    return history
  },
  preflightAuthoringCode: async input => {
    const result = parse.authoringCodePreflight(await Service.PreflightAuthoringCode(input))
    if (result.pipelineId !== input.pipelineId || result.pipelineRevision !== input.expectedPipelineRevision || result.codePreferenceRevision !== input.expectedCodePreferenceRevision) {
      throw new Error('Authoring Code preflight does not match the requested pipeline snapshot')
    }
    return result
  },
  prepareAuthoringCode: async input => {
    const result = parse.authoringCodeCopyPreparation(await Service.PrepareAuthoringCode(input))
    if (result.pipelineId !== input.pipelineId || result.requestId !== input.requestId || result.pipelineRevision !== input.expectedPipelineRevision ||
      result.codePreferenceRevision !== input.expectedCodePreferenceRevision || result.codeSelectionHash !== input.expectedCodeSelectionHash || result.manifestHash !== input.expectedManifestHash) {
      throw new Error('Authoring Code copy preparation does not match the confirmation request')
    }
    return result
  },
  getAuthoringCodeCopyPreparations: async input => {
    const history = parse.authoringCodeCopyPreparations(await Service.GetAuthoringCodeCopyPreparations(input))
    if (history.pipelineId !== input.pipelineId) throw new Error('Authoring Code copy history belongs to another pipeline')
    return history
  },
  getAuthoringCodeRunPatch: async input => {
    const page = parse.authoringCodeRunPatchPage(await Service.GetAuthoringCodeRunPatch(input))
    if (page.pipelineId !== input.pipelineId || page.runId !== input.runId || page.cursor !== input.cursor) {
      throw new Error('Authoring Code patch page does not match the requested run and cursor')
    }
    return page
  },
  listPipelines: async workspaceId => parse.pipelines(await Service.ListPipelines(workspaceId)),
  listWorkCoordinators: async workspaceId => parse.workCoordinators(await Service.ListWorkCoordinators(workspaceId)),
  ensureWorkChats: async workspaceId => parse.ensureWorkChats(await Service.EnsureWorkChats(workspaceId)),
  getPipeline: async pipelineId => parse.pipeline(await Service.GetPipeline(pipelineId)),
  getPipelineStageActivity: async (pipelineId, stage) => parse.pipelineStageActivity(await Service.GetPipelineStageActivity(pipelineId,stage)),
  savePipelineArtifact: async (pipelineId, stage, content) => parse.pipeline(await Service.SavePipelineArtifact({ pipelineId, stage, content })),
  advancePipeline: async pipelineId => parse.pipeline(await Service.AdvancePipeline(pipelineId)),
  skipPipelineStage: async (pipelineId, reason) => parse.pipeline(await Service.SkipPipelineStage({ pipelineId, reason })),
  previewPipelineCodeWorkspace: async workspaceId => parse.pipelineCodeCopyPreview(await Service.PreviewPipelineCodeWorkspace(workspaceId)),
  createPipelineSession: async (pipelineId, backendId, role, selection, confirmWorkspaceCopy) => parse.pipelineSession(await Service.CreatePipelineSession({ pipelineId, backendId, role, ...(selection ? { selection } : {}), ...(confirmWorkspaceCopy ? { confirmWorkspaceCopy: true } : {}) })),
  getPipelineForSession: async (sessionId, workspaceId) => parse.pipeline(await Service.GetPipelineForSession({ sessionId, workspaceId })),
  completePipelineCode: async pipelineId => parse.pipeline(await Service.CompletePipelineCode(pipelineId)),
  completePipelineEvaluation: async pipelineId => parse.pipeline(await Service.CompletePipelineEvaluation(pipelineId)),
  decidePipelineExecutionArtifact: async input => parse.pipeline(await Service.DecidePipelineExecutionArtifact(input)),
  startPipelineQA: async (pipelineId, evaluator) => parse.qaLoop(await Service.StartPipelineQA({ pipelineId, evaluator: { backendId: evaluator.backendId, ...(evaluator.selection ? { selection: evaluator.selection } : {}) } })),
  resumePipelineFixes: async input => parse.qaLoop(await Service.ResumePipelineFixes({ pipelineId: input.pipelineId, coder: { backendId: input.coder.backendId, ...(input.coder.selection ? { selection: input.coder.selection } : {}) }, evaluator: { backendId: input.evaluator.backendId, ...(input.evaluator.selection ? { selection: input.evaluator.selection } : {}) } })),
  fixPipelineFindings: async input => parse.qaLoop(await Service.FixPipelineFindings({ ...input, coder: { backendId: input.coder.backendId, ...(input.coder.selection ? { selection: input.coder.selection } : {}) }, evaluator: { backendId: input.evaluator.backendId, ...(input.evaluator.selection ? { selection: input.evaluator.selection } : {}) } })),
  getPipelineQALoop: async pipelineId => parse.qaLoop(await Service.GetPipelineQALoop(pipelineId)),
  listPipelinePullRequests: async pipelineId => parse.pullRequests(await Service.ListPipelinePullRequests(pipelineId)),
  setPullRequestWatch: async (id, watch) => parse.pullRequest(await Service.SetPullRequestWatch({ id, watch })),
  checkPullRequestNow: async id => parse.pullRequest(await Service.CheckPullRequestNow(id)),
  onPullRequestsChange: listener => Events.On('harflex:pull-requests', event => {
    const result = event.data as { pipelineId?: unknown } | null
    if (typeof result?.pipelineId === 'string' && result.pipelineId) listener(result.pipelineId)
  }),
  reopenPipelineCodeReview: async input => parse.pipeline(await Service.ReopenPipelineCodeReview(input)),
  applyPipelineCode: async pipelineId => parse.pipeline(await Service.ApplyPipelineCode(pipelineId)),
  finishPipelinePRs: async (pipelineId, outcome, reason) => parse.pipeline(await Service.FinishPipelinePRs({ pipelineId, outcome, reason: reason ?? '' })),
  saveAgent: async input => parse.agent(await Service.SaveAgent({ ...input, id: input.id ?? '' })),
  listAgents: async () => parse.agents(await Service.ListAgents()),
  delegateToAgent: async (parentSessionId, agentId, prompt, requestId) => parse.delegatedSession(await Service.DelegateToAgent({ parentSessionId, agentId, requestId, prompt })),
  listDelegations: async parentSessionId => parse.delegations(await Service.ListDelegations(parentSessionId)),
  getParentDelegation: async childSessionId => parse.parentDelegation(await Service.GetParentDelegation(childSessionId)),
  saveWorkflow: async input => parse.workflow(await Service.SaveWorkflow({ ...input, id: input.id ?? '' })),
  listWorkflows: async workspaceId => parse.workflows(await Service.ListWorkflows(workspaceId)),
  startWorkflow: async input => parse.workflowRun(await Service.StartWorkflow(input)),
  getWorkflowRun: async runId => parse.workflowRun(await Service.GetWorkflowRun(runId)),
  listWorkflowRuns: async workspaceId => parse.workflowRuns(await Service.ListWorkflowRuns(workspaceId)),
  runWorkflowStep: async runId => parse.workflowRun(await Service.RunWorkflowStep(runId)),
  resumeWorkflowRun: async input => parse.workflowRun(await Service.ResumeWorkflowRun(input)),
  cancelWorkflowRun: async runId => parse.workflowRun(await Service.CancelWorkflowRun(runId)),
  saveSchedule: async input => parse.schedule(await Service.SaveSchedule({ ...input, id: input.id ?? '', revision: input.revision ?? 0 })),
  listSchedules: async workspaceId => parse.schedules(await Service.ListSchedules(workspaceId)),
  setSchedulePaused: async (scheduleId, paused, revision) => parse.schedule(await Service.SetSchedulePaused({ scheduleId, paused, revision })),
  runScheduleNow: async scheduleId => parse.scheduleJob(await Service.RunScheduleNow(scheduleId)),
  listScheduleJobs: async workspaceId => parse.scheduleJobs(await Service.ListScheduleJobs(workspaceId)),
  cancelScheduleJob: async jobId => parse.scheduleJob(await Service.CancelScheduleJob(jobId)),
  onScheduleChange: listener => Events.On('harflex:schedule', event => {
    const result = event.data as { workspaceId?: unknown } | null
    if (typeof result?.workspaceId === 'string' && result.workspaceId) listener(result.workspaceId)
  }),
  onPipelineCreated: listener => Events.On('harflex:pipeline', event => {
    const result = event.data as { workspaceId?: unknown; pipelineId?: unknown } | null
    if (typeof result?.workspaceId === 'string' && result.workspaceId && typeof result.pipelineId === 'string' && result.pipelineId) listener({ workspaceId: result.workspaceId, pipelineId: result.pipelineId })
  }),
  saveSkill: async input => parse.skill(await Service.SaveSkill({ ...input, id: input.id ?? '' })),
  importSkill: async input => parse.skill(await Service.ImportSkill(input)),
  listSkills: async workspaceId => parse.skills(await Service.ListSkills(workspaceId)),
  importKnowledge: async input => parse.knowledgeDocument(await Service.ImportKnowledge(input)),
  reindexKnowledge: async input => parse.knowledgeDocument(await Service.ReindexKnowledge(input)),
  listKnowledge: async workspaceId => parse.knowledgeDocuments(await Service.ListKnowledge(workspaceId)),
  searchKnowledge: async input => parse.knowledgeHits(await Service.SearchKnowledge(input)),
  searchKnowledgeDetailed: async input => parse.knowledgeSearch(await Service.SearchKnowledgeDetailed(input)),
  getKnowledgeEmbeddingProfile: async workspaceId => parse.knowledgeEmbeddingProfile(await Service.GetKnowledgeEmbeddingProfile(workspaceId)),
  saveKnowledgeEmbeddingProfile: async input => parse.knowledgeEmbeddingProfile(await Service.SaveKnowledgeEmbeddingProfile(input)),
  indexKnowledgeVectors: async input => parse.knowledgeEmbeddingProfile(await Service.IndexKnowledgeVectors(input)),
  removeKnowledge: async input => { await Service.RemoveKnowledge(input) },
  saveLocalChannel: async input => parse.localChannel(await Service.SaveLocalChannel({ ...input, id: input.id ?? '' })),
  listLocalChannels: async workspaceId => parse.localChannels(await Service.ListLocalChannels(workspaceId)),
  importChannelInbox: async channelId => parse.channelImport(await Service.ImportChannelInbox(channelId)),
  listChannelMessages: async channelId => parse.channelMessages(await Service.ListChannelMessages(channelId)),
  sendChannelMessage: async input => parse.channelMessage(await Service.SendChannelMessage(input)),
  retryChannelMessage: async input => parse.channelMessage(await Service.RetryChannelMessage(input)),
  saveMCPServer: async input => parse.mcpServer(await Service.SaveMCPServer({ ...input, id: input.id ?? '' })),
  listMCPServers: async workspaceId => parse.mcpServers(await Service.ListMCPServers(workspaceId)),
  connectMCPServer: async id => parse.mcpServer(await Service.ConnectMCPServer(id)),
  disableMCPServer: async id => parse.mcpServer(await Service.DisableMCPServer(id)),
  pickDirectory: async () => {
    const selected = await Dialogs.OpenFile({ CanChooseDirectories: true, CanChooseFiles: false, CanCreateDirectories: true, Title: t('Abrir projeto') })
    return typeof selected === 'string' ? selected : ''
  },
  pickSkillFile: async () => {
    const selected = await Dialogs.OpenFile({ CanChooseDirectories: false, CanChooseFiles: true, Title: t('Importar SKILL.md') })
    return typeof selected === 'string' ? selected : ''
  },
  pickKnowledgeFile: async () => {
    const selected = await Dialogs.OpenFile({ CanChooseDirectories: false, CanChooseFiles: true, Title: t('Indexar arquivo do projeto') })
    return typeof selected === 'string' ? selected : ''
  },
  listBackends: async () => parse.backends(await Service.ListBackends()),
  // The type and clear flag cross explicitly; the key goes straight to the binding and the caller clears its copy.
  saveProviderProfile: async input => parse.backend(await Service.SaveProviderProfile({ ...input, kind: 'openai_compatible' })),
  createSession: async (workspaceId, backendId, agentId = '') => parse.session(await Service.CreateSession({ workspaceId, backendId, agentId, mode: '', bypassReason: '', modelId: '', reasoningEffort: '', catalogRevision: '' })),
  createDirectSession: async input => parse.session(await Service.CreateDirectSession({ ...input, agentId: input.agentId ?? '', modelId: input.modelId ?? '', reasoningEffort: input.reasoningEffort ?? '', catalogRevision: input.catalogRevision ?? '' })),
  getSessionModelSelection: async sessionId => parse.sessionModelSelection(await Service.GetSessionModelSelection(sessionId)),
  listSessions: async workspaceId => parse.sessions(await Service.ListSessions({ workspaceId: parse.id(workspaceId) })),
  openSession: async (sessionId, workspaceId) => parse.session(await Service.OpenSession({ sessionId: parse.id(sessionId), workspaceId: parse.id(workspaceId) })),
  exportAudit: async (sessionId, destination) => parse.auditPath(await Service.ExportAudit({ sessionId: parse.id(sessionId), destination: parse.auditPath(destination) })),
  prompt: async (sessionId, text) => parse.runResult(await Service.Prompt({ sessionId, text })),
  approve: async (sessionId, approvalId, allow) => parse.runResult(await Service.Approve({ sessionId, approvalId, allow })),
  cancel: async sessionId => { await Service.Cancel(sessionId) },
  listEvents: async (sessionId, afterSequence, limit = 1000) => parse.events(await Service.ListEvents({ sessionId, afterSequence, limit })),
  onEvent: listener => Events.On('harflex:event', event => {
    const parsed = parse.safeEvent(event.data)
    if (parsed) listener(parsed)
    else listener(invalidEventDiagnostic())
  }),
}
