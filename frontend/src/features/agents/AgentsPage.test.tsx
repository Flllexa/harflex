import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { Agent, Delegation } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { AgentsPage } from './AgentsPage'

afterEach(() => { cleanup(); localStorage.clear() })

const date = '2026-09-26T10:00:00Z'
const agent: Agent = { id: 'agent-1', name: 'Analista', description: '', instructions: 'Leia', backendId: 'local', allowedTools: ['read'], createdAt: date, updatedAt: date }
const base: Delegation = { id: 'delegation-1', parentSessionId: 'parent-session', childSessionId: 'child-running', agentId: agent.id, taskPrompt: 'Revise os critérios.', promptCount: 1, promptLimit: 3, timeoutSeconds: 300, depth: 1, createdAt: date, status: 'running', result: '', errorCode: '' }

function props(backend: ReturnType<typeof createFakeBackend>['backend'], sessionId: string, open = vi.fn(async () => undefined)) {
  return { backend, backends: [{ id: 'local', name: 'Local', kind: 'api' as const, available: true }], workspaceId: 'workspace-1', parentSessionId: sessionId,
    onStart: async () => undefined, onDelegate: async () => undefined, onOpenSession: open, onProjects: () => undefined, onSettings: () => undefined }
}

it('mantém indisponíveis sem seleção e permite escolher CLI explicitamente', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  render(<AgentsPage {...props(backend, 'parent-session')} backends={[
    { id: 'local', name: 'Local', kind: 'api', available: true },
    { id: 'codex', name: 'Codex', kind: 'cli', available: true },
    { id: 'off', name: 'Indisponível', kind: 'api', available: false },
  ]} />)
  const picker = screen.getByTestId('picker-agent-backend')
  await user.click(within(picker).getByRole('button'))
  expect(screen.getByRole('option', { name: 'Indisponível · indisponível' })).toHaveAttribute('aria-disabled', 'true')
  await user.click(screen.getByRole('option', { name: 'Codex' }))
  expect(within(picker).getByRole('button')).toHaveTextContent('Codex')
})

it('shows persisted children with journal outcomes and reads back child cancellation', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const open = vi.fn(async () => undefined)
  let links: Delegation[] = [base, { ...base, id: 'delegation-2', childSessionId: 'child-done', status: 'completed', result: 'Revisão concluída.' }]
  backend.listAgents = async () => [agent]
  backend.getParentDelegation = async () => null
  backend.listDelegations = vi.fn(async () => links)
  backend.cancel = vi.fn(async () => { links = links.map(link => link.childSessionId === base.childSessionId ? { ...link, status: 'cancelled' } : link) })
  render(<AgentsPage {...props(backend, 'parent-session', open)} />)
  expect(await screen.findByText('Revisão concluída.')).toBeInTheDocument()
  expect(screen.getByText('Em execução')).toBeInTheDocument()
  expect(screen.getAllByText(/1 de 3 chamadas · 5 min por trecho ativo/).length).toBeGreaterThan(0)
  await user.click(screen.getByRole('button', { name: 'Abrir conversa do subagente child-ru' }))
  expect(open).toHaveBeenCalledWith('child-running')
  await user.click(screen.getByRole('button', { name: 'Cancelar subagente child-ru' }))
  await waitFor(() => expect(screen.getByText('Cancelado')).toBeInTheDocument())
  expect(backend.cancel).toHaveBeenCalledWith('child-running')
  expect(backend.listDelegations).toHaveBeenCalledWith('parent-session')
})

it('recovers the parent path from the persisted link after selecting a child', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const open = vi.fn(async () => undefined)
  backend.listAgents = async () => [agent]
  backend.getParentDelegation = vi.fn(async childId => childId === 'child-running' ? base : null)
  backend.listDelegations = vi.fn(async parentId => parentId === 'parent-session' ? [base] : [])
  render(<AgentsPage {...props(backend, 'child-running', open)} />)
  await user.click(await screen.findByRole('button', { name: 'Voltar para sessão pai' }))
  expect(open).toHaveBeenCalledWith('parent-session')
  expect(backend.listDelegations).toHaveBeenCalledWith('parent-session')
})

