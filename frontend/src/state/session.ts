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
  /** The context was compacted here: what came before stays on screen, the model works from the summary. */
  | { kind: 'compaction'; id: string; beforeTokens: number; afterTokens: number; summary: string }
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
        // A CLI compacts its own conversation when it fills up; the chat marks where, as for the API runs.
        const raw = record(data.raw)
        if (raw.type === 'system' && raw.subtype === 'compact_boundary') {
          closeExternalMessages()
          if (streaming) streaming.streaming = false
          streaming = null
          const meta = record(raw.compact_metadata)
          messages.push({ kind: 'compaction', id: event.id, beforeTokens: Number(meta.pre_tokens) || 0, afterTokens: 0, summary: '' })
          break
        }
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
      case 'context.compacted':
        closeExternalMessages()
        if (streaming) streaming.streaming = false
        streaming = null
        messages.push({ kind: 'compaction', id: event.id, beforeTokens: Number(data.beforeTokens) || 0, afterTokens: Number(data.afterTokens) || 0, summary: text(data.summary) })
        break
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
  /** The first sequence on screen: a long conversation opens on its last turns (0: nothing read yet, 1: from the start). */
  windowStart: number
  /** Older turns are still in the journal, read when the person scrolls up. */
  hasOlder: boolean
  draft: string
  /** A backend call is in flight (Prompt and Approve block while a run executes). */
  calling: boolean
  error?: string
  setConnection(state: ConnectionState, backends?: BackendOption[]): void
  addBackend(backend: BackendOption): void
  setWorkspace(workspace: Workspace): void
  /** Focuses a conversation; `calling` says a call of this session is still in flight from before. */
  setSession(session: Session, calling?: boolean): void
  updateSession(session: Session): void
  receive(events: AgentEvent[]): void
  /** Starts the conversation at the last turns read from the journal. */
  openWindow(events: AgentEvent[]): void
  /** Puts older turns in front of the ones on screen. */
  receiveOlder(events: AgentEvent[]): void
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

/** What the conversation weighs now: the latest count the provider gave, or what a compaction brought it down to. */
/** The context of a CLI turn: Claude Code reports each call of the turn, and the last one carried the whole context. */
function cliTurnContext(event: AgentEvent | undefined): number {
  if (!event || event.type !== 'external.event') return 0
  const raw = record(record(event.data).raw)
  const calls = record(raw.usage).iterations
  if (raw.type !== 'result' || !Array.isArray(calls) || calls.length === 0) return 0
  const last = record(calls[calls.length - 1])
  return ['input_tokens', 'cache_creation_input_tokens', 'cache_read_input_tokens', 'output_tokens'].reduce((total, key) => total + (Number(last[key]) || 0), 0)
}

export function contextInUse(events: AgentEvent[]): number {
  for (let index = events.length - 1; index >= 0; index--) {
    const data = record(events[index].data)
    if (events[index].type === 'context.compacted') return Number(data.afterTokens) || 0
    if (events[index].type === 'usage.recorded' && Number(data.inputTokens) > 0) {
      // A CLI turn sums every call in inputTokens; its context is what the last call carried.
      if (Number(data.contextTokens) > 0) return Number(data.contextTokens)
      for (let before = index - 1; before >= 0 && before >= index - 3; before--) {
        const context = cliTurnContext(events[before])
        if (context > 0) return context
      }
      return (Number(data.inputTokens) || 0) + (Number(data.outputTokens) || 0)
    }
  }
  return 0
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
    windowStart: 0,
    hasOlder: false,
    draft: '',
    calling: false,
    readOnly: false,
    setConnection: (connectionState, backends) => set(backends ? { connectionState, backends } : { connectionState }),
    addBackend: backend => set(state => ({ backends: [...state.backends.filter(item => item.id !== backend.id), backend].sort((a, b) => a.id.localeCompare(b.id)) })),
    setWorkspace: workspace => set({ workspace, session: undefined, events: [], contiguousSequence: 0, windowStart: 0, hasOlder: false, draft: '', calling: false, readOnly: false, ...emptyProjection, error: undefined }),
    setSession: (session, calling = false) => set({ session, readOnly: sessionReadOnly(session, get().backends), events: [], contiguousSequence: 0, windowStart: 0, hasOlder: false, draft: '', calling, ...emptyProjection, error: undefined }),
    updateSession: session => { if (get().session?.id === session.id) set({ session, readOnly: sessionReadOnly(session, get().backends) }) },
    setDraft: draft => set({ draft }),
    openWindow: incoming => {
      const { session } = get()
      if (!session) return
      const own = incoming.filter(event => event.streamId === session.id && event.sequence > 0)
      const windowStart = own.length > 0 ? Math.min(...own.map(event => event.sequence)) : 1
      set({ windowStart, hasOlder: windowStart > 1 })
      get().receive(own)
    },
    receiveOlder: incoming => {
      const { session, events, windowStart } = get()
      if (!session || windowStart <= 1) return
      const older = incoming.filter(event => event.streamId === session.id && event.sequence > 0 && event.sequence < windowStart)
      const start = older.length > 0 ? Math.min(...older.map(event => event.sequence)) : 1
      // Nothing older came back: the conversation is whole from the start.
      const next = [...older, ...events].sort((a, b) => a.sequence - b.sequence)
      let contiguousSequence = start - 1
      for (const event of next) {
        if (event.streamId !== session.id) continue
        if (event.sequence === contiguousSequence + 1) contiguousSequence++
        else if (event.sequence > contiguousSequence) break
      }
      set({ windowStart: start, hasOlder: start > 1, events: next, contiguousSequence, ...project(next.filter(event => event.sequence <= contiguousSequence)) })
    },
    receive: incoming => {
      const { session, events, windowStart } = get()
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
      // The journal is contiguous from the first turn on screen; older turns load as the person scrolls up.
      let contiguousSequence = windowStart > 1 ? windowStart - 1 : 0
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
