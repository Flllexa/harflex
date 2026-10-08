import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { it, expect, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import type { Backend, Brainstorm, ModelCatalogResult, Pipeline } from '../../lib/backend'
import { AuthoringBrainstorm } from './AuthoringBrainstorm'

const date = new Date().toISOString()
const pipeline: Pipeline = { id: 'pipeline-1', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 0, title: 'Novo trabalho', objective: '', currentStage: 'discovery', stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 1, artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Problema concreto', author: 'user', sourceSessionId: '', updatedAt: date } }, createdAt: date, updatedAt: date }
const catalog: ModelCatalogResult = { backendId: 'profile-1', source: 'openai_models', destination: 'API', profileRevision: 'rev-1', credentialToken: 'a'.repeat(64), searchTerm: '', models: [{ id: 'model-1', displayName: 'Modelo 1', backendId: 'profile-1', source: 'openai_models', availability: 'available', loaded: true, contextLength: 128000 }], nextCursor: '', checkedAt: date, status: 'complete', complete: true, accountFiltered: true }
const run: Brainstorm = { id: 'run-1', pipelineId: pipeline.id, pipelineRevision: 2, discoveryVersion: 1, discoveryContent: 'Problema concreto', selection: { backendId: 'profile-1', modelId: 'model-1', reasoningEffort: '', catalogRevision: 'rev-1', source: 'openai_models', destination: 'API', status: 'listed', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 256, contextLength: 128000, checkedAt: date }, state: 'ready', revision: 1, questionCount: 0, currentQuestionId: '', synthesisVersion: 0, attemptCount: 0, inputBudgetRemaining: 30000, outputBudgetRemaining: 30000, activeDurationMillis: 0, createdAt: date, updatedAt: date, attempts: [], turns: [], syntheses: [] }
const profile = { id: 'profile-1', name: 'API de trabalho', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: '', hasCredential: true, endpointBlocked: false, updatedAt: date }
const question = { number: 1, questionId: 'question-1', question: 'Qual é o limite?', answer: '', sourceSessionId: 'session-1', status: 'waiting_answer', createdAt: date, updatedAt: date }
function readyBackend(current: Brainstorm) {
  const { backend } = createFakeBackend()
  backend.getBrainstorming = async () => current
  backend.getPipeline = async () => pipeline
  backend.listProviderProfiles = async () => [profile]
  backend.queryHTTPModelCatalog = async () => catalog
  return backend
}

it('distinguishes no brainstorm from a real lookup failure', async () => {
  const { backend } = createFakeBackend()
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  expect(await screen.findByText('Escrito por você')).toBeInTheDocument()
  expect(await screen.findByText('Este pipeline precisa de um executor disponível')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' })).not.toBeInTheDocument()
  expect(screen.queryByRole('combobox', { name: 'Provedor desta etapa' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Atualizar catálogo' })).not.toBeInTheDocument()
})

it('explica quando Codex é o padrão mas Professional SDD exige um perfil API', async () => {
  const { backend } = createFakeBackend()
  backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'runtime-model' })
  backend.listProviderProfiles = async () => [profile]
  backend.queryHTTPModelCatalog = vi.fn(async query => ({ ...catalog, backendId: query.profileId, models: [{ ...catalog.models[0], backendId: query.profileId }] }))
  backend.queryCLIModelCatalog = vi.fn(async query => ({ backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'codex-revision', searchTerm: '',
    models: [{ id: 'runtime-model', displayName: 'GPT local model', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }], nextCursor: '', checkedAt: date, status: 'complete' as const, complete: true, accountFiltered: true }))
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)

  expect(await screen.findByText(/Codex CLI fica indisponível porque pode ler arquivos fora do projeto/i)).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: 'Provedor desta etapa' })).toHaveValue('')
  expect(backend.queryCLIModelCatalog).not.toHaveBeenCalled()
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  const provider = screen.getByTestId('picker-brainstorm-provider')
  await userEvent.setup().click(provider.querySelector('button')!)
  const codexOption = await screen.findByRole('option', { name: 'Codex CLI · indisponível no SDD' })
  expect(codexOption).toHaveAttribute('aria-disabled', 'true')
})

it('não presume um provedor quando falha a leitura das Configurações e mantém API como escolha manual', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.getSettings = async () => { throw new Error('settings unavailable') }
  backend.listProviderProfiles = async () => [profile]
  backend.queryCLIModelCatalog = vi.fn(async query => ({ backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'codex-revision', searchTerm: '',
    models: [{ id: 'manual-model', displayName: 'Modelo Codex manual', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }], nextCursor: '', checkedAt: date, status: 'complete' as const, complete: true, accountFiltered: true }))
  backend.queryHTTPModelCatalog = vi.fn(async () => catalog)
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)

  expect(await screen.findByText(/Não foi possível ler o provedor padrão salvo/i)).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: 'Provedor desta etapa' })).toHaveValue('')
  expect(backend.queryCLIModelCatalog).not.toHaveBeenCalled()
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  const provider = screen.getByTestId('picker-brainstorm-provider')
  await user.click(provider.querySelector('button')!)
  expect(await screen.findByRole('option', { name: 'Codex CLI · indisponível no SDD' })).toHaveAttribute('aria-disabled', 'true')
  await user.click(await screen.findByRole('option', { name: 'API de trabalho' }))
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledWith({ profileId: profile.id, searchTerm: '', refresh: true }, expect.any(AbortSignal)))
})

