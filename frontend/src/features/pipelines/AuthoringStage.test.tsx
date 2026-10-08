import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { NO_CATALOG_TIME, type AuthoringStageModelPreference, type ModelCatalogResult, type ProviderProfile, type SaveAuthoringStageModelPreferenceInput } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { stagePipeline, stageReadback } from '../../test/authoringStage'
import { AuthoringStage } from './AuthoringStage'

const legacyTestProfile: ProviderProfile = { id: 'api-1', name: 'API de trabalho', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://api.example.test/v1', model: 'modelo-original', hasCredential: true, endpointBlocked: false, updatedAt: new Date().toISOString() }
async function consultStageModels() {
  const button = await screen.findByRole('button', { name: 'Consultar modelos' })
  await waitFor(() => expect(button).toBeEnabled())
  await userEvent.click(button)
}

it('preserves a supported non-Automatic inherited effort after an explicit catalog refresh', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const inherited = { ...stageReadback, state: 'ready' as const, attempts: [{ ...stageReadback.attempts[0], selection: { ...stageReadback.attempts[0].selection, reasoningEffort: 'high' } }] }
  backend.getAuthoringStage = async () => inherited
  const checkedAt = new Date().toISOString()
  const inheritedPreference: AuthoringStageModelPreference = { pipelineId: stagePipeline.id, stage: 'spec', modelMode: 'inherit', effortMode: 'inherit', explicitEffort: '', preferenceRevision: 2, resolution: 'ready', modelSource: 'brainstorm', effortSource: 'brainstorm', inheritedFrom: 'brainstorm', catalogValidationRequired: false, errorCode: '', selection: { ...inherited.attempts[0].selection, backendId: 'api-1', catalogRevision: 'rev-new', source: 'openai_models', status: 'listed', checkedAt } }
  backend.getAuthoringStageModelPreference = vi.fn(async () => inheritedPreference)
  backend.listProviderProfiles = async () => [{ id: 'api-1', name: 'API de trabalho', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://api.example.test/v1', model: 'modelo-original', hasCredential: true, endpointBlocked: false, updatedAt: checkedAt }]
  backend.queryHTTPModelCatalog = vi.fn(async () => ({ backendId: 'api-1', source: 'openai_models', destination: inheritedPreference.selection.destination, profileRevision: 'rev-new', credentialToken: 'a'.repeat(64), searchTerm: '', models: [{ id: 'modelo-original', displayName: 'Modelo', backendId: 'api-1', source: 'openai_models', availability: 'available', supportedReasoningEfforts: ['high'] }], nextCursor: '', checkedAt, status: 'complete' as const, complete: true, accountFiltered: true }))
  backend.generateAuthoringStage = vi.fn(async () => stageReadback)
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  expect(await screen.findByText('modelo-original · high')).toBeInTheDocument()
  expect(screen.getByText('high · Herdado de Brainstorm')).toBeInTheDocument()
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  await user.click(await screen.findByRole('button', { name: 'Consultar modelos' }))
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  const generate = await screen.findByRole('button', { name: 'Gerar SPEC' })
  expect(generate).toBeEnabled()
  await user.click(generate)
  await waitFor(() => expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1))
  expect(vi.mocked(backend.generateAuthoringStage).mock.calls[0][0].selection).toEqual(expect.objectContaining({ maxOutputTokens: 4096, reasoningEffort: '', credentialToken: '' }))
})

it('blocks an inherited Brainstorm effort when the refreshed model no longer supports it', async () => {
  const { backend } = createFakeBackend()
  const checkedAt = new Date().toISOString()
  const inherited: AuthoringStageModelPreference = {
    ...unconfiguredSnapshotPreference('spec'), modelSource: 'brainstorm', effortSource: 'brainstorm', inheritedFrom: 'brainstorm', catalogValidationRequired: false,
    selection: { ...preferenceSelection, backendId: phaseProfile.id, modelId: 'model-high', reasoningEffort: 'high', catalogRevision: 'profile-rev-2', source: 'openai_models', destination: 'https://api.example.test:443', status: 'listed', contextLength: 128000, checkedAt },
  }
  const catalog: ModelCatalogResult = { ...phaseCatalog(), models: [{ ...phaseCatalog().models[0], supportedReasoningEfforts: ['low'] }] }
  backend.getAuthoringStageModelPreference = async () => inherited
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [phaseProfile]
  backend.queryHTTPModelCatalog = vi.fn(async () => catalog)
  backend.generateAuthoringStage = vi.fn()
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  const effort = within(screen.getByTestId('picker-authoring-stage-effort-spec'))
  const generate = screen.getByRole('button', { name: 'Gerar SPEC' })
  expect(effort.getByRole('button')).toHaveTextContent('Herdar')
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  await consultStageModels()
  expect(await screen.findByRole('alert')).toHaveTextContent(/O modelo escolhido não oferece o esforço herdado “high”/)
  expect(effort.getByRole('button')).toHaveTextContent('Herdar')
  expect(generate).toBeDisabled()
  expect(backend.generateAuthoringStage).not.toHaveBeenCalled()
})

it('blocks an inherited model removed from a complete catalog without selecting a fallback', async () => {
  const { backend } = createFakeBackend()
  const checkedAt = new Date().toISOString()
  const inherited: AuthoringStageModelPreference = {
    ...unconfiguredSnapshotPreference('spec'), modelSource: 'brainstorm', effortSource: 'brainstorm', inheritedFrom: 'brainstorm', catalogValidationRequired: false,
    selection: { ...preferenceSelection, backendId: phaseProfile.id, modelId: 'model-high', reasoningEffort: 'high', catalogRevision: 'profile-rev-2', source: 'openai_models', destination: 'https://api.example.test:443', status: 'listed', contextLength: 128000, checkedAt },
  }
  backend.getAuthoringStageModelPreference = async () => inherited
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [phaseProfile]
  backend.queryHTTPModelCatalog = vi.fn(async () => ({ ...phaseCatalog(), models: [] }))
  backend.generateAuthoringStage = vi.fn()
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  const generate = screen.getByRole('button', { name: 'Gerar SPEC' })
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  await consultStageModels()
  expect(await screen.findByRole('alert')).toHaveTextContent(/O modelo herdado não está neste catálogo completo/)
  expect(screen.getByTestId('picker-authoring-stage-effort-spec')).toHaveTextContent('Herdar')
  expect(generate).toBeDisabled()
  expect(backend.generateAuthoringStage).not.toHaveBeenCalled()
})

it.each(['approve', 'skip'] as const)('refreshes the parent pipeline after recovering a lost %s receipt', async action => {
  const { backend } = createFakeBackend()
  let current = stageReadback
  backend.getAuthoringStage = async () => current
  const lost = vi.fn(async (input: { ref: { requestId: string } }) => {
    current = { ...stageReadback, revision: 3, pipelineRevision: 6, state: action === 'approve' ? 'approved' : 'skipped', actions: [{ requestId: input.ref.requestId, action, actor: 'local_user', artifactVersion: 1, feedback: '', reason: '', attemptId: '', resultStageRevision: 3, resultPipelineRevision: 6, createdAt: stageReadback.createdAt }] }
    throw new Error('Response lost')
  })
  backend.approveAuthoringStage = lost; backend.skipAuthoringStage = lost
  const next = { ...stagePipeline, currentStage: 'plan' as const, revision: 6 }
  backend.getPipeline = vi.fn(async () => next)
  const onPipelineChange = vi.fn()
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={onPipelineChange} />)
  if (action === 'skip') { await userEvent.click(await screen.findByText('Pular esta fase')); await userEvent.type(screen.getByLabelText('Justificativa do pulo'), 'Pular explicitamente.') }
  await userEvent.click(await screen.findByRole('button', { name: action === 'approve' ? 'Aprovar SPEC' : 'Pular SPEC' }))
  await waitFor(() => expect(onPipelineChange).toHaveBeenCalledWith(next))
  expect(backend.getPipeline).toHaveBeenCalledWith(stagePipeline.id)
})

