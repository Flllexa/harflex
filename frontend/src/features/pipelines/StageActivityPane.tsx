import { useEffect, useRef, useState } from 'react'
import { Activity, RefreshCw } from 'lucide-react'
import { errorMessage, type AgentEvent, type Backend, type Pipeline, type PipelineStage, type PipelineStageActivity } from '../../lib/backend'
import { localeTag, useT } from '../../i18n'
import { DesignMarkdown } from './PipelineDesignDocument'
import './stageActivity.css'

type Translate = ReturnType<typeof useT>

const names = { discovery: 'Discovery', spec: 'SPEC', plan: 'Plan', code: 'Code', eval: 'QA', prs: 'PRs' }
const busy = (value?: PipelineStageActivity) => !!value && (value.phase !== '' || ['running','awaiting_approval','cancellation_pending'].includes(value.status))
const follow = (value?:PipelineStageActivity) => busy(value) || !!value?.sessionId && value.status==='ready'

function eventLabel(event: AgentEvent, translate: Translate): string | undefined {
  const data = event.data as Record<string,unknown>
  switch (event.type) {
    case 'run.started': case 'external.run.started': return translate('IA iniciou a execução')
    case 'run.completed': case 'external.run.completed': return translate('Execução concluída')
    case 'run.failed': case 'external.run.failed': return translate('Execução interrompida por erro')
    case 'run.cancelled': case 'external.run.cancelled': return translate('Execução cancelada')
    case 'approval.requested': return translate('Autorização solicitada')
    case 'approval.approved': return translate('Autorização concedida')
    case 'approval.denied': return translate('Autorização recusada')
    case 'tool.called': return translate('Executando {name}', { name: typeof data.name === 'string' ? data.name : translate('ferramenta') })
    case 'tool.completed': return translate('Concluiu {name}', { name: typeof data.name === 'string' ? data.name : translate('a ação') })
    case 'tool.failed': case 'tool.denied': return translate('Ação não executada')
    default: return undefined
  }
}

function preparedText(events: AgentEvent[], stage: PipelineStage): string {
  if (!['discovery','spec','plan'].includes(stage)) return ''
  const message = [...events].reverse().find(event => event.type === 'message.assistant')
  const content = (message?.data as Record<string,unknown> | undefined)?.content
  if (typeof content !== 'string' || content.length > 65536) return ''
  try { const parsed = JSON.parse(content) as {document?:unknown}; return typeof parsed.document === 'string' ? parsed.document : '' } catch { return '' }
}

export function StageActivityPane({backend,pipeline,stage}: {backend:Backend; pipeline:Pipeline; stage:PipelineStage}) {
  const t = useT()
  const labels: Record<string,string> = { pending: t('Ainda não iniciou'), ready: t('Pronto'), active: t('Em andamento'), running: t('IA trabalhando'), completed: t('IA concluiu a etapa'), failed: t('Execução falhou'), cancelled: t('Cancelada'), paused: t('Pausada'), stale: t('Documento desatualizado'), waiting_user: t('Aguardando revisão'), awaiting_approval: t('Aguardando sua autorização') }
  const panel = useRef<HTMLElement>(null)
  const scope = `${pipeline.workspaceId}:${pipeline.id}:${stage}`, epoch = useRef(0), session = useRef(''), order = useRef(0)
  const [activity,setActivity] = useState<PipelineStageActivity>(), [events,setEvents] = useState<AgentEvent[]>([]), [error,setError] = useState(''), [retry,setRetry] = useState(0), [paused,setPaused] = useState(false)
  useEffect(() => { panel.current?.focus({preventScroll:true}); panel.current?.scrollIntoView?.({block:'start',behavior:'auto'}) },[scope])
  useEffect(() => {
    const ticket = ++epoch.current; let live = true, timer:ReturnType<typeof setTimeout> | undefined
    const started = Date.now(); session.current = ''; setActivity(undefined); setEvents([]); setError(''); setPaused(false)
    const valid = () => live && ticket === epoch.current
    const merge = (incoming:AgentEvent[]) => setEvents(current => [...new Map([...current,...incoming].map(event => [event.id,event])).values()].sort((a,b) => a.sequence-b.sequence).slice(-1000))
    const unsubscribe = backend.onEvent(event => { if (valid() && event.streamId === session.current) merge([event]) })
    const read = async () => {
      const readOrder = ++order.current
      try {
        const value = await backend.getPipelineStageActivity(pipeline.id,stage)
        if (!valid() || readOrder !== order.current) return
        if (value.pipelineId !== pipeline.id || value.workspaceId !== pipeline.workspaceId || value.stage !== stage) throw new Error('Activity returned for another work')
        if (session.current !== value.sessionId) { session.current = value.sessionId; setEvents([]) }
        setActivity(value); setError('')
        if (value.sessionId) {
          const found = await backend.listEvents(value.sessionId,0,1000)
          if (!valid() || readOrder !== order.current || session.current !== value.sessionId) return
          if (found.some(event => event.streamId !== value.sessionId)) throw new Error('Activity events returned for another session')
          merge(found)
        }
        if (follow(value)) {
          if (Date.now()-started >= 180000) { setPaused(true); return }
          if (valid()) timer = setTimeout(() => void read(),1000)
        }
      } catch (failure) { if (valid()) setError(errorMessage(failure)) }
    }
    void read()
    return () => { live = false; epoch.current++; order.current++; if (timer) clearTimeout(timer); unsubscribe() }
  },[backend,scope,retry])
  const timeline = events.map(event => ({event,label:eventLabel(event,t)})).filter(item => item.label), document = preparedText(events,stage)
  return <section ref={panel} tabIndex={-1} className="stage-activity-pane" aria-label={t('Atividade de {stage}', { stage: names[stage] })}>
    <header><div><h3><Activity aria-hidden="true" />{t('Atividade de {stage}', { stage: names[stage] })}</h3><p className="muted">{activity ? labels[activity.status] ?? activity.status : t('Carregando atividade…')}{activity?.modelId && ` · ${activity.modelId}`}</p></div><button type="button" className="touch-target secondary-button" onClick={() => setRetry(value => value+1)}><RefreshCw aria-hidden="true" />{t('Atualizar atividade')}</button></header>
    {error && <p className="form-error" role="alert">{error}</p>}
    {activity?.errorCode && <p className="form-error" role="alert">{errorMessage({cause:{code:activity.errorCode}})} {t('Os documentos desta tentativa não foram publicados.')}</p>}
    {busy(activity) && <p className="stage-live" role="status"><span aria-hidden="true" />{activity?.phase === stage ? t('A IA está preparando {stage}…', { stage: names[stage] }) : t('Acompanhe as ações desta etapa abaixo.')}</p>}
    {!activity?.sessionId && activity && <p className="muted">{stage === 'discovery' ? t('O Discovery foi escrito por você. Os ajustes feitos pela IA aparecerão aqui.') : t('Esta etapa ainda não tem execução da IA registrada.')}</p>}
    {timeline.length > 0 && <ol className="stage-event-log">{timeline.map(({event,label}) => <li key={event.id}><span>{label}</span><time dateTime={event.createdAt}>{new Date(event.createdAt).toLocaleTimeString(localeTag())}</time></li>)}</ol>}
    {document && <details className="stage-prepared-preview"><summary className="touch-target">{t('Ver texto produzido pela IA nesta execução')}</summary><DesignMarkdown content={document} /></details>}
    {paused && <p className="muted" role="status">{t('O acompanhamento pausou após 3 minutos. Atualize para conferir o resultado.')}</p>}
  </section>
}