it('valida pelo catálogo o provedor API salvo quando a listagem de perfis falha', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.getSettings = async () => ({ defaultBackendId: profile.id, defaultModelBackendId: profile.id, defaultModelId: 'model-1' })
  backend.listProviderProfiles = async () => { throw new Error('profile list unavailable') }
  backend.queryHTTPModelCatalog = vi.fn(async query => ({ ...catalog, backendId: query.profileId, models: [{ ...catalog.models[0], backendId: query.profileId }] }))
  const start = vi.fn(async () => run)
  backend.startBrainstorming = start
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)

  expect(await screen.findByText(/Não foi possível consultar perfis API/i)).toBeInTheDocument()
  expect(await screen.findByRole('combobox', { name: 'Modelo' })).toHaveValue('Modelo 1')
  expect(screen.queryByText(/não é compatível/i)).not.toBeInTheDocument()
  expect(backend.queryHTTPModelCatalog).toHaveBeenCalledWith({ profileId: profile.id, searchTerm: '', refresh: true }, expect.any(AbortSignal))
  expect(screen.getByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' }))
  await waitFor(() => expect(start).toHaveBeenCalledWith(expect.objectContaining({ selection: expect.objectContaining({ backendId: '', profileId: profile.id, modelId: 'model-1', credentialToken: 'a'.repeat(64) }) })))
})

it('explains when no API provider is available and opens provider settings', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => []
  const onSettings = vi.fn()
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} onSettings={onSettings} />)

  expect(await screen.findByText('Este pipeline precisa de um executor disponível')).toBeInTheDocument()
  expect(screen.queryByRole('combobox', { name: 'Provedor desta etapa' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Configurar provedor API' }))
  expect(onSettings).toHaveBeenCalledOnce()
})

it('blocks the SDD entry clearly when Codex is the only executor but cannot constrain reads', async () => {
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => []
  backend.queryCLIModelCatalog = vi.fn(async query => ({ backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'codex-revision', searchTerm: '',
    models: [{ id: 'runtime-model', displayName: 'GPT local model', backendId: 'codex', source: 'codex_app_server', availability: 'listed', contextLength: 128000, supportedReasoningEfforts: ['low', 'medium', 'high'], defaultReasoningEffort: 'medium' }],
    nextCursor: '', checkedAt: date, status: 'complete' as const, complete: true, accountFiltered: true }))
  const start = vi.fn(async (..._args: Parameters<Backend['startBrainstorming']>) => run)
  backend.startBrainstorming = start
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)

  expect(await screen.findByText('Este pipeline precisa de um executor disponível')).toBeInTheDocument()
  expect(screen.getByRole('alert')).toHaveTextContent(/Codex CLI.*não pode ser confinado à pasta/)
  expect(screen.queryByTestId('picker-brainstorm-provider')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' })).not.toBeInTheDocument()
  expect(backend.queryCLIModelCatalog).not.toHaveBeenCalled()
  expect(start).not.toHaveBeenCalled()
})

it('keeps Codex CLI disabled even if its model catalog is available', async () => {
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => []
  backend.queryCLIModelCatalog = vi.fn(async query => ({ backendId: query.backendId, source: '', destination: '', profileRevision: '', searchTerm: '', models: [], nextCursor: '',
    checkedAt: date, status: 'failed' as const, complete: false, accountFiltered: false, errorCode: 'catalog_cli_unavailable' }))
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  expect(await screen.findByText('Este pipeline precisa de um executor disponível')).toBeInTheDocument()
  expect(screen.queryByTestId('picker-brainstorm-provider')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' })).not.toBeInTheDocument()
  expect(backend.queryCLIModelCatalog).not.toHaveBeenCalled()
})

it('starts with an exact catalog choice without generating a question', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => [profile]
  backend.queryHTTPModelCatalog = async () => catalog
  const start = vi.fn(async () => run)
  backend.startBrainstorming = start
  const generate = vi.fn(async () => run)
  backend.generateBrainstormQuestion = generate
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click((await screen.findByTestId('picker-brainstorm-provider')).querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'API de trabalho' }))
  await user.click(screen.getByTestId('picker-brainstorm-model').querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'Modelo 1' }))
  await user.click(screen.getByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' }))
  await waitFor(() => expect(start).toHaveBeenCalledWith(expect.objectContaining({ pipelineId: 'pipeline-1', pipelineRevision: 1, discoveryVersion: 1, selection: expect.objectContaining({ modelId: 'model-1', credentialToken: 'a'.repeat(64) }) })))
  expect(generate).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: 'Gerar pergunta' })).toBeInTheDocument()
  expect(screen.queryByText('a'.repeat(64))).not.toBeInTheDocument()
})

