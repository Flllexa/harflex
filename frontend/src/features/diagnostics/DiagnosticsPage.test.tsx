import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { DiagnosticsPage } from './DiagnosticsPage'

afterEach(cleanup)

describe('diagnósticos locais', () => {
  it('shows only verified local signals and does not perform network checks', async () => {
    const { backend } = createFakeBackend()
    const listWorkspaces = vi.fn(backend.listWorkspaces)
    backend.listWorkspaces = listWorkspaces
    backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
    backend.listProviderProfiles = async () => []
    backend.probeCredentialStore = async () => ({ status: 'ready', checked: 1, missing: 0 })
    backend.listMCPServers = async () => []
    const onSettings = vi.fn(), onMCP = vi.fn(), onProjects = vi.fn()
    render(<DiagnosticsPage backend={backend} workspaceId="workspace-1" onSettings={onSettings} onMCP={onMCP} onProjects={onProjects} />)
    expect(await screen.findByText('Operacional')).toBeInTheDocument()
    expect(screen.getByText('1 de 1 disponíveis')).toBeInTheDocument()
    expect(screen.getByText('1 referência(s) legíveis')).toBeInTheDocument()
    expect(screen.getByText(/autenticação na API não é testada/)).toBeInTheDocument()
    expect(screen.getByText(/disponibilidade atual só é testada/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Atualizar' }))
    await waitFor(() => expect(listWorkspaces).toHaveBeenCalledTimes(2))
    await userEvent.click(screen.getByRole('button', { name: 'Ver MCP' }))
    expect(onMCP).toHaveBeenCalledOnce()
  })

  it('reports a failed SQLite read instead of inventing healthy status', async () => {
    const { backend } = createFakeBackend()
    backend.listWorkspaces = async () => { throw new Error('offline') }
    render(<DiagnosticsPage backend={backend} onSettings={() => undefined} onMCP={() => undefined} onProjects={() => undefined} />)
    expect(await screen.findByText('Falha na leitura')).toBeInTheDocument()
    expect(screen.getByText('1 consulta(s) local(is) falharam')).toBeInTheDocument()
  })

  it('flags a missing key as a real local issue', async () => {
    const { backend } = createFakeBackend()
    backend.probeCredentialStore = async () => ({ status: 'degraded', checked: 1, missing: 1 })
    render(<DiagnosticsPage backend={backend} onSettings={() => undefined} onMCP={() => undefined} onProjects={() => undefined} />)
    expect(await screen.findByText('1 referência(s) ausente(s)')).toBeInTheDocument()
    expect(screen.getByText('1 consulta(s) local(is) falharam')).toBeInTheDocument()
  })
})
