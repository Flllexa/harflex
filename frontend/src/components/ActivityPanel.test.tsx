import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { AgentEvent } from '../lib/backend'
import { ActivityPanel } from './ActivityPanel'

afterEach(cleanup)

const event = (id: string, sequence: number, type: string, data: unknown = {}): AgentEvent => ({ id, streamId: 'session-1', sequence, type, data, createdAt: '2026-09-29T10:00:00Z' })

const planned = [
  event('user', 1, 'message.user', { role: 'user', content: 'Crie a lista de tarefas' }),
  event('plan', 2, 'message.assistant', { role: 'assistant', content: '', toolCalls: [{ id: 'p', name: 'update_plan', arguments: { plan: [{ step: 'Ler o projeto', status: 'completed' }, { step: 'Escrever o módulo', status: 'in_progress' }, { step: 'Testar', status: 'pending' }] } }] }),
  event('call', 3, 'message.assistant', { role: 'assistant', content: '', toolCalls: [{ id: 'w', name: 'write', arguments: { path: 'src/todo.js', content: 'SHOULD_NOT_APPEAR' } }] }),
  event('started', 4, 'tool.called', { toolCallId: 'w', name: 'write' }),
]

it('draws the plan with the action running under the current step and announces it', () => {
  render(<ActivityPanel onClose={vi.fn()} events={planned} busy />)

  expect(screen.getByText('Em execução · ações atualizadas ao vivo')).toHaveAttribute('aria-live', 'polite')
  expect(screen.getByText('Agora: Escrevendo todo.js')).toHaveAttribute('aria-live', 'polite')
  expect(screen.getByRole('progressbar', { name: 'Progresso do plano' })).toHaveAttribute('aria-valuenow', '1')
  expect(screen.getByText('1 de 3 etapas')).toBeInTheDocument()
  const plan = screen.getByRole('list', { name: 'Plano de execução' })
  expect(within(plan).getByText(/Etapa 2: Escrever o módulo \(em andamento\)/)).toBeInTheDocument()
  expect(within(plan).getByText('Escrevendo todo.js')).toBeInTheDocument()
  expect(document.querySelector('.flow-step.is-in_progress.is-live')).toHaveTextContent('Escrever o módulo')
  expect(document.querySelector('.flow-action.is-running')).toHaveTextContent('Escrevendo todo.js')
  expect(screen.queryByText('SHOULD_NOT_APPEAR')).not.toBeInTheDocument()
})

it('shows approval waiting without presenting it as active execution', () => {
  render(<ActivityPanel onClose={vi.fn()} busy events={[...planned.slice(0, 3), event('approval', 4, 'approval.requested', { approvalId: 'a', toolCallId: 'w', name: 'write', risk: 'write', arguments: {} })]} />)

  expect(screen.getByText('Aguardando aprovação')).toBeInTheDocument()
  expect(document.querySelector('.flow-action.is-approval')).toHaveTextContent('Aprovação solicitada')
  expect(document.querySelector('.flow-step.is-live')).toBeNull()
})

it('chains actions under the request when the agent has no plan, and says when nothing ran', () => {
  const { rerender } = render(<ActivityPanel onClose={vi.fn()} events={[]} />)
  expect(screen.getByText('Nenhuma atividade nesta sessão')).toBeInTheDocument()

  rerender(<ActivityPanel onClose={vi.fn()} events={[event('user', 1, 'message.user', { role: 'user', content: 'oi' }), event('c', 2, 'message.assistant', { role: 'assistant', content: '', toolCalls: [{ id: 'g', name: 'grep', arguments: { query: 'TODO' } }] }), event('done', 3, 'run.completed', { reason: 'stop' })]} />)
  expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  expect(document.querySelector('.flow-request')).toHaveTextContent('oi')
  expect(document.querySelector('.flow-action.is-done')).toHaveTextContent('Buscando “TODO”')
  expect(document.querySelector('.flow-outcome.is-completed')).toHaveTextContent('Concluído')
})