it('keeps a lookup failure distinct from the empty state', async () => {
  const backend = readyBackend(run)
  backend.getBrainstorming = async () => { throw Object.assign(new Error('Storage failed'), { cause: { code: 'internal' } }) }
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  expect(await screen.findByRole('alert')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' })).not.toBeInTheDocument()
})

it('generates only after explicit action and confirms an answer before another question', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const waiting: Brainstorm = { ...run, revision: 2, state: 'waiting_answer', questionCount: 1, currentQuestionId: 'question-1', turns: [question] }
  backend.generateBrainstormQuestion = vi.fn(async () => waiting)
  backend.answerBrainstormQuestion = vi.fn(async (): Promise<Brainstorm> => ({ ...waiting, revision: 3, state: 'ready', turns: [{ ...question, answer: 'Até 5', status: 'answered' }] }))
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  const generate = await screen.findByRole('button', { name: 'Gerar pergunta' })
  await waitFor(() => expect(generate).toBeEnabled())
  await user.click(generate)
  expect(backend.generateBrainstormQuestion).toHaveBeenCalledWith(expect.objectContaining({ ref: expect.objectContaining({ runId: run.id, runRevision: 1, pipelineRevision: 2 }), selection: expect.objectContaining({ modelId: 'model-1' }) }))
  expect(screen.queryByRole('button', { name: 'Perguntas suficientes' })).not.toBeInTheDocument()
  await user.type(await screen.findByLabelText('Sua resposta'), 'Até 5')
  await user.click(screen.getByRole('button', { name: 'Confirmar resposta' }))
  expect(backend.answerBrainstormQuestion).toHaveBeenCalledWith(expect.objectContaining({ questionId: 'question-1', answer: 'Até 5' }))
  expect(await screen.findByRole('button', { name: 'Perguntas suficientes' })).toBeInTheDocument()
})

it('lê o estado terminal pausado após uma geração exceder o orçamento', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const paused: Brainstorm = { ...run, state: 'paused', revision: 3, attemptCount: 1, attempts: [{
    id: 'attempt-1', requestId: 'request-1', kind: 'question', status: 'failed', sessionId: 'session-1', errorCode: 'budget_overrun',
    discoveryVersion: 1, synthesisVersion: 0, selection: run.selection, reservedInputTokens: 26192, reservedOutputTokens: 256,
    usage: { inputTokens: 18903, outputTokens: 68, costUSD: null }, createdAt: date, updatedAt: date,
  }] }
  backend.getBrainstorming = vi.fn(async input => input.runId ? paused : run)
  backend.generateBrainstormQuestion = vi.fn(async () => { throw Object.assign(new Error('Budget exceeded'), { cause: { code: 'brainstorm_budget_exceeded' } }) })
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)

  await user.click(await screen.findByRole('button', { name: 'Gerar pergunta' }))

  expect(await screen.findByText('Pausado')).toBeInTheDocument()
  expect(await screen.findByRole('button', { name: 'Retomar brainstorming' })).toBeEnabled()
  expect(await screen.findByText(/última geração excedeu o orçamento reservado/i)).toBeInTheDocument()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(backend.getBrainstorming).toHaveBeenCalledWith({ runId: run.id, pipelineId: '', discoveryVersion: 0 })
})

