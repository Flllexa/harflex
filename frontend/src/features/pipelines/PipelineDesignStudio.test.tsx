import { StrictMode } from 'react'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import type { PipelineDesign } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { designPipeline, designReadback, installPipelineDesignFixture } from '../../test/pipelineDesignFixture'
import { PipelineDesignStudio } from './PipelineDesignStudio'
import { DesignMarkdown } from './PipelineDesignDocument'
import { PipelinesPage } from './PipelinesPage'

function setup(complete = false) {
  const { backend } = createFakeBackend(), pipeline = designPipeline()
  const fixture = installPipelineDesignFixture(backend, undefined, { pipeline, design: designReadback(pipeline, complete) })
  return { backend, pipeline, fixture }
}

it('opens persisted documents without inference or provider catalog calls, including StrictMode', async () => {
  const { backend, pipeline } = setup(true)
  backend.preparePipelineDesign = vi.fn(); backend.queryHTTPModelCatalog = vi.fn(); backend.queryCLIModelCatalog = vi.fn()
  render(<StrictMode><PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} /></StrictMode>)
  expect(await screen.findByRole('heading', { name: 'Critérios de aceite' })).toBeInTheDocument()
  expect(backend.preparePipelineDesign).not.toHaveBeenCalled()
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  expect(backend.queryCLIModelCatalog).not.toHaveBeenCalled()
})

it('prepares SPEC and Plan from saved Discovery with inherited defaults in one click', async () => {
  const { backend, pipeline } = setup(), prepare = vi.fn(backend.preparePipelineDesign)
  backend.preparePipelineDesign = prepare
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await userEvent.type(await screen.findByLabelText('Pedido para a IA'), 'Próximo ajuste em rascunho.')
  await userEvent.click(await screen.findByRole('button', { name: 'Preparar SPEC e Plan' }))
  expect(await screen.findByRole('heading', { name: 'Critérios de aceite' })).toBeInTheDocument()
  expect(prepare).toHaveBeenCalledOnce()
  expect(prepare).toHaveBeenCalledWith(expect.objectContaining({ message: 'Prepare a SPEC e o Plan para o Discovery salvo.', target: 'all', ref: expect.objectContaining({ pipelineId: pipeline.id, pipelineRevision: 1, designRevision: 1 }) }))
  expect(prepare.mock.calls[0][0].selections).toBeUndefined()
  expect(screen.getByLabelText('Pedido para a IA')).toHaveValue('Próximo ajuste em rascunho.')
})

it('consumes explicit creation preparation once under StrictMode and never repeats it on reload', async () => {
  const { backend, pipeline } = setup(), prepare = vi.fn(backend.preparePipelineDesign), consumed = vi.fn()
  backend.preparePipelineDesign = prepare
  const view = render(<StrictMode><PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} autoPrepareRequest="creation-request" onAutoPrepareConsumed={consumed} /></StrictMode>)
  await screen.findByRole('heading', { name: 'Critérios de aceite' })
  expect(prepare).toHaveBeenCalledOnce(); expect(consumed).toHaveBeenCalledWith('creation-request')
  view.unmount()
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await screen.findByRole('heading', { name: 'Critérios de aceite' })
  expect(prepare).toHaveBeenCalledOnce()
})

it('targets a chat change, clears only admitted text and preserves a new draft during the attempt', async () => {
  const { backend, pipeline, fixture } = setup(true), user = userEvent.setup()
  let resolve!: (value: PipelineDesign) => void
  const prepare = vi.fn((input: Parameters<typeof backend.preparePipelineDesign>[0]) => {
    const admitted = fixture.read()
    admitted.revision++; admitted.state = 'running'; admitted.phase = 'plan'; admitted.activeAttemptId = 'attempt-new'
    admitted.attempts.push({ id: 'attempt-new', requestId: input.ref.requestId, target: input.target, status: 'running', phase: 'plan', selections: {}, sessionIds: {}, errorCode: '', createdAt: admitted.createdAt, updatedAt: admitted.updatedAt })
    admitted.messages.push({ id: 'chat-new', role: 'user', content: input.message, target: input.target, attemptId: 'attempt-new', createdAt: admitted.createdAt })
    fixture.writeDesign(admitted)
    return new Promise<PipelineDesign>(done => { resolve = done })
  })
  backend.preparePipelineDesign = prepare
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await screen.findByRole('heading', { name: 'Critérios de aceite' })
  await user.click(screen.getByTestId('picker-design-target').querySelector('button')!)
  await user.click(screen.getByRole('option', { name: 'Plan' }))
  const composer = screen.getByLabelText('Pedido para a IA')
  await user.type(composer, 'Adicionar verificação das datas.')
  await user.click(screen.getByRole('button', { name: 'Enviar pedido' }))
  await waitFor(() => expect(composer).toHaveValue(''))
  expect(prepare).toHaveBeenCalledWith(expect.objectContaining({ target: 'plan', message: 'Adicionar verificação das datas.' }))
  expect(composer).toBeEnabled(); expect(screen.getByRole('button', { name: 'Enviar pedido' })).toBeDisabled()
  await user.type(composer, 'Próximo pedido ainda em rascunho.')
  const terminal = fixture.read(); terminal.state = 'ready'; terminal.activeAttemptId = ''; terminal.revision++
  await act(async () => resolve(terminal))
  expect(composer).toHaveValue('Próximo pedido ainda em rascunho.')
})

