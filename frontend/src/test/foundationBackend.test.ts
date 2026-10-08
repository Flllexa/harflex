import { createFoundationBackend } from './foundationBackend'

beforeEach(() => {
  sessionStorage.clear()
  vi.useFakeTimers()
})
afterEach(() => { vi.useRealTimers() })

test('streaming waits for explicit release regardless of elapsed time', async () => {
  const backend = createFoundationBackend()
  const run = backend.prompt('session-1', 'edit synthetic')
  await vi.advanceTimersByTimeAsync(60_000)
  const events = await backend.listEvents('session-1', 0)
  expect(events.map(event => event.type)).toEqual(['run.started', 'message.user', 'assistant.delta'])
  backend.releaseStreaming()
  expect((await run).status).toBe('awaiting_approval')
  expect((await backend.listEvents('session-1', 0)).some(event => event.type === 'tool.called')).toBe(true)
})

test.each(['cancel', 'unmount', 'dispose'] as const)('%s settles the streaming gate without executing tools', async action => {
  const backend = createFoundationBackend()
  const unsubscribe = backend.onEvent(() => undefined)
  const run = backend.prompt('session-1', 'edit synthetic')
  if (action === 'cancel') await backend.cancel('session-1')
  if (action === 'unmount') unsubscribe()
  if (action === 'dispose') backend.dispose()
  await vi.advanceTimersByTimeAsync(60_000)
  expect((await run).status).toBe('cancelled')
  backend.releaseStreaming()
  const events = await backend.listEvents('session-1', 0)
  expect(events[events.length - 1]?.type).toBe('run.cancelled')
  expect(events.some(event => event.type === 'tool.called')).toBe(false)
})

test('stream release is scoped and cannot resume a cancellation-only run', async () => {
  const backend = createFoundationBackend()
  const run = backend.prompt('session-1', 'cancel synthetic')
  backend.releaseStreaming()
  await backend.cancel('session-1')
  expect((await run).status).toBe('cancelled')
})

test('lists persisted sessions and reopens an interrupted approval without executing tools', async () => {
  const first = createFoundationBackend()
  const run = first.prompt('session-1', 'edit synthetic')
  first.releaseStreaming()
  await run
  first.dispose()
  const next = createFoundationBackend()
  const sessions = await next.listSessions('workspace-1')
  expect(sessions).toHaveLength(1)
  const session = await next.openSession(sessions[0].id, 'workspace-1')
  expect(session.status).toBe('paused')
  expect(session.resumable).toBe(true)
  const events = await next.listEvents(session.id, 0)
  expect(events[events.length - 1]?.type).toBe('run.interrupted')
  const length = events.length
  await next.openSession(session.id, 'workspace-1')
  expect(await next.listEvents(session.id, 0)).toHaveLength(length)
})

test('preserves synthetic provider metadata across restart without storing its key', async () => {
  const first = createFoundationBackend()
  await first.saveProviderProfile({ id: 'synthetic', name: 'Sintético', providerType: 'generic', model: 'fake', baseUrl: 'https://synthetic.invalid', apiKey: 'DO_NOT_PERSIST', clearCredential: false })
  first.dispose()
  const next = createFoundationBackend()
  expect(await next.listBackends()).toContainEqual({ id: 'synthetic', name: 'Sintético', kind: 'api', available: true })
  expect(JSON.stringify(sessionStorage)).not.toContain('DO_NOT_PERSIST')
})

test('simulates a single replay outage after restart, then permits an explicit retry', async () => {
  const first = createFoundationBackend()
  const run = first.prompt('session-1', 'edit synthetic')
  first.releaseStreaming()
  await run
  const next = createFoundationBackend({ replayOutage: true })
  await expect(next.listEvents('session-1', 0)).rejects.toThrow('Synthetic replay unavailable')
  expect((await next.listEvents('session-1', 0)).length).toBeGreaterThan(0)
})
