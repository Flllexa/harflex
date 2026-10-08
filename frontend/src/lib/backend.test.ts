import { describe, expect, it } from 'vitest'
import { NO_CATALOG_TIME, errorMessage, parse } from './backend'
import * as backendContract from './backend'
import { createFakeBackend } from '../test/fakeBackend'

const envelope = { id: 'e1', streamId: 's1', sequence: 1, createdAt: '2026-09-25T10:00:00Z' }
const authoringDiscovery = { stage: 'discovery', version: 2, content: '# Discovery', author: 'user', sourceSessionId: '', updatedAt: envelope.createdAt }
const authoringReadback = { id: 'pipeline-ai', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 2,
  title: 'Pedido', objective: 'Objetivo', currentStage: 'spec', stageStatus: { discovery: 'completed', spec: 'active' }, revision: 3,
  artifacts: { discovery: authoringDiscovery, spec: { stage: 'spec', version: 1, content: '# SPEC', author: 'ai', sourceSessionId: 'session-1', updatedAt: envelope.createdAt } },
  createdAt: envelope.createdAt, updatedAt: envelope.createdAt }

describe('event payload validation', () => {
  it('validates phase model preference modes and rejects credential fields from readback', () => {
    const parsePreference = (parse as unknown as Record<string, unknown>).authoringStageModelPreference
    expect(parsePreference).toBeTypeOf('function')
    if (typeof parsePreference !== 'function') return
    const preference = {
      pipelineId: 'pipeline-ai', stage: 'plan', modelMode: 'override', effortMode: 'explicit', explicitEffort: 'high',
      preferenceRevision: 4, resolution: 'ready', modelSource: 'phase_override', effortSource: 'phase_override', inheritedFrom: '',
      catalogValidationRequired: false, errorCode: '',
      selection: { backendId: 'profile', modelId: 'model', reasoningEffort: 'high', catalogRevision: 'rev-4', source: 'openai_models', destination: 'https://api.example.test:443', status: 'listed', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096, contextLength: 128000, checkedAt: envelope.createdAt },
    }
    const parsed = (parsePreference as (value: unknown) => unknown)(preference)
    expect(parsed).toEqual(preference)
    expect(() => (parsePreference as (value: unknown) => unknown)({ ...preference, selection: { ...preference.selection, credentialToken: 'secret' } })).toThrow()
    expect(() => (parsePreference as (value: unknown) => unknown)({ ...preference, effortMode: 'automatic', explicitEffort: 'high' })).toThrow()
  })
  it('sends Go a decodable time for calls that carry no catalog choice', () => {
    // Go rejects "" for time.Time, which made every SPEC/Plan generation fail before it ran.
    expect(NO_CATALOG_TIME).toBe('0001-01-01T00:00:00Z')
    expect(NO_CATALOG_TIME).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/)
  })
  it('accepts the effort levels the Go selection always carries, including null for a stale snapshot', () => {
    const preference = {
      pipelineId: 'pipeline-ai', stage: 'spec', modelMode: 'inherit', effortMode: 'inherit', explicitEffort: '',
      preferenceRevision: 0, resolution: 'stale', modelSource: 'brainstorm', effortSource: '', inheritedFrom: '',
      catalogValidationRequired: false, errorCode: 'backend_changed',
      selection: { backendId: 'profile', modelId: 'model', reasoningEffort: '', supportedReasoningEfforts: null, catalogRevision: 'rev', source: 'ollama_tags', destination: 'http://127.0.0.1:9470', status: 'listed', confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 256, contextLength: 0, checkedAt: envelope.createdAt },
    }
    expect(parse.authoringStageModelPreference(preference)).toEqual(preference)
    const withLevels = { ...preference, resolution: 'ready', errorCode: '', selection: { ...preference.selection, supportedReasoningEfforts: ['low', 'high'] } }
    expect(parse.authoringStageModelPreference(withLevels)).toEqual(withLevels)
    expect(() => parse.authoringStageModelPreference({ ...preference, selection: { ...preference.selection, supportedReasoningEfforts: ['x'.repeat(65)] } })).toThrow()
    expect(() => parse.authoringStageModelPreference({ ...preference, selection: { ...preference.selection, apiKey: 'secret' } })).toThrow()
  })
  it('persists fake phase preferences independently by stage and enforces the expected revision', async () => {
    const { backend } = createFakeBackend()
    const input = { pipelineId: 'pipeline-ai', stage: 'spec' as const, expectedRevision: 0, modelMode: 'inherit' as const, effortMode: 'automatic' as const, explicitEffort: '', selection: { executor: 'api' as const, backendId: '' as const, profileId: '', modelId: '', catalogRevision: '', source: '', destination: '', checkedAt: NO_CATALOG_TIME, credentialToken: '', reasoningEffort: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096 } }
    const saved = await backend.saveAuthoringStageModelPreference(input)
    expect(saved.preferenceRevision).toBe(1)
    expect(await backend.getAuthoringStageModelPreference({ pipelineId: input.pipelineId, stage: 'spec' })).toEqual(saved)
    const plan = await backend.getAuthoringStageModelPreference({ pipelineId: input.pipelineId, stage: 'plan' })
    expect(plan.preferenceRevision).toBe(0)
    expect(plan.stage).toBe('plan')
    const credentialToken = 'a'.repeat(64)
    const override = await backend.saveAuthoringStageModelPreference({ ...input, expectedRevision: 1, modelMode: 'override', effortMode: 'explicit', explicitEffort: 'high', selection: { ...input.selection, profileId: 'api-2', modelId: 'model-high', catalogRevision: 'profile-rev-2', source: 'openai_models', destination: 'https://api.example.test:443', checkedAt: new Date().toISOString(), credentialToken } })
    expect(override.preferenceRevision).toBe(2)
    expect(JSON.stringify(override)).not.toContain(credentialToken)
    expect(await backend.getAuthoringStageModelPreference({ pipelineId: input.pipelineId, stage: 'spec' })).toEqual(override)
    await expect(backend.saveAuthoringStageModelPreference(input)).rejects.toMatchObject({ cause: { code: 'pipeline_conflict' } })
  })

  it('accepts public effort capabilities for Automatic and explicit selections without requiring them on legacy readback', () => {
    const selection = { sessionId: 'attempt', backendId: 'api', modelId: 'chosen', reasoningEffort: '', source: 'lm_studio_native', destination: 'http://localhost:1234', status: 'listed', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: true, maxOutputTokens: 321, contextLength: 8192, checkedAt: envelope.createdAt }
    expect(parse.sessionModelSelection(selection)).toEqual(selection)
    for (const reasoningEffort of ['', 'high']) {
      const current = { ...selection, reasoningEffort, supportedReasoningEfforts: ['low', 'high'] }
      expect(parse.sessionModelSelection(current)).toEqual(current)
      expect(() => parse.sessionModelSelection({ ...current, credentialIdentity: 'secret' })).toThrow()
    }
  })
  it('reads the documented legacy text-only Codex completion without accepting generic malformed terminals', () => {
    const legacy = { ...envelope, type:'run.completed', data:{adapter:'codex',purpose:'documents_no_tools',threadId:'document-thread'} }
    expect(parse.safeEvent(legacy)).not.toBeNull()
    expect(parse.safeEvent({ ...legacy,data:{} })).toBeNull()
    expect(parse.safeEvent({ ...legacy,data:{purpose:'unexpected',adapter:'codex'} })).toBeNull()
  })
  it('exports the provider type schema for the typed bridge', () => {
    expect(backendContract).toHaveProperty('providerType')
  })
  it('requires known provider types and explicit endpoint status in profile readbacks', () => {
    const profile = { id: 'remote', name: 'Remote', kind: 'openai_compatible', providerType: 'generic', baseUrl: 'https://remote.example/v1', model: 'qwen', hasCredential: true, endpointBlocked: false, updatedAt: envelope.createdAt }
    expect(parse.providerProfiles([profile])).toEqual([profile])
    expect(() => parse.providerProfiles([{ ...profile, providerType: undefined }])).toThrow()
    expect(() => parse.providerProfiles([{ ...profile, providerType: 'unknown' }])).toThrow()
    expect(() => parse.providerProfiles([{ ...profile, endpointBlocked: undefined }])).toThrow()
  })
  it('validates model catalogs without turning partial or missing models into complete results', () => {
    const model = { id: 'vendor/model-exact', displayName: 'Modelo longo', backendId: 'router', source: 'openrouter_account', availability: 'listed', loaded: false, supportedReasoningEfforts: ['low', 'high'] }
    const result = { backendId: 'router', source: 'openrouter_account', destination: 'https://router.example:443', profileRevision: 'api:revision', searchTerm: '', models: [model], nextCursor: '', checkedAt: envelope.createdAt, status: 'complete', complete: true, accountFiltered: true }
    expect(parse.modelCatalog(result)).toEqual(result)
    expect(parse.modelCatalog({ ...result, credentialToken: 'a'.repeat(64) }).credentialToken).toBe('a'.repeat(64))
    expect(parse.modelCatalog({ ...result, models: null, status: 'failed', complete: false, errorCode: 'catalog_unavailable' }).models).toEqual([])
    expect(parse.modelCatalog({ ...result, backendId: '', source: '', destination: '', profileRevision: '', models: null, checkedAt: '0001-01-01T00:00:00Z', status: 'failed', complete: false }).models).toEqual([])
    expect(() => parse.modelCatalog({ ...result, status: 'unknown' })).toThrow()
    expect(() => parse.modelCatalog({ ...result, models: [{ ...model, id: '' }] })).toThrow()
    expect(() => parse.modelCatalog({ ...result, apiKey: 'must-not-cross' })).toThrow()
  })
  it('reads OpenRouter management status without accepting secret bytes', () => {
    const status = { profileId: 'router', origin: 'https://router.example:443', configured: true, usable: true, updatedAt: envelope.createdAt }
    expect(parse.openRouterManagementKeyStatus(status)).toEqual(status)
    expect(parse.openRouterManagementKeyStatus({ ...status, origin: '', configured: false, usable: false, updatedAt: '0001-01-01T00:00:00Z' }).configured).toBe(false)
    expect(() => parse.openRouterManagementKeyStatus({ ...status, key: 'must-not-cross' })).toThrow()
  })
  it('reads API selection provenance and confirmation without exposing credential identity', () => {
    const selection = { sessionId: 'attempt', backendId: 'api', modelId: 'chosen', reasoningEffort: '', source: 'openrouter_general_unfiltered', destination: 'https://example.test', status: 'listed_unfiltered', confirmUnverifiedManual: false, confirmUnfiltered: true, confirmJitLoad: false, maxOutputTokens: 321, contextLength: 8192, checkedAt: envelope.createdAt }
    expect(parse.sessionModelSelection(selection)).toEqual(selection)
    expect(() => parse.sessionModelSelection({ ...selection, credentialIdentity: 'secret-digest' })).toThrow()
  })
  it('explains blocked provider endpoints with HTTPS guidance', () => {
    expect(errorMessage({ cause: { code: 'provider_endpoint_blocked' } })).toMatch(/HTTPS/)
  })
  it('explains recovery from missing document models and invalid model responses', () => {
    expect(errorMessage({ cause: { code: 'pipeline_design_model_required' } })).toMatch(/modelo padrão.*Configurações/)
    expect(errorMessage({ cause: { code: 'pipeline_design_invalid_response' } })).toMatch(/preservados.*novamente/)
    expect(errorMessage({ cause: { code: 'pipeline_design_derivation_required' } })).toMatch(/continuação/)
  })
  it('names the phase whose chosen executor could not write its document, from the object or the JSON string the bridge carries', () => {
    for (const [stage, name] of [['discovery', 'Discovery'], ['spec', 'SPEC'], ['plan', 'Plan']] as const) {
      const fromObject = errorMessage({ cause: { code: 'pipeline_design_phase_executor_unusable', stage } })
      expect(fromObject).toContain(`escolhido para ${name} não está disponível agora`)
      expect(fromObject).toMatch(/Provedor e modelo por fase/)
      expect(errorMessage({ cause: JSON.stringify({ code: 'pipeline_design_phase_executor_unusable', stage }) })).toBe(fromObject)
    }
    // A phase the screen does not know is not guessed at, and a different failure never borrows the sentence.
    expect(errorMessage({ cause: { code: 'pipeline_design_phase_executor_unusable', stage: 'deploy' } })).toContain('escolhido para uma das fases')
    expect(errorMessage({ cause: { code: 'pipeline_design_phase_executor_unusable' } })).toContain('escolhido para uma das fases')
    expect(errorMessage({ cause: { code: 'stage_executor_unsupported', stage: 'spec' } })).not.toContain('SPEC')
  })
  it('validates schedule and job readbacks before they reach the UI', () => {
    const schedule = { id: 'schedule-1', workspaceId: 'workspace-1', name: 'Daily review', targetKind: 'prompt', workflowId: '', backendId: 'local', prompt: 'Review', frequency: 'daily', timezone: 'America/Sao_Paulo', localDate: '', localTime: '09:30', missedPolicy: 'skip', enabled: true, allowCli: false, nextRunAt: '2026-09-27T12:30:00Z', revision: 1, createdAt: '2026-09-26T10:00:00Z', updatedAt: '2026-09-26T10:00:00Z' }
    expect(parse.schedules([schedule])).toEqual([schedule])
    expect(() => parse.schedules([{ ...schedule, frequency: 'sometimes' }])).toThrow()
    const job = { id: 'job-1', scheduleId: schedule.id, workspaceId: schedule.workspaceId, trigger: 'manual', status: 'completed', errorCode: '', workflowRunId: '', sessionId: 'session-1', dueAt: schedule.createdAt, createdAt: schedule.createdAt, startedAt: schedule.createdAt, finishedAt: schedule.createdAt }
    expect(parse.scheduleJobs([job])).toEqual([job])
    expect(parse.scheduleJobs([{ ...job, status: 'cancel_requested', errorCode: 'cancel_failed', finishedAt: null }])).toHaveLength(1)
    expect(() => parse.scheduleJobs([{ ...job, prompt: 'secret' }])).toThrow()
  })
  it('allows an empty canonical replacement only with a part identity', () => {
    const base = { type: 'assistant.message', raw: {} }
    for (const text of [undefined, '']) {
      const data = { ...base, text }
      expect(parse.safeEvent({ ...envelope, type: 'external.event', data })).toBeNull()
      expect(parse.safeEvent({ ...envelope, type: 'external.event', data: { ...data, mode: 'replace', messageId: 'part_1' } })).not.toBeNull()
      expect(parse.safeEvent({ ...envelope, type: 'external.event', data: { ...data, mode: 'delta', messageId: 'part_1' } })).toBeNull()
    }
  })
  it('validates canonical external text identity and update mode', () => {
    const base = { type: 'assistant.message', text: 'Olá', raw: {}, messageId: 'part_1', mode: 'replace' }
    expect(parse.safeEvent({ ...envelope, type: 'external.event', data: base })).not.toBeNull()
    for (const patch of [{ mode: 'append' }, { messageId: 3 }, { messageId: 'x'.repeat(257) }, { mode: 'delta', messageId: undefined }]) {
      expect(parse.safeEvent({ ...envelope, type: 'external.event', data: { ...base, ...patch } })).toBeNull()
    }
  })
  it('explains the reopening budget without promising an unavailable audit export', () => {
    expect(errorMessage({ cause: { code: 'history_too_large' } })).toBe('O histórico excede o limite de reabertura. Inicie uma nova sessão; o histórico permanece armazenado localmente.')
  })
  it('explains when isolated Code evidence no longer matches its snapshot', () => {
    expect(errorMessage({ cause: { code: 'untracked_evidence_requires_stage' } })).toContain('raiz isolada')
  })
  it('explains why a Code copy requires an explicit confirmation for non-Git folders', () => {
    expect(errorMessage({ cause: { code: 'execution_copy_confirmation_required' } })).toMatch(/Revise a prévia/)
    expect(errorMessage({ cause: { code: 'execution_copy_confirmation_required' } })).toMatch(/confirme a cópia privada/)
  })
  it('explains that Code requires a clean source before creating an isolated snapshot', () => {
    expect(errorMessage({ cause: { code: 'pipeline_git_dirty' } })).toMatch(/A pasta do projeto já tem alterações Git/)
    expect(errorMessage({ cause: { code: 'pipeline_git_dirty' } })).toMatch(/Nenhuma sessão foi iniciada/)
  })
  it('explains when the full Git baseline is too large to verify safely', () => {
    expect(errorMessage({ cause: { code: 'pipeline_git_baseline_unavailable' } })).toMatch(/snapshot completo desta pasta/i)
    expect(errorMessage({ cause: { code: 'pipeline_git_baseline_unavailable' } })).toMatch(/Reduza o escopo/)
  })
  it('explains when measured SDD usage exceeds the reserved token budget', () => {
    expect(errorMessage({ cause: { code: 'brainstorm_budget_exceeded' } })).toMatch(/consumo.*excedeu.*orçamento/i)
    expect(errorMessage({ cause: { code: 'authoring_budget_exceeded' } })).toMatch(/consumo.*excedeu.*orçamento/i)
  })
  it('preserves authoring provenance, derivation, and frozen Discovery on readback', () => {
    const artifact = { stage: 'discovery', version: 2, content: '# Discovery', author: 'user', sourceSessionId: '', updatedAt: envelope.createdAt }
    const pipeline = { id: 'pipeline-2', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: 'pipeline-1', discoveryFrozenVersion: 2,
      title: 'Pedido', objective: 'Objetivo', currentStage: 'discovery', stageStatus: { discovery: 'completed' }, revision: 3,
      artifacts: { discovery: artifact, spec: { ...artifact, stage: 'spec', author: 'ai', sourceSessionId: 'session-1' } },
      createdAt: envelope.createdAt, updatedAt: envelope.createdAt }
    expect(parse.pipeline(pipeline)).toEqual(pipeline)
    expect(() => parse.pipeline({ ...pipeline, kind: 'unknown' })).toThrow()
    expect(() => parse.pipeline({ ...pipeline, discoveryFrozenVersion: -1 })).toThrow()
    expect(() => parse.pipeline({ ...pipeline, artifacts: { discovery: { ...artifact, author: 'untrusted' } } })).toThrow()
  })
  it('accepts a frozen authoring pipeline with an AI-written future-stage artifact', () => {
    expect(parse.authoringPipeline(authoringReadback)).toEqual(authoringReadback)
  })
  it('rejects non-AI future-stage artifacts and missing authoring session identity', () => {
    for (const patch of [
      { author: 'legacy/manual' }, { author: 'user' }, { sourceSessionId: '' }, { sourceSessionId: '  ' },
    ]) {
      const malformed = { ...authoringReadback, artifacts: { ...authoringReadback.artifacts, spec: { ...authoringReadback.artifacts.spec, ...patch } } }
      expect(() => parse.authoringPipeline(malformed)).toThrow()
    }
    const discoveryWithSession = { ...authoringReadback, artifacts: { ...authoringReadback.artifacts,
      discovery: { ...authoringDiscovery, sourceSessionId: 'session-1' } } }
    expect(() => parse.authoringPipeline(discoveryWithSession)).toThrow()
  })
  it('keeps unfrozen Discovery before the gate and rejects mismatched or premature frozen state', () => {
    const draft = { ...authoringReadback, currentStage: 'discovery', discoveryFrozenVersion: 0,
      artifacts: { discovery: authoringDiscovery } }
    expect(parse.authoringPipeline(draft)).toEqual(draft)
    expect(() => parse.authoringPipeline({ ...draft, artifacts: authoringReadback.artifacts })).toThrow()
    expect(() => parse.authoringPipeline({ ...authoringReadback, discoveryFrozenVersion: 3 })).toThrow()
    expect(() => parse.authoringPipeline({ ...authoringReadback, discoveryFrozenVersion: 0 })).toThrow()
  })
  it('keeps historical pipeline fixture objects readable without inventing provenance', () => {
    const artifact = { stage: 'spec', version: 1, content: 'Original', updatedAt: envelope.createdAt }
    const historical = { id: 'pipeline-legacy', workspaceId: 'workspace-1', title: 'Legacy', objective: 'Original', currentStage: 'spec',
      stageStatus: { spec: 'active' }, revision: 1, artifacts: { spec: artifact }, createdAt: envelope.createdAt, updatedAt: envelope.createdAt }
    expect(parse.pipeline(historical)).toEqual(historical)
  })
  it('does not pretend the synthetic backend completed an authoring write', async () => {
    const { backend } = createFakeBackend()
    await expect(backend.createAuthoringPipeline({ workspaceId: 'workspace-1', requestId: 'request-valid-0001', discovery: '# Discovery' })).rejects.toThrow('Synthetic pipeline unavailable')
    await expect(backend.deriveAuthoringPipeline({ parentPipelineId: 'pipeline-1', requestId: 'request-valid-0002', expectedRevision: 1, discovery: '# Revised' })).rejects.toThrow('Synthetic pipeline unavailable')
    await expect(backend.reviseAuthoringDiscovery({ pipelineId: 'pipeline-1', expectedRevision: 1, expectedVersion: 1, discovery: '# Revised' })).rejects.toThrow('Synthetic pipeline unavailable')
  })
  it('explains a reused authoring request safely without exposing SQL or Discovery', () => {
    const raw = 'SQL constraint failed: Discovery secreta'
    const error = { cause: JSON.stringify({ code: 'pipeline_request_conflict', message: raw }), message: raw }
    const message = errorMessage(error)
    expect(message).toMatch(/tentativa|requisi[cç][aã]o/i)
    expect(message).not.toContain('SQL')
    expect(message).not.toContain('Discovery secreta')
  })
  it('validates recovery and external event payloads without interpreting raw fields', () => {
    for (const [type, data] of [
      ['run.interrupted', { reason: 'app_restart' }],
      ['external.event', { type: 'text', text: 'Olá', raw: { nested: true } }],
      ['external.session.bound', { sessionId: 'native-session' }],
    ] as const) expect(parse.safeEvent({ ...envelope, type, data })).not.toBeNull()
    for (const [type, data] of [
      ['run.interrupted', {}], ['external.event', { type: 'text', text: 3 }], ['external.session.bound', { sessionId: '' }],
    ] as const) expect(parse.safeEvent({ ...envelope, type, data })).toBeNull()
  })
  it('requires current session metadata and a safe explicit export path', () => {
    const value = { id: 's', workspaceId: 'w', backendId: 'codex', status: 'ready', resumable: false, createdAt: envelope.createdAt, updatedAt: envelope.createdAt }
    expect(parse.session(value)).toEqual(value)
    expect(() => parse.session({ ...value, resumable: 'yes' })).toThrow()
    expect(() => parse.session({ ...value, updatedAt: 'yesterday' })).toThrow()
    expect(parse.sessions(null)).toEqual([])
    expect(parse.auditPath('/tmp/audit.json')).toBe('/tmp/audit.json')
    for (const path of ['', ' ', '/tmp/a\u0000.json', '/tmp/a\n.json']) expect(() => parse.auditPath(path)).toThrow()
  })
  it('accepts recovery tool closures that have an error code instead of a reason', () => {
    const value = { ...envelope, type: 'tool.skipped', data: { toolCallId: 't', name: 'write', errorCode: 'not_executed', error: 'tool call not executed' } }
    expect(parse.safeEvent(value)).toEqual(value)
    expect(parse.safeEvent({ ...value, data: { ...value.data, errorCode: undefined } })).toBeNull()
  })
  it.each([
    ['assistant.delta', { delta: 'Hi' }],
    ['message.user', { id: '', role: 'user', content: 'Hi' }],
    ['message.assistant', { id: '', role: 'assistant', content: '', toolCalls: [{ id: 'c1', name: 'write', arguments: {} }] }],
    ['tool.called', { toolCallId: 'c1', name: 'write' }],
    ['tool.updated', { toolCallId: 'c1', stream: 'stdout', text: 'done' }],
    ['tool.completed', { toolCallId: 'c1', name: 'write', content: { text: 'written' }, details: { path: 'notes.md', diff: '+Hi' } }],
    ['tool.failed', { toolCallId: 'c1', name: 'write', error: 'failed', errorCode: 'tool_failed', content: null }],
    ['tool.denied', { toolCallId: 'c1', name: 'write', error: 'denied', reason: 'policy_denied' }],
    ['tool.skipped', { toolCallId: 'c1', name: 'write', error: 'skipped', reason: 'cancelled' }],
    ['approval.requested', { approvalId: 'a1', toolCallId: 'c1', name: 'write', risk: 'write', arguments: {} }],
    ['approval.approved', { approvalId: 'a1', toolCallId: 'c1' }],
    ['approval.denied', { approvalId: 'a1', toolCallId: 'c1' }],
    ['run.started', {}], ['run.completed', { reason: '' }], ['run.failed', { reason: 'tool_failed' }], ['run.cancelled', { reason: 'cancelled' }],
    ['external.run.started', { adapter: 'codex' }], ['external.run.completed', { adapter: 'codex' }], ['external.run.failed', { adapter: 'codex' }], ['external.run.cancelled', { adapter: 'codex' }],
    ['usage.recorded', { inputTokens: 3, outputTokens: 2 }],
  ])('accepts the backend contract for %s', (type, data) => {
    const value = { ...envelope, type, data }
    expect(parse.safeEvent(value)).toEqual(value)
  })
  it.each([
    ['assistant.delta', { delta: 1 }],
    ['message.user', { role: 'user', content: false }],
    ['message.assistant', { role: 'assistant', content: '', toolCalls: [{ id: '', name: 'write', arguments: {} }] }],
    ['tool.called', { toolCallId: '', name: 'write' }],
    ['tool.updated', { toolCallId: 'c1', text: false }],
    ['tool.completed', { toolCallId: 'c1', name: 'write', content: { text: [] } }],
    ['tool.failed', { toolCallId: 'c1', name: 'write', error: false }],
    ['approval.requested', {}],
    ['approval.approved', { approvalId: '', toolCallId: 'c1' }],
    ['approval.denied', { approvalId: 'a1', toolCallId: 1 }],
    ['run.started', null],
    ['run.completed', { reason: 1 }],
    ['run.failed', { reason: false }],
    ['run.cancelled', { reason: [] }],
    ['usage.recorded', { inputTokens: -1, outputTokens: 1 }],
  ])('rejects malformed %s data', (type, data) => {
    expect(parse.safeEvent({ ...envelope, type, data })).toBeNull()
  })

  it.each(['completed', 'failed', 'cancelled'])('validates the external %s adapter and optional reason', status => {
    const type = `external.run.${status}`
    for (const data of [{}, { reason: '' }, { adapter: '' }, { adapter: 1 }, { adapter: 'codex', reason: 1 }]) {
      const invalid = { ...envelope, type, data }
      expect(parse.safeEvent(invalid)).toBeNull()
      expect(parse.events([invalid])[0]).toMatchObject({ type: 'diagnostic.invalid_event', sequence: 0 })
    }
    const future = { ...envelope, type, data: { adapter: 'codex', reason: 'future_reason' } }
    expect(parse.safeEvent(future)).toEqual(future)
  })

  it('requires a valid timestamp and preserves unknown events for journal inspection', () => {
    expect(parse.safeEvent({ ...envelope, createdAt: 'yesterday', type: 'assistant.delta', data: { delta: 'Hi' } })).toBeNull()
    const unknown = { ...envelope, type: 'future.event', data: { opaque: ['x'] } }
    expect(parse.safeEvent(unknown)).toEqual(unknown)
  })

  it('turns invalid replay entries into safe local diagnostics', () => {
    const entries = parse.events([{ ...envelope, type: 'approval.requested', data: { secret: 'do-not-serialize' } }])
    expect(entries).toHaveLength(1)
    expect(entries[0]).toMatchObject({ type: 'diagnostic.invalid_event', sequence: 0, data: { code: 'invalid_backend_event', message: 'Evento do backend inválido.' } })
    expect(JSON.stringify(entries)).not.toContain('do-not-serialize')
  })
})
