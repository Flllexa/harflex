import { describe, expect, it } from 'vitest'
import { parse } from './backend'
import { designPipeline, designReadback } from '../test/pipelineDesignFixture'

describe('conversational document bridge', () => {
  it('keeps draft versions and manual authorship separate from published Code artifacts', () => {
    const pipeline = designPipeline()
    const result = designReadback(pipeline, true)
    result.documents.spec.author = 'user'
    expect(parse.pipelineDesign(result).documents.spec).toEqual(result.documents.spec)
    const published = { ...pipeline, derivedFromPipelineId: '', discoveryFrozenVersion: 1, currentStage: 'code', artifacts: { ...pipeline.artifacts,
      discovery: { ...pipeline.artifacts.discovery, sourceSessionId: '' },
      spec: { stage: 'spec', version: 1, content: result.documents.spec.content, author: 'user', sourceSessionId: '', updatedAt: result.updatedAt },
      plan: { stage: 'plan', version: 1, content: result.documents.plan.content, author: 'ai', sourceSessionId: 'session-plan', updatedAt: result.updatedAt } } }
    expect(parse.authoringPipeline(published).artifacts.spec?.author).toBe('user')
  })

  it('rejects incomplete document maps and mismatched stage identities', () => {
    const result = designReadback()
    const { plan: _plan, ...withoutPlan } = result.documents
    expect(() => parse.pipelineDesign({ ...result, documents: withoutPlan })).toThrow()
    expect(() => parse.pipelineDesign({ ...result, documents: { ...result.documents, spec: { ...result.documents.spec, stage: 'plan' } } })).toThrow()
  })

  it('rejects invalid digest fences and unknown execution states', () => {
    const result = designReadback(undefined, true)
    expect(() => parse.pipelineDesign({ ...result, documents: { ...result.documents, spec: { ...result.documents.spec, contentDigest: 'wrong' } } })).toThrow()
    expect(() => parse.pipelineDesign({ ...result, state: 'unexpected_state' })).toThrow()
  })
})
