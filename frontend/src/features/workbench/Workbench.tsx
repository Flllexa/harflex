import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { useStore } from 'zustand'
import { FolderOpen, Settings2, Workflow, X } from 'lucide-react'
import { WorkArea, shortPath } from '../../components/WorkArea'
import { IonPicker } from '../../components/IonPicker'
import { preferredFirst } from '../../lib/backendOrder'
import { cliCatalogProblem, codedError, errorCode, errorMessage, type Agent, type AgentEvent, type Backend, type BackendOption, type Delegation, type ModelCatalogResult, type Pipeline, type PipelineRole, type PipelineStage, type PipelineRoleModelSelection, type RunResult, type Session, type Workspace, type WorkspaceSummary, type WorktreeList, type WorktreeSavePlan } from '../../lib/backend'
import { checkReplayOutcome, replayErrorMessage, replaySession, type ReplayExpectation } from '../../lib/replay'
import { useRecentWork, workProgress } from '../pipelines/RecentWork'
import { useVerifyCoderWhenDone } from './useVerifyCoderWhenDone'
import { createSessionStore, runIsLive, sessionReadOnly, type SessionState } from '../../state/session'
import { saveChatDraft, takeChatDraft } from '../../state/chatDrafts'
import { ProviderDialog } from '../settings/ProviderDialog'
import { ArtifactPane } from './ArtifactPane'
import { ConversationPane } from './ConversationPane'
import { continuationPrompt, requestOf } from './continuation'
import { CasualStartPanel } from './CasualStartPanel'
import { ChatModelBar } from './ChatModelBar'
import { ChatProjectPicker } from './ChatProjectPicker'
import { useChatModelChoice, type ChatChoice } from './useChatModelChoice'
import type { Destination } from '../../components/Sidebar'
import { ProjectsPage } from '../projects/ProjectsPage'
import { ProjectMemoryPage } from '../projects/ProjectMemoryPage'
import { ProjectMemoryPrompt } from '../projects/ProjectMemoryPrompt'
import { SettingsPage } from '../settings/SettingsPage'
import { PermissionPicker } from '../permissions/PermissionPicker'
import { LogsPage } from '../logs/LogsPage'
import { MetricsPane } from './MetricsPane'
import { RepositoryPage } from '../repositories/RepositoryPage'
import { WorktreesPage } from '../worktrees/WorktreesPage'
import { WorktreeBar } from '../worktrees/WorktreeBar'
import { WorktreeNotice } from '../worktrees/WorktreeNotice'
import type { SaveWithAIOptions } from '../worktrees/WorktreeDialogs'
import type { WorktreeCleanup } from '../worktrees/cleanup'
import { useWorktrees } from '../worktrees/useWorktrees'
import { blockerText } from '../worktrees/worktreeText'
import { PipelinesPage, type PipelineCreationDraft } from '../pipelines/PipelinesPage'
import { AgentsPage } from '../agents/AgentsPage'
import { WorkflowsPage } from '../workflows/WorkflowsPage'
import { SchedulesPage } from '../schedules/SchedulesPage'
import { SkillsPage } from '../skills/SkillsPage'
import { MCPPage } from '../mcp/MCPPage'
import { DiagnosticsPage } from '../diagnostics/DiagnosticsPage'
import { ExecutionPage } from '../execution/ExecutionPage'
import { KnowledgePage } from '../knowledge/KnowledgePage'
import { VaultPage } from '../vault/VaultPage'
import { ChannelsPage } from '../channels/ChannelsPage'
import type { AppMode } from '../../state/appMode'
import { localeTag, t, useT } from '../../i18n'

const basename = (path: string) => path.split(/[\\/]/).filter(Boolean).pop() ?? path
function backendLabel(state: Pick<SessionState, 'session' | 'backends'>): string | undefined {
  const session = state.session
  if (!session) return undefined
  const known = state.backends.find(item => item.id === session.backendId)
  return known ? `${known.name} · ${known.kind === 'api' ? 'API' : 'CLI'}` : session.backendId
}
const capabilityNote = 'Ferramentas de arquivo ficam nesta pasta. Shell e agentes CLI podem acessar recursos permitidos pela sua conta; revise cada aprovação.'
const skipReasons = ['Pergunta rápida', 'Explorar o código', 'Ajuste pontual', 'Tarefa trivial']
const backendMemory = 'harflex.lastBackend'
function rememberedBackend(): string { try { return localStorage.getItem(backendMemory) ?? '' } catch { return '' } }
function rememberBackend(id: string) { try { localStorage.setItem(backendMemory, id) } catch { /* storage may be unavailable */ } }

export type HistoryOpenRequest = { requestId: string; sessionId: string; workspaceId: string; /** Set when the chat belongs to another project, which is opened first. */ workspacePath?: string }
export type ProjectOpenRequest = { requestId: string; path: string }
export type WorkbenchContext = { workspace?: Workspace; session?: Session; busy: boolean; startingSession?: boolean; loadingHistory: boolean; hasDraft: boolean; historyHasUserMessage: boolean; readOnly: boolean; recoveryPrompt: string }
type WorkbenchProps = {
  backend: Backend
  /** Reopen the most recently used project at startup (default). */
  restoreProject?: boolean
  selected: Destination
  newWorkRequest?: number
  newChatPrompt?: string
  viewMode?: AppMode
  historyOpenRequest?: HistoryOpenRequest
  projectOpenRequest?: ProjectOpenRequest
  stageViewRequest?: {requestId:number;pipelineId:string;workspaceId:string;stage?:PipelineStage}
  /** Opens a work item of the project on the Pipelines screen. */
  onOpenPipeline?: (pipelineId: string, workspaceId: string) => void
  onNavigate?: (destination: Destination) => void
  onActivity?: (events: SessionState['events']) => void
  onPipeline?: (pipeline?: Pipeline) => void
  /** The pipeline stage whose screen is showing on the Pipelines page. */
  onShownStage?: (stage?: PipelineStage) => void
  onContextChange?: (context: WorkbenchContext) => void
}

function queuedDelegationTask(events: SessionState['events']): string {
  if (events.some(event => event.type === 'message.user' || event.type === 'run.started' || event.type === 'external.run.started')) return ''
  const queued = events.find(event => event.type === 'subagent.prompt.queued')?.data
  if (!queued || typeof queued !== 'object' || !('prompt' in queued)) return ''
  return typeof queued.prompt === 'string' ? queued.prompt : ''
}

function bounded<T>(request: Promise<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('request_timeout')), 15_000)
    request.then(resolve, reject).finally(() => clearTimeout(timer))
  })
}

/** `text` is what was sent; `draft` is what the composer held, when that differs (a continued conversation sends the person's words plus context). */
type PendingDraft = { sessionId: string; text: string; draft?: string; afterSequence: number; draftRevision: number }
type SettingsReadState = 'loading' | 'ready' | 'error'
/** Failures of a prompt that mean the conversation cannot take another one, however it looked when it was opened. */
const closesConversation = new Set(['session_not_resumable', 'pipeline_code_session_closed', 'backend_changed', 'history_too_large'])

/** A CLI session needs a model its own catalog confirms; the saved default is used only when it does. */
function catalogConfirms(catalog: ModelCatalogResult, backendId: string, model: string): boolean {
  return catalog.complete && catalog.status === 'complete' && !!catalog.profileRevision
    && catalog.models.some(item => item.id === model && item.backendId === backendId && item.source === catalog.source && item.availability !== 'unavailable')
}

function userMessageContent(event: AgentEvent): string | undefined {
  if (event.type !== 'message.user' || !event.data || typeof event.data !== 'object' || Array.isArray(event.data)) return undefined
  const content = (event.data as Record<string, unknown>).content
  return typeof content === 'string' ? content : undefined
}

