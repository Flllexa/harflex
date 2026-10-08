import type { AgentEvent } from '../lib/backend'
import { createFakeBackend } from './fakeBackend'

/** Browser fixture for missing pushes, pagination and a recoverable replay outage. */
export function createReplayBackend(failOnce: boolean) {
  const { backend } = createFakeBackend()
  let listener: (event: AgentEvent) => void = () => undefined
  let started = false
  const journal: AgentEvent[] = Array.from({ length: 1002 }, (_, index) => ({
    id: `e${index + 1}`, streamId: 'session-1', sequence: index + 1,
    type: index === 0 ? 'run.started' : index === 1001 ? 'run.completed' : 'usage.recorded',
    data: index === 1001 ? { reason: '' } : { inputTokens: 1, outputTokens: 1 }, createdAt: '2026-09-25T10:00:00Z',
  }))
  backend.onEvent = receive => { listener = receive; return () => { listener = () => undefined } }
  backend.prompt = async () => {
    started = true
    listener(journal[0])
    listener(journal[4])
    return { status: 'completed' }
  }
  backend.listEvents = async (_session, after, limit = 1000) => {
    if (!started) return []
    if (failOnce) { failOnce = false; throw new Error('Fixture replay unavailable') }
    return journal.filter(event => event.sequence > after).slice(0, limit)
  }
  return backend
}

/** An approval push is lost and its first replay fails before cancellation. */
export function createApprovalReplayBackend(cancelFailsOnce = false) {
  const { backend } = createFakeBackend()
  let listener: (event: AgentEvent) => void = () => undefined
  const journal: AgentEvent[] = []
  let replayFailsOnce = true
  const append = (type: string, data: unknown = {}) => {
    const event: AgentEvent = { id: `e${journal.length + 1}`, streamId: 'session-1', sequence: journal.length + 1, type, data, createdAt: '2026-09-25T10:00:00Z' }
    journal.push(event)
    return event
  }
  backend.onEvent = receive => { listener = receive; return () => { listener = () => undefined } }
  backend.prompt = async () => {
    listener(append('run.started'))
    const approval = { approvalId: 'a1', toolCallId: 'c1', name: 'write', risk: 'write', arguments: { path: 'notes.md' } }
    append('approval.requested', approval)
    return { status: 'awaiting_approval', approval }
  }
  backend.cancel = async () => {
    if (cancelFailsOnce) { cancelFailsOnce = false; throw new Error('Fixture cancel unavailable') }
    listener(append('run.cancelled', { reason: '' }))
  }
  backend.listEvents = async (_session, after, limit = 1000) => {
    if (journal.length === 0) return []
    if (replayFailsOnce) { replayFailsOnce = false; throw new Error('Fixture replay unavailable') }
    return journal.filter(event => event.sequence > after).slice(0, limit)
  }
  return backend
}
