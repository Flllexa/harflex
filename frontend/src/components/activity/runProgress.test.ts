import { expect, it } from 'vitest'
import type { AgentEvent } from '../../lib/backend'
import { shellReadPaths } from './activityFlow'
import { buildRunProgress, formatElapsed, usageTotals } from './runProgress'

let sequence = 0
const at = (second: number) => new Date(Date.UTC(2026, 9, 16, 10, 0, second)).toISOString()
const event = (type: string, data: unknown = {}, second = 0): AgentEvent => ({ id: `e${++sequence}`, streamId: 's', sequence, type, data, createdAt: at(second) })
const call = (id: string, name: string, args: unknown, second = 0) => event('message.assistant', { role: 'assistant', content: '', toolCalls: [{ id, name, arguments: args }] }, second)
const raw = (value: unknown, second = 0) => event('external.event', { type: 'external.raw', raw: value }, second)

it('follows the latest run: the step, the counts, the files and the start', () => {
  const progress = buildRunProgress([
    event('run.started', {}, 0),
    event('message.user', { role: 'user', content: 'antigo' }, 0),
    call('old', 'read', { path: '/repo/old.go' }, 1),
    event('run.completed', { reason: '' }, 2),
    event('run.started', {}, 10),
    event('message.user', { role: 'user', content: 'Corrija a baixa' }, 10),
    call('r1', 'read', { path: '/repo/internal/baixa.go' }, 12),
    event('tool.called', { toolCallId: 'r1', name: 'read' }, 12),
    event('tool.completed', { toolCallId: 'r1', name: 'read', content: {} }, 13),
    event('usage.recorded', { inputTokens: 100, outputTokens: 10 }, 13),
    call('g1', 'grep', { query: 'contrato', path: '/repo' }, 14),
    event('tool.called', { toolCallId: 'g1', name: 'grep' }, 14),
  ])
  expect(progress.live).toBe(true)
  expect(progress.phase).toBe('acting')
  expect(progress.step).toBe('Buscando “contrato”')
  expect(progress.startedAt).toBe(Date.parse(at(10)))
  expect(progress.filesRead).toEqual(['/repo/internal/baixa.go'])
  expect(progress.counts).toMatchObject({ read: 1, search: 1, total: 2, failed: 0 })
  expect(progress.tokens).toEqual({ input: 100, output: 10 })
})

it('says what it waits for without reading the model text', () => {
  const started = [event('run.started', {}, 0), event('message.user', { role: 'user', content: 'oi' }, 0)]
  expect(buildRunProgress(started).step).toBe('Aguardando a primeira resposta do modelo')
  const writing = buildRunProgress([...started, event('assistant.delta', { delta: 'Primeiro vou pensar sobre…' }, 1)])
  expect(writing.phase).toBe('writing')
  expect(writing.step).toBe('Escrevendo a resposta')
  expect(writing.step).not.toContain('pensar')
  expect(writing.outputChars).toBe(26)
})

it('asks for approval by name and ends with the outcome and its time', () => {
  const asking = buildRunProgress([
    event('run.started', {}, 0),
    event('message.user', { role: 'user', content: 'edite' }, 0),
    call('w1', 'edit', { path: 'src/a.ts' }, 1),
    event('approval.requested', { approvalId: 'a', toolCallId: 'w1', name: 'edit', risk: 'write', arguments: {} }, 1),
  ])
  expect(asking.phase).toBe('approval')
  expect(asking.step).toBe('Aguardando sua aprovação: Editando a.ts')
  expect(asking.filesChanged).toEqual(['src/a.ts'])
  const failed = buildRunProgress([event('run.started', {}, 0), event('message.user', { role: 'user', content: 'x' }, 0), event('run.failed', { reason: 'timeout' }, 65)])
  expect(failed.live).toBe(false)
  expect(failed.step).toBe('Falhou')
  expect((failed.endedAt ?? 0) - (failed.startedAt ?? 0)).toBe(65_000)
})

it('reads Codex and Claude Code runs: shell reads, file changes and turn usage', () => {
  const codex = buildRunProgress([
    event('external.run.started', { adapter: 'codex' }, 0),
    event('message.user', { role: 'user', content: 'faça' }, 0),
    raw({ type: 'item.completed', item: { id: 'c1', type: 'command_execution', command: "/bin/zsh -lc \"sed -n '1,80p' internal/app.go && cat README.md\"", exit_code: 0, status: 'completed' } }, 1),
    raw({ type: 'item.completed', item: { id: 'f', type: 'file_change', changes: [{ path: '/repo/a.ts', kind: 'update' }], status: 'completed' } }, 2),
    raw({ type: 'item.completed', item: { id: 'r', type: 'reasoning', text: 'segredo do modelo' } }, 3),
    raw({ type: 'turn.completed', usage: { input_tokens: 50, output_tokens: 5 } }, 4),
    event('usage.recorded', { inputTokens: 50, outputTokens: 5 }, 4),
    event('external.run.completed', { adapter: 'codex' }, 4),
  ])
  expect(codex.filesRead).toEqual(['internal/app.go', 'README.md'])
  expect(codex.filesChanged).toEqual(['/repo/a.ts'])
  expect(codex.tokens).toEqual({ input: 50, output: 5 })
  expect(JSON.stringify(codex)).not.toContain('segredo')

  const claude = buildRunProgress([
    event('external.run.started', { adapter: 'claude' }, 0),
    event('message.user', { role: 'user', content: 'leia' }, 0),
    raw({ type: 'assistant', message: { content: [{ type: 'thinking', thinking: 'não mostrar' }, { type: 'tool_use', id: 't1', name: 'Read', input: { file_path: '/repo/go.mod' } }] } }, 1),
  ])
  expect(claude.step).toBe('Lendo go.mod')
  expect(claude.filesRead).toEqual(['/repo/go.mod'])
  expect(JSON.stringify(claude)).not.toContain('não mostrar')
})

it('counts each model call once, with its last usage snapshot', () => {
  expect(usageTotals([
    event('usage.recorded', { inputTokens: 10, outputTokens: 1 }),
    event('usage.recorded', { inputTokens: 10, outputTokens: 4 }),
    call('a', 'read', { path: 'x' }),
    event('usage.recorded', { inputTokens: 30, outputTokens: 6 }),
    event('message.assistant', { role: 'assistant', content: 'fim' }),
  ])).toEqual({ input: 40, output: 10 })
  expect(usageTotals([event('run.started')])).toBeUndefined()
})

it('names the files a shell command only prints', () => {
  expect(shellReadPaths("sed -n '1,40p' src/a.ts | head -n 20 && cat -n docs/b.md 2>&1")).toEqual(['src/a.ts', 'docs/b.md'])
  expect(shellReadPaths("sed -i '' 's/a/b/' src/a.ts")).toEqual([])
  expect(shellReadPaths('npm test')).toEqual([])
})

it('formats the elapsed time', () => {
  expect(formatElapsed(4_200)).toBe('4 s')
  expect(formatElapsed(125_000)).toBe('2 min 05 s')
  expect(formatElapsed(3_780_000)).toBe('1 h 03 min')
})
