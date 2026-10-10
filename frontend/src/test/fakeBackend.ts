import type { AgentEvent, AuthoringStageModelPreference, Backend, PipelineStage, RunResult, SaveAuthoringStageModelPreferenceInput, StageExecutor } from '../lib/backend'

export const diff = '--- a/notes.md\n+++ b/notes.md\n@@ -0,0 +1 @@\n+olá'
export const sessionMetadata = { resumable: true, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z' }

/** Simulates the Go facade: events are journaled and pushed before each call resolves. */
export function createFakeBackend() {
  const listeners = new Set<(event: AgentEvent) => void>()
  const journal: AgentEvent[] = []
  let pendingApproval = ''
  const workspacePaths = new Map<string, string>()
  const emit = (type: string, data: unknown = {}) => {
    const event = { id: `event-${journal.length + 1}`, streamId: 'session-1', sequence: journal.length + 1, type, data, createdAt: '2026-09-25T10:00:00Z' }
    journal.push(event)
    listeners.forEach(listener => listener(event))
  }
  // The executor each project chose per phase, kept in pipeline order like the Go service lists it.
  const stageExecutors = new Map<string, StageExecutor>()
  const stageOrder: PipelineStage[] = ['discovery', 'spec', 'plan', 'code', 'eval', 'prs']
  const authoringPreferences = new Map<string, AuthoringStageModelPreference>()
  const preferenceKey = (pipelineId: string, stage: 'spec' | 'plan') => `${pipelineId}:${stage}`
  const getAuthoringStageModelPreference = async (input: { pipelineId: string; stage: 'spec' | 'plan' }): Promise<AuthoringStageModelPreference> => {
    const existing = authoringPreferences.get(preferenceKey(input.pipelineId, input.stage))
    if (existing) return existing
    return {
      pipelineId: input.pipelineId, stage: input.stage, modelMode: 'inherit', effortMode: 'inherit', explicitEffort: '', preferenceRevision: 0,
      resolution: 'ready', modelSource: 'global_default', effortSource: 'global_default', inheritedFrom: 'global_default', catalogValidationRequired: true, errorCode: '',
      selection: { backendId: 'api-1', modelId: 'modelo-original', reasoningEffort: '', catalogRevision: 'profile-rev-1', source: 'global_default', destination: 'https://api.example.test:443', status: 'unverified_default', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096, contextLength: 0, checkedAt: new Date().toISOString() },
    }
  }
  const saveAuthoringStageModelPreference = async (input: SaveAuthoringStageModelPreferenceInput): Promise<AuthoringStageModelPreference> => {
    const current = await getAuthoringStageModelPreference({ pipelineId: input.pipelineId, stage: input.stage })
    if (current.preferenceRevision !== input.expectedRevision) throw Object.assign(new Error('Authoring preference changed'), { cause: { code: 'pipeline_conflict' } })
    const modelSource = input.modelMode === 'override' ? 'phase_override' : current.modelSource
    const inheritedFrom = input.modelMode === 'override' && input.effortMode === 'inherit' ? current.modelSource : input.modelMode === 'inherit' || input.effortMode === 'inherit' ? modelSource : ''
    const selection = input.modelMode === 'override' ? {
      backendId: input.selection.profileId, modelId: input.selection.modelId,
      reasoningEffort: input.effortMode === 'explicit' ? input.explicitEffort : input.effortMode === 'automatic' ? '' : current.selection.reasoningEffort,
      catalogRevision: input.selection.catalogRevision, source: input.selection.source, destination: input.selection.destination,
      status: input.selection.source === 'openrouter_general_unfiltered' ? 'listed_unfiltered' : 'listed',
      confirmUnfiltered: input.selection.confirmUnfiltered, confirmJitLoad: false,
      maxOutputTokens: input.selection.maxOutputTokens || 4096, contextLength: 0, checkedAt: input.selection.checkedAt,
    } : {
      ...current.selection, reasoningEffort: input.effortMode === 'explicit' ? input.explicitEffort : input.effortMode === 'automatic' ? '' : current.selection.reasoningEffort,
    }
    const saved: AuthoringStageModelPreference = {
      ...current, modelMode: input.modelMode, effortMode: input.effortMode, explicitEffort: input.explicitEffort,
      preferenceRevision: current.preferenceRevision + 1, resolution: 'ready', modelSource,
      effortSource: input.effortMode === 'explicit' ? 'phase_override' : input.effortMode === 'automatic' ? 'automatic' : input.modelMode === 'override' ? current.effortSource : modelSource,
      inheritedFrom, catalogValidationRequired: modelSource === 'global_default' && selection.status === 'unverified_default', selection,
    }
    authoringPreferences.set(preferenceKey(input.pipelineId, input.stage), saved)
    return saved
  }
  const writeCall = (id: string) => ({ id, name: 'write', arguments: { path: 'notes.md', content: 'olá' } })
  const respondReadOnly = (text: string, content: string): RunResult => {
    emit('run.started')
    emit('message.user', { role: 'user', content: text })
    emit('assistant.delta', { delta: content })
    emit('message.assistant', { role: 'assistant', content })
    emit('run.completed', { reason: '' })
    return { status: 'completed' }
  }
  const backend: Backend = {
    openWorkspace: async path => { workspacePaths.set('workspace-1', path); return { id: 'workspace-1', path, profile: 'ask' } },
    listWorkspaces: async () => [],
    setWorkspaceArchived: async (workspaceId, archived) => ({ id: workspaceId, path: workspacePaths.get(workspaceId) ?? '/synthetic/workspace', profile: 'ask', available: true, archived }),
    setWorkspaceProfile: async (workspaceId, profile) => ({ id: workspaceId, path: workspacePaths.get(workspaceId) ?? '/synthetic/workspace', profile }),
    listProviderProfiles: async () => [],
    queryHTTPModelCatalog: async query => ({ backendId: query.profileId, source: '', destination: '', profileRevision: '', searchTerm: query.searchTerm, models: [], nextCursor: '', checkedAt: sessionMetadata.createdAt, status: 'unsupported', complete: false, accountFiltered: false, errorCode: 'catalog_unsupported' }),
    queryCLIModelCatalog: async query => ({ backendId: query.backendId, source: '', destination: '', profileRevision: '', searchTerm: '', models: [], nextCursor: '', checkedAt: sessionMetadata.createdAt, status: 'unsupported', complete: false, accountFiltered: false, errorCode: 'catalog_unsupported' }),
    getOpenRouterManagementKeyStatus: async profileId => ({ profileId, origin: '', configured: false, usable: false, updatedAt: sessionMetadata.createdAt }),
    saveOpenRouterManagementKey: async () => { throw new Error('Synthetic management key unavailable') },
    clearOpenRouterManagementKey: async () => { throw new Error('Synthetic management key unavailable') },
    probeCredentialStore: async () => ({ status: 'unconfigured', checked: 0, missing: 0 }),
    getSettings: async () => ({ defaultBackendId: '', defaultModelBackendId: '', defaultModelId: '' }),
    saveSettings: async input => input,
    listLogEvents: async () => [],
    inspectRepository: async () => ({ isRepository: false, root: '', branch: '', files: [], stagedDiff: '', unstagedDiff: '', truncated: false }),
    listAllWorktrees: async () => [],
    listChatProjects: async () => [],
    setSessionPinned: async () => undefined,
    revealWorkspace: async () => undefined,
    startTerminal: async (_workspaceId, _cols, _rows) => ({ id: 'term-1', shell: '/bin/zsh', path: '/synthetic/workspace' }),
    writeTerminal: async () => undefined,
    resizeTerminal: async () => undefined,
    closeTerminal: async () => undefined,
    onTerminalOutput: () => () => undefined,
    getProjectMemory: async workspaceId => ({ workspaceId, status: 'declined', content: '', sources: [], backendId: '', modelId: '', edited: false, updatedAt: '0001-01-01T00:00:00Z' }),
    declineProjectMemory: async workspaceId => ({ workspaceId, status: 'declined', content: '', sources: [], backendId: '', modelId: '', edited: false, updatedAt: '2026-10-02T12:00:00Z' }),
    refreshProjectMemory: async workspaceId => ({ workspaceId, status: 'reading', content: '', sources: [], backendId: '', modelId: '', edited: false, updatedAt: '2026-10-02T12:00:00Z' }),
    saveProjectMemory: async (workspaceId, content) => ({ workspaceId, status: 'ready', content, sources: [], backendId: '', modelId: '', edited: true, updatedAt: '2026-10-02T12:00:00Z' }),
    listWorktrees: async () => ({ isRepository: false, root: '', base: '', baseKnown: false, currentPath: '', items: [] }),
    deleteWorktree: async () => { throw new Error('Synthetic worktrees unavailable') },
    pruneWorktrees: async () => ({ isRepository: false, root: '', base: '', baseKnown: false, currentPath: '', items: [] }),
    prepareWorktreeSave: async () => { throw new Error('Synthetic worktrees unavailable') },
    createPipeline: async () => { throw new Error('Synthetic pipeline unavailable') },
    createAuthoringPipeline: async () => { throw new Error('Synthetic pipeline unavailable') },
    openPipelineDesign: async () => { throw new Error('Synthetic document workspace unavailable') },
    preparePipelineDesign: async () => { throw new Error('Synthetic document generation unavailable') },
    editPipelineDesignDocument: async () => { throw new Error('Synthetic document editing unavailable') },
    restorePipelineDesignDocument: async () => { throw new Error('Synthetic document history unavailable') },
    cancelPipelineDesign: async () => { throw new Error('Synthetic document cancellation unavailable') },
    approvePipelineDesign: async () => { throw new Error('Synthetic document approval unavailable') },
    deriveAuthoringPipeline: async () => { throw new Error('Synthetic pipeline unavailable') },
    reviseAuthoringDiscovery: async () => { throw new Error('Synthetic pipeline unavailable') },
    startBrainstorming: async () => { throw new Error('Synthetic brainstorm unavailable') },
    getBrainstorming: async () => { throw Object.assign(new Error('No brainstorm'), { cause: { code: 'brainstorm_not_found' } }) },
    listBrainstorming: async () => [],
    answerBrainstormQuestion: async () => { throw new Error('Synthetic brainstorm unavailable') },
    generateBrainstormQuestion: async () => { throw new Error('Synthetic brainstorm unavailable') },
    questionsSufficient: async () => { throw new Error('Synthetic brainstorm unavailable') },
    finishAndGenerateSynthesis: async () => { throw new Error('Synthetic brainstorm unavailable') },
    approveBrainstormSynthesis: async () => { throw new Error('Synthetic brainstorm unavailable') },
    requestBrainstormRevision: async () => { throw new Error('Synthetic brainstorm unavailable') },
    skipBrainstormQuestions: async () => { throw new Error('Synthetic brainstorm unavailable') },
    confirmDiscoveryAfterSkip: async () => { throw new Error('Synthetic brainstorm unavailable') },
    cancelBrainstormAttempt: async () => { throw new Error('Synthetic brainstorm unavailable') },
    resumePausedBrainstorm: async () => { throw new Error('Synthetic brainstorm unavailable') },
    getAuthoringStage: async () => { throw new Error('Synthetic authoring stage unavailable') },
    listStageExecutors: async workspaceId => stageOrder.flatMap(stage => stageExecutors.get(`${workspaceId}:${stage}`) ?? []),
    saveStageExecutor: async input => {
      const saved: StageExecutor = { ...input, updatedAt: '2026-10-02T10:00:00Z' }
      stageExecutors.set(`${input.workspaceId}:${input.stage}`, saved)
      return saved
    },
    clearStageExecutor: async (workspaceId, stage) => { stageExecutors.delete(`${workspaceId}:${stage}`) },
    getAuthoringStageModelPreference,
    saveAuthoringStageModelPreference,
    generateAuthoringStage: async () => { throw new Error('Synthetic authoring stage unavailable') },
    requestAuthoringStageRevision: async () => { throw new Error('Synthetic authoring stage unavailable') },
    approveAuthoringStage: async () => { throw new Error('Synthetic authoring stage unavailable') },
    skipAuthoringStage: async () => { throw new Error('Synthetic authoring stage unavailable') },
    cancelAuthoringStage: async () => { throw new Error('Synthetic authoring stage unavailable') },
    startAuthoringCode: async () => { throw new Error('Synthetic authoring Code unavailable') },
    cancelAuthoringCode: async () => { throw new Error('Synthetic authoring Code unavailable') },
    getAuthoringCodeRuns: async input => ({ pipelineId: input.pipelineId, runs: [] }),
    preflightAuthoringCode: async () => { throw new Error('Synthetic authoring Code preflight unavailable') },
    prepareAuthoringCode: async () => { throw new Error('Synthetic authoring Code copy unavailable') },
    getAuthoringCodeCopyPreparations: async input => ({ pipelineId: input.pipelineId, attemptCount: 0, maxAttemptCount: 3, attempts: [] }),
    getAuthoringCodeRunPatch: async () => { throw new Error('Synthetic authoring Code patch unavailable') },
    listPipelines: async () => [],
    checkForUpdate: async () => ({ currentVersion: '0.2.2', available: false, version: '', pageUrl: '', canInstall: false }),
    installUpdate: async () => undefined,
    onUpdateState: () => () => undefined,
    continueWorkChat: async () => false,
    listWorkCoordinators: async () => [],
    ensureWorkChats: async () => ({ coordinators: [], created: 0 }),
    getPipelineStageActivity: async (pipelineId, stage) => ({ pipelineId, workspaceId: 'workspace-1', stage, status: 'pending', phase: '', sessionId: '', modelId: '', updatedAt: sessionMetadata.updatedAt }),
    getPipeline: async () => { throw new Error('Synthetic pipeline unavailable') },
    savePipelineArtifact: async () => { throw new Error('Synthetic pipeline unavailable') },
    advancePipeline: async () => { throw new Error('Synthetic pipeline unavailable') },
    skipPipelineStage: async () => { throw new Error('Synthetic pipeline unavailable') },
    previewPipelineCodeWorkspace: async () => ({ isGit: true, fileCount: 2, totalBytes: 128, excludedPaths: ['.git'], unsafePaths: [] }),
    createPipelineSession: async () => { throw new Error('Synthetic pipeline unavailable') },
    getPipelineForSession: async () => { throw Object.assign(new Error('No linked pipeline'), { cause: { code: 'pipeline_not_found' } }) },
    completePipelineCode: async () => { throw new Error('Synthetic pipeline unavailable') },
    completePipelineEvaluation: async () => { throw new Error('Synthetic pipeline unavailable') },
    decidePipelineExecutionArtifact: async () => { throw new Error('Synthetic pipeline review unavailable') },
    startPipelineQA: async () => { throw new Error('Synthetic QA unavailable') },
    fixPipelineFindings: async () => { throw new Error('Synthetic QA unavailable') },
    resumePipelineFixes: async () => { throw new Error('Synthetic QA unavailable') },
    listPipelinePullRequests: async () => [],
    setPullRequestWatch: async () => { throw new Error('Synthetic pull request unavailable') },
    checkPullRequestNow: async () => { throw new Error('Synthetic pull request unavailable') },
    onPullRequestsChange: () => () => undefined,
    getPipelineQALoop: async pipelineId => ({ pipelineId, phase: '', round: 0, message: '', updatedAt: sessionMetadata.updatedAt, running: false }),
    reopenPipelineCodeReview: async () => { throw new Error('Synthetic pipeline recovery unavailable') },
    applyPipelineCode: async () => { throw new Error('Synthetic pipeline unavailable') },
    finishPipelinePRs: async () => { throw new Error('Synthetic pipeline unavailable') },
    saveAgent: async () => { throw new Error('Synthetic agent unavailable') },
    listAgents: async () => [],
    delegateToAgent: async () => { throw new Error('Synthetic delegation unavailable') },
    listDelegations: async () => [],
    getParentDelegation: async () => null,
    saveWorkflow: async () => { throw new Error('Synthetic workflow unavailable') },
    listWorkflows: async () => [],
    startWorkflow: async () => { throw new Error('Synthetic workflow unavailable') },
    getWorkflowRun: async () => { throw new Error('Synthetic workflow unavailable') },
    listWorkflowRuns: async () => [],
    runWorkflowStep: async () => { throw new Error('Synthetic workflow unavailable') },
    resumeWorkflowRun: async () => { throw new Error('Synthetic workflow recovery unavailable') },
    cancelWorkflowRun: async () => { throw new Error('Synthetic workflow unavailable') },
    saveSchedule: async () => { throw new Error('Synthetic schedule unavailable') },
    listSchedules: async () => [],
    setSchedulePaused: async () => { throw new Error('Synthetic schedule unavailable') },
    runScheduleNow: async () => { throw new Error('Synthetic schedule unavailable') },
    listScheduleJobs: async () => [],
    cancelScheduleJob: async () => { throw new Error('Synthetic schedule job unavailable') },
    onScheduleChange: () => () => undefined,
    onPipelineCreated: () => () => undefined,
    saveSkill: async () => { throw new Error('Synthetic skill unavailable') },
    importSkill: async () => { throw new Error('Synthetic skill import unavailable') },
    listSkills: async () => [],
    importKnowledge: async () => { throw new Error('Synthetic knowledge unavailable') },
    reindexKnowledge: async () => { throw new Error('Synthetic knowledge unavailable') },
    listKnowledge: async () => [],
    searchKnowledge: async () => [],
    searchKnowledgeDetailed: async () => ({ mode: 'textual', hits: [] }),
    getKnowledgeEmbeddingProfile: async () => ({ configured: false, kind: '', baseUrl: '', model: '', fingerprint: '', progress: { total: 0, indexed: 0, dimension: 0 } }),
    saveKnowledgeEmbeddingProfile: async () => { throw new Error('Synthetic embedding profile unavailable') },
    indexKnowledgeVectors: async () => { throw new Error('Synthetic vector index unavailable') },
    removeKnowledge: async () => { throw new Error('Synthetic knowledge unavailable') },
    saveLocalChannel: async () => { throw new Error('Synthetic channel unavailable') },
    listLocalChannels: async () => [],
    importChannelInbox: async () => { throw new Error('Synthetic channel unavailable') },
    listChannelMessages: async () => [],
    sendChannelMessage: async () => { throw new Error('Synthetic channel unavailable') },
    retryChannelMessage: async () => { throw new Error('Synthetic channel unavailable') },
    saveMCPServer: async () => { throw new Error('Synthetic MCP unavailable') },
    listMCPServers: async () => [],
    connectMCPServer: async () => { throw new Error('Synthetic MCP unavailable') },
    disableMCPServer: async () => { throw new Error('Synthetic MCP unavailable') },
    pickDirectory: async () => '',
    pickSkillFile: async () => '',
    pickKnowledgeFile: async () => '',
    listBackends: async () => [{ id: 'local', name: 'Local', kind: 'api' as const, available: true }, { id: 'opencode', name: 'opencode', kind: 'cli' as const, available: false }],
    saveProviderProfile: async input => ({ id: input.id, name: input.name, kind: 'api' as const, available: true }),
    createSession: async (workspaceId, backendId) => ({ id: 'session-1', workspaceId, backendId, status: 'ready', ...sessionMetadata }),
    createDirectSession: async input => backend.createSession(input.workspaceId, input.backendId, input.agentId),
    getSessionModelSelection: async sessionId => ({ sessionId, backendId: 'codex', modelId: 'runtime-model', reasoningEffort: '', source: 'codex_app_server', destination: '', status: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 0, contextLength: 0, checkedAt: sessionMetadata.createdAt }),
    listSessions: async () => [],
    openSession: async (id, workspaceId) => ({ id, workspaceId, backendId: 'local', status: 'completed', ...sessionMetadata }),
    exportAudit: async (_sessionId, destination) => destination,
    prompt: async (_session, text): Promise<RunResult> => {
      const callID = `call-${journal.length}`
      if (text.includes('arquivo inexistente')) {
        // The model reads a file that is not there yet; the tool reports it, the run goes on and completes.
        emit('run.started')
        emit('message.user', { role: 'user', content: text })
        emit('assistant.delta', { delta: 'Vou ler o arquivo antes de criá-lo.' })
        emit('message.assistant', { role: 'assistant', content: 'Vou ler o arquivo antes de criá-lo.', toolCalls: [{ id: callID, name: 'read', arguments: { path: 'index.html' } }] })
        emit('tool.called', { toolCallId: callID, name: 'read' })
        emit('tool.failed', { toolCallId: callID, name: 'read', errorCode: 'not_found', error: '"index.html" does not exist', recoverable: true, content: null })
        emit('assistant.delta', { delta: 'Como ele não existe, vou criá-lo.' })
        emit('message.assistant', { role: 'assistant', content: 'Como ele não existe, vou criá-lo.' })
        emit('run.completed', { reason: '' })
        return { status: 'completed' }
      }
      emit('run.started')
      emit('message.user', { role: 'user', content: text })
      emit('assistant.delta', { delta: 'Vou escrever as notas.' })
      emit('message.assistant', { role: 'assistant', content: 'Vou escrever as notas.', toolCalls: [writeCall(callID)] })
      pendingApproval = `approval-${callID}`
      const approval = { approvalId: pendingApproval, toolCallId: callID, name: 'write', risk: 'write', arguments: writeCall(callID).arguments }
      emit('approval.requested', approval)
      return { status: 'awaiting_approval', approval }
    },
    approve: async (_session, approvalId, allow): Promise<RunResult> => {
      const toolCallId = approvalId.replace('approval-', '')
      if (!allow) {
        emit('approval.denied', { approvalId, toolCallId })
        emit('tool.skipped', { toolCallId, name: 'write', reason: 'approval_denied', error: 'tool call not executed: approval_denied' })
        emit('run.failed', { reason: 'approval_denied' })
        return { status: 'failed', reason: 'approval_denied' }
      }
      emit('approval.approved', { approvalId, toolCallId })
      emit('tool.called', { toolCallId, name: 'write' })
      emit('tool.completed', { toolCallId, name: 'write', content: { text: 'Wrote 4 bytes to notes.md' }, details: { path: 'notes.md', diff } })
      emit('assistant.delta', { delta: 'Pronto.' })
      emit('message.assistant', { role: 'assistant', content: 'Pronto.' })
      emit('run.completed', { reason: '' })
      return { status: 'completed' }
    },
    cancel: async () => undefined,
    listEvents: async (_session, after) => journal.filter(event => event.sequence > after),
    onEvent: listener => { listeners.add(listener); return () => listeners.delete(listener) },
  }
  return { backend, journal, pendingApproval: () => pendingApproval, respondReadOnly }
}
