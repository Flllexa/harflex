import React from 'react'
import ReactDOM from 'react-dom/client'
import App from '../app/App'
import '../styles/global.css'
import { createFakeBackend } from './fakeBackend'
import { createApprovalReplayBackend, createReplayBackend } from './replayBackend'
import { createFoundationBackend } from './foundationBackend'
import { installAuthoringStageFixture } from './authoringStageFixture'
import { installPipelineDesignFixture } from './pipelineDesignFixture'
import { installWorktrees } from './worktreeFixture'
import { installShowcase, showcaseDocuments, type ShowcaseView } from './showcaseFixture'
import type { Agent, AgentEvent, Brainstorm, ChannelMessage, Delegation, KnowledgeDocument, KnowledgeEmbeddingProfile, LocalChannel, MCPServer, Pipeline, PipelineStage, ProviderProfile, Schedule, ScheduleJob, Skill, Workflow, WorkflowRun } from '../lib/backend'

const scenario = new URLSearchParams(window.location.search).get('scenario')
const foundation = scenario === 'foundation' || scenario === 'foundation-readonly' || scenario === 'foundation-replay-recovery'
  ? createFoundationBackend({ readOnly: scenario === 'foundation-readonly', replayOutage: scenario === 'foundation-replay-recovery' }) : undefined
if (foundation) {
  window.harflexSynthetic = { releaseStreaming: () => foundation.releaseStreaming() }
  const dispose = () => { foundation.dispose(); delete window.harflexSynthetic }
  window.addEventListener('pagehide', dispose, { once: true })
  import.meta.hot?.dispose(dispose)
}
const fake = createFakeBackend()
const backend = foundation ?? (scenario === 'approval-cancel' ? createApprovalReplayBackend()
  : scenario === 'replay' || scenario === 'replay-outage' ? createReplayBackend(scenario === 'replay-outage') : fake.backend)