it('offers the persisted redacted task for manual recovery without auto-running', async () => {
  const { backend } = createFakeBackend()
  backend.listAgents = async () => [agent]
  backend.getParentDelegation = async () => null
  backend.listDelegations = async () => [{ ...base, status: 'ready', taskPrompt: 'Revise [REDACTED]' }]
  render(<AgentsPage {...props(backend, 'parent-session')} />)
  expect(await screen.findByText(/Revise \[REDACTED\]/)).toBeInTheDocument()
  expect(screen.getByText(/use “Preparar tarefa delegada”/i)).toBeInTheDocument()
})

it('explains the call and time limits before delegation', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listAgents = async () => [agent]
  backend.getParentDelegation = async () => null
  render(<AgentsPage {...props(backend, 'parent-session')} />)
  await user.click(await screen.findByRole('button', { name: 'Delegar a Analista' }))
  expect(screen.getByText(/3 chamadas por subagente, 5 min por execução/)).toBeInTheDocument()
  expect(screen.getByText(/não são limites de custo ou tokens/i)).toBeInTheDocument()
})

it('retains only the request token across a failed delegation and remount', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listAgents = async () => [agent]
  backend.getParentDelegation = async () => null
  const onDelegate = vi.fn().mockRejectedValueOnce(new Error('transport failed')).mockResolvedValue(undefined)
  const first = render(<AgentsPage {...props(backend, 'parent-session')} onDelegate={onDelegate} />)
  await user.click(await screen.findByRole('button', { name: 'Delegar a Analista' }))
  await user.type(screen.getByLabelText('Tarefa para Analista'), 'Revisar arquivo')
  await user.click(screen.getByRole('button', { name: 'Iniciar subagente' }))
  await screen.findByRole('alert')
  const firstID = onDelegate.mock.calls[0][2]
  expect(firstID).toMatch(/^[a-f0-9]{32}$/)
  expect(JSON.stringify(localStorage)).not.toContain('Revisar arquivo')
  first.unmount()

  const second = render(<AgentsPage {...props(backend, 'parent-session')} onDelegate={onDelegate} />)
  await user.click(await screen.findByRole('button', { name: 'Delegar a Analista' }))
  expect(screen.getByText(/tentativa pendente/i)).toBeInTheDocument()
  await user.type(screen.getByLabelText('Tarefa para Analista'), 'Revisar arquivo')
  await user.click(screen.getByRole('button', { name: 'Iniciar subagente' }))
  await waitFor(() => expect(onDelegate).toHaveBeenCalledTimes(2))
  expect(onDelegate.mock.calls[1][2]).toBe(firstID)
  await waitFor(() => expect(localStorage.length).toBe(0))
  second.unmount()

  render(<AgentsPage {...props(backend, 'parent-session')} onDelegate={onDelegate} />)
  await user.click(await screen.findByRole('button', { name: 'Delegar a Analista' }))
  expect(screen.queryByText(/tentativa pendente/i)).not.toBeInTheDocument()
  await user.type(screen.getByLabelText('Tarefa para Analista'), 'Revisar arquivo')
  await user.click(screen.getByRole('button', { name: 'Iniciar subagente' }))
  await waitFor(() => expect(onDelegate).toHaveBeenCalledTimes(3))
  expect(onDelegate.mock.calls[2][2]).not.toBe(firstID)
})

