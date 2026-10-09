import { useState } from 'react'
import type { AgentEvent, Backend, Pipeline, PipelineStage } from '../../lib/backend'
import type { ConversationItem } from '../../state/session'
import { AuditDialog } from './AuditDialog'
import { DiffView } from '../../components/DiffView'
import { localeTag, useT } from '../../i18n'

const views = ['SPEC', 'Plano', 'Diff', 'Testes', 'Avaliação', 'PRs', 'Eventos'] as const
type View = typeof views[number]

export function ArtifactPane({ messages, events, backend, sessionId, pipeline, onPipelines }: { messages: ConversationItem[]; events: AgentEvent[]; backend: Backend; sessionId?: string; pipeline?: Pipeline; onPipelines?: () => void }) {
  const t = useT()
  const [view, setView] = useState<View>('Diff')
  const [exporting, setExporting] = useState(false)
  const diffs = messages.flatMap(item => item.kind === 'tool' && item.call.diff ? [{ id: item.id, path: item.call.path ?? item.call.name, diff: item.call.diff }] : [])
  const testCalls = messages.flatMap(item => item.kind === 'tool' && ['bash', 'powershell'].includes(item.call.name) && /\b(?:go test|npm (?:run )?test|pnpm test|pytest|cargo test|mvn test|gradle test)\b/i.test(JSON.stringify(item.call.arguments)) ? [item.call] : [])
  function artifact(stage: PipelineStage, label: string) {
    const value = pipeline?.artifacts[stage]
    if (value?.content) return <section className="artifact-document"><div className="destination-heading"><h3>{t('{label} · versão {version}', { label, version: value.version })}</h3><span className="muted">{new Date(value.updatedAt).toLocaleString(localeTag())}</span></div><pre className="mono">{value.content}</pre></section>
    return <div className="empty-state"><p>{pipeline?.stageStatus[stage] === 'skipped' ? t('{label} pulado', { label }) : t('Nenhum {label} salvo', { label })}</p><span className="muted">{pipeline ? t('Consulte a fase e suas evidências em Pipelines.') : t('Esta sessão não tem um pipeline SDD vinculado.')}</span>{onPipelines && <button type="button" className="touch-target secondary-button" onClick={onPipelines}>{t('Abrir Pipelines')}</button>}</div>
  }
  return <div className="artifacts">
    <div className="segmented" role="group" aria-label={t('Tipo de artefato')}>
      {views.map(name => <button key={name} type="button" className="touch-target segment" aria-pressed={view === name} onClick={() => setView(name)}>{t(name)}</button>)}
    </div>
    {sessionId && <button type="button" className="touch-target secondary-button audit-trigger" onClick={() => setExporting(true)}>{t('Exportar auditoria')}</button>}
    {exporting && sessionId && <AuditDialog backend={backend} sessionId={sessionId} onClose={() => setExporting(false)} />}
    {view === 'SPEC' && artifact('spec', 'SPEC')}
    {view === 'Plano' && artifact('plan', t('Plano'))}
    {view === 'Avaliação' && artifact('eval', t('Avaliação'))}
    {view === 'PRs' && artifact('prs', t('Relatório dos PRs'))}
    {view === 'Testes' && (testCalls.length === 0 ? <div className="empty-state"><p>{t('Nenhum comando de teste registrado')}</p><span className="muted">{t('Os testes executados pelo agente nesta sessão aparecerão aqui.')}</span></div> : <div className="artifact-checks">{testCalls.map(call => <section key={call.toolCallId} className="artifact-check"><div className="destination-heading"><h3>{call.name}</h3><span className={`status-chip${call.status === 'completed' ? ' status-ready' : ''}`}>{call.status}</span></div><pre className="mono">{JSON.stringify(call.arguments)}</pre>{(call.result || call.output) && <pre className="mono">{call.result || call.output}</pre>}</section>)}</div>)}
    {view === 'Diff' && (pipeline?.artifacts.code?.content ? <section className="diff-block" aria-label={t('Diff salvo na fase Code')}><h3 className="mono">{t('Code · evidência persistida')}</h3><DiffView diff={pipeline.artifacts.code.content} /></section> : diffs.length === 0
      ? <div className="empty-state"><p>{t('Nenhuma alteração de arquivo')}</p><span className="muted">{t('Os diffs aprovados aparecerão aqui.')}</span></div>
      : diffs.map(item => <section key={item.id} className="diff-block" aria-label={t('Diff de {path}', { path: item.path })}>
        <h3 className="mono">{item.path}</h3>
        <DiffView diff={item.diff} />
      </section>))}
    {view === 'Eventos' && (events.length === 0
      ? <div className="empty-state"><p>{t('Nenhum evento registrado')}</p></div>
      : <table className="event-table">
        <caption className="visually-hidden">{t('Eventos da sessão')}</caption>
        <thead><tr><th scope="col">#</th><th scope="col">{t('Tipo')}</th><th scope="col">{t('Horário')}</th></tr></thead>
        <tbody>{events.map(event => <tr key={event.id}><td className="mono">{event.sequence}</td><td className="mono">{event.type}</td><td className="mono"><time dateTime={event.createdAt}>{formatTime(event.createdAt)}</time></td></tr>)}</tbody>
      </table>)}
  </div>
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleTimeString(localeTag(), { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
