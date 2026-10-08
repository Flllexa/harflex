import { describe, expect, it } from 'vitest'
import { parse } from './backend'

const now = '2026-09-28T12:00:00Z'
const stage = {
  pipelineId: 'pipeline-ai', stage: 'spec', pipelineRevision: 5, discoveryVersion: 1,
  revision: 2, state: 'cancellation_pending', cancellationPending: true, cancellationAttemptId: 'attempt-1',
  artifactVersion: 0, attemptCount: 1, inputBudgetRemaining: 1000, outputBudgetRemaining: 4096,
  attempts: null, artifacts: null, actions: null, createdAt: now, updatedAt: now,
}

describe('SPEC/Plan readback contract', () => {
  it('accepts the exact artifact lifecycle persisted by migration 031', () => {
    for (const status of ['waiting_user', 'approved', 'rejected', 'bypassed']) {
      const content = { summary: 'Resumo', requirements: ['Requisito'], nonGoals: [], acceptanceCriteria: [{ id: 'AC1', criterion: 'Verificado' }] }
      expect(parse.authoringStage({ ...stage, artifacts: [{ version: 1, content, contentHash: 'hash', author: 'ai', attemptId: 'a', sourceSessionId: 's', status, createdAt: now, updatedAt: now }] }).artifacts[0].status).toBe(status)
    }
  })
  it('preserves the durable cancellation fence instead of treating it as a retryable pause', () => {
    expect(parse.authoringStage(stage)).toMatchObject({ state: 'cancellation_pending', cancellationPending: true, cancellationAttemptId: 'attempt-1', attempts: [], artifacts: [], actions: [] })
    expect(() => parse.authoringStage({ ...stage, cancellationPending: undefined })).toThrow()
    expect(() => parse.authoringStage({ ...stage, state: 'unknown' })).toThrow()
  })
  it('accepts structured SPEC sections and refuses raw or wrong-phase JSON', () => {
    const artifact = { version: 1, content: { summary: 'Pagamento auditável', requirements: ['Registrar decisão'], nonGoals: ['Executar pagamento'], acceptanceCriteria: [{ id: 'AC-1', criterion: 'Decisão persistida' }] }, contentHash: 'hash', author: 'ai', attemptId: 'attempt-1', sourceSessionId: 'session-1', status: 'waiting_user', createdAt: now, updatedAt: now }
    const readback = { ...stage, state: 'waiting_user', cancellationPending: false, cancellationAttemptId: '', artifactVersion: 1, artifacts: [artifact] }
    expect(parse.authoringStage(readback).artifacts[0].content).toEqual(artifact.content)
    expect(() => parse.authoringStage({ ...readback, artifacts: [{ ...artifact, content: JSON.stringify(artifact.content) }] })).toThrow()
    expect(() => parse.authoringStage({ ...readback, stage: 'plan' })).toThrow()
  })
})
