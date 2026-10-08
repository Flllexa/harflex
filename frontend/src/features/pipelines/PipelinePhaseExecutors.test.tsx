import { useState } from 'react'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Backend, BackendOption, ModelCatalogResult, ProviderProfile, StageExecutor } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { PipelinePhaseExecutors, phaseCanUse } from './PipelinePhaseExecutors'

afterEach(cleanup)

const date = '2026-10-02T10:00:00Z'
const profile = (id: string, over: Partial<ProviderProfile> = {}): ProviderProfile => ({ id, name: id, kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://example.test/v1', model: `${id}-model`, hasCredential: true, endpointBlocked: false, updatedAt: date, ...over })
const api: BackendOption = { id: 'api', name: 'API local', kind: 'api', available: true }
const other: BackendOption = { id: 'other', name: 'Outra API', kind: 'api', available: true }
const plain: BackendOption = { id: 'plain', name: 'Servidor genérico', kind: 'api', available: true }
const codex: BackendOption = { id: 'codex', name: 'Codex CLI', kind: 'cli', available: true, professionalAvailable: true }
const claude: BackendOption = { id: 'claude', name: 'Claude Code', kind: 'cli', available: true, professionalAvailable: true }
const opencode: BackendOption = { id: 'opencode', name: 'opencode', kind: 'cli', available: true, professionalAvailable: true }
const profiles = [profile('api', { name: 'API local' }), profile('other', { name: 'Outra API' }), profile('plain', { name: 'Servidor genérico', providerType: 'generic' })]
const catalogOf = (backendId: string, source: string, ids: string[]): ModelCatalogResult => ({ backendId, source, destination: backendId, profileRevision: 'revision', credentialToken: 'a'.repeat(64), searchTerm: '',
  models: ids.map(id => ({ id, displayName: id, backendId, source, availability: 'available' })), nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete', complete: true, accountFiltered: true })
const executor = (stage: StageExecutor['stage'], backendId: string, modelId = ''): StageExecutor => ({ workspaceId: 'workspace-1', stage, backendId, modelId, updatedAt: date })

function fixture(initial: StageExecutor[] = []) {
  const { backend } = createFakeBackend()
  backend.queryHTTPModelCatalog = vi.fn(async query => catalogOf(query.profileId, 'openai_models', [`${query.profileId}-model`, 'large-model']))
  backend.queryCLIModelCatalog = vi.fn(async query => catalogOf(query.backendId, 'codex_app_server', ['gpt-5-codex', 'gpt-5-mini']))
  const save = vi.spyOn(backend, 'saveStageExecutor')
  const clear = vi.spyOn(backend, 'clearStageExecutor')
  for (const item of initial) void backend.saveStageExecutor(item)
  save.mockClear()
  return { backend, save, clear, initial }
}

function Harness({ backend, backends = [api, other, plain, codex], initial = [], open = true, ...rest }: { backend: Backend; backends?: BackendOption[]; initial?: StageExecutor[]; open?: boolean } & Partial<Parameters<typeof PipelinePhaseExecutors>[0]>) {
  const [executors, setExecutors] = useState(initial)
  return <PipelinePhaseExecutors backend={backend} backends={backends} profiles={profiles} workspaceId="workspace-1" executors={executors} onExecutorsChange={setExecutors}
    defaults={{ backendId: 'api', modelBackendId: 'api', modelId: 'api-model' }} open={open} onToggle={() => undefined} {...rest} />
}

const picker = (id: string) => screen.getByTestId(`picker-${id}`)
async function choose(user: ReturnType<typeof userEvent.setup>, id: string, option: string) {
  await user.click(within(picker(id)).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: option }))
}

describe('what each phase can run on', () => {
  it('takes an API profile with a catalog for documents, Code and QA, and Codex or Claude Code when the Harflex can hold them to its contract', () => {
    for (const stage of ['discovery', 'spec', 'plan', 'code', 'eval'] as const) {
      expect(phaseCanUse(stage, api, profiles[0])).toBe(true)
      expect(phaseCanUse(stage, plain, profiles[2])).toBe(false)
      expect(phaseCanUse(stage, codex)).toBe(true)
      expect(phaseCanUse(stage, { ...codex, professionalAvailable: false })).toBe(false)
      expect(phaseCanUse(stage, claude)).toBe(true)
      expect(phaseCanUse(stage, { ...claude, professionalAvailable: false })).toBe(false)
      expect(phaseCanUse(stage, opencode)).toBe(false)
      expect(phaseCanUse(stage, { ...api, available: false }, profiles[0])).toBe(false)
      expect(phaseCanUse(stage, api, { ...profiles[0], hasCredential: false })).toBe(false)
      expect(phaseCanUse(stage, api, { ...profiles[0], endpointBlocked: true })).toBe(false)
      expect(phaseCanUse(stage, api, { ...profiles[0], providerType: 'ollama', hasCredential: false })).toBe(true)
    }
  })

  it('gives the pull requests only the profiles whose tool loop it can use, a generic server included', () => {
    expect(phaseCanUse('prs', api, profiles[0])).toBe(true)
    expect(phaseCanUse('prs', plain, profiles[2])).toBe(true)
    expect(phaseCanUse('prs', codex)).toBe(false)
    expect(phaseCanUse('prs', claude)).toBe(false)
  })
})

describe('the card of phases', () => {
  it('lists the six phases and says each follows the default until it is chosen', async () => {
    const { backend } = fixture()
    render(<Harness backend={backend} />)
    const list = await screen.findByRole('list', { name: 'Executor de cada fase' })
    expect(within(list).getAllByRole('listitem').map(item => item.querySelector('strong')?.textContent)).toEqual(['Discovery', 'SPEC', 'Plan', 'Code', 'QA', 'PRs'])
    expect(within(list).getAllByText('Segue o padrão: API local · api-model.')).toHaveLength(6)
    expect(screen.getByText('Todas seguem o padrão das Configurações')).toBeInTheDocument()
    expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  })

  it('offers each phase only what it can run on', async () => {
    const { backend } = fixture()
    render(<Harness backend={backend} />)
    await waitFor(() => expect(within(picker('phase-executor-spec')).getByRole('button')).toBeEnabled())
    const optionsOf = async (id: string) => {
      const user = userEvent.setup()
      await user.click(within(picker(id)).getByRole('button'))
      const names = (await screen.findAllByRole('option')).map(item => item.textContent)
      await user.keyboard('{Escape}')
      return names
    }
    expect(await optionsOf('phase-executor-spec')).toEqual(['Usar o padrão', 'API local · API', 'Outra API · API', 'Codex CLI · CLI'])
    expect(await optionsOf('phase-executor-prs')).toEqual(['Usar o padrão', 'API local · API', 'Outra API · API', 'Servidor genérico · API'])
  })

  it('saves an API profile at once, with the model of the profile', async () => {
    const user = userEvent.setup()
    const { backend, save } = fixture()
    render(<Harness backend={backend} />)
    await waitFor(() => expect(within(picker('phase-executor-spec')).getByRole('button')).toBeEnabled())
    await choose(user, 'phase-executor-spec', 'Outra API · API')
    await waitFor(() => expect(save).toHaveBeenCalledWith({ workspaceId: 'workspace-1', stage: 'spec', backendId: 'other', modelId: '' }))
    expect(await screen.findByText('Salvo · Outra API · modelo do perfil')).toBeInTheDocument()
    expect(screen.getByText('1 de 6 com escolha própria')).toBeInTheDocument()
    // The other phases are untouched.
    expect(screen.getAllByText('Segue o padrão: API local · api-model.')).toHaveLength(5)
    // The models of that provider are read to be offered.
    await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledWith(expect.objectContaining({ profileId: 'other' })))
  })

  it('keeps both phases when the answer for the first arrives after the second was saved', async () => {
    const user = userEvent.setup()
    const { backend } = fixture()
    let finishSpec!: () => void
    const real = backend.saveStageExecutor.bind(backend)
    backend.saveStageExecutor = vi.fn(async input => {
      const saved = await real(input)
      if (input.stage === 'spec') await new Promise<void>(resolve => { finishSpec = resolve })
      return saved
    })
    render(<Harness backend={backend} />)
    await waitFor(() => expect(within(picker('phase-executor-spec')).getByRole('button')).toBeEnabled())
    await choose(user, 'phase-executor-spec', 'Outra API · API')
    await waitFor(() => expect(backend.saveStageExecutor).toHaveBeenCalledTimes(1))
    await choose(user, 'phase-executor-plan', 'API local · API')
    expect(await screen.findByText('1 de 6 com escolha própria')).toBeInTheDocument()
    finishSpec()
    expect(await screen.findByText('2 de 6 com escolha própria')).toBeInTheDocument()
    expect(screen.getByText('Salvo · Outra API · modelo do perfil')).toBeInTheDocument()
    expect(screen.getByText('Salvo · API local · modelo do perfil')).toBeInTheDocument()
  })

  it('keeps a CLI unsaved until it has a model, then saves both', async () => {
    const user = userEvent.setup()
    const { backend, save } = fixture()
    render(<Harness backend={backend} />)
    await waitFor(() => expect(within(picker('phase-executor-code')).getByRole('button')).toBeEnabled())
    await choose(user, 'phase-executor-code', 'Codex CLI · CLI')
    expect(await screen.findByText('Escolha um modelo de Codex CLI para salvar.')).toBeInTheDocument()
    expect(save).not.toHaveBeenCalled()
    await waitFor(() => expect(backend.queryCLIModelCatalog).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'codex' }))
    await waitFor(() => expect(within(picker('phase-model-code')).getByRole('combobox')).toBeEnabled())
    await user.click(within(picker('phase-model-code')).getByRole('button', { name: /Abrir opções de Modelo de Code/ }))
    await user.click(await screen.findByRole('option', { name: 'gpt-5-codex' }))
    await waitFor(() => expect(save).toHaveBeenCalledWith({ workspaceId: 'workspace-1', stage: 'code', backendId: 'codex', modelId: 'gpt-5-codex' }))
    expect(await screen.findByText('Salvo · Codex CLI · gpt-5-codex')).toBeInTheDocument()
  })

  it('changes the model of a phase that already has an executor', async () => {
    const user = userEvent.setup()
    const { backend, save } = fixture([executor('plan', 'other')])
    render(<Harness backend={backend} initial={[executor('plan', 'other')]} />)
    await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalled())
    await waitFor(() => expect(within(picker('phase-model-plan')).getByRole('combobox')).toBeEnabled())
    await user.click(within(picker('phase-model-plan')).getByRole('button', { name: /Abrir opções de Modelo de Plan/ }))
    await user.click(await screen.findByRole('option', { name: 'large-model' }))
    await waitFor(() => expect(save).toHaveBeenCalledWith({ workspaceId: 'workspace-1', stage: 'plan', backendId: 'other', modelId: 'large-model' }))
    expect(await screen.findByText('Salvo · Outra API · large-model')).toBeInTheDocument()
  })

  it('returns a phase to the default of Settings', async () => {
    const user = userEvent.setup()
    const { backend, clear } = fixture([executor('eval', 'other', 'large-model')])
    render(<Harness backend={backend} initial={[executor('eval', 'other', 'large-model')]} />)
    await waitFor(() => expect(within(picker('phase-executor-eval')).getByRole('button')).toBeEnabled())
    await choose(user, 'phase-executor-eval', 'Usar o padrão')
    await waitFor(() => expect(clear).toHaveBeenCalledWith('workspace-1', 'eval'))
    await waitFor(() => expect(screen.getByText('Todas seguem o padrão das Configurações')).toBeInTheDocument())
    expect(screen.queryByTestId('picker-phase-model-eval')).not.toBeInTheDocument()
  })

  it('marks the phase the open pipeline is in', async () => {
    const { backend } = fixture()
    render(<Harness backend={backend} currentStage="plan" />)
    const rows = await screen.findAllByRole('listitem')
    expect(rows.filter(row => row.getAttribute('aria-current') === 'step').map(row => row.querySelector('strong')?.textContent)).toEqual(['Plan'])
  })

  it('reads no catalog while it is closed', async () => {
    const { backend } = fixture([executor('spec', 'other')])
    render(<Harness backend={backend} initial={[executor('spec', 'other')]} open={false} />)
    await waitFor(() => expect(screen.getByText('1 de 6 com escolha própria')).toBeInTheDocument())
    expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  })
})