describe('running activities', () => {
  const session = (id: string, title: string) => ({ id, workspaceId: 'workspace-1', backendId: 'local', status: 'running', resumable: true, createdAt: '2026-10-18T10:00:00Z', updatedAt: '2026-10-18T10:00:00Z', title, purpose: 'chat' as const })
  it('lists two running activities and opens the other one', async () => {
    const onOpenRunning = vi.fn()
    const running = [{ session: session('chat-a', 'Chat A'), pipelineId: '', role: '' as const }, { session: session('coder-b', 'Coder B'), pipelineId: 'pipeline-1', role: 'coder' as const }]
    render(<ActivityPanel onClose={() => undefined} events={[]} running={running} focusedId="chat-a" onOpenRunning={onOpenRunning} />)
    const list = screen.getByRole('list', { name: 'Em execução · 2' })
    expect(within(list).getByRole('button', { name: /Chat A/ })).toHaveAttribute('aria-current', 'true')
    await userEvent.setup().click(within(list).getByRole('button', { name: /Coder B/ }))
    expect(onOpenRunning).toHaveBeenCalledWith(running[1])
  })
  it('shows no list when only the focused activity runs', () => {
    render(<ActivityPanel onClose={() => undefined} events={[]} running={[{ session: session('chat-a', 'Chat A'), pipelineId: '', role: '' }]} focusedId="chat-a" onOpenRunning={() => undefined} />)
    expect(screen.queryByRole('list', { name: /Em execução/ })).not.toBeInTheDocument()
  })
})

it('stops whatever it is following while it runs', async () => {
  const onStop = vi.fn()
  render(<ActivityPanel onClose={vi.fn()} events={planned} busy onStop={onStop} />)
  await userEvent.click(screen.getByRole('button', { name: 'Parar' }))
  expect(onStop).toHaveBeenCalled()
  cleanup()
  render(<ActivityPanel onClose={vi.fn()} events={planned} onStop={onStop} />)
  expect(screen.queryByRole('button', { name: 'Parar' })).not.toBeInTheDocument()
})

// A reply that left watchers is not over: the flow waits for them, and says done only when every one came back.
describe('a chat waiting on its watchers', () => {
  const finished = [
    event('user', 1, 'message.user', { role: 'user', content: 'Acompanhe a ingestão' }),
    event('done', 2, 'run.completed', { reason: '' }),
  ]
  const watcher = (id: string, overrides: object = {}) => ({ id, sessionId: 'session-1', chatTitle: 'Graphiti', title: `vigia ${id}`, command: 'true', intervalSeconds: 60, status: 'watching' as const,
    reason: '', lastOutput: '', lastExitCode: 1, checks: 2, delivered: false, nextCheckAt: '2026-10-22T10:00:42Z', deadlineAt: '2026-10-22T12:00:00Z', createdAt: '2026-10-22T09:50:00Z', ...overrides })
  const now = new Date('2026-10-22T10:00:00Z')

  it('waits with a countdown while a watcher of this chat runs', () => {
    render(<ActivityPanel onClose={vi.fn()} events={finished} focusedId="session-1" watcherNow={now} onStopWatcher={vi.fn()} onOpenWatcher={vi.fn()}
      watchers={[watcher('a'), watcher('b', { nextCheckAt: '2026-10-22T10:00:12Z' }), watcher('other', { sessionId: 'session-2' })]} />)
    expect(document.querySelector('.flow-outcome')).toHaveTextContent('Aguardando 2 vigias · próxima em 12 s')
    expect(document.querySelector('.flow-outcome.is-waiting')).toBeInTheDocument()
    expect(screen.getByText('Aguardando as vigias deste chat')).toBeInTheDocument()
  })

  it('says done once every watcher came back', () => {
    render(<ActivityPanel onClose={vi.fn()} events={finished} focusedId="session-1" watcherNow={now} onStopWatcher={vi.fn()} onOpenWatcher={vi.fn()}
      watchers={[watcher('a', { status: 'fired', delivered: true }), watcher('other', { sessionId: 'session-2' })]} />)
    expect(document.querySelector('.flow-outcome.is-completed')).toBeInTheDocument()
  })
})