export function Workbench({ backend, restoreProject = true, selected, newWorkRequest = 0, newChatPrompt = '', viewMode = 'professional', historyOpenRequest, projectOpenRequest, stageViewRequest, onOpenPipeline, onNavigate = () => undefined, onActivity = () => undefined, onPipeline = () => undefined, onShownStage, onContextChange }: WorkbenchProps) {
  const t = useT()
  const [store] = useState(createSessionStore)
  const [loadingHistory, setLoadingHistory] = useState(false)
  const [cancelPending, setCancelPending] = useState(false)
  const [defaultBackendId, setDefaultBackendId] = useState('')
  const [defaultModelBackendId, setDefaultModelBackendId] = useState('')
  const [defaultModelId, setDefaultModelId] = useState('')
  const [settingsReadState, setSettingsReadState] = useState<SettingsReadState>('loading')
  const [initialChatPrompt, setInitialChatPrompt] = useState('')
  const [initialDiscoverySeed,setInitialDiscoverySeed] = useState<{workspaceId:string;requestId:string;text:string}>()
  const pipelineCreationDrafts = useRef(new WeakMap<Backend, Map<string, PipelineCreationDraft>>())
  const rememberPipelineCreation = useCallback((workspaceId: string, draft: PipelineCreationDraft) => {
    let drafts = pipelineCreationDrafts.current.get(backend)
    if (!drafts) { drafts = new Map(); pipelineCreationDrafts.current.set(backend, drafts) }
    drafts.set(workspaceId, draft)
  }, [backend])
  const [startingSession,setStartingSession] = useState(false)
  const [currentPipeline, setCurrentPipeline] = useState<Pipeline>()
  // A pipeline the chat agent just created, offered until the person opens or dismisses it.
  const [agentPipeline, setAgentPipeline] = useState<Pipeline>()
  const [sessionPipeline, setSessionPipeline] = useState<Pipeline>()
  const [delegation, setDelegation] = useState<Delegation>()
  const delegationReadOrder = useRef(0)
  const [sddLaunchRequest, setSDDLaunchRequest] = useState(0)
  const [reviewedWorkflowSessions, setReviewedWorkflowSessions] = useState<ReadonlySet<string>>(new Set())
  const [savedProjects, setSavedProjects] = useState<WorkspaceSummary[]>([])
  const [lastBackendId, setLastBackendId] = useState(rememberedBackend)
  const state = useStore(store)
  useEffect(() => { setInitialDiscoverySeed(current => current?.workspaceId === state.workspace?.id ? current : undefined) },[state.workspace?.id])
  const selectedRef = useRef(selected)
  selectedRef.current = selected
  useVerifyCoderWhenDone({ backend, session: state.session, activeRun: state.activeRun, outcome: state.outcome?.status, pipeline: sessionPipeline, onVerified: updated => { setCurrentPipeline(updated); onOpenPipeline?.(updated.id, updated.workspaceId) } })
  const busy = startingSession || state.calling || runIsLive(state) || loadingHistory || cancelPending
  const executionBusy = runIsLive(state) || state.calling && !loadingHistory || cancelPending
  const userChat = !state.session?.purpose || state.session.purpose === 'chat'
  const historyHasUserMessage = userChat && state.events.some(event => event.type === 'message.user')
  const recoveryPrompt = userChat && state.readOnly ? requestOf([...state.messages].reverse().find(message => message.kind === 'user')?.text ?? '') : ''
  const historyOpenOrder = useRef(0)
  const handledNewWorkRequest = useRef(0)
  // Pages that default to the first usable backend should agree with the setup screen's choice.
  const orderedBackends = useMemo(() => preferredFirst(state.backends, defaultBackendId, lastBackendId), [state.backends, defaultBackendId, lastBackendId])
  // The task's worktree: read it again after every run (the agent may have edited files) and when its page opens.
  const [worktreeRefresh, setWorktreeRefresh] = useState(0)
  const worktrees = useWorktrees(backend, state.workspace?.id, worktreeRefresh)
  const [worktreeCleanup, setWorktreeCleanup] = useState<WorktreeCleanup>()
  const cleanupSawRun = useRef(false)
  const previousRun = useRef(state.activeRun)
  useEffect(() => {
    const wasActive = previousRun.current === 'running' || previousRun.current === 'awaiting_approval'
    previousRun.current = state.activeRun
    if (wasActive && state.activeRun !== 'running' && state.activeRun !== 'awaiting_approval') setWorktreeRefresh(current => current + 1)
  }, [state.activeRun])
  useEffect(() => { if (selected === 'Worktrees') void worktrees.refresh() }, [selected])
  useEffect(() => backend.onPipelineCreated(change => {
    if (change.workspaceId !== store.getState().workspace?.id) return
    void backend.getPipeline(change.pipelineId).then(value => { if (value.workspaceId === store.getState().workspace?.id) setAgentPipeline(value) }, () => undefined)
  }), [backend])
  useEffect(() => {
    const cleanup = worktreeCleanup
    // After a check that found work still pending, a follow-up run in the same conversation is checked again when it ends.
    if (!cleanup || (cleanup.phase !== 'saving' && cleanup.phase !== 'attention') || state.session?.id !== cleanup.sessionId) return
    if (state.calling || state.activeRun === 'running' || state.activeRun === 'awaiting_approval') { cleanupSawRun.current = true; return }
    if (cleanupSawRun.current && !loadingHistory) { cleanupSawRun.current = false; void finishWorktreeCleanup(cleanup) }
  }, [state.session?.id, state.calling, state.activeRun, worktreeCleanup?.phase, worktreeCleanup?.sessionId, loadingHistory])
  useEffect(() => { onActivity(state.events) }, [onActivity, state.events])
  useEffect(() => { onPipeline(selected === 'Pipelines' ? currentPipeline : state.session ? sessionPipeline : currentPipeline) }, [onPipeline, selected, currentPipeline, sessionPipeline, state.session?.id])
  useEffect(() => {
    if (!stageViewRequest || stageViewRequest.workspaceId !== state.workspace?.id) return
    let live = true
    void backend.getPipeline(stageViewRequest.pipelineId).then(value => {
      if (live && store.getState().workspace?.id === stageViewRequest.workspaceId && value.workspaceId === stageViewRequest.workspaceId && value.id === stageViewRequest.pipelineId) setCurrentPipeline(value)
    }).catch(failure => { if (live) store.setState({error:errorMessage(failure)}) })
    return () => { live = false }
  },[backend,stageViewRequest?.requestId,state.workspace?.id])
  useEffect(() => {
    onContextChange?.({ workspace: state.workspace, session: state.session, busy: executionBusy || startingSession, startingSession, loadingHistory, hasDraft: !!state.draft.trim(), historyHasUserMessage, readOnly: state.readOnly, recoveryPrompt })
  }, [onContextChange, state.workspace?.id, state.workspace?.path, state.session?.id, state.session?.status, state.session?.resumable, state.session?.updatedAt, state.draft, executionBusy, startingSession, loadingHistory, historyHasUserMessage, state.readOnly, recoveryPrompt])
  useEffect(() => {
    const sessionId = state.session?.id
    if (!sessionId) { delegationReadOrder.current++; setDelegation(undefined); return }
    setDelegation(undefined)
    void refreshDelegation(sessionId).catch(() => setDelegation(undefined))
    return () => { delegationReadOrder.current++ }
  }, [backend, state.session?.id])
  // A Code or QA conversation can be closed from the Pipelines page (verifying the Code, recording the verdict): when the
  // person comes back to it, ask what it can still do instead of letting the first message find out.
  useEffect(() => {
    const purpose = state.session?.purpose
    if (selected === 'Conversas' && (purpose === 'code' || purpose === 'evaluation') && !executionBusy && !loadingHistory) void refreshContinuity()
  }, [selected])
  async function refreshDelegation(sessionId: string) {
    const order = ++delegationReadOrder.current
    const link = await backend.getParentDelegation(sessionId)
    if (order === delegationReadOrder.current && store.getState().session?.id === sessionId) setDelegation(link ?? undefined)
  }
  useEffect(() => {
    const sessionID = state.session?.id, workspaceID = state.workspace?.id
    if (!sessionID || !workspaceID) { setSessionPipeline(undefined); return }
    setSessionPipeline(undefined)
    let live = true
    backend.getPipelineForSession(sessionID, workspaceID).then(linked => { if (live) setSessionPipeline(linked) }, () => { if (live) setSessionPipeline(undefined) })
    return () => { live = false }
  }, [backend, state.session?.id, state.workspace?.id, currentPipeline?.revision])
  useEffect(() => {
    const workspaceId = state.workspace?.id
    if (!workspaceId) { setCurrentPipeline(undefined); return }
    let live = true
    backend.listPipelines(workspaceId).then(async runs => {
      const selected = runs.find(run => run.currentStage) ?? runs[0]
      if (!selected) { if (live) setCurrentPipeline(undefined); return }
      const detailed = await backend.getPipeline(selected.id)
      if (live && detailed.workspaceId === workspaceId) setCurrentPipeline(detailed)
    }).catch(() => { if (live) setCurrentPipeline(undefined) })
    return () => { live = false }
  }, [backend, state.workspace?.id])
  const lifetime = useRef<AbortController>()
  const expectedReplay = useRef<ReplayExpectation>()
  const pendingDraft = useRef<PendingDraft>()
  const draftRevision = useRef(0)
  const selection = useRef<AbortController>()
  const newWorkPending = useRef(false)
  const continuationPending = useRef(false)
  // The context of a continued conversation travels with the first message of the new one, even when that first send
  // has to be retried from the box.
  const continuationContext = useRef<{ sessionId: string; context: string }>()
  const cancelPendingRef = useRef(false)

  const reconcilePendingDraft = useCallback(() => {
    const pending = pendingDraft.current
    if (!pending || !store.getState().events.some(event => event.streamId === pending.sessionId
      && event.sequence > pending.afterSequence && userMessageContent(event) === pending.text)) return false
    const current = store.getState()
    if (draftRevision.current === pending.draftRevision && current.draft === (pending.draft ?? pending.text)) current.setDraft('')
    if (continuationContext.current?.sessionId === pending.sessionId) continuationContext.current = undefined
    pendingDraft.current = undefined
    return true
  }, [store])

  useEffect(() => {
    reconcilePendingDraft()
  }, [state.events, state.draft, reconcilePendingDraft])

  useEffect(() => {
    let live = true
    const controller = new AbortController()
    lifetime.current = controller
    const unsubscribe = backend.onEvent(event => { if (live) store.getState().receive([event]) })
    setDefaultBackendId('')
    setDefaultModelBackendId('')
    setDefaultModelId('')
    setSettingsReadState('loading')
    backend.listBackends().then(
      backends => { if (live) store.getState().setConnection('ready', backends) },
      () => { if (live) store.getState().setConnection('unavailable') },
    )
    backend.getSettings().then(settings => {
      if (live) { setDefaultBackendId(settings.defaultBackendId); setDefaultModelBackendId(settings.defaultModelBackendId); setDefaultModelId(settings.defaultModelId); setSettingsReadState('ready') }
    }, () => { if (live) { setDefaultBackendId(''); setDefaultModelBackendId(''); setDefaultModelId(''); setSettingsReadState('error') } })
    // Pick up where the last launch stopped; the catalog lists the most recently opened project first.
    void backend.listWorkspaces().then(async items => {
      const reachable = items.filter(item => item.available && !item.archived)
      if (live) setSavedProjects(reachable.slice(0, 5))
      const last = restoreProject ? reachable[0] : undefined
      if (!live || !last || store.getState().workspace) return
      const workspace = await backend.openWorkspace(last.path)
      if (live && !store.getState().workspace) store.getState().setWorkspace(workspace)
    }).catch(() => undefined)
    return () => { live = false; controller.abort(); selection.current?.abort(); unsubscribe() }
  }, [backend, store])

  useEffect(() => {
    if (newWorkRequest === 0 || handledNewWorkRequest.current === newWorkRequest) return
    handledNewWorkRequest.current = newWorkRequest
    const current = store.getState()
    if (current.calling && !loadingHistory || runIsLive(current)) {
      onNavigate('Conversas')
      store.setState({ error: t('Cancele ou conclua a execução atual antes de iniciar outro trabalho.') })
      return
    }
    stashDraft()
    historyOpenOrder.current++
    selection.current?.abort()
    selection.current = undefined
    expectedReplay.current = undefined
    pendingDraft.current = undefined
    setInitialChatPrompt(newChatPrompt)
    setLoadingHistory(false)
    if (current.workspace) current.setWorkspace(current.workspace)
    newWorkPending.current = !current.workspace
    onNavigate(current.workspace ? viewMode === 'casual' ? 'Conversas' : 'Pipelines' : 'Projetos')
  }, [newWorkRequest, newChatPrompt, store, viewMode, loadingHistory])

  // Leaving a chat keeps what was typed in it, to come back when the chat is opened again.
  function stashDraft() {
    const current = store.getState()
    if (!current.session || !current.draft.trim()) return
    saveChatDraft(current.session.id, current.draft)
    draftRevision.current++
    current.setDraft('')
  }
  async function activateSession(session: Session, workspaceId: string): Promise<boolean> {
    const signal = lifetime.current?.signal
    if (!signal || signal.aborted || store.getState().workspace?.id !== workspaceId || session.workspaceId !== workspaceId) return false
    selection.current?.abort()
    const controller = new AbortController()
    selection.current = controller
    const current = () => !signal.aborted && !controller.signal.aborted && selection.current === controller
      && store.getState().workspace?.id === workspaceId && store.getState().session?.id === session.id
    expectedReplay.current = undefined
    pendingDraft.current = undefined
    store.getState().setSession(session)
    store.getState().startCall()
    setLoadingHistory(true)
    try {
      await replaySession(backend, store, controller.signal)
      if (!current()) return false
      store.getState().setConnection('ready')
      store.getState().finishCall()
      const saved = takeChatDraft(session.id)
      if (saved && !store.getState().draft.trim()) store.getState().setDraft(saved)
      return true
    } catch {
      if (!current()) return false
      store.getState().setConnection('degraded')
      store.getState().finishCall(undefined, t(replayErrorMessage))
      return false
    } finally {
      if (current()) setLoadingHistory(false)
    }
  }

  // Pushed events may be missed while a call is in flight; the journal replay fills gaps.
  async function catchUp() {
    const signal = lifetime.current?.signal
    if (!signal || signal.aborted) return
    try { await replaySession(backend, store, signal) } catch (failure) {
      if (!signal.aborted) throw failure
    }
  }

  async function refreshContinuity() {
    const session = store.getState().session
    const signal = lifetime.current?.signal
    if (!session || !signal || signal.aborted) return
    try {
      const current = await bounded(backend.openSession(session.id, session.workspaceId))
      if (!signal.aborted) store.getState().updateSession(current)
    } catch {
      if (!signal.aborted && store.getState().session?.id === session.id && !session.resumable) store.setState({ readOnly: true })
    }
  }

  async function run(action: (sessionId: string) => Promise<RunResult>): Promise<boolean> {
    const session = store.getState().session
    const signal = lifetime.current?.signal
    if (!session || !signal || signal.aborted || store.getState().readOnly || store.getState().calling) return false
    const after = store.getState().contiguousSequence
    store.getState().startCall()
    let result: RunResult | undefined
    let callError: string | undefined
    let closed = false
    try {
      result = await action(session.id)
    } catch (failure) {
      callError = errorMessage(failure)
      // The conversation was closed since it was opened (its Code was verified from the Pipelines page, the provider
      // changed): it is read-only now, and the box carries on in a new chat instead of failing on every message.
      closed = closesConversation.has(errorCode(failure))
      if (closed) store.setState({ readOnly: true })
    }
    if (signal.aborted) return false
    let delegationReadbackError: string | undefined
    try { await refreshDelegation(session.id) }
    catch { delegationReadbackError = t('Não foi possível reler o orçamento do subagente. Atualize a sessão antes de enviar outra mensagem.') }
    expectedReplay.current = result ? { after, approval: result.status === 'awaiting_approval' } : undefined
    try {
      await catchUp()
      if (signal.aborted) return false
      checkReplayOutcome(store.getState(), expectedReplay.current)
      // A call that failed for an unknown reason says nothing about the conversation: ask what it can still do. One that
      // said it is closed has answered already.
      if (!closed && (!result || result.status !== 'awaiting_approval')) await refreshContinuity()
      if (signal.aborted) return false
      expectedReplay.current = undefined
      store.getState().setConnection('ready')
      store.getState().finishCall(result, callError ?? delegationReadbackError)
      return !callError && result?.status !== 'failed'
    } catch {
      if (!signal.aborted) {
        store.getState().setConnection('degraded')
        store.getState().finishCall(undefined, t(replayErrorMessage))
      }
      return false
    }
  }

  async function retryReplay() {
    const signal = lifetime.current?.signal
    if (!signal || signal.aborted || store.getState().calling) return
    store.getState().startCall()
    try {
      await catchUp()
      if (signal.aborted) return
      reconcilePendingDraft()
      checkReplayOutcome(store.getState(), expectedReplay.current)
      if (store.getState().outcome) await refreshContinuity()
      if (signal.aborted) return
      expectedReplay.current = undefined
      store.getState().setConnection('ready')
      store.getState().finishCall()
      pendingDraft.current = undefined
    } catch {
      if (!signal.aborted) store.getState().finishCall(undefined, t(replayErrorMessage))
    }
  }

  async function sendPrompt(text: string) {
    const signal = lifetime.current?.signal
    const current = store.getState()
    const carried = continuationContext.current?.sessionId === current.session?.id ? continuationContext.current : undefined
    const sent = carried ? `${text.trimEnd()}${carried.context}` : text
    pendingDraft.current = { sessionId: current.session?.id ?? '', text: sent, draft: text, afterSequence: current.contiguousSequence, draftRevision: draftRevision.current }
    await run(id => backend.prompt(id, sent))
    if (signal?.aborted) return
    reconcilePendingDraft()
    if (!expectedReplay.current && store.getState().connectionState !== 'degraded') pendingDraft.current = undefined
  }

  function updateDraft(text: string) {
    draftRevision.current++
    store.getState().setDraft(text)
  }

  async function cancel() {
    const { session, contiguousSequence: after } = store.getState()
    const signal = lifetime.current?.signal
    if (!session || !signal || signal.aborted || cancelPendingRef.current) return
    cancelPendingRef.current = true
    setCancelPending(true)
    try {
      await backend.cancel(session.id)
      if (signal.aborted) return
      // Only a successful cancellation changes the expected journal outcome.
      expectedReplay.current = { after, approval: false }
      await retryReplay()
    } catch (failure) {
      if (!signal.aborted) store.setState({ error: errorMessage(failure) })
    } finally {
      cancelPendingRef.current = false
      if (!signal.aborted) setCancelPending(false)
    }
  }

  // A project that was never read nor asked about asks once, when it is opened.
  const [memoryQuestion, setMemoryQuestion] = useState<{ workspaceId: string; name: string }>()
  const [memoryNotice, setMemoryNotice] = useState<{ workspaceId: string; text: string }>()
  const openWorkspaceId = state.workspace?.id, openWorkspacePath = state.workspace?.path
  useEffect(() => {
    setMemoryQuestion(undefined)
    if (!openWorkspaceId || !openWorkspacePath) return
    let live = true
    void backend.getProjectMemory(openWorkspaceId).then(memory => {
      if (live && memory.status === '' && store.getState().workspace?.id === openWorkspaceId) setMemoryQuestion({ workspaceId: openWorkspaceId, name: basename(openWorkspacePath) })
    }, () => undefined)
    return () => { live = false }
  }, [backend, openWorkspaceId, openWorkspacePath])
  const queuedTask = delegation && state.session?.status === 'ready' && !loadingHistory ? queuedDelegationTask(state.events) : ''
  const chatChoice = useChatModelChoice(backend, state.workspace?.id, state.session, state.backends, viewMode === 'casual' && !!state.session && state.session.purpose !== 'preparation' && !delegation)
  const modelBar = chatChoice.choice ? <ChatModelBar backends={state.backends} backendId={chatChoice.choice.backendId} onBackend={chatChoice.setBackend} fixedModel={chatChoice.fixedModel} loading={chatChoice.loading}
    modelOptions={chatChoice.listed.map(item => ({ value: item.id, label: item.displayName || item.id, disabled: item.availability === 'unavailable' }))} modelId={chatChoice.choice.modelId} onModel={chatChoice.setModel}
    efforts={chatChoice.model?.supportedReasoningEfforts ?? []} effort={chatChoice.choice.effort} onEffort={chatChoice.setEffort} disabled={busy || loadingHistory} /> : undefined
  const conversation = state.session?.purpose === 'preparation'
    ? <section className="setup-step"><h2>{t('Preparação do trabalho')}</h2><p className="muted">{t('A conversa de Discovery, SPEC e Plan está na área do trabalho.')}</p><button type="button" className="touch-target primary-button" onClick={() => onNavigate('Pipelines')}>{t('Abrir documentos e atividade')}</button></section>
    : state.session
    ? <ConversationPane state={state} loadingHistory={loadingHistory} cancelPending={cancelPending} permissionControl={state.workspace ? <PermissionPicker backend={backend} workspace={state.workspace} onWorkspaceUpdated={workspace => store.setState({ workspace })} /> : undefined} viewMode={viewMode} backendLabel={backendLabel(state)} modelBar={modelBar} projectControl={viewMode === 'casual' && state.workspace ? <ChatProjectPicker backend={backend} workspace={state.workspace} disabled={busy || loadingHistory} onPick={path => void switchProject(path)} /> : undefined} switchModel={chatChoice.changed ? { usable: chatChoice.usable, onReset: chatChoice.reset } : undefined} delegationParentId={delegation?.parentSessionId} delegation={delegation} queuedTask={queuedTask} onPrepareDelegatedTask={() => { if (!store.getState().draft && queuedTask) store.getState().setDraft(queuedTask) }} onOpenParent={() => { if (delegation) void openDelegatedSession(delegation.parentSessionId).catch(failure => store.setState({ error: errorMessage(failure) })) }} onDraft={updateDraft} onPrompt={sendPrompt} onResolve={(approvalId, allow) => { void run(id => backend.approve(id, approvalId, allow)) }} onCancel={cancel} onRetry={retryReplay} onContinue={text => continueInNewChat(text, chatChoice.changed ? chatChoice.choice : undefined)} onNewWork={recoveryPrompt => {
      if (!state.workspace || executionBusy || state.readOnly && loadingHistory) return
      historyOpenOrder.current++
      setInitialChatPrompt(userChat ? recoveryPrompt ?? '' : '')
      selection.current?.abort()
      selection.current = undefined
      setLoadingHistory(false)
      expectedReplay.current = undefined; pendingDraft.current = undefined
      store.getState().setWorkspace(state.workspace)
    }} />
      : <WorkspaceSetup backend={backend} workRevision={currentPipeline ? `${currentPipeline.id}:${currentPipeline.revision}` : ''} onOpenPipeline={onOpenPipeline} onSeeAllWork={() => onNavigate('Pipelines')} state={state} store={store} onSwitchProject={(path, draft) => void switchProject(path, draft)} onAdmissionStateChange={setStartingSession} activateSession={activateSession} recents={savedProjects} preferredBackendId={defaultBackendId} onBackendUsed={id => { rememberBackend(id); setLastBackendId(id) }} settingsReadState={settingsReadState} viewMode={viewMode} initialPrompt={initialChatPrompt} onPrompt={sendPrompt} onStartSDD={discovery => { setInitialDiscoverySeed({workspaceId:state.workspace!.id,requestId:crypto.randomUUID(),text:discovery ?? ''}); setSDDLaunchRequest(current => current + 1); onNavigate('Pipelines') }} />
  const project = state.workspace ? { name: basename(state.workspace.path), detail: state.workspace.path } : { name: t('Nenhum projeto aberto'), detail: t('Escolha uma pasta local para começar.') }
  const connection = state.connectionState === 'ready' ? t('Backend local conectado') : state.connectionState === 'connecting' ? t('Conectando ao backend local') : state.connectionState === 'degraded' ? t('Atualização dos eventos pendente') : t('Backend local indisponível')
  // The project picker of a chat: open the other project, bringing along what was typed on a new chat.
  async function switchProject(path: string, draft = '') {
    try {
      await openProject(path)
      if (draft.trim()) setInitialChatPrompt(draft)
      onNavigate('Conversas')
    } catch (failure) { store.setState({ error: errorMessage(failure) }) }
  }
  async function openProject(path: string) {
    if (busy) throw new Error('session_busy')
    stashDraft()
    const previousWorkspace = store.getState().workspace
    const workspace = await backend.openWorkspace(path)
    if (previousWorkspace?.id !== workspace.id || previousWorkspace.path !== workspace.path) setInitialChatPrompt('')
    selection.current?.abort()
    selection.current = undefined
    expectedReplay.current = undefined
    pendingDraft.current = undefined
    store.getState().setWorkspace(workspace)
    const destination = newWorkPending.current && viewMode === 'professional' ? 'Pipelines' : 'Conversas'
    newWorkPending.current = false
    onNavigate(destination)
  }
  async function startWorktreeSave(plan: WorktreeSavePlan, options: SaveWithAIOptions) {
    if (busy) throw codedError('session_busy')
    const option = state.backends.find(item => item.id === options.backendId)
    const model = option?.kind === 'cli' && defaultModelBackendId === option.id ? defaultModelId : ''
    if (!option || !option.available || (option.kind === 'cli' && (option.id === 'opencode' || !model))) throw codedError('worktree_backend_unusable')
    // The assistant works in the main checkout, where the merge happens.
    if (store.getState().workspace?.path !== plan.workspace.path) await openProject(plan.workspace.path)
    const workspace = store.getState().workspace
    if (!workspace) throw codedError('session_busy')
    let selected = {}
    if (option.kind === 'cli') {
      const catalog = await backend.queryCLIModelCatalog({ workspaceId: workspace.id, backendId: option.id })
      if (!catalogConfirms(catalog, option.id, model)) throw codedError('worktree_model_unconfirmed')
      selected = { modelId: model, reasoningEffort: '', catalogRevision: catalog.profileRevision }
    }
    const session = await backend.createDirectSession({ workspaceId: workspace.id, backendId: option.id, reason: plan.reason, ...selected })
    setLastBackendId(option.id); rememberBackend(option.id)
    if (!await activateSession(session, workspace.id)) throw codedError('session_replay_failed')
    const name = plan.branch || basename(plan.path)
    cleanupSawRun.current = false
    setWorktreeCleanup({ workspaceId: workspace.id, path: plan.path, name, branch: plan.branch, base: plan.base, sessionId: session.id, isMain: plan.path === plan.workspace.path, deleteWhenSaved: options.deleteWhenSaved, deleteBranch: options.deleteBranch,
      acknowledgeIgnored: options.acknowledgeIgnored, phase: 'saving', message: t('A IA está salvando {name} na conversa. Cada comando pede a sua aprovação; o Harflex confere no fim.', { name }) })
    onNavigate('Conversas')
    await sendPrompt(plan.prompt)
  }
  async function finishWorktreeCleanup(cleanup: WorktreeCleanup) {
    const { workspaceId } = cleanup
    // The list on screen belongs to the open project; only adopt an answer about that same project.
    const adopt = (list: WorktreeList) => { if (store.getState().workspace?.id === workspaceId) worktrees.replace(list) }
    const update = (phase: WorktreeCleanup['phase'], message: string) => setWorktreeCleanup(current => current && current.sessionId === cleanup.sessionId ? { ...current, phase, message } : current)
    update('checking', cleanup.isMain ? t('Conferindo se tudo foi commitado…') : t('Conferindo se tudo foi salvo e mesclado…'))
    try {
      const list = await backend.listWorktrees(workspaceId)
      const item = list.items.find(entry => entry.path === cleanup.path)
      if (!item) { adopt(list); update('deleted', t('O worktree {name} não existe mais.', { name: cleanup.name })); return }
      if (cleanup.isMain) {
        adopt(list)
        if (item.changed === 0) update('saved', t('As alterações de {name} foram commitadas. Os outros worktrees já podem ser salvos e mesclados na página Worktrees.', { name: cleanup.name }))
        else update('attention', t('{name} ainda tem {count} {changes}. Se a conversa continuar, o Harflex confere de novo quando a IA terminar.', { name: cleanup.name, count: item.changed, changes: item.changed === 1 ? t('alteração não salva') : t('alterações não salvas') }))
        return
      }
      if (!item.canDelete) {
        adopt(list)
        const reasons = item.blockers.filter(blocker => blocker.code !== 'nothing_to_save').map(blocker => blockerText(blocker, list.base)).join(' ')
        const onlyInUse = item.blockers.every(blocker => blocker.code === 'in_use' || blocker.code === 'nothing_to_save')
        update('attention', t('Ainda não dá para excluir {name}: {reasons} Nada foi excluído. {next}', { name: cleanup.name, reasons, next: onlyInUse ? t('Depois de fechar, use Conferir agora.') : t('Se a conversa continuar, o Harflex confere de novo quando a IA terminar.') }))
        return
      }
      if (!cleanup.deleteWhenSaved) { adopt(list); update('saved', t('Tudo salvo e mesclado em {base}. {name} já pode ser excluído na página Worktrees.', { base: cleanup.base, name: cleanup.name })); return }
      const result = await backend.deleteWorktree({ workspaceId, path: cleanup.path, deleteBranch: cleanup.deleteBranch, acknowledgeIgnored: cleanup.acknowledgeIgnored })
      adopt(result.list)
      update('deleted', t('Worktree {name} excluído. Tudo estava salvo e mesclado em {base}{extra}.', { name: cleanup.name, base: cleanup.base, extra: result.branchDeleted ? t('; a branch {branch} também foi apagada', { branch: result.branch }) : '' }))
    } catch (failure) {
      update('attention', t('Não foi possível conferir {name}: {error} Nada foi excluído.', { name: cleanup.name, error: errorMessage(failure) }))
    }
  }
  // A conversation that cannot be resumed (a finished CLI run, a Code or QA conversation already verified, a backend
  // that went away) does not end the chat: the next message opens a new conversation that carries what the old one
  // was about, so the person can go on, start something else or apply the fixes a review asked for.
  async function continueInNewChat(text: string, override?: ChatChoice) {
    const { workspace, session: previous, messages } = store.getState()
    const request = text.trim()
    if (!workspace || !previous || !request || busy || continuationPending.current) return
    const patchPending = sessionPipeline?.codePatchPending
    // A provider, model and effort the person chose in the bar is the only one tried: it is what they asked for.
    const modelOf = (option: BackendOption) => override ? override.modelId : option.kind === 'cli' && defaultModelBackendId === option.id ? defaultModelId : ''
    const effortOf = () => override?.effort ?? ''
    const usable = (option?: BackendOption): option is BackendOption => !!option && option.available && (option.kind === 'api' || (option.id !== 'opencode' && !!modelOf(option)))
    // The backend the conversation had while it still works, then the person's usual ones: each is tried in turn, so one
    // whose catalog does not confirm its model, or that refuses to start, does not leave the person stuck.
    const candidates = (override ? [state.backends.find(item => item.id === override.backendId)] : [state.backends.find(item => item.id === previous.backendId), ...orderedBackends])
      .filter((option, index, all): option is BackendOption => usable(option) && all.findIndex(other => other?.id === option?.id) === index)
    if (candidates.length === 0) { store.setState({ error: t('Nenhuma IA disponível para continuar. Configure um perfil de API, ou o modelo padrão de um CLI, em Configurações.') }); return }
    continuationPending.current = true
    store.getState().startCall()
    try {
      let created: Session | undefined
      let firstFailure: unknown
      for (const option of candidates) {
        try {
          let selected = {}
          if (option.kind === 'cli') {
            const modelId = modelOf(option)
            const catalog = await backend.queryCLIModelCatalog({ workspaceId: workspace.id, backendId: option.id })
            if (!catalogConfirms(catalog, option.id, modelId)) throw codedError('cli_model_unconfirmed')
            selected = { modelId, reasoningEffort: effortOf(), catalogRevision: catalog.profileRevision }
          }
          created = await backend.createDirectSession({ workspaceId: workspace.id, backendId: option.id, reason: override ? `Continuação da conversa ${previous.id} com outro modelo` : `Continuação da conversa ${previous.id}, encerrada`, ...selected })
          setLastBackendId(option.id); rememberBackend(option.id)
          break
        } catch (failure) { firstFailure ??= failure }
      }
      if (!created) throw firstFailure
      // The person may have opened something else while the conversation was being created.
      if (store.getState().session?.id !== previous.id || store.getState().workspace?.id !== workspace.id) { store.getState().finishCall(); return }
      // The chat of a work stays the work's chat, whichever conversation carries it now.
      await backend.continueWorkChat(previous.id, created.id).catch(() => false)
      const opened = await activateSession(created, workspace.id)
      continuationContext.current = { sessionId: created.id, context: continuationPrompt(request, { session: previous, messages, patchPending, switched: !!override }).slice(request.length) }
      // What was typed stays in the box until the journal shows the message, so a send that fails loses nothing; the
      // context stays with the new conversation for the retry.
      if (store.getState().session?.id === created.id) store.getState().setDraft(request)
      if (opened) await sendPrompt(request)
    } catch (failure) {
      if (store.getState().session?.id === previous.id) store.getState().finishCall(undefined, errorMessage(failure))
    } finally { continuationPending.current = false }
  }
  async function startPipelineRole(pipelineId: string, backendId: string, role: PipelineRole, selection: PipelineRoleModelSelection | undefined, confirmWorkspaceCopy: boolean) {
    const workspace = store.getState().workspace
    if (!workspace || busy) throw new Error('session_busy')
    const linked = await backend.createPipelineSession(pipelineId, backendId, role, selection, confirmWorkspaceCopy)
    await activateSession(linked.session, workspace.id)
    onNavigate('Conversas')
    await sendPrompt(linked.prompt)
  }
  async function startAgent(agent: Agent, reason: string) {
    const workspace = store.getState().workspace
    if (!workspace || busy) throw new Error('session_busy')
    const session = await backend.createDirectSession({ workspaceId: workspace.id, backendId: agent.backendId, agentId: agent.id, reason })
    await activateSession(session, workspace.id)
    onNavigate('Conversas')
  }
  async function delegateAgent(agent: Agent, prompt: string, requestId: string) {
    const parent = store.getState().session
    const workspace = store.getState().workspace
    if (!parent || !workspace || busy) throw new Error('session_busy')
    const delegated = await backend.delegateToAgent(parent.id, agent.id, prompt, requestId)
    const child = delegated.created ? delegated.session : await backend.openSession(delegated.session.id, workspace.id)
    if (!await activateSession(child, workspace.id)) throw new Error('session_replay_failed')
    onNavigate('Conversas')
    if (delegated.created) await sendPrompt(delegated.prompt)
    else {
      store.getState().setDraft(delegated.prompt)
      store.setState({ error: t('Tarefa já registrada neste subagente. Nenhuma execução foi repetida; revise o histórico e envie manualmente se necessário.') })
    }
  }
  async function openDelegatedSession(sessionId: string, draft?: string, keepTyped = false) {
    const current = store.getState()
    if (!current.workspace || current.calling || runIsLive(current)) throw new Error('session_busy')
    // Opening another conversation replaces this one's message box. From the pull request panel what was typed there is
    // not thrown away; the delegation flows have always taken the person to the parent or the child with the box emptied.
    if (keepTyped) stashDraft()
    const session = await backend.openSession(sessionId, current.workspace.id)
    if (!await activateSession(session, current.workspace.id)) throw new Error('session_replay_failed')
    // A prepared request waits in the box to be read and sent; nothing runs by itself.
    if (draft) store.getState().setDraft(draft)
    onNavigate('Conversas')
  }
  // A pipeline run in the background, such as the QA fix loop, shows its live conversation in the activity panel
  // without leaving the screen. It never takes over a conversation that is running or one the person is in.
  async function followPipelineSession(sessionId: string) {
    const current = store.getState()
    if (selectedRef.current !== 'Pipelines' || !current.workspace || current.session?.id === sessionId || current.calling || runIsLive(current)) return
    stashDraft()
    const session = await backend.openSession(sessionId, current.workspace.id).catch(() => undefined)
    if (!session || selectedRef.current !== 'Pipelines' || store.getState().session?.id !== current.session?.id) return
    await activateSession(session, current.workspace.id)
  }
  async function openWorkflowSession(sessionId: string) {
    const workspace = store.getState().workspace
    if (!workspace) return
    const reviewKey = `${workspace.id}:${sessionId}`
    setReviewedWorkflowSessions(current => {
      if (!current.has(reviewKey)) return current
      const next = new Set(current)
      next.delete(reviewKey)
      return next
    })
    const session = await backend.openSession(sessionId, workspace.id)
    const replayed = await activateSession(session, workspace.id)
    const inspected = replayed && store.getState().session?.id === sessionId
      && store.getState().events.some(event => event.streamId === sessionId && event.sequence > 0)
    if (inspected) setReviewedWorkflowSessions(current => new Set(current).add(reviewKey))
    onNavigate('Conversas')
  }

  async function openSidebarSession(request: HistoryOpenRequest) {
    const current = store.getState()
    if ((current.calling && !loadingHistory) || runIsLive(current)) {
      store.setState({ error: t('Conclua ou cancele a execução atual antes de trocar de conversa.') })
      return
    }
    stashDraft()
    if (current.workspace?.id !== request.workspaceId) {
      if (!request.workspacePath) return
      // A chat of another project: open that project first, then the chat.
      try { await openProject(request.workspacePath) } catch (failure) { store.setState({ error: errorMessage(failure) }); return }
      if (store.getState().workspace?.id !== request.workspaceId) return
    }
    const order = ++historyOpenOrder.current
    selection.current?.abort()
    selection.current = undefined
    setLoadingHistory(true)
    try {
      const session = await bounded(backend.openSession(request.sessionId, request.workspaceId))
      if (order !== historyOpenOrder.current || store.getState().workspace?.id !== request.workspaceId) return
      if (!await activateSession(session, request.workspaceId)) {
        if (order === historyOpenOrder.current && store.getState().session?.id !== request.sessionId) {
          store.getState().finishCall()
          store.setState({ error: t('Esta conversa pertence a outro projeto ou não está mais disponível. Atualize o histórico.') })
        }
        return
      }
      onNavigate('Conversas')
    } catch (failure) {
      if (order === historyOpenOrder.current) {
        store.getState().finishCall()
        store.setState({ error: errorMessage(failure) })
      }
    } finally {
      if (order === historyOpenOrder.current) setLoadingHistory(false)
    }
  }

  useEffect(() => {
    if (historyOpenRequest) void openSidebarSession(historyOpenRequest)
  }, [historyOpenRequest?.requestId])
  useEffect(() => {
    if (!projectOpenRequest) return
    void openProject(projectOpenRequest.path).then(() => onNavigate('Conversas'), failure => store.setState({ error: errorMessage(failure) }))
  }, [projectOpenRequest?.requestId])

  const memoryPrompt = memoryQuestion && <ProjectMemoryPrompt backend={backend} workspaceId={memoryQuestion.workspaceId} projectName={memoryQuestion.name}
    onClose={answer => { setMemoryQuestion(undefined); if (answer === 'read') setMemoryNotice({ workspaceId: memoryQuestion.workspaceId, text: t('A IA está lendo {name} para a memória do projeto. Leva cerca de um minuto.', { name: memoryQuestion.name }) }) }} />
  const agentPipelineNotice = agentPipeline && agentPipeline.workspaceId === state.workspace?.id && <div className="agent-pipeline-notice" role="status">
    <Workflow aria-hidden="true" />
    <div><strong>{t('Pipeline criado pelo agente')}</strong><span>{agentPipeline.title}</span></div>
    <button type="button" className="touch-target primary-button" onClick={() => { setCurrentPipeline(agentPipeline); setAgentPipeline(undefined); onNavigate('Pipelines') }}>{t('Abrir')}</button>
    <button type="button" className="touch-target icon-button" aria-label={t('Dispensar aviso do pipeline')} onClick={() => setAgentPipeline(undefined)}><X aria-hidden="true" /></button>
  </div>
  return <>{memoryPrompt}{agentPipelineNotice}<WorkArea selected={selected} workState="empty" project={project} compact={viewMode==='casual' && selected==='Conversas'}
    panels={[conversation, <ArtifactPane messages={state.messages} events={state.events} backend={backend} sessionId={state.session?.id} pipeline={sessionPipeline} onPipelines={() => onNavigate('Pipelines')} />, <MetricsPane events={state.events} />]}
    projectContent={<ProjectsPage backend={backend} currentWorkspaceId={state.workspace?.id} busy={busy} onOpen={openProject} />}
    settingsContent={<SettingsPage backend={backend} backends={state.backends} connectionState={state.connectionState} workspace={state.workspace} onWorkspaceUpdated={workspace => store.setState({ workspace })} onBackendSaved={saved => store.getState().addBackend(saved)} onDefaultSaved={() => {
      setDefaultBackendId('')
      setDefaultModelBackendId('')
      setDefaultModelId('')
      setSettingsReadState('loading')
      void backend.getSettings().then(settings => {
        setDefaultBackendId(settings.defaultBackendId); setDefaultModelBackendId(settings.defaultModelBackendId); setDefaultModelId(settings.defaultModelId); setSettingsReadState('ready')
      }, () => { setDefaultBackendId(''); setDefaultModelBackendId(''); setDefaultModelId(''); setSettingsReadState('error') })
    }} />}
    logsContent={<LogsPage backend={backend} workspaceId={state.workspace?.id} />}
    repositoryContent={<RepositoryPage backend={backend} workspaceId={state.workspace?.id} onProjects={() => onNavigate('Projetos')} />}
    worktreesContent={<WorktreesPage backend={backend} workspaceId={state.workspace?.id} backends={orderedBackends} defaultBackendId={defaultBackendId} worktrees={worktrees} onProjects={() => onNavigate('Projetos')} onSettings={() => onNavigate('Configurações')} onSaveWithAI={startWorktreeSave} />}
    projectExtra={state.workspace ? <WorktreeBar backend={backend} workspaceId={state.workspace.id} worktrees={worktrees} backends={orderedBackends} defaultBackendId={defaultBackendId} showManage={selected !== 'Worktrees'}
      onOpenWorktrees={() => onNavigate('Worktrees')} onSettings={() => onNavigate('Configurações')} onSaveWithAI={startWorktreeSave} /> : undefined}
    projectNotice={worktreeCleanup && worktreeCleanup.workspaceId === state.workspace?.id ? <WorktreeNotice cleanup={worktreeCleanup} showManage={selected !== 'Worktrees'} onOpenWorktrees={() => onNavigate('Worktrees')}
      onCheckCleanup={() => void finishWorktreeCleanup(worktreeCleanup)} onDismissCleanup={() => setWorktreeCleanup(undefined)} />
      : memoryNotice && memoryNotice.workspaceId === state.workspace?.id ? <div className="worktree-cleanup is-saving" role="status" aria-label={t('Leitura do projeto')}><span>{memoryNotice.text}</span>
        <div className="worktree-bar-actions">{selected !== 'Memória' && <button type="button" className="touch-target secondary-button" onClick={() => onNavigate('Memória')}>{t('Abrir Memória')}</button>}
          <button type="button" className="touch-target icon-button" aria-label={t('Fechar aviso')} onClick={() => setMemoryNotice(undefined)}><X aria-hidden="true" /></button></div></div> : undefined}
    pipelineContent={<PipelinesPage backend={backend} backends={orderedBackends} settingsStatus={settingsReadState} preferredBackendId={defaultBackendId} preferredModelBackendId={defaultModelBackendId} preferredModelId={defaultModelId} workspaceId={state.workspace?.id} selectedPipeline={currentPipeline} creationDraft={pipelineCreationDrafts.current.get(backend)?.get(state.workspace?.id ?? '')} onCreationDraftChange={rememberPipelineCreation} initialDiscovery={initialDiscoverySeed?.workspaceId===state.workspace?.id ? initialDiscoverySeed?.text : undefined} initialDiscoveryRequestId={initialDiscoverySeed?.workspaceId===state.workspace?.id ? initialDiscoverySeed?.requestId : undefined} onInitialDiscoveryConsumed={requestId => setInitialDiscoverySeed(current => current?.requestId===requestId ? undefined : current)} viewStage={stageViewRequest && stageViewRequest.pipelineId===currentPipeline?.id ? stageViewRequest.stage : undefined} viewRequestId={stageViewRequest?.requestId} newWorkRequest={newWorkRequest + sddLaunchRequest} onSelectPipeline={setCurrentPipeline} onProjects={() => onNavigate('Projetos')} onSettings={() => onNavigate('Configurações')} onMCPServers={() => onNavigate('MCP Servers')} onShownStage={onShownStage} onStartRole={startPipelineRole} onOpenSession={(sessionId, draft) => openDelegatedSession(sessionId, draft, true)} onFollowSession={sessionId => void followPipelineSession(sessionId)} permissionProfile={state.workspace?.profile} permissionControl={state.workspace ? <PermissionPicker backend={backend} workspace={state.workspace} onWorkspaceUpdated={workspace => store.setState({ workspace })} /> : undefined} />}
    agentsContent={<AgentsPage backend={backend} backends={orderedBackends} workspaceId={state.workspace?.id} parentSessionId={state.session?.id} onStart={startAgent} onDelegate={delegateAgent} onOpenSession={openDelegatedSession} onProjects={() => onNavigate('Projetos')} onSettings={() => onNavigate('Configurações')} />}
    workflowsContent={<WorkflowsPage backend={backend} backends={orderedBackends} workspaceId={state.workspace?.id} reviewedSessions={reviewedWorkflowSessions} onProjects={() => onNavigate('Projetos')} onSettings={() => onNavigate('Configurações')} onOpenSession={openWorkflowSession} />}
    schedulesContent={<SchedulesPage backend={backend} backends={orderedBackends} workspaceId={state.workspace?.id} onProjects={() => onNavigate('Projetos')} onSettings={() => onNavigate('Configurações')} onWorkflows={() => onNavigate('Workflows')} onOpenSession={openWorkflowSession} />}
    skillsContent={<SkillsPage backend={backend} workspaceId={state.workspace?.id} onProjects={() => onNavigate('Projetos')} />}
    mcpContent={<MCPPage backend={backend} workspaceId={state.workspace?.id} onProjects={() => onNavigate('Projetos')} />}
    diagnosticsContent={<DiagnosticsPage backend={backend} workspaceId={state.workspace?.id} onSettings={() => onNavigate('Configurações')} onMCP={() => onNavigate('MCP Servers')} onProjects={() => onNavigate('Projetos')} />}
    executionContent={<ExecutionPage backend={backend} workspaceId={state.workspace?.id} onProjects={() => onNavigate('Projetos')} onOpenSession={openWorkflowSession} />}
    memoryContent={<ProjectMemoryPage backend={backend} workspaceId={state.workspace?.id} projectName={state.workspace ? basename(state.workspace.path) : undefined} onProjects={() => onNavigate('Projetos')} />}
    knowledgeContent={<KnowledgePage backend={backend} workspaceId={state.workspace?.id} onProjects={() => onNavigate('Projetos')} />}
    vaultContent={<VaultPage backend={backend} workspaceId={state.workspace?.id} onSettings={() => onNavigate('Configurações')} onMCP={() => onNavigate('MCP Servers')} onProjects={() => onNavigate('Projetos')} />}
    channelsContent={<ChannelsPage backend={backend} workspaceId={state.workspace?.id} onProjects={() => onNavigate('Projetos')} />}
    footer={[connection, state.session ? t('Sessão {id}', { id: state.session.id.slice(0, 8) }) : t('Nenhuma sessão ativa')]} /></>
}

