import { z } from 'zod'
import { t } from '../i18n'
import { pipelineDesignSchema, type PipelineDesign, type PreparePipelineDesignInput, type EditPipelineDesignDocumentInput, type RestorePipelineDesignDocumentInput, type ApprovePipelineDesignInput } from './pipelineDesign'
export type { PipelineDesign, PipelineDesignDocument, PipelineDesignStage, PipelineDesignRef, PreparePipelineDesignInput, EditPipelineDesignDocumentInput, RestorePipelineDesignDocumentInput, ApprovePipelineDesignInput } from './pipelineDesign'

const workspace = z.object({ id: z.string().min(1), path: z.string().min(1), profile: z.string() })
const workspaceSummary = workspace.extend({ available: z.boolean(), archived: z.boolean().optional() })
export const providerType = z.enum(['openai', 'openrouter', 'lm_studio', 'ollama', 'generic'])
const providerProfile = z.object({ id: z.string().min(1), name: z.string(), kind: z.string(), providerType, baseUrl: z.string().url(), model: z.string(), hasCredential: z.boolean(), endpointBlocked: z.boolean(), updatedAt: z.iso.datetime({ offset: true }) })
const modelCatalogStatus = z.enum(['complete', 'empty', 'partial', 'interrupted', 'unsupported', 'failed'])
const catalogModel = z.strictObject({
  id: z.string().min(1).max(512), displayName: z.string().max(512), backendId: z.string().min(1).max(128),
  source: z.string().min(1).max(64), availability: z.string().min(1).max(64), loaded: z.boolean().nullish(),
  ownedBy: z.string().max(256).optional(), contextLength: z.number().int().nonnegative().optional(),
  supportedReasoningEfforts: z.array(z.string().max(64)).max(32).nullish(), defaultReasoningEffort: z.string().max(64).optional(),
})
const modelCatalog = z.strictObject({
  backendId: z.string(), source: z.string(), destination: z.string(), profileRevision: z.string(), credentialToken: z.string().regex(/^[a-f0-9]{64}$/).optional(),
  searchTerm: z.string().max(256), models: z.array(catalogModel).max(1000).nullable().transform(value => value ?? []),
  nextCursor: z.string(), checkedAt: z.iso.datetime({ offset: true }), status: modelCatalogStatus,
  complete: z.boolean(), accountFiltered: z.boolean(), errorCode: z.string().max(128).optional(),
})
const openRouterManagementKeyStatus = z.strictObject({ profileId: z.string().min(1), origin: z.string(), configured: z.boolean(), usable: z.boolean(), updatedAt: z.iso.datetime({ offset: true }) })
const settings = z.object({ defaultBackendId: z.string(), defaultModelBackendId: z.string().default(''), defaultModelId: z.string().default('') })
const logEntry = z.object({ cursor: z.number().int().positive(), id: z.string().min(1), sessionId: z.string().min(1), workspaceId: z.string().min(1), sequence: z.number().int().positive(), type: z.string().min(1), createdAt: z.iso.datetime({ offset: true }) })
const repository = z.object({ isRepository: z.boolean(), root: z.string(), branch: z.string(), files: z.array(z.string()), stagedDiff: z.string(), unstagedDiff: z.string(), truncated: z.boolean() })
const worktreeBlocker = z.object({ code: z.string().min(1), detail: z.string(), count: z.number().int().nonnegative() })
const worktree = z.object({
  path: z.string().min(1), name: z.string(), branch: z.string(), head: z.string(),
  isMain: z.boolean(), isCurrent: z.boolean(), detached: z.boolean(), locked: z.boolean(), lockReason: z.string(), missing: z.boolean(), statusKnown: z.boolean(),
  staged: z.number().int().nonnegative(), unstaged: z.number().int().nonnegative(), untracked: z.number().int().nonnegative(), conflicts: z.number().int().nonnegative(),
  changed: z.number().int().nonnegative(), changedFiles: z.array(z.string()), changesTruncated: z.boolean(), operation: z.string(),
  ahead: z.number().int().nonnegative(), behind: z.number().int().nonnegative(), merged: z.boolean(), ignored: z.array(z.string()), ignoredMore: z.number().int().nonnegative(),
  canDelete: z.boolean(), blockers: z.array(worktreeBlocker), canSaveWithAi: z.boolean(), saveBlockers: z.array(worktreeBlocker),
})
const worktreeList = z.object({ isRepository: z.boolean(), root: z.string(), base: z.string(), baseKnown: z.boolean(), currentPath: z.string(), items: z.array(worktree) })
const worktreeGroup = z.object({ workspaceId: z.string().min(1), name: z.string(), path: z.string(), projects: z.array(z.string()), list: worktreeList, error: z.string().optional() })
const projectMemory = z.object({ workspaceId: z.string().min(1), status: z.enum(['', 'reading', 'ready', 'failed', 'needs_model', 'declined']), content: z.string(), sources: z.array(z.string()), backendId: z.string(), modelId: z.string(), errorCode: z.string().optional(), edited: z.boolean(), updatedAt: z.string() })
const deleteWorktreeResult = z.object({ path: z.string(), branch: z.string(), removed: z.boolean(), branchDeleted: z.boolean(), list: worktreeList })
const worktreeSavePlan = z.object({
  workspace,
  path: z.string().min(1), branch: z.string(), base: z.string(), snapshotRef: z.string(), snapshotCommit: z.string(), hadPending: z.boolean(), reason: z.string().min(1), prompt: z.string().min(1),
})
const pipelineStage = z.enum(['discovery', 'spec', 'plan', 'code', 'eval', 'prs'])
/** Who works on one phase of a project's pipelines: a provider profile or a CLI, and a model ('' = the executor's own). */
const stageExecutor = z.strictObject({ workspaceId: z.string().min(1), stage: pipelineStage, backendId: z.string().min(1).max(128), modelId: z.string().max(512), updatedAt: z.iso.datetime({ offset: true }) })
const stageExecutors = z.array(stageExecutor).nullish().transform(value => value ?? [])
const pipelineArtifact = z.object({ stage: pipelineStage, version: z.number().int().positive(), content: z.string(), author: z.enum(['legacy/manual', 'user', 'ai']).optional(), sourceSessionId: z.string().optional(), contentDigest: z.string().optional(), reviewStatus: z.string().optional(), reviewActor: z.string().optional(), reviewFeedback: z.string().optional(), reviewedAt: z.iso.datetime({ offset: true }).optional(), updatedAt: z.iso.datetime({ offset: true }) })
const pipelineArchivedArtifact = z.object({ stage: z.enum(['code', 'eval']), version: z.number().int().positive(), content: z.string(), author: z.string(), sourceSessionId: z.string(), contentDigest: z.string(), reason: z.string(), createdAt: z.iso.datetime({ offset: true }) })
const pipelineExecutionReview = z.object({ stage: z.enum(['code', 'eval']), version: z.number().int().positive(), content: z.string(), contentDigest: z.string(), sourceSessionId: z.string(), decision: z.enum(['approve', 'request_revision']), actor: z.string(), feedback: z.string(), createdAt: z.iso.datetime({ offset: true }) })
const qaLoop = z.object({ pipelineId: z.string(), phase: z.enum(['', 'qa', 'fixing', 'done', 'waiting', 'failed']), round: z.number().int().nonnegative(), message: z.string(), sessionId: z.string().optional(), updatedAt: z.string(), running: z.boolean() })
export type QALoop = z.infer<typeof qaLoop>
/** Who runs a role in the QA loop: an executor and, when it needs one, the confirmed model. */
export type PipelineRoleChoice = { backendId: string; selection?: PipelineRoleModelSelection }
export type FixPipelineFindingsInput = { pipelineId: string; findings: string[]; improvements: string[]; note: string; coder: PipelineRoleChoice; evaluator: PipelineRoleChoice }
export type ResumePipelineFixesInput = { pipelineId: string; coder: PipelineRoleChoice; evaluator: PipelineRoleChoice }
const pullRequestEvent = z.object({ at: z.string(), kind: z.string(), summary: z.string() })
const pullRequest = z.object({ id: z.string().min(1), pipelineId: z.string(), sessionId: z.string(), url: z.string(), title: z.string(), branch: z.string(), state: z.enum(['open', 'merged', 'closed']), watch: z.boolean(),
  lastCheckedAt: z.string().optional(), nextCheckAt: z.string().optional(), checking: z.boolean(), timeline: z.array(pullRequestEvent).nullish().transform(value => value ?? []) })
