import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { VaultPage } from './VaultPage'

afterEach(cleanup)

describe('Vault de referências', () => {
  it('lists credential references without reading secret values', async () => {
    const { backend } = createFakeBackend()
    backend.listProviderProfiles = async () => [{ id: 'local', name: 'Modelo local', kind: 'openai_compatible', providerType: 'lm_studio', baseUrl: 'http://127.0.0.1:1234/v1', model: 'local-model', hasCredential: true, endpointBlocked: false, updatedAt: '2026-09-25T10:00:00Z' }]
    backend.listMCPServers = async () => [{ id: 'mcp-12345678', workspaceId: 'workspace-1', name: 'Docs', transport: 'http', command: '', args: [], url: 'http://127.0.0.1:3333/mcp', tokenEnvVar: '', authScheme: 'bearer', enabled: true, tools: [], hasCredential: true, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z' }]
    const onSettings = vi.fn(), onMCP = vi.fn()
    render(<VaultPage backend={backend} workspaceId="workspace-1" onSettings={onSettings} onMCP={onMCP} onProjects={() => undefined} />)
    expect(await screen.findByText('2 referência(s) de credencial cadastrada(s)')).toBeInTheDocument()
    expect(screen.getByText('Modelo local')).toBeInTheDocument()
    expect(screen.getByText('Docs')).toBeInTheDocument()
    expect(screen.queryByText(/secret|token-value/i)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Gerenciar MCP' }))
    expect(onMCP).toHaveBeenCalledOnce()
    await userEvent.click(screen.getByRole('button', { name: 'Gerenciar provedores' }))
    expect(onSettings).toHaveBeenCalledOnce()
  })
})
