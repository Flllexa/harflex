import { LOCAL_DIAGNOSTIC_STREAM, type AgentEvent, type Backend } from './backend'
import type { SessionState, SessionStore } from '../state/session'
import { t } from '../i18n'

export const replayErrorMessage = 'Não foi possível atualizar os eventos. Tente novamente.'
const pageSize = 1000
const maxPages = 100
/** Turns a conversation opens on, and turns read each time the person scrolls to the top. */
export const historyTurns = 5

export type ReplayExpectation = { after: number; approval: boolean }

export function checkReplayOutcome(state: SessionState, expected?: ReplayExpectation) {
  if (state.activeRun === 'running') throw new Error(t(replayErrorMessage))
  if (!expected) return
  const terminalTypes = ['run.completed', 'run.failed', 'run.cancelled', 'run.interrupted', 'external.run.completed', 'external.run.failed', 'external.run.cancelled', 'external.run.interrupted']
  const callEvents = state.events.filter(event => event.sequence > expected.after && event.sequence <= state.contiguousSequence)
  // A later journal terminal supersedes the call's earlier waiting result.
  if ((state.activeRun === 'idle' || state.activeRun === 'paused') && callEvents.some(event => terminalTypes.includes(event.type))) return
  if (expected.approval && state.activeRun === 'awaiting_approval' && callEvents.some(event => event.type === 'approval.requested')) return
  throw new Error(t(replayErrorMessage))
}

function abortable<T>(request: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(signal.reason); return }
    const aborted = () => reject(signal.reason)
    signal.addEventListener('abort', aborted, { once: true })
    request.then(resolve, reject).finally(() => signal.removeEventListener('abort', aborted))
  })
}

function readPage(backend: Backend, sessionId: string, after: number, signal: AbortSignal): Promise<AgentEvent[]> {
  if (signal.aborted) return Promise.reject(signal.reason)
  return abortable(backend.listEvents(sessionId, after, pageSize), signal)
}

/** Reads the turns before the first one on screen and puts them in front. */
export async function loadOlderTurns(backend: Backend, store: SessionStore) {
  const { session, windowStart, hasOlder } = store.getState()
  if (!session || !hasOlder) return
  const older = await backend.listEventsBefore(session.id, windowStart, historyTurns)
  if (store.getState().session?.id !== session.id || store.getState().windowStart !== windowStart) return
  store.getState().receiveOlder(older)
}

/** Bounded catch-up, projecting once per batch of pages. A conversation opened now starts at its last turns; the
 * journal stays whole in the catalog, and older turns load on demand (loadOlderTurns).
 */
export async function replaySession(backend: Backend, store: SessionStore, signal: AbortSignal) {
  const { session, contiguousSequence } = store.getState()
  if (!session || signal.aborted) return
  const deadline = new AbortController()
  const stop = () => deadline.abort(signal.reason)
  signal.addEventListener('abort', stop, { once: true })
  const timeout = setTimeout(() => deadline.abort(new Error(t(replayErrorMessage))), 30_000)
  const collected: AgentEvent[] = []
  let after = contiguousSequence
  let finished = false
  try {
    // A conversation opened now starts at its last turns; the older ones load as the person scrolls up.
    if (after === 0 && store.getState().windowStart === 0) {
      const tail = await abortable(backend.listEventsBefore(session.id, 0, historyTurns), deadline.signal)
      if (signal.aborted || store.getState().session?.id !== session.id) return
      if (tail.some(event => event.streamId !== session.id)) throw new Error(t(replayErrorMessage))
      store.getState().openWindow(tail)
      after = store.getState().contiguousSequence
    }
    for (let pageNumber = 0; pageNumber < maxPages; pageNumber++) {
      const page = await readPage(backend, session.id, after, deadline.signal)
      if (signal.aborted || store.getState().session?.id !== session.id) return
      const diagnostics = page.filter(event => event.streamId === LOCAL_DIAGNOSTIC_STREAM && event.sequence === 0)
      if (diagnostics.length > 0) { collected.push(...diagnostics); throw new Error(t(replayErrorMessage)) }
      if (page.some(event => event.streamId !== session.id || event.sequence <= after)) throw new Error(t(replayErrorMessage))
      const next = page.reduce((cursor, event) => Math.max(cursor, event.sequence), after)
      if (page.length > 0 && next <= after) throw new Error(t(replayErrorMessage))
      collected.push(...page)
      after = next
      if (page.length < pageSize) { finished = true; break }
    }
    if (!finished) throw new Error(t(replayErrorMessage))
  } finally {
    clearTimeout(timeout)
    signal.removeEventListener('abort', stop)
    if (!signal.aborted && store.getState().session?.id === session.id) store.getState().receive(collected)
  }
  if (signal.aborted || store.getState().session?.id !== session.id) return
  const state = store.getState()
  if (state.events.some(event => event.streamId === session.id && event.sequence > state.contiguousSequence)) throw new Error(t(replayErrorMessage))
}
