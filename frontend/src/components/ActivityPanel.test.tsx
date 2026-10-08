import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
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
