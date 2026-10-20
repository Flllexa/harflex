import { afterEach, describe, expect, it, vi } from 'vitest'
import { invalidEventDiagnostic, type AgentEvent } from './backend'
import { checkReplayOutcome, historyTurns, loadOlderTurns, replaySession } from './replay'
import { createSessionStore } from '../state/session'
import { createFakeBackend, sessionMetadata } from '../test/fakeBackend'

const event = (sequence: number): AgentEvent => ({ id: `e${sequence}`, streamId: 's1', sequence, type: 'future.event', data: {}, createdAt: '2026-09-25T10:00:00Z' })
function setup() {
  const { backend } = createFakeBackend()
  const store = createSessionStore()
  store.getState().setSession({ id: 's1', workspaceId: 'w1', backendId: 'local', status: 'ready', ...sessionMetadata })
  return { backend, store, controller: new AbortController() }
}
afterEach(() => vi.useRealTimers())

describe('replay outcome reconciliation', () => {
  const approval = { ...event(2), type: 'approval.requested', data: { approvalId: 'a1', toolCallId: 'c1', name: 'write', risk: 'write' } }

  it.each(['run.cancelled', 'run.failed', 'run.completed', 'external.run.cancelled', 'external.run.failed', 'external.run.completed'])(
    'accepts %s after an approval request instead of requiring a stale waiting outcome', type => {
      const { store } = setup()
      store.getState().receive([{ ...event(1), type: 'run.started' }, approval, { ...event(3), type }])
      expect(() => checkReplayOutcome(store.getState(), { after: 0, approval: true })).not.toThrow()
      expect(store.getState().activeRun).toBe('idle')
      expect(store.getState().outcome?.status).toBe(type.split('.').pop())
      expect(store.getState().pendingApprovals).toEqual([])
    },
  )

  it.each([true, false])('does not reuse a terminal at or before the call baseline (approval: %s)', approval => {
    const { store } = setup()
    store.getState().receive([{ ...event(1), type: 'run.completed' }, { ...event(2), type: 'run.cancelled' }])
    expect(() => checkReplayOutcome(store.getState(), { after: 2, approval })).toThrow('Não foi possível atualizar')
  })

  it('accepts the normal approval outcome without a later terminal', () => {
    const { store } = setup()
    store.getState().receive([{ ...event(1), type: 'run.started' }, approval])
    expect(() => checkReplayOutcome(store.getState(), { after: 0, approval: true })).not.toThrow()
  })

  it('requires a terminal when cancellation succeeded even if an approval is replayed', () => {
    const { store } = setup()
    store.getState().receive([{ ...event(1), type: 'run.started' }, approval])
    expect(() => checkReplayOutcome(store.getState(), { after: 1, approval: false })).toThrow('Não foi possível atualizar')
  })

  it('does not accept a terminal past a gap in the journal', () => {
    const { store } = setup()
    store.getState().receive([{ ...event(1), type: 'run.started' }, { ...event(3), type: 'run.cancelled' }])
    expect(() => checkReplayOutcome(store.getState(), { after: 0, approval: true })).toThrow('Não foi possível atualizar')
  })
})