it('mantém o motivo do orçamento visível após recarregar uma rodada pausada', async () => {
  const paused: Brainstorm = { ...run, state: 'paused', revision: 3, attemptCount: 1, attempts: [{
    id: 'attempt-1', requestId: 'request-1', kind: 'question', status: 'failed', sessionId: 'session-1', errorCode: 'budget_overrun',
    discoveryVersion: 1, synthesisVersion: 0, selection: run.selection, reservedInputTokens: 26192, reservedOutputTokens: 256,
    usage: { inputTokens: 18903, outputTokens: 68, costUSD: null }, createdAt: date, updatedAt: date,
  }] }
  render(<AuthoringBrainstorm backend={readyBackend(paused)} pipeline={pipeline} onPipelineChange={() => undefined} />)

  expect(await screen.findByText(/última geração excedeu o orçamento reservado/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retomar brainstorming' })).toBeEnabled()
})

it('requires a reason and a second confirmation to skip unanswered questions', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const skipped: Brainstorm = { ...run, state: 'skipped_waiting_confirmation', revision: 2 }
  backend.skipBrainstormQuestions = vi.fn(async () => skipped)
  backend.confirmDiscoveryAfterSkip = vi.fn(async (): Promise<Brainstorm> => ({ ...skipped, state: 'approved', revision: 3 }))
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click(await screen.findByRole('button', { name: 'Pular perguntas' }))
  expect(screen.getByRole('button', { name: 'Registrar pulo' })).toBeDisabled()
  await user.type(screen.getByLabelText('Motivo do pulo'), 'Discovery já é suficiente')
  await user.click(screen.getByRole('button', { name: 'Registrar pulo' }))
  expect(backend.skipBrainstormQuestions).toHaveBeenCalledWith(expect.objectContaining({ reason: 'Discovery já é suficiente' }))
  expect(backend.confirmDiscoveryAfterSkip).not.toHaveBeenCalled()
  await user.click(await screen.findByRole('button', { name: 'Confirmar Discovery após pulo' }))
  expect(backend.confirmDiscoveryAfterSkip).toHaveBeenCalledOnce()
  expect(await screen.findByText(/SPEC está pendente de geração/)).toBeInTheDocument()
})

it('shows exact synthesis content and requires feedback for revision', async () => {
  const user = userEvent.setup()
  const backend = readyBackend({ ...run, state: 'waiting_user', synthesisVersion: 1, syntheses: [{ version: 1, discoveryVersion: 1, content: { scope: 'Escopo aprovado?', decisions: ['Decisão A'], openQuestions: ['Questão B'] }, sourceSessionId: 'session-1', status: 'completed', createdAt: date }] })
  backend.requestBrainstormRevision = vi.fn(async (): Promise<Brainstorm> => ({ ...run, state: 'ready_for_synthesis', revision: 2 }))
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  expect(await screen.findByText('Escopo aprovado?')).toBeInTheDocument()
  expect(screen.getByText('Decisão A')).toBeInTheDocument()
  expect(screen.getByText('Questão B')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Solicitar revisão' })).toBeDisabled()
  await user.type(screen.getByLabelText('Feedback para revisão'), 'Ajustar limite')
  await user.click(screen.getByRole('button', { name: 'Solicitar revisão' }))
  expect(backend.requestBrainstormRevision).toHaveBeenCalledWith(expect.objectContaining({ synthesisVersion: 1, choice: 'more_questions', feedback: 'Ajustar limite' }))
})

it('offers cancellation for a running attempt and explicit resume when paused', async () => {
  const user = userEvent.setup()
  const running: Brainstorm = { ...run, state: 'running_question', attempts: [{ id: 'attempt-1', requestId: 'req-1', kind: 'question', status: 'running', sessionId: 'session-1', errorCode: '', discoveryVersion: 1, synthesisVersion: 0, selection: run.selection, reservedInputTokens: 1, reservedOutputTokens: 256, usage: null, createdAt: date, updatedAt: date }] }
  const backend = readyBackend(running)
  backend.cancelBrainstormAttempt = vi.fn(async (): Promise<Brainstorm> => ({ ...running, state: 'paused', revision: 2 }))
  backend.resumePausedBrainstorm = vi.fn(async (): Promise<Brainstorm> => ({ ...run, revision: 3 }))
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click(await screen.findByRole('button', { name: 'Cancelar geração' }))
  expect(backend.cancelBrainstormAttempt).toHaveBeenCalledWith(expect.objectContaining({ attemptId: 'attempt-1' }))
  await user.click(await screen.findByRole('button', { name: 'Retomar brainstorming' }))
  expect(backend.resumePausedBrainstorm).toHaveBeenCalledWith(expect.objectContaining({ runId: 'run-1', runRevision: 2 }))
})

it('revises editable Discovery with exact revision and preserves prior run history', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const revised: Pipeline = { ...pipeline, revision: 2, artifacts: { discovery: { ...pipeline.artifacts.discovery!, version: 2, content: 'Discovery revisada' } } }
  backend.reviseAuthoringDiscovery = vi.fn(async () => revised)
  const onPipelineChange = vi.fn()
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={onPipelineChange} />)
  await user.click(await screen.findByRole('button', { name: 'Editar Discovery' }))
  await user.clear(screen.getByLabelText('Texto da Discovery'))
  await user.type(screen.getByLabelText('Texto da Discovery'), 'Discovery revisada')
  await user.click(screen.getByRole('button', { name: 'Salvar nova versão' }))
  expect(backend.reviseAuthoringDiscovery).toHaveBeenCalledWith({ pipelineId: pipeline.id, expectedRevision: 2, expectedVersion: 1, discovery: 'Discovery revisada' })
  expect(onPipelineChange).toHaveBeenCalledWith(revised)
})

it('derives a new pipeline from a frozen Discovery with a stable request after uncertainty', async () => {
  const user = userEvent.setup()
  const frozen: Pipeline = { ...pipeline, discoveryFrozenVersion: 1, currentStage: 'spec', revision: 3 }
  const child: Pipeline = { ...pipeline, id: 'child-1', derivedFromPipelineId: pipeline.id, revision: 1 }
  const backend = readyBackend({ ...run, state: 'approved' })
  const derive = vi.fn().mockRejectedValueOnce(new Error('uncertain')).mockResolvedValueOnce(child)
  backend.deriveAuthoringPipeline = derive
  render(<AuthoringBrainstorm backend={backend} pipeline={frozen} onPipelineChange={() => undefined} derivationSafety="clear" />)
  await user.click(await screen.findByRole('button', { name: 'Criar nova versão derivada' }))
  await user.clear(screen.getByLabelText('Discovery da nova versão'))
  await user.type(screen.getByLabelText('Discovery da nova versão'), 'Novo escopo')
  await user.click(screen.getByRole('button', { name: 'Criar pipeline derivado' }))
  await screen.findByRole('alert')
  await user.click(screen.getByRole('button', { name: 'Criar pipeline derivado' }))
  expect(derive).toHaveBeenCalledTimes(2)
  expect(derive.mock.calls[0][0]).toEqual(derive.mock.calls[1][0])
  expect(derive.mock.calls[0][0]).toMatchObject({ parentPipelineId: pipeline.id, expectedRevision: 3, discovery: 'Novo escopo' })
})

it('offers local LM Studio without a key and requires JIT confirmation for an unloaded model', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => [{ ...profile, providerType: 'lm_studio', hasCredential: false }]
  backend.queryHTTPModelCatalog = async () => ({ ...catalog, source: 'lm_studio_models', models: [{ ...catalog.models[0], source: 'lm_studio_models', loaded: false }] })
  backend.startBrainstorming = vi.fn(async () => run)
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click((await screen.findByTestId('picker-brainstorm-provider')).querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'API de trabalho' }))
  await user.click(screen.getByTestId('picker-brainstorm-model').querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'Modelo 1' }))
  const start = screen.getByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' })
  expect(start).toBeDisabled()
  await user.click(screen.getByLabelText(/Confirmo carregar este modelo/))
  await waitFor(() => expect(start).toBeEnabled())
})

