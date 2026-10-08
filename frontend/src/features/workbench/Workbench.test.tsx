import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from '../../app/App'
import { parse, type AgentEvent, type ModelCatalogResult, type RunResult, type Session } from '../../lib/backend'
import { ConversationPane } from './ConversationPane'
import { project } from '../../state/session'
import { createFakeBackend } from '../../test/fakeBackend'
import { designPipeline } from '../../test/pipelineDesignFixture'
import { createApprovalReplayBackend } from '../../test/replayBackend'
import { sessionMetadata } from '../../test/fakeBackend'

function fakeBackend() {
  const fake = createFakeBackend()
  vi.spyOn(fake.backend, 'approve')
  return fake
}

afterEach(cleanup)

async function chooseFreeSession(user: ReturnType<typeof userEvent.setup>) {
  await user.type(await screen.findByLabelText('Motivo para pular SDD nesta sessão'), 'Teste de conversa livre')
  await user.click(screen.getByRole('button', { name: 'Iniciar sessão livre' }))
}

async function startSession(user: ReturnType<typeof userEvent.setup>) {
  await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
  await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
  expect(await screen.findByRole('heading', { level: 1, name: 'api-faturas' })).toBeInTheDocument()
  expect(screen.getByRole('radio', { name: /opencode/ })).toBeDisabled()
  await waitFor(() => expect(screen.getByRole('radio', { name: /Local/ })).toBeChecked())
  await chooseFreeSession(user)
  return screen.findByLabelText('Mensagem')
}

