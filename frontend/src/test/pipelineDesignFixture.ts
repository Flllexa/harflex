import type { Backend, Pipeline, PipelineDesign, PipelineDesignDocument, PipelineDesignStage, PreparePipelineDesignInput } from '../lib/backend'

export const designFixtureKey = 'harflex:conversational-design'
export const designStages = ['discovery', 'spec', 'plan'] as const
const now = () => new Date().toISOString()
const clone = <T,>(value: T): T => JSON.parse(JSON.stringify(value)) as T
// Synthetic provenance for browser fixtures; provider and native hashes are tested by Go.
const digest = (content: string) => { let result = 2166136261; for (const char of content) result = Math.imul(result ^ char.charCodeAt(0), 16777619); return (result >>> 0).toString(16).padStart(8, '0').repeat(8) }

export function designPipeline(id = 'design-1', workspaceId = 'workspace-1'): Pipeline {
  return { id, workspaceId, kind: 'ai_authoring', preparationExperience: 'conversational', title: 'Exportar faturas em CSV', objective: 'Exportar faturas com filtros e datas legíveis.', currentStage: 'discovery', revision: 1,
    stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' }, artifacts: { discovery: { stage: 'discovery', version: 1, content: '# Discovery\n\nExportar faturas em CSV com filtros.', author: 'user', updatedAt: now() } }, createdAt: now(), updatedAt: now() }
}

export function designReadback(pipeline = designPipeline(), complete = false): PipelineDesign {
  const documents = Object.fromEntries(designStages.map(stage => {
    const content = stage === 'discovery' ? pipeline.artifacts.discovery?.content ?? '' : complete ? stage === 'spec' ? '# SPEC\n\n## Critérios de aceite\n\n- Exportar faturas filtradas em CSV.' : '# Plan\n\n## Implementação\n\n1. Criar exportação.\n2. Verificar filtros.' : ''
    return [stage, { stage, version: content ? 1 : 0, content, contentDigest: content ? digest(content) : '', author: stage === 'discovery' ? 'user' : 'ai', sourceSessionId: content && stage !== 'discovery' ? `session-${stage}` : '', sourceDigest: '', selection: null, stale: false, updatedAt: now() } satisfies PipelineDesignDocument]
  }))
  return { pipelineId: pipeline.id, workspaceId: pipeline.workspaceId, pipelineRevision: pipeline.revision, currentPipelineRevision: pipeline.revision, revision: 1, state: 'ready', phase: '', activeAttemptId: '', needsDerivation: false, documents,
    versions: Object.fromEntries(designStages.map(stage => [stage, documents[stage].version ? [{ ...documents[stage], reason: 'imported', restoredFromVersion: 0, createdAt: now() }] : []])), messages: [], attempts: [], createdAt: now(), updatedAt: now() }
}

type Saved = { pipelines: Pipeline[]; designs: Record<string, PipelineDesign>; prepareCount: number }
type Persistence = Pick<Storage, 'getItem' | 'setItem'>
export function installPipelineDesignFixture(backend: Backend, persistence?: Persistence, seed?: { pipeline: Pipeline; design?: PipelineDesign }) {
  const stored = persistence?.getItem(designFixtureKey)
  const saved: Saved = stored ? JSON.parse(stored) : { pipelines: seed ? [clone(seed.pipeline)] : [], designs: seed ? { [seed.pipeline.id]: clone(seed.design ?? designReadback(seed.pipeline)) } : {}, prepareCount: 0 }
  const persist = () => persistence?.setItem(designFixtureKey, JSON.stringify(saved))
  const read = (id = saved.pipelines[0]?.id) => { const value = saved.designs[id]; if (!value) throw new Error('Documentos ausentes'); return clone(value) }
  const pipeline = (id = saved.pipelines[0]?.id) => { const value = saved.pipelines.find(item => item.id === id); if (!value) throw new Error('Trabalho ausente'); return clone(value) }
  const check = (ref: PreparePipelineDesignInput['ref']) => { const value = saved.designs[ref.pipelineId]; if (!value || value.revision !== ref.designRevision || value.currentPipelineRevision !== ref.pipelineRevision || value.needsDerivation || ['running', 'cancellation_pending'].includes(value.state)) throw new Error('Atualize os documentos antes de continuar.'); return value }
  function publish(value: PipelineDesign, stage: PipelineDesignStage, content: string, author: string, reason: string, restoredFromVersion = 0) {
    const previous = value.documents[stage]
    const next: PipelineDesignDocument = { ...previous, version: previous.version + 1, content, contentDigest: digest(content), author, stale: false, sourceSessionId: author === 'ai' ? `synthetic-${stage}-${previous.version + 1}` : '', updatedAt: now() }
    value.documents[stage] = next
    value.versions[stage] = [...(value.versions[stage] ?? []), { ...next, reason, restoredFromVersion, createdAt: now() }]
    value.updatedAt = now()
  }
  backend.listBackends = async () => [{ id: 'api-1', name: 'API de teste', kind: 'api', available: true }, { id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
  backend.getSettings = async () => ({ defaultBackendId: 'api-1', defaultModelBackendId: 'api-1', defaultModelId: 'test-model' })
  backend.listProviderProfiles = async () => [{ id: 'api-1', name: 'API de teste', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://example.test/v1', model: 'test-model', hasCredential: true, endpointBlocked: false, updatedAt: now() }]
  backend.queryHTTPModelCatalog = async query => ({ backendId: query.profileId, source: 'openai_models', destination: 'https://example.test:443', profileRevision: 'fixture-revision', credentialToken: 'a'.repeat(64), searchTerm: '', models: ['test-model', 'alternative-model'].map(id => ({ id, displayName: id === 'test-model' ? 'Modelo de teste' : 'Modelo alternativo', backendId: query.profileId, source: 'openai_models', availability: 'available', contextLength: 128000 })), nextCursor: '', checkedAt: now(), status: 'complete', complete: true, accountFiltered: true })
  backend.queryCLIModelCatalog = async query => ({ backendId: query.backendId, source: 'codex_app_server', destination: '', profileRevision: 'fixture-codex', searchTerm: '', models: [{ id: 'codex-test', displayName: 'Modelo Codex de teste', backendId: query.backendId, source: 'codex_app_server', availability: 'listed' }], nextCursor: '', checkedAt: now(), status: 'complete', complete: true, accountFiltered: true })
  backend.listPipelines = async workspaceId => clone(saved.pipelines.filter(item => item.workspaceId === workspaceId))
  backend.getPipeline = async id => pipeline(id)
  backend.createAuthoringPipeline = async input => {
    const existing = saved.pipelines.find(item => item.id === `design-${input.requestId}`)
    if (existing) return clone(existing)
    const created = { ...designPipeline(`design-${input.requestId}`, input.workspaceId), title: input.discovery.split('\n')[0].slice(0, 80), objective: input.discovery, artifacts: { discovery: { stage: 'discovery' as const, version: 1, content: input.discovery, author: 'user' as const, updatedAt: now() } } }
    saved.pipelines.unshift(created); saved.designs[created.id] = designReadback(created); persist(); return clone(created)
  }
  backend.openPipelineDesign = async id => read(id)
  backend.preparePipelineDesign = async input => {
    const existing = saved.designs[input.ref.pipelineId]?.attempts.find(item => item.requestId === input.ref.requestId)
    if (existing) return read(input.ref.pipelineId)
    const value = check(input.ref)
    const stages = input.target === 'plan' ? ['plan'] as const : input.target === 'spec' || input.target === 'all' && !value.documents.spec.content && !value.documents.plan.content ? ['spec', 'plan'] as const : designStages
    const attempt = { id: `attempt-${input.ref.requestId}`, requestId: input.ref.requestId, target: input.target, status: 'running', phase: stages[0], selections: {}, sessionIds: {}, errorCode: '', createdAt: now(), updatedAt: now() }
    value.state = 'running'; value.activeAttemptId = attempt.id; value.phase = stages[0]; value.revision++; value.attempts.push(attempt); saved.prepareCount++
    if (input.message.trim()) value.messages.push({ id: `message-${input.ref.requestId}`, role: 'user', content: input.message, target: input.target, attemptId: attempt.id, createdAt: now() })
    persist()
    await new Promise(resolve => setTimeout(resolve, 120))
    if (attempt.status !== 'running') return read(value.pipelineId)
    for (const stage of stages) {
      const content = stage === 'discovery' ? `# Discovery\n\n${value.documents.discovery.content}\n\n${input.message}` : stage === 'spec' ? `# SPEC\n\n## Escopo\n\nExportar faturas em CSV.\n\n## Critérios de aceite\n\n- Respeitar filtros e usar datas ISO.\n${input.message ? `- ${input.message}` : ''}` : `# Plan\n\n## Implementação\n\n1. Criar exportação com filtros.\n2. Verificar o formato das datas.\n${input.message ? `3. ${input.message}` : ''}`
      publish(value, stage, content, 'ai', 'generated')
    }
    value.messages.push({ id: `reply-${input.ref.requestId}`, role: 'assistant', content: `${stages.map(stage => stage === 'spec' ? 'SPEC' : stage === 'plan' ? 'Plan' : 'Discovery').join(' e ')} preparados para revisão.`, target: input.target, attemptId: attempt.id, createdAt: now() })
    attempt.status = 'completed'; attempt.updatedAt = now(); value.state = 'ready'; value.phase = ''; value.activeAttemptId = ''; value.revision++; persist(); return read(value.pipelineId)
  }
  backend.editPipelineDesignDocument = async input => {
    const value = check(input.ref)
    if (!input.content.trim()) throw new Error('O documento não pode ficar vazio.')
    publish(value, input.stage, input.content, 'user', 'manual')
    for (const stage of designStages.slice(designStages.indexOf(input.stage) + 1)) value.documents[stage].stale = true
    value.revision++; persist(); return read(value.pipelineId)
  }
  backend.restorePipelineDesignDocument = async input => {
    const value = check(input.ref), prior = value.versions[input.stage]?.find(item => item.version === input.version)
    if (!prior) throw new Error('Versão ausente')
    publish(value, input.stage, prior.content, 'user', 'restored', prior.version)
    for (const stage of designStages.slice(designStages.indexOf(input.stage) + 1)) value.documents[stage].stale = true
    value.revision++; persist(); return read(value.pipelineId)
  }
  backend.cancelPipelineDesign = async input => {
    const value = saved.designs[input.pipelineId], attempt = value?.attempts.find(item => item.id === input.attemptId)
    if (!value || !attempt) throw new Error('Tentativa ausente')
    attempt.status = 'cancelled'; value.state = 'paused'; value.phase = ''; value.activeAttemptId = ''; value.revision++; persist(); return read(value.pipelineId)
  }
  backend.approvePipelineDesign = async input => {
    const value = check(input.ref)
    if (designStages.some(stage => !value.documents[stage].content.trim() || value.documents[stage].stale || input.digests[stage] !== value.documents[stage].contentDigest)) throw new Error('Documentos incompletos ou desatualizados')
    const parent = saved.pipelines.find(item => item.id === value.pipelineId)!
    for (const stage of designStages) { const document = value.documents[stage]; parent.artifacts[stage] = { stage, version: document.version, content: document.content, author: document.author === 'ai' ? 'ai' : 'user', contentDigest: document.contentDigest, sourceSessionId: document.sourceSessionId, updatedAt: document.updatedAt }; parent.stageStatus[stage] = 'completed' }
    parent.currentStage = 'code'; parent.stageStatus.code = 'active'; parent.revision++; parent.discoveryFrozenVersion = value.documents.discovery.version
    value.needsDerivation = true; value.state = 'approved'; value.currentPipelineRevision = parent.revision; value.revision++; persist(); return clone(parent)
  }
  backend.deriveAuthoringPipeline = async input => {
    const parent = pipeline(input.parentPipelineId)
    if (parent.revision !== input.expectedRevision) throw new Error('Trabalho mudou')
    const created = await backend.createAuthoringPipeline({ workspaceId: parent.workspaceId, requestId: input.requestId, discovery: input.discovery })
    const next = saved.pipelines.find(item => item.id === created.id)!, design = saved.designs[next.id]
    next.derivedFromPipelineId = parent.id
    for (const stage of ['spec', 'plan'] as const) { const source = read(parent.id).documents[stage]; if (source.content) publish(design, stage, source.content, source.author, 'derived') }
    persist(); return clone(next)
  }
  backend.previewPipelineCodeWorkspace = async () => ({ isGit: true, fileCount: 5, totalBytes: 2048, excludedPaths: [], unsafePaths: [] })
  persist()
  return { read, pipeline, writeDesign: (value: PipelineDesign) => { saved.designs[value.pipelineId] = clone(value); persist() }, prepareCount: () => saved.prepareCount }
}