it('keeps the composer text when the provider rejects before durable admission', async () => {
  const { backend, pipeline } = setup(true)
  backend.preparePipelineDesign = vi.fn(async () => { throw new Error('Provedor indisponível') })
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await screen.findByRole('heading', { name: 'Critérios de aceite' })
  await userEvent.type(screen.getByLabelText('Pedido para a IA'), 'Preservar meu pedido.')
  await userEvent.click(screen.getByRole('button', { name: 'Enviar pedido' }))
  expect(await screen.findByRole('alert')).toBeInTheDocument()
  expect(screen.getByLabelText('Pedido para a IA')).toHaveValue('Preservar meu pedido.')
  expect(screen.getByRole('button', { name: 'Enviar pedido' })).toBeEnabled()
})

it('saves manual SPEC without a provider, marks Plan stale and blocks approval until updated', async () => {
  const { backend, pipeline, fixture } = setup(true)
  backend.listBackends = async () => []; backend.getSettings = async () => ({ defaultBackendId: '', defaultModelBackendId: '', defaultModelId: '' })
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Editar SPEC' }))
  await userEvent.clear(screen.getByLabelText('Texto de SPEC'))
  await userEvent.type(screen.getByLabelText('Texto de SPEC'), '# SPEC manual\n\nCritérios confirmados.')
  expect(screen.getByRole('button', { name: 'Aprovar documentos e continuar para Code' })).toBeDisabled()
  await userEvent.click(screen.getByRole('button', { name: 'Salvar SPEC' }))
  expect(await screen.findByText('SPEC manual')).toBeInTheDocument()
  expect(fixture.read().documents.plan.stale).toBe(true)
  expect(screen.getByRole('button', { name: 'Aprovar documentos e continuar para Code' })).toBeDisabled()
  await userEvent.click(screen.getByRole('tab', { name: /Plan/ }))
  expect(screen.getByText('Este documento precisa ser atualizado.')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Editar Plan' }))
  await userEvent.click(screen.getByRole('button', { name: 'Salvar Plan' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Aprovar documentos e continuar para Code' })).toBeEnabled())
})

it('restores an archived version as a new version and approves current document hashes', async () => {
  const { backend, pipeline, fixture } = setup(true), changed = vi.fn(), approve = vi.fn(backend.approvePipelineDesign)
  backend.approvePipelineDesign = approve
  const current = fixture.read(); current.documents.plan.version = 2; current.documents.plan.content = '# Plan novo'; current.documents.plan.contentDigest = 'b'.repeat(64); current.versions.plan.push({ ...current.documents.plan, reason: 'manual', restoredFromVersion: 0, createdAt: current.updatedAt }); fixture.writeDesign(current)
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={changed} />)
  await userEvent.click(await screen.findByRole('tab', { name: /Plan/ }))
  await userEvent.click(screen.getByText('Histórico de Plan (2)'))
  await userEvent.click(screen.getByRole('button', { name: 'Restaurar versão 1' }))
  expect(await screen.findByRole('heading', { name: 'Implementação' })).toBeInTheDocument()
  expect(fixture.read().documents.plan.version).toBe(3)
  const digests = Object.fromEntries(['discovery', 'spec', 'plan'].map(stage => [stage, fixture.read().documents[stage].contentDigest]))
  await userEvent.click(screen.getByRole('button', { name: 'Aprovar documentos e continuar para Code' }))
  await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ currentStage: 'code' })))
  expect(approve).toHaveBeenCalledWith(expect.objectContaining({ digests }))
})

