import { useEffect, useRef, useState } from 'react'
import { Activity, FolderOpen, RefreshCw } from 'lucide-react'
import { IonPicker } from '../../components/IonPicker'
import { errorMessage, type AgentEvent, type Backend, type Session } from '../../lib/backend'
import { MetricsPane } from '../workbench/MetricsPane'

type Props = { backend: Backend; workspaceId?: string; onProjects: () => void; onOpenSession: (id: string) => Promise<void> }
const pageSize = 1000
const maxEvents = 5000

export function ExecutionPage({ backend, workspaceId, onProjects, onOpenSession }: Props) {
  const [sessions, setSessions] = useState<Session[]>([])
  const [selectedId, setSelectedId] = useState('')
  const [events, setEvents] = useState<AgentEvent[]>([])
  const [backendFilter, setBackendFilter] = useState('')
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [eventState, setEventState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle')
  const [truncated, setTruncated] = useState(false)
  const [error, setError] = useState<string>()
  const [openError, setOpenError] = useState<string>()
  const sessionsGeneration = useRef(0)
  const eventsGeneration = useRef(0)

  async function refresh() {
    if (!workspaceId) { setState('ready'); setSessions([]); return }
    const current = ++sessionsGeneration.current
    setState('loading'); setError(undefined)
    try {
      const listed = await backend.listSessions(workspaceId)
      if (current !== sessionsGeneration.current) return
      const ordered = [...listed].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
      setSessions(ordered.slice(0, 50))
      setSelectedId(previous => ordered.some(item => item.id === previous) ? previous : ordered[0]?.id ?? '')
      setState('ready')
    } catch (failure) { if (current === sessionsGeneration.current) { setState('error'); setError(errorMessage(failure)) } }
  }
  useEffect(() => { void refresh(); return () => { sessionsGeneration.current++ } }, [backend, workspaceId])

  useEffect(() => {
    if (!selectedId) { setEvents([]); setEventState('idle'); return }
    const current = ++eventsGeneration.current
    setEventState('loading'); setTruncated(false)
    void (async () => {
      try {
        const all: AgentEvent[] = []
        let after = 0
        for (let page = 0; page < maxEvents / pageSize; page++) {
          const batch = await backend.listEvents(selectedId, after, pageSize)
          if (current !== eventsGeneration.current) return
          all.push(...batch)
          if (batch.length < pageSize) { setEvents(all); setEventState('ready'); return }
          const next = batch[batch.length - 1].sequence
          if (next <= after) throw new Error('event_cursor_stalled')
          after = next
        }
        setEvents(all); setTruncated(true); setEventState('ready')
      } catch (failure) { if (current === eventsGeneration.current) { setEventState('error'); setError(errorMessage(failure)) } }
    })()
    return () => { eventsGeneration.current++ }
  }, [backend, selectedId])

  const filtered = sessions.filter(item => !backendFilter || item.backendId === backendFilter)
  const selected = sessions.find(item => item.id === selectedId)
  const backendIds = [...new Set(sessions.map(item => item.backendId))].sort()
  async function openSession(id: string) {
    setOpenError(undefined)
    try { await onOpenSession(id) }
    catch (failure) { setOpenError(errorMessage(failure)) }
  }
  return <div className="execution-page">
    <div className="destination-heading"><div><h2>Custos e execução</h2><p className="muted">Uso e duração derivados do journal local, sem estimar valores monetários.</p></div>{workspaceId && <button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />Atualizar</button>}</div>
    {!workspaceId ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>Abra um projeto para consultar execuções</strong><button type="button" className="touch-target primary-button" onClick={onProjects}>Abrir projetos</button></div> : state === 'loading' ? <p className="muted" role="status">Carregando sessões…</p> : state === 'error' ? <div className="inline-error" role="alert"><p>{error}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>Tentar novamente</button></div> : sessions.length === 0 ? <div className="catalog-empty"><Activity aria-hidden="true" /><strong>Nenhuma execução registrada</strong><span className="muted">As sessões aparecerão aqui após iniciar um trabalho.</span></div> : <div className="execution-layout">
      <section className="execution-sessions"><div className="destination-heading"><div><h3>Sessões recentes</h3><p className="muted">Exibindo até 50 sessões deste projeto.</p></div></div><IonPicker id="execution-backend-filter" label="Filtrar backend" value={backendFilter} searchable onChange={id => { setBackendFilter(id); const first = sessions.find(item => !id || item.backendId === id); if (first) setSelectedId(first.id) }} options={[{ value: '', label: 'Todos' }, ...backendIds.map(id => ({ value: id, label: id }))]} /><ul>{filtered.map(item => <li key={item.id}><button type="button" className={`touch-target execution-session${item.id === selectedId ? ' is-current' : ''}`} onClick={() => setSelectedId(item.id)} aria-current={item.id === selectedId ? 'true' : undefined}><strong>{item.title || item.backendId}</strong><span>{item.title ? `${item.backendId} · ` : ''}{item.status} · {new Date(item.updatedAt).toLocaleString('pt-BR')}</span><span className="mono">{item.id.slice(0, 12)}</span></button></li>)}</ul>{filtered.length === 0 && <p className="muted">Nenhuma sessão usa este backend.</p>}</section>
      <section className="execution-detail"><div className="destination-heading"><div><h3>{selected ? `Sessão ${selected.id.slice(0, 12)}` : 'Selecione uma sessão'}</h3><p className="muted">Origem: eventos persistidos da sessão selecionada.</p></div>{selected && <button type="button" className="touch-target secondary-button" onClick={() => void openSession(selected.id)}>Abrir conversa</button>}</div>{openError && <p className="form-error" role="alert">{openError}</p>}{eventState === 'loading' ? <p className="muted" role="status">Lendo eventos…</p> : eventState === 'error' ? <p className="form-error" role="alert">{error}</p> : <><MetricsPane events={events} />{truncated && <p className="project-warning">A sessão excedeu 5.000 eventos. Os valores acima cobrem somente os primeiros eventos carregados.</p>}</>}</section>
    </div>}
  </div>
}
