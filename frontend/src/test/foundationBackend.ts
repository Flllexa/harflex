import type { AgentEvent, Backend, BackendOption, RunResult, Session } from '../lib/backend'
import { createFakeBackend, diff, sessionMetadata } from './fakeBackend'

// Synthetic facade only. Never imported by the production entry point.
// Storage holds fake journal entries, never API keys or actual workspace data.
export type FoundationTestControl = { releaseStreaming(): void }
export type FoundationBackend = Backend & FoundationTestControl & { dispose(): void }

declare global {
  interface Window { harflexSynthetic?: FoundationTestControl }
}

export function createFoundationBackend(options: { readOnly?: boolean; replayOutage?: boolean } = {}): FoundationBackend {
  const { backend } = createFakeBackend()
  const key = 'harflex:synthetic-foundation-journal'
  const journal: AgentEvent[] = JSON.parse(sessionStorage.getItem(key) ?? '[]')
  let replayUnavailable = !!options.replayOutage && journal.length > 0
  const metadataKey = `${key}:session`
  const profilesKey = `${key}:profiles`
  const profiles: BackendOption[] = JSON.parse(sessionStorage.getItem(profilesKey) ?? '[]')
  const defaultBackends = backend.listBackends
  backend.listBackends = async () => [...await defaultBackends(), ...profiles]
  backend.saveProviderProfile = async input => {
    const result: BackendOption = { id: input.id, name: input.name, kind: 'api', available: true }
    const previous = profiles.findIndex(item => item.id === input.id)
    if (previous >= 0) profiles.splice(previous, 1)
    profiles.push(result)
    sessionStorage.setItem(profilesKey, JSON.stringify(profiles))
    return result
  }
  let session: Session = JSON.parse(sessionStorage.getItem(metadataKey) ?? 'null') ?? { id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata }
  let reopened = false
  const listeners = new Set<(event: AgentEvent) => void>()
  let pending: { kind: 'streaming' | 'cancellation'; resolve: (proceed: boolean) => void } | undefined
  const emit = (type: string, data: unknown = {}) => {
    const event: AgentEvent = { id: `synthetic-${journal.length + 1}`, streamId: 'session-1', sequence: journal.length + 1, type, data, createdAt: '2026-09-25T10:00:00Z' }
    journal.push(event)
    sessionStorage.setItem(key, JSON.stringify(journal))
    listeners.forEach(listener => listener(event))
  }
  const settle = (proceed: boolean) => {
    const gate = pending
    pending = undefined
    gate?.resolve(proceed)
  }
  const cancelPending = () => {
    if (!pending) return
    emit('run.cancelled', { reason: '' })
    settle(false)
  }
  backend.onEvent = listener => {
    listeners.add(listener)
    return () => {
      listeners.delete(listener)
      if (listeners.size === 0) cancelPending()
    }
  }
  backend.listEvents = async (_session, after, limit = 1000) => {
    if (replayUnavailable) { replayUnavailable = false; throw new Error('Synthetic replay unavailable') }
    return journal.filter(event => event.sequence > after).slice(0, limit)
  }
  function currentSession(): Session {
    const terminal = [...journal].reverse().find(event => ['run.completed', 'run.failed', 'run.cancelled', 'run.interrupted', 'approval.requested', 'run.started'].includes(event.type))
    const status = terminal ? ({ 'run.completed': 'completed', 'run.failed': 'failed', 'run.cancelled': 'cancelled', 'run.interrupted': 'paused', 'approval.requested': 'awaiting_approval', 'run.started': 'running' } as Record<string, string>)[terminal.type] : 'ready'
    return { ...session, status, resumable: !options.readOnly, updatedAt: journal[journal.length - 1]?.createdAt ?? session.updatedAt }
  }
  backend.createSession = async (workspaceId, backendId) => {
    session = { ...session, workspaceId, backendId }
    sessionStorage.setItem(metadataKey, JSON.stringify(session))
    return { ...session, ...sessionMetadata }
  }
  backend.listSessions = async workspaceId => journal.length && workspaceId === session.workspaceId ? [currentSession()] : []
  backend.openSession = async (sessionId, workspaceId) => {
    if (sessionId !== session.id || workspaceId !== session.workspaceId) throw { cause: { code: 'session_not_found' } }
    const current = currentSession()
    if (current.status === 'running' || current.status === 'awaiting_approval') emit('run.interrupted', { reason: 'app_restart' })
    reopened = true
    return currentSession()
  }
  backend.exportAudit = async (sessionId, destination) => {
    if (sessionId !== session.id) throw { cause: { code: 'session_not_found' } }
    sessionStorage.setItem(`${key}:audit`, JSON.stringify({ destination, eventCount: journal.length }))
    return destination
  }
  backend.prompt = async (_session, text): Promise<RunResult> => {
    if (options.readOnly && reopened) throw { cause: { code: 'session_not_resumable' } }
    if (pending) throw new Error('Synthetic run already pending')
    const prefix = `call-${journal.length}`
    const released = new Promise<boolean>(resolve => {
      pending = { kind: text === 'cancel synthetic' ? 'cancellation' : 'streaming', resolve }
    })
    emit('run.started')
    emit('message.user', { role: 'user', content: text })
    if (text === 'cancel synthetic') {
      await released
      return { status: 'cancelled' }
    }
    emit('assistant.delta', { delta: 'Lendo o arquivo sintético...' })
    if (!await released) return { status: 'cancelled' }
    const read = { id: prefix + '-read', name: 'read', arguments: { path: 'notes.md' } }
    emit('message.assistant', { role: 'assistant', content: 'Lendo o arquivo sintético...', toolCalls: [read] })
    emit('tool.called', { toolCallId: read.id, name: read.name })
    if (text === 'fail synthetic') {
      emit('tool.failed', { toolCallId: read.id, name: read.name, error: 'Erro sintético: ' + 'caminho-indisponível-'.repeat(60) })
      emit('run.failed', { reason: 'tool_failed' })
      return { status: 'failed', reason: 'tool_failed' }
    }
    emit('tool.completed', { toolCallId: read.id, name: read.name, content: { text: 'arquivo sintético' } })
    const edit = { id: prefix + '-edit', name: 'edit', arguments: { path: 'notes.md', oldText: 'antigo', newText: 'olá' } }
    emit('message.assistant', { role: 'assistant', content: 'A alteração exige sua aprovação.', toolCalls: [edit] })
    const approval = { approvalId: 'approval-' + edit.id, toolCallId: edit.id, name: edit.name, risk: 'write', arguments: edit.arguments }
    emit('external.event', { type: 'text', text: 'Resposta CLI preservada.', raw: { type: 'synthetic.unknown', text: 'RAW_ONLY' } })
    emit('approval.requested', approval)
    return { status: 'awaiting_approval', approval }
  }
  backend.approve = async (_session, approvalId, allow) => {
    const toolCallId = approvalId.replace('approval-', '')
    emit(allow ? 'approval.approved' : 'approval.denied', { approvalId, toolCallId })
    if (!allow) {
      emit('run.failed', { reason: 'approval_denied' })
      return { status: 'failed', reason: 'approval_denied' }
    }
    emit('tool.called', { toolCallId, name: 'edit' })
    emit('tool.completed', { toolCallId, name: 'edit', content: { text: 'Alteração sintética aplicada.' }, details: { path: 'notes.md', diff } })
    emit('run.completed', { reason: '' })
    return { status: 'completed' }
  }
  backend.cancel = async () => { cancelPending() }
  return Object.assign(backend, {
    releaseStreaming: () => { if (pending?.kind === 'streaming') settle(true) },
    dispose: () => { cancelPending(); listeners.clear() },
  })
}
