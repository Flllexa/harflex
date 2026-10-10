import { useEffect, useLayoutEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { ArrowRight, FileText, FolderOpen, ListTree, Plus, RefreshCw, SkipForward } from 'lucide-react'
import { errorCode, errorMessage, isDocumentCLI, type Backend, type BackendOption, type Pipeline, type PipelineCodeCopyPreview, type PipelineRole, type PipelineRoleModelSelection, type PipelineStage, type ProviderProfile, type QALoop, type StageExecutor } from '../../lib/backend'
import { IonPicker } from '../../components/IonPicker'
import { AuthoringWorkbench } from './AuthoringWorkbench'
import { PipelineRoleModelPicker } from './PipelineRoleModelPicker'
import { PipelineExecutionReview } from './PipelineExecutionReview'
import { CodeLiveRun, codeIsRunning, useAutoVerifyCode, useCodeRun } from './CodeLiveRun'
import { PipelineQALab, type RoleDefaults } from './PipelineQALab'
import { PipelineBench } from './bench/PipelineBench'
import { CodeBench } from './bench/CodeBench'
import { PipelinePRsOutcome, PipelinePRsPanel, pullRequestsOpen } from './PipelinePRsPanel'
import { PipelinePhaseExecutors, phaseCanUse, phaseText } from './PipelinePhaseExecutors'
import { PipelineDesignStudio } from './PipelineDesignStudio'
import { StageActivityPane } from './StageActivityPane'
import { localeTag, useT } from '../../i18n'

type SettingsReadState = 'loading' | 'ready' | 'error'
/** What the stage needs before it can be recorded: a run that ended well (a follow-up in its conversation that failed or was cancelled takes it away) and, for the Code, changes to show for it. */
function unfinishedRunText(translate: ReturnType<typeof useT>): Record<PipelineRole, string> {
  return {
    coder: translate('O Code ainda não tem uma execução concluída com mudanças nos arquivos. Na conversa do Code, envie uma mensagem e deixe a execução terminar antes de verificar.'),
    evaluator: translate('O QA ainda não deu um veredito concluído. Deixe a execução do QA terminar antes de registrar.'),
    publisher: translate('A IA ainda não terminou os PRs. Deixe a execução terminar antes de concluir.'),
  }
}

export type PipelineCreationDraft = { discovery: string; open: boolean; newWorkRequest: number; intent?: { workspaceId: string; discovery: string; requestId: string } }
type Props = { backend: Backend; backends: BackendOption[]; settingsStatus?: SettingsReadState; preferredBackendId?: string; preferredModelBackendId?: string; preferredModelId?: string; workspaceId?: string; selectedPipeline?: Pipeline; creationDraft?: PipelineCreationDraft; onCreationDraftChange?: (workspaceId: string, draft: PipelineCreationDraft) => void; newWorkRequest?: number; initialDiscovery?: string; initialDiscoveryRequestId?: string; onInitialDiscoveryConsumed?: (requestId: string) => void; viewStage?: PipelineStage; viewRequestId?: number; onSelectPipeline: (run?: Pipeline) => void; onProjects: () => void; onSettings?: () => void; onStartRole: (pipelineId: string, backendId: string, role: PipelineRole, selection: PipelineRoleModelSelection | undefined, confirmWorkspaceCopy: boolean) => Promise<void>; /** Opens an existing conversation, such as the one where the agent opens the pull requests. */ onOpenSession?: (sessionId: string, draft?: string) => Promise<void>; /** Shows a background run's conversation, such as the QA fix loop's, in the activity panel without leaving this screen. */ onFollowSession?: (sessionId: string) => void; /** The project's permission profile and the menu that changes it, offered next to the pull request step. */ permissionProfile?: string; permissionControl?: ReactNode; /** Opens MCP Servers, where GitHub or Bitbucket is connected for the PRs. */ onMCPServers?: () => void; /** The stage whose screen is showing, for the stage bar to mark. */ onShownStage?: (stage?: PipelineStage) => void }
const labels: Record<PipelineStage, string> = { discovery: 'Discovery', spec: 'Spec', plan: 'Plan', code: 'Code', eval: 'QA', prs: 'PRs' }
const editable = new Set<PipelineStage>(['discovery', 'spec', 'plan'])

function PipelineRoleControls(props: {
  run: Pipeline
  backend: Backend
  backends: BackendOption[]
  workspaceId?: string
  settingsStatus: SettingsReadState
  defaultModelBackendId: string
  defaultModelId: string
  /** The model above is what the project chose for this phase, not the default of Settings. */
  phaseConfigured: boolean
  /** The project chose an executor for this phase that cannot run it now. */
  phaseNote?: string
  /** The choices of the project are read: until then the controls would start on the default and jump. */
  choicesReady: boolean
  selectedBackendId: string
  selectedBackend?: BackendOption
  pending: boolean
  onBackendChange: (id: string) => void
  onStart: (role: PipelineRole, backendId: string, selection: PipelineRoleModelSelection, confirmWorkspaceCopy: boolean) => void
  onComplete: (role: PipelineRole) => void
  onSettings?: () => void
}) {
  const t = useT()
  if ((props.run.currentStage !== 'code' && props.run.currentStage !== 'eval') || props.run.stageStatus[props.run.currentStage] !== 'active') return null
  if (!props.choicesReady) return <p className="muted" role="status">{t('Lendo as escolhas por fase…')}</p>
  return <PipelineRoleExecutionControls {...props} />
}

function PipelineRoleExecutionControls({ run, backend, backends, workspaceId, settingsStatus, defaultModelBackendId, defaultModelId, phaseConfigured, phaseNote, selectedBackendId, selectedBackend, pending, onBackendChange, onStart, onComplete, onSettings }: {
  run: Pipeline
  backend: Backend
  backends: BackendOption[]
  workspaceId?: string
  settingsStatus: SettingsReadState
  defaultModelBackendId: string
  defaultModelId: string
  phaseConfigured: boolean
  phaseNote?: string
  selectedBackendId: string
  selectedBackend?: BackendOption
  pending: boolean
  onBackendChange: (id: string) => void
  onStart: (role: PipelineRole, backendId: string, selection: PipelineRoleModelSelection, confirmWorkspaceCopy: boolean) => void
  onComplete: (role: PipelineRole) => void
  onSettings?: () => void
}) {
  const t = useT()
  const [selectionState, setSelectionState] = useState<{ key: string; selection?: PipelineRoleModelSelection }>()
  const [copyConfirmed, setCopyConfirmed] = useState(false)
  const [copyPreviewState, setCopyPreviewState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [copyPreview, setCopyPreview] = useState<PipelineCodeCopyPreview>()
  const [copyPreviewError, setCopyPreviewError] = useState('')
  const isCode = run.currentStage === 'code'
  const role: PipelineRole = isCode ? 'coder' : 'evaluator'
  const codexUnavailable = backends.some(item => item.available && isDocumentCLI(item.id) && !item.professionalAvailable)
  const selectionKey = `${run.id}:${run.currentStage}:${selectedBackendId}`
  const selectedModel = selectionState?.key === selectionKey ? selectionState.selection : undefined
  const eligible = (item: BackendOption) => item.available && (item.kind === 'api' || isDocumentCLI(item.id) && item.professionalAvailable === true)
  const eligibleBackends = backends.filter(eligible)
  const noAPIBackend = eligibleBackends.length === 0
  useEffect(() => {
    setCopyConfirmed(false)
    if (!isCode || !workspaceId || noAPIBackend) {
      setCopyPreview(undefined)
      setCopyPreviewState(noAPIBackend ? 'ready' : 'loading')
      setCopyPreviewError('')
      return
    }
    let current = true
    setCopyPreview(undefined)
    setCopyPreviewState('loading')
    setCopyPreviewError('')
    void backend.previewPipelineCodeWorkspace(workspaceId).then(result => {
      if (!current) return
      setCopyPreview(result)
      setCopyPreviewState('ready')
    }).catch(failure => {
      if (!current) return
      setCopyPreviewError(errorMessage(failure))
      setCopyPreviewState('error')
    })
    return () => { current = false }
  }, [backend, workspaceId, isCode, noAPIBackend, run.id, run.revision])
  const unsafeCopy = (copyPreview?.unsafePaths.length ?? 0) > 0
  const copyReady = !isCode || copyPreviewState === 'ready' && !unsafeCopy && (!!copyPreview?.isGit || copyConfirmed)
  return <div className="pipeline-run-controls">
    {phaseNote && <p className="project-warning" role="status">{phaseNote}</p>}
    {settingsStatus === 'error' && <p className="project-warning" role="alert">{t('Não foi possível ler o padrão das Configurações. Escolha executor e modelo para esta fase; essa escolha não altera o padrão salvo.')}</p>}
    {settingsStatus === 'loading' && <p className="muted" role="status">{t('Lendo o padrão das Configurações. Você pode escolher um executor manualmente.')}</p>}
    <p className="muted">{isCode ? t('O Coder trabalha numa cópia privada desta pasta. O original só recebe o patch depois da aprovação do QA.') : t('O QA compara os critérios aprovados com as mudanças em uma sessão separada, sem escrita.')}</p>
    {noAPIBackend ? <div className="authoring-provider-empty" role="alert"><strong>{t('Code e QA precisam de um provedor API')}</strong><p>{t('O Codex CLI pode ler arquivos fora do projeto e não está disponível nesta fase Professional. Configure um perfil API para continuar ou mude para Casual no seletor do topo para conversar com Codex.')}</p>{onSettings && <button type="button" className="touch-target secondary-button" disabled={pending} onClick={onSettings}>{t('Configurar provedor API')}</button>}</div> : <>
      {codexUnavailable && <p className="project-warning" role="status">{t('O Codex CLI pode ler arquivos fora do projeto mesmo em sandbox somente leitura; o Professional SDD aceita apenas perfis API.')}</p>}
      <IonPicker id="pipeline-backend" label={t('Executor da fase')} value={selectedBackendId} onChange={onBackendChange} options={[{ value: '', label: t('Escolha um executor') }, ...backends.map(item => ({ value: item.id, label: `${item.name}${!item.available ? ` · ${t('indisponível')}` : isDocumentCLI(item.id) && !item.professionalAvailable ? ` · ${t('indisponível no SDD')}` : ''}`, disabled: !eligible(item) }))]} />
      {selectedBackend && <PipelineRoleModelPicker key={selectionKey} backend={backend} workspaceId={workspaceId} stage={isCode ? 'code' : 'eval'} backendOption={selectedBackend}
        defaultModelBackendId={settingsStatus === 'ready' || phaseConfigured ? defaultModelBackendId : ''} defaultModelId={settingsStatus === 'ready' || phaseConfigured ? defaultModelId : ''} phaseConfigured={phaseConfigured} disabled={pending}
        onSelectionChange={selection => setSelectionState({ key: selectionKey, selection })} />}
    </>}
    {isCode && copyPreviewState === 'loading' && <p className="muted" role="status">{t('Lendo a prévia da raiz que será copiada para Code…')}</p>}
    {isCode && copyPreviewState === 'error' && <div className="inline-error" role="alert"><p>{copyPreviewError || errorMessage({ cause: { code: 'pipeline_execution_snapshot_too_large' } })}</p><button type="button" className="touch-target secondary-button" onClick={() => { setCopyPreviewState('loading'); setCopyPreviewError(''); void backend.previewPipelineCodeWorkspace(workspaceId ?? '').then(result => { setCopyPreview(result); setCopyPreviewState('ready') }).catch(failure => { setCopyPreviewError(errorMessage(failure)); setCopyPreviewState('error') }) }}>{t('Atualizar prévia')}</button></div>}
    {isCode && copyPreviewState === 'ready' && copyPreview && <section className="authoring-copy-preview" aria-label={t('Prévia da cópia isolada')}><strong>{copyPreview.isGit ? t('Snapshot Git limpo') : t('Cópia local confirmada')}</strong><p className="muted">{t('{count} arquivos · {size} MB. A cópia fica privada e a raiz original não é alterada durante Code ou QA.', { count: copyPreview.fileCount.toLocaleString(localeTag()), size: (copyPreview.totalBytes / (1024 * 1024)).toFixed(1) })}</p>{copyPreview.excludedPaths.length > 0 && <details><summary>{t('Itens excluídos ({count})', { count: copyPreview.excludedPaths.length })}</summary><ul>{copyPreview.excludedPaths.map(path => <li key={path} className="mono">{path}</li>)}</ul></details>}{unsafeCopy && <div className="project-warning" role="alert"><p>{t('Estes caminhos não podem entrar na cópia porque escapam da pasta ou são arquivos especiais:')}</p><ul>{copyPreview.unsafePaths.map(path => <li key={path} className="mono">{path}</li>)}</ul></div>}{!copyPreview.isGit && !unsafeCopy && <label className="authoring-confirm"><input type="checkbox" checked={copyConfirmed} onChange={event => setCopyConfirmed(event.target.checked)} disabled={pending} />{t('Confirmo copiar estes arquivos para a execução isolada do modelo selecionado.')}</label>}</section>}
    {!noAPIBackend && <div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => onComplete(role)} disabled={pending}>{isCode ? t('Verificar código') : t('Registrar avaliação')}</button><button type="button" className="touch-target primary-button" onClick={() => selectedModel && onStart(role, selectedBackendId, selectedModel, copyPreview?.isGit ? false : copyConfirmed)} disabled={pending || !selectedBackend || !eligible(selectedBackend) || !selectedModel || !copyReady}>{isCode ? t('Executar Coder') : t('Executar Evaluator')}</button></div>}
    {run.artifacts[run.currentStage] && <pre className="mono diff-body">{run.artifacts[run.currentStage].content}</pre>}
  </div>
}

function PipelineArtifactArchive({ run, stages, inspectedStage, onInspect }: { run: Pipeline; stages: PipelineStage[]; inspectedStage: PipelineStage; onInspect: (stage: PipelineStage) => void }) {
  const t = useT()
  const savedStages = stages.filter(stage => !!run.artifacts[stage])
  const archivedVersions = run.archivedArtifacts ?? []
  if (savedStages.length === 0 && archivedVersions.length === 0) return null
  const selected = savedStages.includes(inspectedStage) ? inspectedStage : savedStages[savedStages.length - 1]
  const artifact = run.artifacts[selected]
  return <section className="pipeline-archive" aria-labelledby={`pipeline-archive-${run.id}`}><h3 id={`pipeline-archive-${run.id}`}>{t('Artefatos salvos')}</h3>{savedStages.length > 0 && <><div className="pipeline-archive-tabs">{savedStages.map(stage => <button type="button" key={stage} className="touch-target secondary-button" aria-pressed={selected === stage} onClick={() => onInspect(stage)}>{t('Ver {stage}', { stage: labels[stage] })}</button>)}</div>{artifact?.reviewStatus === 'stale' && <p className="project-warning" role="status">{t('Este QA pertence a uma versão anterior de Code e não libera aplicação.')}</p>}<pre className="mono diff-body">{artifact?.content ?? ''}</pre></>}
    {archivedVersions.length > 0 && <section className="pipeline-archived-versions" aria-label={t('Versões anteriores de Code e QA')}><h4>{t('Versões anteriores')}</h4><ol>{archivedVersions.map(item => <li key={`${item.stage}:${item.version}`}><details><summary>{labels[item.stage]} v{item.version} · {item.reason === 'superseded' ? t('substituída') : item.reason}</summary><p className="muted">SHA-256 {item.contentDigest} · {new Date(item.createdAt).toLocaleString(localeTag())}</p><pre className="mono diff-body">{item.content}</pre></details></li>)}</ol></section>}
  </section>
}

function hasLegacyCodeReviewGap(run: Pipeline) {
  const code = run.artifacts.code
  if (!code || code.author !== 'ai' || !code.sourceSessionId || !code.contentDigest || run.stageStatus.code !== 'completed' || (run.currentStage !== 'eval' && run.currentStage !== '')) return false
  return !(run.executionReviews ?? []).some(review => review.stage === 'code' && review.version === code.version && review.contentDigest === code.contentDigest && review.decision === 'approve')
}

function ApprovedPreparation({ archived, viewStage, viewRequestId, children }: { archived: boolean; viewStage?: PipelineStage; viewRequestId?: number; children: ReactNode }) {
  const t = useT()
  const [open, setOpen] = useState(!archived)
  useLayoutEffect(() => { setOpen(!archived) }, [archived])
  useEffect(() => { if (archived && (viewStage === 'discovery' || viewStage === 'spec' || viewStage === 'plan')) setOpen(true) }, [archived, viewStage, viewRequestId])
  return <details className={`pipeline-preparation-archive${archived ? ' is-archived' : ''}`} open={!archived || open} onToggle={event => { if (archived) setOpen(event.currentTarget.open) }}><summary hidden={!archived}>{t('Discovery, SPEC e Plan aprovados')}</summary><div hidden={archived && !open}>{children}</div></details>
}

function PipelineLegacyCodeReviewRecovery({ backend, run, onPipelineChange }: { backend: Backend; run: Pipeline; onPipelineChange: (run: Pipeline) => void }) {
  const t = useT()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const artifact = run.artifacts.code!
  async function reopen() {
    if (pending || !artifact.contentDigest || run.codeReviewRecoveryStatus !== 'available') return
    setPending(true)
    setError('')
    try {
      const updated = await backend.reopenPipelineCodeReview({ pipelineId: run.id, artifactVersion: artifact.version, artifactDigest: artifact.contentDigest, pipelineRevision: run.revision })
      onPipelineChange(updated)
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }
  async function branchFromDiscovery() {
    const discovery = run.artifacts.discovery?.content
    if (pending || !discovery) return
    setPending(true)
    setError('')
    try {
      const updated = await backend.deriveAuthoringPipeline({ parentPipelineId: run.id, requestId: crypto.randomUUID(), expectedRevision: run.revision, discovery })
      onPipelineChange(updated)
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }
  const recoverable = run.codeReviewRecoveryStatus === 'available'
  return <section className="pipeline-review-recovery" aria-label={t('Recuperar revisão de Code')}>
    <div><h3>{recoverable ? t('Este Code ainda não tem uma decisão registrada') : t('Esta execução antiga não pode ser reaberta com segurança')}</h3><p className="muted">{t('Versão {version} · SHA-256 {digest}…', { version: artifact.version, digest: artifact.contentDigest?.slice(0, 16) ?? '' })}</p></div>
    <p>{recoverable ? t('O pipeline veio de uma execução anterior ao gate de revisão. Abra o diff abaixo, registre a aprovação de Code e gere uma nova avaliação de QA antes de aplicar qualquer alteração.') : run.codeReviewRecoveryReason || t('O snapshot desta versão não pode ser confirmado.')}</p>
    <details><summary>{t('Revisar diff salvo de Code')}</summary><pre className="mono diff-body">{artifact.content}</pre></details>
    {run.artifacts.eval && <p className="project-warning" role="status">{t('A avaliação anterior será preservada para consulta e marcada como desatualizada após reabrir Code.')}</p>}
    <div className="pipeline-actions">{recoverable && <button type="button" className="touch-target primary-button" onClick={() => void reopen()} disabled={pending}>{pending ? t('Reabrindo revisão…') : t('Reabrir revisão de Code')}</button>}{!recoverable && <button type="button" className="touch-target primary-button" onClick={() => void branchFromDiscovery()} disabled={pending || !run.artifacts.discovery?.content}>{pending ? t('Criando nova execução…') : t('Criar nova execução a partir do Discovery')}</button>}</div>
    {error && <p className="form-error" role="alert">{error}</p>}
  </section>
}

export function PipelinesPage({ backend, backends, settingsStatus = 'ready', preferredBackendId = '', preferredModelBackendId = '', preferredModelId = '', workspaceId, selectedPipeline, creationDraft, onCreationDraftChange, newWorkRequest = 0, initialDiscovery, initialDiscoveryRequestId, onInitialDiscoveryConsumed, viewStage, viewRequestId, onSelectPipeline, onProjects, onSettings, onStartRole, onOpenSession, onFollowSession, permissionProfile, permissionControl, onMCPServers, onShownStage }: Props) {
  const t = useT()
  const [items, setItems] = useState<Pipeline[]>([])
  const [discovery, setDiscovery] = useState(creationDraft?.discovery ?? '')
  const [creationScope, setCreationScope] = useState({ backend, workspaceId })
  const createIntent = useRef(creationDraft?.intent)
  const consumedDiscoverySeeds = useRef(new Set<string>())
  const lastNewWorkRequest = useRef(creationDraft?.newWorkRequest ?? 0)
  const createEpoch = useRef(0)
  const createWorkspace = useRef(workspaceId)
  const [draft, setDraft] = useState('')
  const [draftDirty, setDraftDirty] = useState(false)
  const [draftConflict, setDraftConflict] = useState(false)
  const draftIdentity = useRef('')
  const draftRevision = useRef(0)
  const [reason, setReason] = useState('')
  const [skipConfirm, setSkipConfirm] = useState(false)
  const [backendChoice, setBackendChoice] = useState('')
  const [qaLoop, setQALoop] = useState<QALoop>()
  // Each session the QA loop starts (the Coder fixing, then QA testing) shows live in the activity panel.
  useEffect(() => { if (qaLoop?.running && qaLoop.sessionId) onFollowSession?.(qaLoop.sessionId) }, [qaLoop?.running, qaLoop?.sessionId])
  const [stageExecutors, setStageExecutors] = useState<StageExecutor[]>([])
  // The saved choices of the project: being read, read, or not readable (the controls then start on the default and the card says so).
  const [executorsState, setExecutorsState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [executorsRead, setExecutorsRead] = useState(0)
  // The API profiles, to tell which choices still work; undefined until read, and 'failed' when they could not be.
  const [profiles, setProfiles] = useState<ProviderProfile[]>()
  const [profilesFailed, setProfilesFailed] = useState(false)
  // undefined: open while the project has no pipeline yet, closed afterwards, until the person decides.
  const [phasesOpen, setPhasesOpen] = useState<boolean>()
  const [inspectedStage, setInspectedStage] = useState<PipelineStage>('eval')
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const [createOpen, setCreateOpen] = useState(creationDraft?.open ?? false)
  const [autoPrepare, setAutoPrepare] = useState<{ pipelineId: string; requestId: string }>()
  const [applyConfirmed, setApplyConfirmed] = useState(false)
  const refreshGeneration = useRef(0)
  const run = selectedPipeline?.workspaceId === workspaceId ? selectedPipeline : undefined
  const conversational = run?.kind === 'ai_authoring' && run.preparationExperience === 'conversational'
  const visibleItems = items.filter(item => item.workspaceId === workspaceId)
  const backendAllowed = (item: BackendOption) => item.available && (item.kind === 'api' || isDocumentCLI(item.id) && item.professionalAvailable === true)
  const automaticBackend = backends.find(backendAllowed)
  const globalDefaultIsUnsupported = settingsStatus === 'ready' && !!preferredBackendId && !backends.some(item => item.id === preferredBackendId && backendAllowed(item))
  // What the project chose for the phase being run comes before the default of Settings; the person can still change it here.
  const phaseChoice = run?.currentStage === 'code' || run?.currentStage === 'eval' ? stageExecutors.find(item => item.stage === run.currentStage) : undefined
  const phaseBackend = phaseChoice && backends.find(item => item.id === phaseChoice.backendId && backendAllowed(item) && phaseCanUse(phaseChoice.stage, item, profiles?.find(profile => profile.id === item.id)))
  const choicesReady = executorsState !== 'loading' && (profiles !== undefined || profilesFailed)
  const choicesFailed = executorsState === 'error' || (profilesFailed && profiles === undefined)
  const phaseNote = phaseChoice && !phaseBackend && profiles !== undefined ? t('O executor escolhido para {phase} ({backend}) não está disponível agora; esta fase parte do padrão. Ajuste em "Provedor e modelo por fase".', { phase: phaseText(t)[phaseChoice.stage].label, backend: backends.find(item => item.id === phaseChoice.backendId)?.name ?? phaseChoice.backendId }) : undefined
  const selectedBackendId = backendChoice || (phaseBackend ? phaseBackend.id : settingsStatus === 'ready' ? globalDefaultIsUnsupported ? '' : backends.find(item => item.id === preferredBackendId && backendAllowed(item))?.id || automaticBackend?.id || '' : '')
  const phaseModelApplies = !!phaseBackend && selectedBackendId === phaseBackend.id
  // An empty model means the profile's own: the default model of Settings must not stand in for it.
  const roleModelBackendId = phaseModelApplies ? (phaseChoice!.modelId ? phaseChoice!.backendId : '') : preferredModelBackendId
  const roleModelId = phaseModelApplies ? phaseChoice!.modelId : preferredModelId
  const selectedBackend = backends.find(item => item.id === selectedBackendId)
  const recoverLegacyCodeReview = !!run && hasLegacyCodeReviewGap(run)
  // Code and QA: QA is a lab, and a fix round that goes back to Code keeps showing the lab.
  const roleDefaults = (stage: 'code' | 'eval'): RoleDefaults => {
    const choice = stageExecutors.find(item => item.stage === stage)
    const chosen = choice && backends.find(item => item.id === choice.backendId && backendAllowed(item) && phaseCanUse(choice.stage, item, profiles?.find(profile => profile.id === item.id)))
    const backendId = chosen ? chosen.id : settingsStatus === 'ready' ? globalDefaultIsUnsupported ? '' : backends.find(item => item.id === preferredBackendId && backendAllowed(item))?.id || automaticBackend?.id || '' : ''
    return chosen ? { backendId, defaultModelBackendId: choice!.modelId ? choice!.backendId : '', defaultModelId: choice!.modelId, phaseConfigured: true } : { backendId, defaultModelBackendId: preferredModelBackendId, defaultModelId: preferredModelId, phaseConfigured: false }
  }
  const fixing = !!run && run.currentStage === 'code' && qaLoop?.pipelineId === run.id && qaLoop.running
  // QA's failures went back to Code and the fix loop stopped before its Coder round (the app closed, or the round failed to start).
  const lastReview = [...(run?.executionReviews ?? [])].sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0]
  const resumable = !!run && !fixing && run.currentStage === 'code' && run.stageStatus.code === 'active' && lastReview?.stage === 'eval' && lastReview.decision === 'request_revision'
  const executionView = !run || recoverLegacyCodeReview || (run.currentStage !== 'code' && run.currentStage !== 'eval') ? null
    : run.currentStage === 'eval' || fixing || resumable ? <PipelineQALab key={run.id} resumable={resumable} backend={backend} backends={backends} run={run} workspaceId={workspaceId} roleDefaults={roleDefaults} onPipelineChange={onSelectPipeline} onOpenSession={sessionId => onOpenSession?.(sessionId)} onLoopChange={setQALoop} onSettings={onSettings} onRoleChosen={saveRoleChoice} />
      : run.stageStatus[run.currentStage] === 'waiting_user' ? <PipelineExecutionReview backend={backend} run={run} stage={run.currentStage} onPipelineChange={onSelectPipeline} />
        : <PipelineRoleControls run={run} backend={backend} backends={backends} workspaceId={workspaceId} settingsStatus={settingsStatus} defaultModelBackendId={roleModelBackendId} defaultModelId={roleModelId} phaseConfigured={phaseModelApplies} phaseNote={phaseNote} choicesReady={choicesReady} selectedBackendId={selectedBackendId} selectedBackend={selectedBackend} pending={pending} onSettings={onSettings}
                onBackendChange={value => { setBackendChoice(value); setError(undefined) }} onStart={startRole} onComplete={completeRole} />
  // Code, QA and PRs of an AI-authored pipeline each have a bench of their own.
  // Discovery, SPEC and Plan chosen in the stage bar open the approved documents, not a bench.
  const documentView = viewStage === 'discovery' || viewStage === 'spec' || viewStage === 'plan'
  // What the person picks for Code or QA in the lab becomes the project's choice for that phase.
  async function saveRoleChoice(stage: 'code' | 'eval', backendId: string, modelId: string) {
    if (!workspaceId) return
    try {
      const saved = await backend.saveStageExecutor({ workspaceId, stage, backendId, modelId })
      setStageExecutors(current => [...current.filter(item => item.stage !== stage), saved])
    } catch { /* The run still uses the pick; only the default for next time was not saved. */ }
  }
  const benchMode = !documentView && !!run && run.kind === 'ai_authoring' && !recoverLegacyCodeReview && (run.currentStage === 'code' || run.currentStage === 'eval' || run.currentStage === 'prs' || (run.currentStage === '' && run.stageStatus.code === 'completed'))
  // Outside the benches, the screen shows the documents asked for in the bar, or the current stage.
  useEffect(() => { if (!benchMode) onShownStage?.(documentView ? viewStage : run?.currentStage || undefined) }, [benchMode, documentView, viewStage, run?.currentStage, onShownStage])
  const codeRun = useCodeRun(backend, run)
  useAutoVerifyCode(backend, run, codeRun, updated => { onSelectPipeline(updated); setNotice(t('Code salvo para revisão. Aprove a versão para liberar o QA.')) })
  const codeBenchView = run && <CodeBench backend={backend} run={run} onPipelineChange={onSelectPipeline} live={codeIsRunning(codeRun) ? <CodeLiveRun backend={backend} activity={codeRun!} onOpenSession={sessionId => void onOpenSession?.(sessionId)} /> : undefined} runControls={run.currentStage === 'code' && !fixing && run.stageStatus.code !== 'waiting_user' ? <PipelineRoleControls run={run} backend={backend} backends={backends} workspaceId={workspaceId} settingsStatus={settingsStatus} defaultModelBackendId={roleModelBackendId} defaultModelId={roleModelId} phaseConfigured={phaseModelApplies} phaseNote={phaseNote} choicesReady={choicesReady} selectedBackendId={selectedBackendId} selectedBackend={selectedBackend} pending={pending} onSettings={onSettings}
                onBackendChange={value => { setBackendChoice(value); setError(undefined) }} onStart={startRole} onComplete={completeRole} /> : undefined} />
  const qaBenchView = !run ? null : run.currentStage === 'eval' || fixing || run.artifacts.eval
    ? <PipelineQALab key={run.id} resumable={resumable} backend={backend} backends={backends} run={run} workspaceId={workspaceId} roleDefaults={roleDefaults} onPipelineChange={onSelectPipeline} onOpenSession={sessionId => onOpenSession?.(sessionId)} onLoopChange={setQALoop} onSettings={onSettings} onRoleChosen={saveRoleChoice} />
    : <p className="bench-empty">{t('A QA começa quando o Code for aprovado.')}</p>
  const prsBenchView = !run ? null : pullRequestsOpen(run)
    ? !choicesReady ? <p className="muted" role="status">{t('Lendo as escolhas por fase…')}</p> : <PipelinePRsPanel backend={backend} backends={backends} run={run} workspaceId={workspaceId} defaultBackendId={preferredBackendId} pending={pending}
      permissionProfile={permissionProfile} permissionControl={permissionControl} configured={stageExecutors.find(item => item.stage === 'prs')} onStart={(backendId, selection) => void startRole('publisher', backendId, selection, false)} onFinish={(outcome, reason) => void finishPullRequests(outcome, reason)} onOpenSession={onOpenSession} onApply={applyForPullRequests} onOpenMCP={onMCPServers} onSettings={onSettings} />
    : run.stageStatus.prs === 'completed' || run.stageStatus.prs === 'skipped' ? <PipelinePRsOutcome backend={backend} run={run} onOpenSession={onOpenSession} />
      : <p className="bench-empty">{t('Os PRs abrem quando a QA for aprovada.')}</p>

  useLayoutEffect(() => {
    createWorkspace.current = workspaceId
    createEpoch.current++
    createIntent.current = creationDraft?.intent
    setPending(false); setError(undefined); setNotice(undefined); setDiscovery(creationDraft?.discovery ?? ''); setCreateOpen(creationDraft?.open ?? false); setAutoPrepare(undefined)
    setCreationScope({ backend, workspaceId })
    lastNewWorkRequest.current = creationDraft?.newWorkRequest ?? 0
    return () => { createEpoch.current++ }
  }, [backend, workspaceId])

  async function refresh() {
    if (!workspaceId) return
    const generation = ++refreshGeneration.current
    setState('loading')
    try {
      const result = await backend.listPipelines(workspaceId)
      const selectedID = result.find(item => item.id === run?.id)?.id ?? result[0]?.id
      const detail = selectedID ? await backend.getPipeline(selectedID) : undefined
      if (generation !== refreshGeneration.current) return
      setItems(result)
      onSelectPipeline(detail)
      setState('ready')
    } catch { if (generation === refreshGeneration.current) setState('error') }
  }
  useEffect(() => { void refresh(); return () => { refreshGeneration.current++ } }, [backend, workspaceId])
  useEffect(() => {
    const unusedSeed = !!initialDiscoveryRequestId && initialDiscovery !== undefined && !consumedDiscoverySeeds.current.has(initialDiscoveryRequestId)
    if (newWorkRequest > 0 && newWorkRequest !== lastNewWorkRequest.current) { setCreateOpen(true); if (!unusedSeed) setDiscovery('') }
    lastNewWorkRequest.current = newWorkRequest
    if (newWorkRequest > 0 && unusedSeed) {
      consumedDiscoverySeeds.current.add(initialDiscoveryRequestId!)
      setDiscovery(initialDiscovery!)
      onInitialDiscoveryConsumed?.(initialDiscoveryRequestId!)
    }
  }, [newWorkRequest, initialDiscoveryRequestId, workspaceId])
  useEffect(() => {
    if (workspaceId && creationScope.backend === backend && creationScope.workspaceId === workspaceId) onCreationDraftChange?.(workspaceId, { discovery, open: createOpen, newWorkRequest: lastNewWorkRequest.current, intent: createIntent.current })
  }, [backend, workspaceId, creationScope, discovery, createOpen, pending, newWorkRequest, onCreationDraftChange])
  useEffect(() => {
    const identity = `${run?.id ?? ''}:${run?.currentStage ?? ''}`
    const revision = run?.revision ?? 0
    const changedIdentity = draftIdentity.current !== identity
    if (changedIdentity || !draftDirty) {
      setDraft(run?.currentStage ? run.artifacts[run.currentStage]?.content ?? '' : '')
      setDraftDirty(false)
      setDraftConflict(false)
    } else if (draftRevision.current !== revision) {
      setDraftConflict(true)
    }
    draftIdentity.current = identity
    draftRevision.current = revision
    setSkipConfirm(false)
  }, [run?.id, run?.currentStage, run?.revision])
  useEffect(() => { setBackendChoice('') }, [run?.currentStage])
  useEffect(() => { setStageExecutors([]); setPhasesOpen(undefined) }, [backend, workspaceId])
  useEffect(() => {
    if (!workspaceId) return
    let live = true
    setExecutorsState('loading')
    backend.listStageExecutors(workspaceId).then(found => { if (live) { setStageExecutors(found); setExecutorsState('ready') } }).catch(() => { if (live) setExecutorsState('error') })
    return () => { live = false }
  }, [backend, workspaceId, executorsRead])
  useEffect(() => {
    let live = true
    backend.listProviderProfiles().then(found => { if (live) { setProfiles(found); setProfilesFailed(false) } }).catch(() => { if (live) setProfilesFailed(true) })
    return () => { live = false }
  }, [backend, executorsRead])
  useEffect(() => { if (viewStage) setInspectedStage(viewStage) }, [run?.id, viewStage, viewRequestId])
  useEffect(() => { if (run) setItems(current => current.some(item => item.id === run.id) ? current.map(item => item.id === run.id ? run : item) : [run, ...current]) }, [run?.id, run?.revision])
  useEffect(() => { setApplyConfirmed(false) }, [run?.id, run?.revision, run?.codeAppliedAt])
  useEffect(() => {
    if (!run) return
    const saved = (['discovery', 'spec', 'plan', 'code', 'eval'] as PipelineStage[]).filter(stage => !!run.artifacts[stage])
    if (saved.length) setInspectedStage(saved[saved.length - 1])
  }, [run?.id, run?.revision])

  async function create(event: FormEvent) {
    event.preventDefault()
    if (!workspaceId || pending) return
    const targetWorkspace = workspaceId
    const current = ++createEpoch.current
    const stillCurrent = () => createEpoch.current === current && createWorkspace.current === targetWorkspace
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const content = discovery.trim()
      const intent = createIntent.current?.workspaceId === workspaceId && createIntent.current.discovery === content
        ? createIntent.current : { workspaceId, discovery: content, requestId: crypto.randomUUID() }
      createIntent.current = intent
      onCreationDraftChange?.(workspaceId, { discovery, open: createOpen, newWorkRequest: lastNewWorkRequest.current, intent })
      const created = await backend.createAuthoringPipeline(intent)
      if (!stillCurrent()) return
      if (created.workspaceId !== targetWorkspace) { setError(t('O pipeline retornou para outro projeto. Atualize a lista antes de continuar.')); return }
      setItems(current => [created, ...current]); onSelectPipeline(created); setDiscovery(''); createIntent.current = undefined; setCreateOpen(false)
      if (created.preparationExperience === 'conversational') setAutoPrepare({ pipelineId: created.id, requestId: crypto.randomUUID() })
      // The work gets a chat of its own: it shows in the Casual list and coordinates the work from there. Best effort: without a
      // usable provider the work is still there, and the chat is made the next time one is.
      void backend.ensureWorkChats(targetWorkspace).catch(() => undefined)
    } catch (failure) { if (stillCurrent()) setError(errorMessage(failure)) }
    finally { if (stillCurrent()) setPending(false) }
  }

  async function select(id: string) {
    refreshGeneration.current++
    setPending(true); setError(undefined); setNotice(undefined)
    try { onSelectPipeline(await backend.getPipeline(id)) }
    catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function save() {
    if (!run?.currentStage || pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try { const saved = await backend.savePipelineArtifact(run.id, run.currentStage, draft); setDraftDirty(false); onSelectPipeline(saved); setNotice(t('Artefato salvo e versionado.')) }
    catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function advance() {
    if (!run || pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try { onSelectPipeline(await backend.advancePipeline(run.id)); setNotice(t('Fase avançada com evidência salva.')) }
    catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function skip() {
    if (!run || pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try { onSelectPipeline(await backend.skipPipelineStage(run.id, reason)); setReason(''); setSkipConfirm(false); setNotice(t('Etapa pulada e registrada no histórico.')) }
    catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function startRole(role: PipelineRole, backendId: string, selection: PipelineRoleModelSelection | undefined, confirmWorkspaceCopy: boolean) {
    if (!run || !backendId || pending) return
    // Code and QA run with a model confirmed against the catalog; the pull request chat does too when the project chose a model for it, and otherwise uses the profile's own.
    if (!selection && role !== 'publisher') { setError(t('Escolha um modelo confirmado para esta fase.')); return }
    setPending(true); setError(undefined); setNotice(undefined)
    try { await onStartRole(run.id, backendId, role, selection, confirmWorkspaceCopy) }
    catch (failure) { setError(errorMessage(failure)); setPending(false) }
  }

  async function completeRole(role: PipelineRole) {
    if (!run || pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const updated = role === 'coder' ? await backend.completePipelineCode(run.id) : await backend.completePipelineEvaluation(run.id)
      onSelectPipeline(updated)
      setNotice(role === 'coder' ? t('Code salvo para revisão. Aprove a versão para liberar o QA.') : t('Resultado do QA salvo. Revise as evidências e decida o próximo passo.'))
    } catch (failure) { setError(errorCode(failure) === 'evidence_required' ? unfinishedRunText(t)[role] : errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function finishPullRequests(outcome: 'completed' | 'skipped', reason?: string) {
    if (!run || pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      onSelectPipeline(await backend.finishPipelinePRs(run.id, outcome, reason))
      setNotice(outcome === 'completed' ? t('PRs concluídos. O relatório da IA ficou guardado no pipeline.') : t('Etapa de PRs pulada e registrada no histórico.'))
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  // The one-click PRs action applies the patch QA approved; the backend checks it against the approved version.
  async function applyForPullRequests(): Promise<boolean> {
    if (!run) return false
    setError(undefined)
    try { onSelectPipeline(await backend.applyPipelineCode(run.id)); return true }
    catch (failure) { setError(errorMessage(failure)); return false }
  }

  async function applyCode() {
    if (!run || pending || !applyConfirmed) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const updated = await backend.applyPipelineCode(run.id)
      onSelectPipeline(updated)
      setApplyConfirmed(false)
      setNotice(t('Patch aplicado à pasta original e conferido após a gravação.'))
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  return <div className="pipelines-page">
    <div className="destination-heading"><div><h2>Pipelines SDD</h2>{!conversational && <p className="muted">{t('Da descoberta à avaliação, com artefatos e bypass rastreáveis.')}</p>}</div><div className="pipeline-actions">{workspaceId && visibleItems.length > 0 && <button type="button" className="touch-target secondary-button" onClick={() => setCreateOpen(open => !open)}><Plus aria-hidden="true" />{createOpen ? t('Fechar criação') : t('Novo pipeline')}</button>}{workspaceId && <button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button>}</div></div>
    {!workspaceId ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>{t('Abra um projeto para começar')}</strong><span className="muted">{t('Cada pipeline pertence a uma pasta local autorizada.')}</span><button type="button" className="touch-target primary-button" onClick={onProjects}>{t('Abrir projetos')}</button></div> : <>
      <PipelinePhaseExecutors key={workspaceId} backend={backend} backends={backends} workspaceId={workspaceId} executors={stageExecutors} onExecutorsChange={setStageExecutors} profiles={profiles} loading={!choicesReady} loadFailed={choicesFailed} onRetry={() => setExecutorsRead(count => count + 1)}
        defaults={{ backendId: preferredBackendId, modelBackendId: preferredModelBackendId, modelId: preferredModelId }} open={phasesOpen ?? (state === 'ready' && visibleItems.length === 0)} onToggle={setPhasesOpen} currentStage={run?.currentStage || undefined} disabled={pending} />
      {state === 'ready' && (visibleItems.length === 0 || createOpen) && <form className="pipeline-create" onSubmit={create}><div className="destination-heading"><div><h3>{t('Novo pipeline')}</h3><p className="muted">{t('Descreva o problema, contexto e resultado esperado.')}</p></div><ListTree aria-hidden="true" /></div><div className="pipeline-create-fields"><label className="field">Discovery<textarea aria-label="Discovery" value={discovery} onChange={event => setDiscovery(event.target.value)} maxLength={20000} required rows={7} placeholder={t('O que você quer descobrir ou construir?')} /></label></div><button type="submit" className="touch-target primary-button" disabled={pending || !discovery.trim()}><Plus aria-hidden="true" />{t('Criar pipeline')}</button></form>}
      {state === 'loading' && <p className="muted" role="status">{t('Carregando pipelines…')}</p>}
      {state === 'error' && <div className="inline-error" role="alert"><p>{t('Não foi possível carregar os pipelines.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>{t('Tentar novamente')}</button></div>}
      {visibleItems.length > 0 && <div className={`pipeline-layout${conversational ? ' pipeline-layout-conversational' : ''}`}>{conversational ? <div className="pipeline-work-selector"><IonPicker id="pipeline-work" label={t('Trabalho deste projeto')} value={run.id} onChange={id => void select(id)} disabled={pending} searchable options={visibleItems.map(item => ({ value: item.id, label: item.title }))} /></div> : <section className="pipeline-list" aria-labelledby="pipeline-list-title"><h3 id="pipeline-list-title">{t('Trabalhos deste projeto')}</h3><ul>{visibleItems.map(item => <li key={item.id}><button type="button" className={`touch-target pipeline-list-item${run?.id === item.id ? ' is-selected' : ''}`} onClick={() => void select(item.id)} disabled={pending}><strong>{item.title}</strong><span className="muted">{item.currentStage ? labels[item.currentStage] : t('Encerrado')} · {new Date(item.updatedAt).toLocaleDateString(localeTag())}</span></button></li>)}</ul></section>}
        {run && <section className={`pipeline-detail${benchMode ? ' is-bench' : ''}`} aria-labelledby="pipeline-detail-title">{benchMode ? <h3 id="pipeline-detail-title" className="visually-hidden">{run.title}</h3> : <div className="destination-heading"><div><h3 id="pipeline-detail-title">{run.title}</h3>{!conversational && run.objective !== run.title && <p className="muted">{run.objective}</p>}</div><span className="status-chip status-ready">{run.currentStage ? labels[run.currentStage] : t('Encerrado')}</span></div>}
          {!conversational && <div className="pipeline-phase-list" aria-label={t('Fases do pipeline')}>{(['discovery', 'spec', 'plan', 'code', 'eval', 'prs'] as PipelineStage[]).map(stage => <span key={stage} className={`pipeline-phase phase-${run.stageStatus[stage]}`}>{labels[stage]}<small>{run.stageStatus[stage] === 'skipped' ? t('Pulada') : run.stageStatus[stage] === 'completed' ? t('Concluída') : run.stageStatus[stage] === 'active' ? t('Atual') : run.stageStatus[stage] === 'failed' ? t('Reprovada') : run.stageStatus[stage] === 'waiting_user' ? t('Aguardando decisão') : run.stageStatus[stage] === 'paused' ? t('Pausada') : t('Pendente')}</small></span>)}</div>}
          {viewStage && !(benchMode && (viewStage === 'code' || viewStage === 'eval' || viewStage === 'prs')) && <StageActivityPane key={`${run.id}:${viewStage}:${viewRequestId ?? 0}`} backend={backend} pipeline={run} stage={viewStage} />}
          {recoverLegacyCodeReview && <PipelineLegacyCodeReviewRecovery backend={backend} run={run} onPipelineChange={onSelectPipeline} />}
          {run.kind === 'ai_authoring' ? <>{benchMode ? null : conversational ? <ApprovedPreparation archived={run.currentStage === 'code' || run.currentStage === 'eval' || run.currentStage === 'prs' || run.currentStage === ''} viewStage={viewStage} viewRequestId={viewRequestId}><PipelineDesignStudio key={`${run.workspaceId}:${run.id}`} backend={backend} pipeline={run} stageExecutors={stageExecutors} onPipelineChange={onSelectPipeline} onSettings={onSettings} autoPrepareRequest={autoPrepare?.pipelineId === run.id ? autoPrepare.requestId : undefined} onAutoPrepareConsumed={requestId => setAutoPrepare(current => current?.requestId === requestId ? undefined : current)} requestedStage={viewStage === 'discovery' || viewStage === 'spec' || viewStage === 'plan' ? viewStage : undefined} viewRequestId={viewRequestId} /></ApprovedPreparation> : <AuthoringWorkbench key={`${run.workspaceId}:${run.id}:${run.currentStage}`} backend={backend} pipeline={run} onPipelineChange={onSelectPipeline} onSettings={onSettings} />}
            {benchMode ? <PipelineBench run={run} viewStage={viewStage} viewRequestId={viewRequestId} focus={fixing || resumable ? 'eval' : undefined} onShownStage={onShownStage} code={codeBenchView} qa={qaBenchView} prs={prsBenchView} /> : <>{executionView}
            <PipelineArtifactArchive run={run} stages={['code', 'eval']} inspectedStage={inspectedStage} onInspect={setInspectedStage} /></>}
          </> : run.currentStage && run.currentStage !== 'prs' && <div className="pipeline-editor"><div className="pipeline-editor-heading"><FileText aria-hidden="true" /><div><strong>{t('Artefato de {stage}', { stage: labels[run.currentStage] })}</strong><span className="muted">{run.artifacts[run.currentStage] ? t('Versão {version}', { version: run.artifacts[run.currentStage].version }) : t('Ainda não salvo')}</span></div></div>
            {editable.has(run.currentStage) ? <><label className="field">{t('Artefato da fase')}<textarea value={draft} onChange={event => { setDraft(event.target.value); setDraftDirty(true) }} rows={10} placeholder={t('Registre decisões, critérios e evidências desta fase.')} /></label>{draftConflict && <div className="project-warning pipeline-draft-conflict" role="alert">{t('A fase mudou enquanto você editava. Copie o rascunho se precisar preservá-lo antes de carregar a versão atual.')}<button type="button" className="touch-target secondary-button" onClick={() => { setDraft(run.artifacts[run.currentStage!]?.content ?? ''); setDraftDirty(false); setDraftConflict(false) }}>{t('Descartar rascunho e carregar atualização')}</button></div>}<div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => void save()} disabled={pending || draftConflict || !draft.trim()}>{t('Salvar artefato')}</button><button type="button" className="touch-target primary-button" onClick={() => void advance()} disabled={pending || draftConflict || !run.artifacts[run.currentStage]}><ArrowRight aria-hidden="true" />{t('Avançar fase')}</button></div>
              {!skipConfirm ? <button type="button" className="touch-target text-button pipeline-skip" onClick={() => setSkipConfirm(true)}><SkipForward aria-hidden="true" />{t('Pular esta etapa')}</button> : <div className="pipeline-skip-confirm"><p>{t('Pular registra a etapa como pulada, sem tratá-la como aprovada.')}</p><label className="field">{t('Motivo (opcional)')}<input value={reason} onChange={event => setReason(event.target.value)} maxLength={1000} /></label><div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => setSkipConfirm(false)}>{t('Voltar')}</button><button type="button" className="touch-target secondary-button" onClick={() => void skip()} disabled={pending}>{t('Confirmar pulo')}</button></div></div>}
            </> : executionView}
          </div>}
          {!recoverLegacyCodeReview && run.currentStage === '' && run.stageStatus.code === 'completed' && run.stageStatus.eval === 'completed' && run.artifacts.code?.author === 'ai' && run.artifacts.code.sourceSessionId && <section className="pipeline-apply-panel" aria-label={t('Aplicar patch aprovado')}>
            <div><h3>{t('Patch avaliado')}</h3><p className="muted">{t('O QA foi concluído. Aplicar este patch é uma ação separada; o conteúdo será conferido com a versão aprovada antes e depois da gravação.')}</p></div>
            {run.codeAppliedAt ? <p className="form-success" role="status">{t('Aplicado e conferido em {date}.', { date: new Date(run.codeAppliedAt).toLocaleString(localeTag()) })}</p> : <><label className="authoring-confirm"><input type="checkbox" checked={applyConfirmed} onChange={event => setApplyConfirmed(event.target.checked)} disabled={pending} />{t('Confirmo aplicar ao projeto original o patch exibido em Code.')}</label><div className="pipeline-actions"><button type="button" className="touch-target primary-button" onClick={() => void applyCode()} disabled={pending || !applyConfirmed}>{t('Aplicar patch aprovado')}</button></div></>}
          </section>}
          {!recoverLegacyCodeReview && !benchMode && (pullRequestsOpen(run)
            ? !choicesReady ? <p className="muted" role="status">{t('Lendo as escolhas por fase…')}</p> : <PipelinePRsPanel backend={backend} backends={backends} run={run} workspaceId={workspaceId} defaultBackendId={preferredBackendId} pending={pending}
              permissionProfile={permissionProfile} permissionControl={permissionControl} configured={stageExecutors.find(item => item.stage === 'prs')} onStart={(backendId, selection) => void startRole('publisher', backendId, selection, false)} onFinish={(outcome, reason) => void finishPullRequests(outcome, reason)} onOpenSession={onOpenSession} onApply={applyForPullRequests} />
            : <PipelinePRsOutcome backend={backend} run={run} onOpenSession={onOpenSession} />)}
          {run.kind !== 'ai_authoring' && Object.keys(run.artifacts).length > 0 && <section className="pipeline-archive" aria-labelledby="pipeline-archive-title"><h3 id="pipeline-archive-title">{t('Artefatos salvos')}</h3><div className="pipeline-archive-tabs">{(['discovery', 'spec', 'plan', 'code', 'eval', 'prs'] as PipelineStage[]).filter(stage => !!run.artifacts[stage]).map(stage => <button type="button" key={stage} className="touch-target secondary-button" aria-pressed={inspectedStage === stage} onClick={() => setInspectedStage(stage)}>{t('Ver {stage}', { stage: labels[stage] })}</button>)}</div>{run.artifacts[inspectedStage]?.reviewStatus === 'stale' && <p className="project-warning" role="status">{t('Este QA pertence a uma versão anterior de Code e não libera aplicação.')}</p>}<pre className="mono diff-body">{run.artifacts[inspectedStage]?.content ?? ''}</pre></section>}
        </section>}
      </div>}
      {state === 'ready' && visibleItems.length === 0 && <div className="catalog-empty"><ListTree aria-hidden="true" /><strong>{t('Nenhum pipeline neste projeto')}</strong><span className="muted">{t('Crie um acima para começar pela Discovery.')}</span></div>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
  </div>
}