it.each(['approve', 'skip'] as const)('fences late parent readback for recovered %s when scope switches A to B', async action => {
  const { backend } = createFakeBackend()
  let current = stageReadback, resolveParent!: (value: typeof stagePipeline) => void
  backend.getAuthoringStage = async input => ({ ...current, pipelineId: input.pipelineId })
  const lost = vi.fn(async (input: { ref: { requestId: string } }) => {
    current = { ...stageReadback, revision: 3, pipelineRevision: 6, state: action === 'approve' ? 'approved' : 'skipped', actions: [{ requestId: input.ref.requestId, action, actor: 'local_user', artifactVersion: 1, feedback: '', reason: '', attemptId: '', resultStageRevision: 3, resultPipelineRevision: 6, createdAt: stageReadback.createdAt }] }
    throw new Error('Response lost')
  })
  backend.approveAuthoringStage = lost; backend.skipAuthoringStage = lost
  backend.getPipeline = vi.fn(() => new Promise<typeof stagePipeline>(resolve => { resolveParent = resolve }))
  const onPipelineChange = vi.fn()
  const view = render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={onPipelineChange} />)
  if (action === 'skip') { await userEvent.click(await screen.findByText('Pular esta fase')); await userEvent.type(screen.getByLabelText('Justificativa do pulo'), 'Pular explicitamente.') }
  await userEvent.click(await screen.findByRole('button', { name: action === 'approve' ? 'Aprovar SPEC' : 'Pular SPEC' }))
  await waitFor(() => expect(backend.getPipeline).toHaveBeenCalled())
  view.rerender(<AuthoringStage backend={backend} pipeline={{ ...stagePipeline, id: 'pipeline-B', workspaceId: 'workspace-B' }} stage="spec" onPipelineChange={onPipelineChange} />)
  await act(async () => resolveParent({ ...stagePipeline, currentStage: 'plan', revision: 6 }))
  expect(onPipelineChange).not.toHaveBeenCalled()
})

it.each(['approve', 'skip', 'cancel'] as const)('reconciles a lost %s decision receipt only from a current readback', async action => {
  const { backend } = createFakeBackend()
  backend.getPipeline = async () => stagePipeline
  const initial = action === 'cancel' ? { ...stageReadback, state: 'running' as const, artifacts: [], artifactVersion: 0, attempts: [{ ...stageReadback.attempts[0], status: 'running' as const }] } : stageReadback
  let result = initial, failed = false, readable = false, requestId = ''
  backend.getAuthoringStage = async () => { if (failed && !readable) throw new Error('Readback unavailable'); return result }
  const failDecision = vi.fn(async (input: { ref: { requestId: string } }) => { requestId = input.ref.requestId; failed = true; throw new Error('Response lost') })
  backend.approveAuthoringStage = failDecision; backend.skipAuthoringStage = failDecision; backend.cancelAuthoringStage = failDecision
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await screen.findByRole('button', { name: 'Atualizar estado' })
  if (action === 'skip') { await userEvent.click(await screen.findByText('Pular esta fase')); await userEvent.type(screen.getByLabelText('Justificativa do pulo'), 'Decisão explícita.') }
  await userEvent.click(await screen.findByRole('button', { name: action === 'approve' ? 'Aprovar SPEC' : action === 'skip' ? 'Pular SPEC' : 'Cancelar tentativa' }))
  expect(await screen.findByText(/Resultado do comando ainda não confirmado/)).toBeInTheDocument()
  readable = true
  result = { ...initial, revision: initial.revision - 1 }
  await userEvent.click(screen.getByRole('button', { name: 'Atualizar estado' }))
  expect(screen.getByText(/Resultado do comando ainda não confirmado/)).toBeInTheDocument()
  result = { ...initial, revision: initial.revision + 1, state: action === 'cancel' ? 'cancellation_pending' : action === 'approve' ? 'approved' : 'skipped', cancellationPending: action === 'cancel', cancellationAttemptId: action === 'cancel' ? 'attempt-1' : '', actions: [{ requestId, action, actor: 'local_user', artifactVersion: initial.artifactVersion, feedback: '', reason: '', attemptId: action === 'cancel' ? 'attempt-1' : '', resultStageRevision: initial.revision + 1, resultPipelineRevision: initial.pipelineRevision, createdAt: initial.createdAt }] }
  await userEvent.click(screen.getByRole('button', { name: 'Atualizar estado' }))
  await waitFor(() => expect(screen.queryByText(/Resultado do comando ainda não confirmado/)).not.toBeInTheDocument())
  if (action === 'cancel') expect(screen.getByText(/cancelamento não foi confirmado localmente nem pelo provedor/)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /Aprovar SPEC|Gerar SPEC|Cancelar tentativa|Pular SPEC/ })).not.toBeInTheDocument()
  expect(failDecision).toHaveBeenCalledTimes(1)
})

it.each(['approve', 'skip', 'cancel'] as const)('restores %s after a current post-error snapshot proves no receipt', async action => {
  const { backend } = createFakeBackend()
  const initial = action === 'cancel' ? { ...stageReadback, state: 'running' as const, artifacts: [], artifactVersion: 0, attempts: [{ ...stageReadback.attempts[0], status: 'running' as const }] } : stageReadback
  let failed = false, readable = false
  backend.getAuthoringStage = async () => { if (failed && !readable) throw new Error('Readback unavailable'); return initial }
  const failDecision = vi.fn(async () => { failed = true; throw new Error('Preflight rejected') })
  backend.approveAuthoringStage = failDecision; backend.skipAuthoringStage = failDecision; backend.cancelAuthoringStage = failDecision
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  if (action === 'skip') { await userEvent.click(await screen.findByText('Pular esta fase')); await userEvent.type(screen.getByLabelText('Justificativa do pulo'), 'Decisão explícita.') }
  const label = action === 'approve' ? 'Aprovar SPEC' : action === 'skip' ? 'Pular SPEC' : 'Cancelar tentativa'
  await userEvent.click(await screen.findByRole('button', { name: label }))
  expect(await screen.findByText(/Resultado do comando ainda não confirmado/)).toBeInTheDocument()
  readable = true
  await userEvent.click(screen.getByRole('button', { name: 'Atualizar estado' }))
  if (action === 'skip') await userEvent.click(await screen.findByText('Pular esta fase'))
  expect(await screen.findByRole('button', { name: label })).toBeEnabled()
  expect(failDecision).toHaveBeenCalledTimes(1)
})

it('does not treat a pre-error snapshot arriving late as proof of no admission', async () => {
  const { backend } = createFakeBackend()
  const ready = { ...stageReadback, state: 'ready' as const, artifactVersion: 0, artifacts: [] }
  let resolveOld!: (value: typeof ready) => void, rejectCommand!: (reason: Error) => void
  backend.getAuthoringStage = vi.fn().mockResolvedValueOnce(ready).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve })).mockRejectedValue(new Error('Readback unavailable'))
  backend.listProviderProfiles = async () => [legacyTestProfile]
  backend.queryHTTPModelCatalog = async () => ({ backendId: 'api-1', source: 'openai_models', destination: stageReadback.attempts[0].selection.destination, profileRevision: 'rev-new', credentialToken: 'a'.repeat(64), searchTerm: '', models: [{ id: 'modelo-original', displayName: 'Modelo', backendId: 'api-1', source: 'openai_models', availability: 'available' }], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete', complete: true, accountFiltered: true })
  backend.generateAuthoringStage = vi.fn(() => new Promise<typeof stageReadback>((_, reject) => { rejectCommand = reject }))
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await consultStageModels()
  await userEvent.click(screen.getByRole('button', { name: 'Gerar SPEC' }))
  await userEvent.click(screen.getByRole('button', { name: 'Atualizar estado' }))
  await act(async () => rejectCommand(new Error('Response lost')))
  expect(await screen.findByText(/Resultado do comando ainda não confirmado/)).toBeInTheDocument()
  await act(async () => resolveOld(ready))
  expect(screen.getByText(/Resultado do comando ainda não confirmado/)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Gerar SPEC' })).not.toBeInTheDocument()
  expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1)
})