it('cancels the admitted attempt and fences new requests while cancellation is pending', async () => {
  const { backend, pipeline, fixture } = setup(true)
  const current = fixture.read(); current.state = 'running'; current.phase = 'spec'; current.activeAttemptId = 'attempt-live'; fixture.writeDesign(current)
  backend.cancelPipelineDesign = vi.fn(async () => { const pending = fixture.read(); pending.state = 'cancellation_pending'; fixture.writeDesign(pending); return pending })
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Cancelar preparação' }))
  expect(backend.cancelPipelineDesign).toHaveBeenCalledWith({ pipelineId: pipeline.id, attemptId: 'attempt-live' })
  expect(await screen.findByText(/Aguardando a confirmação do cancelamento/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Enviar pedido' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Editar SPEC' })).toBeDisabled()
})

it('derives from the current parent revision after Code without mutating that parent', async () => {
  const { backend, pipeline, fixture } = setup(true), changed = vi.fn(), current = fixture.read()
  current.needsDerivation = true; current.state = 'approved'; current.currentPipelineRevision = pipeline.revision; fixture.writeDesign(current)
  const derive = vi.fn(backend.deriveAuthoringPipeline); backend.deriveAuthoringPipeline = derive
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={changed} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Criar continuação para editar' }))
  await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ derivedFromPipelineId: pipeline.id })))
  expect(derive).toHaveBeenCalledWith(expect.objectContaining({ expectedRevision: current.currentPipelineRevision, discovery: current.documents.discovery.content }))
  expect(fixture.pipeline(pipeline.id)).toEqual(pipeline)
})

it('ignores preparation and readback results from the previous project', async () => {
  const { backend, pipeline, fixture } = setup(true), changed = vi.fn()
  let resolve!: (value: PipelineDesign) => void
  backend.preparePipelineDesign = vi.fn(() => new Promise<PipelineDesign>(done => { resolve = done }))
  const view = render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={changed} />)
  await screen.findByRole('heading', { name: 'Critérios de aceite' })
  await userEvent.type(screen.getByLabelText('Pedido para a IA'), 'Pedido antigo')
  await userEvent.click(screen.getByRole('button', { name: 'Enviar pedido' }))
  const nextPipeline = designPipeline('design-other', 'workspace-other'), next = designReadback(nextPipeline)
  fixture.writeDesign(next)
  view.rerender(<PipelineDesignStudio backend={backend} pipeline={nextPipeline} onPipelineChange={changed} />)
  await screen.findByRole('button', { name: 'Preparar SPEC e Plan' })
  await act(async () => resolve(fixture.read(pipeline.id)))
  expect(screen.queryByText('Pedido antigo')).not.toBeInTheDocument()
  expect(screen.getByLabelText('Pedido para a IA')).toHaveValue('')
  expect(changed).not.toHaveBeenCalled()
})

it('validates a model override for SPEC without changing settings or overriding other documents', async () => {
  const { backend, pipeline } = setup(true), user = userEvent.setup(), prepare = vi.fn(backend.preparePipelineDesign)
  backend.preparePipelineDesign = prepare; backend.saveSettings = vi.fn()
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await screen.findByRole('heading', { name: 'Critérios de aceite' })
  await user.click(screen.getByText('Modelos'))
  await user.click(await screen.findByTestId('picker-design-provider-spec').then(element => element.querySelector('button')!))
  await user.click(await screen.findByRole('option', { name: 'API de teste' }))
  const model = await screen.findByRole('combobox', { name: 'Modelo de SPEC' })
  await waitFor(() => expect(model).toBeEnabled())
  await user.click(model); await user.clear(model); await user.type(model, 'alternativo')
  await user.click(await screen.findByRole('option', { name: 'Modelo alternativo' }))
  await user.type(screen.getByLabelText('Pedido para a IA'), 'Atualizar os critérios.')
  await user.click(screen.getByRole('button', { name: 'Enviar pedido' }))
  await waitFor(() => expect(prepare).toHaveBeenCalledOnce())
  expect(prepare.mock.calls[0][0].selections).toEqual({ spec: expect.objectContaining({ executor: 'api', profileId: 'api-1', modelId: 'alternative-model', catalogRevision: 'fixture-revision', credentialToken: 'a'.repeat(64) }) })
  expect(backend.saveSettings).not.toHaveBeenCalled()
})

