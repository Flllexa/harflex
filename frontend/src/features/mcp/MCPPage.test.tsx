import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { MCPServer } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { MCPPage } from './MCPPage'

afterEach(cleanup)

it('makes stdio credential delivery explicit and never renders the token after saving', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const token = 'synthetic-ui-mcp-secret'
  let saved: MCPServer[] = []
  const saveMCPServer = vi.fn(async (input: Parameters<typeof backend.saveMCPServer>[0]) => {
    const { token: _, ...config } = input
    const server = { ...config, id: 'mcp-server-1', enabled: false, tools: [], hasCredential: true, createdAt: '2026-09-26T10:00:00Z', updatedAt: '2026-09-26T10:00:00Z' } as MCPServer
    saved = [server]
    return server
  })
  Object.assign(backend, { listMCPServers: async () => saved, saveMCPServer })
  const { container } = render(<MCPPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  await user.click(within(screen.getByTestId('picker-mcp-transport')).getByRole('button'))
  await user.click(screen.getByRole('option', { name: 'Processo local (stdio)' }))
  expect(screen.getByLabelText('Executável absoluto')).toBeVisible()
  await user.type(screen.getByLabelText('Nome do servidor'), 'Servidor local')
  await user.type(screen.getByLabelText('Executável absoluto'), '/usr/local/bin/servidor-mcp')
  await user.type(screen.getByLabelText('Nome da variável de ambiente do token'), 'MCP_TOKEN')
  await user.type(screen.getByLabelText('Token de acesso (opcional)'), token)
  expect(screen.getByText(/processo local pode ler o token/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Salvar servidor' }))
  expect(saveMCPServer).toHaveBeenCalledWith(expect.objectContaining({ transport: 'stdio', tokenEnvVar: 'MCP_TOKEN', token }))
  expect(await screen.findByText(/Token via MCP_TOKEN ao iniciar/)).toBeInTheDocument()
  expect(container.innerHTML).not.toContain(token)
  await user.click(screen.getByRole('button', { name: 'Editar' }))
  expect(screen.getByLabelText('Nome da variável de ambiente do token')).toHaveValue('MCP_TOKEN')
  expect(screen.getByLabelText('Token de acesso (opcional)')).toHaveValue('')
})

it('leads with registered servers and opens the creation form on demand', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const server = { id: 'mcp-1', workspaceId: 'workspace-1', name: 'Docs', transport: 'http', command: '', args: [], url: 'https://docs.example/mcp', tokenEnvVar: '', authScheme: 'bearer', enabled: false, tools: [], hasCredential: false, createdAt: '2026-09-26T10:00:00Z', updatedAt: '2026-09-26T10:00:00Z' } as MCPServer
  backend.listMCPServers = async () => [server]
  render(<MCPPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  expect(await screen.findByText('Docs')).toBeInTheDocument()
  expect(screen.queryByLabelText('Nome do servidor')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Novo servidor' }))
  expect(await screen.findByLabelText('Nome do servidor')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Fechar formulário' }))
  expect(screen.queryByLabelText('Nome do servidor')).not.toBeInTheDocument()
})

it('shows the server form immediately when nothing is registered', async () => {
  const { backend } = createFakeBackend()
  backend.listMCPServers = async () => []
  render(<MCPPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  expect(await screen.findByText('Nenhum servidor cadastrado')).toBeInTheDocument()
  expect(screen.getByLabelText('Nome do servidor')).toBeVisible()
  expect(screen.queryByRole('button', { name: 'Novo servidor' })).not.toBeInTheDocument()
})
