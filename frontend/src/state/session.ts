import { createStore } from 'zustand/vanilla'
import { LOCAL_DIAGNOSTIC_STREAM, type AgentEvent, type Approval, type BackendOption, type RunResult, type Session, type Workspace } from '../lib/backend'

export type ToolStatus = 'pending' | 'awaiting_approval' | 'running' | 'completed' | 'failed' | 'denied' | 'skipped'
export type ToolCallView = {
  toolCallId: string
  name: string
  arguments: unknown
  status: ToolStatus
  output: string
  result?: string
  path?: string
  diff?: string
  error?: string
  errorCode?: string
  /** The call failed but the run went on: the model was told what happened. */
  recoverable?: boolean
}
export type ConversationItem =
  | { kind: 'user'; id: string; text: string }
  | { kind: 'assistant'; id: string; text: string; streaming: boolean }
  | { kind: 'tool'; id: string; call: ToolCallView }
export type ActiveRun = 'idle' | 'running' | 'awaiting_approval' | 'paused'
export type RunOutcome = { status: RunResult['status'] | 'interrupted'; reason?: string; code?: string }
export type ConnectionState = 'connecting' | 'ready' | 'unavailable' | 'degraded'

export type Projection = { messages: ConversationItem[]; pendingApprovals: Approval[]; activeRun: ActiveRun; outcome?: RunOutcome }

type Data = Record<string, unknown>
const text = (value: unknown) => (typeof value === 'string' ? value : '')
const record = (value: unknown): Data => (value && typeof value === 'object' && !Array.isArray(value) ? (value as Data) : {})
const terminal: Record<string, RunOutcome['status']> = {
  'run.completed': 'completed', 'run.failed': 'failed', 'run.cancelled': 'cancelled',
  'external.run.completed': 'completed', 'external.run.failed': 'failed', 'external.run.cancelled': 'cancelled',
  'run.interrupted': 'interrupted', 'external.run.interrupted': 'interrupted',
}