it('cancels a generation while its original provider call is still pending', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const running: Brainstorm = { ...run, state: 'running_question', revision: 2, attempts: [{ id: 'attempt-1', requestId: 'req-1', kind: 'question', status: 'running', sessionId: 'session-1', errorCode: '', discoveryVersion: 1, synthesisVersion: 0, selection: run.selection, reservedInputTokens: 1, reservedOutputTokens: 256, usage: null, createdAt: date, updatedAt: date }] }
  let lookupCount = 0
  backend.getBrainstorming = async () => ++lookupCount === 1 ? run : running
  let finish!: (value: Brainstorm) => void
  backend.generateBrainstormQuestion = vi.fn(() => new Promise<Brainstorm>(resolve => { finish = resolve }))
  backend.cancelBrainstormAttempt = vi.fn(async (): Promise<Brainstorm> => ({ ...running, state: 'paused', revision: 3 }))
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  const generate = await screen.findByRole('button', { name: 'Gerar pergunta' })
  await waitFor(() => expect(generate).toBeEnabled())
  await user.click(generate)
  const cancel = await screen.findByRole('button', { name: 'Cancelar geração' }, { timeout: 3000 })
  expect(cancel).toBeEnabled()
  await user.click(cancel)
  expect(backend.cancelBrainstormAttempt).toHaveBeenCalledWith(expect.objectContaining({ attemptId: 'attempt-1' }))
  finish({ ...running, state: 'waiting_answer', revision: 4 })
  expect(await screen.findByText('Pausado')).toBeInTheDocument()
  expect(screen.queryByText('Aguardando resposta')).not.toBeInTheDocument()
})

it('loads prior Discovery versions from bounded backend history after remount', async () => {
  const user = userEvent.setup()
  const revised: Pipeline = { ...pipeline, revision: 3, artifacts: { discovery: { ...pipeline.artifacts.discovery!, version: 2, content: 'Novo contexto' } } }
  const backend = readyBackend(run)
  backend.getBrainstorming = async () => { throw Object.assign(new Error('No current run'), { cause: { code: 'brainstorm_not_found' } }) }
  backend.listBrainstorming = vi.fn(async (): Promise<Brainstorm[]> => [{ ...run, state: 'invalidated' }])
  render(<AuthoringBrainstorm backend={backend} pipeline={revised} onPipelineChange={() => undefined} />)
  const history = await screen.findByText('Rodadas anteriores (1)')
  await user.click(history)
  await user.click(screen.getByRole('button', { name: /Discovery v1/ }))
  expect(screen.getByText('Problema concreto')).toBeInTheDocument()
  expect(backend.listBrainstorming).toHaveBeenCalledWith({ pipelineId: pipeline.id, workspaceId: pipeline.workspaceId, limit: 20 })
})

it('requires a new OpenRouter confirmation when the provider changes', async () => {
  const user = userEvent.setup()
  const previous: Brainstorm = { ...run, selection: { ...run.selection, source: 'openrouter_general_unfiltered', confirmUnfiltered: true } }
  const backend = readyBackend(previous)
  backend.listProviderProfiles = async () => [{ ...profile, providerType: 'openrouter' }, { ...profile, id: 'profile-2', name: 'Outra conta', providerType: 'openrouter' }]
  backend.queryHTTPModelCatalog = async query => ({ ...catalog, backendId: query.profileId, source: 'openrouter_general_unfiltered', accountFiltered: false, models: [{ ...catalog.models[0], backendId: query.profileId, source: 'openrouter_general_unfiltered' }] })
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click((await screen.findByTestId('picker-brainstorm-provider')).querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'Outra conta' }))
  await user.click(screen.getByTestId('picker-brainstorm-model').querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'Modelo 1' }))
  const generate = screen.getByRole('button', { name: 'Gerar pergunta' })
  expect(generate).toBeDisabled()
  await user.click(screen.getByLabelText(/Confirmo usar a lista geral/))
  await waitFor(() => expect(generate).toBeEnabled())
})

it('does not inherit consent when catalog revision and destination change for the same model', async () => {
  const user = userEvent.setup()
  const previous: Brainstorm = { ...run, selection: { ...run.selection, source: 'openrouter_general_unfiltered', confirmUnfiltered: true } }
  const backend = readyBackend(previous)
  backend.listProviderProfiles = async () => [{ ...profile, providerType: 'openrouter' }]
  backend.queryHTTPModelCatalog = async () => ({ ...catalog, profileRevision: 'new-revision', destination: 'Outra API', source: 'openrouter_general_unfiltered', accountFiltered: false, models: [{ ...catalog.models[0], source: 'openrouter_general_unfiltered' }] })
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  const generate = await screen.findByRole('button', { name: 'Gerar pergunta' })
  expect(generate).toBeDisabled()
  const consent = await screen.findByLabelText(/Confirmo usar a lista geral/)
  expect(consent).not.toBeChecked()
  await user.click(consent)
  await waitFor(() => expect(generate).toBeEnabled())
})

