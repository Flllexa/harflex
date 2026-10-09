import { useCallback, useEffect, useRef, useState } from 'react'
import { Activity, ChevronDown, ChevronRight, FolderOpen, FolderSearch, ListTree, MessageSquare, Pin, PinOff, Plus, RefreshCw, Search, SlidersHorizontal, X } from 'lucide-react'
import type { AgentEvent, Backend, ChatProject } from '../lib/backend'
import type { Session, Workspace } from '../lib/backend'
import { destinations, type Destination } from './Sidebar'
import { requestOf } from '../features/workbench/continuation'
import { useRecentWork, workProgress } from '../features/pipelines/RecentWork'
import { localeTag, t, useT } from '../i18n'

type ChatSummary = { session: Session; title: string; lastUserRequest: string }
type LoadState = 'loading' | 'ready' | 'error'
type Props = {
  backend?: Backend
  workspace?: Workspace
  activeSessionId?: string
  activeSessionUpdatedAt?: string
  historyHasUserMessage: boolean
  selected: Destination
  busy: boolean
  loadingHistory: boolean
  /** Kept for callers; a draft no longer blocks anything here, since each chat keeps its own. */
  hasDraft?: boolean
  recoveryPrompt?: string
  mobileOpen: boolean
  onOpenSession: (sessionId: string, workspaceId: string, workspacePath?: string) => void
  /** Opens a work item of the project on the Pipelines screen. */
  onOpenPipeline?: (pipelineId: string, workspaceId: string) => void
  /** Changes when the open work item changes, so its stage here stays current. */
  pipelineRevision?: string
  /** Opens another project, from its group in the sidebar. */
  onOpenProject?: (path: string) => void
  onSelectDestination: (destination: Destination) => void
  onNewChat: (recoveryPrompt?: string) => void
  onProjects: () => void
  onToggleMode: () => void
  onClose: () => void
}

const pageSize = 24
const otherProjectChats = 5
const data = (value: unknown): Record<string, unknown> => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}

function basename(path: string) {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path
}

