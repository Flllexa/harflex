import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { Pipeline } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { PipelineQALab } from './PipelineQALab'

afterEach(cleanup)

const date = '2026-10-07T10:00:00Z'

function setup(report: object, evalStatus: 'waiting_user' | 'active' = 'waiting_user', prepare?: (backend: ReturnType<typeof createFakeBackend>['backend']) => void) {
  const { backend } = createFakeBackend()
  const run: Pipeline = { id: 'pipeline-qa', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'TODO', objective: 'TODO', currentStage: 'eval',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: evalStatus }, revision: 9,
    artifacts: { code: { stage: 'code', version: 2, content: '+x', author: 'ai', sourceSessionId: 'coder', contentDigest: 'b'.repeat(64), updatedAt: date },
      eval: { stage: 'eval', version: 3, content: JSON.stringify(report), author: 'ai', sourceSessionId: 'qa', contentDigest: 'e'.repeat(64), updatedAt: date } },
    createdAt: date, updatedAt: date }
  backend.getPipeline = async () => run
  backend.listProviderProfiles = async () => [{ id: 'api', name: 'API', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: 'api-model', hasCredential: true, endpointBlocked: false, updatedAt: date }]
  backend.queryHTTPModelCatalog = vi.fn(async query => ({ backendId: query.profileId, source: 'openai_models', destination: query.profileId, profileRevision: 'rev', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: [{ id: 'api-model', displayName: 'api-model', backendId: query.profileId, source: 'openai_models', availability: 'available' as const }], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete' as const, complete: true, accountFiltered: true }))
  const defaults = { backendId: 'api', defaultModelBackendId: 'api', defaultModelId: 'api-model', phaseConfigured: false }
  const changed = vi.fn()
  prepare?.(backend)
  render(<PipelineQALab backend={backend} backends={[{ id: 'api', name: 'API', kind: 'api', available: true }]} run={run} workspaceId="workspace-1" roleDefaults={() => defaults} onPipelineChange={changed} />)
  return { backend, run, changed }
}

it('sends the chosen failures, improvements and note back to Code with who fixes and who tests', async () => {
  const user = userEvent.setup()
  const { backend, run } = setup({ passed: false, checks: [{ name: 'E2E', kind: 'e2e', command: 'npx playwright test', status: 'failed', summary: 'botão não aparece' }], findings: ['Botão Adicionar some no celular'], improvements: ['Animar a remoção', 'Mostrar contador'], criteria: [] })
  backend.fixPipelineFindings = vi.fn(async input => ({ pipelineId: input.pipelineId, phase: 'fixing' as const, round: 1, message: 'Corrigindo', updatedAt: date, running: true }))
  expect(await screen.findByText('npx playwright test')).toBeInTheDocument()
  await user.click(screen.getByRole('checkbox', { name: 'Mostrar contador' }))
  await user.type(screen.getByLabelText('Algo mais para corrigir (opcional)'), 'use o verde do app')
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-role-model-code')).getByRole('combobox')).toHaveValue('api-model'))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Corrigir os selecionados (3)' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Corrigir os selecionados (3)' }))
  expect(backend.fixPipelineFindings).toHaveBeenCalledWith(expect.objectContaining({ pipelineId: run.id, findings: ['Botão Adicionar some no celular'], improvements: ['Mostrar contador'], note: 'use o verde do app',
    coder: expect.objectContaining({ backendId: 'api', selection: expect.objectContaining({ modelId: 'api-model' }) }), evaluator: expect.objectContaining({ backendId: 'api' }) }))
  expect(await screen.findByText('Corrigindo')).toBeInTheDocument()
})

it('approves a passing QA from the lab, bound to the evaluated version', async () => {
  const user = userEvent.setup()
  const { backend, run, changed } = setup({ passed: true, checks: [{ name: 'Testes', kind: 'unit', command: 'npm test', status: 'passed', summary: '12 passaram' }], findings: [], improvements: ['Adicionar tema escuro'], criteria: [{ criterion: 'adicionar tarefa', evidence: 'addTask' }] })
  backend.decidePipelineExecutionArtifact = vi.fn(async () => ({ ...run, currentStage: 'prs' as const, revision: 10 }))
  expect(await screen.findByText('Passou', { selector: '.qa-lab-status' })).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Aprovar QA e seguir para PRs' }))
  expect(backend.decidePipelineExecutionArtifact).toHaveBeenCalledWith(expect.objectContaining({ stage: 'eval', decision: 'approve', artifactVersion: 3, artifactDigest: 'e'.repeat(64), pipelineRevision: 9 }))
  await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ currentStage: 'prs' })))
})

