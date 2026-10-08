import { useState } from 'react'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { WorkflowsPage } from './WorkflowsPage'

afterEach(cleanup)

it('requires a reviewed session and a recovery choice before retrying a paused step', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-26T10:00:00Z'
  const paused = { id: 'run-1', workflowId: 'workflow-1', workspaceId: 'workspace-1', backendId: 'local', steps: [{ name: 'Revisar', prompt: 'Revise' }], currentStep: 0, status: 'paused' as const, lastSessionId: 'session-1', createdAt: date, updatedAt: date }
  const recover = vi.fn(async () => ({ ...paused, status: 'ready' as const }))
  const runStep = vi.fn(async () => ({ ...paused, status: 'completed' as const, currentStep: 1 }))
  const openSession = vi.fn(async (_sessionId: string) => undefined)
  Object.assign(backend, { listWorkflowRuns: async () => [paused], listWorkflows: async () => [], resumeWorkflowRun: recover, runWorkflowStep: runStep })
  function Harness() {
    const [reviewedSessions, setReviewedSessions] = useState<ReadonlySet<string>>(new Set())
    return <WorkflowsPage backend={backend} backends={[{ id: 'local', name: 'Local', kind: 'api', available: true }]} workspaceId="workspace-1" reviewedSessions={reviewedSessions} onProjects={() => undefined} onSettings={() => undefined} onOpenSession={async sessionId => { await openSession(sessionId); setReviewedSessions(current => new Set(current).add(`workspace-1:${sessionId}`)) }} />
  }
  render(<Harness />)

  expect(await screen.findByText('Pausado')).toBeInTheDocument()
  const confirm = screen.getByRole('button', { name: 'Confirmar decisão' })
  expect(confirm).toBeDisabled()
  expect(screen.getByRole('checkbox', { name: /Revisei o histórico da sessão/ })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Abrir sessão da etapa' }))
  expect(screen.getByRole('checkbox', { name: /Revisei o histórico da sessão/ })).toBeEnabled()
  const decision = screen.getByTestId('picker-workflow-recovery-run-1')
  await user.click(within(decision).getByRole('button'))
  await user.click(screen.getByRole('option', { name: 'Preparar nova execução' }))
  expect(within(decision).getByRole('button')).toHaveTextContent('Preparar nova execução')
  await user.click(screen.getByRole('checkbox', { name: /Revisei o histórico da sessão/ }))
  expect(confirm).toBeEnabled()
  await user.click(confirm)
  expect(openSession).toHaveBeenCalledWith('session-1')
  expect(recover).toHaveBeenCalledWith({ runId: 'run-1', reviewedSessionId: 'session-1', choice: 'retry' })
  expect(runStep).not.toHaveBeenCalled()
  expect(await screen.findByText('Pronto')).toBeInTheDocument()
})

it('não inicia workflow com o único backend indisponível', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-27T10:00:00Z'
  backend.listWorkflows = async () => [{ id: 'workflow-1', workspaceId: 'workspace-1', name: 'Revisão', steps: [{ name: 'Revisar', prompt: 'Revise' }], revision: 1, createdAt: date, updatedAt: date }]
  backend.listWorkflowRuns = async () => []
  render(<WorkflowsPage backend={backend} backends={[{ id: 'off', name: 'Offline', kind: 'api', available: false }]} workspaceId="workspace-1" reviewedSessions={new Set()} onProjects={() => undefined} onSettings={() => undefined} onOpenSession={async () => undefined} />)
  const picker = await screen.findByTestId('picker-workflow-backend')
  await user.click(within(picker).getByRole('button'))
  expect(screen.getByRole('option', { name: 'Offline · indisponível' })).toHaveAttribute('aria-disabled', 'true')
  await user.keyboard('{Escape}')
  expect(screen.getByRole('button', { name: 'Iniciar Revisão' })).toBeDisabled()
})

it('leads with saved workflows and keeps the editor one click away', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-26T10:00:00Z'
  const saved = { id: 'workflow-1', workspaceId: 'workspace-1', name: 'Triagem diária', steps: [{ name: 'Resumir', prompt: 'Resuma as mudanças.' }], revision: 1, createdAt: date, updatedAt: date }
  Object.assign(backend, { listWorkflows: async () => [saved], listWorkflowRuns: async () => [] })
  render(<WorkflowsPage backend={backend} backends={[{ id: 'local', name: 'Local', kind: 'api', available: true }]} workspaceId="workspace-1" reviewedSessions={new Set()} onProjects={() => undefined} onSettings={() => undefined} onOpenSession={async () => undefined} />)
  expect(await screen.findByText('Triagem diária')).toBeInTheDocument()
  expect(screen.queryByLabelText('Nome do workflow')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Novo workflow' }))
  expect(await screen.findByLabelText('Nome do workflow')).toHaveValue('')
  await user.click(screen.getByRole('button', { name: 'Fechar formulário' }))
  expect(screen.queryByLabelText('Nome do workflow')).not.toBeInTheDocument()
})