export type PullRequest = z.infer<typeof pullRequest>
const pipeline = z.object({ id: z.string().min(1), workspaceId: z.string().min(1), kind: z.enum(['legacy', 'ai_authoring']).optional(), preparationExperience: z.literal('conversational').optional(), derivedFromPipelineId: z.string().optional(), discoveryFrozenVersion: z.number().int().nonnegative().optional(), title: z.string(), objective: z.string(), currentStage: z.union([pipelineStage, z.literal('')]), stageStatus: z.record(z.string(), z.enum(['pending', 'active', 'completed', 'skipped', 'failed', 'paused', 'waiting_user'])), revision: z.number().int().positive(), artifacts: z.record(z.string(), pipelineArtifact), archivedArtifacts: z.array(pipelineArchivedArtifact).optional(), executionReviews: z.array(pipelineExecutionReview).optional(), codeAppliedAt: z.iso.datetime({ offset: true }).optional(), codePatchPending: z.boolean().optional(), codeReviewRecoveryStatus: z.enum(['available', 'snapshot_unavailable', 'already_applied', 'source_drift']).optional(), codeReviewRecoveryReason: z.string().optional(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const authoringPipeline = pipeline.refine(
  value => value.kind === 'ai_authoring'
    && value.derivedFromPipelineId !== undefined
    && value.discoveryFrozenVersion !== undefined
    && value.artifacts.discovery?.author === 'user'
    && (value.discoveryFrozenVersion === 0
      ? value.currentStage === 'discovery' && Object.keys(value.artifacts).length === 1
      : value.discoveryFrozenVersion === value.artifacts.discovery?.version)
    && Object.entries(value.artifacts).every(([stage, artifact]) => stage === artifact.stage && (stage === 'discovery'
      ? artifact.author === 'user' && artifact.sourceSessionId === ''
      : artifact.author === 'ai' && !!artifact.sourceSessionId?.trim() || value.preparationExperience === 'conversational' && artifact.author === 'user' && artifact.sourceSessionId === '')),
  { message: 'Invalid authoring pipeline' },
)
const brainstormSelection = z.object({ executor: z.enum(['api', 'codex_cli']).optional(), backendId: z.string(), modelId: z.string(), reasoningEffort: z.string(), catalogRevision: z.string(), source: z.string(), destination: z.string(), status: z.string(), confirmUnfiltered: z.boolean(), confirmJitLoad: z.boolean(), maxOutputTokens: z.number().int(), contextLength: z.number().int(), executableVersion: z.string().optional(), workspacePath: z.string().optional(), checkedAt: z.iso.datetime({ offset: true }) })
const brainstormAttempt = z.object({ id: z.string(), requestId: z.string(), kind: z.string(), status: z.string(), sessionId: z.string(), errorCode: z.string(), discoveryVersion: z.number().int(), synthesisVersion: z.number().int(), selection: brainstormSelection, reservedInputTokens: z.number().int(), reservedOutputTokens: z.number().int(), usage: z.unknown().nullable(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const brainstormTurn = z.object({ number: z.number().int(), questionId: z.string(), question: z.string(), answer: z.string(), sourceSessionId: z.string(), status: z.string(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const brainstormSynthesis = z.object({ version: z.number().int(), discoveryVersion: z.number().int(), content: z.object({ scope: z.string(), decisions: z.array(z.string()).nullable(), openQuestions: z.array(z.string()).nullable() }), sourceSessionId: z.string(), status: z.string(), createdAt: z.iso.datetime({ offset: true }) })
const brainstorm = z.object({ id: z.string().min(1), pipelineId: z.string().min(1), pipelineRevision: z.number().int().positive(), discoveryVersion: z.number().int().positive(), discoveryContent: z.string(), selection: brainstormSelection, state: z.enum(['ready', 'running_question', 'waiting_answer', 'ready_for_synthesis', 'running_synthesis', 'waiting_user', 'paused', 'invalidated', 'skipped_waiting_confirmation', 'approved']), revision: z.number().int().positive(), questionCount: z.number().int().min(0).max(5), currentQuestionId: z.string(), synthesisVersion: z.number().int().nonnegative(), attemptCount: z.number().int().nonnegative(), inputBudgetRemaining: z.number().int(), outputBudgetRemaining: z.number().int(), activeDurationMillis: z.number().int(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }), attempts: z.array(brainstormAttempt).nullable().transform(value => value ?? []), turns: z.array(brainstormTurn).nullable().transform(value => value ?? []), syntheses: z.array(brainstormSynthesis).nullable().transform(value => value ?? []) })
const documentText = z.string().min(1).max(16384)
const documentId = z.string().regex(/^[A-Za-z][A-Za-z0-9_-]{0,63}$/)
const documentStrings = z.array(documentText).max(128)
const specDocument = z.strictObject({ summary: documentText, requirements: documentStrings.min(1), nonGoals: documentStrings, acceptanceCriteria: z.array(z.strictObject({ id: documentId, criterion: documentText })).min(1).max(128) })
const planDocument = z.strictObject({ summary: documentText, tasks: z.array(z.strictObject({ id: documentId, title: documentText, files: documentStrings, steps: documentStrings.min(1), tests: documentStrings.min(1), dependsOn: z.array(documentId).max(128) })).min(1).max(128), risks: documentStrings })
const authoringSource = z.object({ discoveryVersion: z.number().int(), discoveryHash: z.string(), brainstormRunId: z.string(), synthesisVersion: z.number().int(), discoveryBypassReason: z.string(), specVersion: z.number().int(), specHash: z.string(), specBypassReason: z.string(), previousArtifactVersion: z.number().int(), feedback: z.string() })
const authoringAttempt = z.object({ id: z.string().min(1), requestId: z.string(), artifactVersion: z.number().int(), status: z.enum(['running', 'completed', 'failed', 'interrupted']), sessionId: z.string(), errorCode: z.string(), cancellationState: z.enum(['', 'pending', 'confirmed']), source: authoringSource, selection: brainstormSelection, reservedInputTokens: z.number().int(), reservedOutputTokens: z.number().int(), usage: z.unknown().nullable(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const authoringArtifact = z.object({ version: z.number().int().positive(), content: z.union([specDocument, planDocument]), contentHash: z.string(), author: z.literal('ai'), attemptId: z.string().min(1), sourceSessionId: z.string().min(1), status: z.enum(['waiting_user', 'approved', 'rejected', 'bypassed']), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const authoringAction = z.object({ requestId: z.string(), action: z.string(), actor: z.string(), artifactVersion: z.number().int(), feedback: z.string(), reason: z.string(), attemptId: z.string(), resultStageRevision: z.number().int(), resultPipelineRevision: z.number().int(), createdAt: z.iso.datetime({ offset: true }) })
const authoringStage = z.object({ pipelineId: z.string().min(1), stage: z.enum(['spec', 'plan']), pipelineRevision: z.number().int().positive(), discoveryVersion: z.number().int().nonnegative(), revision: z.number().int().nonnegative(), state: z.enum(['pending', 'ready', 'running', 'waiting_user', 'paused', 'approved', 'skipped', 'cancellation_pending']), cancellationPending: z.boolean(), cancellationAttemptId: z.string(), artifactVersion: z.number().int().nonnegative(), attemptCount: z.number().int().nonnegative(), inputBudgetRemaining: z.number().int(), outputBudgetRemaining: z.number().int(), attempts: z.array(authoringAttempt).nullable().transform(v => v ?? []), artifacts: z.array(authoringArtifact).nullable().transform(v => v ?? []), actions: z.array(authoringAction).nullable().transform(v => v ?? []), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) }).refine(value => value.artifacts.every(artifact => (value.stage === 'spec' ? specDocument : planDocument).safeParse(artifact.content).success), { message: 'Document does not match authoring stage' })
// The Go selection DTO always carries the model's effort levels (null when unknown), shared with the Code phase.
const authoringStagePreferenceSelection = z.strictObject({ backendId: z.string(), modelId: z.string(), reasoningEffort: z.string().max(64), supportedReasoningEfforts: z.array(z.string().max(64)).max(32).nullable().optional(), catalogRevision: z.string(), source: z.string(), destination: z.string(), status: z.string(), confirmUnfiltered: z.boolean(), confirmJitLoad: z.boolean(), maxOutputTokens: z.number().int().min(0).max(4096), contextLength: z.number().int().nonnegative(), checkedAt: z.iso.datetime({ offset: true }) })
const authoringStageModelPreference = z.strictObject({
  pipelineId: z.string().min(1), stage: z.enum(['spec', 'plan']), modelMode: z.enum(['inherit', 'override']),
  effortMode: z.enum(['inherit', 'automatic', 'explicit']), explicitEffort: z.string().max(64), preferenceRevision: z.number().int().nonnegative(),
  resolution: z.enum(['ready', 'stale', 'unconfigured']), modelSource: z.string(), effortSource: z.string(), inheritedFrom: z.string(),
  catalogValidationRequired: z.boolean(), errorCode: z.string(), selection: authoringStagePreferenceSelection,
}).refine(value => value.effortMode === 'explicit' ? value.explicitEffort !== '' : value.explicitEffort === '', { message: 'Explicit effort must match its mode' })
const authoringCodeSelection = z.strictObject({
  backendId: z.string(), modelId: z.string(), reasoningEffort: z.string(), supportedReasoningEfforts: z.array(z.string()).nullable(),
  catalogRevision: z.string(), source: z.string(), destination: z.string(), status: z.string(), confirmUnfiltered: z.boolean(),
  confirmJitLoad: z.boolean(), maxOutputTokens: z.number().int().nonnegative(), contextLength: z.number().int().nonnegative(),
  checkedAt: z.iso.datetime({ offset: true }),
})
const authoringCodePreference = z.strictObject({
  pipelineId: z.string().min(1), stage: z.literal('code'), modelMode: z.enum(['inherit', 'override']),
  effortMode: z.enum(['inherit', 'automatic', 'explicit']), explicitEffort: z.string().max(64), preferenceRevision: z.number().int().nonnegative(),
  resolution: z.enum(['ready', 'stale', 'unconfigured']), modelSource: z.string(), effortSource: z.string(), inheritedFrom: z.string(),
  catalogValidationRequired: z.boolean(), errorCode: z.string(), selection: authoringCodeSelection,
}).refine(value => value.effortMode === 'explicit' ? value.explicitEffort !== '' : value.explicitEffort === '', { message: 'Explicit effort must match its mode' })
const sha256Hash = z.string().regex(/^[a-f0-9]{64}$/)
const authoringCodeManifest = z.strictObject({
  version: z.number().int().nonnegative(),
  entries: z.array(z.strictObject({ path: z.string(), type: z.enum(['file', 'directory', 'symlink']), mode: z.number().int().nonnegative(), size: z.number().int().nonnegative(), sha256: sha256Hash, target: z.string().optional() })),
  excluded: z.array(z.strictObject({ path: z.string(), reason: z.string() })),
  fileCount: z.number().int().nonnegative(), totalBytes: z.number().int().nonnegative(), hash: sha256Hash,
})
const authoringCodePreflight = z.strictObject({
  pipelineId: z.string().min(1), workspaceId: z.string().min(1), pipelineRevision: z.number().int().positive(),
  codePreferenceRevision: z.number().int().nonnegative(), codePreference: authoringCodePreference,
  sourcePath: z.string().min(1), privateParentPath: z.string().min(1), manifest: authoringCodeManifest,
  manifestHash: sha256Hash, codeSelectionHash: sha256Hash,
}).refine(value => value.manifest.hash === value.manifestHash && value.codePreference.pipelineId === value.pipelineId && value.codePreference.preferenceRevision === value.codePreferenceRevision, { message: 'Preflight snapshot bindings do not match' })
const authoringCodeCopyPreparation = z.strictObject({
  id: z.string().min(1), pipelineId: z.string().min(1), workspaceId: z.string().min(1), requestId: z.string().min(1),
  pipelineRevision: z.number().int().positive(), codePreferenceRevision: z.number().int().nonnegative(), codeSelectionHash: sha256Hash,
  codePreference: authoringCodePreference, sourcePath: z.string().min(1), privatePath: z.string().min(1),
  manifest: authoringCodeManifest, manifestHash: sha256Hash,
  status: z.enum(['preparing', 'prepared', 'interrupted', 'stale', 'failed']), errorCode: z.string().optional(),
  createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }),
}).refine(value => value.manifest.hash === value.manifestHash && value.codePreference.pipelineId === value.pipelineId && value.codePreference.preferenceRevision === value.codePreferenceRevision, { message: 'Preparation snapshot bindings do not match' })
const authoringCodeCopyPreparations = z.strictObject({
  pipelineId: z.string().min(1), attemptCount: z.number().int().nonnegative(), maxAttemptCount: z.number().int().positive(),
  attempts: z.array(authoringCodeCopyPreparation),
}).refine(value => value.attemptCount === value.attempts.length && value.attemptCount <= value.maxAttemptCount && value.attempts.every(attempt => attempt.pipelineId === value.pipelineId), { message: 'Invalid Code preparation history' })
const authoringCodeRun = z.strictObject({
  id: z.string().min(1), pipelineId: z.string().min(1), preparationId: z.string().min(1), preparationRequestId: z.string().min(1), requestId: z.string().min(1),
  pipelineRevision: z.number().int().nonnegative(), planStageRevision: z.number().int().nonnegative(), planArtifactVersion: z.number().int().nonnegative(),
  codePreferenceRevision: z.number().int().nonnegative(), codeSelectionHash: sha256Hash, manifestHash: sha256Hash, planSourceHash: sha256Hash, privatePath: z.string(),
  preference: authoringCodePreference,
  limits: z.strictObject({ maxPromptBytes: z.number().int().nonnegative(), maxOutputTokens: z.number().int().nonnegative(), maxOutputTokensPerTurn: z.number().int().nonnegative(), maxTurns: z.number().int().nonnegative(), maxToolCalls: z.number().int().nonnegative(), timeoutMillis: z.number().int().nonnegative() }),
  sessionId: z.string(), status: z.enum(['running', 'cancelling', 'completed', 'failed', 'cancelled', 'interrupted']), errorCode: z.string().optional(),
  cancellationPending: z.boolean(), usage: z.strictObject({ inputTokens: z.number().int().nonnegative(), outputTokens: z.number().int().nonnegative(), costUsd: z.number().nullable() }).nullable(),
  baseline: authoringCodeManifest, result: authoringCodeManifest.optional(), sourceManifest: authoringCodeManifest.optional(), sourceDrift: z.boolean(),
  sourceReadbackStatus: z.enum(['matched', 'drifted', 'pending', 'unavailable']),
  changes: z.array(z.strictObject({ path: z.string(), kind: z.enum(['added', 'deleted', 'content', 'mode', 'type', 'target']) })),
  patchHash: z.union([z.literal(''), sha256Hash]),
  createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }),
}).refine(value => value.status === 'completed'
  ? sha256Hash.safeParse(value.patchHash).success && value.result?.hash !== undefined && value.sourceManifest?.hash === value.manifestHash && !value.sourceDrift && value.sourceReadbackStatus === 'matched'
  : value.patchHash === '')
  .refine(value => value.preference.pipelineId === value.pipelineId && value.preference.preferenceRevision === value.codePreferenceRevision && value.baseline.hash === value.manifestHash, { message: 'Code run admission snapshot bindings do not match' })
const authoringCodeRuns = z.strictObject({ pipelineId: z.string().min(1), runs: z.array(authoringCodeRun) })
  .refine(value => value.runs.every(run => run.pipelineId === value.pipelineId), { message: 'Code run history contains another pipeline' })
const authoringCodeRunPatchPage = z.strictObject({
  pipelineId: z.string().min(1), runId: z.string().min(1), baselineHash: sha256Hash,
  sourceManifestHash: sha256Hash, resultManifestHash: sha256Hash, patchHash: sha256Hash,
  sourceReadbackStatus: z.enum(['matched', 'drifted', 'unchecked', 'unavailable']), cursor: z.number().int().nonnegative(), nextCursor: z.number().int().nonnegative().nullable(),
  totalBytes: z.number().int().positive().max(64 * 1024 * 1024), content: z.string().min(1).max(256 * 1024),
}).refine(value => {
  const next = value.nextCursor
  const contentBytes = new TextEncoder().encode(value.content).byteLength
  const end = value.cursor + contentBytes
  const readbackStatusMatchesCursor = value.cursor === 0
    ? value.sourceReadbackStatus !== 'unchecked'
    : value.sourceReadbackStatus === 'unchecked'
  return contentBytes <= 256 * 1024 && readbackStatusMatchesCursor && value.baselineHash === value.sourceManifestHash && (next === null ? end === value.totalBytes : next === end && next < value.totalBytes)
}, { message: 'Patch page cursor does not match its content' })
const backend = z.object({ id: z.string().min(1), name: z.string(), kind: z.enum(['api', 'cli']), available: z.boolean(), professionalAvailable: z.boolean().optional() })
const credentialProbe = z.object({ status: z.enum(['ready', 'unconfigured', 'degraded', 'unavailable']), checked: z.number().int().nonnegative(), missing: z.number().int().nonnegative() })
const session = z.object({ id: z.string().min(1), workspaceId: z.string().min(1), backendId: z.string().min(1), status: z.string().min(1),
  purpose: z.enum(['chat', 'preparation', 'code', 'evaluation']).optional(), title: z.string().max(400).optional(),
  resumable: z.boolean(), pinned: z.boolean().optional(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const chatProject = z.object({ workspace: workspaceSummary, chats: z.array(session), total: z.number().int().nonnegative() })
const pipelineStageActivity = z.object({ pipelineId: z.string().min(1), workspaceId: z.string().min(1), stage: pipelineStage, status: z.string(), phase: z.string(), sessionId: z.string(), modelId: z.string(), attemptStatus: z.string().optional(), errorCode: z.string().optional(), updatedAt: z.iso.datetime({ offset:true }) })
const sessionModelSelection = z.strictObject({ sessionId: z.string().min(1), backendId: z.string().min(1), modelId: z.string().min(1).max(512),
  supportedReasoningEfforts: z.array(z.string().max(64)).max(32).optional(),
  reasoningEffort: z.string().max(64), source: z.string().min(1), destination: z.string(),
  status: z.enum(['', 'listed', 'listed_unfiltered', 'unverified_manual']),
  confirmUnverifiedManual: z.boolean(), confirmUnfiltered: z.boolean(), confirmJitLoad: z.boolean(),
  maxOutputTokens: z.number().int().min(0).max(32768), contextLength: z.number().int().min(0), checkedAt: z.iso.datetime({ offset: true }) })
const pipelineSession = z.object({ session, prompt: z.string().min(1), role: z.enum(['coder', 'evaluator', 'publisher']) })
const pipelineCodeCopyPreview = z.object({ isGit: z.boolean(), fileCount: z.number().int().nonnegative(), totalBytes: z.number().int().nonnegative(), excludedPaths: z.array(z.string()), unsafePaths: z.array(z.string()) })
const agent = z.object({ id: z.string().min(1), name: z.string(), description: z.string(), instructions: z.string(), backendId: z.string().min(1), allowedTools: z.array(z.string()), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const delegationLink = z.object({ id: z.string().min(1), parentSessionId: z.string().min(1), childSessionId: z.string().min(1), agentId: z.string().min(1), depth: z.number().int().positive(), createdAt: z.iso.datetime({ offset: true }) })
const delegation = delegationLink.extend({ taskPrompt: z.string().default(''), promptCount: z.number().int().nonnegative(), promptLimit: z.number().int().positive(), timeoutSeconds: z.number().int().positive(), status: z.enum(['ready', 'running', 'awaiting_approval', 'completed', 'failed', 'cancelled', 'paused', 'history_too_large']), result: z.string(), errorCode: z.string() })
const delegatedSession = z.object({ session, prompt: z.string().min(1), depth: z.number().int().positive(), created: z.boolean() })
const workflowStep = z.object({ name: z.string().min(1), prompt: z.string().min(1) })
const workflow = z.object({ id: z.string().min(1), workspaceId: z.string().min(1), name: z.string().min(1), steps: z.array(workflowStep).min(1), revision: z.number().int().positive(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const workflowRun = z.object({ id: z.string().min(1), workflowId: z.string().min(1), workspaceId: z.string().min(1), backendId: z.string().min(1), steps: z.array(workflowStep).min(1), currentStep: z.number().int().nonnegative(), status: z.enum(['ready', 'running', 'waiting_user', 'paused', 'completed', 'cancelled']), lastSessionId: z.string(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const schedule = z.strictObject({ id: z.string().min(1), workspaceId: z.string().min(1), name: z.string().min(1), targetKind: z.enum(['prompt', 'workflow']), workflowId: z.string(), backendId: z.string().min(1), prompt: z.string(), frequency: z.enum(['daily', 'once']), timezone: z.string().min(1), localDate: z.string(), localTime: z.string(), missedPolicy: z.enum(['skip', 'run_once']), enabled: z.boolean(), allowCli: z.boolean(), nextRunAt: z.iso.datetime({ offset: true }).nullable(), revision: z.number().int().positive(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const scheduleJob = z.strictObject({ id: z.string().min(1), scheduleId: z.string().min(1), workspaceId: z.string().min(1), trigger: z.enum(['scheduled', 'manual']), status: z.enum(['queued', 'running', 'waiting_user', 'cancel_requested', 'completed', 'failed', 'cancelled', 'skipped', 'interrupted']), errorCode: z.string(), workflowRunId: z.string(), sessionId: z.string(), dueAt: z.iso.datetime({ offset: true }), createdAt: z.iso.datetime({ offset: true }), startedAt: z.iso.datetime({ offset: true }).nullable(), finishedAt: z.iso.datetime({ offset: true }).nullable() })
const skill = z.object({ id: z.string().min(1), workspaceId: z.string().min(1), name: z.string().min(1), description: z.string(), content: z.string().min(1), enabled: z.boolean(), revision: z.number().int().positive(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const knowledgeDocument = z.object({ id: z.string().min(1), workspaceId: z.string().min(1), path: z.string().min(1), sourceSize: z.number().int().nonnegative(), sourceModifiedAt: z.iso.datetime({ offset: true }), indexedAt: z.iso.datetime({ offset: true }), chunkCount: z.number().int().positive() })
const knowledgeHit = z.object({ documentId: z.string().min(1), path: z.string().min(1), lineStart: z.number().int().positive(), snippet: z.string().min(1), sourceModifiedAt: z.iso.datetime({ offset: true }), indexedAt: z.iso.datetime({ offset: true }) })
const knowledgeEmbeddingProfile = z.object({ configured: z.boolean(), kind: z.string(), baseUrl: z.string(), model: z.string(), fingerprint: z.string(), progress: z.object({ total: z.number().int().nonnegative(), indexed: z.number().int().nonnegative(), dimension: z.number().int().nonnegative() }) })
const knowledgeSearch = z.object({ mode: z.enum(['textual', 'textual_fallback', 'semantic', 'hybrid']), hits: z.array(knowledgeHit) })
const localChannel = z.object({ id: z.string().min(1), workspaceId: z.string().min(1), name: z.string().min(1), folder: z.string().min(1), status: z.enum(['ready', 'error']), lastError: z.string(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const channelMessage = z.object({ id: z.string().min(1), channelId: z.string().min(1), direction: z.enum(['incoming', 'outgoing']), fileName: z.string().min(1), content: z.string(), status: z.enum(['received', 'pending', 'sent', 'failed']), errorCode: z.string(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const channelImport = z.object({ channel: localChannel, imported: z.number().int().nonnegative(), failed: z.number().int().nonnegative() })
const mcpTool = z.object({ name: z.string().min(1), agentName: z.string().min(1), description: z.string(), schema: z.unknown() })
const mcpServer = z.object({ id: z.string().min(1), workspaceId: z.string().min(1), name: z.string().min(1), transport: z.enum(['http', 'stdio']), command: z.string(), args: z.array(z.string()), url: z.string(), tokenEnvVar: z.string(), authScheme: z.enum(['bearer', 'basic']), enabled: z.boolean(), tools: z.array(mcpTool), hasCredential: z.boolean(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }) })
const auditPath = z.string().min(1).max(4096).refine(value => value.trim().length > 0 && !/[\u0000-\u001f\u007f]/.test(value), 'Invalid destination')
const id = z.string().min(1)
const envelope = z.object({ id, streamId: id, sequence: z.number().int().positive(), type: id, data: z.unknown(), createdAt: z.iso.datetime({ offset: true }) })
const approval = z.object({ approvalId: id, toolCallId: id, name: id, risk: id, arguments: z.unknown() })
const toolCall = z.object({ id, name: id, arguments: z.unknown() })
const toolIdentity = z.object({ toolCallId: id, name: id })
const toolContent = z.object({ text: z.string().optional() }).passthrough()
const toolDetails = z.object({ path: z.string().optional(), diff: z.string().optional() }).passthrough()
const terminal = z.object({ reason: z.string() })
const externalTerminal = z.object({ adapter: id, reason: z.string().optional() })
const approvalDecision = z.object({ approvalId: id, toolCallId: id })
const payloads: Record<string, z.ZodType> = {
  'assistant.delta': z.object({ delta: z.string() }),
  'message.user': z.object({ id: z.string().optional(), role: z.literal('user'), content: z.string() }),
  'message.assistant': z.object({ id: z.string().optional(), role: z.literal('assistant'), content: z.string(), toolCalls: z.array(toolCall).optional() }),
  'tool.called': toolIdentity,
  'tool.updated': z.object({ toolCallId: id, stream: z.string(), text: z.string() }),
  'tool.completed': toolIdentity.extend({ content: toolContent, details: toolDetails.optional() }),
  'tool.failed': toolIdentity.extend({ error: z.string(), errorCode: id.optional(), content: toolContent.nullish(), details: toolDetails.optional() }),
  'tool.denied': toolIdentity.extend({ error: z.string(), reason: id }),
  'tool.skipped': z.union([toolIdentity.extend({ error: z.string(), reason: id }), toolIdentity.extend({ error: z.string(), errorCode: z.literal('not_executed') })]),
  'approval.requested': approval,
  'approval.approved': approvalDecision,
  'approval.denied': approvalDecision,
  'run.started': z.object({}),
  'run.completed': z.union([terminal, z.object({adapter:z.enum(['codex','claude']),purpose:z.literal('documents_no_tools'),threadId:z.string()})]),
  'run.failed': terminal,
  'run.cancelled': terminal,
  'run.interrupted': terminal,
  'external.run.started': z.object({ adapter: id }),
  'external.run.completed': externalTerminal,
  'external.run.failed': externalTerminal,
  'external.run.cancelled': externalTerminal,
  'external.run.interrupted': terminal,
  'external.event': z.object({ type: id, text: z.string().max(1024 * 1024).optional(), raw: z.unknown(), sessionId: z.string().optional(),
    messageId: z.string().min(1).max(256).optional(), mode: z.enum(['delta', 'replace']).optional(),
  }).refine(value => !value.mode || (value.type === 'assistant.message' && !!value.messageId))
    .refine(value => value.type !== 'assistant.message' || !!value.text || (value.mode === 'replace' && !!value.messageId)),
  'external.session.bound': z.object({ sessionId: id }),
  'usage.recorded': z.object({ inputTokens: z.number().int().nonnegative(), outputTokens: z.number().int().nonnegative() }),
}
// Validate by event discriminator; unknown event kinds remain inspectable journal entries.
const event = envelope.refine(value => !Object.prototype.hasOwnProperty.call(payloads, value.type) || payloads[value.type].safeParse(value.data).success, { message: 'Invalid event payload' })
const runResult = z.object({
  status: z.enum(['completed', 'awaiting_approval', 'cancelled', 'failed']),
  reason: z.string().optional(),
  code: z.string().optional(),
  approval: approval.nullish(),
})

export type Workspace = z.infer<typeof workspace>
/** Permission profiles a person can choose for a project; sandbox stays unavailable. */
export type WorkspaceProfile = 'ask' | 'trusted_workspace' | 'full_access'
export type WorkspaceSummary = z.infer<typeof workspaceSummary>
export type ProviderProfile = z.infer<typeof providerProfile>
export type ProviderType = z.infer<typeof providerType>
export type ModelCatalogResult = z.infer<typeof modelCatalog>
export type ModelCatalogQuery = { profileId: string; searchTerm: string; refresh: boolean; draft?: { providerType: ProviderType; baseUrl: string; apiKey: string } }
export type CLIModelCatalogQuery = { workspaceId: string; backendId: string }
export type OpenRouterManagementKeyStatus = z.infer<typeof openRouterManagementKeyStatus>
export type Settings = z.infer<typeof settings>
export type LogEntry = z.infer<typeof logEntry>
export type LogQuery = { workspaceId: string; type: string; beforeId: number; limit: number }
export type Repository = z.infer<typeof repository>
export type WorktreeBlocker = z.infer<typeof worktreeBlocker>
export type Worktree = z.infer<typeof worktree>
export type WorktreeList = z.infer<typeof worktreeList>
export type WorktreeGroup = z.infer<typeof worktreeGroup>
export type ProjectMemory = z.infer<typeof projectMemory>
export type ChatProject = z.infer<typeof chatProject>
export type TerminalInfo = { id: string; shell: string; path: string }
/** One chunk of a terminal's output (base64), or its end. */
export type TerminalOutput = { id: string; data?: string; exited?: boolean; code?: number }
export type DeleteWorktreeResult = z.infer<typeof deleteWorktreeResult>
export type WorktreeSavePlan = z.infer<typeof worktreeSavePlan>
export type DeleteWorktreeInput = { workspaceId: string; path: string; deleteBranch: boolean; acknowledgeIgnored: boolean }
export type PrepareWorktreeSaveInput = { workspaceId: string; path: string }
export type Pipeline = z.infer<typeof pipeline>
export type PipelineStageActivity = z.infer<typeof pipelineStageActivity>
export type PipelineStage = z.infer<typeof pipelineStage>
export type StageExecutor = z.infer<typeof stageExecutor>
export type SaveStageExecutorInput = { workspaceId: string; stage: PipelineStage; backendId: string; modelId: string }
/** The agent that runs a stage: the Coder writes, the Evaluator is QA, the Publisher opens the pull requests. */
export type PipelineRole = 'coder' | 'evaluator' | 'publisher'
export type PipelineSession = z.infer<typeof pipelineSession>
export type PipelineCodeCopyPreview = z.infer<typeof pipelineCodeCopyPreview>
export type CreateAuthoringPipelineInput = { workspaceId: string; requestId: string; discovery: string }
export type DeriveAuthoringPipelineInput = { parentPipelineId: string; requestId: string; expectedRevision: number; discovery: string }
export type ReviseAuthoringDiscoveryInput = { pipelineId: string; expectedRevision: number; expectedVersion: number; discovery: string }
export type Brainstorm = z.infer<typeof brainstorm>
export type BrainstormRef = { runId: string; requestId: string; pipelineRevision: number; runRevision: number; discoveryVersion: number }
export type APIModelSelection = { executor: 'api'; backendId: ''; profileId: string; modelId: string; catalogRevision: string; source: string; destination: string; checkedAt: string; credentialToken: string; reasoningEffort: string; confirmUnverifiedManual: boolean; confirmUnfiltered: boolean; confirmJitLoad: boolean; maxOutputTokens: number }
export type CodexCLIModelSelection = { executor: 'codex_cli'; backendId: DocumentCLI; profileId: ''; modelId: string; catalogRevision: string; source: DocumentCLISource; destination: ''; checkedAt: string; credentialToken: ''; reasoningEffort: string; confirmUnverifiedManual: false; confirmUnfiltered: false; confirmJitLoad: false; maxOutputTokens: number }
export type SDDModelSelection = APIModelSelection | CodexCLIModelSelection
export type PipelineRoleModelSelection = SDDModelSelection | { executor: 'cli'; backendId: string; profileId: ''; modelId: string; catalogRevision: string; source: string; destination: ''; checkedAt: string; credentialToken: ''; reasoningEffort: string; confirmUnverifiedManual: false; confirmUnfiltered: false; confirmJitLoad: false; maxOutputTokens: 0 }
export type PipelineExecutionReviewInput = { pipelineId: string; requestId: string; stage: 'code' | 'eval'; artifactVersion: number; artifactDigest: string; decision: 'approve' | 'request_revision'; feedback: string; pipelineRevision: number }
export type ReopenPipelineCodeReviewInput = { pipelineId: string; artifactVersion: number; artifactDigest: string; pipelineRevision: number }
/**
 * Go decodes `checkedAt` as a time.Time, which accepts RFC 3339 text and null but rejects "".
 * Calls that carry no model choice (the server resolves the saved phase preference) send the
 * zero time so the binding decodes; sending "" makes the whole call fail before it runs.
 */
export const NO_CATALOG_TIME = '0001-01-01T00:00:00Z'
export type GenerateBrainstormInput = { ref: BrainstormRef; selection: SDDModelSelection }
export type AuthoringStage = z.infer<typeof authoringStage>
export type AuthoringStageModelPreference = z.infer<typeof authoringStageModelPreference>
export type AuthoringCodeRun = z.infer<typeof authoringCodeRun>
export type AuthoringCodeRuns = z.infer<typeof authoringCodeRuns>
export type AuthoringCodePreflight = z.infer<typeof authoringCodePreflight>
export type AuthoringCodeCopyPreparation = z.infer<typeof authoringCodeCopyPreparation>
export type AuthoringCodeCopyPreparations = z.infer<typeof authoringCodeCopyPreparations>
export type AuthoringCodeRunPatchPage = z.infer<typeof authoringCodeRunPatchPage>
export type PreflightAuthoringCodeInput = { pipelineId: string; expectedPipelineRevision: number; expectedCodePreferenceRevision: number }
export type PrepareAuthoringCodeInput = { pipelineId: string; requestId: string; expectedPipelineRevision: number; expectedCodePreferenceRevision: number; expectedCodeSelectionHash: string; expectedManifestHash: string; confirmCopy: boolean }
export type GetAuthoringCodeCopyPreparationsInput = { pipelineId: string }
export type GetAuthoringCodeRunPatchInput = { pipelineId: string; runId: string; cursor: number }
export type StartAuthoringCodeInput = { pipelineId: string; preparationId: string; preparationRequestId: string; requestId: string; expectedPipelineRevision: number; expectedPlanStageRevision: number; expectedPlanArtifactVersion: number; expectedCodePreferenceRevision: number; expectedCodeSelectionHash: string; expectedManifestHash: string }
export type CancelAuthoringCodeInput = { pipelineId: string; attemptId: string }
export type GetAuthoringCodeRunsInput = { pipelineId: string }
export type AuthoringStageRef = { pipelineId: string; stage: 'spec' | 'plan'; requestId: string; pipelineRevision: number; stageRevision: number; discoveryVersion: number; artifactVersion: number }
export type AuthoringModelConsentBinding = { backendId: string; modelId: string; catalogRevision: string; source: string; destination: string }
export type GenerateAuthoringStageInput = { ref: AuthoringStageRef; selection: SDDModelSelection; feedback: string; confirmUnfiltered: boolean; confirmJitLoad: boolean; consentBinding?: AuthoringModelConsentBinding }
export type AuthoringStageModelPreferenceQuery = { pipelineId: string; stage: 'spec' | 'plan' }
export type SaveAuthoringStageModelPreferenceInput = AuthoringStageModelPreferenceQuery & {
  expectedRevision: number; modelMode: 'inherit' | 'override'; effortMode: 'inherit' | 'automatic' | 'explicit'; explicitEffort: string; selection: APIModelSelection
}
export type AuthoringStageDecisionInput = { ref: AuthoringStageRef; reason: string; attemptId: string }
export type Agent = z.infer<typeof agent>
export type AgentInput = Pick<Agent, 'name' | 'description' | 'instructions' | 'backendId' | 'allowedTools'> & { id?: string }
export type Delegation = z.infer<typeof delegation>
export type DelegatedSession = z.infer<typeof delegatedSession>
export type Workflow = z.infer<typeof workflow>
export type WorkflowRun = z.infer<typeof workflowRun>
export type WorkflowStep = z.infer<typeof workflowStep>
export type Schedule = z.infer<typeof schedule>
export type ScheduleInput = Pick<Schedule, 'workspaceId' | 'name' | 'targetKind' | 'workflowId' | 'backendId' | 'prompt' | 'frequency' | 'timezone' | 'localDate' | 'localTime' | 'missedPolicy' | 'enabled' | 'allowCli'> & { id?: string; revision?: number }
export type ScheduleJob = z.infer<typeof scheduleJob>
export type Skill = z.infer<typeof skill>
export type SkillInput = Pick<Skill, 'workspaceId' | 'name' | 'description' | 'content' | 'enabled'> & { id?: string }
export type ImportSkillInput = { workspaceId: string; path: string; enabled: boolean }
export type KnowledgeDocument = z.infer<typeof knowledgeDocument>
export type KnowledgeHit = z.infer<typeof knowledgeHit>
export type KnowledgeEmbeddingProfile = z.infer<typeof knowledgeEmbeddingProfile>
export type KnowledgeSearch = z.infer<typeof knowledgeSearch>
export type KnowledgeEmbeddingProfileInput = { workspaceId: string; kind: 'ollama' | 'lm_studio'; baseUrl: string; model: string }
export type LocalChannel = z.infer<typeof localChannel>
export type ChannelMessage = z.infer<typeof channelMessage>
export type ChannelImport = z.infer<typeof channelImport>
export type KnowledgeDocumentInput = { workspaceId: string; documentId: string }
export type SearchKnowledgeInput = { workspaceId: string; documentId: string; query: string; limit: number }
export type MCPServer = z.infer<typeof mcpServer>
export type MCPServerInput = Pick<MCPServer, 'workspaceId' | 'name' | 'transport' | 'command' | 'args' | 'url' | 'tokenEnvVar' | 'authScheme'> & { id?: string; token: string }
export type BackendOption = z.infer<typeof backend>
export type CredentialProbe = z.infer<typeof credentialProbe>
export type Session = z.infer<typeof session>
export type SessionModelSelection = z.infer<typeof sessionModelSelection>
export type AgentEvent = z.infer<typeof event>
export type Approval = z.infer<typeof approval>
export type RunResult = z.infer<typeof runResult>
export type ProviderProfileInput = { id: string; name: string; providerType: ProviderType; baseUrl: string; model: string; apiKey: string; clearCredential: boolean }

/** Local diagnostics use sequence zero and never advance the durable replay cursor. */
export const LOCAL_DIAGNOSTIC_STREAM = 'frontend:diagnostics'
let diagnosticSequence = 0
export function invalidEventDiagnostic(): AgentEvent {
  return { id: `frontend-diagnostic-${++diagnosticSequence}`, streamId: LOCAL_DIAGNOSTIC_STREAM, sequence: 0,
    type: 'diagnostic.invalid_event', data: { code: 'invalid_backend_event', message: t('Evento do backend inválido.') }, createdAt: '' }
}

/** Everything the UI may ask of the desktop backend. Tests inject a fake. */
export interface Backend {
  openWorkspace(path: string): Promise<Workspace>
  listWorkspaces(): Promise<WorkspaceSummary[]>
  setWorkspaceProfile(workspaceId: string, profile: WorkspaceProfile, options?: { confirmFullAccess?: boolean }): Promise<Workspace>
  // Archiving only takes the project off the Projects list; opening the same folder again also brings it back.
  setWorkspaceArchived(workspaceId: string, archived: boolean): Promise<WorkspaceSummary>
  listProviderProfiles(): Promise<ProviderProfile[]>
  queryHTTPModelCatalog(query: ModelCatalogQuery, signal?: AbortSignal): Promise<ModelCatalogResult>
  queryCLIModelCatalog(query: CLIModelCatalogQuery, signal?: AbortSignal): Promise<ModelCatalogResult>
  getOpenRouterManagementKeyStatus(profileId: string): Promise<OpenRouterManagementKeyStatus>
  saveOpenRouterManagementKey(profileId: string, key: string): Promise<OpenRouterManagementKeyStatus>
  clearOpenRouterManagementKey(profileId: string): Promise<void>
  probeCredentialStore(workspaceId: string): Promise<CredentialProbe>
  getSettings(): Promise<Settings>
  saveSettings(settings: Settings): Promise<Settings>
  listLogEvents(query: LogQuery): Promise<LogEntry[]>
  inspectRepository(workspaceId: string): Promise<Repository>
  listWorktrees(workspaceId: string): Promise<WorktreeList>
  // Every repository among the active projects, one group each; actions go through the group's workspaceId.
  listAllWorktrees(): Promise<WorktreeGroup[]>
  // What the AI read about a project (technologies, domains, repositories, services); the document phases use it.
  getProjectMemory(workspaceId: string): Promise<ProjectMemory>
  // Every active project with its recent conversations (pinned ones always included), for the Casual sidebar.
  listChatProjects(perProject?: number): Promise<ChatProject[]>
  setSessionPinned(sessionId: string, pinned: boolean): Promise<void>
  /** Opens the project's folder in the system file manager. */
  revealWorkspace(workspaceId: string): Promise<void>
  // The person's own shell in the project folder, for the terminal panel.
  startTerminal(workspaceId: string, cols: number, rows: number): Promise<TerminalInfo>
  writeTerminal(id: string, data: string): Promise<void>
  resizeTerminal(id: string, cols: number, rows: number): Promise<void>
  closeTerminal(id: string): Promise<void>
  onTerminalOutput(listener: (output: TerminalOutput) => void): () => void
  refreshProjectMemory(workspaceId: string): Promise<ProjectMemory>
  declineProjectMemory(workspaceId: string): Promise<ProjectMemory>
  saveProjectMemory(workspaceId: string, content: string): Promise<ProjectMemory>
  deleteWorktree(input: DeleteWorktreeInput): Promise<DeleteWorktreeResult>
  pruneWorktrees(workspaceId: string): Promise<WorktreeList>
  prepareWorktreeSave(input: PrepareWorktreeSaveInput): Promise<WorktreeSavePlan>
  createPipeline(input: { workspaceId: string; title: string; objective: string }): Promise<Pipeline>
  createAuthoringPipeline(input: CreateAuthoringPipelineInput): Promise<Pipeline>
  openPipelineDesign(pipelineId: string): Promise<PipelineDesign>
  preparePipelineDesign(input: PreparePipelineDesignInput): Promise<PipelineDesign>
  editPipelineDesignDocument(input: EditPipelineDesignDocumentInput): Promise<PipelineDesign>
  restorePipelineDesignDocument(input: RestorePipelineDesignDocumentInput): Promise<PipelineDesign>
  cancelPipelineDesign(input: { pipelineId: string; attemptId: string }): Promise<PipelineDesign>
  approvePipelineDesign(input: ApprovePipelineDesignInput): Promise<Pipeline>
  deriveAuthoringPipeline(input: DeriveAuthoringPipelineInput): Promise<Pipeline>
  reviseAuthoringDiscovery(input: ReviseAuthoringDiscoveryInput): Promise<Pipeline>
  startBrainstorming(input: { pipelineId: string; requestId: string; pipelineRevision: number; discoveryVersion: number; selection: SDDModelSelection }): Promise<Brainstorm>
  getBrainstorming(input: { runId: string; pipelineId: string; discoveryVersion: number }): Promise<Brainstorm>
  listBrainstorming(input: { pipelineId: string; workspaceId: string; limit: number }): Promise<Brainstorm[]>
  answerBrainstormQuestion(input: { ref: BrainstormRef; questionId: string; answer: string }): Promise<Brainstorm>
  generateBrainstormQuestion(input: GenerateBrainstormInput): Promise<Brainstorm>
  questionsSufficient(input: GenerateBrainstormInput): Promise<Brainstorm>
  finishAndGenerateSynthesis(input: GenerateBrainstormInput): Promise<Brainstorm>
  approveBrainstormSynthesis(input: { ref: BrainstormRef; synthesisVersion: number }): Promise<Brainstorm>
  requestBrainstormRevision(input: { ref: BrainstormRef; synthesisVersion: number; choice: 'more_questions' | 'new_synthesis'; feedback: string }): Promise<Brainstorm>
  skipBrainstormQuestions(input: { ref: BrainstormRef; reason: string }): Promise<Brainstorm>
  confirmDiscoveryAfterSkip(ref: BrainstormRef): Promise<Brainstorm>
  cancelBrainstormAttempt(input: { ref: BrainstormRef; attemptId: string }): Promise<Brainstorm>
  resumePausedBrainstorm(ref: BrainstormRef): Promise<Brainstorm>
  getAuthoringStage(input: { pipelineId: string; stage: 'spec' | 'plan' }): Promise<AuthoringStage>
  /** The phases of a project that have an executor of their own; the others follow the default of Settings. */
  listStageExecutors(workspaceId: string): Promise<StageExecutor[]>
  saveStageExecutor(input: SaveStageExecutorInput): Promise<StageExecutor>
  clearStageExecutor(workspaceId: string, stage: PipelineStage): Promise<void>
  getAuthoringStageModelPreference(input: AuthoringStageModelPreferenceQuery): Promise<AuthoringStageModelPreference>
  saveAuthoringStageModelPreference(input: SaveAuthoringStageModelPreferenceInput): Promise<AuthoringStageModelPreference>
  generateAuthoringStage(input: GenerateAuthoringStageInput): Promise<AuthoringStage>
  requestAuthoringStageRevision(input: GenerateAuthoringStageInput): Promise<AuthoringStage>
  approveAuthoringStage(input: AuthoringStageDecisionInput): Promise<AuthoringStage>
  skipAuthoringStage(input: AuthoringStageDecisionInput): Promise<AuthoringStage>
  cancelAuthoringStage(input: AuthoringStageDecisionInput): Promise<AuthoringStage>
  startAuthoringCode(input: StartAuthoringCodeInput): Promise<AuthoringCodeRun>
  cancelAuthoringCode(input: CancelAuthoringCodeInput): Promise<AuthoringCodeRun>
  getAuthoringCodeRuns(input: GetAuthoringCodeRunsInput): Promise<AuthoringCodeRuns>
  preflightAuthoringCode(input: PreflightAuthoringCodeInput): Promise<AuthoringCodePreflight>
  prepareAuthoringCode(input: PrepareAuthoringCodeInput): Promise<AuthoringCodeCopyPreparation>
  getAuthoringCodeCopyPreparations(input: GetAuthoringCodeCopyPreparationsInput): Promise<AuthoringCodeCopyPreparations>
  getAuthoringCodeRunPatch(input: GetAuthoringCodeRunPatchInput): Promise<AuthoringCodeRunPatchPage>
  listPipelines(workspaceId: string): Promise<Pipeline[]>
  getPipelineStageActivity(pipelineId: string, stage: PipelineStage): Promise<PipelineStageActivity>
  getPipeline(pipelineId: string): Promise<Pipeline>
  savePipelineArtifact(pipelineId: string, stage: PipelineStage, content: string): Promise<Pipeline>
  advancePipeline(pipelineId: string): Promise<Pipeline>
  skipPipelineStage(pipelineId: string, reason: string): Promise<Pipeline>
  previewPipelineCodeWorkspace(workspaceId: string): Promise<PipelineCodeCopyPreview>
  createPipelineSession(pipelineId: string, backendId: string, role: PipelineRole, selection?: PipelineRoleModelSelection, confirmWorkspaceCopy?: boolean): Promise<PipelineSession>
  getPipelineForSession(sessionId: string, workspaceId: string): Promise<Pipeline>
  completePipelineCode(pipelineId: string): Promise<Pipeline>
  completePipelineEvaluation(pipelineId: string): Promise<Pipeline>
  decidePipelineExecutionArtifact(input: PipelineExecutionReviewInput): Promise<Pipeline>
  /** Runs QA in its lab in the background; follow it with getPipelineQALoop. */
  startPipelineQA(pipelineId: string, evaluator: PipelineRoleChoice): Promise<QALoop>
  /** Sends the chosen findings and improvements back to Code and loops Code → QA in the background. */
  fixPipelineFindings(input: FixPipelineFindingsInput): Promise<QALoop>
  resumePipelineFixes(input: ResumePipelineFixesInput): Promise<QALoop>
  getPipelineQALoop(pipelineId: string): Promise<QALoop>
  /** Pull requests the PRs conversation recorded for a pipeline, with their review watch. */
  listPipelinePullRequests(pipelineId: string): Promise<PullRequest[]>
  setPullRequestWatch(id: string, watch: boolean): Promise<PullRequest>
  checkPullRequestNow(id: string): Promise<PullRequest>
  onPullRequestsChange(listener: (pipelineId: string) => void): () => void
  reopenPipelineCodeReview(input: ReopenPipelineCodeReviewInput): Promise<Pipeline>
  applyPipelineCode(pipelineId: string): Promise<Pipeline>
  /** Ends the pull request stage: completed needs the agent's finished report, skipped is the person's decision. */
  finishPipelinePRs(pipelineId: string, outcome: 'completed' | 'skipped', reason?: string): Promise<Pipeline>
  saveAgent(input: AgentInput): Promise<Agent>
  listAgents(): Promise<Agent[]>
  delegateToAgent(parentSessionId: string, agentId: string, prompt: string, requestId: string): Promise<DelegatedSession>
  listDelegations(parentSessionId: string): Promise<Delegation[]>
  getParentDelegation(childSessionId: string): Promise<Delegation | null>
  saveWorkflow(input: { id?: string; workspaceId: string; name: string; steps: WorkflowStep[] }): Promise<Workflow>
  listWorkflows(workspaceId: string): Promise<Workflow[]>
  startWorkflow(input: { workflowId: string; backendId: string }): Promise<WorkflowRun>
  getWorkflowRun(runId: string): Promise<WorkflowRun>
  listWorkflowRuns(workspaceId: string): Promise<WorkflowRun[]>
  runWorkflowStep(runId: string): Promise<WorkflowRun>
  resumeWorkflowRun(input: { runId: string; reviewedSessionId: string; choice: 'retry' | 'skip' }): Promise<WorkflowRun>
  cancelWorkflowRun(runId: string): Promise<WorkflowRun>
  saveSchedule(input: ScheduleInput): Promise<Schedule>
  listSchedules(workspaceId: string): Promise<Schedule[]>
  setSchedulePaused(scheduleId: string, paused: boolean, revision: number): Promise<Schedule>
  runScheduleNow(scheduleId: string): Promise<ScheduleJob>
  listScheduleJobs(workspaceId: string): Promise<ScheduleJob[]>
  cancelScheduleJob(jobId: string): Promise<ScheduleJob>
  onScheduleChange(listener: (workspaceId: string) => void): () => void
  /** A chat agent created a pipeline through the Harflex tools. */
  onPipelineCreated(listener: (change: { workspaceId: string; pipelineId: string }) => void): () => void
  saveSkill(input: SkillInput): Promise<Skill>
  importSkill(input: ImportSkillInput): Promise<Skill>
  listSkills(workspaceId: string): Promise<Skill[]>
  importKnowledge(input: { workspaceId: string; path: string }): Promise<KnowledgeDocument>
  reindexKnowledge(input: KnowledgeDocumentInput): Promise<KnowledgeDocument>
  listKnowledge(workspaceId: string): Promise<KnowledgeDocument[]>
  searchKnowledge(input: SearchKnowledgeInput): Promise<KnowledgeHit[]>
  searchKnowledgeDetailed(input: SearchKnowledgeInput): Promise<KnowledgeSearch>
  getKnowledgeEmbeddingProfile(workspaceId: string): Promise<KnowledgeEmbeddingProfile>
  saveKnowledgeEmbeddingProfile(input: KnowledgeEmbeddingProfileInput): Promise<KnowledgeEmbeddingProfile>
  indexKnowledgeVectors(input: { workspaceId: string; expectedFingerprint: string }): Promise<KnowledgeEmbeddingProfile>
  removeKnowledge(input: KnowledgeDocumentInput): Promise<void>
  saveLocalChannel(input: { id?: string; workspaceId: string; name: string; folder: string }): Promise<LocalChannel>
  listLocalChannels(workspaceId: string): Promise<LocalChannel[]>
  importChannelInbox(channelId: string): Promise<ChannelImport>
  listChannelMessages(channelId: string): Promise<ChannelMessage[]>
  sendChannelMessage(input: { channelId: string; requestId: string; content: string }): Promise<ChannelMessage>
  retryChannelMessage(input: { channelId: string; requestId: string }): Promise<ChannelMessage>
  saveMCPServer(input: MCPServerInput): Promise<MCPServer>
  listMCPServers(workspaceId: string): Promise<MCPServer[]>
  connectMCPServer(id: string): Promise<MCPServer>
  disableMCPServer(id: string): Promise<MCPServer>
  pickDirectory(): Promise<string>
  pickSkillFile(): Promise<string>
  pickKnowledgeFile(): Promise<string>
  listBackends(): Promise<BackendOption[]>
  saveProviderProfile(input: ProviderProfileInput): Promise<BackendOption>
  createSession(workspaceId: string, backendId: string, agentId?: string): Promise<Session>
  createDirectSession(input: { workspaceId: string; backendId: string; agentId?: string; reason: string; modelId?: string; reasoningEffort?: string; catalogRevision?: string }): Promise<Session>
  getSessionModelSelection(sessionId: string): Promise<SessionModelSelection>
  listSessions(workspaceId: string): Promise<Session[]>
  openSession(sessionId: string, workspaceId: string): Promise<Session>
  exportAudit(sessionId: string, destination: string): Promise<string>
  prompt(sessionId: string, text: string): Promise<RunResult>
  approve(sessionId: string, approvalId: string, allow: boolean): Promise<RunResult>
  cancel(sessionId: string): Promise<void>
  listEvents(sessionId: string, afterSequence: number, limit?: number): Promise<AgentEvent[]>
  onEvent(listener: (event: AgentEvent) => void): () => void
}

export const parse = {
  pipelineDesign: (value: unknown) => pipelineDesignSchema.parse(value),
  workspace: (value: unknown) => workspace.parse(value),
  workspaces: (value: unknown) => z.array(workspaceSummary).parse(value ?? []),
  workspaceSummary: (value: unknown) => workspaceSummary.parse(value),
  providerProfiles: (value: unknown) => z.array(providerProfile).parse(value ?? []),
  modelCatalog: (value: unknown) => modelCatalog.parse(value),
  openRouterManagementKeyStatus: (value: unknown) => openRouterManagementKeyStatus.parse(value),
  credentialProbe: (value: unknown) => credentialProbe.parse(value),
  settings: (value: unknown) => settings.parse(value),
  logEntries: (value: unknown) => z.array(logEntry).parse(value ?? []),
  repository: (value: unknown) => repository.parse(value),
  worktreeList: (value: unknown) => worktreeList.parse(value),
  projectMemory: (value: unknown) => projectMemory.parse(value),
  worktreeGroups: (value: unknown) => z.array(worktreeGroup).parse(value ?? []),
  deleteWorktreeResult: (value: unknown) => deleteWorktreeResult.parse(value),
  worktreeSavePlan: (value: unknown) => worktreeSavePlan.parse(value),
  pipeline: (value: unknown) => pipeline.parse(value),
  qaLoop: (value: unknown) => qaLoop.parse(value),
  pullRequest: (value: unknown) => pullRequest.parse(value),
  pullRequests: (value: unknown) => z.array(pullRequest).nullish().transform(items => items ?? []).parse(value),
  pipelineStageActivity: (value: unknown) => pipelineStageActivity.parse(value),
  authoringPipeline: (value: unknown) => authoringPipeline.parse(value),
  brainstorm: (value: unknown) => brainstorm.parse(value),
  authoringStage: (value: unknown) => authoringStage.parse(value),
  authoringStageModelPreference: (value: unknown) => authoringStageModelPreference.parse(value),
  stageExecutor: (value: unknown) => stageExecutor.parse(value),
  stageExecutors: (value: unknown) => stageExecutors.parse(value),
  authoringCodeRun: (value: unknown) => authoringCodeRun.parse(value),
  authoringCodeRuns: (value: unknown) => authoringCodeRuns.parse(value),
  authoringCodePreflight: (value: unknown) => authoringCodePreflight.parse(value),
  authoringCodeCopyPreparation: (value: unknown) => authoringCodeCopyPreparation.parse(value),
  authoringCodeCopyPreparations: (value: unknown) => authoringCodeCopyPreparations.parse(value),
  authoringCodeRunPatchPage: (value: unknown) => authoringCodeRunPatchPage.parse(value),
  brainstorms: (value: unknown) => z.array(brainstorm).parse(value ?? []),
  pipelines: (value: unknown) => z.array(pipeline).parse(value ?? []),
  pipelineSession: (value: unknown) => pipelineSession.parse(value),
  pipelineCodeCopyPreview: (value: unknown) => pipelineCodeCopyPreview.parse(value),
  agent: (value: unknown) => agent.parse(value),
  agents: (value: unknown) => z.array(agent).parse(value ?? []),
  delegatedSession: (value: unknown) => delegatedSession.parse(value),
  delegations: (value: unknown) => z.array(delegation).parse(value ?? []),
  parentDelegation: (value: unknown) => value == null ? null : delegation.parse(value),
  workflow: (value: unknown) => workflow.parse(value),
  workflows: (value: unknown) => z.array(workflow).parse(value ?? []),
  workflowRun: (value: unknown) => workflowRun.parse(value),
  workflowRuns: (value: unknown) => z.array(workflowRun).parse(value ?? []),
  schedule: (value: unknown) => schedule.parse(value),
  schedules: (value: unknown) => z.array(schedule).parse(value ?? []),
  scheduleJob: (value: unknown) => scheduleJob.parse(value),
  scheduleJobs: (value: unknown) => z.array(scheduleJob).parse(value ?? []),
  skill: (value: unknown) => skill.parse(value),
  skills: (value: unknown) => z.array(skill).parse(value ?? []),
  knowledgeDocument: (value: unknown) => knowledgeDocument.parse(value),
  knowledgeDocuments: (value: unknown) => z.array(knowledgeDocument).parse(value ?? []),
  knowledgeHits: (value: unknown) => z.array(knowledgeHit).parse(value ?? []),
  knowledgeSearch: (value: unknown) => knowledgeSearch.parse(value),
  knowledgeEmbeddingProfile: (value: unknown) => knowledgeEmbeddingProfile.parse(value),
  localChannel: (value: unknown) => localChannel.parse(value),
  localChannels: (value: unknown) => z.array(localChannel).parse(value ?? []),
  channelMessage: (value: unknown) => channelMessage.parse(value),
  channelMessages: (value: unknown) => z.array(channelMessage).parse(value ?? []),
  channelImport: (value: unknown) => channelImport.parse(value),
  mcpServer: (value: unknown) => mcpServer.parse(value),
  mcpServers: (value: unknown) => z.array(mcpServer).parse(value ?? []),
  backends: (value: unknown) => z.array(backend).parse(value ?? []),
  backend: (value: unknown) => backend.parse(value),
  session: (value: unknown) => session.parse(value),
  sessionModelSelection: (value: unknown) => sessionModelSelection.parse(value),
  sessions: (value: unknown) => z.array(session).parse(value ?? []),
  chatProjects: (value: unknown) => z.array(chatProject).parse(value ?? []),
  id: (value: unknown) => id.parse(value),
  auditPath: (value: unknown) => auditPath.parse(value),
  events: (value: unknown) => z.array(z.unknown()).parse(value ?? []).map(value => {
    const result = event.safeParse(value)
    return result.success ? result.data : invalidEventDiagnostic()
  }),
  /** The adapter reports rejected pushed events through a safe local diagnostic. */
  safeEvent: (value: unknown) => { const result = event.safeParse(value); return result.success ? result.data : null },
  runResult: (value: unknown) => runResult.parse(value),
}

/** Stable code from the Go MarshalError payload; unknown failures stay generic. */
export function errorCode(error: unknown): string {
  const cause = (error as { cause?: unknown } | null)?.cause
  const parsed = typeof cause === 'string' ? safeJSON(cause) : cause
  const code = (parsed as { code?: unknown } | null)?.code
  return typeof code === 'string' ? code : 'internal'
}

/** A failure carrying a stable code, so `errorMessage` shows its pt-BR text just as it does for a Go failure. */
export function codedError(code: string): Error {
  return Object.assign(new Error(code), { cause: { code } })
}

function safeJSON(text: string): unknown {
  try { return JSON.parse(text) } catch { return null }
}

const errorMessages: Record<string, string> = {
  brainstorm_not_found: 'Ainda não há brainstorming para esta Discovery.',
  invalid_input: 'Os dados informados não são válidos.',
  workspace_not_found: 'O projeto não foi encontrado.',
  backend_not_found: 'O backend selecionado não está disponível.',
  backend_changed: 'A configuração do provedor mudou. Inicie uma nova sessão.',
  provider_endpoint_blocked: 'Este endpoint está bloqueado. Use HTTPS para provedores remotos ou um endereço local de loopback permitido.',
  session_not_found: 'A sessão não existe mais. Inicie uma nova sessão.',
  session_not_resumable: 'Esta conversa foi encerrada e não pode ser retomada. Escreva abaixo e o Harflex continua em um novo chat.',
  history_too_large: 'O histórico excede o limite de reabertura. Inicie uma nova sessão; o histórico permanece armazenado localmente.',
  brainstorm_budget_exceeded: 'O consumo medido excedeu o orçamento reservado para o brainstorming. A rodada foi pausada e nenhuma pergunta ou síntese foi publicada.',
  authoring_budget_exceeded: 'O consumo medido excedeu o orçamento reservado para esta etapa. A etapa foi pausada e nenhum artefato foi publicado.',
  session_busy: 'A sessão já está executando.',
  session_replay_failed: 'A conversa foi criada, mas não foi possível abri-la. Abra-a em Conversas.',
  worktree_backend_unusable: 'Esta IA não pode ser usada aqui: um CLI precisa de um modelo padrão salvo em Configurações. Escolha uma IA por API ou defina o modelo.',
  worktree_model_unconfirmed: 'O catálogo do CLI não confirmou o modelo padrão. Abra Conversas, escolha o modelo e peça de novo.',
  cli_model_unconfirmed: 'O catálogo do CLI não confirmou o modelo padrão salvo. Escolha outro provedor, ou salve outro modelo padrão em Configurações.',
  conversation_draft_not_empty: 'Envie ou descarte o rascunho da conversa aberta antes de abrir outra.',
  draft_not_empty: 'Envie, descarte ou leve o rascunho para outro chat antes de trocar de projeto.',
  no_active_run: 'Não há execução ativa para cancelar.',
  approval_not_found: 'Esta aprovação já foi resolvida.',
  cancelled: 'A operação foi cancelada.',
  git_unavailable: 'Git não está instalado ou não pode ser encontrado neste computador.',
  not_repository: 'Esta pasta não está dentro de um repositório Git.',
  worktree_not_found: 'Esse worktree não faz parte do repositório do projeto. A lista foi atualizada.',
  worktree_not_deletable: 'O worktree ainda guarda trabalho que seria perdido (ou mudou desde a última leitura). A lista foi atualizada.',
  worktree_save_blocked: 'Não dá para pedir à IA que salve este worktree agora. Veja o motivo na linha dele.',
  worktree_remove_failed: 'O Git não conseguiu remover a pasta do worktree. Feche programas que a usem e tente de novo.',
  pipeline_not_found: 'O pipeline não foi encontrado. Atualize a lista.',
  pipeline_conflict: 'O pipeline mudou em outra ação. Atualize para continuar.',
  pipeline_request_conflict: 'Esta tentativa de criação já foi usada com conteúdo diferente. Revise o pipeline ou inicie uma nova tentativa.',
  evidence_required: 'Salve a evidência exigida antes de avançar.',
  invalid_transition: 'Esta mudança de fase não é permitida.',
  untracked_evidence_requires_stage: 'Não foi possível confirmar o conteúdo da raiz isolada. Atualize a prévia e tente novamente.',
  pipeline_git_required: 'Abra a raiz do repositório Git selecionado para criar o snapshot de Code.',
  pipeline_git_dirty: 'A pasta do projeto já tem alterações Git. Preserve ou conclua essas mudanças antes de iniciar Code. Nenhuma sessão foi iniciada.',
  pipeline_git_baseline_unavailable: 'Não foi possível capturar um snapshot completo desta pasta. Reduza o escopo e tente novamente.',
  execution_copy_confirmation_required: 'Revise a prévia da pasta e confirme a cópia privada antes de iniciar Code.',
  pipeline_execution_unsafe_path: 'A cópia contém um link simbólico ou arquivo especial fora do perímetro seguro. Remova o caminho indicado e atualize a prévia.',
  pipeline_execution_snapshot_too_large: 'A cópia excede 20 mil arquivos ou 2 GiB. Reduza o escopo desta pasta e tente novamente.',
  pipeline_execution_evidence_too_large: 'O diff isolado excede o limite de evidência. Divida o trabalho em uma alteração menor.',
  pipeline_code_snapshot_unavailable: 'O snapshot isolado desta sessão não está disponível. Inspecione o histórico e inicie uma nova execução de Code.',
  pipeline_code_session_closed: 'Esta execução de Code já foi verificada e não aceita mais mensagens. Escreva abaixo para continuar em um novo chat, ou inicie uma nova rodada após a decisão do QA.',
  pull_request_watch_needs_full_access: 'A vigia corrige e faz push sozinha, então precisa de Acesso total no projeto.',
  pull_request_not_found: 'Este pull request não está registrado neste pipeline.',
  pipeline_qa_busy: 'O QA já está rodando para este pipeline.',
  pipeline_code_not_applied: 'Aplique o patch aprovado ao projeto antes de abrir os PRs: o agente trabalha na pasta do projeto.',
  pipeline_design_phase_executor_unusable: 'O provedor ou o modelo escolhido para uma das fases não está disponível agora. Abra "Provedor e modelo por fase", no alto desta página, e escolha outro ou volte ao padrão.',
  stage_executor_unsupported: 'Esta fase não pode usar esse executor. Escolha um perfil de API; o Codex serve às fases de documento, Code e QA, não aos PRs.',
  pipeline_prs_backend_unsupported: 'Os PRs precisam de um provedor API, porque as ferramentas MCP e o terminal rodam no Harflex. Escolha um perfil de API.',
  sdd_cli_read_isolation_unavailable: 'O Codex CLI pode ler arquivos fora do projeto, mesmo em modo somente leitura. Para proteger outros arquivos locais, use um provedor API no Professional SDD; o Codex CLI permanece no modo Casual.',
  pipeline_design_model_required: 'Escolha o provedor e o modelo padrão em Configurações ou personalize o modelo deste documento em Modelos.',
  pipeline_design_spec_stale: 'Atualize a SPEC com o Discovery atual antes de preparar o Plan.',
  pipeline_design_executor_unavailable: 'O executor configurado não está disponível para preparar documentos. No macOS, o modo controlado exige Codex CLI 0.157.0 em /opt/homebrew/bin/codex ou o Claude Code instalado. Confira a instalação ou escolha um provedor API.',
  claude_not_logged_in: 'O Claude Code não está autenticado. Faça o login no terminal com "claude auth login" e tente de novo.',
  pipeline_design_derivation_required: 'Estes documentos já foram aprovados. Crie uma continuação para editar e preservar a execução anterior.',
  pipeline_design_invalid_request: 'O contexto deste trabalho excedeu o limite ou contém uma ferramenta não suportada. Reduza o escopo e tente novamente.',
  pipeline_design_invalid_response: 'A IA retornou uma resposta fora do formato esperado. Os documentos anteriores foram preservados. Tente preparar novamente.',
  pipeline_design_provider_failed: 'O executor não conseguiu concluir a preparação. Os documentos anteriores foram preservados. Confira o executor configurado e tente novamente.',
  pipeline_design_timeout: 'A preparação excedeu 3 minutos. Os documentos anteriores foram preservados. Reduza o pedido ou tente novamente.',
  pipeline_source_drift: 'A pasta original mudou depois da criação do snapshot. Resolva o conflito e execute Code novamente.',
  pipeline_code_apply_conflict: 'O patch mudou ou encontrou conflitos durante a aplicação. Revise a pasta e a prévia antes de tentar novamente.',
  agent_not_found: 'O agente não foi encontrado. Atualize o catálogo.',
  delegation_limit: 'O limite de subagentes foi atingido para esta sessão.',
  delegation_budget_exceeded: 'O subagente atingiu o limite de chamadas. Revise o histórico antes de criar outra delegação.',
  delegation_request_conflict: 'Esta tentativa já foi registrada com outro agente ou tarefa. Revise o histórico ou inicie uma nova tentativa.',
  workflow_not_found: 'O workflow não foi encontrado. Atualize a lista.',
  workflow_run_not_found: 'A execução não foi encontrada. Atualize a lista.',
  workflow_conflict: 'A execução mudou. Atualize antes de continuar.',
  workflow_cancel_unconfirmed: 'Não foi possível confirmar o fim da sessão. Atualize a execução antes de repetir o cancelamento.',
  schedule_not_found: 'O agendamento não foi encontrado. Atualize a lista.',
  schedule_job_not_found: 'O job não foi encontrado. Atualize o histórico.',
  schedule_conflict: 'O agendamento mudou. Atualize antes de salvar.',
  schedule_job_conflict: 'Já existe um job ativo neste agendamento. Aguarde ou cancele antes de repetir.',
  schedule_cancel_unconfirmed: 'Não foi possível confirmar o cancelamento. O job pode continuar; verifique o histórico antes de repetir.',
  schedule_time_invalid: 'O horário não existe ou é ambíguo no fuso escolhido. Selecione outro horário.',
  schedule_time_passed: 'A data desta execução única já passou. Escolha uma data futura.',
  skill_not_found: 'A skill não foi encontrada. Atualize o catálogo.',
  skill_import_failed: 'Não foi possível importar SKILL.md. Confira se está dentro do projeto, é texto UTF-8 e tem até 64 KB.',
  knowledge_not_found: 'O documento não foi encontrado no índice. Atualize a biblioteca.',
  knowledge_source_unavailable: 'O arquivo de origem não está disponível ou saiu do projeto autorizado.',
  knowledge_source_changed: 'O arquivo mudou durante a leitura. Tente indexar novamente.',
  local_embedding_profile_changed: 'O perfil local mudou. Atualize o perfil e confirme o destino antes de indexar.',
  local_embedding_unavailable: 'O servidor ou modelo local de embeddings não respondeu. Confira se está carregado e tente o próximo lote.',
  channel_not_found: 'O canal não foi encontrado. Atualize a lista.',
  channel_folder_unavailable: 'A pasta do canal não está disponível no projeto.',
  channel_inbox_limit: 'A inbox tem mais de 500 entradas. Mova ou remova manualmente arquivos da inbox para liberar novas importações; o Harflex não apaga os originais.',
  channel_source_unavailable: 'Um arquivo da inbox não está disponível ou saiu do projeto.',
  channel_source_changed: 'Um arquivo mudou durante a leitura. Tente importar novamente.',
  channel_message_invalid: 'A mensagem deve ser texto UTF-8 válido de até 64 KB.',
  channel_outbox_conflict: 'Já existe um arquivo diferente na outbox com esse nome. Resolva o conflito antes de repetir.',
  channel_write_failed: 'Não foi possível escrever na outbox. Confira as permissões e repita o envio.',
  channel_request_conflict: 'Esta tentativa de envio já foi usada para outra mensagem.',
  channel_config_locked: 'A pasta de um canal existente não pode mudar. Crie outro canal.',
  mcp_server_not_found: 'O servidor MCP não foi encontrado. Atualize a lista.',
  mcp_connection_failed: 'Não foi possível conectar ao servidor MCP. Verifique o endereço ou executável e tente novamente.',
}

/** What an error says beyond its code, when the backend adds it (the phase a failure belongs to). */
function errorStage(error: unknown): string {
  const cause = (error as { cause?: unknown } | null)?.cause
  const parsed = typeof cause === 'string' ? safeJSON(cause) : cause
  const stage = (parsed as { stage?: unknown } | null)?.stage
  return typeof stage === 'string' ? stage : ''
}

const phaseNames: Record<string, string> = { discovery: 'Discovery', spec: 'SPEC', plan: 'Plan', code: 'Code', eval: 'QA', prs: 'PRs' }

export function errorMessage(error: unknown): string {
  const code = errorCode(error)
  if (code === 'pipeline_design_phase_executor_unusable') return t('O provedor ou o modelo escolhido para {phase} não está disponível agora. Abra "Provedor e modelo por fase", no alto desta página, e escolha outro ou volte ao padrão.', { phase: phaseNames[errorStage(error)] ?? t('uma das fases') })
  return t(errorMessages[code] ?? 'A operação falhou. Consulte os eventos da sessão.')
}

// cliCatalogProblem explains why a local CLI did not hand over its model list.
export function cliCatalogProblem(result: Pick<ModelCatalogResult, 'errorCode'>, name: string) {
  if (result.errorCode === 'catalog_not_logged_in') return t('{name} não está autenticado. Faça o login no terminal com "claude auth login" e tente de novo.', { name })
  if (result.errorCode === 'catalog_cli_unavailable') return t('{name} não foi encontrado nesta máquina.', { name })
  return t('Não foi possível confirmar o catálogo local de {name}.', { name })
}

// The CLIs that can work as a model only in the SDD phases, with the catalog source that lists their models.
// The Harflex keeps the tools; the CLI gets none of its own.
export const documentCLISources = { codex: 'codex_app_server', claude: 'claude_cli' } as const
export type DocumentCLI = keyof typeof documentCLISources
export type DocumentCLISource = typeof documentCLISources[DocumentCLI]
export const isDocumentCLI = (id: string): id is DocumentCLI => id === 'codex' || id === 'claude'
