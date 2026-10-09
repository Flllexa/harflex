import { useEffect, useRef, useState } from 'react'
import { Download, Filter, RefreshCw, ScrollText } from 'lucide-react'
import { IonPicker } from '../../components/IonPicker'
import { errorMessage, type Backend, type LogEntry } from '../../lib/backend'
import { localeTag, useT } from '../../i18n'
import { AuditDialog } from '../workbench/AuditDialog'

type Props = { backend: Backend; workspaceId?: string }
const eventTypes = ['', 'run.started', 'run.completed', 'run.failed', 'run.cancelled', 'run.interrupted', 'approval.requested', 'approval.approved', 'approval.denied', 'tool.called', 'tool.completed', 'tool.failed', 'usage.recorded', 'external.event']
const pageSize = 50

export function LogsPage({ backend, workspaceId }: Props) {
  const t = useT()
  const [items, setItems] = useState<LogEntry[]>([])
  const [eventType, setEventType] = useState('')
  const [allProjects, setAllProjects] = useState(false)
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [more, setMore] = useState(false)
  const [hasMore, setHasMore] = useState(false)
  const [error, setError] = useState<string>()
  const [auditSession, setAuditSession] = useState<string>()
  const generation = useRef(0)
  const scope = allProjects ? '' : workspaceId ?? ''

  async function refresh() {
    const current = ++generation.current
    setState('loading')
    setError(undefined)
    try {
      const result = await backend.listLogEvents({ workspaceId: scope, type: eventType, beforeId: 0, limit: pageSize })
      if (current === generation.current) { setItems(result); setHasMore(result.length === pageSize); setState('ready') }
    } catch {
      if (current === generation.current) setState('error')
    }
  }
  useEffect(() => { void refresh(); return () => { generation.current++ } }, [backend, scope, eventType])

  async function loadMore() {
    const beforeId = items[items.length - 1]?.cursor
    if (!beforeId || more) return
    setMore(true)
    setError(undefined)
    try {
      const next = await backend.listLogEvents({ workspaceId: scope, type: eventType, beforeId, limit: pageSize })
      setItems(current => [...current, ...next])
      setHasMore(next.length === pageSize)
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setMore(false) }
  }

  return <div className="logs-page">
    <div className="destination-heading"><div><h2>{t('Logs e auditoria')}</h2><p className="muted">{t('Eventos persistidos das sessões. Conteúdo sensível não aparece neste índice.')}</p></div><button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button></div>
    <div className="logs-filter"><Filter aria-hidden="true" /><IonPicker id="logs-event-type" label={t('Tipo de evento')} value={eventType} onChange={setEventType} searchable options={eventTypes.map(type => ({ value: type, label: type || t('Todos os tipos') }))} />
      {workspaceId && <label className="logs-scope"><input type="checkbox" checked={allProjects} onChange={event => setAllProjects(event.target.checked)} />{t('Todos os projetos')}</label>}
    </div>
    {state === 'loading' && <p className="muted" role="status">{t('Carregando eventos…')}</p>}
    {state === 'error' && <div className="inline-error" role="alert"><p>{t('Não foi possível carregar os eventos.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>{t('Tentar novamente')}</button></div>}
    {state === 'ready' && (items.length === 0 ? <div className="catalog-empty"><ScrollText aria-hidden="true" /><strong>{t('Nenhum evento encontrado')}</strong><span className="muted">{t('Inicie uma sessão ou ajuste o filtro.')}</span></div>
      : <><div className="logs-table-wrap"><table className="event-table logs-table"><caption className="visually-hidden">{t('Índice de eventos persistidos')}</caption><thead><tr><th scope="col">{t('Horário')}</th><th scope="col">{t('Tipo')}</th><th scope="col">{t('Sessão')}</th><th scope="col">#</th><th scope="col">{t('Auditoria')}</th></tr></thead><tbody>{items.map(item => <tr key={item.id}>
        <td data-label={t('Horário')}><time dateTime={item.createdAt}>{new Date(item.createdAt).toLocaleString(localeTag())}</time></td><td data-label={t('Tipo')} className="mono">{item.type}</td><td data-label={t('Sessão')} className="mono" title={item.sessionId}>{item.sessionId.slice(0, 10)}</td><td data-label="#" className="mono">{item.sequence}</td><td data-label={t('Auditoria')}><button type="button" className="touch-target icon-button" aria-label={t('Exportar auditoria da sessão {id}', { id: item.sessionId.slice(0, 10) })} onClick={() => setAuditSession(item.sessionId)}><Download aria-hidden="true" /></button></td>
      </tr>)}</tbody></table></div>{hasMore && <button type="button" className="touch-target secondary-button logs-more" onClick={() => void loadMore()} disabled={more}>{more ? t('Carregando…') : t('Carregar mais')}</button>}</>)}
    {error && <p className="form-error" role="alert">{error}</p>}
    {auditSession && <AuditDialog backend={backend} sessionId={auditSession} onClose={() => setAuditSession(undefined)} />}
  </div>
}