it('starts ready from the project defaults, keeps the model when the same executor is picked again, and saves a new pick for next time', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const run: Pipeline = { id: 'pipeline-qa', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'TODO', objective: 'TODO', currentStage: 'eval',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'active' }, revision: 9, artifacts: {}, createdAt: date, updatedAt: date }
  backend.getPipeline = async () => run
  backend.listProviderProfiles = async () => [{ id: 'api', name: 'API', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: 'api-model', hasCredential: true, endpointBlocked: false, updatedAt: date }]
  backend.queryHTTPModelCatalog = vi.fn(async query => ({ backendId: query.profileId, source: 'openai_models', destination: query.profileId, profileRevision: 'rev', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: ['api-model', 'big-model'].map(id => ({ id, displayName: id, backendId: query.profileId, source: 'openai_models', availability: 'available' as const })), nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete' as const, complete: true, accountFiltered: true }))
  const chosen = vi.fn()
  const defaults = { backendId: 'api', defaultModelBackendId: 'api', defaultModelId: 'api-model', phaseConfigured: true }
  render(<PipelineQALab backend={backend} backends={[{ id: 'api', name: 'API', kind: 'api', available: true }]} run={run} workspaceId="workspace-1" roleDefaults={() => defaults} onPipelineChange={() => undefined} onRoleChosen={chosen} />)
  // Ready from the defaults, without touching anything.
  await waitFor(() => expect(screen.getByRole('button', { name: 'Rodar QA' })).toBeEnabled())
  expect(chosen).not.toHaveBeenCalled()
  // The same executor picked again keeps its model.
  await user.click(within(screen.getByTestId('picker-qa-role-eval')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'API' }))
  expect(screen.getByRole('button', { name: 'Rodar QA' })).toBeEnabled()
  // Another model becomes the project's choice for QA.
  await user.click(within(screen.getByTestId('picker-pipeline-role-model-eval')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'big-model' }))
  await waitFor(() => expect(chosen).toHaveBeenCalledWith('eval', 'api', 'big-model'))
})

it('offers to run QA again when the report is from before the latest Code, with the old findings only to read', async () => {
  const user = userEvent.setup()
  const { backend, run } = setup({ passed: false, checks: [{ name: 'Build', kind: 'build', command: 'npm run build', status: 'skipped', summary: 'não rodou' }], findings: ['Os testes não rodaram'], improvements: ['Rodar o E2E'], criteria: [] }, 'active')
  backend.startPipelineQA = vi.fn(async pipelineId => ({ pipelineId, phase: 'qa' as const, round: 1, message: 'O QA está rodando', updatedAt: date, running: true }))
  expect(await screen.findByText('O Code mudou depois deste relatório.')).toBeInTheDocument()
  expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
  expect(screen.getByText('Os testes não rodaram')).toBeInTheDocument()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Rodar QA de novo' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Rodar QA de novo' }))
  expect(backend.startPipelineQA).toHaveBeenCalledWith(run.id, expect.objectContaining({ backendId: 'api', selection: expect.objectContaining({ modelId: 'api-model' }) }))
})

it('reads the evaluator report again after a refused reading, without a new QA run', async () => {
  const user = userEvent.setup()
  const { backend, run, changed } = setup({ passed: false, checks: [], findings: ['Os testes não rodaram'], improvements: [], criteria: [] }, 'active', fake => {
    fake.getPipelineQALoop = vi.fn(async pipelineId => ({ pipelineId, phase: 'failed' as const, round: 1, sessionId: 'qa-2', message: 'O QA não entregou um relatório válido: phase evidence required', updatedAt: date, running: false }))
  })
  backend.completePipelineEvaluation = vi.fn(async () => ({ ...run, stageStatus: { ...run.stageStatus, eval: 'waiting_user' as const }, revision: 10 }))
  await user.click(await screen.findByRole('button', { name: 'Ler o relatório de novo' }))
  expect(backend.completePipelineEvaluation).toHaveBeenCalledWith(run.id)
  await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ revision: 10 })))
})

it('resumes fixes that stopped before the Coder round, with who fixes and who tests', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const run: Pipeline = { id: 'pipeline-qa', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'TODO', objective: 'TODO', currentStage: 'code',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'failed' }, revision: 12,
    artifacts: { eval: { stage: 'eval', version: 4, content: JSON.stringify({ passed: false, checks: [], findings: ['O E2E expira'], improvements: [], criteria: [] }), author: 'ai', sourceSessionId: 'qa', contentDigest: 'e'.repeat(64), updatedAt: date } },
    createdAt: date, updatedAt: date }
  backend.listProviderProfiles = async () => [{ id: 'api', name: 'API', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: 'api-model', hasCredential: true, endpointBlocked: false, updatedAt: date }]
  backend.queryHTTPModelCatalog = vi.fn(async query => ({ backendId: query.profileId, source: 'openai_models', destination: query.profileId, profileRevision: 'rev', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: [{ id: 'api-model', displayName: 'api-model', backendId: query.profileId, source: 'openai_models', availability: 'available' as const }], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete' as const, complete: true, accountFiltered: true }))
  backend.resumePipelineFixes = vi.fn(async input => ({ pipelineId: input.pipelineId, phase: 'fixing' as const, round: 1, message: 'Retomando as correções no Code', updatedAt: date, running: true }))
  const defaults = { backendId: 'api', defaultModelBackendId: 'api', defaultModelId: 'api-model', phaseConfigured: false }
  render(<PipelineQALab backend={backend} backends={[{ id: 'api', name: 'API', kind: 'api', available: true }]} run={run} workspaceId="workspace-1" roleDefaults={() => defaults} onPipelineChange={() => undefined} resumable />)
  expect(await screen.findByText('As correções pararam no meio.')).toBeInTheDocument()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Continuar as correções' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Continuar as correções' }))
  expect(backend.resumePipelineFixes).toHaveBeenCalledWith(expect.objectContaining({ pipelineId: run.id, coder: expect.objectContaining({ backendId: 'api' }), evaluator: expect.objectContaining({ backendId: 'api' }) }))
  expect(await screen.findByText('Retomando as correções no Code')).toBeInTheDocument()
})