it('says which documents follow what the project chose for their phase, and leaves the others on the default', async () => {
  const { backend, pipeline } = setup(), user = userEvent.setup()
  const chosen = [{ workspaceId: pipeline.workspaceId, stage: 'spec' as const, backendId: 'api-1', modelId: 'alternative-model', updatedAt: '2026-10-02T10:00:00Z' }, { workspaceId: pipeline.workspaceId, stage: 'code' as const, backendId: 'api-1', modelId: 'code-model', updatedAt: '2026-10-02T10:00:00Z' }]
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} stageExecutors={chosen} onPipelineChange={() => undefined} />)
  const summary = (await screen.findByText('Modelos')).closest('summary')!
  // Only the document phases count here: Code has its own screen.
  expect(summary).toHaveTextContent('Por fase · SPEC: alternative-model')
  expect(summary).not.toHaveTextContent('code-model')
  await user.click(screen.getByText('Modelos'))
  await user.click(await screen.findByTestId('picker-design-provider-spec').then(element => element.querySelector('button')!))
  expect(await screen.findByRole('option', { name: 'Usar a escolha da fase' })).toBeInTheDocument()
  await user.keyboard('{Escape}')
  await user.click(await screen.findByTestId('picker-design-provider-plan').then(element => element.querySelector('button')!))
  expect(await screen.findByRole('option', { name: 'Usar padrão' })).toBeInTheDocument()
})

it('preserves a newer composer draft when admission arrives after the text was replaced', async () => {
  const { backend, pipeline, fixture } = setup(true), user = userEvent.setup()
  let input!: Parameters<typeof backend.preparePipelineDesign>[0], resolve!: (value: PipelineDesign) => void
  backend.preparePipelineDesign = vi.fn(value => { input = value; return new Promise<PipelineDesign>(done => { resolve = done }) })
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await screen.findByRole('heading', { name: 'Critérios de aceite' })
  const composer = screen.getByLabelText('Pedido para a IA')
  await user.type(composer, 'Texto enviado'); await user.click(screen.getByRole('button', { name: 'Enviar pedido' }))
  await user.clear(composer); await user.type(composer, 'Novo rascunho')
  const admitted = fixture.read(); admitted.revision++; admitted.state = 'running'; admitted.phase = 'spec'; admitted.activeAttemptId = 'attempt-later'
  admitted.attempts.push({ id: 'attempt-later', requestId: input.ref.requestId, target: input.target, status: 'running', phase: 'spec', selections: {}, sessionIds: {}, errorCode: '', createdAt: admitted.createdAt, updatedAt: admitted.updatedAt })
  admitted.messages.push({ id: 'message-later', role: 'user', content: input.message, target: input.target, attemptId: 'attempt-later', createdAt: admitted.createdAt }); fixture.writeDesign(admitted)
  await user.click(screen.getByRole('button', { name: 'Atualizar trabalho' }))
  expect(composer).toHaveValue('Novo rascunho')
  await act(async () => resolve({ ...admitted, state: 'ready', activeAttemptId: '', revision: admitted.revision + 1 }))
  expect(composer).toHaveValue('Novo rascunho')
})

it('renders unfinished Markdown markers safely without dropping subsequent document text', () => {
  render(<DesignMarkdown content={'# SPEC\n\n- \n1. \n\nTexto depois dos marcadores.\n\n<script>nunca executar</script>'} />)
  expect(screen.getByText('Texto depois dos marcadores.')).toBeInTheDocument()
  expect(screen.getByText('<script>nunca executar</script>')).toBeInTheDocument()
})

it('reuses the exact request when retrying an unadmitted preparation after a response error', async () => {
  const { backend, pipeline } = setup(true), user = userEvent.setup()
  const prepare = vi.fn(async () => { throw new Error('Resposta indisponível') }); backend.preparePipelineDesign = prepare
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await screen.findByRole('heading', { name: 'Critérios de aceite' })
  await user.type(screen.getByLabelText('Pedido para a IA'), 'Revisar o formato das datas.')
  await user.click(screen.getByRole('button', { name: 'Enviar pedido' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Enviar pedido' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Enviar pedido' }))
  await waitFor(() => expect(prepare).toHaveBeenCalledTimes(2))
  expect(prepare.mock.calls[1]).toEqual(prepare.mock.calls[0])
})