it('keeps the newest journal readback when event requests finish out of order', async () => {
  const { backend } = createFakeBackend()
  backend.listAgents = async () => [agent]
  backend.getParentDelegation = async () => null
  let calls = 0
  let resolveOlder: (links: Delegation[]) => void = () => undefined
  backend.listDelegations = vi.fn(async () => {
    calls++
    if (calls === 1) return [base]
    if (calls === 2) return new Promise<Delegation[]>(resolve => { resolveOlder = resolve })
    return [{ ...base, status: 'completed' as const, result: 'Resposta atual.' }]
  })
  const listeners = new Set<Parameters<typeof backend.onEvent>[0]>()
  backend.onEvent = listener => { listeners.add(listener); return () => listeners.delete(listener) }
  render(<AgentsPage {...props(backend, 'parent-session')} />)
  await screen.findByText('Em execução')
  const event = (type: string, sequence: number) => ({ id: `event-${sequence}`, streamId: base.childSessionId, sequence, type, data: {}, createdAt: date })
  act(() => { listeners.forEach(listener => listener(event('run.started', 1))); listeners.forEach(listener => listener(event('run.completed', 2))) })
  await screen.findByText('Resposta atual.')
  await act(async () => { resolveOlder([base]) })
  expect(screen.getByText('Concluído')).toBeInTheDocument()
})

it('reads terminal state again when a child completes during the initial fetch', async () => {
  const { backend } = createFakeBackend()
  backend.listAgents = async () => [agent]
  backend.getParentDelegation = async () => null
  const listeners = new Set<Parameters<typeof backend.onEvent>[0]>()
  backend.onEvent = listener => { listeners.add(listener); return () => listeners.delete(listener) }
  let calls = 0
  backend.listDelegations = vi.fn(async () => {
    calls++
    if (calls === 1) {
      listeners.forEach(listener => listener({ id: 'terminal-1', streamId: base.childSessionId, sequence: 2, type: 'run.completed', data: { reason: '' }, createdAt: date }))
      return [base]
    }
    return [{ ...base, status: 'completed' as const, result: 'Estado terminal relido.' }]
  })
  render(<AgentsPage {...props(backend, 'parent-session')} />)
  expect(await screen.findByText('Estado terminal relido.')).toBeInTheDocument()
  expect(backend.listDelegations).toHaveBeenCalledTimes(2)
})

it('leads with the saved agents and keeps the creation form one click away', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listAgents = async () => [agent]
  render(<AgentsPage {...props(backend, '')} parentSessionId={undefined} />)
  expect(await screen.findByText('Analista')).toBeInTheDocument()
  expect(screen.queryByLabelText('Nome do agente')).not.toBeInTheDocument()
  const toggle = screen.getByRole('button', { name: 'Novo agente' })
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await user.click(toggle)
  expect(await screen.findByLabelText('Nome do agente')).toHaveValue('')
  expect(screen.getByRole('button', { name: 'Fechar formulário' })).toHaveAttribute('aria-expanded', 'true')
  await user.click(screen.getByRole('button', { name: 'Fechar formulário' }))
  expect(screen.queryByLabelText('Nome do agente')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Editar Analista' }))
  expect(await screen.findByLabelText('Nome do agente')).toHaveValue('Analista')
})

it('shows the creation form straight away when there is no agent yet', async () => {
  const { backend } = createFakeBackend()
  backend.listAgents = async () => []
  render(<AgentsPage {...props(backend, '')} parentSessionId={undefined} />)
  expect(await screen.findByText('Nenhum agente salvo')).toBeInTheDocument()
  expect(screen.getByLabelText('Nome do agente')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Novo agente' })).not.toBeInTheDocument()
})

it('closes the form after saving so the new agent is visible', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listAgents = async () => [agent]
  backend.saveAgent = vi.fn(async input => ({ ...agent, id: 'agent-2', name: input.name, instructions: input.instructions }))
  render(<AgentsPage {...props(backend, '')} parentSessionId={undefined} />)
  await user.click(await screen.findByRole('button', { name: 'Novo agente' }))
  await user.type(await screen.findByLabelText('Nome do agente'), 'Revisor')
  await user.type(screen.getByLabelText('Instruções do agente'), 'Revise o diff.')
  await user.click(screen.getByRole('button', { name: 'Salvar agente' }))
  expect(await screen.findByText('Agente Revisor salvo localmente.')).toBeInTheDocument()
  expect(screen.queryByLabelText('Nome do agente')).not.toBeInTheDocument()
  expect(screen.getByText('Revisor')).toBeInTheDocument()
})
