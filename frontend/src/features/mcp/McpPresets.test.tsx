import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Backend, MCPServer, MCPServerInput } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { MCPPage } from './MCPPage'
import { credentialFor, mcpPresets, presetOf } from './presets'

afterEach(cleanup)

const date = '2026-09-26T10:00:00Z'
const tool = (name: string) => ({ name, agentName: `mcp_${name}`, description: `Ferramenta ${name}`, schema: { type: 'object' } })

/** A backend that keeps the servers it is given, the way the real one does. */
function mcpBackend(initial: MCPServer[] = [], failConnectFor?: string) {
  const { backend } = createFakeBackend()
  let servers = initial
  const save = vi.fn(async (input: MCPServerInput): Promise<MCPServer> => {
    const { token, ...config } = input
    const previous = servers.find(item => item.id === input.id)
    const saved: MCPServer = { ...config, id: input.id ?? `mcp-${String(servers.length + 1).padStart(8, '0')}`, enabled: false, tools: [], hasCredential: !!token || !!previous?.hasCredential, createdAt: date, updatedAt: date }
    servers = [saved, ...servers.filter(item => item.id !== saved.id)]
    return saved
  })
  const connect = vi.fn(async (id: string): Promise<MCPServer> => {
    const found = servers.find(item => item.id === id)!
    if (found.name === failConnectFor) throw { cause: { code: 'mcp_connection_failed' } }
    const connected = { ...found, enabled: true, tools: [tool('create_pull_request'), tool('list_pull_requests')] }
    servers = servers.map(item => item.id === id ? connected : item)
    return connected
  })
  const disable = vi.fn(async (id: string): Promise<MCPServer> => {
    const disabled = { ...servers.find(item => item.id === id)!, enabled: false, tools: [] }
    servers = servers.map(item => item.id === id ? disabled : item)
    return disabled
  })
  Object.assign(backend, { listMCPServers: async () => servers, saveMCPServer: save, connectMCPServer: connect, disableMCPServer: disable })
  return { backend: backend as Backend, save, connect, disable }
}
const show = (backend: Backend) => render(<MCPPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
const card = (name: string) => screen.getByRole('listitem', { name })

describe('ready-made MCP servers', () => {
  it('offers GitHub and Bitbucket before anything is registered', async () => {
    show(mcpBackend().backend)
    expect(await screen.findByRole('heading', { name: 'Servidores prontos' })).toBeInTheDocument()
    for (const name of ['GitHub', 'Bitbucket']) {
      expect(within(card(name)).getByText('Não configurado')).toBeInTheDocument()
      expect(within(card(name)).getByRole('button', { name: `Configurar ${name}` })).toBeEnabled()
    }
  })

  it('connects GitHub with a token: saves the fixed address as Bearer, then connects', async () => {
    const user = userEvent.setup()
    const { backend, save, connect } = mcpBackend()
    const { container } = show(backend)
    await user.click(await screen.findByRole('button', { name: 'Configurar GitHub' }))
    const form = screen.getByRole('form', { name: 'Credencial do GitHub' })
    expect(within(form).getByText(/github.com\/settings\/tokens/)).toBeInTheDocument()
    expect(within(form).queryByLabelText('E-mail da conta Atlassian')).not.toBeInTheDocument()
    expect(within(form).getByRole('button', { name: 'Salvar e conectar GitHub' })).toBeDisabled()
    await user.type(within(form).getByLabelText('Token de acesso pessoal'), 'ghp_synthetic_token')
    await user.click(within(form).getByRole('button', { name: 'Salvar e conectar GitHub' }))

    expect(save).toHaveBeenCalledWith({ id: undefined, workspaceId: 'workspace-1', name: 'GitHub', transport: 'http', url: 'https://api.githubcopilot.com/mcp/', command: '', args: [], tokenEnvVar: '', authScheme: 'bearer', token: 'ghp_synthetic_token' })
    expect(connect).toHaveBeenCalledWith('mcp-00000001')
    expect(await screen.findByText(/GitHub: conexão validada e 2 ferramenta\(s\)/)).toBeInTheDocument()
    expect(within(card('GitHub')).getByText('Ativo')).toBeInTheDocument()
    expect(within(card('GitHub')).getByRole('button', { name: 'Desativar GitHub' })).toBeInTheDocument()
    expect(screen.queryByRole('form', { name: 'Credencial do GitHub' })).not.toBeInTheDocument()
    expect(container.innerHTML).not.toContain('ghp_synthetic_token')
    // The server is managed from its card, not repeated in the list of custom servers.
    expect(screen.queryAllByRole('button', { name: 'Editar' })).toHaveLength(0)
    expect(screen.getAllByRole('button', { name: 'Desativar GitHub' })).toHaveLength(1)
  })

  it('connects Bitbucket with an Atlassian e-mail and API token sent as Basic', async () => {
    const user = userEvent.setup()
    const { backend, save } = mcpBackend()
    const { container } = show(backend)
    await user.click(await screen.findByRole('button', { name: 'Configurar Bitbucket' }))
    const form = screen.getByRole('form', { name: 'Credencial do Bitbucket' })
    expect(within(form).getByText(/read:bitbucket:agent-interface e write:bitbucket:agent-interface/)).toBeInTheDocument()
    expect(within(form).getByText(/administrador da organização/)).toBeInTheDocument()
    await user.type(within(form).getByLabelText('Token de API'), 'atlassian-api-token')
    expect(within(form).getByRole('button', { name: 'Salvar e conectar Bitbucket' })).toBeDisabled()
    await user.type(within(form).getByLabelText('E-mail da conta Atlassian'), ' ana@empresa.com ')
    await user.click(within(form).getByRole('button', { name: 'Salvar e conectar Bitbucket' }))
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ name: 'Bitbucket', url: 'https://mcp.atlassian.com/v2/mcp', authScheme: 'basic', token: 'ana@empresa.com:atlassian-api-token' }))
    expect(await within(card('Bitbucket')).findByText('Ativo')).toBeInTheDocument()
    expect(container.innerHTML).not.toContain('atlassian-api-token')
  })

  it('keeps the server saved and says what to check when the connection is refused', async () => {
    const user = userEvent.setup()
    const { backend, connect } = mcpBackend([], 'Bitbucket')
    show(backend)
    await user.click(await screen.findByRole('button', { name: 'Configurar Bitbucket' }))
    await user.type(screen.getByLabelText('E-mail da conta Atlassian'), 'ana@empresa.com')
    await user.type(screen.getByLabelText('Token de API'), 'token-errado')
    await user.click(screen.getByRole('button', { name: 'Salvar e conectar Bitbucket' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Bitbucket foi salvo, mas a conexão falhou.')
    expect(alert).toHaveTextContent('administrador liberou a autenticação por token de API')
    // Saved and disabled: a retry is one click, without typing the credential again.
    expect(within(card('Bitbucket')).getByText('Desativado')).toBeInTheDocument()
    expect(screen.queryByRole('form', { name: 'Credencial do Bitbucket' })).not.toBeInTheDocument()
    expect(within(card('Bitbucket')).getByRole('button', { name: 'Conectar Bitbucket' })).toBeEnabled()
    await user.click(within(card('Bitbucket')).getByRole('button', { name: 'Conectar Bitbucket' }))
    expect(connect).toHaveBeenCalledTimes(2)
  })

  it('lists the tools of a connected server and replaces its credential in place', async () => {
    const user = userEvent.setup()
    const existing: MCPServer = { id: 'mcp-github-1', workspaceId: 'workspace-1', name: 'GitHub', transport: 'http', command: '', args: [], url: 'https://api.githubcopilot.com/mcp', tokenEnvVar: '', authScheme: 'bearer', enabled: true, tools: [tool('create_pull_request')], hasCredential: true, createdAt: date, updatedAt: date }
    const { backend, save, disable } = mcpBackend([existing])
    show(backend)
    const github = await screen.findByRole('listitem', { name: 'GitHub' })
    expect(within(github).getByText('Ativo')).toBeInTheDocument()
    await user.click(within(github).getByText('Ver as 1 ferramentas'))
    expect(within(github).getByText('create_pull_request')).toBeVisible()
    await user.click(within(github).getByRole('button', { name: 'Trocar credencial do GitHub' }))
    await user.type(screen.getByLabelText('Token de acesso pessoal'), 'ghp_novo')
    await user.click(screen.getByRole('button', { name: 'Salvar e conectar GitHub' }))
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ id: 'mcp-github-1', name: 'GitHub', token: 'ghp_novo', authScheme: 'bearer' }))
    await user.click(await within(card('GitHub')).findByRole('button', { name: 'Desativar GitHub' }))
    expect(disable).toHaveBeenCalledWith('mcp-github-1')
  })

  it('leaves the other servers in their own list, with the way their credential is sent', async () => {
    const custom: MCPServer = { id: 'mcp-custom-1', workspaceId: 'workspace-1', name: 'Docs internos', transport: 'http', command: '', args: [], url: 'https://docs.example/mcp', tokenEnvVar: '', authScheme: 'basic', enabled: false, tools: [], hasCredential: true, createdAt: date, updatedAt: date }
    show(mcpBackend([custom]).backend)
    expect(await screen.findByText('Docs internos')).toBeInTheDocument()
    expect(screen.getByText(/Credencial enviada como Basic/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Conectar Docs internos' })).toBeInTheDocument()
  })

  it('lets a custom server use a Basic credential', async () => {
    const user = userEvent.setup()
    const { backend, save } = mcpBackend()
    show(backend)
    await user.type(await screen.findByLabelText('Nome do servidor'), 'Interno')
    await user.type(screen.getByLabelText('URL do servidor'), 'https://interno.example/mcp')
    await user.click(within(screen.getByTestId('picker-mcp-auth-scheme')).getByRole('button'))
    await user.click(screen.getByRole('option', { name: 'Basic (usuário:token)' }))
    const credential = screen.getByLabelText('Credencial (usuário:token)')
    expect(credential).toBeRequired()
    await user.type(credential, 'ana@empresa.com:token')
    await user.click(screen.getByRole('button', { name: 'Salvar servidor' }))
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ name: 'Interno', transport: 'http', authScheme: 'basic', token: 'ana@empresa.com:token' }))
  })
})

describe('preset helpers', () => {
  it('recognises a preset by its address, with or without the trailing slash, and only over HTTP', () => {
    expect(presetOf({ transport: 'http', url: 'https://api.githubcopilot.com/mcp' })?.id).toBe('github')
    expect(presetOf({ transport: 'http', url: 'https://api.githubcopilot.com/mcp/' })?.id).toBe('github')
    expect(presetOf({ transport: 'http', url: 'https://mcp.atlassian.com/v2/mcp' })?.id).toBe('bitbucket')
    expect(presetOf({ transport: 'stdio', url: 'https://api.githubcopilot.com/mcp/' })).toBeUndefined()
    expect(presetOf({ transport: 'http', url: 'https://outro.example/mcp' })).toBeUndefined()
  })

  it('builds the credential each scheme expects', () => {
    const [github, bitbucket] = [mcpPresets[0], mcpPresets[1]]
    expect(credentialFor(github, 'ignorado', ' ghp_x ')).toBe('ghp_x')
    expect(credentialFor(bitbucket, ' ana@empresa.com ', ' token ')).toBe('ana@empresa.com:token')
  })
})
