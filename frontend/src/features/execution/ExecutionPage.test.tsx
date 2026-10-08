import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { ExecutionPage } from './ExecutionPage'

afterEach(cleanup)

describe('perfil de execução', () => {
  it('shows journal-derived tokens, filters sessions and keeps cost unknown', async () => {
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    backend.listSessions = async () => [
      { id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'completed', resumable: true, createdAt: date, updatedAt: date },
      { id: 'session-2', workspaceId: 'workspace-1', backendId: 'codex', status: 'completed', resumable: true, createdAt: date, updatedAt: '2026-09-25T11:00:00Z' },
    ]
    backend.listEvents = async sessionId => sessionId === 'session-2' ? [
      { id: 'usage-1', streamId: 'session-2', sequence: 1, type: 'usage.recorded', data: { inputTokens: 120, outputTokens: 45 }, createdAt: date },
    ] : []
    const onOpenSession = vi.fn(async () => undefined)
    render(<ExecutionPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} onOpenSession={onOpenSession} />)
    expect(await screen.findByText('120')).toBeInTheDocument()
    expect(screen.getByText('Custo não informado')).toBeInTheDocument()
    const filter = within(screen.getByTestId('picker-execution-backend-filter')).getByRole('combobox')
    await userEvent.type(filter, 'local')
    await userEvent.click(screen.getByRole('option', { name: 'local' }))
    await waitFor(() => expect(screen.getByText('Nenhuma métrica registrada')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Abrir conversa' }))
    expect(onOpenSession).toHaveBeenCalledWith('session-1')
  })
})
