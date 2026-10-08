import { afterEach, describe, expect, it, vi } from 'vitest'
import { Events } from '@wailsio/runtime'
import { Service } from '../../bindings/github.com/persioflexa/harflex/internal/application'
import { wailsBackend } from './wailsBackend'
import { NO_CATALOG_TIME, errorMessage, type AgentEvent } from './backend'
import { createSessionStore } from '../state/session'
import { sessionMetadata } from '../test/fakeBackend'
import { designReadback } from '../test/pipelineDesignFixture'

vi.mock('@wailsio/runtime', () => ({ Events: { On: vi.fn(() => vi.fn()) }, Dialogs: {} }))
vi.mock('../../bindings/github.com/persioflexa/harflex/internal/application', () => ({ Service: {
  ListSessions: vi.fn(), OpenSession: vi.fn(), ExportAudit: vi.fn(), QueryHTTPModelCatalog: vi.fn(), QueryCLIModelCatalog: vi.fn(), CreateDirectSession: vi.fn(), GetSessionModelSelection: vi.fn(),
  GetOpenRouterManagementKeyStatus: vi.fn(), SaveOpenRouterManagementKey: vi.fn(), ClearOpenRouterManagementKey: vi.fn(),
  CreateAuthoringPipeline: vi.fn(), DeriveAuthoringPipeline: vi.fn(), ReviseAuthoringDiscovery: vi.fn(),
  GetAuthoringStage: vi.fn(), GetAuthoringStageModelPreference: vi.fn(), SaveAuthoringStageModelPreference: vi.fn(), GenerateAuthoringStage: vi.fn(), RequestAuthoringStageRevision: vi.fn(), ApproveAuthoringStage: vi.fn(), SkipAuthoringStage: vi.fn(), CancelAuthoringStage: vi.fn(),
  PreflightAuthoringCode: vi.fn(), PrepareAuthoringCode: vi.fn(), GetAuthoringCodeCopyPreparations: vi.fn(),
  StartAuthoringCode: vi.fn(), CancelAuthoringCode: vi.fn(), GetAuthoringCodeRuns: vi.fn(), GetAuthoringCodeRunPatch: vi.fn(),
  OpenPipelineDesign: vi.fn(), PreparePipelineDesign: vi.fn(), EditPipelineDesignDocument: vi.fn(), RestorePipelineDesignDocument: vi.fn(), CancelPipelineDesign: vi.fn(), ApprovePipelineDesign: vi.fn(),
  CreatePipelineSession: vi.fn(),
  DecidePipelineExecutionArtifact: vi.fn(),
  ReopenPipelineCodeReview: vi.fn(),
  StartBrainstorming: vi.fn(), GetBrainstorming: vi.fn(), ListBrainstorming: vi.fn(), AnswerBrainstormQuestion: vi.fn(), GenerateBrainstormQuestion: vi.fn(), QuestionsSufficient: vi.fn(), FinishAndGenerateSynthesis: vi.fn(), ApproveBrainstormSynthesis: vi.fn(), RequestBrainstormRevision: vi.fn(), SkipBrainstormQuestions: vi.fn(), ConfirmDiscoveryAfterSkip: vi.fn(), CancelBrainstormAttempt: vi.fn(), ResumePausedBrainstorm: vi.fn(),
} }))

afterEach(() => vi.clearAllMocks())

const valid: AgentEvent = { id: 'event-1', streamId: 'session-1', sequence: 1, type: 'assistant.delta', data: { delta: 'Olá' }, createdAt: '2026-09-25T10:00:00Z' }