it('reads terminal state when cancellation loses the race to completion', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const running: Brainstorm = { ...run, state: 'running_question', revision: 2, attempts: [{ id: 'attempt-1', requestId: 'req-1', kind: 'question', status: 'running', sessionId: 'session-1', errorCode: '', discoveryVersion: 1, synthesisVersion: 0, selection: run.selection, reservedInputTokens: 1, reservedOutputTokens: 256, usage: null, createdAt: date, updatedAt: date }] }
  const completed: Brainstorm = { ...running, state: 'waiting_answer', revision: 3, currentQuestionId: 'question-1', questionCount: 1, turns: [question], attempts: [{ ...running.attempts[0], status: 'completed' }] }
  let terminal = false
  let lookupCount = 0
  backend.getBrainstorming = async () => ++lookupCount === 1 ? run : terminal ? completed : running
  let finish!: (value: Brainstorm) => void
  backend.generateBrainstormQuestion = vi.fn(() => new Promise<Brainstorm>(resolve => { finish = resolve }))
  backend.cancelBrainstormAttempt = vi.fn(async () => { terminal = true; throw Object.assign(new Error('Conflict'), { cause: { code: 'pipeline_conflict' } }) })
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  const generate = await screen.findByRole('button', { name: 'Gerar pergunta' })
  await waitFor(() => expect(generate).toBeEnabled())
  await user.click(generate)
  await user.click(await screen.findByRole('button', { name: 'Cancelar geração' }, { timeout: 3000 }))
  expect(await screen.findByLabelText('Sua resposta')).toBeEnabled()
  finish(completed)
  expect(screen.getByText('Aguardando resposta')).toBeInTheDocument()
})

it('requires JIT consent again when the same local model becomes unloaded under a new revision', async () => {
  const previous: Brainstorm = { ...run, selection: { ...run.selection, source: 'lm_studio_models', confirmJitLoad: true } }
  const backend = readyBackend(previous)
  backend.listProviderProfiles = async () => [{ ...profile, providerType: 'lm_studio', hasCredential: false }]
  backend.queryHTTPModelCatalog = async () => ({ ...catalog, profileRevision: 'new-revision', source: 'lm_studio_models', models: [{ ...catalog.models[0], source: 'lm_studio_models', loaded: false }] })
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  const consent = await screen.findByLabelText(/Confirmo carregar este modelo/)
  expect(consent).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Gerar pergunta' })).toBeDisabled()
})

it('does not submit pipeline A after its catalog refresh completes on pipeline B', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const pipelineB: Pipeline = { ...pipeline, id: 'pipeline-b', workspaceId: 'workspace-b', title: 'Outro trabalho' }
  backend.getBrainstorming = async () => { throw Object.assign(new Error('No run'), { cause: { code: 'brainstorm_not_found' } }) }
  backend.listProviderProfiles = async () => [profile]
  let release!: (value: ModelCatalogResult) => void
  let queries = 0
  backend.queryHTTPModelCatalog = vi.fn(async () => ++queries === 1 ? catalog : new Promise<ModelCatalogResult>(resolve => { release = resolve }))
  backend.startBrainstorming = vi.fn(async () => run)
  const view = render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click((await screen.findByTestId('picker-brainstorm-provider')).querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'API de trabalho' }))
  await user.click(screen.getByTestId('picker-brainstorm-model').querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'Modelo 1' }))
  await user.click(screen.getByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' }))
  await waitFor(() => expect(queries).toBe(2))
  view.rerender(<AuthoringBrainstorm backend={backend} pipeline={pipelineB} onPipelineChange={() => undefined} />)
  release(catalog)
  await screen.findByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' })
  expect(backend.startBrainstorming).not.toHaveBeenCalled()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

it('ignores pipeline A manual refresh failure after switching to pipeline B', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const pipelineB: Pipeline = { ...pipeline, id: 'pipeline-b', workspaceId: 'workspace-b', title: 'Outro trabalho' }
  let fail!: (reason: Error) => void
  let reads = 0
  backend.getBrainstorming = async () => {
    reads++
    if (reads === 1) return run
    if (reads === 2) return new Promise<Brainstorm>((_resolve, reject) => { fail = reject })
    throw Object.assign(new Error('No run B'), { cause: { code: 'brainstorm_not_found' } })
  }
  const view = render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click(await screen.findByRole('button', { name: 'Atualizar brainstorming' }))
  view.rerender(<AuthoringBrainstorm backend={backend} pipeline={pipelineB} onPipelineChange={() => undefined} />)
  fail(new Error('old read failed'))
  await screen.findByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' })
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.queryByText('Pronto para a próxima pergunta')).not.toBeInTheDocument()
})

it('clears answer and skip confirmation when moving from pipeline A to B', async () => {
  const user = userEvent.setup()
  const pipelineB: Pipeline = { ...pipeline, id: 'pipeline-b', workspaceId: 'workspace-b', title: 'Outro trabalho' }
  const waitingA: Brainstorm = { ...run, state: 'waiting_answer', questionCount: 1, currentQuestionId: 'question-1', turns: [question] }
  const waitingB: Brainstorm = { ...waitingA, id: 'run-b', pipelineId: pipelineB.id, turns: [{ ...question, questionId: 'question-b', question: 'Outra pergunta?' }], currentQuestionId: 'question-b' }
  const backend = readyBackend(waitingA)
  backend.getBrainstorming = async input => input.pipelineId === pipelineB.id ? waitingB : waitingA
  const view = render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.type(await screen.findByLabelText('Sua resposta'), 'Resposta de A')
  await user.click(screen.getByRole('button', { name: 'Pular perguntas' }))
  expect(screen.getByLabelText('Motivo do pulo')).toBeInTheDocument()
  view.rerender(<AuthoringBrainstorm backend={backend} pipeline={pipelineB} onPipelineChange={() => undefined} />)
  expect(await screen.findByText('Outra pergunta?')).toBeInTheDocument()
  expect(screen.getByLabelText('Sua resposta')).toHaveValue('')
  expect(screen.queryByLabelText('Motivo do pulo')).not.toBeInTheDocument()
})