it('shows the failed attempt cause and retries its saved message without clearing a newer draft', async () => {
  const { backend, pipeline, fixture } = setup(true), current = fixture.read(), prepare = vi.fn(backend.preparePipelineDesign)
  backend.preparePipelineDesign = prepare
  current.state = 'paused'; current.attempts.push({ id: 'failed-attempt', requestId: 'failed-request', target: 'plan', status: 'failed', phase: 'plan', selections: {}, sessionIds: {}, errorCode: 'pipeline_design_invalid_response', createdAt: current.createdAt, updatedAt: current.updatedAt })
  current.messages.push({ id: 'failed-message', role: 'user', content: 'Verificar datas ISO.', target: 'plan', attemptId: 'failed-attempt', createdAt: current.createdAt }); fixture.writeDesign(current)
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  expect(await screen.findByRole('alert')).toHaveTextContent(/fora do formato esperado/i)
  await userEvent.type(screen.getByLabelText('Pedido para a IA'), 'Meu próximo pedido')
  await userEvent.click(screen.getByRole('button', { name: 'Tentar preparação novamente' }))
  await waitFor(() => expect(prepare).toHaveBeenCalledWith(expect.objectContaining({ message: 'Verificar datas ISO.', target: 'plan' })))
  expect(screen.getByLabelText('Pedido para a IA')).toHaveValue('Meu próximo pedido')
})

it('admits Codex for Code only when the backend declares Professional capabilities', async () => {
  const { backend } = createFakeBackend(), pipeline = { ...designPipeline(), currentStage: 'code' as const, stageStatus: { discovery: 'completed' as const, spec: 'completed' as const, plan: 'completed' as const, code: 'active' as const, eval: 'pending' as const } }
  const design = { ...designReadback(pipeline, true), needsDerivation: true, state: 'approved' }
  installPipelineDesignFixture(backend, undefined, { pipeline, design })
  const start = vi.fn(async () => undefined)
  render(<PipelinesPage backend={backend} workspaceId={pipeline.workspaceId} selectedPipeline={pipeline} backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true, professionalAvailable: true }]} preferredBackendId="codex" preferredModelBackendId="codex" preferredModelId="codex-test" onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={start} />)
  await waitFor(() => expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeEnabled())
  await userEvent.click(screen.getByRole('button', { name: 'Executar Coder' }))
  expect(start).toHaveBeenCalledWith(pipeline.id, 'codex', 'coder', expect.objectContaining({ executor: 'codex_cli', backendId: 'codex', modelId: 'codex-test', source: 'codex_app_server' }), false)
})

it('reads the committed pipeline after losing an approval response and publishes its current revision', async () => {
  const { backend, pipeline, fixture } = setup(true), changed = vi.fn(), approve = backend.approvePipelineDesign
  backend.approvePipelineDesign = vi.fn(async input => { await approve(input); throw new Error('Resposta da aprovação perdida') })
  backend.getPipeline = vi.fn(backend.getPipeline)
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={changed} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Aprovar documentos e continuar para Code' }))
  await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ id: pipeline.id, currentStage: 'code', revision: 2 })))
  expect(backend.getPipeline).toHaveBeenCalledWith(pipeline.id)
  expect(screen.getByRole('button', { name: 'Criar continuação para editar' })).toBeEnabled()
  expect(fixture.pipeline().artifacts.spec.contentDigest).toBe(fixture.read().documents.spec.contentDigest)
})

it.each(['unavailable', 'wrong-identity', 'old-revision'] as const)('blocks derivation until pipeline readback confirms an approval (%s)', async problem => {
  const { backend, pipeline } = setup(true), changed = vi.fn(), approve = backend.approvePipelineDesign, getPipeline = backend.getPipeline
  backend.approvePipelineDesign = vi.fn(async input => { await approve(input); throw new Error('Resposta perdida') })
  let valid = false
  backend.getPipeline = vi.fn(async id => {
    const result = await getPipeline(id)
    if (valid) return result
    if (problem === 'unavailable') throw new Error('Pipeline indisponível')
    return problem === 'wrong-identity' ? { ...result, id: 'outro-trabalho' } : { ...result, revision: pipeline.revision }
  })
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={changed} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Aprovar documentos e continuar para Code' }))
  await waitFor(() => expect(backend.getPipeline).toHaveBeenCalled())
  expect(changed).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: 'Criar continuação para editar' })).toBeDisabled()
  valid = true
  await userEvent.click(screen.getByRole('button', { name: 'Atualizar trabalho' }))
  await waitFor(() => expect(changed).toHaveBeenCalledWith(expect.objectContaining({ currentStage: 'code', revision: 2 })))
  expect(screen.getByRole('button', { name: 'Criar continuação para editar' })).toBeEnabled()
})