it.each([false, true])('recovers a rejected preflight only after successful post-error readback (outage=%s)', async outage => {
  const { backend } = createFakeBackend()
  const ready = { ...stageReadback, state: 'ready' as const, artifactVersion: 0, artifacts: [] }
  let rejected = false, lookupFails = outage
  backend.getAuthoringStage = async () => { if (rejected && lookupFails) throw new Error('Readback unavailable'); return ready }
  backend.listProviderProfiles = async () => [legacyTestProfile]
  backend.queryHTTPModelCatalog = async () => ({ backendId: 'api-1', source: 'openai_models', destination: stageReadback.attempts[0].selection.destination, profileRevision: 'rev-new', credentialToken: 'a'.repeat(64), searchTerm: '', models: [{ id: 'modelo-original', displayName: 'Modelo', backendId: 'api-1', source: 'openai_models', availability: 'available' }], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete', complete: true, accountFiltered: true })
  backend.generateAuthoringStage = vi.fn(async () => { rejected = true; throw new Error('Preflight rejected') })
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await consultStageModels()
  await userEvent.click(screen.getByRole('button', { name: 'Gerar SPEC' }))
  if (outage) {
    expect(await screen.findByText(/Resultado do comando ainda não confirmado/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Gerar SPEC' })).not.toBeInTheDocument()
    lookupFails = false
    await userEvent.click(screen.getByRole('button', { name: 'Atualizar estado' }))
  }
  expect(await screen.findByRole('button', { name: 'Gerar SPEC' })).toBeEnabled()
  expect(screen.queryByText(/Resultado do comando ainda não confirmado/)).not.toBeInTheDocument()
  expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1)
})

it('recovers an admitted running request after command error and keeps exact cancellation available', async () => {
  const { backend } = createFakeBackend()
  let current = { ...stageReadback, state: 'ready' as typeof stageReadback.state, artifactVersion: 0, artifacts: [] }
  backend.getAuthoringStage = async () => current
  backend.listProviderProfiles = async () => [legacyTestProfile]
  backend.queryHTTPModelCatalog = async () => ({ backendId: 'api-1', source: 'openai_models', destination: stageReadback.attempts[0].selection.destination, profileRevision: 'rev-new', credentialToken: 'a'.repeat(64), searchTerm: '', models: [{ id: 'modelo-original', displayName: 'Modelo', backendId: 'api-1', source: 'openai_models', availability: 'available' }], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete', complete: true, accountFiltered: true })
  backend.generateAuthoringStage = vi.fn(async input => {
    current = { ...current, state: 'running', revision: 3, attempts: [{ ...stageReadback.attempts[0], id: 'admitted-attempt', requestId: input.ref.requestId, status: 'running' }] }
    throw new Error('Command response lost')
  })
  backend.cancelAuthoringStage = vi.fn(async () => { current = { ...current, state: 'cancellation_pending', cancellationPending: true, cancellationAttemptId: 'admitted-attempt' }; return current })
  backend.getPipeline = async () => stagePipeline
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await consultStageModels()
  await userEvent.click(screen.getByRole('button', { name: 'Gerar SPEC' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Cancelar tentativa' }))
  expect(backend.cancelAuthoringStage).toHaveBeenCalledWith(expect.objectContaining({ attemptId: 'admitted-attempt', ref: expect.objectContaining({ stageRevision: 3 }) }))
  expect(await screen.findByText(/cancelamento não foi confirmado localmente nem pelo provedor/)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Gerar SPEC' })).not.toBeInTheDocument()
  expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1)
})

it('shows failed attempts and human decisions even when no artifact was published', async () => {
  const { backend } = createFakeBackend()
  backend.getAuthoringStage = async () => ({ ...stageReadback, state: 'paused', artifactVersion: 0, artifacts: [], attempts: [{ ...stageReadback.attempts[0], status: 'failed', errorCode: 'provider_failed' }], actions: [{ requestId: 'decision-1', action: 'revision', actor: 'local_user', artifactVersion: 0, feedback: 'Explicitar os limites.', reason: '', attemptId: 'attempt-1', resultStageRevision: 2, resultPipelineRevision: 5, createdAt: stageReadback.createdAt }] })
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await userEvent.click(await screen.findByText('Tentativas e decisões'))
  expect(screen.getByText(/Falha do provedor/)).toBeInTheDocument()
  expect(screen.getByText('Explicitar os limites.')).toBeInTheDocument()
})

it('reads back a durable cancellation fence after Abort uncertainty and exposes no retry or skip', async () => {
  const { backend } = createFakeBackend()
  let current = { ...stageReadback, state: 'running' as typeof stageReadback.state, artifacts: [], artifactVersion: 0, attempts: [{ ...stageReadback.attempts[0], status: 'running' as const }] }
  backend.getAuthoringStage = async () => current
  backend.cancelAuthoringStage = vi.fn(async () => {
    current = { ...current, state: 'cancellation_pending', cancellationPending: true, cancellationAttemptId: 'attempt-1' }
    throw Object.assign(new Error('Abort timeout'), { cause: { code: 'authoring_cancellation_pending' } })
  })
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Cancelar tentativa' }))
  expect(await screen.findByText(/cancelamento não foi confirmado localmente nem pelo provedor/i)).toBeInTheDocument()
  expect(backend.cancelAuthoringStage).toHaveBeenCalledWith(expect.objectContaining({ attemptId: 'attempt-1', ref: expect.objectContaining({ stageRevision: 2, pipelineRevision: 5 }) }))
  expect(screen.queryByRole('button', { name: /Gerar|Pular|Aprovar|Cancelar tentativa/ })).not.toBeInTheDocument()
})

it('permits an explicit justified skip after budget exhaustion without a new generation', async () => {
  const { backend } = createFakeBackend()
  backend.getAuthoringStage = async () => ({ ...stageReadback, state: 'paused', attemptCount: 6, artifacts: [], artifactVersion: 0 })
  backend.skipAuthoringStage = vi.fn(async () => ({ ...stageReadback, state: 'skipped' as const }))
  backend.getPipeline = async () => ({ ...stagePipeline, currentStage: 'plan' })
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await userEvent.click(await screen.findByText('Pular esta fase'))
  await userEvent.type(screen.getByLabelText('Justificativa do pulo'), 'Orçamento esgotado; decisão humana registrada.')
  await userEvent.click(screen.getByRole('button', { name: 'Pular SPEC' }))
  expect(backend.skipAuthoringStage).toHaveBeenCalledTimes(1)
})

it('uses the global default on an explicit Generate click without a client catalog call', async () => {
  const { backend } = createFakeBackend()
  const ready = { ...stageReadback, state: 'ready' as const, artifactVersion: 0, artifacts: [] }
  backend.getAuthoringStage = async () => ready
  backend.getPipeline = async () => stagePipeline
  backend.queryHTTPModelCatalog = vi.fn()
  backend.generateAuthoringStage = vi.fn(async () => stageReadback)
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  const generate = await screen.findByRole('button', { name: 'Gerar SPEC' })
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  await userEvent.click(generate)
  await waitFor(() => expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1))
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  expect(backend.generateAuthoringStage).toHaveBeenCalledWith(expect.objectContaining({ ref: expect.objectContaining({ pipelineId: 'pipeline-1', pipelineRevision: 5, stageRevision: 2, discoveryVersion: 1 }), selection: { executor: 'api', backendId: '', profileId: '', modelId: '', catalogRevision: '', source: '', destination: '', checkedAt: NO_CATALOG_TIME, credentialToken: '', reasoningEffort: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096 } }))
})

it('requires feedback before a revision and never approves a historical version', async () => {
  const { backend } = createFakeBackend()
  backend.getAuthoringStage = async () => ({ ...stageReadback, artifactVersion: 2, artifacts: [stageReadback.artifacts[0], { ...stageReadback.artifacts[0], version: 2 }] })
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  expect(await screen.findByRole('button', { name: 'Pedir revisão e gerar' })).toBeDisabled()
  await userEvent.click(screen.getByText('Versões do documento (2)'))
  await userEvent.click(screen.getByRole('button', { name: 'Versão 1 · rascunho' }))
  expect(screen.queryByRole('button', { name: 'Aprovar SPEC' })).not.toBeInTheDocument()
})

it('does not publish old readback after the project/pipeline identity changes', async () => {
  let resolveOld!: (value: typeof stageReadback) => void
  const { backend } = createFakeBackend()
  backend.getAuthoringStage = vi.fn().mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve })).mockResolvedValue({ ...stageReadback, pipelineId: 'pipeline-2', artifacts: [], artifactVersion: 0 })
  const view = render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  view.rerender(<AuthoringStage backend={backend} pipeline={{ ...stagePipeline, id: 'pipeline-2', workspaceId: 'workspace-2' }} stage="spec" onPipelineChange={() => undefined} />)
  await screen.findByText('Nenhum documento publicado. A geração exige uma ação sua.')
  await act(async () => resolveOld(stageReadback))
  expect(screen.queryByText('Decisão persistida')).not.toBeInTheDocument()
})

it('renders readable SPEC sections with artifact-specific provenance and no inference on mount', async () => {
  const { backend } = createFakeBackend()
  backend.getAuthoringStage = vi.fn(async () => ({ ...stageReadback, attempts: [...stageReadback.attempts, { ...stageReadback.attempts[0], id: 'attempt-new', selection: { ...stageReadback.attempts[0].selection, modelId: 'modelo-posterior' } }] }))
  backend.generateAuthoringStage = vi.fn()
  backend.queryHTTPModelCatalog = vi.fn()
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  expect(await screen.findByRole('heading', { name: 'Critérios de aceite' })).toBeInTheDocument()
  expect(screen.getByText('Decisão persistida')).toBeInTheDocument()
  expect(screen.getByText('Origem desta versão · modelo-original')).toBeInTheDocument()
  expect(screen.queryByText('"acceptanceCriteria"')).not.toBeInTheDocument()
  expect(backend.generateAuthoringStage).not.toHaveBeenCalled()
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
})

it('keeps uncertain cancellation read-only, with no retry, skip or approval', async () => {
  const { backend } = createFakeBackend()
  backend.getAuthoringStage = async () => ({ ...stageReadback, state: 'cancellation_pending', cancellationPending: true, cancellationAttemptId: 'attempt-1' })
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  expect(await screen.findByText(/cancelamento não foi confirmado localmente nem pelo provedor/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /Gerar|Pedir revisão|Pular|Aprovar/ })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Atualizar estado' }))
  expect(screen.getByText(/cancelamento não foi confirmado/i)).toBeInTheDocument()
})

it.each([
  {
    stage: 'spec' as const,
    preference: {
      pipelineId: stagePipeline.id, stage: 'spec', modelMode: 'inherit', effortMode: 'inherit', explicitEffort: '',
      preferenceRevision: 0, resolution: 'ready', modelSource: 'global_default', effortSource: 'global_default', inheritedFrom: 'global_default',
      catalogValidationRequired: true, errorCode: '',
      selection: { backendId: 'default-api', modelId: 'modelo-global', reasoningEffort: '', catalogRevision: 'profile-rev-1', source: 'global_default', destination: 'https://api.example.test:443', status: 'unverified_default', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096, contextLength: 0, checkedAt: new Date().toISOString() },
    },
    visible: ['Configurações', 'Modelo herdado', 'modelo-global'],
  },
  {
    stage: 'plan' as const,
    preference: {
      pipelineId: stagePipeline.id, stage: 'plan', modelMode: 'override', effortMode: 'explicit', explicitEffort: 'high',
      preferenceRevision: 7, resolution: 'ready', modelSource: 'phase_override', effortSource: 'phase_override', inheritedFrom: '',
      catalogValidationRequired: false, errorCode: '',
      selection: { backendId: 'plan-api', modelId: 'modelo-plan', reasoningEffort: 'high', catalogRevision: 'profile-rev-7', source: 'openai_models', destination: 'https://plan.example.test:443', status: 'listed', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096, contextLength: 128000, checkedAt: new Date().toISOString() },
    },
    visible: ['Modelo salvo nesta fase', 'high'],
  },
])('reads persisted $stage model and effort preference without refreshing provider catalogs on mount', async ({ stage, preference, visible }) => {
  const { backend } = createFakeBackend()
  const getPreference = vi.fn(async () => preference)
  const backendWithPreference = Object.assign(backend, { getAuthoringStageModelPreference: getPreference })
  backend.getAuthoringStage = async input => ({ ...stageReadback, pipelineId: input.pipelineId, stage: input.stage, state: 'ready' })
  backend.queryHTTPModelCatalog = vi.fn()
  const pipeline = { ...stagePipeline, currentStage: stage }

  render(<AuthoringStage backend={backendWithPreference} pipeline={pipeline} stage={stage} onPipelineChange={() => undefined} />)

  expect(await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })).toBeInTheDocument()
  for (const value of visible) expect(screen.getAllByText(new RegExp(value, 'i')).length).toBeGreaterThan(0)
  if (stage === 'spec') {
    const effort = within(screen.getByTestId('picker-authoring-stage-effort-spec'))
    await userEvent.click(effort.getByRole('button'))
    expect(screen.getByRole('option', { name: 'Herdar' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Automático' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'high' })).not.toBeInTheDocument()
  }
  expect(getPreference).toHaveBeenCalledWith({ pipelineId: pipeline.id, stage })
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
})

const preferenceSelection = { backendId: 'default-api', modelId: 'modelo-global', reasoningEffort: '', catalogRevision: 'profile-rev-1', source: 'global_default', destination: 'https://api.example.test:443', status: 'unverified_default', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096, contextLength: 0, checkedAt: new Date().toISOString() }
const unconfiguredSnapshotPreference = (stage: 'spec' | 'plan'): AuthoringStageModelPreference => ({
  pipelineId: stagePipeline.id, stage, modelMode: 'inherit', effortMode: 'inherit', explicitEffort: '', preferenceRevision: 0,
  resolution: 'ready', modelSource: 'global_default', effortSource: 'global_default', inheritedFrom: 'global_default', catalogValidationRequired: true, errorCode: '',
  selection: preferenceSelection,
})
const savedOverridePreference = (stage: 'spec' | 'plan'): AuthoringStageModelPreference => ({
  ...unconfiguredSnapshotPreference(stage), modelMode: 'override', effortMode: 'explicit', explicitEffort: 'high', preferenceRevision: 4,
  resolution: 'ready', modelSource: 'phase_override', effortSource: 'phase_override', inheritedFrom: '', catalogValidationRequired: false,
  selection: { ...preferenceSelection, backendId: phaseProfile.id, modelId: 'model-high', reasoningEffort: 'high', catalogRevision: 'profile-rev-1', source: 'openai_models', destination: 'https://api.example.test:443', status: 'listed', contextLength: 128000 },
})
const readyStage = (stage: 'spec' | 'plan') => ({ ...stageReadback, stage, state: 'ready' as const, artifactVersion: 0, artifacts: [], attempts: [] })
const phaseProfile: ProviderProfile = { id: 'phase-api', name: 'API principal', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://api.example.test/v1', model: 'default-model', hasCredential: true, endpointBlocked: false, updatedAt: new Date().toISOString() }
const phaseCatalog = (): ModelCatalogResult => ({ backendId: phaseProfile.id, source: 'openai_models', destination: 'https://api.example.test:443', profileRevision: 'profile-rev-2', credentialToken: 'a'.repeat(64), searchTerm: '', models: [
  { id: 'model-high', displayName: 'Modelo High', backendId: phaseProfile.id, source: 'openai_models', availability: 'available', supportedReasoningEfforts: ['low', 'high'] },
], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete', complete: true, accountFiltered: true })

it('requires an explicit complete catalog, saves an allowlisted phase override by CAS, then generates with only the output cap', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const initial = unconfiguredSnapshotPreference('spec')
  const saved: AuthoringStageModelPreference = { ...initial, modelMode: 'override', effortMode: 'explicit', explicitEffort: 'high', preferenceRevision: 1, modelSource: 'phase_override', effortSource: 'phase_override', inheritedFrom: '', catalogValidationRequired: false, selection: { ...preferenceSelection, backendId: phaseProfile.id, modelId: 'model-high', reasoningEffort: 'high', catalogRevision: 'profile-rev-2', source: 'openai_models', destination: 'https://api.example.test:443', status: 'listed', contextLength: 128000, checkedAt: new Date().toISOString() } }
  const getPreference = vi.fn(async () => initial)
  const savePreference = vi.fn(async (_input: SaveAuthoringStageModelPreferenceInput) => saved)
  backend.getAuthoringStageModelPreference = getPreference
  backend.saveAuthoringStageModelPreference = savePreference
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = vi.fn(async () => [phaseProfile])
  backend.queryHTTPModelCatalog = vi.fn(async () => phaseCatalog())
  backend.generateAuthoringStage = vi.fn(async () => readyStage('spec'))
  backend.getPipeline = async () => stagePipeline

  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  await user.click(within(screen.getByTestId('picker-authoring-stage-model-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'Escolher para esta fase' }))
  await user.click(within(await screen.findByTestId('picker-authoring-stage-provider-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'API principal' }))
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Consultar modelos' }))
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  await user.click(within(await screen.findByTestId('picker-authoring-stage-override-model-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'Modelo High' }))
  await user.click(within(screen.getByTestId('picker-authoring-stage-effort-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'high' }))
  await user.click(screen.getByRole('button', { name: 'Salvar preferência' }))
  await waitFor(() => expect(savePreference).toHaveBeenCalledTimes(1))
  expect(savePreference).toHaveBeenCalledWith(expect.objectContaining({ pipelineId: stagePipeline.id, stage: 'spec', expectedRevision: 0, modelMode: 'override', effortMode: 'explicit', explicitEffort: 'high', selection: expect.objectContaining({ profileId: phaseProfile.id, modelId: 'model-high', reasoningEffort: '', maxOutputTokens: 4096, credentialToken: 'a'.repeat(64) }) }))
  await user.click(await screen.findByRole('button', { name: 'Gerar SPEC' }))
  await waitFor(() => expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1))
  const generation = vi.mocked(backend.generateAuthoringStage).mock.calls[0][0]
  expect(generation.selection.maxOutputTokens).toBe(4096)
  expect(JSON.stringify(generation.selection)).not.toContain('a'.repeat(64))
  expect(savePreference.mock.invocationCallOrder[0]).toBeLessThan(vi.mocked(backend.generateAuthoringStage).mock.invocationCallOrder[0])
})

it('saves an explicit inherited effort without sending a model override selection', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const checkedAt = new Date().toISOString()
  const inherited: AuthoringStageModelPreference = {
    ...unconfiguredSnapshotPreference('spec'), modelSource: 'brainstorm', effortSource: 'brainstorm', inheritedFrom: 'brainstorm', catalogValidationRequired: false,
    selection: { ...preferenceSelection, backendId: phaseProfile.id, modelId: 'model-high', reasoningEffort: '', catalogRevision: 'profile-rev-2', source: 'openai_models', destination: 'https://api.example.test:443', status: 'listed', contextLength: 128000, checkedAt },
  }
  const saved: AuthoringStageModelPreference = { ...inherited, effortMode: 'explicit', explicitEffort: 'high', effortSource: 'phase_override', preferenceRevision: 1, selection: { ...inherited.selection, reasoningEffort: 'high' } }
  const savePreference = vi.fn(async (_input: SaveAuthoringStageModelPreferenceInput) => saved)
  backend.getAuthoringStageModelPreference = async () => inherited
  backend.saveAuthoringStageModelPreference = savePreference
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [phaseProfile]
  backend.queryHTTPModelCatalog = vi.fn(async () => phaseCatalog())
  backend.generateAuthoringStage = vi.fn(async () => readyStage('spec'))
  backend.getPipeline = async () => stagePipeline
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  await consultStageModels()
  await user.click(within(screen.getByTestId('picker-authoring-stage-effort-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'high' }))
  await user.click(screen.getByRole('button', { name: 'Salvar preferência' }))
  await waitFor(() => expect(savePreference).toHaveBeenCalledTimes(1))
  expect(savePreference).toHaveBeenCalledWith(expect.objectContaining({ modelMode: 'inherit', effortMode: 'explicit', explicitEffort: 'high', selection: { executor: 'api', backendId: '', profileId: '', modelId: '', catalogRevision: '', source: '', destination: '', checkedAt: NO_CATALOG_TIME, credentialToken: '', reasoningEffort: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096 } }))
  await user.click(await screen.findByRole('button', { name: 'Gerar SPEC' }))
  await waitFor(() => expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1))
  expect(vi.mocked(backend.generateAuthoringStage).mock.calls[0][0].selection.maxOutputTokens).toBe(4096)
})

it('disables every phase control until preference and local profiles finish loading', async () => {
  const { backend } = createFakeBackend()
  let resolvePreference!: (value: AuthoringStageModelPreference) => void
  let resolveProfiles!: (value: ProviderProfile[]) => void
  backend.getAuthoringStageModelPreference = vi.fn(() => new Promise<AuthoringStageModelPreference>(resolve => { resolvePreference = resolve }))
  backend.listProviderProfiles = vi.fn(() => new Promise<ProviderProfile[]>(resolve => { resolveProfiles = resolve }))
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.queryHTTPModelCatalog = vi.fn()
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  const model = within(await screen.findByTestId('picker-authoring-stage-model-spec')).getByRole('button')
  const effort = within(screen.getByTestId('picker-authoring-stage-effort-spec')).getByRole('button')
  const consult = screen.getByRole('button', { name: 'Consultar modelos' })
  const generate = screen.getByRole('button', { name: 'Gerar SPEC' })
  expect(model).toBeDisabled()
  expect(effort).toBeDisabled()
  expect(consult).toBeDisabled()
  expect(generate).toBeDisabled()

  await act(async () => resolvePreference(unconfiguredSnapshotPreference('spec')))
  expect(model).toBeDisabled()
  expect(effort).toBeDisabled()
  expect(consult).toBeDisabled()
  expect(generate).toBeDisabled()
  await act(async () => resolveProfiles([phaseProfile]))
  await waitFor(() => expect(model).toBeEnabled())
  expect(effort).toBeEnabled()
  expect(consult).toBeEnabled()
  expect(generate).toBeEnabled()
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
})

it('keeps inherited effort distinct from Automatic and lets the server validate the global default only on Generate', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const initial = unconfiguredSnapshotPreference('plan')
  const automatic: AuthoringStageModelPreference = { ...initial, effortMode: 'automatic', effortSource: 'automatic', preferenceRevision: 1, catalogValidationRequired: true }
  const savePreference = vi.fn(async () => automatic)
  backend.getAuthoringStageModelPreference = vi.fn(async () => initial)
  backend.saveAuthoringStageModelPreference = savePreference
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.queryHTTPModelCatalog = vi.fn()
  backend.generateAuthoringStage = vi.fn(async () => readyStage('plan'))
  backend.getPipeline = async () => ({ ...stagePipeline, currentStage: 'plan' })
  const pipeline = { ...stagePipeline, currentStage: 'plan' as const }

  render(<AuthoringStage backend={backend} pipeline={pipeline} stage="plan" onPipelineChange={() => undefined} />)
  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  const effortPicker = within(screen.getByTestId('picker-authoring-stage-effort-plan'))
  expect(effortPicker.getByRole('button')).toHaveTextContent('Herdar')
  await user.click(effortPicker.getByRole('button'))
  expect(screen.getByRole('option', { name: 'Herdar' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Automático' })).toBeInTheDocument()
  expect(screen.queryByRole('option', { name: 'high' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('option', { name: 'Automático' }))
  await user.click(screen.getByRole('button', { name: 'Salvar preferência' }))
  await waitFor(() => expect(savePreference).toHaveBeenCalledWith(expect.objectContaining({ stage: 'plan', expectedRevision: 0, modelMode: 'inherit', effortMode: 'automatic', explicitEffort: '' })))
  await user.click(await screen.findByRole('button', { name: 'Gerar Plan' }))
  await waitFor(() => expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1))
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  expect(vi.mocked(backend.generateAuthoringStage).mock.calls[0][0].selection.maxOutputTokens).toBe(4096)
})

it('surfaces a CAS conflict, reads the current revision, and waits for an explicit refresh before enabling Generate', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const initial = unconfiguredSnapshotPreference('spec')
  const latest: AuthoringStageModelPreference = { ...initial, effortMode: 'automatic', effortSource: 'automatic', preferenceRevision: 1 }
  let reads = 0
  const getPreference = vi.fn(async () => ++reads === 1 ? initial : latest)
  const savePreference = vi.fn(async () => { throw Object.assign(new Error('Conflict'), { cause: { code: 'pipeline_conflict' } }) })
  backend.getAuthoringStageModelPreference = getPreference
  backend.saveAuthoringStageModelPreference = savePreference
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.generateAuthoringStage = vi.fn()

  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  const effort = within(screen.getByTestId('picker-authoring-stage-effort-spec'))
  await waitFor(() => expect(effort.getByRole('button')).toBeEnabled())
  await user.click(effort.getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'Automático' }))
  await user.click(screen.getByRole('button', { name: 'Salvar preferência' }))
  await waitFor(() => expect(savePreference).toHaveBeenCalledWith(expect.objectContaining({ expectedRevision: 0, modelMode: 'inherit', effortMode: 'automatic' })))

  expect(await screen.findByRole('alert')).toHaveTextContent(/A preferência mudou em outra ação/)
  expect(screen.getByText(/Preferência v1/)).toBeInTheDocument()
  const refresh = screen.getByRole('button', { name: 'Atualizar preferência' })
  expect(refresh).toBeEnabled()
  expect(screen.getByRole('button', { name: 'Gerar SPEC' })).toBeDisabled()
  expect(savePreference).toHaveBeenCalledTimes(1)
  await user.click(refresh)
  await waitFor(() => expect(screen.getByRole('button', { name: 'Gerar SPEC' })).toBeEnabled())
  expect(savePreference).toHaveBeenCalledTimes(1)
})

it.each([
  { label: 'partial catalog', message: /parcial ou incompleto/, response: { ...phaseCatalog(), status: 'partial' as const, complete: false } },
  { label: 'stale catalog', message: /desatualizado/, response: { ...phaseCatalog(), checkedAt: new Date(Date.now() - 360_000).toISOString() } },
  { label: 'provider profile drift', message: /perfil ou a credencial mudou/, response: { ...phaseCatalog(), backendId: 'different-profile' } },
  { label: 'removed model', message: /modelo salvo saiu do catálogo/, response: { ...phaseCatalog(), models: [] } },
])('blocks saved override after $label without a fallback', async ({ response, message }) => {
  const { backend } = createFakeBackend()
  backend.getAuthoringStageModelPreference = async () => savedOverridePreference('spec')
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [phaseProfile]
  backend.queryHTTPModelCatalog = vi.fn(async () => response)
  backend.generateAuthoringStage = vi.fn()
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  expect(await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })).toBeInTheDocument()
  const generate = screen.getByRole('button', { name: 'Gerar SPEC' })
  expect(generate).toBeDisabled()
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  await consultStageModels()
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  expect(await screen.findByText(message)).toBeInTheDocument()
  expect(generate).toBeDisabled()
  expect(backend.generateAuthoringStage).not.toHaveBeenCalled()
})

it.each([
  { providerType: 'openrouter' as const, source: 'openrouter_general_unfiltered', loaded: undefined, confirmation: /Confirmo que este catálogo geral não está filtrado/ },
  { providerType: 'lm_studio' as const, source: 'lm_studio_native', loaded: false, confirmation: /Autorizo carregar este modelo no LM Studio/ },
])('requires fresh $source consent for a phase override', async ({ providerType, source, loaded, confirmation }) => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const provider: ProviderProfile = { ...phaseProfile, providerType }
  const result: ModelCatalogResult = { ...phaseCatalog(), backendId: provider.id, source, accountFiltered: source !== 'openrouter_general_unfiltered', models: [{ ...phaseCatalog().models[0], source, loaded }] }
  backend.getAuthoringStageModelPreference = async () => unconfiguredSnapshotPreference('spec')
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [provider]
  backend.queryHTTPModelCatalog = vi.fn(async () => result)

  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  await user.click(within(screen.getByTestId('picker-authoring-stage-model-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'Escolher para esta fase' }))
  await user.click(within(await screen.findByTestId('picker-authoring-stage-provider-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: provider.name }))
  await user.click(screen.getByRole('button', { name: 'Consultar modelos' }))
  await user.click(within(await screen.findByTestId('picker-authoring-stage-override-model-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'Modelo High' }))
  expect(await screen.findByLabelText(confirmation)).not.toBeChecked()
  const save = screen.getByRole('button', { name: 'Salvar preferência' })
  if (source === 'openrouter_general_unfiltered') expect(save).toBeDisabled()
  else expect(save).toBeEnabled()
  await user.click(screen.getByLabelText(confirmation))
  expect(save).toBeEnabled()
})

it('offers no specific effort levels when the active catalog announces none', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const noLevelCatalog: ModelCatalogResult = { ...phaseCatalog(), models: [{ ...phaseCatalog().models[0], supportedReasoningEfforts: [] }] }
  backend.getAuthoringStageModelPreference = async () => unconfiguredSnapshotPreference('spec')
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [phaseProfile]
  backend.queryHTTPModelCatalog = vi.fn(async () => noLevelCatalog)
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  await waitFor(() => expect(within(screen.getByTestId('picker-authoring-stage-model-spec')).getByRole('button')).toBeEnabled())
  await user.click(within(screen.getByTestId('picker-authoring-stage-model-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'Escolher para esta fase' }))
  await waitFor(() => expect(within(screen.getByTestId('picker-authoring-stage-provider-spec')).getByRole('button')).toBeEnabled())
  await user.click(within(screen.getByTestId('picker-authoring-stage-provider-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: phaseProfile.name }))
  await user.click(await screen.findByRole('button', { name: 'Consultar modelos' }))
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  await user.click(within(await screen.findByTestId('picker-authoring-stage-override-model-spec')).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'Modelo High' }))
  const effort = within(screen.getByTestId('picker-authoring-stage-effort-spec'))
  await user.click(effort.getByRole('button'))
  expect(screen.getByRole('option', { name: 'Herdar' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Automático' })).toBeInTheDocument()
  expect(screen.queryByRole('option', { name: 'low' })).not.toBeInTheDocument()
  expect(screen.queryByRole('option', { name: 'high' })).not.toBeInTheDocument()
})

it('discards a late preference read from pipeline A after switching to pipeline B', async () => {
  const { backend } = createFakeBackend()
  const pipelineB = { ...stagePipeline, id: 'pipeline-B', workspaceId: 'workspace-B' }
  const preferenceA: AuthoringStageModelPreference = { ...unconfiguredSnapshotPreference('spec'), modelSource: 'brainstorm', inheritedFrom: 'brainstorm' }
  const preferenceB = { ...unconfiguredSnapshotPreference('spec'), pipelineId: pipelineB.id }
  let resolveA!: (value: AuthoringStageModelPreference) => void
  backend.getAuthoringStageModelPreference = vi.fn(async input => input.pipelineId === stagePipeline.id ? new Promise<AuthoringStageModelPreference>(resolve => { resolveA = resolve }) : preferenceB)
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  const view = render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)
  await waitFor(() => expect(backend.getAuthoringStageModelPreference).toHaveBeenCalledWith({ pipelineId: stagePipeline.id, stage: 'spec' }))
  view.rerender(<AuthoringStage backend={backend} pipeline={pipelineB} stage="spec" onPipelineChange={() => undefined} />)
  await waitFor(() => expect(backend.getAuthoringStageModelPreference).toHaveBeenCalledWith({ pipelineId: pipelineB.id, stage: 'spec' }))
  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  await act(async () => resolveA(preferenceA))
  expect(screen.queryByText(/Brainstorm/)).not.toBeInTheDocument()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(backend.getAuthoringStageModelPreference).toHaveBeenCalledWith({ pipelineId: pipelineB.id, stage: 'spec' })
})

it.each([
  { source: 'openrouter_general_unfiltered', accountFiltered: false, requiresConsent: true },
  { source: 'openrouter_account', accountFiltered: true, requiresConsent: false },
])('requires a current inherited OpenRouter catalog and only general catalogs need consent ($source)', async ({ source, accountFiltered, requiresConsent }) => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const router: ProviderProfile = { ...phaseProfile, providerType: 'openrouter', name: 'OpenRouter' }
  const preference: AuthoringStageModelPreference = {
    ...unconfiguredSnapshotPreference('spec'),
    selection: { ...preferenceSelection, backendId: router.id, modelId: 'model-high', source: 'global_default', status: 'unverified_default' },
  }
  const catalog: ModelCatalogResult = {
    ...phaseCatalog(), source, accountFiltered,
    models: [{ ...phaseCatalog().models[0], backendId: router.id, id: 'model-high', source }],
  }
  backend.getAuthoringStageModelPreference = async () => preference
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [router]
  backend.queryHTTPModelCatalog = vi.fn(async () => catalog)
  backend.generateAuthoringStage = vi.fn(async () => readyStage('spec'))
  backend.getPipeline = async () => stagePipeline
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  const generate = screen.getByRole('button', { name: 'Gerar SPEC' })
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  expect(generate).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Consultar modelos' }))
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  if (requiresConsent) {
    const consent = await screen.findByLabelText(/Confirmo que este catálogo geral não está filtrado/)
    expect(generate).toBeDisabled()
    await user.click(consent)
  } else {
    expect(screen.queryByLabelText(/Confirmo que este catálogo geral não está filtrado/)).not.toBeInTheDocument()
  }
  await waitFor(() => expect(generate).toBeEnabled())
  await user.click(generate)
  await waitFor(() => expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1))
  const call = vi.mocked(backend.generateAuthoringStage).mock.calls[0][0]
  expect(call).toEqual(expect.objectContaining({ confirmUnfiltered: requiresConsent }))
  expect(call.selection.confirmUnfiltered).toBe(false)
  if (requiresConsent) expect(call.consentBinding).toEqual({ backendId: router.id, modelId: 'model-high', catalogRevision: catalog.profileRevision, source: catalog.source, destination: catalog.destination })
})

it('previews the safe API destination, model effort, output cap, and local-only permissions before action', async () => {
  const { backend } = createFakeBackend()
  const preference = savedOverridePreference('spec')
  const catalogWithSecret: ModelCatalogResult = { ...phaseCatalog(), credentialToken: 'credential-preview-canary', destination: preference.selection.destination }
  backend.getAuthoringStageModelPreference = async () => preference
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [phaseProfile]
  backend.queryHTTPModelCatalog = vi.fn(async () => catalogWithSecret)
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  await userEvent.click(await screen.findByRole('button', { name: 'Consultar modelos' }))
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  expect(screen.getByText('Destino seguro da API').parentElement).toHaveTextContent(preference.selection.destination)
  expect(screen.getByText('Modelo e esforço').parentElement).toHaveTextContent('model-high · high')
  expect(screen.getByText('Limite de saída').parentElement).toHaveTextContent('4096 tokens')
  expect(screen.getByText('Permissões desta fase').parentElement).toHaveTextContent(/A fase acessa somente a API selecionada; não acessa arquivos do workspace, shell, rede geral ou MCP/)
  expect(screen.queryByText('credential-preview-canary')).not.toBeInTheDocument()
})

it('does not present a stale phase destination as currently validated', async () => {
  const { backend } = createFakeBackend()
  const stale = { ...savedOverridePreference('spec'), resolution: 'stale' as const, errorCode: 'backend_changed' }
  backend.getAuthoringStageModelPreference = async () => stale
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [phaseProfile]
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  expect(screen.getByRole('alert')).toHaveTextContent(/seleção salva está desatualizada/i)
  expect(screen.queryByText(stale.selection.destination)).not.toBeInTheDocument()
  expect(screen.getByText('Destino não validado')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Gerar SPEC' })).toBeDisabled()
})

it('preserves an already-consented OpenRouter phase override without treating it as global consent', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const router: ProviderProfile = { ...phaseProfile, providerType: 'openrouter', name: 'OpenRouter' }
  const checkedAt = new Date().toISOString()
  const preference: AuthoringStageModelPreference = {
    ...savedOverridePreference('spec'), effortMode: 'automatic', explicitEffort: '',
    selection: { ...preferenceSelection, backendId: router.id, modelId: 'model-high', reasoningEffort: '', catalogRevision: 'profile-rev-2', source: 'openrouter_general_unfiltered', destination: 'https://api.example.test:443', status: 'listed_unfiltered', confirmUnfiltered: true, confirmJitLoad: false, checkedAt },
  }
  const catalog: ModelCatalogResult = { ...phaseCatalog(), source: 'openrouter_general_unfiltered', accountFiltered: false, models: [{ ...phaseCatalog().models[0], source: 'openrouter_general_unfiltered' }] }
  backend.getAuthoringStageModelPreference = async () => preference
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [router]
  backend.queryHTTPModelCatalog = vi.fn(async () => catalog)
  backend.generateAuthoringStage = vi.fn(async () => readyStage('spec'))
  backend.getPipeline = async () => stagePipeline
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  await user.click(screen.getByRole('button', { name: 'Consultar modelos' }))
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  expect(await screen.findByLabelText(/Confirmo que este catálogo geral não está filtrado/)).toBeChecked()
  const generate = screen.getByRole('button', { name: 'Gerar SPEC' })
  await waitFor(() => expect(generate).toBeEnabled())
  await user.click(generate)
  await waitFor(() => expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1))
  expect(vi.mocked(backend.generateAuthoringStage).mock.calls[0][0]).toEqual(expect.objectContaining({
    confirmUnfiltered: true,
    consentBinding: { backendId: router.id, modelId: 'model-high', catalogRevision: catalog.profileRevision, source: catalog.source, destination: catalog.destination },
  }))
})

it('preserves explicit OpenRouter consent inherited from Brainstorm for the matching catalog selection', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const router: ProviderProfile = { ...phaseProfile, providerType: 'openrouter', name: 'OpenRouter' }
  const checkedAt = new Date().toISOString()
  const preference: AuthoringStageModelPreference = {
    ...unconfiguredSnapshotPreference('spec'), modelSource: 'brainstorm', effortSource: 'brainstorm', inheritedFrom: 'brainstorm', catalogValidationRequired: false,
    selection: { ...preferenceSelection, backendId: router.id, modelId: 'model-high', reasoningEffort: 'high', catalogRevision: 'profile-rev-2', source: 'openrouter_general_unfiltered', destination: 'https://api.example.test:443', status: 'listed_unfiltered', confirmUnfiltered: true, checkedAt },
  }
  const catalog: ModelCatalogResult = { ...phaseCatalog(), source: 'openrouter_general_unfiltered', accountFiltered: false, models: [{ ...phaseCatalog().models[0], source: 'openrouter_general_unfiltered' }] }
  backend.getAuthoringStageModelPreference = async () => preference
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [router]
  backend.queryHTTPModelCatalog = vi.fn(async () => catalog)
  backend.generateAuthoringStage = vi.fn(async () => readyStage('spec'))
  backend.getPipeline = async () => stagePipeline
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  const generate = screen.getByRole('button', { name: 'Gerar SPEC' })
  expect(generate).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Consultar modelos' }))
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  expect(await screen.findByLabelText(/Confirmo que este catálogo geral não está filtrado/)).toBeChecked()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Gerar SPEC' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Gerar SPEC' }))
  await waitFor(() => expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1))
  expect(vi.mocked(backend.generateAuthoringStage).mock.calls[0][0]).toEqual(expect.objectContaining({
    confirmUnfiltered: true,
    consentBinding: { backendId: router.id, modelId: 'model-high', catalogRevision: catalog.profileRevision, source: catalog.source, destination: catalog.destination },
  }))
})

it('requires and consumes LM Studio JIT consent for each generation attempt', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const checkedAt = new Date().toISOString()
  const localProfile: ProviderProfile = { ...phaseProfile, id: 'local-lm', providerType: 'lm_studio', name: 'LM Studio' }
  const inherited: AuthoringStageModelPreference = {
    ...unconfiguredSnapshotPreference('spec'), modelSource: 'brainstorm', effortSource: 'brainstorm', inheritedFrom: 'brainstorm', catalogValidationRequired: false,
    selection: { ...preferenceSelection, backendId: localProfile.id, modelId: 'local/model', reasoningEffort: 'high', catalogRevision: 'profile-rev-2', source: 'lm_studio_native', destination: 'http://127.0.0.1:1234', status: 'listed', confirmJitLoad: true, checkedAt },
  }
  const localCatalog: ModelCatalogResult = { ...phaseCatalog(), backendId: localProfile.id, source: 'lm_studio_native', destination: inherited.selection.destination, profileRevision: inherited.selection.catalogRevision, accountFiltered: true, models: [{ id: 'local/model', displayName: 'Local Model', backendId: localProfile.id, source: 'lm_studio_native', availability: 'available', loaded: false, supportedReasoningEfforts: ['high'] }] }
  backend.getAuthoringStageModelPreference = async () => inherited
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [localProfile]
  backend.queryHTTPModelCatalog = vi.fn(async () => localCatalog)
  backend.generateAuthoringStage = vi.fn(async () => readyStage('spec'))
  backend.getPipeline = async () => stagePipeline
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await screen.findByRole('heading', { name: 'Modelo e esforço desta fase' })
  const generate = screen.getByRole('button', { name: 'Gerar SPEC' })
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  expect(generate).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Consultar modelos' }))
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  const consent = await screen.findByLabelText(/Autorizo carregar este modelo no LM Studio para esta tentativa/)
  expect(consent).not.toBeChecked()
  expect(generate).toBeDisabled()
  await user.click(consent)
  await waitFor(() => expect(generate).toBeEnabled())
  await user.click(generate)
  await waitFor(() => expect(backend.generateAuthoringStage).toHaveBeenCalledTimes(1))
  expect(vi.mocked(backend.generateAuthoringStage).mock.calls[0][0]).toEqual(expect.objectContaining({
    confirmJitLoad: true,
    consentBinding: { backendId: localProfile.id, modelId: 'local/model', catalogRevision: localCatalog.profileRevision, source: localCatalog.source, destination: localCatalog.destination },
  }))
  expect(vi.mocked(backend.generateAuthoringStage).mock.calls[0][0].selection.confirmJitLoad).toBe(false)
  await waitFor(() => { expect(consent).not.toBeChecked(); expect(screen.getByRole('button', { name: 'Gerar SPEC' })).toBeDisabled() })
})

it('lets an expired Brainstorm choice be reconfirmed without re-picking provider and model', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const expired: AuthoringStageModelPreference = {
    ...unconfiguredSnapshotPreference('spec'), resolution: 'stale', errorCode: 'backend_changed', modelSource: 'brainstorm', effortSource: '', inheritedFrom: '', catalogValidationRequired: false,
    selection: { ...preferenceSelection, backendId: phaseProfile.id, modelId: 'model-high', catalogRevision: 'profile-rev-2', source: 'openai_models', status: 'listed', maxOutputTokens: 256, checkedAt: new Date(Date.now() - 30 * 60_000).toISOString() },
  }
  const resolved: AuthoringStageModelPreference = { ...expired, modelMode: 'override', resolution: 'ready', errorCode: '', modelSource: 'phase_override', preferenceRevision: 1, selection: { ...expired.selection, maxOutputTokens: 4096, checkedAt: new Date().toISOString() } }
  let current = expired
  const save = vi.fn(async (_input: SaveAuthoringStageModelPreferenceInput) => { current = resolved; return resolved })
  backend.getAuthoringStageModelPreference = async () => current
  backend.saveAuthoringStageModelPreference = save
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [phaseProfile]
  backend.queryHTTPModelCatalog = vi.fn(async () => phaseCatalog())
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  const reconfirm = await screen.findByRole('button', { name: 'Reconfirmar model-high · API principal' })
  expect(screen.getByRole('button', { name: 'Gerar SPEC' })).toBeDisabled()
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  await user.click(reconfirm)
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  // The click chose this exact provider and model, so the clean catalog read saves it without another click.
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1))
  expect(save.mock.calls[0][0]).toMatchObject({ modelMode: 'override', selection: { profileId: phaseProfile.id, modelId: 'model-high' } })
  await waitFor(() => expect(screen.getByRole('button', { name: 'Gerar SPEC' })).toBeEnabled())
  expect(save).toHaveBeenCalledTimes(1)
})

it('stops reconfirming at the catalog when the model left it and saves nothing', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const expired: AuthoringStageModelPreference = {
    ...unconfiguredSnapshotPreference('spec'), resolution: 'stale', errorCode: 'backend_changed', modelSource: 'brainstorm', catalogValidationRequired: false,
    selection: { ...preferenceSelection, backendId: phaseProfile.id, modelId: 'model-gone', catalogRevision: 'profile-rev-2', source: 'openai_models', status: 'listed' },
  }
  const save = vi.fn()
  backend.getAuthoringStageModelPreference = async () => expired
  backend.saveAuthoringStageModelPreference = save
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [phaseProfile]
  backend.queryHTTPModelCatalog = vi.fn(async () => phaseCatalog())
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  await user.click(await screen.findByRole('button', { name: 'Reconfirmar model-gone · API principal' }))
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledTimes(1))
  expect(await screen.findByText(/O modelo salvo saiu do catálogo/)).toBeInTheDocument()
  expect(save).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: 'Gerar SPEC' })).toBeDisabled()
})

it('offers no reconfirmation for an expired choice whose provider is gone', async () => {
  const { backend } = createFakeBackend()
  const expired: AuthoringStageModelPreference = {
    ...unconfiguredSnapshotPreference('spec'), resolution: 'stale', errorCode: 'backend_changed', modelSource: 'brainstorm', catalogValidationRequired: false,
    selection: { ...preferenceSelection, backendId: 'removed-profile', modelId: 'model-high', catalogRevision: 'profile-rev-2', source: 'openai_models', status: 'listed' },
  }
  backend.getAuthoringStageModelPreference = async () => expired
  backend.getAuthoringStage = async input => ({ ...readyStage(input.stage), pipelineId: input.pipelineId })
  backend.listProviderProfiles = async () => [phaseProfile]
  render(<AuthoringStage backend={backend} pipeline={stagePipeline} stage="spec" onPipelineChange={() => undefined} />)

  expect(await screen.findByRole('alert')).toHaveTextContent(/seleção salva está desatualizada/i)
  expect(screen.queryByRole('button', { name: /Reconfirmar/ })).not.toBeInTheDocument()
})
