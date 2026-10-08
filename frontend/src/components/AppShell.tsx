import { useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from 'react'
import { Activity, Menu, Moon, PanelRightClose, PanelRightOpen, SquareTerminal, Sun } from 'lucide-react'
import { TerminalPanel } from '../features/terminal/TerminalPanel'
import { Sidebar, type Destination } from './Sidebar'
import { CasualSidebar } from './CasualSidebar'
import { PipelineBar } from './PipelineBar'
import { useStageActivity } from '../features/pipelines/useStageActivity'
import { stageNames } from '../lib/pipelineStages'
import { ActivityPanel } from './ActivityPanel'
import { maxSidePanelWidth, minSidePanelWidth, useSidePanelWidth } from './sidePanelWidth'
import { WorkArea } from './WorkArea'
import { Workbench, type HistoryOpenRequest, type ProjectOpenRequest, type WorkbenchContext } from '../features/workbench/Workbench'
import type { AgentEvent, Backend, Pipeline, PipelineStage } from '../lib/backend'
import { readAppMode, writeAppMode, type AppMode } from '../state/appMode'
import { useTheme } from '../state/theme'

export type AppShellProps = { initialActivityOpen?: boolean; workState?: 'empty' | 'loading'; backend?: Backend; /** Reopen the most recently used project at startup (default). */ restoreProject?: boolean }
const desktopQuery = '(min-width: 1024px)'

export function AppShell({ initialActivityOpen, workState = 'empty', backend, restoreProject = true }: AppShellProps) {
  const [desktop, setDesktop] = useState(() => window.matchMedia?.(desktopQuery).matches ?? true)
  const [viewMode, setViewMode] = useState<AppMode>(() => readAppMode())
  const [activityOpen, setActivityOpen] = useState(initialActivityOpen ?? (desktop && viewMode === 'professional'))
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [selected, setSelected] = useState<Destination>('Conversas')
  const professionalDestination = useRef<Destination>('Conversas')
  const [newWorkRequest, setNewWorkRequest] = useState(0)
  const [activityEvents, setActivityEvents] = useState<AgentEvent[]>([])
  const [currentPipeline, setCurrentPipeline] = useState<Pipeline>()
  const [stageViewRequest,setStageViewRequest] = useState<{requestId:number;pipelineId:string;workspaceId:string;stage?:PipelineStage}>()
  // What the Pipelines page is showing, which can move on its own (Code approved opens QA); the bar marks it.
  const [shownStage, setShownStage] = useState<PipelineStage>()
  const stageViewSequence = useRef(0)
  const stageActivity = useStageActivity(backend, currentPipeline)
  const preparingDocuments = selected === 'Pipelines' && currentPipeline?.preparationExperience === 'conversational' && ['discovery', 'spec', 'plan'].includes(currentPipeline.currentStage)
  const [newChatPrompt, setNewChatPrompt] = useState('')
  const [workbenchContext, setWorkbenchContext] = useState<WorkbenchContext>({ busy: false, loadingHistory: false, hasDraft: false, historyHasUserMessage: false, readOnly: false, recoveryPrompt: '' })
  const [historyRequest, setHistoryRequest] = useState<HistoryOpenRequest>()
  const [projectRequest, setProjectRequest] = useState<ProjectOpenRequest>()
  // The side panel shows the activity or the project's terminal; the shell opens the first time the terminal is shown.
  const [sidePanel, setSidePanel] = useState<'activity' | 'terminal'>('activity')
  const [terminalUsed, setTerminalUsed] = useState(false)
  const panelWidth = useSidePanelWidth()
  const [theme, toggleTheme] = useTheme()

  const historyRequestSequence = useRef(0)
  const activityTrigger = useRef<HTMLButtonElement>(null)
  const sidebarTrigger = useRef<HTMLButtonElement>(null)
  const activityContainer = useRef<HTMLDivElement>(null)
  const sidebarContainer = useRef<HTMLDivElement>(null)
  const background = useRef<HTMLDivElement>(null)
  const shellHeader = useRef<HTMLElement>(null)
  const restoreFocus = useRef<HTMLButtonElement | null>(null)
  const overlayOpen = sidebarOpen || (!desktop && activityOpen)

  useLayoutEffect(() => {
    const header = shellHeader.current, workspace = background.current
    if (!header || !workspace) return
    const measure = () => workspace.style.setProperty('--shell-header-size', `${header.getBoundingClientRect().height}px`)
    measure()
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(measure)
    observer?.observe(header)
    window.addEventListener('resize', measure)
    return () => { observer?.disconnect(); window.removeEventListener('resize', measure); workspace.style.removeProperty('--shell-header-size') }
  }, [viewMode, currentPipeline?.id])

  const previousMode = useRef(viewMode)
  useLayoutEffect(() => {
    if (previousMode.current !== viewMode) background.current?.querySelector<HTMLElement>('#work-area')?.scrollIntoView?.({ block: 'start', behavior: 'auto' })
    previousMode.current = viewMode
  }, [viewMode])

  function closeActivity() { restoreFocus.current = activityTrigger.current; setActivityOpen(false) }
  function closeSidebar() { restoreFocus.current = sidebarTrigger.current; setSidebarOpen(false) }
  const changeMode = useCallback((next: AppMode) => {
    if (next === viewMode || workbenchContext.startingSession || workbenchContext.loadingHistory) return
    if (sidebarOpen) restoreFocus.current = sidebarTrigger.current
    setViewMode(next)
    setSidebarOpen(false)
    if (next === 'casual') {
      professionalDestination.current = selected
      setSelected('Conversas')
      setActivityOpen(false)
    } else {
      setSelected(professionalDestination.current)
      if (desktop) setActivityOpen(preparingDocuments ? false : initialActivityOpen ?? true)
    }
  }, [viewMode, selected, desktop, initialActivityOpen, sidebarOpen, preparingDocuments, workbenchContext.startingSession, workbenchContext.loadingHistory])
  const reportWorkbenchContext = useCallback((next: WorkbenchContext) => {
    setWorkbenchContext(current => current.workspace?.id === next.workspace?.id
      && current.workspace?.path === next.workspace?.path
      && current.session?.id === next.session?.id
      && current.session?.status === next.session?.status
      && current.session?.resumable === next.session?.resumable
      && current.session?.updatedAt === next.session?.updatedAt
      && current.busy === next.busy
      && current.startingSession === next.startingSession
      && current.loadingHistory === next.loadingHistory
      && current.hasDraft === next.hasDraft
      && current.historyHasUserMessage === next.historyHasUserMessage
      && current.readOnly === next.readOnly
      && current.recoveryPrompt === next.recoveryPrompt ? current : next)
  }, [])

  useEffect(() => { writeAppMode(viewMode) }, [viewMode])
  useEffect(() => { if (preparingDocuments) setActivityOpen(false) }, [preparingDocuments])

  // A work item opens on the Pipelines screen at its own stage.
  function openPipeline(pipelineId: string, workspaceId: string) {
    if (workbenchContext.startingSession) return
    setStageViewRequest({ requestId: ++stageViewSequence.current, pipelineId, workspaceId })
    setSelected('Pipelines')
  }
  function openHistorySession(sessionId: string, workspaceId: string, workspacePath?: string) {
    setHistoryRequest({ requestId: String(++historyRequestSequence.current), sessionId, workspaceId, workspacePath })
    setSelected('Conversas')
    if (sidebarOpen) closeSidebar()
  }

  function openProjectFromSidebar(path: string) {
    setProjectRequest({ requestId: String(++historyRequestSequence.current), path })
    setSelected('Conversas')
    if (sidebarOpen) closeSidebar()
  }

  function startNewChat(recoveryPrompt?: string) {
    if (workbenchContext.startingSession) return
    if (backend) {
      setNewChatPrompt(recoveryPrompt ?? '')
      setNewWorkRequest(current => current + 1)
    }
    else setSelected('Conversas')
    if (sidebarOpen) closeSidebar()
  }

  function selectDestination(name: Destination) {
    if (workbenchContext.startingSession) return
    setSelected(name)
    if (sidebarOpen) closeSidebar()
  }

  // A new destination starts at its top, whichever element scrolled the previous one.
  useEffect(() => {
    document.getElementById('work-area')?.scrollTo?.({ top: 0 })
    if (document.scrollingElement) document.scrollingElement.scrollTop = 0
  }, [selected])

  useEffect(() => {
    const query = window.matchMedia?.(desktopQuery)
    if (!query) return
    const update = () => {
      if (!query.matches && activityContainer.current?.contains(document.activeElement)) {
        restoreFocus.current = activityTrigger.current
      }
      setDesktop(query.matches)
      setSidebarOpen(false)
      if (!query.matches) setActivityOpen(false)
    }
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [viewMode])

  useEffect(() => {
    const query = window.matchMedia?.('(max-width: 767px)')
    if (!query) return
    const update = () => {
      if (!query.matches && sidebarOpen) {
        restoreFocus.current = sidebarContainer.current?.querySelector<HTMLButtonElement>('[aria-current="page"]') ?? activityTrigger.current
        setSidebarOpen(false)
      }
    }
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [sidebarOpen])

  useEffect(() => {
    const panel = sidebarOpen ? sidebarContainer.current : activityContainer.current
    if (background.current) background.current.inert = overlayOpen
    if (sidebarContainer.current) sidebarContainer.current.inert = overlayOpen && !sidebarOpen
    if (!overlayOpen && restoreFocus.current) { restoreFocus.current.focus(); restoreFocus.current = null }
    if (overlayOpen) panel?.querySelector<HTMLElement>('button, input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])')?.focus()
    const keydown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        if (sidebarOpen) closeSidebar()
        else if (activityOpen) closeActivity()
      }
      if (event.key !== 'Tab' || !overlayOpen || !panel) return
      const controls = Array.from(panel.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])')).filter(element => element.getClientRects().length > 0)
      const first = controls[0], last = controls[controls.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', keydown)
    return () => { document.removeEventListener('keydown', keydown); if (background.current) background.current.inert = false }
  }, [activityOpen, sidebarOpen, overlayOpen])

  return <div className={`app-shell mode-${viewMode}${activityOpen && desktop ? ' has-activity' : ''}${panelWidth.dragging ? ' is-resizing-panel' : ''}`} style={{ '--side-panel-width': `${panelWidth.width}px` } as CSSProperties}>
    <a className="skip-link touch-target" href="#work-area">Ir para o trabalho</a>
    {overlayOpen && <div className="drawer-scrim" aria-hidden="true" />}
    <div ref={sidebarContainer} id="sidebar" className={`sidebar${sidebarOpen ? ' is-open' : ''}`} role={sidebarOpen ? 'dialog' : undefined} aria-modal={sidebarOpen || undefined} aria-label={sidebarOpen ? viewMode === 'casual' ? 'Histórico de conversas' : 'Navegação' : undefined}>
      {viewMode === 'professional'
        ? <Sidebar selected={selected} onClose={closeSidebar} onSelect={selectDestination} onNewWork={startNewChat} onToggleMode={() => changeMode('casual')} />
        : <CasualSidebar backend={backend} workspace={workbenchContext.workspace} activeSessionId={workbenchContext.session?.id} activeSessionUpdatedAt={workbenchContext.session?.updatedAt} historyHasUserMessage={workbenchContext.historyHasUserMessage} recoveryPrompt={workbenchContext.recoveryPrompt} selected={selected} busy={workbenchContext.busy} loadingHistory={workbenchContext.loadingHistory} hasDraft={workbenchContext.hasDraft} mobileOpen={sidebarOpen} onOpenSession={openHistorySession} onOpenPipeline={openPipeline} pipelineRevision={currentPipeline ? `${currentPipeline.id}:${currentPipeline.revision}` : ''} onOpenProject={openProjectFromSidebar} onSelectDestination={selectDestination} onNewChat={startNewChat} onProjects={() => selectDestination('Projetos')} onToggleMode={() => changeMode('professional')} onClose={closeSidebar} />}
    </div>
    <div className="workspace" ref={background}>
      <header ref={shellHeader} className="shell-header">
        <div className="compact-toolbar">
          <button ref={sidebarTrigger} className="touch-target icon-button sidebar-toggle" aria-label={viewMode === 'casual' ? 'Abrir histórico de chats' : 'Abrir navegação'} aria-expanded={sidebarOpen} aria-controls="sidebar" onClick={() => { setActivityOpen(false); setSidebarOpen(true) }}><Menu aria-hidden="true" /></button>
          <span className="toolbar-title">{selected}</span>
          <div className="top-mode-switch" role="group" aria-label="Modo de uso">
            <button type="button" className="touch-target top-mode-option" aria-pressed={viewMode === 'casual'} disabled={workbenchContext.loadingHistory || workbenchContext.startingSession} title={workbenchContext.loadingHistory ? 'Aguarde o carregamento da conversa' : undefined} onClick={() => changeMode('casual')}>Casual</button>
            <button type="button" className="touch-target top-mode-option" aria-pressed={viewMode === 'professional'} disabled={workbenchContext.loadingHistory || workbenchContext.startingSession} title={workbenchContext.loadingHistory ? 'Aguarde o carregamento da conversa' : undefined} onClick={() => changeMode('professional')}>Professional</button>
          </div>
          {viewMode === 'casual' && (workbenchContext.session || workbenchContext.loadingHistory) && workbenchContext.session?.purpose !== 'preparation' && currentPipeline?.currentStage && <button type="button" className="touch-target casual-pipeline-link" onClick={() => setSelected('Pipelines')} disabled={workbenchContext.loadingHistory || workbenchContext.startingSession} aria-label={`Abrir pipeline ${currentPipeline.title}, fase ${stageNames[currentPipeline.currentStage]}`} title={workbenchContext.loadingHistory ? 'Aguarde o carregamento da conversa' : undefined}><span>{currentPipeline.title}</span><strong>{stageNames[currentPipeline.currentStage]}</strong></button>}
          <button type="button" className="touch-target icon-button theme-toggle" aria-label={theme === 'dark' ? 'Mudar para o tema claro' : 'Mudar para o tema escuro'} title={theme === 'dark' ? 'Tema claro' : 'Tema escuro'} onClick={toggleTheme}>{theme === 'dark' ? <Sun aria-hidden="true" /> : <Moon aria-hidden="true" />}</button>
          <button ref={activityTrigger} className="touch-target icon-button side-panel-toggle" aria-label="Painel lateral" title={activityOpen ? 'Fechar painel lateral' : 'Abrir painel lateral (atividade e terminal)'} aria-expanded={activityOpen} aria-controls="side-panel" onClick={() => { if (activityOpen) closeActivity(); else setActivityOpen(true) }}>{activityOpen ? <PanelRightClose aria-hidden="true" /> : <PanelRightOpen aria-hidden="true" />}</button>
        </div>
        {viewMode === 'professional' && <PipelineBar pipeline={currentPipeline} activity={stageActivity} viewStage={selected === 'Pipelines' && shownStage ? shownStage : stageViewRequest?.pipelineId===currentPipeline?.id ? stageViewRequest?.stage : undefined} onViewStage={stage => { if (!workbenchContext.startingSession && currentPipeline) { setStageViewRequest({requestId:++stageViewSequence.current,pipelineId:currentPipeline.id,workspaceId:currentPipeline.workspaceId,stage}); setSelected('Pipelines') } }} />}
      </header>
      {backend ? <Workbench backend={backend} restoreProject={restoreProject} selected={selected} newWorkRequest={newWorkRequest} newChatPrompt={newChatPrompt} viewMode={viewMode} historyOpenRequest={historyRequest} projectOpenRequest={projectRequest} stageViewRequest={stageViewRequest} onNavigate={selectDestination} onActivity={setActivityEvents} onPipeline={setCurrentPipeline} onShownStage={setShownStage} onOpenPipeline={openPipeline} onContextChange={reportWorkbenchContext} /> : <WorkArea selected={selected} workState={workState} />}
    </div>
    {activityOpen && <div ref={activityContainer} className={`activity-container${!desktop ? ' is-overlay' : ''}`} role={!desktop ? 'dialog' : undefined} aria-modal={!desktop || undefined} aria-labelledby={!desktop ? 'activity-title' : undefined}>
      {desktop && <div className="side-panel-resizer" role="separator" aria-orientation="vertical" aria-label="Redimensionar painel lateral" aria-controls="side-panel" tabIndex={0}
        aria-valuenow={panelWidth.width} aria-valuemin={minSidePanelWidth} aria-valuemax={maxSidePanelWidth()} title="Arraste para redimensionar · duplo clique volta ao tamanho padrão"
        onPointerDown={panelWidth.onPointerDown} onKeyDown={panelWidth.onKeyDown} onDoubleClick={panelWidth.reset} />}
      <div id="side-panel" className="side-panel">
        <div className="side-panel-tabs" role="tablist" aria-label="Painel lateral">
          <button type="button" role="tab" className="side-panel-tab" aria-selected={sidePanel === 'activity'} onClick={() => setSidePanel('activity')}><Activity aria-hidden="true" />Atividade</button>
          <button type="button" role="tab" className="side-panel-tab" aria-selected={sidePanel === 'terminal'} onClick={() => { setSidePanel('terminal'); setTerminalUsed(true) }}><SquareTerminal aria-hidden="true" />Terminal</button>
        </div>
        <div className="side-panel-body">
          <div className="side-panel-activity" hidden={sidePanel !== 'activity'}><ActivityPanel onClose={closeActivity} events={activityEvents} busy={workbenchContext.busy} /></div>
          {backend && terminalUsed && <TerminalPanel backend={backend} workspace={workbenchContext.workspace} onClose={closeActivity} visible={sidePanel === 'terminal'} />}
        </div>
      </div>
    </div>}
  </div>
}