it('ignores a pipeline confirmation arriving after switching projects', async () => {
  const { backend, pipeline, fixture } = setup(true), changed = vi.fn(), approve = backend.approvePipelineDesign
  let resolve!: (value: typeof pipeline) => void
  backend.approvePipelineDesign = async input => { await approve(input); throw new Error('Resposta perdida') }
  backend.getPipeline = vi.fn(() => new Promise<typeof pipeline>(done => { resolve = done }))
  const view = render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={changed} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Aprovar documentos e continuar para Code' }))
  await waitFor(() => expect(backend.getPipeline).toHaveBeenCalled())
  const other = designPipeline('other-design', 'other-workspace'); fixture.writeDesign(designReadback(other))
  view.rerender(<PipelineDesignStudio backend={backend} pipeline={other} onPipelineChange={changed} />)
  await screen.findByRole('button', { name: 'Preparar SPEC e Plan' })
  await act(async () => resolve(fixture.pipeline(pipeline.id)))
  expect(changed).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: 'Preparar SPEC e Plan' })).toBeInTheDocument()
})

it('changes the inspected document from the top stage request without editing or inferring', async () => {
  const { backend, pipeline } = setup(true)
  backend.preparePipelineDesign = vi.fn(); backend.editPipelineDesignDocument = vi.fn()
  const view = render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} requestedStage="plan" viewRequestId={1} />)
  expect(await screen.findByRole('tab', { name: 'Plan', selected: true })).toBeInTheDocument()
  expect(screen.getByRole('region', { name: 'Preparar trabalho' })).toHaveAttribute('data-pane', 'documents')
  view.rerender(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} requestedStage="discovery" viewRequestId={2} />)
  expect(await screen.findByRole('tab', { name: 'Discovery', selected: true })).toBeInTheDocument()
  expect(backend.preparePipelineDesign).not.toHaveBeenCalled()
  expect(backend.editPipelineDesignDocument).not.toHaveBeenCalled()
})

it('keeps a composer draft and target after returning from settings without storage writes or inference', async () => {
  const { backend, pipeline } = setup(true), user = userEvent.setup(), settings = vi.fn(), writes = vi.spyOn(Storage.prototype, 'setItem')
  backend.preparePipelineDesign = vi.fn()
  const view = render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} onSettings={settings} />)
  await user.type(await screen.findByLabelText('Pedido para a IA'), 'Rascunho ainda não enviado.')
  await user.click(screen.getByTestId('picker-design-target').querySelector('button')!)
  await user.click(screen.getByRole('option', { name: 'Plan' }))
  await user.click(screen.getByText('Modelos')); await user.click(screen.getByRole('button', { name: 'Abrir Configurações' }))
  expect(settings).toHaveBeenCalledOnce(); view.unmount()
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  expect(await screen.findByLabelText('Pedido para a IA')).toHaveValue('Rascunho ainda não enviado.')
  expect(screen.getByTestId('picker-design-target').querySelector('button')).toHaveTextContent('Plan')
  expect(backend.preparePipelineDesign).not.toHaveBeenCalled(); expect(writes).not.toHaveBeenCalled(); writes.mockRestore()
})

it('blocks a Plan chat request based on stale SPEC while keeping Plan readable', async () => {
  const { backend, pipeline, fixture } = setup(true), current = fixture.read(), user = userEvent.setup()
  current.documents.spec.stale = true; current.documents.plan.stale = true; fixture.writeDesign(current)
  backend.preparePipelineDesign = vi.fn()
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click(await screen.findByTestId('picker-design-target').then(element => element.querySelector('button')!))
  await user.click(screen.getByRole('option', { name: 'Plan' }))
  await user.type(screen.getByLabelText('Pedido para a IA'), 'Mudar somente o Plan.')
  expect(screen.getByRole('button', { name: 'Enviar pedido' })).toBeDisabled()
  expect(screen.getByText('Atualize a SPEC com o Discovery atual antes de preparar o Plan.')).toBeInTheDocument()
  await user.click(screen.getByRole('tab', { name: /Plan/ }))
  expect(screen.getByRole('article', { name: 'Documento Plan versão 1' })).toBeInTheDocument()
  expect(backend.preparePipelineDesign).not.toHaveBeenCalled()
})