describe('bounded session replay', () => {
  it('starts at the contiguous prefix and projects paginated entries once', async () => {
    const { backend, store, controller } = setup()
    store.getState().receive([event(1), event(5)])
    const journal = Array.from({ length: 1002 }, (_, index) => event(index + 1))
    backend.listEvents = vi.fn(async (_id, after) => journal.filter(item => item.sequence > after).slice(0, 1000))
    const updates = vi.fn()
    store.subscribe(updates)
    await replaySession(backend, store, controller.signal)
    expect(vi.mocked(backend.listEvents).mock.calls).toEqual([['s1', 1, 1000], ['s1', 1001, 1000]])
    expect(store.getState().contiguousSequence).toBe(1002)
    expect(updates).toHaveBeenCalledTimes(1)
  })

  it('stops a repeated cursor and retains the recoverable first page', async () => {
    const { backend, store, controller } = setup()
    const page = Array.from({ length: 1000 }, (_, index) => event(index + 1))
    backend.listEvents = vi.fn(async () => page)
    await expect(replaySession(backend, store, controller.signal)).rejects.toThrow('Não foi possível atualizar')
    expect(backend.listEvents).toHaveBeenCalledTimes(2)
    expect(store.getState().contiguousSequence).toBe(1000)
  })

  it('fails on a short page with a missing sequence instead of skipping the gap', async () => {
    const { backend, store, controller } = setup()
    backend.listEvents = async () => [event(1), event(3)]
    await expect(replaySession(backend, store, controller.signal)).rejects.toThrow('Não foi possível atualizar')
    expect(store.getState().contiguousSequence).toBe(1)
  })

  it('records invalid replay diagnostics without advancing the journal cursor', async () => {
    const { backend, store, controller } = setup()
    backend.listEvents = async () => [invalidEventDiagnostic()]
    await expect(replaySession(backend, store, controller.signal)).rejects.toThrow('Não foi possível atualizar')
    expect(store.getState().events[0].type).toBe('diagnostic.invalid_event')
    expect(store.getState().contiguousSequence).toBe(0)
  })

  it('stops after the page budget and preserves a checkpoint for an explicit retry', async () => {
    const { backend, store, controller } = setup()
    backend.listEvents = vi.fn(async (_id, after) => Array.from({ length: 1000 }, (_, index) => event(after + index + 1)))
    await expect(replaySession(backend, store, controller.signal)).rejects.toThrow('Não foi possível atualizar')
    expect(backend.listEvents).toHaveBeenCalledTimes(100)
    expect(store.getState().contiguousSequence).toBe(100_000)
  })

  it('aborts an outstanding page and ignores its late response after cleanup', async () => {
    const { backend, store, controller } = setup()
    let resolve: (events: AgentEvent[]) => void = () => undefined
    // A conversation opening now reads its last turns first: that is the request in flight.
    backend.listEventsBefore = vi.fn(() => new Promise<AgentEvent[]>(complete => { resolve = complete }))
    backend.listEvents = vi.fn(async () => [])
    const updates = vi.fn()
    store.subscribe(updates)
    const replay = replaySession(backend, store, controller.signal)
    const failure = expect(replay).rejects.toMatchObject({ name: 'AbortError' })
    controller.abort()
    await failure
    resolve([event(1)])
    await Promise.resolve()
    expect(updates).not.toHaveBeenCalled()
    expect(backend.listEventsBefore).toHaveBeenCalledTimes(1)
    expect(backend.listEvents).not.toHaveBeenCalled()
  })

  it('times out a stalled page instead of leaving a call pending indefinitely', async () => {
    vi.useFakeTimers()
    const { backend, store, controller } = setup()
    backend.listEvents = () => new Promise(() => undefined)
    const failure = expect(replaySession(backend, store, controller.signal)).rejects.toThrow('Não foi possível atualizar')
    await vi.advanceTimersByTimeAsync(30_000)
    await failure
  })
})

describe('a long conversation opens on its last turns', () => {
  const turn = (index: number): AgentEvent[] => ['message.user', 'run.started', 'run.completed'].map((type, offset) => ({ ...event(index * 3 + offset + 1), type, data: type === 'message.user' ? { content: `pedido ${index}` } : {} }))
  const journal = Array.from({ length: historyTurns + 5 }, (_, index) => turn(index)).flat()
  const before = vi.fn(async (_id: string, beforeSequence: number, turns: number) => {
    const end = beforeSequence > 0 ? beforeSequence : Number.MAX_SAFE_INTEGER
    const starts = journal.filter(item => item.type === 'message.user' && item.sequence < end).map(item => item.sequence)
    const start = starts.length > turns ? starts[starts.length - turns] : 1
    return journal.filter(item => item.sequence >= start && item.sequence < end)
  })

  it('reads only the last turns, then catches up from there', async () => {
    const { backend, store, controller } = setup()
    backend.listEventsBefore = before
    backend.listEvents = vi.fn(async (_id, after) => journal.filter(item => item.sequence > after))
    await replaySession(backend, store, controller.signal)
    const state = store.getState()
    expect(state.windowStart).toBe(5 * 3 + 1)
    expect(state.hasOlder).toBe(true)
    expect(state.contiguousSequence).toBe(journal.length)
    expect(state.messages.filter(item => item.kind === 'user')).toHaveLength(historyTurns)
    expect(vi.mocked(backend.listEvents).mock.calls[0]).toEqual(['s1', journal.length, 1000])
  })

  it('brings older turns in front of the ones on screen', async () => {
    const { backend, store, controller } = setup()
    backend.listEventsBefore = before
    backend.listEvents = vi.fn(async (_id, after) => journal.filter(item => item.sequence > after))
    await replaySession(backend, store, controller.signal)
    await loadOlderTurns(backend, store)
    const state = store.getState()
    expect(state.windowStart).toBe(1)
    expect(state.hasOlder).toBe(false)
    const users = state.messages.filter(item => item.kind === 'user')
    expect(users).toHaveLength(historyTurns + 5)
    expect(users[0]).toMatchObject({ text: 'pedido 0' })
    expect(state.contiguousSequence).toBe(journal.length)
  })
})
