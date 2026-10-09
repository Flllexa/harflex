import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, RefreshCw } from 'lucide-react'
import { errorMessage, type Agent, type Backend, type Delegation } from '../../lib/backend'
import { delegationBudgetText } from '../../lib/delegationBudget'
import { useT } from '../../i18n'

type Props = { backend: Backend; sessionId: string; agents: Agent[]; onOpenSession: (id: string) => Promise<void> }

const labels: Record<Delegation['status'], string> = {
  ready: 'Pronto', running: 'Em execução', awaiting_approval: 'Aguardando aprovação',
  completed: 'Concluído', failed: 'Falhou', cancelled: 'Cancelado', paused: 'Interrompido',
  history_too_large: 'Histórico indisponível',
}
const reasons: Record<string, string> = {
  approval_denied: 'Aprovação negada', turn_limit: 'Limite de turnos atingido',
  execution_failed: 'Falha na execução', journal_unavailable: 'Journal indisponível',
}
const shortID = (id: string) => id.slice(0, 8)
const active = (status: Delegation['status']) => status === 'running' || status === 'awaiting_approval'

export function DelegationTree({ backend, sessionId, agents, onOpenSession }: Props) {
  const t = useT()
  const [rootId, setRootId] = useState('')
  const [parentId, setParentId] = useState('')
  const [children, setChildren] = useState<Record<string, Delegation[]>>({})
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set())
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [pendingId, setPendingId] = useState('')
  const [refreshKey, setRefreshKey] = useState(0)
  const readbackOrder = useRef<Record<string, number>>({})
  const knownParents = useRef<Record<string, string>>({})
  const pendingTerminals = useRef(new Set<string>())
  const readingParents = useRef(0)

  async function refreshParent(id: string): Promise<Delegation[]> {
    readingParents.current++
    const order = (readbackOrder.current[id] ?? 0) + 1
    readbackOrder.current[id] = order
    try {
      const links = await backend.listDelegations(id)
      if (readbackOrder.current[id] === order) {
        for (const link of links) knownParents.current[link.childSessionId] = id
        setChildren(current => ({ ...current, [id]: links }))
        if (links.some(link => pendingTerminals.current.delete(link.childSessionId))) return refreshParent(id)
      }
      return links
    } finally {
      readingParents.current--
      if (readingParents.current === 0) pendingTerminals.current.clear()
    }
  }

  useEffect(() => {
    let live = true
    setState('loading'); setError(''); setNotice('')
    async function load() {
      try {
        const ancestors: Delegation[] = []
        const seen = new Set([sessionId])
        let cursor = sessionId
        for (let depth = 0; depth < 3; depth++) {
          const link = await backend.getParentDelegation(cursor)
          if (!link) break
          if (link.childSessionId !== cursor || seen.has(link.parentSessionId)) throw new Error('invalid_delegation_path')
          ancestors.unshift(link)
          cursor = link.parentSessionId
          seen.add(cursor)
        }
        const path = [cursor, ...ancestors.map(link => link.childSessionId)]
        await Promise.all(path.map(id => refreshParent(id)))
        if (!live) return
        setRootId(cursor)
        setParentId(ancestors[ancestors.length - 1]?.parentSessionId ?? '')
        setExpanded(new Set(path))
        setState('ready')
      } catch (failure) {
        if (!live) return
        setError(errorMessage(failure))
        setState('error')
      }
    }
    void load()
    return () => { live = false }
  }, [backend, sessionId, refreshKey])

  useEffect(() => backend.onEvent(event => {
    if (!['run.started', 'external.run.started', 'approval.requested', 'approval.approved', 'approval.denied',
      'run.completed', 'run.failed', 'run.cancelled', 'run.interrupted',
      'external.run.completed', 'external.run.failed', 'external.run.cancelled', 'external.run.interrupted'].includes(event.type)) return
    const parent = knownParents.current[event.streamId]
    if (parent) void refreshParent(parent).catch(() => setError(t('Não foi possível atualizar o estado do subagente.')))
    else if (readingParents.current > 0) pendingTerminals.current.add(event.streamId)
  }), [backend])

  async function open(id: string) {
    if (pendingId || id === sessionId) return
    setPendingId(id); setError('')
    try { await onOpenSession(id) }
    catch (failure) { setError(errorMessage(failure)) }
    finally { setPendingId('') }
  }

  async function toggle(id: string) {
    if (expanded.has(id)) {
      setExpanded(current => { const next = new Set(current); next.delete(id); return next })
      return
    }
    if (children[id] === undefined) {
      setPendingId(id); setError('')
      try {
        await refreshParent(id)
      } catch (failure) { setError(errorMessage(failure)); setPendingId(''); return }
      setPendingId('')
    }
    setExpanded(current => new Set(current).add(id))
  }

  async function cancel(link: Delegation) {
    if (pendingId || !active(link.status)) return
    setPendingId(link.childSessionId); setError(''); setNotice('')
    try {
      await backend.cancel(link.childSessionId)
      const links = await refreshParent(link.parentSessionId)
      const updated = links.find(item => item.id === link.id)
      setNotice(updated?.status === 'cancelled' ? t('Cancelamento confirmado no journal.') : t('Cancelamento solicitado; aguardando registro no journal.'))
    } catch (failure) {
      setError(errorMessage(failure))
      try {
        await refreshParent(link.parentSessionId)
      } catch { /* Keep the visible state and show the original error. */ }
    } finally { setPendingId('') }
  }

  function branch(parent: string): ReactNode {
    const links = children[parent] ?? []
    if (links.length === 0) return <p className="muted delegation-empty">{t('Nenhum subagente nesta sessão.')}</p>
    return <ul className="delegation-tree">{links.map(link => {
      const name = agents.find(agent => agent.id === link.agentId)?.name ?? t('Agente {id}', { id: shortID(link.agentId) })
      const isCurrent = link.childSessionId === sessionId
      const openBranch = expanded.has(link.childSessionId)
      return <li key={link.id}>
        <div className={`delegation-node${isCurrent ? ' delegation-current' : ''}`}>
          <div className="delegation-node-main">
            {link.depth < 3 && <button type="button" className="touch-target delegation-expand" onClick={() => void toggle(link.childSessionId)} disabled={!!pendingId} aria-expanded={openBranch} aria-label={openBranch ? t('Ocultar subagentes de {id}', { id: shortID(link.childSessionId) }) : t('Ver subagentes de {id}', { id: shortID(link.childSessionId) })}>{openBranch ? <ChevronDown aria-hidden="true" /> : <ChevronRight aria-hidden="true" />}</button>}
            <div className="delegation-identity"><strong>{name}</strong><span className="muted mono">{shortID(link.childSessionId)} · {t('nível {level}', { level: link.depth })}{isCurrent ? ` · ${t('sessão atual')}` : ''}</span></div>
            <span className={`status-chip delegation-status delegation-status-${link.status}`}>{t(labels[link.status])}</span>
          </div>
          {link.taskPrompt && <p className="delegation-outcome">{t('Prévia da tarefa registrada (redigida): {prompt}', { prompt: link.taskPrompt })}</p>}
          <p className="muted delegation-outcome">{t('{budget}. A espera por aprovação não conta; tentativas interrompidas contam. Não é limite de custo ou tokens.', { budget: delegationBudgetText(link) })}</p>
          {link.status === 'ready' && link.taskPrompt && <p className="muted delegation-outcome">{t('Abra a conversa e use “Preparar tarefa delegada” para revisar o texto completo no rascunho. Não há reexecução automática.')}</p>}
          {link.status === 'completed' && <p className="delegation-outcome">{link.result ? <>{t('Resultado: ')}<span>{link.result}</span></> : t('Concluído sem resposta textual no journal.')}</p>}
          {link.status === 'failed' && <p className="delegation-outcome delegation-error">{t(reasons[link.errorCode] ?? 'Falha registrada sem detalhe no journal.')}</p>}
          <div className="delegation-actions">
            {!isCurrent && <button type="button" className="touch-target secondary-button" onClick={() => void open(link.childSessionId)} disabled={!!pendingId} aria-label={t('Abrir conversa do subagente {id}', { id: shortID(link.childSessionId) })}>{t('Abrir conversa')}</button>}
            {active(link.status) && <button type="button" className="touch-target secondary-button delegation-cancel" onClick={() => void cancel(link)} disabled={!!pendingId} aria-label={t('Cancelar subagente {id}', { id: shortID(link.childSessionId) })}>{t('Cancelar execução')}</button>}
            {active(link.status) && link.depth < 3 && <span className="muted delegation-cancel-note">{t('Também cancela descendentes em execução.')}</span>}
          </div>
        </div>
        {openBranch && <div className="delegation-branch">{branch(link.childSessionId)}</div>}
      </li>
    })}</ul>
  }

  return <section className="delegation-panel" aria-labelledby="delegation-heading">
    <div className="destination-heading"><div><h3 id="delegation-heading">{t('Delegações da sessão')}</h3><p className="muted">{t('Vínculos e desfechos registrados localmente.')}</p></div><button type="button" className="touch-target secondary-button" onClick={() => setRefreshKey(value => value + 1)} disabled={state === 'loading'} aria-label={t('Atualizar delegações')}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button></div>
    {state === 'loading' && <p role="status" className="muted">{t('Lendo delegações…')}</p>}
    {state === 'error' && <div className="inline-error" role="alert"><p>{t('Não foi possível ler as delegações. {error}', { error })}</p><button type="button" className="touch-target secondary-button" onClick={() => setRefreshKey(value => value + 1)}>{t('Tentar novamente')}</button></div>}
    {state === 'ready' && <>
      {parentId && <button type="button" className="touch-target secondary-button delegation-back" onClick={() => void open(parentId)} disabled={!!pendingId}>{t('Voltar para sessão pai')}</button>}
      <div className="delegation-root"><div><strong>{t('Sessão principal')}</strong><span className="muted mono">{shortID(rootId)}{rootId === sessionId ? ` · ${t('sessão atual')}` : ''}</span></div>{rootId !== sessionId && <button type="button" className="touch-target secondary-button" onClick={() => void open(rootId)} disabled={!!pendingId}>{t('Abrir conversa principal')}</button>}</div>
      {branch(rootId)}
    </>}
    {state !== 'error' && error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
  </section>
}
