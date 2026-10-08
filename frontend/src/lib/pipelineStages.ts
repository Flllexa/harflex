import type { PipelineStage } from './backend'

/** How each stage is named on screen. The identifier of QA stays "eval" because saved pipelines already hold it. */
export const stageNames: Record<PipelineStage, string> = { discovery: 'Discovery', spec: 'SPEC', plan: 'Plan', code: 'Code', eval: 'QA', prs: 'PRs' }
