import { describe, expect, it } from 'vitest'
import { parse, type AgentEvent } from '../lib/backend'
import codexEvents from '../../../internal/externalagent/testdata/codex-events.json'
import opencodeEvents from '../../../internal/externalagent/testdata/opencode-events.json'
import { createSessionStore, project } from './session'
import { sessionMetadata } from '../test/fakeBackend'

const event = (sequence: number, delta: string): AgentEvent => ({
  id: `event-${sequence}`, streamId: 'session-1', sequence,
  type: 'assistant.delta', data: { delta }, createdAt: '',
})

function sessionStore() {
  const store = createSessionStore()
  store.getState().setSession({ id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata })
  return store
}

describe('session event ingestion', () => {
  it('keeps a failed call the run survived, with what it had produced', () => {
    const entries: [string, unknown][] = [
      ['run.started', {}],
      ['message.user', { role: 'user', content: 'rode os testes' }],
      ['message.assistant', { role: 'assistant', content: 'Vou rodar.', toolCalls: [{ id: 't1', name: 'bash', arguments: { command: 'go test ./...' } }] }],
      ['tool.called', { toolCallId: 't1', name: 'bash' }],
      ['tool.failed', { toolCallId: 't1', name: 'bash', errorCode: 'exit_status', error: 'the command exited with status 1', recoverable: true, content: { text: 'FAIL: TestCriar' }, details: { exitCode: 1 } }],
    ]
    const events = entries.map(([type, data], index) => ({ ...event(index + 1, ''), type, data }))
    const view = project(events)
    const tool = view.messages.find(message => message.kind === 'tool')
    expect(tool).toMatchObject({ kind: 'tool', call: { status: 'failed', errorCode: 'exit_status', recoverable: true, result: 'FAIL: TestCriar' } })
    // The run is not over: the model was told and goes on.
    expect(view.outcome).toBeUndefined()
    // A failure that ended the run is not claimed as recoverable.
    const ended = project([...events.slice(0, 4), { ...event(5, ''), type: 'tool.failed', data: { toolCallId: 't1', name: 'bash', errorCode: 'tool_failed', error: 'tool execution failed', content: null } }, { ...event(6, ''), type: 'run.failed', data: { reason: 'tool_failed' } }])
    expect(ended.messages.find(message => message.kind === 'tool')).toMatchObject({ call: { status: 'failed', errorCode: 'tool_failed' } })
    expect(ended.messages.find(message => message.kind === 'tool')).not.toMatchObject({ call: { recoverable: true } })
    expect(ended.outcome).toEqual({ status: 'failed', reason: 'tool_failed' })
  })
  it('replaces a part with shorter text and removes its content when a later snapshot is empty', () => {
    const events = ['Olá mundo', 'Olá', ''].map((content, index) => ({
      ...event(index + 1, ''), type: 'external.event', data: {
        type: 'assistant.message', ...(content ? { text: content } : {}), raw: {}, mode: 'replace', messageId: 'part_1',
      },
    }))
    expect(project(events.slice(0, 2)).messages).toMatchObject([{ kind: 'assistant', text: 'Olá' }])
    expect(project(events).messages).toEqual([])
  })
  it('keeps the writing dots on the last reply only, when a run sends several messages', () => {
    const message = (index: number, messageId: string, content: string) => ({
      ...event(index, ''), type: 'external.event', data: { type: 'assistant.message', text: content, raw: {}, mode: 'replace', messageId },
    })
    const events = [{ ...event(1, ''), type: 'external.run.started', data: {} }, message(2, 'm1', 'Primeiro.'), message(3, 'm2', 'Segundo.'), message(4, 'm3', 'Terceiro.')]
    expect(project(events).messages).toMatchObject([{ text: 'Primeiro.', streaming: false }, { text: 'Segundo.', streaming: false }, { text: 'Terceiro.', streaming: true }])
  })
  it('replays shortened and emptied OpenCode fixture prefixes in journal order', () => {
    const events = parse.events(opencodeEvents.map((data, index) => ({
      ...event(index + 1, ''), type: 'external.event', data, createdAt: '2026-09-26T12:00:00Z',
    })))
    expect(project(events.slice(0, 5)).messages).toMatchObject([{ kind: 'assistant', text: 'Olá' }])
    expect(project(events.slice(0, 6)).messages).toEqual([])
    expect(project(events).messages).toMatchObject([{ kind: 'assistant', text: 'Segunda parte.' }])
  })
  it.each([
    ['codex', codexEvents, ['Resposta do Codex.\n']],
    ['opencode', opencodeEvents, ['Segunda parte.']],
  ])('projects persisted %s protocol fixtures exactly once per part and run', (adapter, fixture, answers) => {
    const entries = [
      ['external.run.started', { adapter }],
      ['message.user', { role: 'user', content: 'Pergunta' }],
      ...(fixture as unknown[]).map(data => ['external.event', data]),
      ['external.run.completed', { adapter }],
    ]
    const events = parse.events([...entries, ...entries].map(([type, data], i) => ({
      ...event(i + 1, ''), type: type as string, data, createdAt: '2026-09-26T12:00:00Z',
    })))
    expect(events.some(item => item.type === 'diagnostic.invalid_event')).toBe(false)
    const view = project(events)
    expect(view.messages.map(message => message.kind !== 'tool' && message.text)).toEqual(['Pergunta', ...(answers as string[]), 'Pergunta', ...(answers as string[])])
    expect(view.messages.filter(message => message.kind === 'assistant').every(message => !message.streaming)).toBe(true)
    expect(JSON.stringify(view)).not.toContain('RAW_ONLY')
  })
  it('does not project unknown external text or stdout diagnostics as assistant messages', () => {
    expect(project([
      { ...event(1, ''), type: 'external.event', data: { type: 'external.raw', text: 'RAW_ONLY', raw: {} } },
      { ...event(2, ''), type: 'external.event', data: { type: 'external.stdout', text: 'LOG_ONLY', raw: null } },
    ]).messages).toEqual([])
  })
  it('interrupts legacy approvals and stops streaming before a resumed run', () => {
    const view = project([
      { ...event(1, 'Antes'), type: 'run.started', data: {} },
      { ...event(2, ''), type: 'approval.requested', data: { approvalId: 'a', toolCallId: 't', name: 'write', risk: 'write', arguments: {} } },
      event(3, 'Parcial'),
      { ...event(4, ''), type: 'run.interrupted', data: { reason: 'app_restart' } },
    ])
    expect(view.pendingApprovals).toEqual([])
    expect(view.activeRun).toBe('paused')
    expect(view.outcome).toEqual({ status: 'interrupted', reason: 'app_restart' })
    expect(view.messages[0]).toMatchObject({ text: 'Parcial', streaming: false })
  })
  it('projects ordered external text per run and never displays raw unknown data', () => {
    const entries = [
      ['external.run.started', { adapter: 'opencode' }],
      ['message.user', { role: 'user', content: 'Pergunta' }],
      ['external.event', { type: 'text', text: 'Olá', raw: { secret: 'RAW_ONLY' } }],
      ['external.event', { type: 'text', text: ' ', raw: {} }],
      ['external.event', { type: 'text', text: 'mundo', raw: {} }],
      ['external.session.bound', { sessionId: 'native' }],
      ['external.event', { type: 'unknown', raw: { text: 'RAW_ONLY' } }],
      ['external.run.completed', { adapter: 'opencode' }],
      ['external.run.started', { adapter: 'opencode' }],
      ['external.event', { type: 'text', text: 'Segunda', raw: {} }],
      ['run.interrupted', { reason: 'app_restart' }],
    ].map(([type, data], i) => ({ ...event(i + 1, ''), type: type as string, data }))
    const view = project(entries)
    expect(view.messages.map(message => message.kind !== 'tool' && message.text)).toEqual(['Pergunta', 'Olá mundo', 'Segunda'])
    expect(JSON.stringify(view)).not.toContain('RAW_ONLY')
  })
  it('closes legacy tool cards when interruption has no explicit tool closure', () => {
    const view = project([
      { ...event(1, ''), type: 'message.assistant', data: { content: '', toolCalls: [{ id: 't1', name: 'bash', arguments: {} }, { id: 't2', name: 'write', arguments: {} }] } },
      { ...event(2, ''), type: 'tool.called', data: { toolCallId: 't1', name: 'bash' } },
      { ...event(3, ''), type: 'approval.requested', data: { approvalId: 'a', toolCallId: 't2', name: 'write', risk: 'write', arguments: {} } },
      { ...event(4, ''), type: 'run.interrupted', data: { reason: 'app_restart' } },
    ])
    expect(view.messages[0]).toMatchObject({ call: { status: 'failed', errorCode: 'outcome_unknown' } })
    expect(view.messages[1]).toMatchObject({ call: { status: 'skipped', errorCode: 'not_executed' } })
  })
  it('keeps unknown prototype-named events in the journal without projecting an outcome', () => {
    const store = sessionStore()
    store.getState().receive([{ ...event(1, ''), type: 'constructor' }])
    expect(store.getState().outcome).toBeUndefined()
    expect(store.getState().events).toHaveLength(1)
    expect(store.getState().messages).toEqual([])
  })
  it('projects only the contiguous session prefix and fills pushed gaps from replay', () => {
    const store = sessionStore()
    store.getState().receive([event(1, '1'), event(5, '5')])
    expect(store.getState().contiguousSequence).toBe(1)
    expect(store.getState().messages[0]).toMatchObject({ text: '1' })
    store.getState().receive([event(2, '2'), event(3, '3'), event(4, '4')])
    expect(store.getState().contiguousSequence).toBe(5)
    expect(store.getState().messages[0]).toMatchObject({ text: '12345' })
    store.getState().setSession({ id: 'session-2', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata })
    expect(store.getState().contiguousSequence).toBe(0)
  })

  it('applies a duplicate delta only once within one batch', () => {
    const store = sessionStore()
    const delta = event(1, 'Olá')
    const batch = [delta, delta]
    Object.freeze(batch)

    store.getState().receive(batch)

    expect(store.getState().events).toEqual([delta])
    expect(store.getState().messages).toEqual([{ kind: 'assistant', id: delta.id, text: 'Olá', streaming: true }])
    expect(batch).toEqual([delta, delta])
  })

  it('deduplicates mixed batches and prior state while preserving sequence and input arrays', () => {
    const store = sessionStore()
    const first = event(1, 'Olá')
    const second = event(2, ', mundo')
    const third = event(3, '!')
    store.getState().receive([first])
    const previous = store.getState().events
    Object.freeze(previous)
    const batch = [third, second, third, first, { ...second, id: 'foreign', streamId: 'other-session' }]
    Object.freeze(batch)

    store.getState().receive(batch)

    expect(store.getState().events).toEqual([first, second, third])
    expect(store.getState().messages).toEqual([{ kind: 'assistant', id: first.id, text: 'Olá, mundo!', streaming: true }])
    expect(previous).toEqual([first])
    expect(batch.map(item => item.id)).toEqual(['event-3', 'event-2', 'event-3', 'event-1', 'foreign'])
  })
})