function plainTitle(content: unknown): string | undefined {
  if (typeof content !== 'string' || !content.trim()) return undefined
  // A continued conversation carries the old one's context after the person's words; the title is the words.
  const text = requestOf(content).trim()
  try { const parsed: unknown = JSON.parse(text); if (parsed && typeof parsed === 'object') return undefined } catch { /* Plain user text is expected. */ }
  if (/^\{\s*"|^```(?:json)?\s*[\[{]/i.test(text)) return undefined
  const normalized = text.replace(/\s+/g, ' ').trim()
  return normalized.length > 72 ? `${normalized.slice(0, 69).trimEnd()}…` : normalized
}
function titleFromEvents(events: AgentEvent[], session: Session): string {
  const saved = plainTitle(session.title)
  if (saved) return saved
  for (const event of events) {
    if (event.type !== 'message.user') continue
    const title = plainTitle(data(event.data).content)
    if (title) return title
  }
  return t('Conversa {id}', { id: session.id.slice(0, 12) })
}

function lastUserRequestFromEvents(events: AgentEvent[]) {
  for (let index = events.length - 1; index >= 0; index--) {
    if (events[index].type !== 'message.user') continue
    const content = data(events[index].data).content
    if (typeof content === 'string' && plainTitle(content)) return requestOf(content)
  }
  return ''
}
async function readChatSummary(backend: Backend, session: Session): Promise<ChatSummary> {
  const saved = plainTitle(session.title)
  if (saved && session.resumable) return { session, title: saved, lastUserRequest: '' }
  try {
    const events = await backend.listEvents(session.id, 0, 12)
    return { session, title: titleFromEvents(events, session), lastUserRequest: lastUserRequestFromEvents(events) }
  } catch { return { session, title: saved ?? t('Conversa {id}', { id: session.id.slice(0, 12) }), lastUserRequest: '' } }
}

function sessionState(session: Session) {
  if (session.status === 'running' || session.status === 'awaiting_approval') return 'Em execução'
  if (session.status === 'failed' && !session.resumable) return 'Somente leitura'
  if (session.resumable) return 'Retomável'
  return session.status === 'completed' ? 'Concluída' : 'Histórico'
}

export function CasualSidebar({ backend, workspace, activeSessionId, activeSessionUpdatedAt, historyHasUserMessage, selected, busy, loadingHistory, recoveryPrompt, mobileOpen, onOpenSession, onOpenPipeline, pipelineRevision, onOpenProject, onSelectDestination, onNewChat, onProjects, onToggleMode, onClose }: Props) {
  const t = useT()
  const work = useRecentWork(backend, workspace?.id, pipelineRevision)
  const [chats, setChats] = useState<ChatSummary[]>([])
  const [totalSessions, setTotalSessions] = useState(0)
  const [loadState, setLoadState] = useState<LoadState>('loading')
  const [search, setSearch] = useState('')
  const [moreState, setMoreState] = useState(false)
  const [loadError, setLoadError] = useState(false)
  const [openedChatId, setOpenedChatId] = useState<string>()
  const [projects, setProjects] = useState<ChatProject[]>([])
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const generation = useRef(0)
  const projectsRequest = useRef(0)

  // Every project's recent conversations and the pinned ones; the open project keeps its own fuller list below.
  const loadProjects = useCallback(async () => {
    if (!backend) { setProjects([]); return }
    const request = ++projectsRequest.current
    try {
      const next = await backend.listChatProjects(otherProjectChats)
      if (request === projectsRequest.current) setProjects(next)
    } catch { if (request === projectsRequest.current) setProjects([]) }
  }, [backend])
  useEffect(() => { void loadProjects() }, [loadProjects, workspace?.id, activeSessionId, activeSessionUpdatedAt])
  const pinnedIds = new Set(projects.flatMap(project => project.chats.filter(chat => chat.pinned).map(chat => chat.id)))
  const pinned = projects.flatMap(project => project.chats.filter(chat => chat.pinned).map(chat => ({ chat, project })))
  const others = projects.filter(project => project.workspace.id !== workspace?.id && project.workspace.available)
  async function togglePin(sessionId: string, value: boolean) {
    if (!backend) return
    try { await backend.setSessionPinned(sessionId, value) } finally { void loadProjects() }
  }
  function reveal(workspaceId: string) { if (backend) void backend.revealWorkspace(workspaceId).catch(() => undefined) }

  const loadHistory = useCallback(async () => {
    const request = ++generation.current
    if (!workspace || !backend) {
      setChats([])
      setTotalSessions(0)
      setLoadError(false)
      setLoadState('ready')
      return
    }
    setLoadState('loading')
    setLoadError(false)
    setMoreState(false)
    try {
      const sessions = (await backend.listSessions(workspace.id)).filter(session => session.workspaceId === workspace.id && (!session.purpose || session.purpose === 'chat'))
      if (generation.current !== request) return
      const summaries = await Promise.all(sessions.slice(0, pageSize).map(session => readChatSummary(backend, session)))
      if (generation.current !== request) return
      setChats(summaries)
      setTotalSessions(sessions.length)
      setLoadState('ready')
    } catch {
      if (generation.current !== request) return
      setChats([])
      setLoadError(true)
      setLoadState('error')
    }
  }, [backend, workspace])

  useEffect(() => {
    void loadHistory()
    return () => { generation.current++ }
  }, [loadHistory, activeSessionId, activeSessionUpdatedAt, historyHasUserMessage])
  useEffect(() => { setOpenedChatId(undefined) }, [workspace?.id])

  const filtered = chats.filter(chat => chat.title.toLocaleLowerCase('pt-BR').includes(search.trim().toLocaleLowerCase('pt-BR')))
  const canLoadMore = chats.length < totalSessions
  function startChat() {
    const active = chats.find(chat => chat.session.id === (activeSessionId ?? openedChatId))
    const terminalReadOnlyRequest = active && !active.session.resumable && active.session.status !== 'ready' ? active.lastUserRequest : ''
    setOpenedChatId(undefined)
    onNewChat(recoveryPrompt || terminalReadOnlyRequest || undefined)
  }

  async function loadMore() {
    if (!workspace || !backend || moreState) return
    setMoreState(true)
    const request = ++generation.current
    try {
      const sessions = (await backend.listSessions(workspace.id)).filter(session => session.workspaceId === workspace.id && (!session.purpose || session.purpose === 'chat'))
      if (generation.current !== request) return
      const start = chats.length
      setTotalSessions(sessions.length)
      const next = sessions.slice(start, start + pageSize)
      const summaries = await Promise.all(next.map(session => readChatSummary(backend, session)))
      if (generation.current !== request) return
      setChats(current => [...current, ...summaries])
    } catch {
      if (generation.current === request) setLoadError(true)
    } finally {
      if (generation.current === request) setMoreState(false)
    }
  }

  return <aside className={`casual-sidebar-content${mobileOpen ? ' is-open' : ''}`} aria-label={t('Histórico de conversas')}>
    <div className="sidebar-brand casual-brand">
      <span className="brand-word">Harflex</span><span className="local-label">Local</span>
      <button className="touch-target icon-button sidebar-close" aria-label={t('Fechar histórico')} onClick={onClose}><X aria-hidden="true" /></button>
    </div>

    <button type="button" className="touch-target primary-button new-work casual-new-chat" onClick={startChat} disabled={busy || loadingHistory || loadState === 'loading'}>
      <Plus aria-hidden="true" /><span>{t('Novo chat')}</span>
    </button>
    {workspace && <label className="casual-search"><Search aria-hidden="true" /><span className="visually-hidden">{t('Buscar conversas')}</span><input aria-label={t('Buscar conversas')} value={search} onChange={event => setSearch(event.target.value)} placeholder={t('Buscar conversas')} /></label>}
    {busy && <p className="casual-history-notice" role="status">{t('Sessão em execução; conclua ou cancele antes de trocar de conversa.')}</p>}
    {!busy && loadingHistory && <p className="casual-history-notice" role="status">{t('Carregando conversa… escolha outro chat para cancelar esta abertura.')}</p>}

    <div className="casual-history-scroll">
    {pinned.length > 0 && <section className="casual-group" aria-labelledby="casual-pinned-title">
      <h2 id="casual-pinned-title" className="casual-group-label"><Pin aria-hidden="true" />{t('Fixados')}</h2>
      <ul className="casual-chat-list" aria-label={t('Conversas fixadas')}>
        {pinned.filter(({ chat }) => (plainTitle(chat.title) ?? '').toLocaleLowerCase('pt-BR').includes(search.trim().toLocaleLowerCase('pt-BR'))).map(({ chat, project }) => <li key={chat.id} className="casual-chat-row">
          <button type="button" className="touch-target casual-chat-item" aria-current={chat.id === (activeSessionId ?? openedChatId) ? 'true' : undefined} disabled={busy}
            onClick={() => { setOpenedChatId(chat.id); onOpenSession(chat.id, chat.workspaceId, project.workspace.path) }}>
            <span className="casual-chat-title">{plainTitle(chat.title) ?? t('Conversa {id}', { id: chat.id.slice(0, 12) })}</span>
            <span className="casual-chat-meta"><span>{basename(project.workspace.path)}</span><time dateTime={chat.updatedAt}>{new Date(chat.updatedAt).toLocaleDateString(localeTag())}</time></span>
          </button>
          <button type="button" className="casual-pin is-on" aria-label={t('Desafixar conversa')} title={t('Desafixar')} onClick={() => void togglePin(chat.id, false)}><PinOff aria-hidden="true" /></button>
        </li>)}
      </ul>
    </section>}

    {workspace && work.runs.length > 0 && <section className="casual-group" aria-labelledby="casual-work-title">
      <h2 id="casual-work-title" className="casual-group-label"><ListTree aria-hidden="true" />{t('Trabalhos')}</h2>
      <ul className="casual-chat-list" aria-label={t('Trabalhos do projeto')}>
        {work.runs.slice(0, 5).map(run => <li key={run.id} className="casual-chat-row">
          <button type="button" className="touch-target casual-chat-item casual-work-item" disabled={busy || loadingHistory}
            aria-current={selected === 'Pipelines' && pipelineRevision?.startsWith(`${run.id}:`) ? 'true' : undefined}
            onClick={() => { onOpenPipeline?.(run.id, run.workspaceId); if (mobileOpen) onClose() }}>
            <span className="casual-chat-title">{run.title || t('Trabalho sem título')}</span>
            <span className="casual-chat-meta"><span>{workProgress(run)}</span><time dateTime={run.updatedAt}>{new Date(run.updatedAt).toLocaleDateString(localeTag())}</time></span>
          </button>
        </li>)}
      </ul>
      {work.runs.length > 5 && <button type="button" className="touch-target text-button casual-load-more" onClick={() => onSelectDestination('Pipelines')}>{t('Ver todos os {count} trabalhos', { count: work.runs.length })}</button>}
    </section>}

    <section className="casual-group casual-history" aria-label={t('Projeto atual')}>
      <div className="casual-group-heading">
        <div className="casual-group-title"><FolderOpen aria-hidden="true" /><div><strong>{workspace ? basename(workspace.path) : t('Nenhum projeto aberto')}</strong><span className="muted">{workspace ? t('Projeto aberto') : t('Escolha uma pasta para começar')}</span></div></div>
        <div className="casual-group-actions">
          {workspace && <button type="button" className="touch-target icon-button" aria-label={t('Abrir a pasta de {name}', { name: basename(workspace.path) })} title={t('Abrir a pasta')} onClick={() => reveal(workspace.id)}><FolderSearch aria-hidden="true" /></button>}
          <button type="button" className="touch-target icon-button" aria-label={t('Atualizar histórico')} onClick={() => { void loadHistory(); void loadProjects() }} disabled={loadState === 'loading' || !workspace}><RefreshCw aria-hidden="true" /></button>
        </div>
      </div>
      {!workspace ? <div className="casual-history-empty"><MessageSquare aria-hidden="true" /><p>{t('Abra um projeto para ver o histórico de chats.')}</p><button type="button" className="touch-target secondary-button" onClick={onProjects} disabled={busy || loadingHistory}>{t('Ver projetos')}</button></div>
        : loadState === 'loading' ? <p className="muted casual-history-state" role="status">{t('Carregando chats…')}</p>
          : loadState === 'error' ? <div className="casual-history-empty" role="alert"><p>{t('Não foi possível carregar o histórico.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void loadHistory()}>{t('Tentar novamente')}</button></div>
            : chats.length === 0 ? <div className="casual-history-empty"><MessageSquare aria-hidden="true" /><p>{t('Nenhuma conversa neste projeto.')}</p></div>
              : <>{filtered.length === 0 ? <p className="muted casual-history-state">{t('Nenhum chat corresponde à busca.')}</p> : <ul className="casual-chat-list" aria-label={t('Histórico de chats')}>
                  {filtered.map(chat => <li key={chat.session.id} className="casual-chat-row">
                    {/* The open chat stays reachable while it runs: clicking it only goes back to it. */}
                    <button type="button" className="touch-target casual-chat-item" aria-current={chat.session.id === (activeSessionId ?? openedChatId) ? 'true' : undefined} disabled={chat.session.id !== activeSessionId && busy}
                      onClick={() => { if (chat.session.id === activeSessionId && busy) { onSelectDestination('Conversas'); if (mobileOpen) onClose(); return } setOpenedChatId(chat.session.id); onOpenSession(chat.session.id, chat.session.workspaceId) }}>
                      <span className="casual-chat-title">{chat.title}</span>
                      <span className="casual-chat-meta"><span>{t(sessionState(chat.session))}</span><time dateTime={chat.session.updatedAt}>{new Date(chat.session.updatedAt).toLocaleDateString(localeTag())}</time></span>
                    </button>
                    <button type="button" className={`casual-pin${pinnedIds.has(chat.session.id) ? ' is-on' : ''}`} aria-label={pinnedIds.has(chat.session.id) ? t('Desafixar conversa') : t('Fixar conversa')} title={pinnedIds.has(chat.session.id) ? t('Desafixar') : t('Fixar')}
                      onClick={() => void togglePin(chat.session.id, !pinnedIds.has(chat.session.id))}>{pinnedIds.has(chat.session.id) ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />}</button>
                  </li>)}
                </ul>}
                {canLoadMore && <button type="button" className="touch-target text-button casual-load-more" disabled={moreState || busy || loadingHistory} onClick={() => void loadMore()}>{moreState ? t('Carregando…') : search.trim() ? t('Carregar mais para esta busca') : t('Carregar mais conversas')}</button>}
                {loadError && <p className="form-error" role="alert">{t('Não foi possível carregar mais conversas.')}</p>}</>}
    </section>

    {(others.length > 0 || workspace) && <section className="casual-group" aria-labelledby="casual-others-title">
      <div className="casual-others-heading">
        <h2 id="casual-others-title" className="casual-group-label">{t('Outros projetos')}</h2>
        <button type="button" className="touch-target icon-button" aria-label={t('Adicionar projeto')} title={t('Adicionar projeto')} onClick={onProjects} disabled={busy || loadingHistory}><Plus aria-hidden="true" /></button>
      </div>
      {others.map(project => {
        const name = basename(project.workspace.path)
        const open = !collapsed.has(project.workspace.id)
        const visible = project.chats.filter(chat => !chat.pinned && (plainTitle(chat.title) ?? '').toLocaleLowerCase('pt-BR').includes(search.trim().toLocaleLowerCase('pt-BR')))
        return <div key={project.workspace.id} className="casual-project-group" role="group" aria-label={t('Projeto {name}', { name })}>
          <div className="casual-group-heading">
            <button type="button" className="casual-group-toggle" aria-expanded={open} onClick={() => setCollapsed(previous => { const next = new Set(previous); if (next.has(project.workspace.id)) next.delete(project.workspace.id); else next.add(project.workspace.id); return next })}>
              {open ? <ChevronDown aria-hidden="true" /> : <ChevronRight aria-hidden="true" />}<FolderOpen aria-hidden="true" /><span>{name}</span>
            </button>
            <div className="casual-group-actions">
              <button type="button" className="touch-target icon-button" aria-label={t('Abrir a pasta de {name}', { name })} title={t('Abrir a pasta')} onClick={() => reveal(project.workspace.id)}><FolderSearch aria-hidden="true" /></button>
              {onOpenProject && <button type="button" className="touch-target icon-button" aria-label={t('Novo chat em {name}', { name })} title={t('Novo chat neste projeto')} disabled={busy} onClick={() => onOpenProject(project.workspace.path)}><Plus aria-hidden="true" /></button>}
            </div>
          </div>
          {open && (visible.length === 0 ? <p className="muted casual-history-state">{project.total === 0 ? t('Nenhuma conversa.') : t('Nenhum chat corresponde à busca.')}</p>
            : <ul className="casual-chat-list" aria-label={t('Conversas de {name}', { name })}>
              {visible.map(chat => <li key={chat.id} className="casual-chat-row">
                <button type="button" className="touch-target casual-chat-item" disabled={busy} onClick={() => { setOpenedChatId(chat.id); onOpenSession(chat.id, chat.workspaceId, project.workspace.path) }}>
                  <span className="casual-chat-title">{plainTitle(chat.title) ?? t('Conversa {id}', { id: chat.id.slice(0, 12) })}</span>
                  <span className="casual-chat-meta"><span>{t(sessionState(chat))}</span><time dateTime={chat.updatedAt}>{new Date(chat.updatedAt).toLocaleDateString(localeTag())}</time></span>
                </button>
                <button type="button" className="casual-pin" aria-label={t('Fixar conversa')} title={t('Fixar')} onClick={() => void togglePin(chat.id, true)}><Pin aria-hidden="true" /></button>
              </li>)}
            </ul>)}
          {open && project.total > project.chats.length && onOpenProject && <button type="button" className="touch-target text-button casual-load-more" disabled={busy} onClick={() => onOpenProject(project.workspace.path)}>{t('Ver as {count} conversas', { count: project.total })}</button>}
        </div>
      })}
    </section>}
    </div>

    <details className="casual-tools">
      <summary className="touch-target nav-item"><SlidersHorizontal aria-hidden="true" /><span>{t('Ferramentas')}</span></summary>
      <nav aria-label={t('Ferramentas')}>
        {destinations.map(([name, Icon]) => <button key={name} type="button" className="touch-target nav-item" aria-current={selected === name ? 'page' : undefined} disabled={loadingHistory} onClick={() => { onSelectDestination(name); if (mobileOpen) onClose() }} title={t(name)}>
          <Icon aria-hidden="true" /><span>{t(name)}</span>
        </button>)}
      </nav>
    </details>

    <div className="sidebar-footer casual-sidebar-footer">
      <button type="button" className="touch-target secondary-button casual-mode-switch" onClick={onToggleMode} disabled={loadingHistory} title={loadingHistory ? t('Aguarde o carregamento da conversa') : undefined}><Activity aria-hidden="true" /><span>{t('Mudar para Professional')}</span></button>
      <span className="muted">{t('SDD, evidências e operação completa')}</span>
    </div>
  </aside>
}
