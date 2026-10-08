import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import App from '../../app/App'
import { createFakeBackend } from '../../test/fakeBackend'

afterEach(() => { cleanup(); localStorage.clear() })
async function openCasual(backend: ReturnType<typeof createFakeBackend>['backend']) {
  localStorage.setItem('harflex:view-mode', 'casual'); const user = userEvent.setup()
  render(<App backend={backend} />)
  await user.type(await screen.findByLabelText('Caminho da pasta'), '/synthetic/chat')
  await user.click(screen.getByRole('button', { name: 'Abrir projeto' })); return user
}
function cliCatalog() { return { backendId: 'codex', source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'verified-revision', searchTerm: '', models: [{ id: 'configured-model', displayName: 'Modelo configurado', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete' as const, complete: true, accountFiltered: true } }
it('uses a verified configured CLI model automatically in a compact Casual start', async () => {
  const { backend } = createFakeBackend()
  backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'configured-model' })
  backend.queryCLIModelCatalog = vi.fn(async () => cliCatalog()); const create = vi.spyOn(backend, 'createDirectSession')
  const user = await openCasual(backend)
  await waitFor(() => expect(backend.queryCLIModelCatalog).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'codex' }, expect.any(AbortSignal)))
  await user.type(screen.getByLabelText('Mensagem inicial'), 'Revisar este projeto.')
  await waitFor(() => expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled())
  expect(screen.queryByRole('radio')).not.toBeInTheDocument()
  expect(screen.queryByText(/bypass|Shell e agentes CLI/)).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^Provedor, modelo e esforço: Codex CLI, Modelo configurado/ })).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Enviar' }))
  await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ backendId: 'codex', modelId: 'configured-model', catalogRevision: 'verified-revision' })))
})
it('requires a manual model choice when the configured CLI model is not listed', async () => {
  const { backend } = createFakeBackend()
  backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'missing-model' })
  backend.queryCLIModelCatalog = vi.fn(async () => cliCatalog())
  const user = await openCasual(backend)
  await user.type(await screen.findByLabelText('Mensagem inicial'), 'Meu rascunho.')
  await waitFor(() => expect(backend.queryCLIModelCatalog).toHaveBeenCalled())
  expect(screen.getByRole('button', { name: 'Enviar' })).toBeDisabled()
  await user.click(await screen.findByRole('button', { name: /^Provedor, modelo e esforço: Codex CLI, Escolha um modelo/ }))
  await user.click(within(await screen.findByRole('radiogroup', { name: 'Modelo da sessão' })).getByRole('radio', { name: 'Modelo configurado' }))
  await user.keyboard('{Escape}')
  await waitFor(() => expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled())
  expect(screen.getByLabelText('Mensagem inicial')).toHaveValue('Meu rascunho.')
})
it('keeps API model provenance tied to its provider profile without applying a Settings model override', async () => {
  const { backend } = createFakeBackend()
  backend.getSettings = async () => ({ defaultBackendId: 'local', defaultModelBackendId: 'local', defaultModelId: 'settings-model-not-applied' })
  backend.listProviderProfiles = async () => [{ id: 'local', name: 'Local', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://example.test/v1', model: 'profile-runtime-model', hasCredential: true, endpointBlocked: false, updatedAt: new Date().toISOString() }]
  const user = await openCasual(backend)
  expect(await screen.findByRole('button', { name: /^Provedor, modelo e esforço: Local, profile-runtime-model/ })).toBeInTheDocument()
  expect(screen.queryByText('settings-model-not-applied')).not.toBeInTheDocument()
  await user.type(screen.getByLabelText('Mensagem inicial'), 'Texto preservado ao abrir opções.')
  expect(screen.getByRole('button', { name: 'Configurar provedor' })).toBeInTheDocument()
  expect(screen.getByLabelText('Mensagem inicial')).toHaveValue('Texto preservado ao abrir opções.')
})

it('supports Enter send, Shift+Enter newline and IME composition without early submission', async () => {
  const { backend } = createFakeBackend(), create = vi.spyOn(backend, 'createDirectSession'), user = await openCasual(backend)
  const composer = await screen.findByLabelText('Mensagem inicial')
  await user.type(composer, 'Primeira linha.'); await user.keyboard('{Shift>}{Enter}{/Shift}'); await user.type(composer, 'Segunda linha.')
  expect(composer).toHaveValue('Primeira linha.\nSegunda linha.')
  fireEvent.keyDown(composer, { key: 'Enter', isComposing: true }); expect(create).not.toHaveBeenCalled()
  await user.keyboard('{Enter}')
  await waitFor(() => expect(create).toHaveBeenCalledOnce())
})

it('blocks new chat and mode switches during deferred session admission', async () => {
  const { backend } = createFakeBackend()
  let finish!: (value: Awaited<ReturnType<typeof backend.createDirectSession>>) => void
  backend.createDirectSession = vi.fn(() => new Promise<Awaited<ReturnType<typeof backend.createDirectSession>>>(resolve => { finish = resolve }))
  const user = await openCasual(backend)
  await user.type(await screen.findByLabelText('Mensagem inicial'), 'Intenção original da admissão.')
  await user.click(screen.getByRole('button', { name: 'Enviar' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Novo chat' })).toBeDisabled())
  expect(screen.getByRole('button', { name: 'Professional' })).toBeDisabled()
  expect(screen.getByLabelText('Mensagem inicial')).toHaveValue('Intenção original da admissão.')
  await act(async () => finish({ id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', resumable: true, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }))
})

it('carries the initial Casual draft into a Discovery preview without creating or generating', async () => {
  const { backend } = createFakeBackend(), create = vi.spyOn(backend, 'createAuthoringPipeline'), prepare = vi.spyOn(backend, 'preparePipelineDesign'), user = await openCasual(backend)
  await user.type(await screen.findByLabelText('Mensagem inicial'), 'Preparar relatórios por período com filtros.')
  await user.click(screen.getByRole('button', { name: /Iniciar trabalho SDD/ }))
  expect(await screen.findByLabelText('Discovery', { exact: true })).toHaveValue('Preparar relatórios por período com filtros.')
  expect(create).not.toHaveBeenCalled(); expect(prepare).not.toHaveBeenCalled()
})

it('keeps provider, model and effort in view during the chat and continues with a new choice in a new chat', async () => {
  const { backend } = createFakeBackend()
  backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'configured-model' })
  const catalog = cliCatalog()
  catalog.models.push({ id: 'deep-model', displayName: 'Modelo profundo', backendId: 'codex', source: 'codex_app_server', availability: 'listed', supportedReasoningEfforts: ['low', 'high'] } as never)
  backend.queryCLIModelCatalog = vi.fn(async () => catalog)
  backend.getSessionModelSelection = vi.fn(async sessionId => ({ sessionId, backendId: 'codex', modelId: 'configured-model', reasoningEffort: '', source: 'codex_app_server', destination: '', status: 'listed' as const, confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 0, contextLength: 0, workspacePath: '/synthetic/chat', checkedAt: new Date().toISOString() }) as never)
  const reopen = backend.openSession
  backend.openSession = async (id, workspaceId) => ({ ...(await reopen(id, workspaceId)), backendId: 'codex' })
  const create = vi.spyOn(backend, 'createDirectSession')
  const user = await openCasual(backend)
  await user.type(await screen.findByLabelText('Mensagem inicial'), 'Primeira pergunta.')
  await waitFor(() => expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Enviar' }))
  await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
  // The conversation shows the same bar, on what it runs.
  // The synthetic run asks for an approval; the bar unlocks once the run is over.
  await user.click(await screen.findByRole('button', { name: 'Negar' }))
  const bar = await screen.findByRole('button', { name: /^Provedor, modelo e esforço: Codex CLI, Modelo configurado/ })
  await waitFor(() => expect(bar).toBeEnabled())
  await user.click(bar)
  await user.click(within(await screen.findByRole('radiogroup', { name: 'Modelo da sessão' })).getByRole('radio', { name: 'Modelo profundo' }))
  await user.click(within(await screen.findByRole('radiogroup', { name: 'Esforço do modelo' })).getByRole('radio', { name: 'high' }))
  await user.keyboard('{Escape}')
  expect(screen.getByText(/A próxima mensagem abre um novo chat com esta escolha/)).toBeInTheDocument()
  await user.type(screen.getByLabelText('Mensagem'), 'Continue mais a fundo.')
  await user.click(screen.getByRole('button', { name: 'Enviar' }))
  await waitFor(() => expect(create).toHaveBeenCalledTimes(2))
  expect(create).toHaveBeenLastCalledWith(expect.objectContaining({ backendId: 'codex', modelId: 'deep-model', reasoningEffort: 'high', catalogRevision: 'verified-revision' }))
})

it('switches the project from the message card and keeps what was typed', async () => {
  const { backend } = createFakeBackend()
  const open = vi.spyOn(backend, 'openWorkspace')
  const user = await openCasual(backend)
  backend.listWorkspaces = async () => [
    { id: 'workspace-1', path: '/synthetic/chat', profile: 'ask', available: true },
    { id: 'workspace-2', path: '/synthetic/painel', profile: 'ask', available: true },
    { id: 'workspace-3', path: '/synthetic/antigo', profile: 'ask', available: true, archived: true },
  ]
  await user.type(await screen.findByLabelText('Mensagem inicial'), 'Revisar o tema escuro.')
  open.mockClear()
  await user.click(screen.getByRole('button', { name: 'Projeto do chat: chat' }))
  const list = await screen.findByRole('radiogroup', { name: 'Projeto do chat' })
  expect(within(list).queryByRole('radio', { name: 'antigo' })).not.toBeInTheDocument()
  await user.click(within(list).getByRole('radio', { name: 'painel' }))
  await waitFor(() => expect(open).toHaveBeenCalledWith('/synthetic/painel'))
  await waitFor(() => expect(screen.getByLabelText('Mensagem inicial')).toHaveValue('Revisar o tema escuro.'))
})