it('ignores an old manual catalog error after the workspace changes', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const pipelineB: Pipeline = { ...pipeline, id: 'pipeline-b', workspaceId: 'workspace-b', title: 'Outro trabalho' }
  let rejectOld!: (reason: Error) => void
  let reads = 0
  backend.queryHTTPModelCatalog = async () => ++reads === 2 ? new Promise<ModelCatalogResult>((_resolve, reject) => { rejectOld = reject }) : catalog
  const view = render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await screen.findByText(/Catálogo completo/)
  await user.click(screen.getByRole('button', { name: 'Atualizar catálogo' }))
  await waitFor(() => expect(reads).toBe(2))
  view.rerender(<AuthoringBrainstorm backend={backend} pipeline={pipelineB} onPipelineChange={() => undefined} />)
  rejectOld(new Error('old catalog failure'))
  expect(await screen.findByText(/Catálogo completo/)).toBeInTheDocument()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

it('invalidates OpenRouter consent if the fresh admission catalog changes', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => [{ ...profile, providerType: 'openrouter' }]
  backend.getBrainstorming = async () => { throw Object.assign(new Error('No run'), { cause: { code: 'brainstorm_not_found' } }) }
  const listed = { ...catalog, source: 'openrouter_general_unfiltered', accountFiltered: false, models: [{ ...catalog.models[0], source: 'openrouter_general_unfiltered' }] }
  let reads = 0
  backend.queryHTTPModelCatalog = async () => ++reads === 1 ? listed : { ...listed, profileRevision: 'rev-2', destination: 'Outra conta' }
  backend.startBrainstorming = vi.fn(async () => run)
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click((await screen.findByTestId('picker-brainstorm-provider')).querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'API de trabalho' }))
  await user.click(screen.getByTestId('picker-brainstorm-model').querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'Modelo 1' }))
  await user.click(screen.getByLabelText(/Confirmo usar a lista geral/))
  await user.click(screen.getByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' }))
  expect(backend.startBrainstorming).not.toHaveBeenCalled()
  expect(await screen.findByRole('alert')).toHaveTextContent('O catálogo ou o contexto do modelo mudou')
  expect(screen.getByLabelText(/Confirmo usar a lista geral/)).not.toBeChecked()
})

it('ignores a delayed running poll after the final answer-required receipt', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const running: Brainstorm = { ...run, state: 'running_question', revision: 2 }
  const waiting: Brainstorm = { ...run, state: 'waiting_answer', revision: 3, questionCount: 1, currentQuestionId: 'question-1', turns: [question] }
  let finish!: (value: Brainstorm) => void
  let releasePoll!: (value: Brainstorm) => void
  let reads = 0
  backend.getBrainstorming = async () => ++reads === 1 ? run : new Promise<Brainstorm>(resolve => { releasePoll = resolve })
  backend.generateBrainstormQuestion = vi.fn(() => new Promise<Brainstorm>(resolve => { finish = resolve }))
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  const generate = await screen.findByRole('button', { name: 'Gerar pergunta' })
  await waitFor(() => expect(generate).toBeEnabled())
  await user.click(generate)
  await waitFor(() => expect(reads).toBeGreaterThanOrEqual(2), { timeout: 3000 })
  finish(waiting)
  expect(await screen.findByLabelText('Sua resposta')).toBeInTheDocument()
  await act(async () => { releasePoll(running) })
  expect(screen.getByText('Aguardando resposta')).toBeInTheDocument()
  expect(screen.queryByText('Gerando pergunta')).not.toBeInTheDocument()
})

it('loads deferred provider profiles and history after an answer command', async () => {
  const user = userEvent.setup()
  const waiting: Brainstorm = { ...run, state: 'waiting_answer', revision: 2, questionCount: 1, currentQuestionId: 'question-1', turns: [question] }
  const backend = readyBackend(waiting)
  let releaseProfiles!: (value: typeof profile[]) => void
  let releaseHistory!: (value: Brainstorm[]) => void
  backend.listProviderProfiles = () => new Promise(resolve => { releaseProfiles = resolve })
  backend.listBrainstorming = () => new Promise(resolve => { releaseHistory = resolve })
  backend.answerBrainstormQuestion = async (): Promise<Brainstorm> => ({ ...waiting, state: 'ready', revision: 3, turns: [{ ...question, answer: 'Equipe', status: 'answered' }] })
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.type(await screen.findByLabelText('Sua resposta'), 'Equipe')
  await user.click(screen.getByRole('button', { name: 'Confirmar resposta' }))
  await screen.findByText('Resposta confirmada: Equipe')
  releaseProfiles([profile])
  releaseHistory([{ ...run, id: 'old-run', state: 'invalidated', discoveryVersion: 1 }])
  expect(await screen.findByText('Rodadas anteriores (1)')).toBeInTheDocument()
  await user.click(screen.getByTestId('picker-brainstorm-provider').querySelector('button')!)
  expect(await screen.findByRole('option', { name: 'API de trabalho' })).toBeInTheDocument()
})