/** Rebuilds the conversation from the durable journal; the journal is the source of truth. */
export function project(events: AgentEvent[]): Projection {
  const messages: ConversationItem[] = []
  const tools = new Map<string, ToolCallView>()
  const pending = new Map<string, Approval>()
  const externalMessages = new Map<string, Extract<ConversationItem, { kind: 'assistant' }>>()
  const closeExternalMessages = () => {
    for (const message of externalMessages.values()) message.streaming = false
    externalMessages.clear()
  }
  let streaming: Extract<ConversationItem, { kind: 'assistant' }> | null = null
  let activeRun: ActiveRun = 'idle'
  let outcome: RunOutcome | undefined
  for (const event of events) {
    const data = record(event.data)
    const tool = tools.get(text(data.toolCallId))
    switch (event.type) {
      case 'run.started':
      case 'external.run.started':
        closeExternalMessages()
        if (streaming) streaming.streaming = false
        streaming = null
        pending.clear()
        activeRun = 'running'
        outcome = undefined
        break
      case 'message.user':
        closeExternalMessages()
        if (streaming) streaming.streaming = false
        streaming = null
        messages.push({ kind: 'user', id: event.id, text: text(data.content) })
        break
      case 'external.event': {
        if (data.type === 'assistant.message') {
          const content = text(data.text)
          const messageId = text(data.messageId)
          const key = `${text(data.sessionId)}/${messageId}`
          let message = messageId ? externalMessages.get(key) : undefined
          if (!message && content) {
            if (streaming) streaming.streaming = false
            streaming = null
            message = { kind: 'assistant', id: event.id, text: '', streaming: true }
            messages.push(message)
            if (messageId) externalMessages.set(key, message)
            else streaming = message
          }
          if (message) message.text = data.mode === 'delta' ? message.text + content : content
          break
        }
        // Retain old journals' generic text events; raw/log output is audit-only.
        if (data.type !== 'text' && data.type !== 'delta') break
        if (!text(data.text)) break
        if (!streaming) {
          streaming = { kind: 'assistant', id: event.id, text: '', streaming: true }
          messages.push(streaming)
        }
        streaming.text += text(data.text)
        break
      }
      case 'assistant.delta':
        if (!streaming) {
          streaming = { kind: 'assistant', id: event.id, text: '', streaming: true }
          messages.push(streaming)
        }
        streaming.text += text(data.delta)
        break
      case 'message.assistant': {
        const content = text(data.content)
        if (streaming) {
          streaming.text = content || streaming.text
          streaming.streaming = false
          streaming = null
        } else if (content) messages.push({ kind: 'assistant', id: event.id, text: content, streaming: false })
        for (const raw of Array.isArray(data.toolCalls) ? data.toolCalls : []) {
          const call = record(raw)
          const view: ToolCallView = { toolCallId: text(call.id), name: text(call.name), arguments: call.arguments, status: 'pending', output: '' }
          tools.set(view.toolCallId, view)
          messages.push({ kind: 'tool', id: `${event.id}:${view.toolCallId}`, call: view })
        }
        break
      }
      case 'approval.requested':
        pending.set(text(data.approvalId), { approvalId: text(data.approvalId), toolCallId: text(data.toolCallId), name: text(data.name), risk: text(data.risk), arguments: data.arguments })
        if (tool) tool.status = 'awaiting_approval'
        break
      case 'approval.approved':
      case 'approval.denied':
        pending.delete(text(data.approvalId))
        if (tool) tool.status = event.type === 'approval.denied' ? 'denied' : 'pending'
        break
      case 'tool.called':
        if (tool) tool.status = 'running'
        break
      case 'tool.updated':
        if (tool) tool.output += text(data.text)
        break
      case 'tool.completed': {
        if (!tool) break
        const details = record(data.details)
        tool.status = 'completed'
        tool.result = text(record(data.content).text)
        tool.path = text(details.path) || undefined
        tool.diff = text(details.diff) || undefined
        break
      }
      case 'tool.failed':
      case 'tool.denied':
      case 'tool.skipped':
        if (!tool) break
        // A user denial stays visible as the cause of the skipped call.
        if (!(event.type === 'tool.skipped' && tool.status === 'denied')) tool.status = event.type === 'tool.failed' ? 'failed' : event.type === 'tool.denied' ? 'denied' : 'skipped'
        tool.error = text(data.error) || undefined
        tool.errorCode = text(data.errorCode) || undefined
        if (event.type === 'tool.failed') {
          // What the call had produced before it failed (a command's output) stays visible.
          tool.result = text(record(data.content).text) || tool.result
          tool.recoverable = data.recoverable === true || undefined
        }
        break
      default:
        if (Object.prototype.hasOwnProperty.call(terminal, event.type)) {
          closeExternalMessages()
          activeRun = terminal[event.type] === 'interrupted' ? 'paused' : 'idle'
          outcome = { status: terminal[event.type], reason: text(data.reason) || undefined }
          if (outcome.status === 'interrupted') {
            for (const call of tools.values()) {
              if (call.status === 'running') { call.status = 'failed'; call.errorCode = 'outcome_unknown' }
              else if (call.status === 'pending' || call.status === 'awaiting_approval') { call.status = 'skipped'; call.errorCode = 'not_executed' }
            }
          }
          if (streaming) streaming.streaming = false
          streaming = null
          pending.clear()
        }
    }
  }
  if (pending.size > 0) activeRun = 'awaiting_approval'
  // A complete empty replacement retracts the part; keep no empty chat bubble.
  const shown = messages.filter(message => message.kind !== 'assistant' || message.text !== '')
  // Only the reply at the end of the conversation is still being written: an agent that sends several messages in one
  // run (CLI agents, with a message id each) leaves the earlier ones closed, or every one would keep its own dots.
  shown.forEach((message, index) => { if (message.kind === 'assistant' && index < shown.length - 1) message.streaming = false })
  return { messages: shown, pendingApprovals: [...pending.values()], activeRun, outcome }
}