const authoringPipeline = {
  id: 'pipeline-1', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 0,
  title: 'Solicitação', objective: 'Objetivo', currentStage: 'discovery', stageStatus: { discovery: 'active' }, revision: 3,
  artifacts: { discovery: { stage: 'discovery', version: 2, content: '# Discovery', author: 'user', sourceSessionId: '', updatedAt: valid.createdAt } },
  createdAt: valid.createdAt, updatedAt: valid.createdAt,
}
const brainstormDTO = { id: 'brainstorm-1', pipelineId: 'pipeline-1', pipelineRevision: 4, discoveryVersion: 2, discoveryContent: '# Discovery', selection: { backendId: 'api-1', modelId: 'model-1', reasoningEffort: '', catalogRevision: 'revision', source: 'openai_models', destination: 'API', status: 'listed', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 256, contextLength: 8192, checkedAt: valid.createdAt }, state: 'ready', revision: 1, questionCount: 0, currentQuestionId: '', synthesisVersion: 0, attemptCount: 0, inputBudgetRemaining: 30000, outputBudgetRemaining: 30000, activeDurationMillis: 0, createdAt: valid.createdAt, updatedAt: valid.createdAt, attempts: [], turns: [], syntheses: [] }
const stagePreferenceDTO = { pipelineId: 'pipeline-1', stage: 'plan', modelMode: 'override', effortMode: 'explicit', explicitEffort: 'high', preferenceRevision: 7, resolution: 'ready', modelSource: 'phase_override', effortSource: 'phase_override', inheritedFrom: '', catalogValidationRequired: false, errorCode: '', selection: { backendId: 'api-1', modelId: 'model-1', reasoningEffort: 'high', catalogRevision: 'revision', source: 'openai_models', destination: 'https://api.example.test:443', status: 'listed', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096, contextLength: 8192, checkedAt: valid.createdAt } }
const codeManifestDTO = { version: 1, entries: [], excluded: [], fileCount: 0, totalBytes: 0, hash: 'a'.repeat(64) }
const codeBaselineDTO = { ...codeManifestDTO, hash: 'c'.repeat(64) }
const codeResultDTO = { ...codeManifestDTO, hash: 'd'.repeat(64) }
const authoringCodeRunDTO = {
  id: 'attempt-code-1', pipelineId: 'pipeline-1', preparationId: 'preparation-1', preparationRequestId: 'prepare-request-1', requestId: 'code-request-1',
  pipelineRevision: 5, planStageRevision: 2, planArtifactVersion: 1, codePreferenceRevision: 3,
  codeSelectionHash: 'b'.repeat(64), manifestHash: 'c'.repeat(64), planSourceHash: 'd'.repeat(64), privatePath: '/private/code-copy',
  preference: { pipelineId: 'pipeline-1', stage: 'code', modelMode: 'inherit', effortMode: 'inherit', explicitEffort: '', preferenceRevision: 3, resolution: 'ready', modelSource: 'global_default', effortSource: 'global_default', inheritedFrom: 'global_default', catalogValidationRequired: false, errorCode: '', selection: { backendId: 'api-1', modelId: 'model-1', reasoningEffort: '', supportedReasoningEfforts: null, catalogRevision: 'catalog-1', source: 'openai_models', destination: 'https://api.example.test:443', status: 'listed', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096, contextLength: 8192, checkedAt: valid.createdAt } },
  limits: { maxPromptBytes: 16384, maxOutputTokens: 4096, maxOutputTokensPerTurn: 2048, maxTurns: 20, maxToolCalls: 50, timeoutMillis: 300000 },
  sessionId: 'session-code-1', status: 'completed', cancellationPending: false, usage: null, baseline: codeBaselineDTO, result: codeResultDTO,
  sourceManifest: codeBaselineDTO,
  sourceDrift: false, sourceReadbackStatus: 'matched', changes: [], patchHash: 'e'.repeat(64), createdAt: valid.createdAt, updatedAt: valid.createdAt,
}
const authoringCodePreflightDTO = {
  pipelineId: 'pipeline-1', workspaceId: 'workspace-1', pipelineRevision: 5, codePreferenceRevision: 3,
  codePreference: authoringCodeRunDTO.preference, sourcePath: '/source/project', privateParentPath: '/private',
  manifest: codeBaselineDTO, manifestHash: 'c'.repeat(64), codeSelectionHash: 'b'.repeat(64),
}
const authoringCodePreparationDTO = {
  id: 'preparation-1', pipelineId: 'pipeline-1', workspaceId: 'workspace-1', requestId: 'prepare-request-1',
  pipelineRevision: 5, codePreferenceRevision: 3, codeSelectionHash: 'b'.repeat(64), codePreference: authoringCodeRunDTO.preference,
  sourcePath: '/source/project', privatePath: '/private/code-copy', manifest: codeBaselineDTO, manifestHash: 'c'.repeat(64),
  status: 'prepared', createdAt: valid.createdAt, updatedAt: valid.createdAt,
}
const authoringCodeCopyPreparationsDTO = { pipelineId: 'pipeline-1', attemptCount: 1, maxAttemptCount: 3, attempts: [authoringCodePreparationDTO] }
const authoringCodeRunPatchPageDTO = {
  pipelineId: 'pipeline-1', runId: 'attempt-code-1', baselineHash: 'c'.repeat(64), sourceManifestHash: 'c'.repeat(64),
  resultManifestHash: 'd'.repeat(64), patchHash: 'e'.repeat(64), cursor: 0, nextCursor: 19, totalBytes: 37,
  content: '{"version":1,"patch', sourceReadbackStatus: 'matched',
}

function subscribe(listener: (event: AgentEvent) => void) {
  const unsubscribe = wailsBackend.onEvent(listener)
  const calls = vi.mocked(Events.On).mock.calls
  const [name, receive] = calls[calls.length - 1]
  expect(name).toBe('harflex:event')
  return { unsubscribe, emit: (data: unknown) => receive({ name, data }) }
}