describe('durable agent workbench', () => {
  it('keeps recovery disabled while a read-only session history is replaying', async () => {
    const user = userEvent.setup()
    const onNewWork = vi.fn()
    render(<ConversationPane
      state={{ messages: [], pendingApprovals: [], activeRun: 'idle', calling: true, draft: '', connectionState: 'ready', readOnly: true }}
      loadingHistory
      viewMode="casual"
      onDraft={vi.fn()}
      onPrompt={vi.fn()}
      onResolve={vi.fn()}
      onCancel={vi.fn()}
      onRetry={vi.fn()}
      onNewWork={onNewWork}
    />)

    const continueButton = screen.getByRole('button', { name: 'Continuar em novo chat' })
    expect(continueButton).toBeDisabled()
    await user.click(continueButton)
    expect(onNewWork).not.toHaveBeenCalled()
  })

  it('shows the live tool action and a motion signal without exposing its command', () => {
    render(<ConversationPane
      state={{ messages: [{ kind: 'tool', id: 'tool-1', call: { toolCallId: 'call-1', name: 'write', arguments: { path: 'src/todo.js' }, status: 'running', output: '' } }], pendingApprovals: [], activeRun: 'running', calling: true, draft: '', connectionState: 'ready', readOnly: false }}
      loadingHistory={false}
      viewMode="casual"
      onDraft={vi.fn()}
      onPrompt={vi.fn()}
      onResolve={vi.fn()}
      onCancel={vi.fn()}
      onRetry={vi.fn()}
      onNewWork={vi.fn()}
    />)

    const status = screen.getByRole('status')
    expect(status).toHaveTextContent('Escrevendo arquivo')
    expect(status).toHaveTextContent('src/todo.js')
    expect(status).toHaveAttribute('aria-live', 'polite')
    expect(status).toHaveAttribute('aria-atomic', 'true')
    expect(status.querySelector('.run-status-motion')).toBeInTheDocument()
  })

  it('requires explicit CLI discovery and carries the chosen model and effort into the session', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    backend.listBackends = async () => [{ id: 'codex', name: 'Codex', kind: 'cli', available: true }]
    const query = vi.fn(async () => ({ backendId: 'codex', source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'revision', searchTerm: '',
      models: [{ id: 'runtime-model', displayName: 'Modelo dinâmico', backendId: 'codex', source: 'codex_app_server', availability: 'listed', supportedReasoningEfforts: ['low', 'high'], defaultReasoningEffort: 'high' }],
      nextCursor: '', checkedAt: sessionMetadata.createdAt, status: 'complete' as const, complete: true, accountFiltered: true }))
    backend.queryCLIModelCatalog = query
    const create = vi.spyOn(backend, 'createDirectSession')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.type(screen.getByLabelText('Motivo para pular SDD nesta sessão'), 'Pesquisa local')
    expect(query).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Iniciar sessão livre' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Consultar modelos' }))
    expect(query).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'codex' }, expect.any(AbortSignal))
    const modelPicker = await screen.findByTestId('picker-session-model')
    await user.click(within(modelPicker).getByRole('button', { name: /Abrir opções de Modelo da sessão/ }))
    await user.click(screen.getByRole('option', { name: 'Modelo dinâmico' }))
    const effortPicker = screen.getByTestId('picker-session-effort')
    await user.click(within(effortPicker).getByRole('button'))
    await user.click(screen.getByRole('option', { name: 'high' }))
    await user.click(screen.getByRole('button', { name: 'Iniciar sessão livre' }))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ modelId: 'runtime-model', reasoningEffort: 'high', catalogRevision: 'revision' })))
  })
  it('requires an explicit executor when saved Settings cannot be read', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    backend.listBackends = async () => [
      { id: 'local', name: 'Local', kind: 'api', available: true },
      { id: 'codex', name: 'Codex CLI', kind: 'cli', available: true },
    ]
    backend.getSettings = async () => { throw new Error('settings unavailable') }
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))

    expect(await screen.findByText(/Não foi possível ler o padrão salvo/i)).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /Local/ })).not.toBeChecked()
    expect(screen.getByRole('radio', { name: /Codex CLI/ })).not.toBeChecked()
    await user.click(screen.getByRole('radio', { name: /Codex CLI/ }))
    expect(screen.getByRole('radio', { name: /Codex CLI/ })).toBeChecked()
  })
  it('discards a late CLI catalog response after the backend changes', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    backend.listBackends = async () => [{ id: 'codex', name: 'Codex', kind: 'cli', available: true }, { id: 'local', name: 'Local', kind: 'api', available: true }]
    let finish!: (result: ModelCatalogResult) => void
    backend.queryCLIModelCatalog = vi.fn(() => new Promise<ModelCatalogResult>(resolve => { finish = resolve }))
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(screen.getByRole('button', { name: 'Consultar modelos' }))
    await user.click(screen.getByRole('radio', { name: /Local/ }))
    await act(async () => finish({ backendId: 'codex', source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'stale', searchTerm: '',
      models: [{ id: 'stale-model', displayName: 'Modelo antigo', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }],
      nextCursor: '', checkedAt: sessionMetadata.createdAt, status: 'complete', complete: true, accountFiltered: true }))
    expect(screen.queryByTestId('picker-session-model')).not.toBeInTheDocument()
    expect(screen.queryByText('Modelo antigo')).not.toBeInTheDocument()
  })
  it('offers a direct return to the persisted parent from a child conversation', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    backend.listEvents = vi.fn(async id => id === 'session-1' ? [{
      id: 'queued-task', streamId: id, sequence: 1, type: 'subagent.prompt.queued',
      data: { prompt: 'Tarefa recuperada depois do reinício' }, createdAt: '2026-09-25T10:00:00Z',
    }] : [])
    const prompt = vi.spyOn(backend, 'prompt')
    backend.getParentDelegation = vi.fn(async id => id === 'session-1' ? {
      id: 'delegation-1', parentSessionId: 'parent-session', childSessionId: 'session-1', agentId: 'agent-1', taskPrompt: '', promptCount: 0, promptLimit: 3, timeoutSeconds: 300, depth: 1, createdAt: '2026-09-25T10:00:00Z', status: 'ready' as const, result: '', errorCode: '',
    } : null)
    backend.openSession = vi.fn(async id => ({ id, workspaceId: 'workspace-1', backendId: 'local', status: 'completed', ...sessionMetadata }))
    render(<App backend={backend} />)
    await startSession(user)
    await user.click(await screen.findByRole('button', { name: 'Preparar tarefa delegada' }))
    expect(screen.getByLabelText('Mensagem')).toHaveValue('Tarefa recuperada depois do reinício')
    expect(prompt).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: 'Voltar à sessão pai' }))
    expect(backend.openSession).toHaveBeenCalledWith('parent-session', 'workspace-1')
  })
  it('prepares a persisted delegated task after reopening without running it', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const child = { id: 'child-session', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata }
    backend.listSessions = async () => [child]
    backend.openSession = async () => child
    backend.listEvents = async id => id === child.id ? [{
      id: 'queued-task', streamId: id, sequence: 1, type: 'subagent.prompt.queued',
      data: { prompt: 'Tarefa redigida recuperada' }, createdAt: '2026-09-25T10:00:00Z',
    }] : []
    backend.getParentDelegation = async id => id === child.id ? {
      id: 'delegation-1', parentSessionId: 'parent-session', childSessionId: id, agentId: 'agent-1', taskPrompt: 'Prévia', promptCount: 0, promptLimit: 3, timeoutSeconds: 300, depth: 1, createdAt: '2026-09-25T10:00:00Z', status: 'ready' as const, result: '', errorCode: '',
    } : null
    const prompt = vi.spyOn(backend, 'prompt')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(await screen.findByRole('button', { name: 'Abrir histórico' }))
    await user.click(await screen.findByRole('button', { name: 'Preparar tarefa delegada' }))
    expect(screen.getByLabelText('Mensagem')).toHaveValue('Tarefa redigida recuperada')
    expect(prompt).not.toHaveBeenCalled()
  })
  it('shows and refreshes the delegated call budget in the child conversation', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    let promptCount = 0
    backend.getParentDelegation = vi.fn(async id => id === 'session-1' ? {
      id: 'delegation-1', parentSessionId: 'parent-session', childSessionId: 'session-1', agentId: 'agent-1', taskPrompt: 'Revisar', promptCount, promptLimit: 3, timeoutSeconds: 300,
      depth: 1, createdAt: '2026-09-25T10:00:00Z', status: 'ready' as const, result: '', errorCode: '',
    } : null)
    const originalPrompt = backend.prompt
    backend.prompt = async (id, text) => { promptCount++; return originalPrompt(id, text) }
    render(<App backend={backend} />)
    const input = await startSession(user)
    expect(await screen.findByText(/0 de 3 chamadas · 5 min por trecho ativo/)).toBeInTheDocument()
    await user.type(input, 'Revisar')
    await user.click(screen.getByRole('button', { name: 'Enviar' }))
    expect(await screen.findByText(/1 de 3 chamadas · 5 min por trecho ativo/)).toBeInTheDocument()
  })
  it('prevents a fourth delegated prompt when the durable budget is exhausted', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    backend.getParentDelegation = async id => id === 'session-1' ? {
      id: 'delegation-1', parentSessionId: 'parent-session', childSessionId: 'session-1', agentId: 'agent-1', taskPrompt: 'Revisar', promptCount: 3, promptLimit: 3, timeoutSeconds: 300,
      depth: 1, createdAt: '2026-09-25T10:00:00Z', status: 'completed', result: '', errorCode: '',
    } : null
    const prompt = vi.spyOn(backend, 'prompt')
    render(<App backend={backend} />)
    const input = await startSession(user)
    await user.type(input, 'Outra rodada')
    expect(await screen.findByText(/3 de 3 chamadas · 5 min por trecho ativo/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Enviar' })).toBeDisabled()
    expect(prompt).not.toHaveBeenCalled()
  })
  it.each(['same', 'another', 'new'])('clears degraded connection after a successful initial replay of a %s session', async target => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const current = { id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'paused', ...sessionMetadata }
    const other = { ...current, id: 'another-session' }
    backend.listSessions = async () => [current, other]
    backend.openSession = async id => id === current.id ? current : other
    backend.listEvents = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue([])
    render(<App backend={backend} />)
    await startSession(user)
    await screen.findByRole('button', { name: 'Atualizar eventos' })
    expect(screen.getByText('Atualização dos eventos pendente')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Sessões do projeto' }))
    if (target === 'new') await chooseFreeSession(user)
    else await user.click((await screen.findAllByRole('button', { name: 'Abrir histórico' }))[target === 'same' ? 0 : 1])
    await waitFor(() => expect(screen.getByText('Backend local conectado')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Atualizar eventos' })).not.toBeInTheDocument()
    expect(screen.queryByText('Atualização dos eventos pendente')).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('Mensagem'), 'Continue')
    expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled()
  })
  it('ignores a failed older initial replay after a newer session is selected', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const newer = { id: 'newer-session', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata }
    let rejectOld: (reason: unknown) => void = () => undefined
    backend.listEvents = vi.fn().mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectOld = reject })).mockResolvedValue([])
    backend.listSessions = async () => [newer]
    backend.openSession = async () => newer
    render(<App backend={backend} />)
    await startSession(user)
    await user.click(screen.getByRole('button', { name: 'Sessões do projeto' }))
    await user.click(await screen.findByRole('button', { name: 'Abrir histórico' }))
    await screen.findByText('Sessão newer-se')
    await act(async () => { rejectOld(new Error('older replay failed')) })
    expect(screen.getByText('Backend local conectado')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Atualizar eventos' })).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('Mensagem'), 'Continue')
    expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled()
  })
  it('ignores an older OpenSession response after a newer selection from a remounted list', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const older = { id: 'older-session', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata }
    const newer = { ...older, id: 'newer-session' }
    let resolveOld: (value: typeof older) => void = () => undefined
    backend.listSessions = async () => [older, newer]
    backend.openSession = vi.fn().mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve })).mockResolvedValue(newer)
    backend.listEvents = vi.fn(async () => [])
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click((await screen.findAllByRole('button', { name: 'Abrir histórico' }))[0])
    await user.click(screen.getByRole('tab', { name: 'Artefatos' }))
    await user.click(screen.getByRole('tab', { name: 'Conversa' }))
    await user.click((await screen.findAllByRole('button', { name: 'Abrir histórico' }))[1])
    await screen.findByText('Sessão newer-se')
    await act(async () => { resolveOld(older) })
    expect(screen.getByText('Sessão newer-se')).toBeInTheDocument()
    expect(backend.listEvents).toHaveBeenCalledTimes(1)
    expect(backend.listEvents).toHaveBeenCalledWith(newer.id, 0, 1000)
  })
  it('allows the first isolated Codex prompt, then offers to continue in a new chat after metadata refresh', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const dto = { id: 'session-1', workspaceId: 'workspace-1', backendId: 'codex', status: 'ready', resumable: false, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:01:00Z' }
    backend.listBackends = async () => [{ id: 'codex', name: 'Codex', kind: 'cli', available: true }]
    backend.listSessions = async () => [dto]
    backend.openSession = vi.fn().mockResolvedValueOnce(dto).mockResolvedValue({ ...dto, status: 'completed' })
    let sent = false
    backend.prompt = vi.fn(async () => { sent = true; return { status: 'completed' as const } })
    backend.listEvents = async () => sent ? [{ id: 'e1', streamId: dto.id, sequence: 1, type: 'external.run.completed', data: { adapter: 'codex' }, createdAt: dto.createdAt }] : []
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    expect(await screen.findByText('Nova execução isolada')).toBeInTheDocument()
    expect(screen.queryByText('Retomável')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Abrir histórico' }))
    await user.type(await screen.findByLabelText('Mensagem'), 'Primeira{Enter}')
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Concluído'))
    expect(backend.prompt).toHaveBeenCalledWith(dto.id, 'Primeira')
    // The run is over and cannot be picked up again, but the chat is not locked: the next message goes to a new conversation.
    expect(screen.getByLabelText('Mensagem')).toBeEnabled()
    expect(screen.getByText(/Somente leitura: esta execução foi encerrada/)).toBeInTheDocument()
    expect(backend.prompt).toHaveBeenCalledTimes(1)
  })
  it('clears the composer when the sent message is journaled while the run is still active', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    let publish!: (event: AgentEvent) => void
    let resolvePrompt!: (result: RunResult) => void
    let journal: AgentEvent[] = []
    backend.onEvent = listener => { publish = listener; return () => undefined }
    backend.listEvents = async () => journal
    backend.prompt = vi.fn(() => new Promise<RunResult>(resolve => { resolvePrompt = resolve }))
    const view = render(<App backend={backend} />)
    const input = await startSession(user)
    const text = 'Crie um arquivo de exemplo'
    await user.type(input, text)
    await user.click(screen.getByRole('button', { name: 'Enviar' }))
    await waitFor(() => expect(backend.prompt).toHaveBeenCalledWith('session-1', text))
    expect(input).toHaveValue(text)

    const base = { streamId: 'session-1', createdAt: sessionMetadata.createdAt }
    const events = parse.events([
      { ...base, id: 'sent-started', sequence: 1, type: 'run.started', data: {} },
      { ...base, id: 'sent-user', sequence: 2, type: 'message.user', data: { role: 'user', content: text } },
    ])
    journal = events.slice(0, 2)
    await act(async () => { journal.forEach(publish) })
    await waitFor(() => expect(input).toHaveValue(''))
    expect(screen.getByText('Crie um arquivo de exemplo', { selector: '.turn-user p' })).toBeInTheDocument()
    view.unmount()
    await act(async () => { resolvePrompt({ status: 'completed' }) })
  })
  it('preserves a first Casual message in the conversation composer when the journal rejects it', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const previousMode = window.localStorage.getItem('harflex:view-mode')
    window.localStorage.setItem('harflex:view-mode', 'casual')
    try {
      const text = 'Comece o primeiro chat com esta tarefa'
      backend.listBackends = async () => [{ id: 'local', name: 'Local', kind: 'api', available: true }]
      backend.getSettings = async () => ({ defaultBackendId: 'local', defaultModelBackendId: '', defaultModelId: '' })
      backend.createDirectSession = vi.fn(async () => ({ id: 'casual-session', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata }))
      backend.prompt = vi.fn(async () => { throw new Error('provider temporarily unavailable') })
      render(<App backend={backend} />)
      await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
      await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
      await user.type(await screen.findByLabelText('Mensagem inicial'), text)
      await user.click(screen.getByRole('button', { name: 'Enviar' }))

      expect(await screen.findByLabelText('Mensagem')).toHaveValue(text)
      expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled()
      expect(backend.prompt).toHaveBeenCalledWith('casual-session', text)
    } finally {
      if (previousMode === null) window.localStorage.removeItem('harflex:view-mode')
      else window.localStorage.setItem('harflex:view-mode', previousMode)
    }
  })
  it('turns a session_not_resumable refusal into read-only history', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    backend.prompt = vi.fn(async () => { throw { cause: { code: 'session_not_resumable' } } })
    render(<App backend={backend} />)
    await user.type(await startSession(user), 'Continue{Enter}')
    expect(screen.getByLabelText('Mensagem')).toBeEnabled()
    expect(screen.getByRole('status')).toHaveTextContent('não pode ser retomada')
    expect(screen.getByText(/Somente leitura: esta execução foi encerrada/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Continuar em novo chat' }))
    expect(await screen.findByRole('heading', { name: 'Começar pelo SDD' })).toBeInTheDocument()
  })
  it('refreshes continuity after retrying a failed terminal replay', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const dto = { id: 'session-1', workspaceId: 'workspace-1', backendId: 'codex', status: 'ready', resumable: false, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:01:00Z' }
    backend.listBackends = async () => [{ id: 'codex', name: 'Codex', kind: 'cli', available: true }]
    backend.createSession = async () => dto
    backend.queryCLIModelCatalog = async () => ({ backendId: 'codex', source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'revision', searchTerm: '',
      models: [{ id: 'runtime-model', displayName: 'Modelo dinâmico', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }],
      nextCursor: '', checkedAt: dto.createdAt, status: 'complete', complete: true, accountFiltered: true })
    backend.openSession = vi.fn(async () => ({ ...dto, status: 'completed' }))
    backend.prompt = async () => ({ status: 'completed' })
    backend.listEvents = vi.fn().mockResolvedValueOnce([]).mockRejectedValueOnce(new Error('offline')).mockResolvedValue([{ id: 'e1', streamId: dto.id, sequence: 1, type: 'external.run.completed', data: { adapter: 'codex' }, createdAt: dto.createdAt }])
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(screen.getByRole('button', { name: 'Consultar modelos' }))
    const modelPicker = await screen.findByTestId('picker-session-model')
    await user.click(within(modelPicker).getByRole('button', { name: /Abrir opções de Modelo da sessão/ }))
    await user.click(screen.getByRole('option', { name: 'Modelo dinâmico' }))
    await chooseFreeSession(user)
    await user.type(await screen.findByLabelText('Mensagem'), 'Primeira{Enter}')
    await user.click(await screen.findByRole('button', { name: 'Atualizar eventos' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Concluído'))
    expect(screen.getByLabelText('Mensagem')).toBeEnabled()
    expect(screen.getByText(/Somente leitura: esta execução foi encerrada/)).toBeInTheDocument()
    expect(backend.openSession).toHaveBeenCalledWith(dto.id, dto.workspaceId)
  })
  it('opens completed Codex evaluation history without sending anything to it', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const dto = { id: 'evaluation-session', workspaceId: 'workspace-1', backendId: 'codex', status: 'completed', resumable: false, createdAt: sessionMetadata.createdAt, updatedAt: sessionMetadata.createdAt }
    backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
    backend.listSessions = vi.fn(async () => [dto])
    backend.openSession = vi.fn(async () => dto)
    backend.listEvents = vi.fn(async () => [
      { id: 'e1', streamId: dto.id, sequence: 1, type: 'message.user', data: { role: 'user', content: 'Avalie o resultado' }, createdAt: dto.createdAt },
      { id: 'e2', streamId: dto.id, sequence: 2, type: 'external.event', data: { type: 'text', text: 'Avaliação concluída' }, createdAt: dto.createdAt },
      { id: 'e3', streamId: dto.id, sequence: 3, type: 'external.run.completed', data: { adapter: 'codex' }, createdAt: dto.createdAt },
    ])
    backend.prompt = vi.fn()
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    expect(await screen.findByRole('heading', { name: 'Sessões recentes' })).toBeInTheDocument()
    expect(await screen.findByText('Somente leitura')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Abrir histórico' }))
    expect(await screen.findByText('Avaliação concluída')).toBeInTheDocument()
    // Nothing is sent to the closed conversation; the box is there to continue in a new one.
    expect(screen.getByLabelText('Mensagem')).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Enviar' })).toBeDisabled()
    expect(backend.prompt).not.toHaveBeenCalled()
    expect(backend.openSession).toHaveBeenCalledWith(dto.id, dto.workspaceId)
  })
  it('lists the project work under recent sessions and opens it on Pipelines', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const work = { ...designPipeline('work-1'), title: 'Crie um TODO com html', currentStage: 'eval' as const, stageStatus: { ...designPipeline().stageStatus, eval: 'waiting_user' as const } }
    backend.listPipelines = vi.fn(async () => [work])
    backend.getPipeline = vi.fn(async () => work)
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    const list = await screen.findByRole('list', { name: 'Trabalhos' })
    expect(within(list).getByText('Crie um TODO com html')).toBeInTheDocument()
    expect(within(list).getByText('QA · aguardando você')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Pipelines SDD' })).not.toBeInTheDocument()
    await user.click(within(list).getByRole('button', { name: 'Abrir trabalho' }))
    expect(await screen.findByRole('heading', { name: 'Pipelines SDD' })).toBeInTheDocument()
    expect(screen.queryByRole('list', { name: 'Trabalhos' })).not.toBeInTheDocument()
  })
  it('offers the pipeline a chat agent created and opens it on Pipelines', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const listeners = new Set<(change: { workspaceId: string; pipelineId: string }) => void>()
    const announce = (change: { workspaceId: string; pipelineId: string }) => listeners.forEach(listener => listener(change))
    backend.onPipelineCreated = listener => { listeners.add(listener); return () => { listeners.delete(listener) } }
    const created = { ...designPipeline(), id: 'agent-pipeline', workspaceId: 'workspace-1', title: 'Baixa parcial' }
    backend.getPipeline = vi.fn(async () => created)
    render(<App backend={backend} />)
    await startSession(user)
    act(() => announce({ workspaceId: 'other-project', pipelineId: 'agent-pipeline' }))
    expect(screen.queryByText('Pipeline criado pelo agente')).not.toBeInTheDocument()
    act(() => announce({ workspaceId: 'workspace-1', pipelineId: 'agent-pipeline' }))
    expect(await screen.findByText('Pipeline criado pelo agente')).toBeInTheDocument()
    expect(screen.getByText('Baixa parcial')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Dispensar aviso do pipeline' }))
    expect(screen.queryByText('Pipeline criado pelo agente')).not.toBeInTheDocument()
  })
  it('presents interrupted tool outcomes without suggesting execution succeeded', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const source = [
      ['message.assistant', { role: 'assistant', content: '', toolCalls: [{ id: 't1', name: 'bash', arguments: {} }, { id: 't2', name: 'write', arguments: {} }] }],
      ['tool.failed', { toolCallId: 't1', name: 'bash', errorCode: 'outcome_unknown', error: 'RAW_ERROR' }],
      ['tool.skipped', { toolCallId: 't2', name: 'write', errorCode: 'not_executed', error: 'RAW_ERROR' }],
      ['run.interrupted', { reason: 'app_restart' }],
    ]
    backend.listEvents = async () => parse.events(source.map(([type, data], index) => ({ id: `e${index + 1}`, streamId: 'session-1', sequence: index + 1, type, data, createdAt: '2026-09-25T10:00:00Z' })))
    render(<App backend={backend} />)
    await startSession(user)
    expect(await screen.findByText('Resultado desconhecido após a interrupção. Verifique os efeitos antes de executar novamente.')).toBeInTheDocument()
    expect(screen.getByText('Não executada antes da interrupção.')).toBeInTheDocument()
    expect(screen.queryByText('RAW_ERROR')).not.toBeInTheDocument()
  })
  it.each([true, false])('opens interrupted history without execution, resumable=%s', async resumable => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const backendId = resumable ? 'local' : 'codex'
    if (!resumable) backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
    const dto = { id: 'session-1', workspaceId: 'workspace-1', backendId, status: 'paused', resumable, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:01:00Z' }
    backend.listSessions = vi.fn(async () => [dto])
    backend.openSession = vi.fn(async () => dto)
    backend.prompt = vi.fn()
    backend.listEvents = vi.fn(async () => [
      { id: 'e1', streamId: dto.id, sequence: 1, type: 'message.user', data: { role: 'user', content: 'Pergunta anterior' }, createdAt: dto.createdAt },
      { id: 'e2', streamId: dto.id, sequence: 2, type: 'external.event', data: { type: 'text', text: 'Resposta anterior', raw: { text: 'RAW_ONLY' } }, createdAt: dto.createdAt },
      { id: 'e3', streamId: dto.id, sequence: 3, type: 'approval.requested', data: { approvalId: 'a', toolCallId: 't', name: 'write', risk: 'write', arguments: {} }, createdAt: dto.createdAt },
      { id: 'e4', streamId: dto.id, sequence: 4, type: 'run.interrupted', data: { reason: 'app_restart' }, createdAt: dto.createdAt },
    ])
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    expect(screen.getByText('Ferramentas de arquivo ficam nesta pasta. Shell e agentes CLI podem acessar recursos permitidos pela sua conta; revise cada aprovação.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    expect(await screen.findByRole('heading', { name: 'Sessões recentes' })).toBeInTheDocument()
    expect(await screen.findByText(resumable ? 'Retomável' : 'Somente leitura')).toBeInTheDocument()
    expect(backend.openSession).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Abrir histórico' }))
    expect(await screen.findByText('Resposta anterior')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Interrompido')
    expect(screen.queryByRole('group', { name: 'Aprovação necessária' })).not.toBeInTheDocument()
    expect(screen.queryByText('RAW_ONLY')).not.toBeInTheDocument()
    expect(backend.prompt).not.toHaveBeenCalled()
    expect(backend.openSession).toHaveBeenCalledWith(dto.id, dto.workspaceId)
    expect(backend.listEvents).toHaveBeenCalledWith(dto.id, 0, 1000)
    if (resumable) {
      await user.type(screen.getByLabelText('Mensagem'), 'Continue')
      expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled()
    } else {
      expect(screen.getByLabelText('Mensagem')).toBeEnabled()
      expect(screen.getByText(/Escreva abaixo e o Harflex continua em um novo chat/)).toBeInTheDocument()
    }
  })
  it('retries a failed session list without leaking the error', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    backend.listSessions = vi.fn().mockRejectedValueOnce(new Error('PRIVATE')).mockResolvedValue([])
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(await screen.findByRole('button', { name: 'Atualizar sessões' }))
    expect(await screen.findByText('Nenhuma sessão salva neste projeto.')).toBeInTheDocument()
    expect(screen.queryByText('PRIVATE')).not.toBeInTheDocument()
  })
  it('exports audit to an explicit destination with safe failure and focus restoration', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    backend.exportAudit = vi.fn().mockRejectedValueOnce(new Error('PRIVATE')).mockResolvedValue('/tmp/audit.jsonl')
    render(<App backend={backend} />)
    await startSession(user)
    await user.click(screen.getByRole('tab', { name: 'Artefatos' }))
    const trigger = screen.getByRole('button', { name: 'Exportar auditoria' })
    await user.click(trigger)
    const dialog = screen.getByRole('dialog', { name: 'Exportar auditoria' })
    const input = within(dialog).getByLabelText('Caminho de destino')
    expect(dialog).toHaveTextContent('arquivo JSONL')
    expect(input).toHaveAttribute('placeholder', expect.stringContaining('.jsonl'))
    expect(input).toHaveFocus()
    expect(input).toHaveValue('')
    expect(within(dialog).getByRole('button', { name: 'Exportar' })).toBeDisabled()
    await user.type(input, '/tmp/audit.jsonl')
    await user.click(within(dialog).getByRole('button', { name: 'Exportar' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Não foi possível exportar')
    expect(dialog).not.toHaveTextContent('PRIVATE')
    await user.click(within(dialog).getByRole('button', { name: 'Exportar' }))
    expect(await within(dialog).findByRole('status')).toHaveTextContent('Auditoria JSONL exportada: /tmp/audit.jsonl')
    expect(backend.exportAudit).toHaveBeenLastCalledWith('session-1', '/tmp/audit.jsonl')
    await user.keyboard('{Escape}')
    expect(trigger).toHaveFocus()
  })
  it('recovers a lost approval push and failed replay after successful cancellation', async () => {
    const user = userEvent.setup()
    const backend = createApprovalReplayBackend()
    render(<App backend={backend} />)
    await user.type(await startSession(user), 'Execute{Enter}')
    await screen.findByRole('button', { name: 'Atualizar eventos' })
    await user.click(screen.getByRole('button', { name: 'Cancelar execução' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Cancelado'))
    expect(screen.getByText('Backend local conectado')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Atualizar eventos' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Cancelar execução' })).not.toBeInTheDocument()
    expect(screen.queryByRole('group', { name: 'Aprovação necessária' })).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('Mensagem'), 'Próximo')
    expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled()
  })

  it('retains the approval expectation and cancellation control when the backend rejects cancellation', async () => {
    const user = userEvent.setup()
    const backend = createApprovalReplayBackend(true)
    vi.spyOn(backend, 'listEvents')
    render(<App backend={backend} />)
    await user.type(await startSession(user), 'Execute{Enter}')
    await screen.findByRole('button', { name: 'Atualizar eventos' })
    const callsBeforeCancel = vi.mocked(backend.listEvents).mock.calls.length
    await user.click(screen.getByRole('button', { name: 'Cancelar execução' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('A operação falhou'))
    expect(screen.getByRole('button', { name: 'Cancelar execução' })).toBeEnabled()
    expect(backend.listEvents).toHaveBeenCalledTimes(callsBeforeCancel)
    await user.click(screen.getByRole('button', { name: 'Atualizar eventos' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Aguardando aprovação'))
    expect(screen.getByRole('group', { name: 'Aprovação necessária' })).toBeInTheDocument()
    expect(screen.getByText('Backend local conectado')).toBeInTheDocument()
  })

  it.each([
    ['completed', 'Concluído'], ['failed', 'Falhou'], ['cancelled', 'Cancelado'],
  ] as const)('replays canonical external %s events without degrading the session', async (status, label) => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    let started = false
    let listener: (event: AgentEvent) => void = () => undefined
    const envelope = { streamId: 'session-1', data: { adapter: 'codex' }, createdAt: '2026-09-25T10:00:00Z' }
    backend.onEvent = receive => { listener = receive; return () => undefined }
    backend.prompt = async () => {
      started = true
      listener(parse.events([{ ...envelope, id: 'e1', sequence: 1, type: 'external.run.started' }])[0])
      return { status }
    }
    backend.listEvents = vi.fn(async () => started ? parse.events([{ ...envelope, id: 'e2', sequence: 2, type: `external.run.${status}` }]) : [])
    render(<App backend={backend} />)
    await user.type(await startSession(user), 'Execute{Enter}')
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(label))
    expect(screen.getByText('Backend local conectado')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Atualizar eventos' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Cancelar execução' })).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('Mensagem'), 'Próximo')
    expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled()
  })

  it('preserves a draft across panels and a failed send, then clears it on success', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const prompt = backend.prompt
    backend.prompt = vi.fn(async () => { throw new Error('offline') })
    render(<App backend={backend} />)
    await user.type(await startSession(user), 'Rascunho')
    await user.click(screen.getByRole('tab', { name: 'Artefatos' }))
    await user.click(screen.getByRole('tab', { name: 'Conversa' }))
    expect(screen.getByLabelText('Mensagem')).toHaveValue('Rascunho')
    await user.click(screen.getByRole('button', { name: 'Enviar' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('A operação falhou'))
    expect(screen.getByLabelText('Mensagem')).toHaveValue('Rascunho')
    backend.prompt = prompt
    await user.click(screen.getByRole('button', { name: 'Enviar' }))
    await screen.findByRole('group', { name: 'Aprovação necessária' })
    expect(screen.getByLabelText('Mensagem')).toHaveValue('')
  })

  it('keeps the draft when a prompt reports completion without a journaled user message', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const text = 'Mensagem sem confirmação do journal'
    const journal: AgentEvent[] = []
    backend.prompt = vi.fn(async () => {
      journal.push(
        { id: 'e1', streamId: 'session-1', sequence: 1, type: 'run.started', data: { reason: '' }, createdAt: sessionMetadata.createdAt },
        { id: 'e2', streamId: 'session-1', sequence: 2, type: 'run.completed', data: { reason: '' }, createdAt: sessionMetadata.createdAt },
      )
      return { status: 'completed' as const }
    })
    backend.openSession = vi.fn(async (id, workspaceId) => ({ id, workspaceId, backendId: 'local', status: 'completed', ...sessionMetadata }))
    backend.listEvents = vi.fn(async () => journal)
    render(<App backend={backend} />)
    const input = await startSession(user)
    await user.type(input, text)
    await user.click(screen.getByRole('button', { name: 'Enviar' }))
    await waitFor(() => expect(backend.prompt).toHaveBeenCalledWith('session-1', text))
    await waitFor(() => expect(backend.openSession).toHaveBeenCalledWith('session-1', 'workspace-1'))
    expect(input).toHaveValue(text)
  })

  it('clears a sent message when it is journaled while the agent run is still active', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const journal: AgentEvent[] = []
    let receive: (event: AgentEvent) => void = () => undefined
    let finishPrompt!: (result: RunResult) => void
    const promptResult = new Promise<RunResult>(resolve => { finishPrompt = resolve })
    backend.onEvent = listener => { receive = listener; return () => undefined }
    backend.listEvents = async (_sessionId, after) => journal.filter(event => event.sequence > after)
    backend.prompt = async (_sessionId, text) => {
      for (const [sequence, type, data] of [
        [1, 'run.started', { reason: '' }],
        [2, 'message.user', { role: 'user', content: text }],
      ] as const) {
        const event: AgentEvent = { id: `e${sequence}`, streamId: 'session-1', sequence, type, data, createdAt: sessionMetadata.createdAt }
        journal.push(event)
        receive(event)
      }
      return promptResult
    }
    render(<App backend={backend} />)
    const input = await startSession(user)
    await user.type(input, 'conseguiu criar?')
    await user.click(screen.getByRole('button', { name: 'Enviar' }))

    expect(within(screen.getByRole('list', { name: 'Conversa' })).getByText('conseguiu criar?', { exact: true })).toBeInTheDocument()
    await waitFor(() => expect(input).toHaveValue(''))
    expect(screen.getByRole('status')).toHaveTextContent('Agente trabalhando')
    await user.type(input, 'Próxima mensagem')

    await act(async () => {
      const terminal: AgentEvent = { id: 'e3', streamId: 'session-1', sequence: 3, type: 'run.completed', data: { reason: '' }, createdAt: sessionMetadata.createdAt }
      journal.push(terminal)
      receive(terminal)
      finishPrompt({ status: 'completed' })
    })
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Concluído'))
    expect(input).toHaveValue('Próxima mensagem')
  })

  it('clears a message accepted by the journal when the prompt call fails before its reply', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const journal: AgentEvent[] = []
    let accepted = false
    const createdAt = sessionMetadata.createdAt
    backend.onEvent = () => () => undefined
    backend.listEvents = async (_sessionId, after) => accepted ? journal.filter(event => event.sequence > after) : []
    backend.prompt = async (_sessionId, text) => {
      accepted = true
      for (const [sequence, type, data] of [
        [1, 'run.started', { reason: '' }],
        [2, 'message.user', { role: 'user', content: text }],
      ] as const) journal.push({ id: `e${sequence}`, streamId: 'session-1', sequence, type, data, createdAt })
      throw new Error('connection lost after journal commit')
    }
    render(<App backend={backend} />)
    const input = await startSession(user)
    await user.type(input, 'Mensagem já aceita')
    await user.click(screen.getByRole('button', { name: 'Enviar' }))

    expect(within(await screen.findByRole('list', { name: 'Conversa' })).getByText('Mensagem já aceita', { exact: true })).toBeInTheDocument()
    await waitFor(() => expect(input).toHaveValue(''))
  })

  it('preserves a retyped draft when it happens to match the accepted message', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    const journal: AgentEvent[] = []
    let receive: (event: AgentEvent) => void = () => undefined
    let finishPrompt!: (result: RunResult) => void
    let acceptMessage!: () => void
    const promptResult = new Promise<RunResult>(resolve => { finishPrompt = resolve })
    const messageAccepted = new Promise<void>(resolve => { acceptMessage = resolve })
    const createdAt = sessionMetadata.createdAt
    backend.onEvent = listener => { receive = listener; return () => undefined }
    backend.listEvents = async (_sessionId, after) => journal.filter(event => event.sequence > after)
    backend.prompt = async (_sessionId, text) => {
      const started: AgentEvent = { id: 'e1', streamId: 'session-1', sequence: 1, type: 'run.started', data: { reason: '' }, createdAt }
      journal.push(started)
      receive(started)
      await messageAccepted
      const userMessage: AgentEvent = { id: 'e2', streamId: 'session-1', sequence: 2, type: 'message.user', data: { role: 'user', content: text }, createdAt }
      journal.push(userMessage)
      receive(userMessage)
      return promptResult
    }
    render(<App backend={backend} />)
    const input = await startSession(user)
    await user.type(input, 'Mensagem idêntica')
    await user.click(screen.getByRole('button', { name: 'Enviar' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Agente trabalhando'))

    await user.clear(input)
    await user.type(input, 'Mensagem idêntica')
    await act(async () => { acceptMessage() })
    await waitFor(() => expect(within(screen.getByRole('list', { name: 'Conversa' })).getByText('Mensagem idêntica', { exact: true })).toBeInTheDocument())
    expect(input).toHaveValue('Mensagem idêntica')

    await act(async () => {
      const terminal: AgentEvent = { id: 'e3', streamId: 'session-1', sequence: 3, type: 'run.completed', data: { reason: '' }, createdAt }
      journal.push(terminal)
      receive(terminal)
      finishPrompt({ status: 'completed' })
    })
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Concluído'))
    expect(input).toHaveValue('Mensagem idêntica')
  })

  it('keeps an unaccepted draft after an empty journal retry', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    let failNextRead = false
    backend.listEvents = async () => {
      if (failNextRead) {
        failNextRead = false
        throw new Error('offline')
      }
      return []
    }
    backend.prompt = async () => {
      failNextRead = true
      throw new Error('offline before acceptance')
    }
    render(<App backend={backend} />)
    const input = await startSession(user)
    await user.type(input, 'Rascunho para retry')
    await user.click(screen.getByRole('button', { name: 'Enviar' }))
    await screen.findByRole('button', { name: 'Atualizar eventos' })
    expect(input).toHaveValue('Rascunho para retry')

    await user.click(screen.getByRole('button', { name: 'Atualizar eventos' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Atualizar eventos' })).not.toBeInTheDocument())
    expect(input).toHaveValue('Rascunho para retry')
  })

  it('recovers pushed gaps and pages the journal through the terminal event', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    let listener: (event: AgentEvent) => void = () => undefined
    let started = false
    const journal: AgentEvent[] = Array.from({ length: 1002 }, (_, index) => ({
      id: `e${index + 1}`, streamId: 'session-1', sequence: index + 1,
      type: index === 0 ? 'run.started' : index === 1001 ? 'run.completed' : 'usage.recorded',
      data: index === 1001 ? { reason: '' } : { inputTokens: 1, outputTokens: 1 }, createdAt: '2026-09-25T10:00:00Z',
    }))
    backend.onEvent = receive => { listener = receive; return () => undefined }
    backend.prompt = async () => { started = true; listener(journal[1000]); return { status: 'completed' } }
    backend.listEvents = vi.fn(async (_session, after) => started ? journal.filter(event => event.sequence > after).slice(0, 1000) : [])
    render(<App backend={backend} />)
    await user.type(await startSession(user), 'Execute{Enter}')
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Concluído'))
    expect(vi.mocked(backend.listEvents).mock.calls.map(call => call[1])).toEqual([0, 0, 1000])
    await user.type(screen.getByLabelText('Mensagem'), 'Próximo')
    expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled()
  })

  it('offers replay retry and cancellation after a journal failure without claiming completion', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    let listener: (event: AgentEvent) => void = () => undefined
    let started = false
    let unavailable = true
    let promptText = ''
    const startedEvent: AgentEvent = { id: 'e1', streamId: 'session-1', sequence: 1, type: 'run.started', data: {}, createdAt: '2026-09-25T10:00:00Z' }
    backend.onEvent = receive => { listener = receive; return () => undefined }
    backend.prompt = async (_sessionId, text) => { started = true; promptText = text; listener(startedEvent); return { status: 'completed' } }
    backend.listEvents = vi.fn(async () => {
      if (!started) return []
      if (unavailable) throw new Error('offline')
      return [
        { ...startedEvent, id: 'e2', sequence: 2, type: 'message.user', data: { role: 'user', content: promptText } },
        { ...startedEvent, id: 'e3', sequence: 3, type: 'run.completed', data: { reason: '' } },
      ]
    })
    render(<App backend={backend} />)
    await user.type(await startSession(user), 'Execute{Enter}')
    const retry = await screen.findByRole('button', { name: 'Atualizar eventos' })
    expect(screen.getByRole('status')).not.toHaveTextContent('Concluído')
    expect(screen.getByRole('button', { name: 'Cancelar execução' })).toBeEnabled()
    unavailable = false
    await user.click(retry)
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Concluído'))
    expect(screen.queryByRole('button', { name: 'Atualizar eventos' })).not.toBeInTheDocument()
    expect(screen.getByLabelText('Mensagem')).toHaveValue('')
  })

  it('does not reuse an older terminal event when the latest call has no journal outcome', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    backend.listEvents = async (_session, after) => after === 0 ? [{ id: 'old', streamId: 'session-1', sequence: 1, type: 'run.completed', data: { reason: '' }, createdAt: '2026-09-25T10:00:00Z' }] : []
    backend.prompt = async () => ({ status: 'completed' })
    render(<App backend={backend} />)
    await user.type(await startSession(user), 'Execute{Enter}')
    await screen.findByRole('button', { name: 'Atualizar eventos' })
    expect(screen.getByRole('status')).not.toHaveTextContent('Concluído')
  })

  it('does not start replay when a prompt settles after the workbench is unmounted', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    let complete: (result: { status: 'completed' }) => void = () => undefined
    backend.prompt = () => new Promise(resolve => { complete = resolve })
    backend.listEvents = vi.fn(async () => [])
    const { unmount } = render(<App backend={backend} />)
    await user.type(await startSession(user), 'Execute{Enter}')
    unmount()
    complete({ status: 'completed' })
    await Promise.resolve()
    expect(backend.listEvents).toHaveBeenCalledTimes(1)
  })

  it('shows the unavailable state honestly when the desktop backend cannot be reached', async () => {
    const { backend } = fakeBackend()
    backend.listBackends = vi.fn(async () => { throw new Error('offline') })
    render(<App backend={backend} />)
    expect(await screen.findByText('Backend local indisponível', { selector: 'p' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: 'Nenhum projeto aberto' })).toBeInTheDocument()
    expect(screen.queryByText('api-faturas')).not.toBeInTheDocument()
  })

  it('runs a prompt, requires approval for writes, denies, then approves and shows the diff', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    render(<App backend={backend} />)
    await user.type(await startSession(user), 'Crie notas{Enter}')

    const log = await screen.findByRole('list', { name: 'Conversa' })
    expect(within(log).getByText('Crie notas')).toBeInTheDocument()
    expect(within(log).getByText('Vou escrever as notas.')).toBeInTheDocument()
    const tool = within(log).getByRole('article', { name: 'Ferramenta write' })
    expect(tool).toHaveTextContent('notes.md')
    expect(tool).toHaveTextContent('Aguardando aprovação')

    const approval = screen.getByRole('group', { name: 'Aprovação necessária' })
    expect(approval).toHaveTextContent('Cria ou altera arquivos no projeto.')
    expect(screen.getByRole('status')).toHaveTextContent('Aguardando aprovação')
    expect(screen.getByRole('button', { name: 'Enviar' })).toBeDisabled()
    within(approval).getByRole('button', { name: 'Aprovar' }).focus()
    await user.keyboard('{Enter}')
    expect(backend.approve).not.toHaveBeenCalled()

    await user.click(within(approval).getByRole('button', { name: 'Negar' }))
    expect(backend.approve).toHaveBeenLastCalledWith('session-1', expect.any(String), false)
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Falhou: aprovação negada'))
    expect(tool).toHaveTextContent('Negada')
    expect(screen.queryByRole('group', { name: 'Aprovação necessária' })).not.toBeInTheDocument()

    await user.type(screen.getByLabelText('Mensagem'), 'Tente de novo{Enter}')
    await user.click(await screen.findByRole('button', { name: 'Aprovar' }))
    expect(backend.approve).toHaveBeenLastCalledWith('session-1', expect.any(String), true)
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Concluído'))
    expect(within(log).getByText('Pronto.')).toBeInTheDocument()
    expect(within(log).getAllByRole('article', { name: 'Ferramenta write' })[1]).toHaveTextContent('Concluída')

    await user.click(screen.getByRole('tab', { name: 'Artefatos' }))
    const diffBlock = screen.getByRole('region', { name: 'Diff de notes.md' })
    expect(within(diffBlock).getByText('+olá')).toHaveClass('diff-add')
    await user.click(screen.getByRole('button', { name: 'Eventos' }))
    expect(screen.getByRole('table')).toHaveTextContent('run.completed')
  })

  it('reports refused calls with the stable error message and keeps the session usable', async () => {
    const user = userEvent.setup()
    const { backend } = fakeBackend()
    backend.prompt = vi.fn(async () => { throw Object.assign(new Error('prompt: operation failed'), { cause: { code: 'session_busy' } }) })
    render(<App backend={backend} />)
    await user.type(await startSession(user), 'Olá{Enter}')
    expect(await screen.findByRole('status')).toHaveTextContent('A sessão já está executando.')
    expect(screen.getByLabelText('Mensagem')).toBeEnabled()
  })
})

// A conversation that cannot be resumed does not end the chat: the next message opens a new one that carries it.
describe('continuing a closed conversation', () => {
  const stamp = '2026-09-25T10:00:00Z'
  const closed = { id: 'old-session', workspaceId: 'workspace-1', backendId: 'codex', status: 'completed', resumable: false, createdAt: stamp, updatedAt: stamp }
  const codex = { id: 'codex', name: 'Codex CLI', kind: 'cli' as const, available: true }
  const local = { id: 'local', name: 'Local', kind: 'api' as const, available: true }
  const oldJournal = (): AgentEvent[] => [
    { id: 'o1', streamId: closed.id, sequence: 1, type: 'message.user', data: { role: 'user', content: 'Crie o TODO' }, createdAt: stamp },
    { id: 'o2', streamId: closed.id, sequence: 2, type: 'message.assistant', data: { role: 'assistant', content: '', toolCalls: [{ id: 't1', name: 'write', arguments: { path: 'index.html' } }] }, createdAt: stamp },
    { id: 'o3', streamId: closed.id, sequence: 3, type: 'tool.called', data: { toolCallId: 't1', name: 'write' }, createdAt: stamp },
    { id: 'o4', streamId: closed.id, sequence: 4, type: 'tool.completed', data: { toolCallId: 't1', name: 'write', content: { text: 'Wrote 14 bytes to index.html' }, details: { path: 'index.html' } }, createdAt: stamp },
    { id: 'o5', streamId: closed.id, sequence: 5, type: 'message.assistant', data: { role: 'assistant', content: 'Pronto, criei o TODO.' }, createdAt: stamp },
    { id: 'o6', streamId: closed.id, sequence: 6, type: 'run.completed', data: { reason: '' }, createdAt: stamp },
  ]
  /** A project with one closed conversation of a CLI agent, and the journals of every conversation the test opens. */
  function closedProject(backends = [codex, local]) {
    const { backend } = fakeBackend()
    const journals: Record<string, AgentEvent[]> = { [closed.id]: oldJournal() }
    backend.listBackends = async () => backends
    backend.listSessions = async () => [closed]
    backend.openSession = vi.fn(async (id: string) => id === closed.id ? closed : { id, workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata })
    backend.listEvents = async (id: string, after: number) => (journals[id] ?? []).filter(event => event.sequence > after)
    backend.createDirectSession = vi.fn(async input => ({ id: 'new-session', workspaceId: input.workspaceId, backendId: input.backendId, status: 'ready', ...sessionMetadata }))
    backend.prompt = vi.fn(async (id: string, text: string) => {
      const base = { streamId: id, createdAt: stamp }
      journals[id] = [
        { ...base, id: 'n1', sequence: 1, type: 'run.started', data: {} },
        { ...base, id: 'n2', sequence: 2, type: 'message.user', data: { role: 'user', content: text } },
        { ...base, id: 'n3', sequence: 3, type: 'message.assistant', data: { role: 'assistant', content: 'Título ajustado.' } },
        { ...base, id: 'n4', sequence: 4, type: 'run.completed', data: { reason: '' } },
      ]
      return { status: 'completed' as const }
    })
    return { backend, journals }
  }
  async function openClosed(user: ReturnType<typeof userEvent.setup>, backend: ReturnType<typeof fakeBackend>['backend']) {
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(await screen.findByRole('button', { name: 'Abrir histórico' }))
    expect(await screen.findByText('Pronto, criei o TODO.')).toBeInTheDocument()
    return screen.getByLabelText('Mensagem')
  }

  it('opens a new chat on a usable backend, carries the old conversation and sends the message there', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject()
    const box = await openClosed(user, backend)
    await user.type(box, 'Agora ajuste o título{Enter}')

    await waitFor(() => expect(backend.prompt).toHaveBeenCalledTimes(1))
    // The CLI that ran it has no saved default model, so the person's API profile takes over.
    expect(backend.createDirectSession).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'local', reason: `Continuação da conversa ${closed.id}, encerrada` })
    const [sessionId, text] = vi.mocked(backend.prompt).mock.calls[0]
    expect(sessionId).toBe('new-session')
    expect(text.split('\n')[0]).toBe('Agora ajuste o título')
    expect(text).toContain('Primeiro pedido nela:\nCrie o TODO')
    expect(text).toContain('Arquivos que ela alterou:\n- index.html')
    expect(text).toContain('Última resposta do agente nela:\nPronto, criei o TODO.')
    expect(vi.mocked(backend.prompt).mock.calls.some(([id]) => id === closed.id)).toBe(false)

    // The new conversation is the open one; it shows the person's words and keeps the context folded.
    expect(await screen.findByText('Título ajustado.')).toBeInTheDocument()
    expect(screen.getByText('Agora ajuste o título', { selector: '.turn-user p' })).toBeInTheDocument()
    expect(screen.getByText('Contexto levado da conversa anterior')).toBeInTheDocument()
    expect(screen.getByText(/Sessão new-sess/)).toBeInTheDocument()
    expect(screen.getByLabelText('Mensagem')).toHaveValue('')
    expect(screen.getByLabelText('Mensagem')).toBeEnabled()
    expect(screen.queryByText(/Somente leitura/)).not.toBeInTheDocument()
  })

  it('goes on with the same CLI when its saved default model is confirmed by its own catalog', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject([codex])
    backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'runtime-model' })
    backend.queryCLIModelCatalog = vi.fn(async () => ({ backendId: 'codex', source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'revision', searchTerm: '',
      models: [{ id: 'runtime-model', displayName: 'Modelo dinâmico', backendId: 'codex', source: 'codex_app_server', availability: 'listed' as const }],
      nextCursor: '', checkedAt: stamp, status: 'complete' as const, complete: true, accountFiltered: true }))
    const box = await openClosed(user, backend)
    await user.type(box, 'Siga daqui{Enter}')
    await waitFor(() => expect(backend.createDirectSession).toHaveBeenCalledTimes(1))
    expect(backend.createDirectSession).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'codex', reason: `Continuação da conversa ${closed.id}, encerrada`, modelId: 'runtime-model', reasoningEffort: '', catalogRevision: 'revision' })
  })

  it('keeps the closed conversation and what was typed when the new chat cannot be opened', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject()
    backend.createDirectSession = vi.fn(async () => { throw new Error('provider unavailable') })
    const box = await openClosed(user, backend)
    await user.type(box, 'Agora ajuste o título{Enter}')
    await waitFor(() => expect(backend.createDirectSession).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(screen.getByRole('status')).not.toHaveTextContent('Executando'))
    expect(backend.prompt).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Mensagem')).toHaveValue('Agora ajuste o título')
    expect(screen.getByText('Pronto, criei o TODO.')).toBeInTheDocument()
    expect(screen.getByText(/Somente leitura/)).toBeInTheDocument()
  })

  it('keeps what was typed when the message cannot be sent in the new chat', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject()
    backend.prompt = vi.fn(async () => { throw new Error('provider temporarily unavailable') })
    const box = await openClosed(user, backend)
    await user.type(box, 'Agora ajuste o título{Enter}')
    await waitFor(() => expect(backend.prompt).toHaveBeenCalledTimes(1))
    // The new chat is open and empty, and the person's words are back in its box to be sent again.
    await waitFor(() => expect(screen.getByLabelText('Mensagem')).toHaveValue('Agora ajuste o título'))
    expect(screen.getByText(/Sessão new-sess/)).toBeInTheDocument()
    expect(screen.getByLabelText('Mensagem')).toBeEnabled()
    expect(screen.queryByText(/Somente leitura/)).not.toBeInTheDocument()
  })

  it('says what is missing when no backend can carry the conversation on', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject([codex])
    const box = await openClosed(user, backend)
    await user.type(box, 'Siga daqui{Enter}')
    expect(await screen.findByRole('status')).toHaveTextContent('Nenhuma IA disponível para continuar')
    expect(backend.createDirectSession).not.toHaveBeenCalled()
    expect(backend.prompt).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Mensagem')).toHaveValue('Siga daqui')
  })

  it('lets the person leave a closed conversation whose journal ends at an approval nobody can give any more', async () => {
    const user = userEvent.setup()
    const { backend, journals } = closedProject()
    journals[closed.id] = [...oldJournal().slice(0, 5), { id: 'o6', streamId: closed.id, sequence: 6, type: 'approval.requested', data: { approvalId: 'a1', toolCallId: 't2', name: 'write', risk: 'write', arguments: {} }, createdAt: stamp }]
    const box = await openClosed(user, backend)
    expect(screen.getByRole('status')).toHaveTextContent('Aguardando aprovação')
    expect(screen.getByRole('button', { name: 'Aprovar' })).toBeDisabled()
    expect(box).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Continuar em novo chat' }))
    expect(await screen.findByRole('heading', { name: 'Começar pelo SDD' })).toBeInTheDocument()
  })

  it('opens one new chat even when Enter reaches the box twice before the screen has drawn again', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject()
    const box = await openClosed(user, backend)
    await user.type(box, 'Siga daqui')
    await act(async () => {
      fireEvent.keyDown(box, { key: 'Enter' })
      fireEvent.keyDown(box, { key: 'Enter' })
    })
    await waitFor(() => expect(backend.prompt).toHaveBeenCalledTimes(1))
    expect(backend.createDirectSession).toHaveBeenCalledTimes(1)
  })

  it('notices a conversation that was closed behind its back and carries on in a new chat', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject()
    let closedNow = false
    const open = { ...closed, backendId: 'local' }
    backend.listSessions = async () => [open]
    backend.openSession = vi.fn(async (id: string) => id === closed.id ? { ...open, resumable: !closedNow } : { id, workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata })
    const sendToNew = backend.prompt
    backend.prompt = vi.fn(async (id: string, text: string) => {
      if (id === closed.id) throw Object.assign(new Error('prompt: operation failed'), { cause: { code: 'pipeline_code_session_closed' } })
      return sendToNew(id, text)
    })
    const box = await openClosed(user, backend)
    expect(screen.queryByText(/Somente leitura/)).not.toBeInTheDocument()
    // The Code is verified from the Pipelines page while this conversation stays open on the screen.
    closedNow = true
    await user.type(box, 'Ajuste o título{Enter}')
    expect(await screen.findByText(/Somente leitura: esta execução foi encerrada/)).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Esta execução de Code já foi verificada')
    expect(screen.getByLabelText('Mensagem')).toHaveValue('Ajuste o título')
    expect(backend.createDirectSession).not.toHaveBeenCalled()
    // The same words, sent again, no longer fail: they open a new chat that carries the old one.
    await user.type(screen.getByLabelText('Mensagem'), '{Enter}')
    await waitFor(() => expect(vi.mocked(backend.prompt).mock.calls.some(([id]) => id === 'new-session')).toBe(true))
    expect(backend.createDirectSession).toHaveBeenCalledTimes(1)
    const [, text] = vi.mocked(backend.prompt).mock.calls.find(([id]) => id === 'new-session')!
    expect(text.split('\n')[0]).toBe('Ajuste o título')
    expect(text).toContain('Contexto da conversa anterior')
  })

  const unconfirmedCatalog = (): ModelCatalogResult => ({ backendId: 'codex', source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'revision', searchTerm: '',
    models: [{ id: 'another-model', displayName: 'Outro modelo', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }], nextCursor: '', checkedAt: stamp, status: 'complete', complete: true, accountFiltered: true })

  it('goes on to the next backend when the first one cannot start the conversation', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject([codex, local])
    backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'gpt-5-codex' })
    backend.queryCLIModelCatalog = vi.fn(async () => unconfirmedCatalog())
    const box = await openClosed(user, backend)
    await user.type(box, 'Siga daqui{Enter}')
    await waitFor(() => expect(backend.prompt).toHaveBeenCalledTimes(1))
    // The CLI that ran the old conversation was tried first, and its catalog did not confirm the saved model.
    expect(backend.queryCLIModelCatalog).toHaveBeenCalledTimes(1)
    expect(backend.createDirectSession).toHaveBeenCalledTimes(1)
    expect(backend.createDirectSession).toHaveBeenCalledWith(expect.objectContaining({ backendId: 'local' }))
    expect(vi.mocked(backend.prompt).mock.calls[0][0]).toBe('new-session')
  })

  it('says why when no backend can start the conversation, and keeps the words', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject([codex])
    backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'gpt-5-codex' })
    backend.queryCLIModelCatalog = vi.fn(async () => unconfirmedCatalog())
    const box = await openClosed(user, backend)
    await user.type(box, 'Siga daqui{Enter}')
    expect(await screen.findByRole('status')).toHaveTextContent('O catálogo do CLI não confirmou o modelo padrão salvo')
    expect(backend.createDirectSession).not.toHaveBeenCalled()
    expect(backend.prompt).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Mensagem')).toHaveValue('Siga daqui')
  })

  it('keeps the context of the old conversation for a retry when the first message of the new one fails', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject()
    const send = backend.prompt
    let attempts = 0
    backend.prompt = vi.fn(async (id: string, text: string) => {
      if (id === 'new-session' && ++attempts === 1) throw new Error('provider temporarily unavailable')
      return send(id, text)
    })
    const box = await openClosed(user, backend)
    await user.type(box, 'Agora ajuste o título{Enter}')
    await waitFor(() => expect(screen.getByLabelText('Mensagem')).toHaveValue('Agora ajuste o título'))
    expect(screen.queryByText(/Somente leitura/)).not.toBeInTheDocument()
    // The retry goes through the ordinary box of the new chat, and still carries what the first attempt carried.
    await user.type(screen.getByLabelText('Mensagem'), '{Enter}')
    await waitFor(() => expect(backend.prompt).toHaveBeenCalledTimes(2))
    const [[, first], [, second]] = vi.mocked(backend.prompt).mock.calls
    expect(first).toContain('Contexto da conversa anterior')
    expect(second).toBe(first)
    expect(await screen.findByText('Título ajustado.')).toBeInTheDocument()
    expect(screen.getByLabelText('Mensagem')).toHaveValue('')
  })

  it('lets new work start while a closed conversation waits on an approval nobody can give any more', async () => {
    const user = userEvent.setup()
    const { backend, journals } = closedProject()
    journals[closed.id] = [...oldJournal().slice(0, 5), { id: 'o6', streamId: closed.id, sequence: 6, type: 'approval.requested', data: { approvalId: 'a1', toolCallId: 't2', name: 'write', risk: 'write', arguments: {} }, createdAt: stamp }]
    await openClosed(user, backend)
    await user.click(screen.getByRole('button', { name: 'Novo trabalho' }))
    expect(await screen.findByRole('heading', { name: 'Pipelines SDD' })).toBeInTheDocument()
    expect(screen.queryByText('Cancele ou conclua a execução atual antes de iniciar outro trabalho.')).not.toBeInTheDocument()
  })

  it('opens one new chat however many times Enter is pressed while it is being created', async () => {
    const user = userEvent.setup()
    const { backend } = closedProject()
    let create!: (session: Session) => void
    backend.createDirectSession = vi.fn(() => new Promise<Session>(resolve => { create = resolve }))
    const box = await openClosed(user, backend)
    await user.type(box, 'Siga daqui{Enter}')
    await waitFor(() => expect(backend.createDirectSession).toHaveBeenCalledTimes(1))
    // Words typed while the chat opens would be lost with the old conversation, so the box stays shut until it is open.
    expect(box).toBeDisabled()
    await user.type(box, '{Enter}{Enter}')
    expect(backend.createDirectSession).toHaveBeenCalledTimes(1)
    await act(async () => { create({ id: 'new-session', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata }) })
    await waitFor(() => expect(backend.prompt).toHaveBeenCalledTimes(1))
    expect(backend.createDirectSession).toHaveBeenCalledTimes(1)
  })
})

describe('event projection', () => {
  const event = (sequence: number, type: string, data: unknown = {}): AgentEvent => ({ id: `e${sequence}`, streamId: 's', sequence, type, data, createdAt: '' })

  it('keeps a user denial visible when the call is later skipped', () => {
    const view = project([
      event(1, 'run.started'),
      event(2, 'message.assistant', { content: '', toolCalls: [{ id: 'c1', name: 'write', arguments: {} }] }),
      event(3, 'approval.requested', { approvalId: 'a1', toolCallId: 'c1', name: 'write', risk: 'write' }),
      event(4, 'approval.denied', { approvalId: 'a1', toolCallId: 'c1' }),
      event(5, 'tool.skipped', { toolCallId: 'c1', error: 'tool call not executed: approval_denied' }),
      event(6, 'run.failed', { reason: 'approval_denied' }),
    ])
    const tool = view.messages[0]
    expect(tool.kind === 'tool' && tool.call.status).toBe('denied')
    expect(view.pendingApprovals).toEqual([])
    expect(view.activeRun).toBe('idle')
    expect(view.outcome).toEqual({ status: 'failed', reason: 'approval_denied' })
  })
})