it('uses the confirmed pipeline revision for continuation after the parent has advanced', async () => {
  const { backend, pipeline, fixture } = setup(true), current = fixture.read(), changed = vi.fn()
  current.state = 'approved'; current.needsDerivation = true; current.currentPipelineRevision = 2; fixture.writeDesign(current)
  backend.getPipeline = vi.fn(async () => ({ ...pipeline, revision: 3, currentStage: 'eval' as const }))
  backend.deriveAuthoringPipeline = vi.fn(async () => ({ ...designPipeline('continued', pipeline.workspaceId), derivedFromPipelineId: pipeline.id }))
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={changed} />)
  await waitFor(() => expect(screen.getByRole('button', { name: 'Criar continuação para editar' })).toBeEnabled())
  await userEvent.click(screen.getByRole('button', { name: 'Criar continuação para editar' }))
  expect(backend.deriveAuthoringPipeline).toHaveBeenCalledWith(expect.objectContaining({ expectedRevision: 3, parentPipelineId: pipeline.id }))
})

it('preserves an open manual edit when the top bar requests another document', async () => {
  const { backend, pipeline } = setup(true), view = render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} requestedStage="spec" viewRequestId={1} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Editar SPEC' }))
  await userEvent.clear(screen.getByLabelText('Texto de SPEC')); await userEvent.type(screen.getByLabelText('Texto de SPEC'), 'Minha edição ainda não salva.')
  view.rerender(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} requestedStage="plan" viewRequestId={2} />)
  expect(screen.getByLabelText('Texto de SPEC')).toHaveValue('Minha edição ainda não salva.')
  expect(screen.getByRole('tab', { name: 'SPEC', selected: true })).toBeInTheDocument()
  expect(screen.getByText('Salve ou cancele a edição antes de trocar o documento.')).toBeInTheDocument()
})

it('reopens the manually edited Plan after navigation and keeps approval blocked until saving or cancelling', async () => {
  const { backend, pipeline } = setup(true), user = userEvent.setup()
  const view = render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  await user.click(await screen.findByRole('tab', { name: 'Plan' }))
  await user.click(screen.getByRole('button', { name: 'Editar Plan' }))
  await user.clear(screen.getByLabelText('Texto de Plan')); await user.type(screen.getByLabelText('Texto de Plan'), 'Meu Plan ainda não salvo.')
  view.unmount()
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={() => undefined} />)
  expect(await screen.findByLabelText('Texto de Plan')).toHaveValue('Meu Plan ainda não salvo.')
  expect(screen.getByRole('tab', { name: 'Plan', selected: true })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Aprovar documentos e continuar para Code' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Cancelar edição' }))
  expect(screen.getByRole('button', { name: 'Aprovar documentos e continuar para Code' })).toBeEnabled()
})

it('confirms the published pipeline again by itself when a stale confirmation would fence every action', async () => {
  const { backend, pipeline, fixture } = setup(true)
  const published = fixture.read()
  published.currentPipelineRevision = pipeline.revision + 1
  fixture.writeDesign(published)
  const latest = { ...pipeline, revision: pipeline.revision + 1 }
  backend.openPipelineDesign = vi.fn(async () => published)
  // The first confirmation races with the preparation's end and still reads the older revision.
  backend.getPipeline = vi.fn().mockResolvedValueOnce(pipeline).mockResolvedValue(latest)
  const changed = vi.fn()
  render(<PipelineDesignStudio backend={backend} pipeline={pipeline} onPipelineChange={changed} />)
  await userEvent.type(await screen.findByLabelText('Pedido para a IA'), 'deixe com visual futurista')
  await waitFor(() => expect(screen.getByRole('button', { name: 'Enviar pedido' })).toBeEnabled())
  expect(screen.getByRole('button', { name: 'Aprovar documentos e continuar para Code' })).toBeEnabled()
  expect(changed).toHaveBeenCalledWith(latest)
})