it('renders decisions and open questions from an older synthesis', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const old: Brainstorm = { ...run, id: 'old-run', state: 'invalidated', discoveryVersion: 1, synthesisVersion: 1, syntheses: [{ version: 1, discoveryVersion: 1, content: { scope: 'Escopo anterior', decisions: ['Manter CSV'], openQuestions: ['Como paginar?'] }, sourceSessionId: 'session-1', status: 'completed', createdAt: date }] }
  backend.listBrainstorming = async () => [old]
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click(await screen.findByText('Rodadas anteriores (1)'))
  await user.click(screen.getByRole('button', { name: /Discovery v1/ }))
  expect(screen.getByText('Escopo anterior')).toBeInTheDocument()
  expect(screen.getByText('Manter CSV')).toBeInTheDocument()
  expect(screen.getByText('Como paginar?')).toBeInTheDocument()
})

it('keeps a new generation poll alive when the cancelled generation settles late', async () => {
  const user = userEvent.setup()
  const backend = readyBackend(run)
  const attempt = { id: 'old-attempt', requestId: 'old-request', kind: 'question', status: 'running', sessionId: 'session-1', errorCode: '', discoveryVersion: 1, synthesisVersion: 0, selection: run.selection, reservedInputTokens: 1, reservedOutputTokens: 256, usage: null, createdAt: date, updatedAt: date }
  const oldRunning: Brainstorm = { ...run, state: 'running_question', revision: 2, attempts: [attempt] }
  const paused: Brainstorm = { ...oldRunning, state: 'paused', revision: 3, attempts: [{ ...attempt, status: 'interrupted' }] }
  const resumed: Brainstorm = { ...run, state: 'ready', revision: 4 }
  const newRunning: Brainstorm = { ...run, state: 'running_question', revision: 5, attempts: [{ ...attempt, id: 'new-attempt' }] }
  let phase: 'initial' | 'old' | 'new' = 'initial'
  backend.getBrainstorming = async () => phase === 'initial' ? run : phase === 'old' ? oldRunning : newRunning
  let finishOld!: (value: Brainstorm) => void
  let finishNew!: (value: Brainstorm) => void
  let calls = 0
  backend.generateBrainstormQuestion = () => ++calls === 1 ? new Promise(resolve => { finishOld = resolve }) : new Promise(resolve => { finishNew = resolve })
  backend.cancelBrainstormAttempt = async () => paused
  backend.resumePausedBrainstorm = async () => resumed
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  const generate = await screen.findByRole('button', { name: 'Gerar pergunta' })
  await waitFor(() => expect(generate).toBeEnabled())
  await user.click(generate)
  phase = 'old'
  await user.click(await screen.findByRole('button', { name: 'Cancelar geração' }, { timeout: 3000 }))
  await user.click(await screen.findByRole('button', { name: 'Retomar brainstorming' }))
  phase = 'new'
  await user.click(await screen.findByRole('button', { name: 'Gerar pergunta' }))
  await act(async () => { finishOld(oldRunning) })
  expect(await screen.findByRole('button', { name: 'Cancelar geração' }, { timeout: 3000 })).toBeInTheDocument()
  await act(async () => { finishNew(newRunning) })
})

it('finishes a manual catalog refresh after confirming an answer', async () => {
  const user = userEvent.setup()
  const waiting: Brainstorm = { ...run, state: 'waiting_answer', revision: 2, questionCount: 1, currentQuestionId: 'question-1', turns: [question] }
  const backend = readyBackend(waiting)
  let releaseCatalog!: (value: ModelCatalogResult) => void
  let catalogReads = 0
  backend.queryHTTPModelCatalog = async () => ++catalogReads === 1 ? catalog : new Promise(resolve => { releaseCatalog = resolve })
  backend.answerBrainstormQuestion = async (): Promise<Brainstorm> => ({ ...waiting, state: 'ready', revision: 3, turns: [{ ...question, answer: 'Equipe', status: 'answered' }] })
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await screen.findByText(/Catálogo completo/)
  await user.click(screen.getByRole('button', { name: 'Atualizar catálogo' }))
  expect(screen.getByText('Atualizando catálogo…')).toBeInTheDocument()
  await user.type(screen.getByLabelText('Sua resposta'), 'Equipe')
  await user.click(screen.getByRole('button', { name: 'Confirmar resposta' }))
  await screen.findByText('Resposta confirmada: Equipe')
  await act(async () => { releaseCatalog(catalog) })
  expect(screen.getByText(/Catálogo completo/)).toBeInTheDocument()
  expect(screen.queryByText('Atualizando catálogo…')).not.toBeInTheDocument()
})

it('blocks a stale catalog before admitting another attempt', async () => {
  const backend = readyBackend(run)
  backend.queryHTTPModelCatalog = async () => ({ ...catalog, checkedAt: new Date(Date.now() - 360_000).toISOString() })
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  expect(await screen.findByText(/Catálogo desatualizado/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Gerar pergunta' })).toBeDisabled()
})

it('names what is missing while the start action is unavailable', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => [profile]
  backend.queryHTTPModelCatalog = async () => catalog
  render(<AuthoringBrainstorm backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  expect(await screen.findByText('Escolha um provedor e um modelo para iniciar o brainstorming.')).toBeInTheDocument()
  await user.click((await screen.findByTestId('picker-brainstorm-provider')).querySelector('button')!)
  await user.click(await screen.findByRole('option', { name: 'API de trabalho' }))
  expect(await screen.findByText('Escolha um modelo para iniciar o brainstorming.')).toBeInTheDocument()
  expect(screen.queryByText(/Nenhum provedor elegível/)).not.toBeInTheDocument()
})