type SetupProps = { backend: Backend; workRevision?: string; onOpenPipeline?: (pipelineId: string, workspaceId: string) => void; onSeeAllWork?: () => void; state: SessionState; store: ReturnType<typeof createSessionStore>; activateSession: (session: Session, workspaceId: string) => Promise<boolean>; recents: WorkspaceSummary[]; preferredBackendId: string; onBackendUsed: (id: string) => void; settingsReadState: SettingsReadState; viewMode: AppMode; initialPrompt: string; onPrompt: (text: string) => Promise<void>; onStartSDD: (discovery?: string) => void; onAdmissionStateChange?: (pending: boolean) => void ; onSwitchProject: (path: string, draft: string) => void }

function WorkspaceSetup({ backend, workRevision, onOpenPipeline, onSeeAllWork, state, store, activateSession, recents, preferredBackendId, onBackendUsed, settingsReadState, viewMode, initialPrompt, onPrompt, onStartSDD, onAdmissionStateChange, onSwitchProject }: SetupProps) {
  const t = useT()
  const [path, setPath] = useState('')
  const [choice, setChoice] = useState('')
  const [directReason, setDirectReason] = useState('')
  const [firstPrompt, setFirstPrompt] = useState(initialPrompt)
  useEffect(() => { setFirstPrompt(initialPrompt) }, [initialPrompt])
  const [modelCatalog, setModelCatalog] = useState<ModelCatalogResult>()
  const [casualDefaults, setCasualDefaults] = useState<Awaited<ReturnType<Backend['getSettings']>>>()
  const [casualDefaultsLoaded, setCasualDefaultsLoaded] = useState(false)
  const [casualProfiles, setCasualProfiles] = useState<Awaited<ReturnType<Backend['listProviderProfiles']>>>([])
  const [modelId, setModelId] = useState('')
  const [reasoningEffort, setReasoningEffort] = useState('')
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [catalogError, setCatalogError] = useState('')
  const [pending, setPending] = useState(false)
  useEffect(() => { onAdmissionStateChange?.(pending); return () => { onAdmissionStateChange?.(false) } }, [pending, onAdmissionStateChange])
  const [error, setError] = useState<string>()
  const [providerOpen, setProviderOpen] = useState(false)
  const [sessions, setSessions] = useState<Session[]>([])
  const [listState, setListState] = useState<'loading' | 'ready' | 'error'>('loading')
  const providerTrigger = useRef<HTMLButtonElement>(null)
  const mounted = useRef(false)
  const generation = useRef(0)
  const catalogRequest = useRef<AbortController>()
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; generation.current++; catalogRequest.current?.abort() } }, [])
  const available = state.backends.filter(item => item.available)
      const remembered = rememberedBackend()
      const selectedBackend = choice || (settingsReadState === 'ready' ? available.find(item => item.id === preferredBackendId)?.id || available.find(item => item.id === remembered)?.id || available.find(item => item.kind === 'api')?.id || available[0]?.id || '' : '')
  const workspaceId = state.workspace?.id
  const selectedOption = state.backends.find(item => item.id === selectedBackend)
  const selectedModel = modelCatalog?.models.find(item => item.id === modelId && item.backendId === selectedBackend && item.source === modelCatalog.source && item.availability !== 'unavailable')
  const catalogReady = selectedOption?.kind === 'cli' && selectedBackend !== 'opencode' && modelCatalog?.backendId === selectedBackend
    && modelCatalog.status === 'complete' && modelCatalog.complete && !!modelCatalog.profileRevision && !!selectedModel
  useEffect(() => {
    catalogRequest.current?.abort()
    catalogRequest.current = undefined
    setModelCatalog(undefined)
    setModelId('')
    setReasoningEffort('')
    setCatalogError('')
    setCatalogLoading(false)
  }, [workspaceId, selectedBackend])
  useEffect(() => {
    if (!workspaceId || viewMode !== 'casual' || settingsReadState !== 'ready') return
    let live = true
    setCasualDefaults(undefined); setCasualDefaultsLoaded(false); setCasualProfiles([])
    void backend.getSettings().then(value => { if (live && store.getState().workspace?.id === workspaceId) { setCasualDefaults(value); setCasualDefaultsLoaded(true) } }).catch(() => { if (live) setCasualDefaultsLoaded(true) })
    void backend.listProviderProfiles().then(value => { if (live && store.getState().workspace?.id === workspaceId) setCasualProfiles(value) }).catch(() => undefined)
    return () => { live = false }
  }, [backend, workspaceId, viewMode, settingsReadState])
  const defaultCLIModel = casualDefaults && (casualDefaults.defaultModelBackendId || casualDefaults.defaultBackendId) === selectedBackend ? casualDefaults.defaultModelId : ''
  // Casual reads the models of whichever CLI is chosen, so the bar always offers them; the default model is preferred.
  useEffect(() => {
    if (viewMode === 'casual' && workspaceId && selectedOption?.kind === 'cli' && casualDefaultsLoaded) void queryModels(defaultCLIModel || undefined)
  }, [workspaceId, viewMode, selectedBackend, defaultCLIModel, casualDefaultsLoaded])
  useEffect(() => { if (workspaceId && viewMode === 'professional') void loadSessions(workspaceId) }, [workspaceId, backend, viewMode])

  async function loadSessions(id: string) {
    setListState('loading')
    try {
      const result = await bounded(backend.listSessions(id))
      if (mounted.current && store.getState().workspace?.id === id) { setSessions(result.filter(session => !session.purpose || session.purpose === 'chat')); setListState('ready') }
    } catch { if (mounted.current) setListState('error') }
  }

  async function attempt(action: (current: () => boolean) => Promise<void>) {
    const token = ++generation.current
    const current = () => mounted.current && generation.current === token
    setPending(true)
    setError(undefined)
    try { await action(current) } catch (failure) { if (current()) setError(errorMessage(failure)) } finally { if (current()) setPending(false) }
  }
  function openWorkspace(event: FormEvent) {
    event.preventDefault()
    if (path.trim()) attempt(async current => {
      const workspace = await backend.openWorkspace(path.trim())
      if (current()) store.getState().setWorkspace(workspace)
    })
  }
  function pick() {
    attempt(async current => {
      const chosen = await backend.pickDirectory()
      if (chosen && current()) {
        const workspace = await backend.openWorkspace(chosen)
        if (current()) store.getState().setWorkspace(workspace)
      }
    })
  }
  function openRecent(path: string) {
    attempt(async current => {
      const workspace = await backend.openWorkspace(path)
      if (current()) store.getState().setWorkspace(workspace)
    })
  }
  function startSession(event: FormEvent) {
    event.preventDefault()
    const workspace = state.workspace
    const casual = viewMode === 'casual'
    const reason = casual ? 'Conversa livre no modo Casual' : directReason.trim()
    const initialPrompt = firstPrompt.trim()
    if (workspace && selectedBackend && reason && (!casual || initialPrompt) && (selectedOption?.kind !== 'cli' || catalogReady)) attempt(async current => {
      const selected = selectedOption?.kind === 'cli' ? { modelId, reasoningEffort, catalogRevision: modelCatalog!.profileRevision } : {}
      const session = await backend.createDirectSession({ workspaceId: workspace.id, backendId: selectedBackend, reason, ...selected })
          onBackendUsed(selectedBackend)
      if (!current() || store.getState().workspace?.id !== workspace.id || store.getState().session) return
      const activated = await activateSession(session, workspace.id)
      if (activated) {
        if (casual) {
          store.getState().setDraft(initialPrompt)
          setFirstPrompt('')
          await onPrompt(initialPrompt)
        } else if (initialPrompt) {
          store.getState().setDraft(initialPrompt)
        }
      }
    })
  }
  async function queryModels(preferredModel = '') {
    const workspace = state.workspace
    if (!workspace || selectedOption?.kind !== 'cli' || catalogLoading) return
    catalogRequest.current?.abort()
    const controller = new AbortController()
    catalogRequest.current = controller
    setCatalogLoading(true)
    setCatalogError('')
    setModelCatalog(undefined)
    setModelId('')
    setReasoningEffort('')
    try {
      const result = await backend.queryCLIModelCatalog({ workspaceId: workspace.id, backendId: selectedBackend }, controller.signal)
      if (controller.signal.aborted || catalogRequest.current !== controller || store.getState().workspace?.id !== workspace.id) return
      setModelCatalog(result)
      if (!result.complete || result.status !== 'complete') {
        setCatalogError(result.status === 'empty' ? t('Nenhum modelo disponível neste CLI.') : result.status === 'partial' ? t('A lista veio parcial; nenhuma seleção foi liberada.') : cliCatalogProblem(result, selectedOption?.name ?? t('este CLI')))
      } else if (preferredModel && result.models.some(item => item.id === preferredModel && item.backendId === selectedBackend && item.source === result.source && item.availability !== 'unavailable')) {
        setModelId(preferredModel)
      }
    } catch (failure) {
      if (!controller.signal.aborted && catalogRequest.current === controller) setCatalogError(errorMessage(failure))
    } finally {
      if (catalogRequest.current === controller) setCatalogLoading(false)
    }
  }
  const recentWork = useRecentWork(backend, viewMode === 'professional' ? state.workspace?.id : undefined, workRevision)
  const isUserSession = (session: Session) => !session.purpose || session.purpose === 'chat'
  const internalSessions = sessions.filter(session => !isUserSession(session))
  const userSessions = sessions.filter(isUserSession)
  function sessionRow(session: Session) {
    const title = session.title || (isUserSession(session) ? t('Sessão sem mensagens') : t('Sessão do SDD'))
    return <li key={session.id}>
      <div className="session-summary"><strong className="session-title" title={session.title || undefined}>{title}</strong>
        <div className="session-meta"><span>{state.backends.find(item => item.id === session.backendId)?.name ?? session.backendId}</span>
          <span>{session.resumable ? t('Retomável') : sessionReadOnly(session, state.backends) ? t('Somente leitura') : t('Nova execução isolada')}</span>
          <span className="muted">{sessionStatus(session.status)} · <time dateTime={session.updatedAt}>{new Date(session.updatedAt).toLocaleString(localeTag())}</time></span></div></div>
      <button type="button" className="touch-target secondary-button" disabled={pending} onClick={() => openHistory(session.id)}>{t('Abrir histórico')}</button>
    </li>
  }
  function openHistory(id: string) {
    const workspace = state.workspace
    if (!workspace || pending) return
    void attempt(async current => {
      const session = await bounded(backend.openSession(id, workspace.id))
      if (!current() || store.getState().workspace?.id !== workspace.id || store.getState().session) return
      await activateSession(session, workspace.id)
    })
  }
  function closeProvider() { setProviderOpen(false); providerTrigger.current?.focus() }

  if (state.connectionState === 'connecting') return <div className="empty-state" role="status"><p>{t('Conectando ao backend local')}</p></div>
  if (state.connectionState === 'unavailable') return <div className="empty-state"><p>{t('Backend local indisponível')}</p><span className="muted">{t('Abra o Harflex pelo aplicativo desktop para usar projetos e sessões.')}</span></div>
  // A new Casual chat is drawn in the conversation's own frame, so starting it does not change the screen.
  if (state.workspace && viewMode === 'casual') return <CasualStartPanel firstPrompt={firstPrompt} onFirstPrompt={setFirstPrompt} onSubmit={startSession} backends={state.backends} selectedBackend={selectedBackend} onBackend={setChoice} apiModelName={casualProfiles.find(profile => profile.id === selectedBackend)?.model ?? ''} catalog={modelCatalog} modelId={modelId} onModel={value => { setModelId(value); setReasoningEffort('') }} effort={reasoningEffort} onEffort={setReasoningEffort} catalogLoading={catalogLoading} catalogError={catalogError} onQueryModels={() => void queryModels()} settingsStatus={settingsReadState} pending={pending} canStart={!!selectedOption?.available && !!firstPrompt.trim() && (selectedOption.kind !== 'cli' || !!catalogReady)} onConfigure={() => setProviderOpen(true)} providerTrigger={providerTrigger} onStartSDD={() => onStartSDD(firstPrompt)} projectName={basename(state.workspace.path)} error={error ?? state.error}
    projectControl={<ChatProjectPicker backend={backend} workspace={state.workspace} disabled={pending} onPick={path => onSwitchProject(path, firstPrompt)} />}
    permissionControl={<PermissionPicker backend={backend} workspace={state.workspace} onWorkspaceUpdated={workspace => store.setState({ workspace })} />} />
  return <div className="setup">
    {!state.workspace ? <form className="setup-step" onSubmit={openWorkspace} aria-labelledby="setup-workspace">
      <h2 id="setup-workspace">{t('Abrir projeto')}</h2>
      <p className="muted">{t(capabilityNote)}</p>
      <button type="button" className="touch-target secondary-button" onClick={pick} disabled={pending}><FolderOpen aria-hidden="true" />{t('Escolher pasta')}</button>
      <label className="field">{t('Caminho da pasta')}<input className="mono" value={path} onChange={event => setPath(event.target.value)} autoComplete="off" spellCheck={false} /></label>
      <button type="submit" className="touch-target primary-button" disabled={pending || !path.trim()}>{t('Abrir projeto')}</button>
    </form> : <div className={`setup-step setup-step-${viewMode}`} aria-labelledby="setup-backend">
      {viewMode === 'professional' ? <>
        <h2 id="setup-backend">{t('Começar pelo SDD')}</h2>
        <p className="muted">{t('Discovery, SPEC, plano, código e avaliação formam o caminho padrão. Você pode pular fases com registro do motivo.')}</p>
        <button type="button" className="touch-target primary-button" onClick={() => onStartSDD(firstPrompt || undefined)}>{t('Iniciar trabalho SDD')}</button>
      </> : <>
        <h2 id="setup-backend">{t('Nova conversa')}</h2>
        <p className="muted">{t('Converse diretamente com um agente neste projeto. O histórico fica na lista à esquerda.')}</p>
      </>}
      <form className="setup-direct" onSubmit={startSession}><h3>{t('Conversa livre sem pipeline')}</h3><p className="project-warning">{t('Esta escolha pula o SDD para a sessão; o motivo fica registrado no journal local.')}</p>
      <p className="muted">{t(capabilityNote)}</p>
      {selectedBackend === 'opencode' && <p className="project-warning">{t('O OpenCode recebe o prompt por argumento de processo; ele pode ficar visível temporariamente a ferramentas locais de inspeção de processos.')}</p>}
      {settingsReadState === 'loading' && <p className="muted" role="status">{t('Lendo o provedor padrão das Configurações. Você também pode escolher manualmente.')}</p>}
      {settingsReadState === 'error' && <p className="project-warning" role="alert">{t('Não foi possível ler o padrão salvo. Escolha um executor manualmente; a seleção vale apenas para esta sessão.')}</p>}
      {viewMode === 'professional' && initialPrompt && <label className="field">{t('Pedido anterior · rascunho')}<textarea value={firstPrompt} onChange={event => setFirstPrompt(event.target.value)} rows={4} maxLength={20000} placeholder={t('Revise o pedido antes de iniciar a sessão')} /></label>}
      {state.backends.length === 0 ? <p className="muted">{t('Nenhum backend configurado. Configure um provedor para começar.')}</p>
        : <fieldset className="backend-list"><legend className="visually-hidden">{t('Backend da sessão')}</legend>
          {state.backends.map(item => <label key={item.id} className={`backend-option touch-target${item.available ? '' : ' is-unavailable'}`}>
            <input type="radio" name="backend" value={item.id} checked={selectedBackend === item.id} disabled={!item.available} onChange={() => setChoice(item.id)} />
            <span>{item.name}</span><span className="muted mono backend-kind">{item.kind === 'api' ? 'API' : 'CLI'}{item.available ? '' : t(' · indisponível')}</span>{item.id === 'codex' && <span className="muted backend-note">{t('Uma mensagem por sessão; para continuar, inicie outra sessão.')}</span>}
          </label>)}
        </fieldset>}
      {selectedOption?.kind === 'cli' && <section className="session-model-catalog" aria-label={t('Modelo da sessão CLI')}>
        <div className="session-model-catalog-heading"><strong>{t('Modelo e esforço')}</strong><span className="muted">{t('Consulta local explícita no CLI instalado; a escolha fica vinculada a esta sessão.')}</span></div>
        <button type="button" className="touch-target secondary-button" disabled={pending || catalogLoading} onClick={() => void queryModels()}>{catalogLoading ? t('Consultando…') : t('Consultar modelos')}</button>
        {selectedBackend === 'opencode' && <p className="project-warning">{t('A execução OpenCode aguarda prova de isolamento dos plugins nesta instalação. A lista pode ser consultada, mas não inicia uma sessão.')}</p>}
        {catalogError && <p className="form-error" role="alert">{catalogError}</p>}
        {modelCatalog?.complete && modelCatalog.status === 'complete' && <>
          <p className="muted session-model-source">{t('{count} modelos · {destination} · consulta de {date}', { count: modelCatalog.models.length, destination: modelCatalog.destination || selectedBackend, date: new Date(modelCatalog.checkedAt).toLocaleString(localeTag()) })}</p>
          <IonPicker id="session-model" label={t('Modelo da sessão')} value={modelId} onChange={value => { setModelId(value); setReasoningEffort('') }} searchable required
            options={[{ value: '', label: t('Escolha um modelo') }, ...modelCatalog.models.map(item => ({ value: item.id, label: item.displayName || item.id }))]} />
          {selectedModel && <><IonPicker id="session-effort" label={t('Esforço do modelo')} value={reasoningEffort} onChange={setReasoningEffort}
            options={[{ value: '', label: t('Automático') }, ...(selectedModel.supportedReasoningEfforts ?? []).map(value => ({ value, label: value }))]} />
            {!selectedModel.supportedReasoningEfforts?.length && <p className="muted session-model-source">{t('Este catálogo não informa níveis de esforço; Automático não envia essa opção.')}</p>}</>}
        </>}
      </section>}
          {viewMode === 'professional' && <>
            <label className="field">{t('Motivo para pular SDD nesta sessão')}<input value={directReason} onChange={event => setDirectReason(event.target.value)} maxLength={1000} required placeholder={t('Ex.: pesquisa rápida sem edição')} /></label>
            <div className="reason-chips" role="group" aria-label={t('Motivos comuns')}>{skipReasons.map(reason => <button key={reason} type="button" className="chip-button" aria-pressed={directReason === reason} onClick={() => setDirectReason(reason)}>{t(reason)}</button>)}</div>
          </>}
      <div className="setup-actions">
        <button ref={providerTrigger} type="button" className="touch-target secondary-button" onClick={() => setProviderOpen(true)}><Settings2 aria-hidden="true" />{t('Configurar provedor')}</button>
        <button type="submit" className="touch-target secondary-button" disabled={pending || !selectedBackend || !directReason.trim() || selectedOption?.kind === 'cli' && !catalogReady}>{t('Iniciar sessão livre')}</button>
      </div>
      </form>
    </div>}
    {!state.workspace && recents.length > 0 && <section className="recent-projects" aria-labelledby="recent-projects-title">
      <h3 id="recent-projects-title">{t('Continuar em um projeto recente')}</h3>
      <ul>{recents.map(item => <li key={item.id}><button type="button" className="touch-target secondary-button recent-project" disabled={pending} aria-label={t('Abrir {name}', { name: basename(item.path) })} onClick={() => openRecent(item.path)}>
        <FolderOpen aria-hidden="true" /><span><strong>{basename(item.path)}</strong><span className="muted mono" title={item.path}>{shortPath(item.path)}</span></span></button></li>)}</ul>
    </section>}
    {state.workspace && viewMode === 'professional' && <section className="recent-sessions" aria-labelledby="recent-sessions-title">
      <div className="recent-heading"><h2 id="recent-sessions-title">{t('Sessões recentes')}</h2><button type="button" className="touch-target secondary-button" disabled={listState === 'loading' || pending} onClick={() => void loadSessions(state.workspace!.id)}>{t('Atualizar sessões')}</button></div>
      {recentWork.runs.length > 0 && <div className="recent-work">
        <h3 id="recent-work-title">{t('Trabalhos')}</h3>
        <ul className="session-list" aria-labelledby="recent-work-title">{recentWork.runs.slice(0, 5).map(run => <li key={run.id}>
          <div className="session-summary"><strong className="session-title" title={run.title}>{run.title || t('Trabalho sem título')}</strong>
            <div className="session-meta"><span>{workProgress(run)}</span><span className="muted"><time dateTime={run.updatedAt}>{new Date(run.updatedAt).toLocaleString(localeTag())}</time></span></div></div>
          <button type="button" className="touch-target secondary-button" disabled={pending} onClick={() => onOpenPipeline?.(run.id, run.workspaceId)}>{t('Abrir trabalho')}</button>
        </li>)}</ul>
        {recentWork.runs.length > 5 && <button type="button" className="touch-target text-button" onClick={() => onSeeAllWork?.()}>{t('Ver todos os {count} trabalhos', { count: recentWork.runs.length })}</button>}
        <h3>{t('Conversas')}</h3>
      </div>}
      {listState === 'loading' && <p className="muted">{t('Carregando sessões…')}</p>}
      {listState === 'error' && <p className="form-error" role="alert">{t('Não foi possível listar as sessões. Use Atualizar sessões para tentar novamente.')}</p>}
      {listState === 'ready' && (sessions.length === 0 ? <p className="muted">{t('Nenhuma sessão salva neste projeto.')}</p> : <>
        <ul className="session-list" aria-label={t('Sessões recentes')}>{userSessions.map(session => sessionRow(session))}</ul>
        {internalSessions.length > 0 && <details className="session-internal"><summary>{t('Sessões internas do SDD ({count})', { count: internalSessions.length })}</summary>
          <p className="muted">{t('Criadas pelo pipeline para gerar perguntas, SPEC e Plan. Servem só para consulta.')}</p>
          <ul className="session-list" aria-label={t('Sessões internas do SDD')}>{internalSessions.map(session => sessionRow(session))}</ul></details>}
      </>)}
    </section>}
    {(error ?? state.error) && <p className="form-error" role="alert">{error ?? state.error}</p>}
    {providerOpen && <ProviderDialog backend={backend} onClose={closeProvider} onSaved={saved => { store.getState().addBackend(saved); setChoice(saved.id); if (viewMode === 'casual') void backend.listProviderProfiles().then(value => { if (mounted.current && store.getState().workspace?.id === workspaceId) setCasualProfiles(value) }).catch(() => undefined) }} />}
  </div>
}

function sessionStatus(status: string) {
  const label = ({ ready: 'Pronta', running: 'Em execução', paused: 'Interrompida', completed: 'Concluída', failed: 'Falhou', cancelled: 'Cancelada', awaiting_approval: 'Aguardando aprovação' } as Record<string, string>)[status]
  return t(label ?? 'Histórico salvo')
}
