import type { AuthoringStage, AuthoringStageModelPreference, Backend, Brainstorm, GenerateAuthoringStageInput, Pipeline, SaveAuthoringStageModelPreferenceInput } from '../lib/backend'

/** Browser-only synthetic backend: never calls a provider. */
export function installAuthoringStageFixture(backend: Backend, pipeline: () => Pipeline, savePipeline: (value: Pipeline) => Pipeline, brainstorm: () => Brainstorm) {
  const preferenceKey = (pipelineId: string, stage: 'spec' | 'plan') => `harflex:synthetic-model-preference:${pipelineId}:${stage}`
  const defaultPreference = (pipelineId: string, stage: 'spec' | 'plan'): AuthoringStageModelPreference => {
    const selection = stage === 'plan' ? {
      ...brainstorm().selection, reasoningEffort: '', source: 'global_default', status: 'unverified_default', maxOutputTokens: 4096, contextLength: 0,
    } : { ...brainstorm().selection }
    return {
      pipelineId, stage, modelMode: 'inherit', effortMode: stage === 'plan' ? 'automatic' : 'inherit', explicitEffort: '', preferenceRevision: 0, resolution: 'ready',
      modelSource: stage === 'plan' ? 'global_default' : 'brainstorm', effortSource: stage === 'plan' ? 'automatic' : 'brainstorm', inheritedFrom: stage === 'plan' ? 'global_default' : 'brainstorm',
      catalogValidationRequired: stage === 'plan', errorCode: '', selection,
    }
  }
  backend.getAuthoringStageModelPreference = async input => {
    const saved = sessionStorage.getItem(preferenceKey(input.pipelineId, input.stage))
    return saved ? JSON.parse(saved) as AuthoringStageModelPreference : defaultPreference(input.pipelineId, input.stage)
  }
  backend.saveAuthoringStageModelPreference = async (input: SaveAuthoringStageModelPreferenceInput) => {
    const prior = await backend.getAuthoringStageModelPreference({ pipelineId: input.pipelineId, stage: input.stage })
    if (prior.preferenceRevision !== input.expectedRevision) throw Object.assign(new Error('Phase preference changed'), { cause: { code: 'pipeline_conflict' } })
    const inherited = prior.modelMode === 'inherit' ? prior.selection : brainstorm().selection
    const selection = input.modelMode === 'override' ? {
      backendId: input.selection.profileId, modelId: input.selection.modelId,
      reasoningEffort: input.effortMode === 'explicit' ? input.explicitEffort : input.effortMode === 'automatic' ? '' : inherited.reasoningEffort,
      catalogRevision: input.selection.catalogRevision, source: input.selection.source, destination: input.selection.destination,
      status: input.selection.source === 'openrouter_general_unfiltered' ? 'listed_unfiltered' : 'listed',
      confirmUnfiltered: input.selection.confirmUnfiltered, confirmJitLoad: false,
      maxOutputTokens: 4096, contextLength: 8192, checkedAt: input.selection.checkedAt,
    } : {
      ...prior.selection, reasoningEffort: input.effortMode === 'explicit' ? input.explicitEffort : input.effortMode === 'automatic' ? '' : prior.selection.reasoningEffort,
    }
    const saved: AuthoringStageModelPreference = {
      ...prior, modelMode: input.modelMode, effortMode: input.effortMode, explicitEffort: input.explicitEffort,
      preferenceRevision: prior.preferenceRevision + 1, resolution: 'ready',
      modelSource: input.modelMode === 'override' ? 'phase_override' : prior.modelSource,
      effortSource: input.effortMode === 'explicit' ? 'phase_override' : input.effortMode === 'automatic' ? 'automatic' : prior.effortSource,
      inheritedFrom: input.modelMode === 'override' && input.effortMode === 'inherit' ? prior.modelSource : input.modelMode === 'inherit' || input.effortMode === 'inherit' ? prior.inheritedFrom : '',
      catalogValidationRequired: false, selection,
    }
    sessionStorage.setItem(preferenceKey(input.pipelineId, input.stage), JSON.stringify(saved))
    return saved
  }
  const read = (stage: 'spec' | 'plan'): AuthoringStage => {
    const saved = sessionStorage.getItem(`harflex:synthetic-stage:${pipeline().id}:${stage}`)
    if (saved) return { ...JSON.parse(saved), pipelineRevision: pipeline().revision }
    return { pipelineId: pipeline().id, stage, pipelineRevision: pipeline().revision, discoveryVersion: 1, revision: 0, state: pipeline().currentStage === stage ? 'ready' : 'pending', cancellationPending: false, cancellationAttemptId: '', artifactVersion: 0, attemptCount: 0, inputBudgetRemaining: 1572864, outputBudgetRemaining: 24576, attempts: [], artifacts: [], actions: [], createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }
  }
  const save = (value: AuthoringStage) => { sessionStorage.setItem(`harflex:synthetic-stage:${pipeline().id}:${value.stage}`, JSON.stringify(value)); return value }
  backend.getAuthoringStage = async input => read(input.stage)
  const generate = async (input: GenerateAuthoringStageInput) => {
    const previous = read(input.ref.stage), version = previous.artifactVersion + 1, date = new Date().toISOString()
    const id = `attempt-${input.ref.stage}-${version}`, sessionId = `session-${input.ref.stage}-${version}`
    const content = input.ref.stage === 'spec'
      ? { summary: 'Exportar faturas em CSV com os filtros confirmados.', requirements: ['Preservar os filtros aplicados pela equipe financeira.', 'Registrar datas no formato ISO 8601.'], nonGoals: ['Não alterar o processamento financeiro.'], acceptanceCriteria: [{ id: 'AC1', criterion: 'O CSV inclui somente as faturas filtradas.' }, { id: 'AC2', criterion: 'As datas mantêm o formato aprovado.' }] }
      : { summary: 'Implementar a exportação com testes dos critérios aprovados.', tasks: [{ id: 'T1', title: 'Implementar exportação', files: ['src/exportacao.ts'], steps: ['Usar os filtros confirmados.', 'Serializar as datas no formato aprovado.'], tests: ['Validar AC1 e AC2 com dados sintéticos.'], dependsOn: [] }], risks: ['Arquivos grandes precisam manter uso de memória limitado.'] }
    const preference = await backend.getAuthoringStageModelPreference({ pipelineId: input.ref.pipelineId, stage: input.ref.stage })
    const attempt: AuthoringStage['attempts'][number] = { id, requestId: input.ref.requestId, artifactVersion: version, status: 'completed', sessionId, errorCode: '', cancellationState: '', source: { discoveryVersion: 1, discoveryHash: 'synthetic-discovery-hash', brainstormRunId: brainstorm().id, synthesisVersion: 1, discoveryBypassReason: '', specVersion: input.ref.stage === 'plan' ? 2 : 0, specHash: input.ref.stage === 'plan' ? 'synthetic-spec-hash' : '', specBypassReason: '', previousArtifactVersion: previous.artifactVersion, feedback: input.feedback }, selection: { ...preference.selection, confirmUnfiltered: preference.selection.confirmUnfiltered || input.confirmUnfiltered, confirmJitLoad: input.confirmJitLoad, maxOutputTokens: input.selection.maxOutputTokens, checkedAt: preference.selection.checkedAt }, reservedInputTokens: 1000, reservedOutputTokens: input.selection.maxOutputTokens, usage: null, createdAt: date, updatedAt: date }
    const next: AuthoringStage = { ...previous, revision: previous.revision + 2, state: 'waiting_user', artifactVersion: version, attemptCount: previous.attemptCount + 1, attempts: [...previous.attempts, attempt], artifacts: [...previous.artifacts.map(item => ({ ...item, status: 'rejected' as const })), { version, content, contentHash: `synthetic-hash-${version}`, author: 'ai', attemptId: id, sourceSessionId: sessionId, status: 'waiting_user', createdAt: date, updatedAt: date }], inputBudgetRemaining: previous.inputBudgetRemaining - 1000, outputBudgetRemaining: previous.outputBudgetRemaining - 4096 }
    savePipeline({ ...pipeline(), revision: pipeline().revision + 1, stageStatus: { ...pipeline().stageStatus, [input.ref.stage]: 'waiting_user' } })
    return save({ ...next, pipelineRevision: pipeline().revision })
  }
  backend.generateAuthoringStage = generate
  backend.requestAuthoringStageRevision = generate
  backend.approveAuthoringStage = async input => {
    const previous = read(input.ref.stage)
    const artifact = previous.artifacts.find(item => item.version === input.ref.artifactVersion)!
    const nextStage = input.ref.stage === 'spec' ? 'plan' : 'code'
    savePipeline({ ...pipeline(), revision: pipeline().revision + 1, currentStage: nextStage, stageStatus: { ...pipeline().stageStatus, [input.ref.stage]: 'completed', [nextStage]: 'active' }, artifacts: { ...pipeline().artifacts, [input.ref.stage]: { stage: input.ref.stage, version: artifact.version, content: JSON.stringify(artifact.content), author: 'ai', sourceSessionId: artifact.sourceSessionId, updatedAt: artifact.updatedAt } } })
    return save({ ...previous, pipelineRevision: pipeline().revision, revision: previous.revision + 1, state: 'approved', artifacts: previous.artifacts.map(item => item.version === artifact.version ? { ...item, status: 'approved' } : item) })
  }
}
