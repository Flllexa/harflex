import type { PipelineStage } from './backend'

/** The stages of a pipeline, in order. The identifier of QA stays "eval" because saved pipelines already hold it. */
export const stageOrder: readonly PipelineStage[] = ['discovery', 'spec', 'brainstorm', 'plan', 'code', 'eval', 'prs']

/** The documents the preparation writes before Code, in order. */
export const designStageOrder = ['discovery', 'spec', 'brainstorm', 'plan'] as const

/** How each stage is named on screen. */
export const stageNames: Record<PipelineStage, string> = { discovery: 'Discovery', spec: 'SPEC', brainstorm: 'Brainstorm', plan: 'Plan', code: 'Code', eval: 'QA', prs: 'PRs' }

export function stageLabel(stage: PipelineStage): string {
  return stageNames[stage] ?? stage
}

/** The stages as the narrow pipeline bar groups them. Labels are pt-BR keys for t(). */
export const stageGroups: { id: 'prepare' | 'execute' | 'deliver'; label: string; stages: PipelineStage[] }[] = [
  { id: 'prepare', label: 'Preparação', stages: ['discovery', 'spec', 'brainstorm', 'plan'] },
  { id: 'execute', label: 'Execução', stages: ['code', 'eval'] },
  { id: 'deliver', label: 'Entrega', stages: ['prs'] },
]

/** Whether the stage is one of the preparation documents. */
export function isDesignStage(stage: string): stage is (typeof designStageOrder)[number] {
  return (designStageOrder as readonly string[]).includes(stage)
}