describe('Wails event boundary', () => {
  it('exposes and delegates Code preflight, copy preparation, preparation history, and paged patch readback', async () => {
    const backend = wailsBackend as unknown as Record<string, unknown>
    const invoke = (name: string, input: unknown) => {
      const method = backend[name]
      if (typeof method !== 'function') throw new Error(`${name} is unavailable`)
      return (method as (value: unknown) => Promise<unknown>)(input)
    }
    const preflightInput = { pipelineId: 'pipeline-1', expectedPipelineRevision: 5, expectedCodePreferenceRevision: 3 }
    const prepareInput = { pipelineId: 'pipeline-1', requestId: 'prepare-request-1', expectedPipelineRevision: 5, expectedCodePreferenceRevision: 3, expectedCodeSelectionHash: 'b'.repeat(64), expectedManifestHash: 'c'.repeat(64), confirmCopy: true }
    const preparationsInput = { pipelineId: 'pipeline-1' }
    const patchInput = { pipelineId: 'pipeline-1', runId: 'attempt-code-1', cursor: 0 }
    vi.mocked(Service.PreflightAuthoringCode).mockResolvedValue(authoringCodePreflightDTO as never)
    vi.mocked(Service.PrepareAuthoringCode).mockResolvedValue(authoringCodePreparationDTO as never)
    vi.mocked(Service.GetAuthoringCodeCopyPreparations).mockResolvedValue(authoringCodeCopyPreparationsDTO as never)
    vi.mocked(Service.GetAuthoringCodeRunPatch).mockResolvedValue(authoringCodeRunPatchPageDTO as never)

    expect(await invoke('preflightAuthoringCode', preflightInput)).toEqual(authoringCodePreflightDTO)
    expect(Service.PreflightAuthoringCode).toHaveBeenCalledWith(preflightInput)
    expect(await invoke('prepareAuthoringCode', prepareInput)).toEqual(authoringCodePreparationDTO)
    expect(Service.PrepareAuthoringCode).toHaveBeenCalledWith(prepareInput)
    expect(await invoke('getAuthoringCodeCopyPreparations', preparationsInput)).toEqual(authoringCodeCopyPreparationsDTO)
    expect(Service.GetAuthoringCodeCopyPreparations).toHaveBeenCalledWith(preparationsInput)
    expect(await invoke('getAuthoringCodeRunPatch', patchInput)).toEqual(authoringCodeRunPatchPageDTO)
    expect(Service.GetAuthoringCodeRunPatch).toHaveBeenCalledWith(patchInput)
  })

  it('preserves matched, drifted, unavailable, and unchecked patch readback states', async () => {
    vi.mocked(Service.GetAuthoringCodeRunPatch).mockResolvedValue({
      ...authoringCodeRunPatchPageDTO, sourceReadbackStatus: 'drifted',
    } as never)
    const drifted = await wailsBackend.getAuthoringCodeRunPatch({ pipelineId: 'pipeline-1', runId: 'attempt-code-1', cursor: 0 })
    expect(drifted.sourceReadbackStatus).toBe('drifted')

    vi.mocked(Service.GetAuthoringCodeRunPatch).mockResolvedValue({
      ...authoringCodeRunPatchPageDTO, sourceReadbackStatus: 'unavailable',
    } as never)
    const unavailable = await wailsBackend.getAuthoringCodeRunPatch({ pipelineId: 'pipeline-1', runId: 'attempt-code-1', cursor: 0 })
    expect(unavailable.sourceReadbackStatus).toBe('unavailable')

    vi.mocked(Service.GetAuthoringCodeRunPatch).mockResolvedValue({
      ...authoringCodeRunPatchPageDTO, sourceReadbackStatus: 'unchecked', cursor: 19,
      nextCursor: null, totalBytes: 37, content: 'x'.repeat(18),
    } as never)
    const unchecked = await wailsBackend.getAuthoringCodeRunPatch({ pipelineId: 'pipeline-1', runId: 'attempt-code-1', cursor: 19 })
    expect(unchecked.sourceReadbackStatus).toBe('unchecked')

    vi.mocked(Service.GetAuthoringCodeRunPatch).mockResolvedValue({
      ...authoringCodeRunPatchPageDTO, sourceReadbackStatus: 'unchecked',
    } as never)
    await expect(wailsBackend.getAuthoringCodeRunPatch({ pipelineId: 'pipeline-1', runId: 'attempt-code-1', cursor: 0 })).rejects.toThrow()

    vi.mocked(Service.GetAuthoringCodeRunPatch).mockResolvedValue({
      ...authoringCodeRunPatchPageDTO, cursor: 19, sourceReadbackStatus: 'matched',
      nextCursor: null, totalBytes: 37, content: 'x'.repeat(18),
    } as never)
    await expect(wailsBackend.getAuthoringCodeRunPatch({ pipelineId: 'pipeline-1', runId: 'attempt-code-1', cursor: 19 })).rejects.toThrow()
  })

  it('rejects malformed or extended Code preparation and patch page readbacks', async () => {
    const backend = wailsBackend as unknown as Record<string, unknown>
    const invoke = (name: string, input: unknown) => {
      const method = backend[name]
      if (typeof method !== 'function') throw new Error(`${name} is unavailable`)
      return (method as (value: unknown) => Promise<unknown>)(input)
    }
    vi.mocked(Service.PreflightAuthoringCode).mockResolvedValue({ ...authoringCodePreflightDTO, unexpected: 'sensitive' } as never)
    await expect(invoke('preflightAuthoringCode', { pipelineId: 'pipeline-1', expectedPipelineRevision: 5, expectedCodePreferenceRevision: 3 })).rejects.toThrow()
    vi.mocked(Service.GetAuthoringCodeRunPatch).mockResolvedValue({ ...authoringCodeRunPatchPageDTO, patchHash: 'invalid', unexpected: 'sensitive' } as never)
    await expect(invoke('getAuthoringCodeRunPatch', { pipelineId: 'pipeline-1', runId: 'attempt-code-1', cursor: 0 })).rejects.toThrow()
    vi.mocked(Service.GetAuthoringCodeRunPatch).mockResolvedValue({ ...authoringCodeRunPatchPageDTO, runId: 'another-run' } as never)
    await expect(wailsBackend.getAuthoringCodeRunPatch({ pipelineId: 'pipeline-1', runId: 'attempt-code-1', cursor: 0 })).rejects.toThrow()
    vi.mocked(Service.GetAuthoringCodeRunPatch).mockResolvedValue({
      ...authoringCodeRunPatchPageDTO, cursor: 0, nextCursor: null, totalBytes: 262146, content: 'ç'.repeat(131073),
    } as never)
    await expect(wailsBackend.getAuthoringCodeRunPatch({ pipelineId: 'pipeline-1', runId: 'attempt-code-1', cursor: 0 })).rejects.toThrow()
    vi.mocked(Service.GetAuthoringCodeRuns).mockResolvedValue({
      pipelineId: 'pipeline-1', runs: [{ ...authoringCodeRunDTO, result: { ...codeResultDTO, hash: 'invalid' } }],
    } as never)
    await expect(wailsBackend.getAuthoringCodeRuns({ pipelineId: 'pipeline-1' })).rejects.toThrow()
  })

  it('rejects Code readbacks scoped to a different pipeline, request, or revision', async () => {
    const preflightInput = { pipelineId: 'pipeline-1', expectedPipelineRevision: 5, expectedCodePreferenceRevision: 3 }
    const prepareInput = { pipelineId: 'pipeline-1', requestId: 'prepare-request-1', expectedPipelineRevision: 5, expectedCodePreferenceRevision: 3, expectedCodeSelectionHash: 'b'.repeat(64), expectedManifestHash: 'c'.repeat(64), confirmCopy: true }
    vi.mocked(Service.PreflightAuthoringCode).mockResolvedValue({ ...authoringCodePreflightDTO, pipelineId: 'pipeline-other' } as never)
    await expect(wailsBackend.preflightAuthoringCode(preflightInput)).rejects.toThrow()
    vi.mocked(Service.PrepareAuthoringCode).mockResolvedValue({ ...authoringCodePreparationDTO, requestId: 'prepare-request-other' } as never)
    await expect(wailsBackend.prepareAuthoringCode(prepareInput)).rejects.toThrow()
    vi.mocked(Service.GetAuthoringCodeCopyPreparations).mockResolvedValue({ ...authoringCodeCopyPreparationsDTO, pipelineId: 'pipeline-other' } as never)
    await expect(wailsBackend.getAuthoringCodeCopyPreparations({ pipelineId: 'pipeline-1' })).rejects.toThrow()
    vi.mocked(Service.GetAuthoringCodeRuns).mockResolvedValue({ pipelineId: 'pipeline-other', runs: [] } as never)
    await expect(wailsBackend.getAuthoringCodeRuns({ pipelineId: 'pipeline-1' })).rejects.toThrow()
    const startInput = { pipelineId: 'pipeline-1', preparationId: 'preparation-1', preparationRequestId: 'prepare-request-1', requestId: 'code-request-1', expectedPipelineRevision: 5, expectedPlanStageRevision: 2, expectedPlanArtifactVersion: 1, expectedCodePreferenceRevision: 3, expectedCodeSelectionHash: 'b'.repeat(64), expectedManifestHash: 'c'.repeat(64) }
    vi.mocked(Service.StartAuthoringCode).mockResolvedValue({ ...authoringCodeRunDTO, requestId: 'code-request-other' } as never)
    await expect(wailsBackend.startAuthoringCode(startInput)).rejects.toThrow()
    vi.mocked(Service.CancelAuthoringCode).mockResolvedValue({ ...authoringCodeRunDTO, id: 'attempt-other' } as never)
    await expect(wailsBackend.cancelAuthoringCode({ pipelineId: 'pipeline-1', attemptId: 'attempt-code-1' })).rejects.toThrow()
  })

  it('delegates Code start, cancel, and run history with validated DTO readbacks', async () => {
    const start = { pipelineId: 'pipeline-1', preparationId: 'preparation-1', preparationRequestId: 'prepare-request-1', requestId: 'code-request-1', expectedPipelineRevision: 5, expectedPlanStageRevision: 2, expectedPlanArtifactVersion: 1, expectedCodePreferenceRevision: 3, expectedCodeSelectionHash: 'b'.repeat(64), expectedManifestHash: 'c'.repeat(64) }
    const cancel = { pipelineId: 'pipeline-1', attemptId: 'attempt-code-1' }
    const query = { pipelineId: 'pipeline-1' }
    const history = { pipelineId: 'pipeline-1', runs: [authoringCodeRunDTO] }
    vi.mocked(Service.StartAuthoringCode).mockResolvedValue(authoringCodeRunDTO as never)
    vi.mocked(Service.CancelAuthoringCode).mockResolvedValue(authoringCodeRunDTO as never)
    vi.mocked(Service.GetAuthoringCodeRuns).mockResolvedValue(history as never)

    expect(await wailsBackend.startAuthoringCode(start)).toEqual(authoringCodeRunDTO)
    expect(Service.StartAuthoringCode).toHaveBeenCalledWith(start)
    expect(await wailsBackend.cancelAuthoringCode(cancel)).toEqual(authoringCodeRunDTO)
    expect(Service.CancelAuthoringCode).toHaveBeenCalledWith(cancel)
    expect(await wailsBackend.getAuthoringCodeRuns(query)).toEqual(history)
    expect(Service.GetAuthoringCodeRuns).toHaveBeenCalledWith(query)
  })

  it('rejects malformed or extended Code run readbacks', async () => {
    const query = { pipelineId: 'pipeline-1' }
    vi.mocked(Service.GetAuthoringCodeRuns).mockResolvedValue({ pipelineId: 'pipeline-1', runs: [{ ...authoringCodeRunDTO, status: 'unknown' }] } as never)
    await expect(wailsBackend.getAuthoringCodeRuns(query)).rejects.toThrow()
    vi.mocked(Service.GetAuthoringCodeRuns).mockResolvedValue({ pipelineId: 'pipeline-1', runs: [{ ...authoringCodeRunDTO, sourceManifest: { ...codeBaselineDTO, hash: 'd'.repeat(64) } }] } as never)
    await expect(wailsBackend.getAuthoringCodeRuns(query)).rejects.toThrow()
    vi.mocked(Service.GetAuthoringCodeRuns).mockResolvedValue({ pipelineId: 'pipeline-1', runs: [{ ...authoringCodeRunDTO, unexpected: 'sensitive' }] } as never)
    await expect(wailsBackend.getAuthoringCodeRuns(query)).rejects.toThrow()
  })

  it('reads and saves phase preference through the validated adapter with the expected CAS revision', async () => {
    const backend = wailsBackend as unknown as Record<string, unknown>
    const getPreference = backend.getAuthoringStageModelPreference
    const savePreference = backend.saveAuthoringStageModelPreference
    expect(getPreference).toBeTypeOf('function')
    expect(savePreference).toBeTypeOf('function')
    if (typeof getPreference !== 'function' || typeof savePreference !== 'function') return
    vi.mocked(Service.GetAuthoringStageModelPreference).mockResolvedValue(stagePreferenceDTO as never)
    vi.mocked(Service.SaveAuthoringStageModelPreference).mockResolvedValue(stagePreferenceDTO as never)
    const query = { pipelineId: 'pipeline-1', stage: 'plan' as const }
    expect(await (getPreference as (input: typeof query) => Promise<unknown>)(query)).toEqual(stagePreferenceDTO)
    expect(Service.GetAuthoringStageModelPreference).toHaveBeenCalledWith(query)
    const credentialToken = 'a'.repeat(64)
    const save = { ...query, expectedRevision: 7, modelMode: 'override', effortMode: 'explicit', explicitEffort: 'high', selection: { profileId: 'api-1', modelId: 'model-1', catalogRevision: 'revision', source: 'openai_models', destination: 'https://api.example.test:443', checkedAt: valid.createdAt, credentialToken, reasoningEffort: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096 } }
    const saved = await (savePreference as (input: typeof save) => Promise<unknown>)(save)
    expect(Service.SaveAuthoringStageModelPreference).toHaveBeenCalledWith(save)
    expect(JSON.stringify(saved)).not.toContain(credentialToken)
  })

  it('forwards one-attempt generation consents separately from the legacy selection envelope', async () => {
    const result = { pipelineId: 'pipeline-1', stage: 'spec', pipelineRevision: 5, discoveryVersion: 1, revision: 2, state: 'waiting_user', cancellationPending: false, cancellationAttemptId: '', artifactVersion: 1, attemptCount: 1, inputBudgetRemaining: 1000, outputBudgetRemaining: 4096, attempts: [], artifacts: [], actions: [], createdAt: valid.createdAt, updatedAt: valid.createdAt }
    vi.mocked(Service.GenerateAuthoringStage).mockResolvedValue(result as never)
    const input = {
      ref: { pipelineId: 'pipeline-1', stage: 'spec' as const, requestId: 'request-valid-0001', pipelineRevision: 4, stageRevision: 1, discoveryVersion: 1, artifactVersion: 0 },
      selection: { executor: 'api' as const, backendId: '' as const, profileId: '', modelId: '', catalogRevision: '', source: '', destination: '', checkedAt: NO_CATALOG_TIME, credentialToken: '', reasoningEffort: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096 },
      feedback: '', confirmUnfiltered: true, confirmJitLoad: false,
      consentBinding: { backendId: 'openrouter-1', modelId: 'model-1', catalogRevision: 'revision-1', source: 'openrouter_general_unfiltered', destination: 'https://api.example.test:443' },
    }
    await wailsBackend.generateAuthoringStage(input)
    expect(Service.GenerateAuthoringStage).toHaveBeenCalledWith(input)
  })

  it('forwards stable request identity and exact document fences through preparation, edits, cancellation and approval', async () => {
    const result = designReadback(undefined, true)
    const ref = { pipelineId: result.pipelineId, requestId: 'design_request_0000001', pipelineRevision: result.pipelineRevision, designRevision: result.revision }
    vi.mocked(Service.OpenPipelineDesign).mockResolvedValue(result as never)
    vi.mocked(Service.PreparePipelineDesign).mockResolvedValue(result as never)
    vi.mocked(Service.EditPipelineDesignDocument).mockResolvedValue(result as never)
    vi.mocked(Service.RestorePipelineDesignDocument).mockResolvedValue(result as never)
    vi.mocked(Service.CancelPipelineDesign).mockResolvedValue(result as never)
    vi.mocked(Service.ApprovePipelineDesign).mockResolvedValue(authoringPipeline as never)
    expect(await wailsBackend.openPipelineDesign(result.pipelineId)).toEqual(result)
    const prepare = { ref, message: 'Usar datas ISO.', target: 'spec' as const }
    expect(await wailsBackend.preparePipelineDesign(prepare)).toEqual(result)
    expect(Service.PreparePipelineDesign).toHaveBeenCalledWith(prepare)
    const edit = { ref, stage: 'plan' as const, content: '# Plan\n\nAdicionar testes.' }
    await wailsBackend.editPipelineDesignDocument(edit)
    expect(Service.EditPipelineDesignDocument).toHaveBeenCalledWith(edit)
    const restore = { ref, stage: 'spec' as const, version: 1 }
    await wailsBackend.restorePipelineDesignDocument(restore)
    expect(Service.RestorePipelineDesignDocument).toHaveBeenCalledWith(restore)
    const cancel = { pipelineId: result.pipelineId, attemptId: 'attempt-1' }
    await wailsBackend.cancelPipelineDesign(cancel)
    expect(Service.CancelPipelineDesign).toHaveBeenCalledWith(cancel)
    const approve = { ref, digests: { discovery: result.documents.discovery.contentDigest, spec: result.documents.spec.contentDigest, plan: result.documents.plan.contentDigest } }
    await wailsBackend.approvePipelineDesign(approve)
    expect(Service.ApprovePipelineDesign).toHaveBeenCalledWith(approve)
  })
  it('forwards the exact version and digest fence for Code/Eval review decisions', async () => {
    vi.mocked(Service.DecidePipelineExecutionArtifact).mockResolvedValue(authoringPipeline as never)
    const input = { pipelineId: 'pipeline-1', requestId: 'review-request-0001', stage: 'code' as const, artifactVersion: 2,
      artifactDigest: 'a'.repeat(64), decision: 'approve' as const, feedback: '', pipelineRevision: 7 }
    expect(await wailsBackend.decidePipelineExecutionArtifact(input)).toMatchObject({ id: 'pipeline-1' })
    expect(Service.DecidePipelineExecutionArtifact).toHaveBeenCalledWith(input)
  })

  it('forwards the exact legacy Code snapshot fence for human review recovery', async () => {
    vi.mocked(Service.ReopenPipelineCodeReview).mockResolvedValue(authoringPipeline as never)
    const input = { pipelineId: 'pipeline-1', artifactVersion: 3, artifactDigest: 'b'.repeat(64), pipelineRevision: 11 }
    expect(await wailsBackend.reopenPipelineCodeReview(input)).toMatchObject({ id: 'pipeline-1' })
    expect(Service.ReopenPipelineCodeReview).toHaveBeenCalledWith(input)
  })

  it('forwards the selected Code/Eval model with its catalog binding', async () => {
    const result = { session: { id: 'session-1', workspaceId: 'workspace-1', backendId: 'codex', status: 'ready', ...sessionMetadata }, prompt: 'Implement the approved plan.', role: 'coder' }
    vi.mocked(Service.CreatePipelineSession).mockResolvedValue(result as never)
    const selection = { executor: 'codex_cli' as const, backendId: 'codex' as const, profileId: '' as const, modelId: 'model-stage', catalogRevision: 'revision', source: 'codex_app_server' as const, destination: '' as const,
      checkedAt: valid.createdAt, credentialToken: '' as const, reasoningEffort: '', confirmUnverifiedManual: false as const, confirmUnfiltered: false as const, confirmJitLoad: false as const, maxOutputTokens: 0 }
    expect(await wailsBackend.createPipelineSession('pipeline-1', 'codex', 'coder', selection)).toMatchObject({ role: 'coder', prompt: result.prompt })
    expect(Service.CreatePipelineSession).toHaveBeenCalledWith({ pipelineId: 'pipeline-1', backendId: 'codex', role: 'coder', selection })
  })

  it('preserves uncertain cancellation in stage readback and forwards exact decision fences', async () => {
    const result = { pipelineId: 'pipeline-1', stage: 'spec', pipelineRevision: 5, discoveryVersion: 1, revision: 2, state: 'cancellation_pending', cancellationPending: true, cancellationAttemptId: 'attempt-1', artifactVersion: 0, attemptCount: 1, inputBudgetRemaining: 1000, outputBudgetRemaining: 4096, attempts: [], artifacts: [], actions: [], createdAt: valid.createdAt, updatedAt: valid.createdAt }
    vi.mocked(Service.GetAuthoringStage).mockResolvedValue(result as never)
    vi.mocked(Service.CancelAuthoringStage).mockResolvedValue(result as never)
    const query = { pipelineId: 'pipeline-1', stage: 'spec' as const }
    expect(await wailsBackend.getAuthoringStage(query)).toEqual(result)
    expect(Service.GetAuthoringStage).toHaveBeenCalledWith(query)
    const decision = { ref: { ...query, requestId: 'request-valid-0001', pipelineRevision: 4, stageRevision: 1, discoveryVersion: 1, artifactVersion: 0 }, reason: '', attemptId: 'attempt-1' }
    expect(await wailsBackend.cancelAuthoringStage(decision)).toEqual(result)
    expect(Service.CancelAuthoringStage).toHaveBeenCalledWith(decision)
  })
  it('validates brainstorm receipts and bounded history without exposing a credential', async () => {
    vi.mocked(Service.StartBrainstorming).mockResolvedValue(brainstormDTO as never)
    vi.mocked(Service.ListBrainstorming).mockResolvedValue([brainstormDTO] as never)
    const selection = { executor: 'api' as const, backendId: '' as const, profileId: 'api-1', modelId: 'model-1', catalogRevision: 'revision', source: 'openai_models', destination: 'API', checkedAt: valid.createdAt, credentialToken: 'a'.repeat(64), reasoningEffort: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 256 }
    const input = { pipelineId: 'pipeline-1', requestId: 'request-valid-0001', pipelineRevision: 3, discoveryVersion: 2, selection }
    const started = await wailsBackend.startBrainstorming(input)
    expect(Service.StartBrainstorming).toHaveBeenCalledWith(input)
    expect(started).toMatchObject({ id: 'brainstorm-1', state: 'ready' })
    expect(JSON.stringify(started)).not.toContain(selection.credentialToken)
    const query = { pipelineId: 'pipeline-1', workspaceId: 'workspace-1', limit: 20 }
    expect(await wailsBackend.listBrainstorming(query)).toHaveLength(1)
    expect(Service.ListBrainstorming).toHaveBeenCalledWith(query)
    vi.mocked(Service.GetBrainstorming).mockResolvedValue({ ...brainstormDTO, questionCount: 6 } as never)
    await expect(wailsBackend.getBrainstorming({ runId: 'brainstorm-1', pipelineId: '', discoveryVersion: 0 })).rejects.toThrow()
  })
  it.each(['A_b-000000000001', 'Z'.repeat(64)])('forwards the exact authoring create request ID %s', async requestId => {
    vi.mocked(Service.CreateAuthoringPipeline).mockResolvedValue(authoringPipeline)
    const input = { workspaceId: 'workspace-1', requestId, discovery: '# Discovery' }
    expect(await wailsBackend.createAuthoringPipeline(input)).toEqual(authoringPipeline)
    expect(Service.CreateAuthoringPipeline).toHaveBeenCalledWith(input)
  })
  it.each(['A_b-000000000001', 'Z'.repeat(64)])('forwards the exact authoring derive request ID %s', async requestId => {
    const child = { ...authoringPipeline, id: 'pipeline-2', derivedFromPipelineId: 'pipeline-1' }
    vi.mocked(Service.DeriveAuthoringPipeline).mockResolvedValue(child)
    const input = { parentPipelineId: 'pipeline-1', requestId, expectedRevision: 3, discovery: '# Revised Discovery' }
    expect(await wailsBackend.deriveAuthoringPipeline(input)).toEqual(child)
    expect(Service.DeriveAuthoringPipeline).toHaveBeenCalledWith(input)
  })
  it('forwards both optimistic concurrency values when revising Discovery', async () => {
    vi.mocked(Service.ReviseAuthoringDiscovery).mockResolvedValue(authoringPipeline)
    const input = { pipelineId: 'pipeline-1', expectedRevision: 3, expectedVersion: 2, discovery: '# Revised Discovery' }
    expect(await wailsBackend.reviseAuthoringDiscovery(input)).toEqual(authoringPipeline)
    expect(Service.ReviseAuthoringDiscovery).toHaveBeenCalledWith(input)
  })
  it('rejects invalid authoring readback without turning it into a completed creation', async () => {
    vi.mocked(Service.CreateAuthoringPipeline).mockResolvedValue({ ...authoringPipeline, artifacts: { discovery: { ...authoringPipeline.artifacts.discovery, version: 0 } } })
    await expect(wailsBackend.createAuthoringPipeline({ workspaceId: 'workspace-1', requestId: 'request-valid-0001', discovery: '# Discovery' })).rejects.toThrow()
  })
  it('rejects authoring readback that is legacy or missing required provenance', async () => {
    const create = () => wailsBackend.createAuthoringPipeline({ workspaceId: 'workspace-1', requestId: 'request-valid-0001', discovery: '# Discovery' })
    for (const malformed of [
      { ...authoringPipeline, kind: 'legacy' },
      { ...authoringPipeline, kind: undefined },
      { ...authoringPipeline, derivedFromPipelineId: undefined },
      { ...authoringPipeline, discoveryFrozenVersion: undefined },
      { ...authoringPipeline, artifacts: { discovery: { ...authoringPipeline.artifacts.discovery, author: undefined } } },
      { ...authoringPipeline, artifacts: { discovery: { ...authoringPipeline.artifacts.discovery, sourceSessionId: undefined } } },
    ]) {
      vi.mocked(Service.CreateAuthoringPipeline).mockResolvedValue(malformed as never)
      await expect(create()).rejects.toThrow()
    }
    vi.mocked(Service.DeriveAuthoringPipeline).mockResolvedValue({ ...authoringPipeline, kind: 'legacy' })
    await expect(wailsBackend.deriveAuthoringPipeline({ parentPipelineId: 'pipeline-1', requestId: 'request-valid-0002', expectedRevision: 3, discovery: '# Revised' })).rejects.toThrow()
    vi.mocked(Service.ReviseAuthoringDiscovery).mockResolvedValue({ ...authoringPipeline, kind: 'legacy' })
    await expect(wailsBackend.reviseAuthoringDiscovery({ pipelineId: 'pipeline-1', expectedRevision: 3, expectedVersion: 2, discovery: '# Revised' })).rejects.toThrow()
  })
  it('propagates an uncertain write rejection and exposes only safe error copy', async () => {
    const raw = 'SQL failed: confidential Discovery'
    const aborted = new DOMException(raw, 'AbortError')
    vi.mocked(Service.DeriveAuthoringPipeline).mockRejectedValue(aborted)
    const call = wailsBackend.deriveAuthoringPipeline({ parentPipelineId: 'pipeline-1', requestId: 'request-valid-0002', expectedRevision: 3, discovery: '# Discovery' })
    await expect(call).rejects.toBe(aborted)
    expect(errorMessage(aborted)).not.toContain('SQL')
    expect(errorMessage(aborted)).not.toContain('confidential Discovery')
  })
  it('validates the HTTP catalog result and binds cancellation without making an already-aborted call', async () => {
    const result = { backendId: 'router', source: 'openrouter_account', destination: 'https://router.example:443', profileRevision: 'api:revision', searchTerm: '', models: [{ id: 'vendor/model', displayName: 'Vendor Model', backendId: 'router', source: 'openrouter_account', availability: 'listed' }], nextCursor: '', checkedAt: valid.createdAt, status: 'complete', complete: true, accountFiltered: true }
    const controller = new AbortController()
    const call = Object.assign(Promise.resolve(result), { cancelOn: vi.fn(function (this: Promise<typeof result>) { return this }) })
    vi.mocked(Service.QueryHTTPModelCatalog).mockReturnValue(call as never)
    const query = { profileId: 'router', searchTerm: '', refresh: false }
    expect(await wailsBackend.queryHTTPModelCatalog(query, controller.signal)).toEqual(result)
    expect(Service.QueryHTTPModelCatalog).toHaveBeenCalledWith(query)
    expect(call.cancelOn).toHaveBeenCalledWith(controller.signal)

    controller.abort()
    await expect(wailsBackend.queryHTTPModelCatalog(query, controller.signal)).rejects.toMatchObject({ name: 'AbortError' })
    expect(Service.QueryHTTPModelCatalog).toHaveBeenCalledTimes(1)
  })
  it('queries a CLI catalog for the exact workspace and cancels a stale request', async () => {
    const result = { backendId: 'codex', source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'cli-revision', searchTerm: '',
      models: [{ id: 'runtime-model', displayName: 'Modelo', backendId: 'codex', source: 'codex_app_server', availability: 'listed', supportedReasoningEfforts: ['low', 'high'], defaultReasoningEffort: 'high' }],
      nextCursor: '', checkedAt: valid.createdAt, status: 'complete', complete: true, accountFiltered: true }
    const controller = new AbortController()
    const call = Object.assign(Promise.resolve(result), { cancelOn: vi.fn(function (this: Promise<typeof result>) { return this }) })
    vi.mocked(Service.QueryCLIModelCatalog).mockReturnValue(call as never)
    const query = { workspaceId: 'workspace-1', backendId: 'codex' }
    expect(await wailsBackend.queryCLIModelCatalog(query, controller.signal)).toEqual(result)
    expect(Service.QueryCLIModelCatalog).toHaveBeenCalledWith(query)
    expect(call.cancelOn).toHaveBeenCalledWith(controller.signal)
    controller.abort()
    await expect(wailsBackend.queryCLIModelCatalog(query, controller.signal)).rejects.toMatchObject({ name: 'AbortError' })
    expect(Service.QueryCLIModelCatalog).toHaveBeenCalledTimes(1)
  })
  it('sends the selected CLI model and effort through the direct-session binding', async () => {
    const session = { id: 'session-1', workspaceId: 'workspace-1', backendId: 'codex', status: 'ready', ...sessionMetadata }
    vi.mocked(Service.CreateDirectSession).mockResolvedValue(session)
    vi.mocked(Service.GetSessionModelSelection).mockResolvedValue({ sessionId: session.id, backendId: 'codex', modelId: 'runtime-model', reasoningEffort: 'high', source: 'codex_app_server', destination: '', status: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 0, contextLength: 0, checkedAt: valid.createdAt })
    const input = { workspaceId: session.workspaceId, backendId: session.backendId, reason: 'pesquisa', modelId: 'runtime-model', reasoningEffort: 'high', catalogRevision: 'revision' }
    expect(await wailsBackend.createDirectSession(input)).toEqual(session)
    expect(Service.CreateDirectSession).toHaveBeenCalledWith({ ...input, agentId: '' })
    expect(await wailsBackend.getSessionModelSelection(session.id)).toMatchObject({ modelId: 'runtime-model', reasoningEffort: 'high' })
  })
  it('keeps OpenRouter management key bytes out of status readbacks', async () => {
    const status = { profileId: 'router', origin: 'https://router.example:443', configured: true, usable: true, updatedAt: valid.createdAt }
    vi.mocked(Service.GetOpenRouterManagementKeyStatus).mockResolvedValue(status)
    vi.mocked(Service.SaveOpenRouterManagementKey).mockResolvedValue(status)
    vi.mocked(Service.ClearOpenRouterManagementKey).mockResolvedValue(undefined)
    expect(await wailsBackend.getOpenRouterManagementKeyStatus('router')).toEqual(status)
    expect(await wailsBackend.saveOpenRouterManagementKey('router', 'management-canary')).toEqual(status)
    expect(Service.SaveOpenRouterManagementKey).toHaveBeenCalledWith('router', 'management-canary')
    await wailsBackend.clearOpenRouterManagementKey('router')
    expect(Service.ClearOpenRouterManagementKey).toHaveBeenCalledWith('router')
    expect(JSON.stringify(status)).not.toContain('canary')
  })
  it('validates session discovery, reopening and audit export at the binding boundary', async () => {
    const session = { id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'paused', ...sessionMetadata }
    vi.mocked(Service.ListSessions).mockResolvedValue([session])
    vi.mocked(Service.OpenSession).mockResolvedValue(session)
    vi.mocked(Service.ExportAudit).mockResolvedValue('/tmp/audit.json')
    expect(await wailsBackend.listSessions('workspace-1')).toEqual([session])
    expect(Service.ListSessions).toHaveBeenCalledWith({ workspaceId: 'workspace-1' })
    expect(await wailsBackend.openSession('session-1', 'workspace-1')).toEqual(session)
    expect(Service.OpenSession).toHaveBeenCalledWith({ sessionId: 'session-1', workspaceId: 'workspace-1' })
    expect(await wailsBackend.exportAudit('session-1', '/tmp/audit.json')).toBe('/tmp/audit.json')
    expect(Service.ExportAudit).toHaveBeenCalledWith({ sessionId: 'session-1', destination: '/tmp/audit.json' })
    await expect(wailsBackend.exportAudit('session-1', '\u0000')).rejects.toThrow()
    expect(Service.ExportAudit).toHaveBeenCalledTimes(1)
    vi.mocked(Service.ListSessions).mockResolvedValue([{ ...session, resumable: 'invalid' }] as never)
    await expect(wailsBackend.listSessions('workspace-1')).rejects.toThrow()
  })
  it('emits one safe local diagnostic per invalid payload and preserves valid events', () => {
    const listener = vi.fn()
    const { emit, unsubscribe } = subscribe(listener)
    const invalid = { streamId: 'sensitive-stream', data: { apiKey: 'never-copy-this' } }

    expect(() => emit(invalid)).not.toThrow()

    expect(listener).toHaveBeenCalledTimes(1)
    const diagnostic = listener.mock.calls[0][0] as AgentEvent
    expect(diagnostic).toEqual({
      id: expect.stringMatching(/^frontend-diagnostic-\d+$/),
      streamId: 'frontend:diagnostics', sequence: 0, type: 'diagnostic.invalid_event',
      data: { code: 'invalid_backend_event', message: 'Evento do backend inválido.' }, createdAt: '',
    })
    expect(JSON.stringify(diagnostic)).not.toContain('never-copy-this')
    expect(JSON.stringify(diagnostic)).not.toContain('sensitive-stream')
    emit(null)
    expect(listener).toHaveBeenCalledTimes(2)
    expect(listener.mock.calls[1][0].id).not.toBe(diagnostic.id)
    emit(valid)
    expect(listener).toHaveBeenCalledTimes(3)
    expect(listener).toHaveBeenLastCalledWith(valid)
    const subscriptions = vi.mocked(Events.On).mock.results
    expect(unsubscribe).toBe(subscriptions[subscriptions.length - 1].value)
  })

  it('retains diagnostics without changing the conversation, session, or replay cursor', () => {
    const store = createSessionStore()
    const session = { id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata }
    store.getState().setSession(session)
    const { emit } = subscribe(event => store.getState().receive([event]))
    emit(valid)
    const messages = store.getState().messages

    emit(undefined)

    expect(store.getState().events).toHaveLength(2)
    expect(store.getState().events.map(event => event.sequence)).toEqual([0, 1])
    expect(store.getState().session).toEqual(session)
    expect(store.getState().messages).toEqual(messages)
    const events = store.getState().events
    expect(store.getState().contiguousSequence).toBe(1)
    expect(events[events.length - 1].sequence).toBe(1)
    emit({ ...valid, id: 'event-2', sequence: 2, data: { delta: '!' } })
    expect(store.getState().events.map(event => event.sequence)).toEqual([0, 1, 2])
    expect(store.getState().messages[0]).toMatchObject({ text: 'Olá!' })
  })

  it('turns a malformed known payload into a diagnostic without creating an approval', () => {
    const store = createSessionStore()
    store.getState().setSession({ id: 'session-1', workspaceId: 'w1', backendId: 'local', status: 'ready', ...sessionMetadata })
    const { emit } = subscribe(event => store.getState().receive([event]))
    emit({ ...valid, type: 'approval.requested', data: {} })
    expect(store.getState().events).toHaveLength(1)
    expect(store.getState().events[0].type).toBe('diagnostic.invalid_event')
    expect(store.getState().pendingApprovals).toEqual([])
    expect(store.getState().contiguousSequence).toBe(0)
  })
})
