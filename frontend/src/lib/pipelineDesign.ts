import { z } from 'zod'
import type { SDDModelSelection } from './backend'

export const pipelineDesignStage = z.enum(['discovery', 'spec', 'plan'])
const date = z.iso.datetime({ offset: true })
const choice = z.object({ executor: z.string(), backendId: z.string(), modelId: z.string(), reasoningEffort: z.string(), catalogRevision: z.string(), source: z.string(), destination: z.string(), status: z.string(), confirmUnfiltered: z.boolean(), confirmJitLoad: z.boolean(), maxOutputTokens: z.number().int(), contextLength: z.number().int(), executableVersion: z.string().optional(), workspacePath: z.string().optional(), checkedAt: date })
const document = z.object({ stage: pipelineDesignStage, version: z.number().int().nonnegative(), content: z.string(), contentDigest: z.string().refine(value => value === '' || /^[a-f0-9]{64}$/.test(value), 'Invalid content digest'), author: z.string(), sourceSessionId: z.string(), sourceDigest: z.string(), selection: choice.nullish(), stale: z.boolean(), updatedAt: date })
const version = document.extend({ reason: z.string(), restoredFromVersion: z.number().int().nonnegative(), createdAt: date })
const message = z.object({ id: z.string().min(1), role: z.enum(['user', 'assistant', 'system']), content: z.string(), target: z.string(), attemptId: z.string(), createdAt: date })
const attempt = z.object({ id: z.string().min(1), requestId: z.string(), target: z.string(), status: z.string(), phase: z.string(), selections: z.record(z.string(), choice), sessionIds: z.record(z.string(), z.string()), errorCode: z.string(), createdAt: date, updatedAt: date })
export const pipelineDesignSchema = z.object({ pipelineId: z.string().min(1), workspaceId: z.string().min(1), pipelineRevision: z.number().int().positive(), currentPipelineRevision: z.number().int().positive(), revision: z.number().int().positive(), state: z.string().refine(value => ['ready', 'running', 'paused', 'cancellation_pending', 'approved'].includes(value), 'Unknown document workspace state'), phase: z.string(), activeAttemptId: z.string(), needsDerivation: z.boolean(), documents: z.record(z.string(), document), versions: z.record(z.string(), z.array(version)), messages: z.array(message).nullable().transform(value => value ?? []), attempts: z.array(attempt).nullable().transform(value => value ?? []), createdAt: date, updatedAt: date })
  .refine(value => ['discovery', 'spec', 'plan'].every(stage => value.documents[stage]?.stage === stage && Array.isArray(value.versions[stage])) && Object.keys(value.documents).length === 3, 'Incomplete document workspace')

export type PipelineDesign = z.infer<typeof pipelineDesignSchema>
export type PipelineDesignDocument = z.infer<typeof document>
export type PipelineDesignStage = z.infer<typeof pipelineDesignStage>
export type PipelineDesignRef = { pipelineId: string; requestId: string; pipelineRevision: number; designRevision: number }
export type PreparePipelineDesignInput = { ref: PipelineDesignRef; message: string; target: PipelineDesignStage | 'all'; selections?: Partial<Record<PipelineDesignStage, SDDModelSelection>> }
export type EditPipelineDesignDocumentInput = { ref: PipelineDesignRef; stage: PipelineDesignStage; content: string }
export type RestorePipelineDesignDocumentInput = { ref: PipelineDesignRef; stage: PipelineDesignStage; version: number }
export type ApprovePipelineDesignInput = { ref: PipelineDesignRef; digests: Record<PipelineDesignStage, string> }