export type SessionState = Projection & {
  connectionState: ConnectionState
  backends: BackendOption[]
  workspace?: Workspace
  session?: Session
  readOnly: boolean
  events: AgentEvent[]
  contiguousSequence: number
  draft: string
  /** A backend call is in flight (Prompt and Approve block while a run executes). */
  calling: boolean
  error?: string
  setConnection(state: ConnectionState, backends?: BackendOption[]): void
  addBackend(backend: BackendOption): void
  setWorkspace(workspace: Workspace): void
  setSession(session: Session): void
  updateSession(session: Session): void
  receive(events: AgentEvent[]): void
  setDraft(draft: string): void
  finishCall(result?: RunResult, error?: string): void
  startCall(): void
}

const emptyProjection: Projection = { messages: [], pendingApprovals: [], activeRun: 'idle', outcome: undefined }

/**
 * A run is live while it works or waits for an approval someone can still give. A conversation that cannot be resumed
 * has nobody to give it, even when its journal ends at an approval request, so that must not hold the screen.
 */
export function runIsLive(state: Pick<SessionState, 'activeRun' | 'readOnly'>): boolean {
  return state.activeRun === 'running' || (state.activeRun === 'awaiting_approval' && !state.readOnly)
}

export function sessionReadOnly(session: Session, backends: BackendOption[]) {
  const backend = backends.find(backend => backend.id === session.backendId)
  return !backend?.available || (!session.resumable && !(backend.kind === 'cli' && session.status === 'ready'))
}

export function createSessionStore() {
  return createStore<SessionState>()((set, get) => ({
    ...emptyProjection,
    connectionState: 'connecting',
    backends: [],
    events: [],
    contiguousSequence: 0,
    draft: '',
    calling: false,
    readOnly: false,
    setConnection: (connectionState, backends) => set(backends ? { connectionState, backends } : { connectionState }),
    addBackend: backend => set(state => ({ backends: [...state.backends.filter(item => item.id !== backend.id), backend].sort((a, b) => a.id.localeCompare(b.id)) })),
    setWorkspace: workspace => set({ workspace, session: undefined, events: [], contiguousSequence: 0, draft: '', calling: false, readOnly: false, ...emptyProjection, error: undefined }),
    setSession: session => set({ session, readOnly: sessionReadOnly(session, get().backends), events: [], contiguousSequence: 0, draft: '', calling: false, ...emptyProjection, error: undefined }),
    updateSession: session => { if (get().session?.id === session.id) set({ session, readOnly: sessionReadOnly(session, get().backends) }) },
    setDraft: draft => set({ draft }),
    receive: incoming => {
      const { session, events } = get()
      if (!session) return
      const known = new Set(events.map(event => event.id))
      const fresh = incoming.filter(event => {
        const localDiagnostic = event.streamId === LOCAL_DIAGNOSTIC_STREAM && event.sequence === 0 && event.type === 'diagnostic.invalid_event'
        if ((!localDiagnostic && event.streamId !== session.id) || known.has(event.id)) return false
        known.add(event.id)
        return true
      })
      if (fresh.length === 0) return
      const next = [...events, ...fresh].sort((a, b) => a.sequence - b.sequence)
      let contiguousSequence = 0
      for (const event of next) {
        if (event.streamId !== session.id) continue
        if (event.sequence === contiguousSequence + 1) contiguousSequence++
        else if (event.sequence > contiguousSequence) break
      }
      // Missing pushes must not allow a later terminal event to claim completion.
      set({ events: next, contiguousSequence, ...project(next.filter(event => event.sequence <= contiguousSequence)) })
    },
    startCall: () => set({ calling: true, error: undefined }),
    finishCall: (result, error) => set(state => ({
      calling: false,
      error,
      // The journal owns the outcome; the call result only fills in the stable code.
      outcome: result && state.outcome ? { ...state.outcome, code: result.code } : state.outcome,
    })),
  }))
}

export type SessionStore = ReturnType<typeof createSessionStore>
