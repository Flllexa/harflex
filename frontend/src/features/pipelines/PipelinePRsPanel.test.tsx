import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Backend, MCPServer, Pipeline, PipelineStageActivity } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { PipelinePRsOutcome, PipelinePRsPanel, pullRequestsOpen, reviewFixesRequest } from './PipelinePRsPanel'

afterEach(cleanup)

const date = '2026-10-02T10:00:00Z'
const approved = { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed' } as const
const pipeline = (overrides: Partial<Pipeline> = {}): Pipeline => ({
  id: 'pipeline-prs', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'Exportar faturas', objective: 'CSV', currentStage: 'prs', stageStatus: { ...approved, prs: 'active' }, revision: 9,
  artifacts: { code: { stage: 'code', version: 1, content: 'diff', author: 'ai', sourceSessionId: 'coder-session', updatedAt: date } }, codeAppliedAt: date, createdAt: date, updatedAt: date, ...overrides,
})
const github: MCPServer = { id: 'mcp-1', workspaceId: 'workspace-1', name: 'GitHub', transport: 'http', command: '', args: [], url: 'https://api.githubcopilot.com/mcp/', tokenEnvVar: '', authScheme: 'bearer', enabled: true, tools: [], hasCredential: true, createdAt: date, updatedAt: date }
const activity = (status: string): PipelineStageActivity => ({ pipelineId: 'pipeline-prs', workspaceId: 'workspace-1', stage: 'prs', status, phase: '', sessionId: 'session-prs', modelId: '', updatedAt: date })

function backendWith(servers: MCPServer[] = [github], status?: string): Backend {
  const { backend } = createFakeBackend()
  const profile = { id: 'api-work', name: 'API local', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: 'api-model', hasCredential: true, endpointBlocked: false, updatedAt: new Date().toISOString() }
  backend.listProviderProfiles = async () => [profile]
  backend.queryHTTPModelCatalog = async query => ({ backendId: query.profileId, source: 'openai_models', destination: profile.name, profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: [{ id: 'api-model', displayName: 'Modelo API', backendId: query.profileId, source: 'openai_models', availability: 'available' }], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete', complete: true, accountFiltered: true })
  backend.listMCPServers = async () => servers
  backend.getPipelineStageActivity = async () => status ? activity(status) : { ...activity('ready'), sessionId: '' }
  return backend
}
const apiBackends = [{ id: 'api-work', name: 'API local', kind: 'api' as const, available: true }]
type Overrides = Partial<Parameters<typeof PipelinePRsPanel>[0]>
function show(backend: Backend, overrides: Overrides = {}) {
  const handlers = { onStart: vi.fn(), onFinish: vi.fn(), onOpenSession: vi.fn(async () => undefined) }
  render(<PipelinePRsPanel backend={backend} backends={apiBackends} run={pipeline()} workspaceId="workspace-1" defaultBackendId="api-work" pending={false}
    permissionProfile="ask" permissionControl={<span>MENU DE PERMISSÕES</span>} {...handlers} {...overrides} />)
  return handlers
}
const panel = () => screen.getByRole('region', { name: 'Abrir pull requests' })

describe('when the pull request stage is open', () => {
  it('is open once QA is approved: as the current stage, or on a pipeline that finished before the stage existed', () => {
    expect(pullRequestsOpen(pipeline())).toBe(true)
    expect(pullRequestsOpen(pipeline({ currentStage: '', stageStatus: { ...approved } }))).toBe(true)
    expect(pullRequestsOpen(pipeline({ currentStage: '', stageStatus: { ...approved, prs: 'pending' } }))).toBe(true)
    expect(pullRequestsOpen(pipeline({ currentStage: '', stageStatus: { ...approved, prs: 'completed' } }))).toBe(false)
    expect(pullRequestsOpen(pipeline({ currentStage: '', stageStatus: { ...approved, prs: 'skipped' } }))).toBe(false)
    expect(pullRequestsOpen(pipeline({ currentStage: 'eval', stageStatus: { ...approved, eval: 'waiting_user', prs: 'pending' } }))).toBe(false)
  })
})

describe('the pull request panel', () => {
  it('checks what the agent needs and starts only with the patch applied and a model chosen', async () => {
    const user = userEvent.setup()
    const { onStart } = show(backendWith())
    expect(await within(panel()).findByText('Servidor MCP conectado: GitHub.')).toBeInTheDocument()
    expect(within(panel()).getByText('Patch aprovado aplicado ao projeto.')).toBeInTheDocument()
    expect(within(panel()).getByText(/Hoje a IA pede a sua aprovação/)).toBeInTheDocument()
    expect(within(panel()).getByText('MENU DE PERMISSÕES')).toBeInTheDocument()
    const start = within(panel()).getByRole('button', { name: 'Abrir PRs com a IA' })
    // The chat uses the profile's own model, so nothing has to be chosen from a catalog before it can start.
    expect(await within(panel()).findByText('api-model')).toBeInTheDocument()
    expect(within(panel()).getByText(/o configurado no perfil/)).toBeInTheDocument()
    expect(within(panel()).queryByRole('combobox', { name: /Modelo da fase/ })).not.toBeInTheDocument()
    await waitFor(() => expect(start).toBeEnabled())
    await user.click(start)
    expect(onStart).toHaveBeenCalledWith('api-work')
    expect(within(panel()).queryByRole('button', { name: 'Concluir PRs' })).not.toBeInTheDocument()
  })

  it('applies the approved patch and then starts the agent, in one click', async () => {
    const order: string[] = []
    const onApply = vi.fn(async () => { order.push('apply'); return true })
    const handlers = show(backendWith(), { run: pipeline({ codeAppliedAt: undefined, codePatchPending: true }), onApply, onStart: vi.fn(() => { order.push('start') }) })
    expect(await within(panel()).findByText(/vai para a pasta do projeto quando você clicar em Aprovar e abrir os PRs/)).toBeInTheDocument()
    await userEvent.click(within(panel()).getByRole('button', { name: 'Aprovar e abrir os PRs' }))
    await waitFor(() => expect(order).toEqual(['apply', 'start']))
    expect(handlers.onStart).not.toHaveBeenCalled()
  })

  it('does not start the agent when the patch could not be applied', async () => {
    const onStart = vi.fn()
    show(backendWith(), { run: pipeline({ codeAppliedAt: undefined, codePatchPending: true }), onApply: vi.fn(async () => false), onStart })
    await userEvent.click(await within(panel()).findByRole('button', { name: 'Aprovar e abrir os PRs' }))
    await waitFor(() => expect(within(panel()).getByRole('button', { name: 'Aprovar e abrir os PRs' })).toBeEnabled())
    expect(onStart).not.toHaveBeenCalled()
  })

  it('does not wait for a patch that an earlier Code run already wrote into the project', async () => {
    show(backendWith(), { run: pipeline({ codeAppliedAt: undefined }) })
    expect(await within(panel()).findByText('Patch aprovado aplicado ao projeto.')).toBeInTheDocument()
    expect(within(panel()).getByRole('button', { name: 'Abrir PRs com a IA' })).toBeEnabled()
  })

  it('says what is missing without blocking the work: no MCP server, or approvals still on', async () => {
    show(backendWith([]))
    expect(await within(panel()).findByText(/Nenhum servidor MCP conectado/)).toBeInTheDocument()
    expect(within(panel()).getByText(/entrega o link para você abrir o PR/)).toBeInTheDocument()
    cleanup()
    show(backendWith(), { permissionProfile: 'full_access' })
    expect(await within(panel()).findByText('Acesso total: a IA não pede aprovação.')).toBeInTheDocument()
    expect(within(panel()).queryByText(/Hoje a IA pede a sua aprovação/)).not.toBeInTheDocument()
  })

  it('offers an API profile or Claude Code, never Codex: the PRs return to their conversation, which Codex does not continue', () => {
    show(backendWith(), { backends: [{ id: 'codex', name: 'Codex CLI', kind: 'cli' as const, available: true }] })
    expect(within(panel()).getByText('Os PRs precisam de um provedor API ou do Claude Code')).toBeInTheDocument()
    expect(within(panel()).getByRole('button', { name: 'Abrir PRs com a IA' })).toBeDisabled()
  })

  it('runs on Claude Code when that is the phase default and no API profile is set up', async () => {
    const user = userEvent.setup()
    const { onStart } = show(backendWith(), { backends: [{ id: 'codex', name: 'Codex CLI', kind: 'cli' as const, available: true }, { id: 'claude', name: 'Claude Code', kind: 'cli' as const, available: true }], defaultBackendId: 'claude' })
    expect(within(panel()).queryByText('Os PRs precisam de um provedor API ou do Claude Code')).not.toBeInTheDocument()
    await user.click(within(panel()).getByRole('button', { name: 'Abrir PRs com a IA' }))
    expect(onStart).toHaveBeenCalledWith('claude')
  })

  it('follows the agent: working, then a finished report that can be concluded', async () => {
    const user = userEvent.setup()
    const working = backendWith([github], 'running')
    show(working)
    expect(await within(panel()).findByText('A IA está trabalhando nos PRs. Acompanhe na conversa.')).toBeInTheDocument()
    expect(within(panel()).getByRole('button', { name: 'Pedir outra rodada à IA' })).toBeDisabled()
    expect(within(panel()).queryByRole('button', { name: 'Concluir PRs' })).not.toBeInTheDocument()
    cleanup()

    const { onFinish, onOpenSession } = show(backendWith([github], 'completed'))
    expect(await within(panel()).findByText('A IA terminou. Leia o relatório na conversa e conclua a etapa.')).toBeInTheDocument()
    expect(within(panel()).getByRole('button', { name: 'Pedir outra rodada à IA' })).toBeInTheDocument()
    await user.click(within(panel()).getByRole('button', { name: 'Abrir a conversa dos PRs' }))
    expect(onOpenSession).toHaveBeenCalledWith('session-prs')
    await user.click(within(panel()).getByRole('button', { name: 'Concluir PRs' }))
    expect(onFinish).toHaveBeenCalledWith('completed')
  })

  it('follows the agent of a pipeline that finished before the stage existed, so it can be concluded', async () => {
    const user = userEvent.setup()
    const legacy = pipeline({ currentStage: '', stageStatus: { ...approved } })
    const { onFinish } = show(backendWith([github], 'completed'), { run: legacy })
    expect(await within(panel()).findByText('A IA terminou. Leia o relatório na conversa e conclua a etapa.')).toBeInTheDocument()
    expect(within(panel()).getByRole('button', { name: 'Pedir outra rodada à IA' })).toBeInTheDocument()
    await user.click(within(panel()).getByRole('button', { name: 'Concluir PRs' }))
    expect(onFinish).toHaveBeenCalledWith('completed')
  })

  it('skips only after a confirmation, with the reason', async () => {
    const user = userEvent.setup()
    const { onFinish } = show(backendWith())
    await user.click(within(panel()).getByRole('button', { name: 'Pular os PRs' }))
    expect(within(panel()).getByText(/sem abrir nenhum pull request/)).toBeInTheDocument()
    expect(onFinish).not.toHaveBeenCalled()
    await user.type(within(panel()).getByLabelText('Motivo (opcional)'), '  abro pelo site  ')
    await user.click(within(panel()).getByRole('button', { name: 'Confirmar pulo' }))
    expect(onFinish).toHaveBeenCalledWith('skipped', 'abro pelo site')
  })

  it('explains a failed run and lets the person try again', async () => {
    show(backendWith([github], 'failed'))
    expect(await within(panel()).findByText(/parou por um erro/)).toBeInTheDocument()
    expect(within(panel()).queryByRole('button', { name: 'Concluir PRs' })).not.toBeInTheDocument()
    expect(within(panel()).getByRole('button', { name: 'Pedir outra rodada à IA' })).toBeInTheDocument()
  })
})

// What the project chose for this phase in "Provedor e modelo por fase" is where the pull request agent starts from.
describe('the executor and model the project chose for the pull requests', () => {
  const twoProfiles = ['api-work', 'api-big'].map(id => ({ id, name: id === 'api-work' ? 'API local' : 'API grande', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: `${id}-model`, hasCredential: true, endpointBlocked: false, updatedAt: date }))
  const bothBackends = [{ id: 'api-work', name: 'API local', kind: 'api' as const, available: true }, { id: 'api-big', name: 'API grande', kind: 'api' as const, available: true }]
  const chosen = (backendId: string, modelId: string) => ({ workspaceId: 'workspace-1', stage: 'prs' as const, backendId, modelId, updatedAt: date })
  function twoApis() {
    const backend = backendWith()
    backend.listProviderProfiles = async () => twoProfiles
    backend.queryHTTPModelCatalog = vi.fn(async query => ({ backendId: query.profileId, source: 'openai_models', destination: query.profileId, profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
      models: ['api-work-model', 'api-big-model', 'large-model'].map(id => ({ id, displayName: id, backendId: query.profileId, source: 'openai_models', availability: 'available' })), nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete' as const, complete: true, accountFiltered: true }))
    return backend
  }

  it('starts with the executor chosen for the phase instead of the default, and keeps the profile\'s own model when none was chosen', async () => {
    const user = userEvent.setup()
    const { onStart } = show(twoApis(), { backends: bothBackends, configured: chosen('api-big', '') })
    expect(within(screen.getByTestId('picker-pipeline-prs-backend')).getByRole('button')).toHaveTextContent('API grande')
    expect(await within(panel()).findByText('api-big-model')).toBeInTheDocument()
    expect(within(panel()).getByText(/escolha-o em Provedor e modelo por fase/)).toBeInTheDocument()
    expect(screen.queryByTestId('picker-pipeline-role-model-prs')).not.toBeInTheDocument()
    await user.click(within(panel()).getByRole('button', { name: 'Abrir PRs com a IA' }))
    expect(onStart).toHaveBeenCalledWith('api-big')
  })

  it('binds the conversation to the model chosen for the phase, confirmed against the catalog', async () => {
    const user = userEvent.setup()
    const { onStart } = show(twoApis(), { backends: bothBackends, configured: chosen('api-big', 'large-model') })
    const modelPicker = await screen.findByTestId('picker-pipeline-role-model-prs')
    await waitFor(() => expect(within(modelPicker).getByRole('combobox', { name: 'Modelo da fase PRs' })).toHaveValue('large-model'))
    expect(screen.getByText('Escolhido para PRs em Provedor e modelo por fase · API grande')).toBeInTheDocument()
    const start = within(panel()).getByRole('button', { name: 'Abrir PRs com a IA' })
    await waitFor(() => expect(start).toBeEnabled())
    await user.click(start)
    expect(onStart).toHaveBeenCalledWith('api-big', expect.objectContaining({ executor: 'api', profileId: 'api-big', modelId: 'large-model' }))
  })

  it('does not start until the model chosen for the phase is confirmed, and can leave it for the profile\'s own', async () => {
    const user = userEvent.setup()
    const backend = twoApis()
    backend.queryHTTPModelCatalog = vi.fn(async () => { throw new Error('offline') })
    const { onStart } = show(backend, { backends: bothBackends, configured: chosen('api-big', 'large-model') })
    const start = within(panel()).getByRole('button', { name: 'Abrir PRs com a IA' })
    await within(panel()).findByRole('alert')
    expect(start).toBeDisabled()
    await user.click(within(panel()).getByRole('button', { name: 'Usar o modelo do perfil (api-big-model)' }))
    expect(screen.queryByTestId('picker-pipeline-role-model-prs')).not.toBeInTheDocument()
    await waitFor(() => expect(start).toBeEnabled())
    await user.click(start)
    expect(onStart).toHaveBeenCalledWith('api-big')
  })

  it('does not apply the model of the phase to another executor', async () => {
    const user = userEvent.setup()
    show(twoApis(), { backends: bothBackends, configured: chosen('api-big', 'large-model') })
    await screen.findByTestId('picker-pipeline-role-model-prs')
    await user.click(within(screen.getByTestId('picker-pipeline-prs-backend')).getByRole('button'))
    await user.click(await screen.findByRole('option', { name: 'API local' }))
    await waitFor(() => expect(screen.queryByTestId('picker-pipeline-role-model-prs')).not.toBeInTheDocument())
    expect(await within(panel()).findByText('api-work-model')).toBeInTheDocument()
  })

  it('ignores a choice for an executor that cannot run the stage and follows the default, and says so', () => {
    show(twoApis(), { backends: bothBackends, configured: chosen('codex', 'gpt-5'), defaultBackendId: 'api-work' })
    expect(within(screen.getByTestId('picker-pipeline-prs-backend')).getByRole('button')).toHaveTextContent('API local')
    expect(screen.queryByTestId('picker-pipeline-role-model-prs')).not.toBeInTheDocument()
    expect(within(panel()).getByText(/O executor escolhido para PRs \(codex\) não está disponível agora; a fase parte do padrão/)).toHaveAttribute('role', 'status')
  })

  it('names the profile that was chosen and is gone, and stays quiet when the choice works or nothing was chosen', () => {
    const gone = show(twoApis(), { backends: bothBackends, configured: { ...chosen('api-removed', 'large-model') }, defaultBackendId: 'api-work' })
    expect(gone.onStart).not.toHaveBeenCalled()
    expect(within(panel()).getByText(/O executor escolhido para PRs \(api-removed\) não está disponível agora/)).toBeInTheDocument()
    expect(screen.queryByTestId('picker-pipeline-role-model-prs')).not.toBeInTheDocument()
    cleanup()
    show(twoApis(), { backends: bothBackends, configured: chosen('api-big', ''), defaultBackendId: 'api-work' })
    expect(screen.queryByText(/não está disponível agora/)).not.toBeInTheDocument()
    cleanup()
    show(twoApis(), { backends: bothBackends, defaultBackendId: 'api-work' })
    expect(screen.queryByText(/não está disponível agora/)).not.toBeInTheDocument()
  })
})

describe('what the stage leaves behind', () => {
  it('shows the agent report as the stage evidence', () => {
    render(<PipelinePRsOutcome run={pipeline({ currentStage: '', stageStatus: { ...approved, prs: 'completed' }, artifacts: { prs: { stage: 'prs', version: 1, content: '## Pull requests\n- PR de teste', author: 'ai', sourceSessionId: 'session-prs', updatedAt: date } } })} />)
    const region = screen.getByRole('region', { name: 'Pull requests abertos' })
    expect(within(region).getByText('PR de teste')).toBeInTheDocument()
  })

  it('says a skipped stage opened nothing, and stays out of the way otherwise', () => {
    render(<PipelinePRsOutcome run={pipeline({ currentStage: '', stageStatus: { ...approved, prs: 'skipped' } })} />)
    expect(screen.getByRole('region', { name: 'Pull requests' })).toHaveTextContent('Etapa pulada')
    cleanup()
    const { container } = render(<PipelinePRsOutcome run={pipeline()} />)
    expect(container).toBeEmptyDOMElement()
  })
})

// Finishing the stage does not close the work: a review comes back with fixes, and the PRs conversation is still there.
describe('after the stage ended, the conversation of the PRs is still open', () => {
  const report = { prs: { stage: 'prs' as const, version: 1, content: '## Pull requests\n- PR de teste', author: 'ai' as const, sourceSessionId: 'session-prs', updatedAt: date } }
  const ended = () => pipeline({ currentStage: '', stageStatus: { ...approved, prs: 'completed' }, artifacts: report })
  const region = () => screen.findByRole('region', { name: 'Pull requests abertos' })

  it('goes back to the conversation as it was, or with the fixes a review asked for ready to read and send', async () => {
    const user = userEvent.setup()
    const onOpenSession = vi.fn(async () => undefined)
    render(<PipelinePRsOutcome backend={backendWith([github], 'ready')} run={ended()} onOpenSession={onOpenSession} />)
    const outcome = await region()
    await user.click(await within(outcome).findByRole('button', { name: 'Continuar a conversa dos PRs' }))
    expect(onOpenSession).toHaveBeenLastCalledWith('session-prs', undefined)
    await user.click(within(outcome).getByRole('button', { name: 'Corrigir os comentários da revisão' }))
    expect(onOpenSession).toHaveBeenLastCalledWith('session-prs', reviewFixesRequest)
    expect(within(outcome).getByText(/Nada é enviado sozinho/)).toBeInTheDocument()
  })

  it('asks the agent for review fixes on the same branches, without rewriting history', () => {
    expect(reviewFixesRequest).toMatch(/comentários de revisão/)
    expect(reviewFixesRequest).toMatch(/mesma branch de cada PR/)
    expect(reviewFixesRequest).toMatch(/sem push forçado/)
    expect(reviewFixesRequest).toMatch(/pergunte antes de mudar/)
  })

  it('says why the conversation could not be opened', async () => {
    const user = userEvent.setup()
    const onOpenSession = vi.fn(async () => { throw Object.assign(new Error('open session: operation failed'), { cause: { code: 'session_busy' } }) })
    render(<PipelinePRsOutcome backend={backendWith([github], 'ready')} run={ended()} onOpenSession={onOpenSession} />)
    await user.click(await within(await region()).findByRole('button', { name: 'Continuar a conversa dos PRs' }))
    expect(await within(await region()).findByRole('alert')).toHaveTextContent('A sessão já está executando.')
  })

  it('offers nothing when no conversation was kept, or when there is nobody to open it', async () => {
    const quiet = backendWith([github])
    render(<PipelinePRsOutcome backend={quiet} run={ended()} onOpenSession={vi.fn()} />)
    await region()
    expect(screen.queryByRole('button', { name: 'Continuar a conversa dos PRs' })).not.toBeInTheDocument()
    cleanup()
    render(<PipelinePRsOutcome backend={backendWith([github], 'ready')} run={ended()} />)
    await region()
    await waitFor(() => expect(screen.queryByRole('button')).not.toBeInTheDocument())
  })

  it('reads the stage only for a pipeline that ended it', async () => {
    const backend = backendWith([github], 'ready')
    const read = vi.spyOn(backend, 'getPipelineStageActivity')
    render(<PipelinePRsOutcome backend={backend} run={pipeline()} onOpenSession={vi.fn()} />)
    cleanup()
    render(<PipelinePRsOutcome backend={backend} run={pipeline({ currentStage: '', stageStatus: { ...approved, prs: 'skipped' } })} onOpenSession={vi.fn()} />)
    expect(read).not.toHaveBeenCalled()
    cleanup()
    render(<PipelinePRsOutcome backend={backend} run={ended()} onOpenSession={vi.fn()} />)
    await waitFor(() => expect(read).toHaveBeenCalledWith('pipeline-prs', 'prs'))
  })
})