if (scenario === 'conversational-design') installPipelineDesignFixture(backend, sessionStorage, undefined, new URLSearchParams(window.location.search).get('demo') === 'todo' ? showcaseDocuments : undefined)
if (scenario === 'showcase') installShowcase(backend, (new URLSearchParams(window.location.search).get('view') ?? 'code') as ShowcaseView)
if (scenario === 'approval-cancel') {
  const cancel = backend.cancel
  const prompt = backend.prompt
  backend.prompt = async (sessionId, text) => {
    const calls = JSON.parse(sessionStorage.getItem('harflex:approval-prompt-count') ?? '[]') as string[]
    calls.push(`${sessionId}:${text}`)
    sessionStorage.setItem('harflex:approval-prompt-count', JSON.stringify(calls))
    return prompt(sessionId, text)
  }
  backend.cancel = async sessionId => {
    sessionStorage.setItem('harflex:approval-cancel-called', sessionId)
    await cancel(sessionId)
  }
}
if (scenario === 'cli-models') {
  backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  backend.queryCLIModelCatalog = async query => ({ backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'fixture-revision', searchTerm: '',
    models: [{ id: 'runtime-model', displayName: 'Modelo dinâmico com nome longo para conferir truncamento responsivo', backendId: query.backendId, source: 'codex_app_server', availability: 'listed', supportedReasoningEfforts: ['low', 'medium', 'high'], defaultReasoningEffort: 'medium' }],
    nextCursor: '', checkedAt: '2026-09-28T10:00:00Z', status: 'complete', complete: true, accountFiltered: true })
}
if (scenario === 'casual-history') {
  const date = '2026-09-29T10:00:00Z'
  const listDefaultBackends = backend.listBackends
  backend.listBackends = async () => [...await listDefaultBackends(), { id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  const sessions = [
    { id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'completed', resumable: true, createdAt: date, updatedAt: date },
    { id: 'session-failed', workspaceId: 'workspace-1', backendId: 'codex', status: 'failed', resumable: false, createdAt: date, updatedAt: '2026-09-29T09:00:00Z' },
  ] as const
  const journals: Record<string, AgentEvent[]> = {
    'session-1': [
      { id: 'session-1-start', streamId: 'session-1', sequence: 1, type: 'run.started', data: {}, createdAt: date },
      { id: 'session-1-user', streamId: 'session-1', sequence: 2, type: 'message.user', data: { role: 'user', content: 'Criar uma lista TODO em TypeScript' }, createdAt: date },
      { id: 'session-1-assistant', streamId: 'session-1', sequence: 3, type: 'message.assistant', data: { role: 'assistant', content: 'Vou organizar a tarefa em uma lista.' }, createdAt: date },
      { id: 'session-1-done', streamId: 'session-1', sequence: 4, type: 'run.completed', data: {}, createdAt: date },
    ],
    'session-failed': [
      { id: 'session-failed-start', streamId: 'session-failed', sequence: 1, type: 'external.run.started', data: { adapter: 'codex' }, createdAt: date },
      { id: 'session-failed-user', streamId: 'session-failed', sequence: 2, type: 'message.user', data: { role: 'user', content: 'Corrigir o bloqueio do fluxo Code' }, createdAt: date },
      { id: 'session-failed-terminal', streamId: 'session-failed', sequence: 3, type: 'external.run.failed', data: { reason: 'execution_failed' }, createdAt: date },
    ],
  }
  backend.listSessions = async workspaceId => sessions.filter(session => session.workspaceId === workspaceId)
  backend.openSession = async (id, workspaceId) => {
    const session = sessions.find(item => item.id === id)
    if (!session || session.workspaceId !== workspaceId) throw new Error('Session absent')
    return session
  }
  backend.listEvents = async (id, after = 0) => (journals[id] ?? []).filter(event => event.sequence > after)
}
if (scenario === 'casual-cli-recovery') {
  const date = new Date().toISOString()
  const failed = { id: 'session-failed', workspaceId: 'workspace-1', backendId: 'codex', status: 'failed', resumable: false, createdAt: date, updatedAt: date }
  const failedEvents: AgentEvent[] = [
    { id: 'failed-start', streamId: failed.id, sequence: 1, type: 'external.run.started', data: { adapter: 'codex' }, createdAt: date },
    { id: 'failed-user', streamId: failed.id, sequence: 2, type: 'message.user', data: { role: 'user', content: 'Corrigir o bloqueio do fluxo Code' }, createdAt: date },
    { id: 'failed-terminal', streamId: failed.id, sequence: 3, type: 'external.run.failed', data: { reason: 'execution_failed' }, createdAt: date },
  ]
  backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'gpt-5-codex' })
  backend.queryCLIModelCatalog = async query => ({
    backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'fixture-codex-revision', searchTerm: '',
    models: [{ id: 'codex-test-model', displayName: 'Modelo Codex de teste', backendId: query.backendId, source: 'codex_app_server', availability: 'listed', supportedReasoningEfforts: ['medium'], defaultReasoningEffort: 'medium' }],
    nextCursor: '', checkedAt: date, status: 'complete', complete: true, accountFiltered: true,
  })
  backend.listSessions = async workspaceId => workspaceId === failed.workspaceId ? [failed] : []
  backend.openSession = async (id, workspaceId) => {
    if (id === failed.id && workspaceId === failed.workspaceId) return failed
    if (id === 'session-1' && workspaceId === failed.workspaceId) return { ...failed, id, status: 'completed', resumable: true }
    throw Object.assign(new Error('Session absent'), { cause: { code: 'session_not_found' } })
  }
  backend.listEvents = async (id, after = 0, limit = 1000) => {
    const events = id === failed.id ? failedEvents : id === 'session-1' ? fake.journal : []
    return events.filter(event => event.sequence > after).slice(0, limit)
  }
}
// A Codex chat whose run ended cannot be resumed; its saved default model is confirmed by the CLI's own catalog, so the next message
// can open a new conversation on the same agent, carrying this one.
if (scenario === 'casual-cli-continuation') {
  const date = new Date().toISOString()
  const closed = { id: 'session-failed', workspaceId: 'workspace-1', backendId: 'codex', status: 'failed', resumable: false, createdAt: date, updatedAt: date }
  const closedEvents: AgentEvent[] = [
    { id: 'closed-start', streamId: closed.id, sequence: 1, type: 'external.run.started', data: { adapter: 'codex' }, createdAt: date },
    { id: 'closed-user', streamId: closed.id, sequence: 2, type: 'message.user', data: { role: 'user', content: 'Corrigir o bloqueio do fluxo Code' }, createdAt: date },
    { id: 'closed-text', streamId: closed.id, sequence: 3, type: 'external.event', data: { type: 'text', text: 'Parei antes de mexer no fluxo.' }, createdAt: date },
    { id: 'closed-terminal', streamId: closed.id, sequence: 4, type: 'external.run.failed', data: { reason: 'execution_failed' }, createdAt: date },
  ]
  backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'gpt-5-codex' })
  backend.queryCLIModelCatalog = async query => ({
    backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'fixture-codex-revision', searchTerm: '',
    models: [{ id: 'gpt-5-codex', displayName: 'GPT-5 Codex', backendId: query.backendId, source: 'codex_app_server', availability: 'listed', supportedReasoningEfforts: ['medium'], defaultReasoningEffort: 'medium' }],
    nextCursor: '', checkedAt: date, status: 'complete', complete: true, accountFiltered: true,
  })
  backend.listSessions = async workspaceId => workspaceId === closed.workspaceId ? [closed] : []
  backend.openSession = async (id, workspaceId) => {
    if (id === closed.id && workspaceId === closed.workspaceId) return closed
    throw Object.assign(new Error('Session absent'), { cause: { code: 'session_not_found' } })
  }
  backend.listEvents = async (id, after = 0, limit = 1000) => (id === closed.id ? closedEvents : id === 'session-1' ? fake.journal : []).filter(event => event.sequence > after).slice(0, limit)
}
if (scenario === 'casual-cli-recovery-replay') {
  const date = new Date().toISOString()
  const failed = { id: 'session-failed', workspaceId: 'workspace-1', backendId: 'codex', status: 'failed', resumable: false, createdAt: date, updatedAt: date }
  const failedEvents: AgentEvent[] = [
    { id: 'failed-start', streamId: failed.id, sequence: 1, type: 'external.run.started', data: { adapter: 'codex' }, createdAt: date },
    { id: 'failed-user', streamId: failed.id, sequence: 2, type: 'message.user', data: { role: 'user', content: 'Recuperar o pedido durante a abertura do histórico' }, createdAt: date },
    { id: 'failed-terminal', streamId: failed.id, sequence: 3, type: 'external.run.failed', data: { reason: 'execution_failed' }, createdAt: date },
  ]
  let releaseReplay!: () => void
  const replayGate = new Promise<void>(resolve => { releaseReplay = resolve })
  const syntheticWindow = window as unknown as { harflexSynthetic?: { releaseReplay(): void } }
  syntheticWindow.harflexSynthetic = { releaseReplay }
  const dispose = () => { delete syntheticWindow.harflexSynthetic }
  window.addEventListener('pagehide', dispose, { once: true })
  import.meta.hot?.dispose(dispose)
  backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'gpt-5-codex' })
  backend.listSessions = async workspaceId => workspaceId === failed.workspaceId ? [failed] : []
  backend.openSession = async (id, workspaceId) => {
    if (id === failed.id && workspaceId === failed.workspaceId) return failed
    throw Object.assign(new Error('Session absent'), { cause: { code: 'session_not_found' } })
  }
  backend.listEvents = async (id, after = 0, limit = 1000) => {
    if (limit > 12) await replayGate
    return (id === failed.id ? failedEvents : []).filter(event => event.sequence > after).slice(0, limit)
  }
}
if (scenario === 'activity-live') {
  const date = new Date().toISOString()
  const running = { id: 'session-running', workspaceId: 'workspace-1', backendId: 'local', status: 'running' as const, resumable: true, createdAt: date, updatedAt: date }
  const events: AgentEvent[] = [
    { id: 'run-started', streamId: running.id, sequence: 1, type: 'run.started', data: {}, createdAt: date },
    { id: 'user-request', streamId: running.id, sequence: 2, type: 'message.user', data: { role: 'user', content: 'Implementar um arquivo de teste' }, createdAt: date },
    { id: 'assistant-tool', streamId: running.id, sequence: 3, type: 'message.assistant', data: { role: 'assistant', content: 'Vou escrever o módulo.', toolCalls: [{ id: 'write-1', name: 'write', arguments: { path: 'src/todo.js' } }] }, createdAt: date },
    { id: 'tool-called', streamId: running.id, sequence: 4, type: 'tool.called', data: { toolCallId: 'write-1', name: 'write' }, createdAt: date },
    { id: 'session-bound', streamId: running.id, sequence: 5, type: 'external.session.bound', data: { sessionId: 'thread-1' }, createdAt: date },
  ]
  backend.listSessions = async workspaceId => workspaceId === running.workspaceId ? [running] : []
  backend.openSession = async (id, workspaceId) => {
    if (id === running.id && workspaceId === running.workspaceId) return running
    throw Object.assign(new Error('Session absent'), { cause: { code: 'session_not_found' } })
  }
  backend.listEvents = async (id, after = 0, limit = 1000) => (id === running.id ? events : []).filter(event => event.sequence > after).slice(0, limit)
}
if (scenario === 'projects') {
  backend.listWorkspaces = async () => [
    { id: 'workspace-1', path: '/synthetic/workspace/api-faturas', profile: 'ask', available: true },
    { id: 'workspace-2', path: '/synthetic/workspace/sistema-de-pagamentos-com-nome-longo', profile: 'trusted_workspace', available: true },
    { id: 'workspace-3', path: '/synthetic/workspace/pasta-movida', profile: 'ask', available: false },
  ]
}
if (scenario === 'settings') {
  let profiles: ProviderProfile[] = [
    { id: 'local', name: 'Local', kind: 'openai_compatible', providerType: 'generic', baseUrl: 'http://127.0.0.1:1234/v1', model: 'modelo-local', hasCredential: true, endpointBlocked: false, updatedAt: '2026-09-25T10:00:00Z' },
    { id: 'router', name: 'Router', kind: 'openai_compatible', providerType: 'openrouter', baseUrl: 'https://openrouter.ai/api/v1', model: 'provider/model', hasCredential: true, endpointBlocked: false, updatedAt: '2026-09-25T10:00:00Z' },
  ]
  let settings = { defaultBackendId: '', defaultModelBackendId: '', defaultModelId: '' }
  let managementConfigured = false
  const managementStatus = () => ({ profileId: 'router', origin: 'https://openrouter.ai:443', configured: managementConfigured, usable: managementConfigured, updatedAt: '2026-09-27T10:00:00Z' })
  backend.listProviderProfiles = async () => profiles
  backend.getSettings = async () => settings
  backend.saveSettings = async input => { settings = input; return input }
  backend.getOpenRouterManagementKeyStatus = async () => managementStatus()
  backend.saveOpenRouterManagementKey = async () => { managementConfigured = true; return managementStatus() }
  backend.clearOpenRouterManagementKey = async () => { managementConfigured = false }
  backend.queryHTTPModelCatalog = async query => {
    if (query.profileId === 'router' && !query.draft) return {
      backendId: 'router', source: managementConfigured ? 'openrouter_account' : 'openrouter_general_unfiltered', destination: 'https://openrouter.ai:443', profileRevision: 'api:fixture', searchTerm: query.searchTerm,
      models: [{ id: 'provider/model', displayName: 'Modelo Router', backendId: 'router', source: managementConfigured ? 'openrouter_account' : 'openrouter_general_unfiltered', availability: 'listed' }],
      nextCursor: '', checkedAt: '2026-09-27T10:00:00Z', status: 'complete', complete: true, accountFiltered: managementConfigured,
    }
    if (query.draft?.providerType === 'lm_studio') return {
      backendId: query.profileId, source: 'lm_studio_native', destination: 'http://127.0.0.1:1234', profileRevision: 'api:fixture', searchTerm: query.searchTerm,
      models: [{ id: 'local/qwen', displayName: 'Qwen local', backendId: query.profileId, source: 'lm_studio_native', availability: 'listed', loaded: false }],
      nextCursor: '', checkedAt: '2026-09-27T10:00:00Z', status: 'complete', complete: true, accountFiltered: false,
    }
    return { backendId: query.profileId, source: '', destination: '', profileRevision: '', searchTerm: query.searchTerm, models: [], nextCursor: '', checkedAt: '2026-09-27T10:00:00Z', status: 'unsupported', complete: false, accountFiltered: false, errorCode: 'catalog_unsupported' }
  }
  backend.saveProviderProfile = async input => {
    const existing = profiles.find(item => item.id === input.id)
    const saved = { id: input.id, name: input.name, kind: 'openai_compatible', providerType: input.providerType, baseUrl: input.baseUrl, model: input.model, hasCredential: input.clearCredential ? false : !!input.apiKey || !!existing?.hasCredential, endpointBlocked: false, updatedAt: '2026-09-27T10:00:00Z' }
    profiles = [saved, ...profiles.filter(item => item.id !== input.id)]
    return { id: input.id, name: input.name, kind: 'api', available: true }
  }
}
if (scenario === 'logs') {
  const entries = [
    { cursor: 2, id: 'e2', sessionId: 'session-1', workspaceId: 'workspace-1', sequence: 2, type: 'run.failed', createdAt: '2026-09-25T10:01:00Z' },
    { cursor: 1, id: 'e1', sessionId: 'session-1', workspaceId: 'workspace-1', sequence: 1, type: 'run.started', createdAt: '2026-09-25T10:00:00Z' },
  ]
  backend.listLogEvents = async query => entries.filter(item => (!query.type || item.type === query.type) && (!query.workspaceId || item.workspaceId === query.workspaceId) && (!query.beforeId || item.cursor < query.beforeId)).slice(0, query.limit)
}
if (scenario === 'repositories') {
  backend.inspectRepository = async () => ({
    isRepository: true, root: '/synthetic/workspace/api-faturas', branch: 'feature/exportacao-de-faturas',
    files: ['M  internal/service/export.go', ' M internal/api/router.go', '?? docs/exportacao.md'],
    stagedDiff: 'diff --git a/internal/service/export.go b/internal/service/export.go\n@@ -1 +1 @@\n+func Exportar() {}',
    unstagedDiff: 'diff --git a/internal/api/router.go b/internal/api/router.go\n@@ -1 +1 @@\n+rotaExportacao()', truncated: false,
  })
}
if (scenario === 'worktrees') {
  // The simulated Go service judges every worktree against the project that is open. The assistant's run
  // commits and merges the worktree it is asked about, unless the URL says it stalls (`assistant=stalls`).
  const params = new URLSearchParams(window.location.search)
  const wt = installWorktrees(backend, undefined, { followProject: true })
  backend.prompt = async (_session, text) => {
    const target = wt.items().find(item => item.canSaveWithAi && text.includes(item.branch || item.name))
    if (target && params.get('assistant') !== 'stalls') wt.settle(target.path)
    return fake.respondReadOnly(text, 'Commitei o que estava pendente e mesclei em main.')
  }
  if (params.get('backends') === 'none') backend.listBackends = async () => [{ id: 'local', name: 'Local', kind: 'api' as const, available: false }]
}
if (scenario === 'pipelines') {
  const key = 'harflex:synthetic-pipeline'
  let pipeline: Pipeline | undefined = JSON.parse(sessionStorage.getItem(key) ?? 'null') ?? undefined
  const persist = (next: Pipeline) => { pipeline = next; sessionStorage.setItem(key, JSON.stringify(next)); return next }
  const stages: PipelineStage[] = ['discovery', 'spec', 'plan', 'code', 'eval']
  backend.listPipelines = async () => pipeline ? [pipeline] : []
  backend.getPipeline = async () => { if (!pipeline) throw new Error('Pipeline absent'); return pipeline }
  backend.createPipeline = async input => persist({ id: 'pipeline-1', workspaceId: input.workspaceId, title: input.title, objective: input.objective, currentStage: 'discovery', stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 1, artifacts: {}, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z' })
  backend.savePipelineArtifact = async (_id, stage, content) => { if (!pipeline) throw new Error('Pipeline absent'); return persist({ ...pipeline, revision: pipeline.revision + 1, artifacts: { ...pipeline.artifacts, [stage]: { stage, content, version: (pipeline.artifacts[stage]?.version ?? 0) + 1, updatedAt: pipeline.updatedAt } } }) }
  const transition = (action: 'completed' | 'skipped') => { if (!pipeline || !pipeline.currentStage) throw new Error('Pipeline absent'); const index = stages.indexOf(pipeline.currentStage), next = stages[index + 1] ?? ''; return persist({ ...pipeline, currentStage: next, revision: pipeline.revision + 1, stageStatus: { ...pipeline.stageStatus, [pipeline.currentStage]: action, ...(next ? { [next]: 'active' } : {}) } }) }
  backend.advancePipeline = async () => transition('completed')
  backend.skipPipelineStage = async () => transition('skipped')
}
if (scenario === 'sdd-authoring' || scenario === 'sdd-authoring-no-api') {
  const date = new Date().toISOString()
  const pipelineKey = 'harflex:synthetic-authoring-pipeline'
  const runKey = 'harflex:synthetic-brainstorm'
  const catalogQueryKey = 'harflex:synthetic-authoring-catalog-queries'
  let pipeline: Pipeline | undefined = JSON.parse(sessionStorage.getItem(pipelineKey) ?? 'null') ?? undefined
  let run: Brainstorm | undefined = JSON.parse(sessionStorage.getItem(runKey) ?? 'null') ?? undefined
  const savePipeline = (value: Pipeline) => { pipeline = value; sessionStorage.setItem(pipelineKey, JSON.stringify(value)); return value }
  const saveRun = (value: Brainstorm) => { run = value; sessionStorage.setItem(runKey, JSON.stringify(value)); return value }
  backend.listPipelines = async () => pipeline ? [pipeline] : []
  backend.getPipeline = async () => { if (!pipeline) throw new Error('Pipeline absent'); return pipeline }
  backend.createAuthoringPipeline = async input => savePipeline({ id: 'authoring-1', workspaceId: input.workspaceId, kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 0, title: 'Exportar faturas', objective: '', currentStage: 'discovery', stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 1, artifacts: { discovery: { stage: 'discovery', version: 1, content: input.discovery, author: 'user', sourceSessionId: '', updatedAt: date } }, createdAt: date, updatedAt: date })
  backend.listBackends = async () => [{ id: 'api-1', name: 'API de teste', kind: 'api', available: true }]
  const apiAvailable = scenario === 'sdd-authoring'
  backend.getSettings = async () => ({ defaultBackendId: apiAvailable ? 'api-1' : 'codex', defaultModelBackendId: apiAvailable ? 'api-1' : 'codex', defaultModelId: apiAvailable ? 'test-model' : 'gpt-test' })
  backend.listProviderProfiles = async () => apiAvailable ? [{ id: 'api-1', name: 'API de testes financeiros com identificação extensa', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://example.test/v1', model: 'Modelo configurado com nome extenso para validar largura', hasCredential: true, endpointBlocked: false, updatedAt: date }] : []
  backend.queryHTTPModelCatalog = async query => {
    sessionStorage.setItem(catalogQueryKey, String(Number(sessionStorage.getItem(catalogQueryKey) ?? '0') + 1))
    return { backendId: query.profileId, source: 'openai_models', destination: 'API de teste', profileRevision: 'revision-1', credentialToken: 'a'.repeat(64), searchTerm: '', models: [{ id: 'test-model', displayName: 'Modelo de teste com descrição longa para validar truncamento responsivo', backendId: query.profileId, source: 'openai_models', availability: 'listed', loaded: true, contextLength: 8192, supportedReasoningEfforts: ['low', 'high'] }], nextCursor: '', checkedAt: date, status: 'complete', complete: true, accountFiltered: true }
  }
  backend.getBrainstorming = async () => { if (run) return run; throw Object.assign(new Error('No brainstorm'), { cause: { code: 'brainstorm_not_found' } }) }
  backend.startBrainstorming = async input => saveRun({ id: 'brainstorm-1', pipelineId: input.pipelineId, pipelineRevision: 2, discoveryVersion: input.discoveryVersion, discoveryContent: pipeline!.artifacts.discovery!.content, selection: { backendId: input.selection.executor === 'api' ? input.selection.profileId : input.selection.backendId, modelId: input.selection.modelId, reasoningEffort: '', catalogRevision: input.selection.catalogRevision, source: input.selection.source, destination: input.selection.destination, status: 'listed', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 256, contextLength: 8192, checkedAt: input.selection.checkedAt }, state: 'ready', revision: 1, questionCount: 0, currentQuestionId: '', synthesisVersion: 0, attemptCount: 0, inputBudgetRemaining: 30000, outputBudgetRemaining: 30000, activeDurationMillis: 0, createdAt: date, updatedAt: date, attempts: [], turns: [], syntheses: [] })
  backend.generateBrainstormQuestion = async () => saveRun({ ...run!, state: 'waiting_answer', revision: run!.revision + 1, questionCount: 1, currentQuestionId: 'question-1', turns: [{ number: 1, questionId: 'question-1', question: 'Quem usa a exportação?', answer: '', sourceSessionId: 'session-1', status: 'waiting_answer', createdAt: date, updatedAt: date }] })
  backend.answerBrainstormQuestion = async input => saveRun({ ...run!, state: 'ready', revision: run!.revision + 1, turns: [{ ...run!.turns[0], answer: input.answer, status: 'answered' }] })
  backend.questionsSufficient = async () => saveRun({ ...run!, state: 'waiting_user', revision: run!.revision + 1, synthesisVersion: 1, syntheses: [{ version: 1, discoveryVersion: 1, content: { scope: 'Exportar faturas em CSV', decisions: ['Preservar filtros atuais'], openQuestions: ['Formato de datas'] }, sourceSessionId: 'session-2', status: 'completed', createdAt: date }] })
  backend.approveBrainstormSynthesis = async () => { const approved = saveRun({ ...run!, state: 'approved', revision: run!.revision + 1 }); savePipeline({ ...pipeline!, currentStage: 'spec', discoveryFrozenVersion: 1, revision: pipeline!.revision + 1, stageStatus: { ...pipeline!.stageStatus, discovery: 'completed', spec: 'active' } }); return approved }
  installAuthoringStageFixture(backend, () => pipeline!, savePipeline, () => run!)
  backend.deriveAuthoringPipeline = async input => {
    const child: Pipeline = { ...pipeline!, id: 'authoring-derived-1', derivedFromPipelineId: input.parentPipelineId, currentStage: 'discovery', discoveryFrozenVersion: 0, revision: 1, stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' }, artifacts: { discovery: { stage: 'discovery', version: 1, content: input.discovery, author: 'user', sourceSessionId: '', updatedAt: date } } }
    run = undefined; sessionStorage.removeItem(runKey)
    return savePipeline(child)
  }
}
// A project with no pipeline yet, two API profiles and Codex: the card where each phase gets its own provider and model.
if (scenario === 'phase-executors') {
  const checkedAt = new Date().toISOString()
  const profile = (id: string, name: string, model: string) => ({ id, name, kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model, hasCredential: true, endpointBlocked: false, updatedAt: checkedAt })
  backend.listPipelines = async () => []
  backend.getSettings = async () => ({ defaultBackendId: 'local', defaultModelBackendId: 'local', defaultModelId: 'api-model' })
  backend.listProviderProfiles = async () => [profile('local', 'Local API', 'api-model'), profile('large', 'API grande', 'large-default')]
  backend.listBackends = async () => [{ id: 'local', name: 'Local API', kind: 'api', available: true }, { id: 'large', name: 'API grande', kind: 'api', available: true }, { id: 'codex', name: 'Codex CLI', kind: 'cli', available: true, professionalAvailable: true }]
  backend.queryHTTPModelCatalog = async query => ({ backendId: query.profileId, source: 'openai_models', destination: query.profileId, profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: ['api-model', 'large-default', 'large-reasoning'].map(id => ({ id, displayName: id, backendId: query.profileId, source: 'openai_models', availability: 'available' })), nextCursor: '', checkedAt, status: 'complete', complete: true, accountFiltered: true })
  backend.queryCLIModelCatalog = async query => ({ backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'codex-revision', searchTerm: '',
    models: [{ id: 'codex-model', displayName: 'Modelo Codex', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }], nextCursor: '', checkedAt, status: 'complete', complete: true, accountFiltered: true })
  // `?unreadable=1`: the saved choices cannot be read at first, so the page has to say so and offer another try.
  // (StrictMode runs the page's effect twice on mount and drops the first answer, so the first two reads fail.)
  if (new URLSearchParams(window.location.search).get('unreadable') === '1') {
    const read = backend.listStageExecutors.bind(backend)
    let attempts = 0
    backend.listStageExecutors = async workspaceId => { if (++attempts <= 2) throw new Error('offline'); return read(workspaceId) }
  }
}
if (scenario === 'pipeline-code' || scenario === 'pipeline-code-cli' || scenario === 'pipeline-code-no-api') {
  const requestedStage = new URLSearchParams(window.location.search).get('stage') === 'eval' ? 'eval' : 'code'
  let pipeline: Pipeline = { id: 'pipeline-code', workspaceId: 'workspace-1', title: 'Exportar faturas', objective: 'Adicionar exportação CSV', currentStage: requestedStage,
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: requestedStage === 'code' ? 'active' : 'completed', eval: requestedStage === 'eval' ? 'active' : 'pending' }, revision: 4,
    artifacts: { spec: { stage: 'spec', version: 1, content: 'Critério: CSV correto', updatedAt: '2026-09-25T10:00:00Z' }, plan: { stage: 'plan', version: 1, content: 'Implementar endpoint e testes', updatedAt: '2026-09-25T10:00:00Z' } }, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z' }
  backend.listPipelines = async () => [pipeline]
  backend.getPipeline = async () => pipeline
  backend.getPipelineForSession = async () => pipeline
  const codexDefault = scenario === 'pipeline-code-cli' || scenario === 'pipeline-code-no-api'
  const noAPIProfile = scenario === 'pipeline-code-no-api'
  const checkedAt = new Date().toISOString()
  const digest = async (text: string) => {
    const result = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text))
    return Array.from(new Uint8Array(result), byte => byte.toString(16).padStart(2, '0')).join('')
  }
  backend.getSettings = async () => ({ defaultBackendId: codexDefault ? 'codex' : 'local', defaultModelBackendId: codexDefault ? 'codex' : 'local', defaultModelId: codexDefault ? 'codex-model' : 'api-model' })
  backend.listProviderProfiles = async () => noAPIProfile ? [] : [{ id: 'local', name: 'Local API', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://example.test/v1', model: 'api-model', hasCredential: true, endpointBlocked: false, updatedAt: checkedAt }]
  backend.queryHTTPModelCatalog = async query => ({ backendId: query.profileId, source: 'openai_models', destination: 'Local API', profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: [{ id: 'api-model', displayName: 'Modelo API', backendId: 'local', source: 'openai_models', availability: 'available' }], nextCursor: '', checkedAt, status: 'complete', complete: true, accountFiltered: true })
  backend.queryCLIModelCatalog = async query => ({ backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'codex-revision', searchTerm: '',
    models: [{ id: 'codex-model', displayName: 'Modelo Codex', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }], nextCursor: '', checkedAt, status: 'complete', complete: true, accountFiltered: true })
  let activeRole: 'coder' | 'evaluator' | 'publisher' = 'coder'
  const codePrompt = backend.prompt
  let stageSession: { stage: PipelineStage; id: string } | undefined
  // What the pipeline reports about the session of a stage, read from the very events the conversation shows.
  backend.getPipelineStageActivity = async (pipelineId, stage) => {
    const idle = { pipelineId, workspaceId: 'workspace-1', stage, status: 'ready', phase: '', sessionId: '', modelId: '', updatedAt: '2026-09-25T10:00:00Z' }
    if (!stageSession || stageSession.stage !== stage) return idle
    const lifecycle = ['run.started', 'run.completed', 'run.failed', 'run.cancelled', 'approval.requested', 'approval.approved', 'approval.denied']
    const last = [...await backend.listEvents(stageSession.id, 0)].reverse().find(event => lifecycle.includes(event.type))
    const status = !last ? 'ready' : last.type === 'run.completed' ? 'completed' : last.type === 'run.failed' ? 'failed' : last.type === 'run.cancelled' ? 'cancelled' : last.type === 'approval.requested' ? 'awaiting_approval' : 'running'
    return { ...idle, status, sessionId: stageSession.id }
  }
  // The conversation of a stage as the real backend reports it: with its purpose, and closed once its result is verified
  // (the Code is verified, the QA verdict is recorded). The PRs chat is an ordinary chat and is never closed.
  const stageConversation = (backendId: string) => ({
    id: 'session-1', workspaceId: 'workspace-1', backendId, status: 'ready',
    purpose: activeRole === 'coder' ? 'code' as const : activeRole === 'evaluator' ? 'evaluation' as const : 'chat' as const,
    title: activeRole === 'publisher' ? `PRs · ${pipeline.title}` : pipeline.title,
    resumable: !(activeRole === 'coder' && pipeline.stageStatus.code !== 'active') && !(activeRole === 'evaluator' && pipeline.stageStatus.eval !== 'active'),
    createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z',
  })
  backend.openSession = async () => stageConversation('local')
  backend.createPipelineSession = async (_pipelineId, _backendId, role, selection) => {
    if (!selection && role !== 'publisher') throw new Error('Selecione um modelo para esta fase')
    activeRole = role
    if (role === 'publisher' && pipeline.codePatchPending) throw { cause: { code: 'pipeline_code_not_applied' } }
    stageSession = { stage: role === 'coder' ? 'code' : role === 'evaluator' ? 'eval' : 'prs', id: 'session-1' }
    return { session: stageConversation(_backendId), prompt: role === 'coder' ? 'Implementar CSV com testes' : role === 'evaluator' ? 'Avaliar a SPEC e o diff aprovado' : 'Abrir o pull request deste trabalho', role }
  }
  const prReport = '## Pull requests\n- https://github.com/acme/api-faturas/pull/12 · harflex/exportar-csv → main · Exporta faturas em CSV.'
  backend.prompt = async (sessionId, text) => activeRole === 'evaluator'
    ? fake.respondReadOnly(text, JSON.stringify({ passed: true, findings: [], criteria: [{ criterion: 'CSV correto', evidence: 'olá' }] }))
    : activeRole === 'publisher' ? fake.respondReadOnly(text, prReport) : codePrompt(sessionId, text)
  backend.completePipelineCode = async pipelineId => {
    if (pipelineId !== pipeline.id || pipeline.currentStage !== 'code') throw new Error('Pipeline Code indisponível')
    const events = await backend.listEvents('session-1', 0)
    const completed = [...events].reverse().find(event => ['run.completed', 'run.failed', 'run.cancelled'].includes(event.type))
    const approvedWrite = events.some(event => event.type === 'approval.approved') && events.some(event => event.type === 'tool.completed' && (event.data as { name?: string }).name === 'write')
    if (completed?.type !== 'run.completed' || !approvedWrite) throw { cause: { code: 'evidence_required' } }
    const content = events.flatMap(event => event.type === 'tool.completed' ? [(event.data as { details?: { diff?: string } }).details?.diff ?? ''] : []).filter(Boolean).join('\n')
    const version = (pipeline.artifacts.code?.version ?? 0) + 1
    pipeline = { ...pipeline, currentStage: 'code', stageStatus: { ...pipeline.stageStatus, code: 'waiting_user', eval: 'pending' }, artifacts: { ...pipeline.artifacts, code: { stage: 'code', version, content, contentDigest: await digest(content), author: 'ai', sourceSessionId: 'session-1', updatedAt: new Date().toISOString() } }, revision: pipeline.revision + 1, updatedAt: new Date().toISOString() }
    return pipeline
  }
  backend.completePipelineEvaluation = async pipelineId => {
    if (pipelineId !== pipeline.id || pipeline.currentStage !== 'eval') throw new Error('Pipeline Eval indisponível')
    const events = await backend.listEvents('session-1', 0)
    let latestRun = -1
    for (let index = events.length - 1; index >= 0; index--) {
      if (events[index].type === 'run.started') { latestRun = index; break }
    }
    const evaluation = events.slice(latestRun)
    const completed = evaluation[evaluation.length - 1]?.type === 'run.completed'
    const readOnly = evaluation.some(event => event.type === 'message.assistant') && !evaluation.some(event => ['approval.requested', 'tool.called', 'tool.completed'].includes(event.type))
    if (!completed || !readOnly) throw { cause: { code: 'evidence_required' } }
    const content = JSON.stringify({ passed: true, findings: [], criteria: [{ criterion: 'CSV correto', evidence: 'olá' }], criteriaSource: { sourceStage: 'spec', sourceVersion: 1, sourceDigest: await digest(pipeline.artifacts.spec?.content ?? ''), discoveryVersion: 1, bypasses: [] } })
    const version = (pipeline.artifacts.eval?.version ?? 0) + 1
    pipeline = { ...pipeline, currentStage: 'eval', stageStatus: { ...pipeline.stageStatus, eval: 'waiting_user' }, artifacts: { ...pipeline.artifacts, eval: { stage: 'eval', version, content, contentDigest: await digest(content), author: 'ai', sourceSessionId: 'session-1', updatedAt: new Date().toISOString() } }, revision: pipeline.revision + 1, updatedAt: new Date().toISOString() }
    return pipeline
  }
  backend.decidePipelineExecutionArtifact = async input => {
    const artifact = pipeline.artifacts[input.stage]
    if (input.pipelineId !== pipeline.id || input.pipelineRevision !== pipeline.revision || pipeline.currentStage !== input.stage || pipeline.stageStatus[input.stage] !== 'waiting_user' ||
      !artifact || artifact.version !== input.artifactVersion || artifact.contentDigest !== input.artifactDigest || input.decision === 'request_revision' && !input.feedback.trim()) throw new Error('Decisão fora da versão atual')
    if (input.stage === 'eval' && input.decision === 'approve' && JSON.parse(artifact.content).passed !== true) throw new Error('Eval reprovada não pode ser aprovada')
    const review = { stage: input.stage, version: artifact.version, content: artifact.content, contentDigest: artifact.contentDigest!, sourceSessionId: artifact.sourceSessionId ?? '', decision: input.decision, actor: 'local_user', feedback: input.feedback, createdAt: new Date().toISOString() }
    let currentStage: Pipeline['currentStage']
    const stageStatus = { ...pipeline.stageStatus }
    if (input.decision === 'request_revision') {
      currentStage = 'code'; stageStatus.code = 'active'; stageStatus.eval = input.stage === 'code' ? 'pending' : 'failed'
    } else if (input.stage === 'code') {
      currentStage = 'eval'; stageStatus.code = 'completed'; stageStatus.eval = 'active'
    } else {
      currentStage = 'prs'; stageStatus.code = 'completed'; stageStatus.eval = 'completed'; stageStatus.prs = 'active'
    }
    pipeline = { ...pipeline, currentStage, stageStatus, codePatchPending: input.decision === 'approve' && input.stage === 'eval' ? true : pipeline.codePatchPending, executionReviews: [...(pipeline.executionReviews ?? []), review], revision: pipeline.revision + 1, updatedAt: new Date().toISOString() }
    return pipeline
  }
  backend.applyPipelineCode = async pipelineId => {
    if (pipelineId !== pipeline.id || (pipeline.currentStage !== '' && pipeline.currentStage !== 'prs') || pipeline.stageStatus.eval !== 'completed' || !pipeline.artifacts.code ||
      !(pipeline.executionReviews ?? []).some(item => item.stage === 'code' && item.version === pipeline.artifacts.code?.version && item.decision === 'approve') ||
      !(pipeline.executionReviews ?? []).some(item => item.stage === 'eval' && item.version === pipeline.artifacts.eval?.version && item.decision === 'approve')) throw new Error('O patch só pode ser aplicado após Code e Eval aprovadas')
    pipeline = { ...pipeline, codeAppliedAt: new Date().toISOString(), codePatchPending: false }
    return pipeline
  }
  backend.finishPipelinePRs = async (pipelineId, outcome, reason) => {
    if (pipelineId !== pipeline.id || pipeline.currentStage !== 'prs' || pipeline.stageStatus.prs !== 'active') throw new Error('PRs indisponíveis')
    const stageStatus = { ...pipeline.stageStatus, prs: outcome }
    let artifacts = pipeline.artifacts
    if (outcome === 'completed') {
      const events = await backend.listEvents('session-1', 0)
      const last = [...events].reverse().find(event => ['run.started', 'run.completed', 'run.failed', 'run.cancelled'].includes(event.type))
      const answer = [...events].reverse().find(event => event.type === 'message.assistant')?.data as { content?: string } | undefined
      if (activeRole !== 'publisher' || last?.type !== 'run.completed' || !answer?.content) throw { cause: { code: 'evidence_required' } }
      artifacts = { ...artifacts, prs: { stage: 'prs', version: 1, content: answer.content, author: 'ai', sourceSessionId: 'session-1', updatedAt: new Date().toISOString() } }
    }
    void reason
    pipeline = { ...pipeline, currentStage: '', stageStatus, artifacts, revision: pipeline.revision + 1, updatedAt: new Date().toISOString() }
    return pipeline
  }
  backend.listMCPServers = async () => [{ id: 'mcp-github-1', workspaceId: 'workspace-1', name: 'GitHub', transport: 'http', command: '', args: [], url: 'https://api.githubcopilot.com/mcp/', tokenEnvVar: '', authScheme: 'bearer', enabled: true, tools: [], hasCredential: true, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z' }]
  if (scenario === 'pipeline-code-cli') backend.previewPipelineCodeWorkspace = async () => ({ isGit: false, fileCount: 4, totalBytes: 1200, excludedPaths: ['.git'], unsafePaths: [] })
  if (noAPIProfile) backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  if (scenario === 'pipeline-code-cli') backend.listBackends = async () => [
    { id: 'codex', name: 'Codex CLI', kind: 'cli', available: true },
    { id: 'local', name: 'Local API', kind: 'api', available: true },
  ]
}
if (scenario === 'pipeline-code-legacy') {
  const date = new Date().toISOString()
  let pipeline: Pipeline = { id: 'pipeline-code-legacy', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 1,
    title: 'Exportar faturas', objective: 'Adicionar exportação CSV', currentStage: '',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed' }, revision: 18, codeReviewRecoveryStatus: 'available',
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Exportar CSV sem perder dados.', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'Critério: CSV correto', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Implementar endpoint e testes', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date },
      code: { stage: 'code', version: 2, content: 'diff --git a/index.html b/index.html\n+<h1>TODO</h1>', contentDigest: 'e'.repeat(64), author: 'ai', sourceSessionId: 'coder-session', updatedAt: date },
      eval: { stage: 'eval', version: 1, content: 'Avaliação anterior sem recibo', contentDigest: 'f'.repeat(64), author: 'ai', sourceSessionId: 'evaluator-session', updatedAt: date } }, executionReviews: [], createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [pipeline]
  backend.getPipeline = async () => pipeline
  backend.getPipelineForSession = async () => pipeline
  backend.getSettings = async () => ({ defaultBackendId: 'local', defaultModelBackendId: 'local', defaultModelId: 'api-model' })
  backend.listProviderProfiles = async () => [{ id: 'local', name: 'Local API', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://example.test/v1', model: 'api-model', hasCredential: true, endpointBlocked: false, updatedAt: date }]
  backend.queryHTTPModelCatalog = async query => ({ backendId: query.profileId, source: 'openai_models', destination: 'Local API', profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: [{ id: 'api-model', displayName: 'Modelo API', backendId: 'local', source: 'openai_models', availability: 'available' }], nextCursor: '', checkedAt: date, status: 'complete', complete: true, accountFiltered: true })
  backend.listBackends = async () => [{ id: 'local', name: 'Local API', kind: 'api', available: true }]
  backend.reopenPipelineCodeReview = async input => {
    if (input.pipelineId !== pipeline.id || input.artifactVersion !== pipeline.artifacts.code?.version || input.artifactDigest !== pipeline.artifacts.code?.contentDigest || input.pipelineRevision !== pipeline.revision) throw new Error('Versão antiga do Code')
    pipeline = { ...pipeline, currentStage: 'code', revision: pipeline.revision + 1, stageStatus: { ...pipeline.stageStatus, code: 'waiting_user', eval: 'pending' }, artifacts: { ...pipeline.artifacts, eval: { ...pipeline.artifacts.eval!, reviewStatus: 'stale' } }, updatedAt: new Date().toISOString() }
    return pipeline
  }
  backend.decidePipelineExecutionArtifact = async input => {
    const artifact = pipeline.artifacts[input.stage]
    if (input.pipelineId !== pipeline.id || input.pipelineRevision !== pipeline.revision || pipeline.currentStage !== input.stage || pipeline.stageStatus[input.stage] !== 'waiting_user' ||
      !artifact || artifact.version !== input.artifactVersion || artifact.contentDigest !== input.artifactDigest || input.decision !== 'approve') throw new Error('Decisão fora da versão atual')
    const review = { stage: input.stage, version: artifact.version, content: artifact.content, contentDigest: artifact.contentDigest!, sourceSessionId: artifact.sourceSessionId ?? '', decision: 'approve' as const, actor: 'local_user', feedback: '', createdAt: new Date().toISOString() }
    const stageStatus = { ...pipeline.stageStatus, code: 'completed' as const, eval: 'active' as const }
    pipeline = { ...pipeline, currentStage: 'eval', revision: pipeline.revision + 1, stageStatus, executionReviews: [...(pipeline.executionReviews ?? []), review], updatedAt: new Date().toISOString() }
    return pipeline
  }
  backend.createPipelineSession = async (pipelineId, backendId, role, selection) => {
    if (!selection) throw new Error('Modelo ausente')
    return { session: { id: `${role}-legacy-session`, workspaceId: 'workspace-1', backendId, status: 'ready', resumable: true, createdAt: date, updatedAt: date }, prompt: `Executar ${role}`, role }
  }
}
if (scenario === 'agents') {
  let agents: Agent[] = []
  backend.listAgents = async () => agents
  backend.saveAgent = async input => {
    const saved: Agent = { id: input.id ?? `agent-${agents.length + 1}`, name: input.name, description: input.description, instructions: input.instructions, backendId: input.backendId, allowedTools: input.allowedTools, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z' }
    agents = [saved, ...agents.filter(item => item.id !== saved.id)]
    return saved
  }
}
if (scenario === 'delegations') {
  const date = '2026-09-26T10:00:00Z'
  const agent: Agent = { id: 'agent-analyst', name: 'Analista', description: 'Revisa o trabalho delegado', instructions: 'Leia antes de responder.', backendId: 'local', allowedTools: ['read'], createdAt: date, updatedAt: date }
  const parent = 'parent-session', child = 'child-running', grandchild = 'grandchild-done'
  const key = 'harflex:synthetic-delegation-cancelled'
  let cancelled = sessionStorage.getItem(key) === '1'
  const listeners = new Set<(event: AgentEvent) => void>()
  const session = (id: string) => ({ id, workspaceId: 'workspace-1', backendId: 'local', status: id === child ? cancelled ? 'cancelled' : 'running' : id === grandchild ? 'completed' : 'ready', resumable: true, createdAt: date, updatedAt: date })
  const links = (): Delegation[] => [
    { id: 'delegation-1', parentSessionId: parent, childSessionId: child, agentId: agent.id, taskPrompt: 'Revisar os critérios', promptCount: 1, promptLimit: 3, timeoutSeconds: 300, depth: 1, createdAt: date, status: cancelled ? 'cancelled' : 'running', result: '', errorCode: '' },
    { id: 'delegation-2', parentSessionId: child, childSessionId: grandchild, agentId: agent.id, taskPrompt: 'Avaliar evidências', promptCount: 1, promptLimit: 3, timeoutSeconds: 300, depth: 2, createdAt: date, status: 'completed', result: 'Critérios revisados no journal.', errorCode: '' },
  ]
  backend.listAgents = async () => [agent]
  backend.listSessions = async () => [session(parent), session(child), session(grandchild)]
  backend.openSession = async id => session(id)
  backend.getParentDelegation = async id => links().find(link => link.childSessionId === id) ?? null
  backend.listDelegations = async id => links().filter(link => link.parentSessionId === id)
  backend.listEvents = async (id, after) => (id === child ? [
    { id: 'child-start', streamId: child, sequence: 1, type: 'run.started', data: {}, createdAt: date },
    ...(cancelled ? [{ id: 'child-cancel', streamId: child, sequence: 2, type: 'run.cancelled', data: { reason: 'cancelled' }, createdAt: date }] : []),
  ] : []).filter(event => event.sequence > after)
  backend.onEvent = listener => { listeners.add(listener); return () => listeners.delete(listener) }
  backend.cancel = async id => {
    if (id !== child || cancelled) throw new Error('no_active_run')
    cancelled = true
    sessionStorage.setItem(key, '1')
    listeners.forEach(listener => listener({ id: 'child-cancel', streamId: child, sequence: 2, type: 'run.cancelled', data: { reason: 'cancelled' }, createdAt: date }))
  }
}
if (scenario === 'delegation-queued') {
  const date = '2026-09-26T10:00:00Z'
  const child = { id: 'child-queued', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', resumable: true, createdAt: date, updatedAt: date }
  backend.listSessions = async () => [child]
  backend.openSession = async () => child
  backend.getParentDelegation = async id => id === child.id ? {
    id: 'delegation-queued', parentSessionId: 'parent-session', childSessionId: child.id, agentId: 'agent-analyst',
    taskPrompt: 'Tarefa recuperada', promptCount: 0, promptLimit: 3, timeoutSeconds: 300, depth: 1,
    createdAt: date, status: 'ready', result: '', errorCode: '',
  } : null
  backend.listEvents = async (id, after) => id === child.id && after === 0 ? [{
    id: 'queued-task', streamId: id, sequence: 1, type: 'subagent.prompt.queued',
    data: { prompt: 'Revisar a SPEC recuperada' }, createdAt: date,
  }] : []
  backend.prompt = async () => { throw new Error('Queued task must not run automatically') }
}
if (scenario === 'workflows') {
  let definitions: Workflow[] = []
  let runs: WorkflowRun[] = []
  const date = '2026-09-25T10:00:00Z'
  backend.listWorkflows = async () => definitions
  backend.listWorkflowRuns = async () => runs
  backend.saveWorkflow = async input => {
    const item: Workflow = { id: input.id ?? `workflow-${definitions.length + 1}`, workspaceId: input.workspaceId, name: input.name, steps: input.steps, revision: 1, createdAt: date, updatedAt: date }
    definitions = [item, ...definitions.filter(existing => existing.id !== item.id)]
    return item
  }
  backend.startWorkflow = async input => {
    const definition = definitions.find(item => item.id === input.workflowId)
    if (!definition) throw new Error('Workflow absent')
    const run: WorkflowRun = { id: `run-${runs.length + 1}`, workflowId: definition.id, workspaceId: definition.workspaceId, backendId: input.backendId, steps: definition.steps, currentStep: 0, status: 'ready', lastSessionId: '', createdAt: date, updatedAt: date }
    runs = [run, ...runs]
    return run
  }
  backend.runWorkflowStep = async runId => {
    const run = runs.find(item => item.id === runId)
    if (!run) throw new Error('Run absent')
    const next: WorkflowRun = { ...run, currentStep: run.currentStep + 1, lastSessionId: 'session-1', status: run.currentStep + 1 >= run.steps.length ? 'completed' : 'ready' }
    runs = runs.map(item => item.id === runId ? next : item)
    return next
  }
}
if (scenario === 'workflow-recovery') {
  const date = '2026-09-26T10:00:00Z'
  const definition: Workflow = { id: 'workflow-recovery', workspaceId: 'workspace-1', name: 'Revisão', steps: [{ name: 'Checar alterações', prompt: 'Cheque as alterações' }], revision: 1, createdAt: date, updatedAt: date }
  let run: WorkflowRun = { id: 'run-recovery', workflowId: definition.id, workspaceId: 'workspace-1', backendId: 'local', steps: definition.steps, currentStep: 0, status: 'paused', lastSessionId: 'session-review', createdAt: date, updatedAt: date }
  backend.listWorkflows = async () => [definition]
  backend.listWorkflowRuns = async () => [run]
  backend.openSession = async () => ({ id: 'session-review', workspaceId: 'workspace-1', backendId: 'local', status: 'failed', resumable: true, createdAt: date, updatedAt: date })
  backend.listEvents = async () => [{ id: 'review-event', streamId: 'session-review', sequence: 1, type: 'run.failed', data: { reason: 'execution_failed' }, createdAt: date }]
  backend.resumeWorkflowRun = async input => {
    if (input.runId !== run.id || input.reviewedSessionId !== run.lastSessionId || input.choice !== 'retry' || run.status !== 'paused') throw new Error('Invalid recovery decision')
    run = { ...run, status: 'ready' }
    return run
  }
  backend.runWorkflowStep = async runId => {
    if (runId !== run.id || run.status !== 'ready') throw new Error('Step was not prepared')
    run = { ...run, status: 'completed', currentStep: 1, lastSessionId: 'session-retry' }
    return run
  }
}
if (scenario === 'schedules') {
  const cancelPending = new URLSearchParams(window.location.search).get('cancelPending') === '1'
  const scheduleKey = 'harflex:synthetic-schedules'
  const jobKey = 'harflex:synthetic-schedule-jobs'
  let schedules: Schedule[] = JSON.parse(sessionStorage.getItem(scheduleKey) ?? '[]')
  let jobs: ScheduleJob[] = JSON.parse(sessionStorage.getItem(jobKey) ?? '[]')
  const listeners = new Set<(workspaceId: string) => void>()
  const date = '2026-09-26T10:00:00Z'
  backend.listBackends = async () => [{ id: 'local', name: 'Local', kind: 'api', available: true }, { id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  backend.listWorkflows = async () => [{ id: 'workflow-fixture', workspaceId: 'workspace-1', name: 'Revisar e documentar', steps: [{ name: 'Revisar', prompt: 'Revise' }], revision: 1, createdAt: date, updatedAt: date }]
  const publish = () => { sessionStorage.setItem(scheduleKey, JSON.stringify(schedules)); sessionStorage.setItem(jobKey, JSON.stringify(jobs)); listeners.forEach(listener => listener('workspace-1')) }
  backend.onScheduleChange = listener => { listeners.add(listener); return () => listeners.delete(listener) }
  backend.listSchedules = async () => schedules
  backend.listScheduleJobs = async () => jobs
  backend.saveSchedule = async input => {
    const prior = schedules.find(item => item.id === input.id)
    const saved: Schedule = { ...input, id: input.id ?? `schedule-${schedules.length + 1}`, revision: (prior?.revision ?? 0) + 1, nextRunAt: input.enabled ? '2026-09-27T12:30:00Z' : null, createdAt: prior?.createdAt ?? date, updatedAt: date }
    schedules = [saved, ...schedules.filter(item => item.id !== saved.id)]
    publish()
    return saved
  }
  backend.setSchedulePaused = async (id, paused, revision) => {
    const prior = schedules.find(item => item.id === id)
    if (!prior || prior.revision !== revision) throw new Error('Schedule conflict')
    const saved: Schedule = { ...prior, enabled: !paused, nextRunAt: paused ? null : '2026-09-27T12:30:00Z', revision: revision + 1 }
    schedules = schedules.map(item => item.id === id ? saved : item)
    publish()
    return saved
  }
  backend.runScheduleNow = async scheduleId => {
    const created: ScheduleJob = { id: `job-${jobs.length + 1}`, scheduleId, workspaceId: 'workspace-1', trigger: 'manual', status: cancelPending ? 'running' : 'queued', errorCode: '', workflowRunId: '', sessionId: '', dueAt: date, createdAt: date, startedAt: cancelPending ? date : null, finishedAt: null }
    jobs = [created, ...jobs]; publish()
    if (!cancelPending) setTimeout(() => { jobs = jobs.map(item => item.id === created.id ? { ...item, status: 'completed', sessionId: 'session-1', startedAt: date, finishedAt: date } : item); publish() }, 100)
    return created
  }
  backend.cancelScheduleJob = async jobId => {
    jobs = jobs.map(item => item.id === jobId ? { ...item, status: cancelPending ? 'cancel_requested' : 'cancelled', errorCode: cancelPending ? '' : 'cancelled', finishedAt: cancelPending ? null : date } : item)
    publish()
    return jobs.find(item => item.id === jobId)!
  }
  if (new URLSearchParams(window.location.search).get('openError') === '1') {
    backend.openSession = async () => { throw { cause: { code: 'session_not_found' } } }
  }
}
if (scenario === 'skills') {
  let skills: Skill[] = []
  const date = '2026-09-25T10:00:00Z'
  backend.listSkills = async () => skills
  backend.saveSkill = async input => {
    const prior = skills.find(item => item.id === input.id)
    const saved: Skill = { ...input, id: input.id ?? `skill-${skills.length + 1}`, revision: (prior?.revision ?? 0) + 1, createdAt: prior?.createdAt ?? date, updatedAt: date }
    skills = [saved, ...skills.filter(item => item.id !== saved.id)]
    return saved
  }
  backend.importSkill = async input => {
    const saved: Skill = { id: `skill-${skills.length + 1}`, workspaceId: input.workspaceId, name: 'review', description: 'Imported skill', content: 'Revise testes importados', enabled: input.enabled, revision: 1, createdAt: date, updatedAt: date }
    skills = [saved, ...skills]
    return saved
  }
}
if (scenario === 'mcp') {
  let servers: MCPServer[] = []
  const date = '2026-09-25T10:00:00Z'
  backend.listMCPServers = async () => servers
  backend.saveMCPServer = async input => {
    const { token, ...safeInput } = input
    const saved: MCPServer = { ...safeInput, id: input.id ?? `mcp-${String(servers.length + 1).padStart(8, '0')}`, enabled: false,
      tools: [], hasCredential: !!token, createdAt: date, updatedAt: date }
    servers = [saved, ...servers.filter(item => item.id !== saved.id)]
    return saved
  }
  backend.connectMCPServer = async id => {
    const found = servers.find(item => item.id === id)
    if (!found) throw new Error('Servidor ausente')
    const connected: MCPServer = { ...found, enabled: true, tools: [{ name: 'search', agentName: 'mcp_search', description: 'Busca documentos locais', schema: { type: 'object' } }] }
    servers = servers.map(item => item.id === id ? connected : item)
    return connected
  }
  backend.disableMCPServer = async id => {
    const found = servers.find(item => item.id === id)
    if (!found) throw new Error('Servidor ausente')
    const disabled = { ...found, enabled: false, tools: [] }
    servers = servers.map(item => item.id === id ? disabled : item)
    return disabled
  }
}
if (scenario === 'diagnostics') {
  backend.listWorkspaces = async () => [{ id: 'workspace-1', path: '/synthetic/workspace/api-faturas', profile: 'ask', available: true }]
  backend.listBackends = async () => [
    { id: 'codex', name: 'Codex CLI', kind: 'cli', available: true },
    { id: 'opencode', name: 'OpenCode CLI', kind: 'cli', available: false },
  ]
  backend.listProviderProfiles = async () => []
  backend.probeCredentialStore = async () => ({ status: 'unconfigured', checked: 0, missing: 0 })
  backend.listMCPServers = async () => []
}
if (scenario === 'execution') {
  const date = '2026-09-25T10:00:00Z'
  backend.listSessions = async () => [{ id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'completed', resumable: true, createdAt: date, updatedAt: date }]
  backend.listEvents = async () => [
    { id: 'run-1', streamId: 'session-1', sequence: 1, type: 'run.started', data: {}, createdAt: date },
    { id: 'usage-1', streamId: 'session-1', sequence: 2, type: 'usage.recorded', data: { inputTokens: 120, outputTokens: 45 }, createdAt: '2026-09-25T10:00:03Z' },
    { id: 'done-1', streamId: 'session-1', sequence: 3, type: 'run.completed', data: {}, createdAt: '2026-09-25T10:00:05Z' },
  ]
}
if (scenario === 'vault') {
  const date = '2026-09-25T10:00:00Z'
  backend.listProviderProfiles = async () => [{ id: 'local', name: 'Modelo local', kind: 'openai_compatible', providerType: 'lm_studio', baseUrl: 'http://127.0.0.1:1234/v1', model: 'local-model', hasCredential: true, endpointBlocked: false, updatedAt: date }]
  backend.listMCPServers = async () => [{ id: 'mcp-12345678', workspaceId: 'workspace-1', name: 'Docs', transport: 'http', command: '', args: [], url: 'http://127.0.0.1:3333/mcp', tokenEnvVar: '', authScheme: 'bearer', enabled: true, tools: [], hasCredential: true, createdAt: date, updatedAt: date }]
}
if (scenario === 'knowledge') {
  const key = 'harflex:synthetic-knowledge'
  const profileKey = 'harflex:synthetic-embedding-profile'
  let documents: KnowledgeDocument[] = JSON.parse(sessionStorage.getItem(key) ?? '[]')
  let embeddingProfile: KnowledgeEmbeddingProfile = JSON.parse(sessionStorage.getItem(profileKey) ?? '{"configured":false,"kind":"","baseUrl":"","model":"","fingerprint":"","progress":{"total":0,"indexed":0,"dimension":0}}')
  const persist = (items: KnowledgeDocument[]) => { documents = items; sessionStorage.setItem(key, JSON.stringify(items)) }
  const persistProfile = (profile: KnowledgeEmbeddingProfile) => { embeddingProfile = profile; sessionStorage.setItem(profileKey, JSON.stringify(profile)) }
  const date = '2026-09-26T10:00:00Z'
  backend.listKnowledge = async () => documents
  backend.pickKnowledgeFile = async () => '/synthetic/workspace/api-faturas/docs/arquitetura.md'
  backend.importKnowledge = async input => {
    if (!['docs/arquitetura.md', '/synthetic/workspace/api-faturas/docs/arquitetura.md'].includes(input.path)) throw new Error('Source absent')
    const item: KnowledgeDocument = { id: 'document-1', workspaceId: input.workspaceId, path: 'docs/arquitetura.md', sourceSize: 312, sourceModifiedAt: date, indexedAt: date, chunkCount: 1 }
    persist([item])
    if (embeddingProfile.configured) persistProfile({ ...embeddingProfile, progress: { ...embeddingProfile.progress, total: 1, indexed: 0 } })
    return item
  }
  backend.searchKnowledge = async input => input.query.toLocaleLowerCase('pt-BR').includes('proveniência')
    ? documents.filter(item => !input.documentId || input.documentId === item.id).map(item => ({ documentId: item.id, path: item.path, lineStart: 4, snippet: 'Toda resposta deve carregar proveniência da fonte local.', sourceModifiedAt: item.sourceModifiedAt, indexedAt: item.indexedAt })) : []
  backend.searchKnowledgeDetailed = async input => ({ mode: embeddingProfile.progress.indexed > 0 ? 'hybrid' : 'textual', hits: await backend.searchKnowledge(input) })
  backend.getKnowledgeEmbeddingProfile = async () => embeddingProfile
  backend.saveKnowledgeEmbeddingProfile = async input => {
    const profile: KnowledgeEmbeddingProfile = { configured: true, kind: input.kind, baseUrl: input.baseUrl, model: input.model, fingerprint: `${input.kind}:${input.model}`, progress: { total: documents.length, indexed: 0, dimension: 0 } }
    persistProfile(profile)
    return profile
  }
  backend.indexKnowledgeVectors = async () => {
    const profile: KnowledgeEmbeddingProfile = { ...embeddingProfile, progress: { ...embeddingProfile.progress, indexed: embeddingProfile.progress.total, dimension: 2 } }
    persistProfile(profile)
    return profile
  }
  backend.reindexKnowledge = async input => {
    const item = documents.find(existing => existing.id === input.documentId)
    if (!item) throw new Error('Document absent')
    return item
  }
  backend.removeKnowledge = async input => {
    persist(documents.filter(item => item.id !== input.documentId))
    if (embeddingProfile.configured) persistProfile({ ...embeddingProfile, progress: { ...embeddingProfile.progress, total: documents.length, indexed: 0 } })
  }
}
if (scenario === 'channels' || scenario === 'channels-uncertain') {
  const key = `harflex:synthetic-${scenario}`
  const attemptsKey = `${key}:attempts`
  const date = '2026-09-26T10:00:00Z'
  const saved: { channels: LocalChannel[]; messages: ChannelMessage[] } = JSON.parse(sessionStorage.getItem(key) ?? '{"channels":[],"messages":[]}')
  const persist = () => sessionStorage.setItem(key, JSON.stringify(saved))
  backend.listLocalChannels = async () => saved.channels
  backend.saveLocalChannel = async input => {
    const prior = saved.channels.find(item => item.folder === input.folder)
    if (prior) return prior
    const channel: LocalChannel = { id: `channel-${saved.channels.length + 1}`, workspaceId: input.workspaceId, name: input.name, folder: input.folder, status: 'ready', lastError: '', createdAt: date, updatedAt: date }
    saved.channels.push(channel)
    persist()
    return channel
  }
  backend.listChannelMessages = async channelId => saved.messages.filter(item => item.channelId === channelId).reverse()
  backend.importChannelInbox = async channelId => {
    const channel = saved.channels.find(item => item.id === channelId)
    if (!channel) throw new Error('Channel absent')
    if (saved.messages.some(item => item.id === `incoming-${channelId}`)) return { channel, imported: 0, failed: 0 }
    saved.messages.push({ id: `incoming-${channelId}`, channelId, direction: 'incoming', fileName: 'pedido.md', content: 'Pedido local de revisão.', status: 'received', errorCode: '', createdAt: date, updatedAt: date })
    persist()
    return { channel, imported: 1, failed: 0 }
  }
  backend.sendChannelMessage = async input => {
    const attempts: string[] = JSON.parse(sessionStorage.getItem(attemptsKey) ?? '[]')
    attempts.push(input.requestId)
    sessionStorage.setItem(attemptsKey, JSON.stringify(attempts))
    if (scenario === 'channels-uncertain' && attempts.length === 1) throw new Error('Synthetic transport lost before reservation')
    const existing = saved.messages.find(item => item.fileName === `harflex-${input.requestId}.md`)
    if (existing) return existing
    const sent: ChannelMessage = { id: `outgoing-${saved.messages.length}`, channelId: input.channelId, direction: 'outgoing', fileName: `harflex-${input.requestId}.md`, content: input.content, status: 'sent', errorCode: '', createdAt: date, updatedAt: date }
    saved.messages.push(sent)
    persist()
    return sent
  }
  backend.retryChannelMessage = async input => {
    const found = saved.messages.find(item => item.channelId === input.channelId && item.fileName === `harflex-${input.requestId}.md`)
    if (!found) throw new Error('Message absent')
    found.status = 'sent'; found.errorCode = ''
    persist()
    return found
  }
}

if (scenario === 'restore-project') {
  backend.listWorkspaces = async () => [{ id: 'workspace-1', path: '/synthetic/workspace/api-faturas', profile: 'ask', available: true }]
}

// Browser-only E2E fixture: the full app against the simulated Go facade.
// Scenarios start from a blank shell unless they exercise reopening the last project.
ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App backend={backend} restoreProject={scenario === 'restore-project'} />
  </React.StrictMode>,
)
