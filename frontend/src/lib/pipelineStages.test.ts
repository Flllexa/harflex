import { describe, expect, it } from 'vitest'
import { pipelineDesignSchema } from './pipelineDesign'
import { designStageOrder, stageGroups, stageLabel, stageOrder } from './pipelineStages'
import { designPipeline, designReadback } from '../test/pipelineDesignFixture'

describe('pipeline stages', () => {
  it('puts Brainstorm between SPEC and Plan', () => {
    expect(stageOrder).toEqual(['discovery', 'spec', 'brainstorm', 'plan', 'code', 'eval', 'prs'])
    expect(stageOrder.map(stageLabel)).toEqual(['Discovery', 'SPEC', 'Brainstorm', 'Plan', 'Code', 'QA', 'PRs'])
    expect(designStageOrder).toEqual(['discovery', 'spec', 'brainstorm', 'plan'])
  })

  it('groups the stages as preparation, execution and delivery', () => {
    expect(stageGroups.map(group => [group.id, group.label, group.stages])).toEqual([
      ['prepare', 'Preparação', ['discovery', 'spec', 'brainstorm', 'plan']],
      ['execute', 'Execução', ['code', 'eval']],
      ['deliver', 'Entrega', ['prs']],
    ])
    expect(stageGroups.flatMap(group => group.stages)).toEqual(stageOrder)
  })

  it('reads a document workspace with four documents and refuses one with three', () => {
    const design = designReadback(designPipeline())
    expect(pipelineDesignSchema.parse(design).documents.brainstorm.stage).toBe('brainstorm')
    const three = structuredClone(design)
    delete (three.documents as Record<string, unknown>).brainstorm
    delete (three.versions as Record<string, unknown>).brainstorm
    expect(pipelineDesignSchema.safeParse(three).success).toBe(false)
  })
})