describe('when something is wrong', () => {
  it('keeps showing a saved executor that cannot be offered any more, and says so', async () => {
    const { backend } = fixture([executor('spec', 'gone', 'some-model')])
    render(<Harness backend={backend} initial={[executor('spec', 'gone', 'some-model')]} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('gone não está disponível para SPEC agora. Escolha outro executor ou volte ao padrão.')
    expect(within(picker('phase-executor-spec')).getByRole('button')).toHaveTextContent('gone · indisponível')
  })

  it('says when the catalog no longer lists the saved model', async () => {
    const { backend } = fixture([executor('plan', 'other', 'retired-model')])
    render(<Harness backend={backend} initial={[executor('plan', 'other', 'retired-model')]} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('O catálogo de Outra API não lista mais retired-model. Escolha outro modelo.')
    expect(screen.queryByText(/Salvo · Outra API/)).not.toBeInTheDocument()
  })

  it('says why the models could not be read, and tries again on request', async () => {
    const user = userEvent.setup()
    const { backend } = fixture([executor('plan', 'other', 'large-model')])
    const ask = vi.fn().mockRejectedValueOnce(new Error('offline')).mockImplementation(async (query: { profileId: string }) => catalogOf(query.profileId, 'openai_models', ['large-model']))
    backend.queryHTTPModelCatalog = ask
    render(<Harness backend={backend} initial={[executor('plan', 'other', 'large-model')]} />)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Tentar de novo')
    await user.click(within(alert).getByRole('button', { name: 'Tentar de novo' }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(ask).toHaveBeenCalledTimes(2)
    expect(await screen.findByText('Salvo · Outra API · large-model')).toBeInTheDocument()
  })

  it('says when Claude Code is not logged in instead of a generic failure', async () => {
    const { backend } = fixture()
    backend.queryCLIModelCatalog = vi.fn(async () => ({ ...catalogOf('claude', 'claude_cli', []), status: 'failed' as const, complete: false, errorCode: 'catalog_not_logged_in' }))
    render(<Harness backend={backend} backends={[api, claude]} initial={[executor('spec', 'claude')]} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Claude Code não está autenticado. Faça o login no terminal com "claude auth login" e tente de novo.')
  })

  it('shows why a choice was refused and keeps the phase as it was', async () => {
    const user = userEvent.setup()
    const { backend } = fixture()
    backend.saveStageExecutor = vi.fn(async () => { throw Object.assign(new Error('save: operation failed'), { cause: { code: 'stage_executor_unsupported' } }) })
    render(<Harness backend={backend} />)
    await waitFor(() => expect(within(picker('phase-executor-spec')).getByRole('button')).toBeEnabled())
    await choose(user, 'phase-executor-spec', 'Outra API · API')
    expect(await screen.findByRole('alert')).toHaveTextContent('Esta fase não pode usar esse executor.')
    expect(screen.getByText('Todas seguem o padrão das Configurações')).toBeInTheDocument()
    expect(within(picker('phase-executor-spec')).getByRole('button')).toHaveTextContent('Usar o padrão')
  })

  it('does not let anything be changed while the saved choices cannot be read', async () => {
    const user = userEvent.setup()
    const retry = vi.fn()
    const { backend } = fixture()
    render(<Harness backend={backend} loadFailed onRetry={retry} />)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Não foi possível ler as escolhas por fase')
    for (const stage of ['discovery', 'spec', 'plan', 'code', 'eval', 'prs']) expect(within(picker(`phase-executor-${stage}`)).getByRole('button')).toBeDisabled()
    await user.click(within(alert).getByRole('button', { name: 'Tentar de novo' }))
    expect(retry).toHaveBeenCalledTimes(1)
  })
})

describe('what the card does not take for granted', () => {
  const lm: BackendOption = { id: 'lm', name: 'LM Studio', kind: 'api', available: true }
  const lmProfile = profile('lm', { name: 'LM Studio', providerType: 'lm_studio', hasCredential: false })
  const router: BackendOption = { id: 'router', name: 'OpenRouter', kind: 'api', available: true }
  const routerProfile = profile('router', { name: 'OpenRouter', providerType: 'openrouter' })
  const listing = (query: { profileId: string }, source: string, models: Array<{ id: string; loaded?: boolean }>): ModelCatalogResult => ({ ...catalogOf(query.profileId, source, []),
    models: models.map(model => ({ id: model.id, displayName: model.id, backendId: query.profileId, source, availability: 'available' as const, ...(model.loaded === undefined ? {} : { loaded: model.loaded }) })) })
  const rowOf = (index: number) => screen.getAllByRole('listitem')[index]

  it('counts the saved choices that no longer work, and says nothing of it while the choices are being read', async () => {
    const saved = [executor('spec', 'gone', 'm'), executor('plan', 'api'), executor('code', 'gone-too')]
    const { backend } = fixture(saved)
    const first = render(<Harness backend={backend} initial={saved} />)
    expect(await screen.findByText('3 de 6 com escolha própria')).toBeInTheDocument()
    expect(screen.getByText('2 indisponíveis')).toBeInTheDocument()
    first.unmount()
    const second = render(<Harness backend={backend} initial={[executor('spec', 'gone', 'm')]} />)
    expect(await screen.findByText('1 indisponível')).toBeInTheDocument()
    second.unmount()
    render(<Harness backend={backend} initial={saved} loading />)
    expect(await screen.findByText('Lendo as escolhas…')).toBeInTheDocument()
    expect(screen.queryByText(/^\d+ indisponíve/)).not.toBeInTheDocument()
  })

  it('has no model to choose for a generic server, and asks it for no catalog', async () => {
    const { backend } = fixture([executor('prs', 'plain')])
    render(<Harness backend={backend} initial={[executor('prs', 'plain')]} />)
    expect(await screen.findByText('Salvo · Servidor genérico · modelo do perfil')).toBeInTheDocument()
    expect(screen.queryByTestId('picker-phase-model-prs')).not.toBeInTheDocument()
    expect(screen.queryByText(/^\d+ indisponíve/)).not.toBeInTheDocument()
    expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  })

  it('does not offer a document a model LM Studio has not loaded, since nobody is there to load it, but offers it to Code', async () => {
    const user = userEvent.setup()
    const saved = [executor('spec', 'lm'), executor('code', 'lm')]
    const { backend } = fixture(saved)
    backend.queryHTTPModelCatalog = vi.fn(async query => listing(query, 'lm_studio_models', [{ id: 'warm-model', loaded: true }, { id: 'cold-model', loaded: false }]))
    render(<Harness backend={backend} backends={[lm]} profiles={[lmProfile]} initial={saved} defaults={{ backendId: 'lm', modelBackendId: 'lm', modelId: 'warm-model' }} />)
    await waitFor(() => expect(within(picker('phase-model-spec')).getByRole('combobox')).toBeEnabled())
    await user.click(within(picker('phase-model-spec')).getByRole('button', { name: /Abrir opções de Modelo de SPEC/ }))
    expect(await screen.findByRole('option', { name: 'cold-model · não carregado' })).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByRole('option', { name: 'warm-model' })).not.toHaveAttribute('aria-disabled', 'true')
    await user.keyboard('{Escape}')
    await user.click(within(picker('phase-model-code')).getByRole('button', { name: /Abrir opções de Modelo de Code/ }))
    expect(await screen.findByRole('option', { name: 'cold-model' })).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('says a document cannot use the general list of OpenRouter, which asks to be confirmed at every use, and says nothing to Code', async () => {
    const saved = [executor('plan', 'router', 'x-model'), executor('code', 'router', 'x-model')]
    const { backend } = fixture(saved)
    backend.queryHTTPModelCatalog = vi.fn(async query => listing(query, 'openrouter_general_unfiltered', [{ id: 'x-model' }]))
    render(<Harness backend={backend} backends={[router]} profiles={[routerProfile]} initial={saved} defaults={{ backendId: 'router', modelBackendId: '', modelId: '' }} />)
    expect(await within(rowOf(2)).findByText(/A lista geral da OpenRouter pede uma confirmação a cada uso/)).toBeInTheDocument()
    expect(within(rowOf(3)).getByText('Salvo · OpenRouter · x-model')).toBeInTheDocument()
    expect(within(rowOf(3)).queryByText(/lista geral da OpenRouter/)).not.toBeInTheDocument()
  })

  it('says when the default of Settings cannot serve a phase, and when there is no default at all', async () => {
    const { backend } = fixture()
    const generic = render(<Harness backend={backend} defaults={{ backendId: 'plain', modelBackendId: '', modelId: '' }} />)
    expect(await screen.findAllByText(/O padrão \(Servidor genérico · plain-model\) não serve a /)).toHaveLength(5)
    expect(within(rowOf(5)).getByText('Segue o padrão: Servidor genérico · plain-model.')).toBeInTheDocument()
    generic.unmount()
    render(<Harness backend={backend} defaults={{ backendId: '', modelBackendId: '', modelId: '' }} />)
    expect(await screen.findAllByText(/Não há padrão definido nas Configurações\. Escolha um executor para /)).toHaveLength(6)
  })

  it('claims nothing about an executor, and changes nothing, while the profiles are still being read', async () => {
    const saved = [executor('spec', 'other', 'large-model')]
    const { backend } = fixture(saved)
    render(<Harness backend={backend} initial={saved} profiles={undefined} />)
    expect(await screen.findByText('Salvo · Outra API · large-model')).toBeInTheDocument()
    expect(within(picker('phase-executor-spec')).getByRole('button')).toHaveTextContent('Outra API')
    expect(within(picker('phase-executor-spec')).getByRole('button')).not.toHaveTextContent('indisponível')
    expect(screen.queryByText(/^\d+ indisponíve/)).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    for (const stage of ['discovery', 'spec', 'plan', 'code', 'eval', 'prs']) expect(within(picker(`phase-executor-${stage}`)).getByRole('button')).toBeDisabled()
    expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  })

  it('locks every picker while the saved choices are being read', async () => {
    const { backend } = fixture()
    render(<Harness backend={backend} loading />)
    expect(await screen.findByText('Lendo as escolhas…')).toBeInTheDocument()
    for (const stage of ['discovery', 'spec', 'plan', 'code', 'eval', 'prs']) expect(within(picker(`phase-executor-${stage}`)).getByRole('button')).toBeDisabled()
  })

  it('does not hand a project a change that ended after the card was gone', async () => {
    const user = userEvent.setup()
    const { backend } = fixture()
    let finish: (() => void) | undefined
    const real = backend.saveStageExecutor.bind(backend)
    backend.saveStageExecutor = vi.fn(async input => {
      const saved = await real(input)
      await new Promise<void>(resolve => { finish = resolve })
      return saved
    })
    const onChange = vi.fn()
    const view = render(<Harness backend={backend} onExecutorsChange={onChange} />)
    await waitFor(() => expect(within(picker('phase-executor-spec')).getByRole('button')).toBeEnabled())
    await choose(user, 'phase-executor-spec', 'Outra API · API')
    await waitFor(() => expect(finish).toBeTypeOf('function'))
    view.unmount()
    finish!()
    await new Promise(resolve => setTimeout(resolve, 30))
    expect(onChange).not.toHaveBeenCalled()
  })

  it('draws the pickers again from what is saved when their choice could not be saved', async () => {
    const user = userEvent.setup()
    const { backend } = fixture([executor('plan', 'other', 'large-model')])
    backend.saveStageExecutor = vi.fn(async () => { throw new Error('offline') })
    render(<Harness backend={backend} initial={[executor('plan', 'other', 'large-model')]} />)
    await waitFor(() => expect(within(picker('phase-model-plan')).getByRole('combobox')).toBeEnabled())
    expect(within(picker('phase-model-plan')).getByRole('combobox')).toHaveValue('large-model')
    await user.click(within(picker('phase-model-plan')).getByRole('button', { name: /Abrir opções de Modelo de Plan/ }))
    await user.click(await screen.findByRole('option', { name: 'other-model' }))
    expect(await screen.findByText('A operação falhou. Consulte os eventos da sessão.')).toBeInTheDocument()
    await waitFor(() => expect(within(picker('phase-model-plan')).getByRole('combobox')).toHaveValue('large-model'))
    await choose(user, 'phase-executor-plan', 'API local · API')
    await waitFor(() => expect(backend.saveStageExecutor).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(within(picker('phase-executor-plan')).getByRole('button')).toHaveTextContent('Outra API'))
    expect(screen.getByText('Salvo · Outra API · large-model')).toBeInTheDocument()
  })
})
