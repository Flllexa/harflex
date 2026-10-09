import { Activity, CheckCircle2, Coins, Database, Timer, Wrench } from 'lucide-react'
import type { AgentEvent } from '../../lib/backend'
import { localeTag, useT } from '../../i18n'

type Usage = { inputTokens?: number; outputTokens?: number }
const started = new Set(['run.started', 'external.run.started'])
const terminal = new Set(['run.completed', 'run.failed', 'run.cancelled', 'run.interrupted', 'external.run.completed', 'external.run.failed', 'external.run.cancelled', 'external.run.interrupted'])

function duration(events: AgentEvent[]): string {
  let began: number | undefined
  let elapsed = 0
  for (const event of events) {
    const time = Date.parse(event.createdAt)
    if (!Number.isFinite(time)) continue
    if (started.has(event.type)) began = time
    if (terminal.has(event.type) && began !== undefined) { elapsed += Math.max(0, time - began); began = undefined }
  }
  if (elapsed === 0) return '—'
  const minutes = Math.floor(elapsed / 60_000)
  return minutes ? `${minutes} min` : `${Math.max(1, Math.round(elapsed / 1000))} s`
}

export function MetricsPane({ events }: { events: AgentEvent[] }) {
  const t = useT()
  const usage = events.filter(event => event.type === 'usage.recorded').map(event => event.data as Usage)
  const inputTokens = usage.reduce((sum, item) => sum + (item.inputTokens ?? 0), 0)
  const outputTokens = usage.reduce((sum, item) => sum + (item.outputTokens ?? 0), 0)
  const tools = events.filter(event => event.type === 'tool.completed').length
  const completed = events.filter(event => event.type === 'run.completed' || event.type === 'external.run.completed').length
  const failed = events.filter(event => event.type === 'run.failed' || event.type === 'external.run.failed').length
  if (!events.length) return <div className="empty-state"><p>{t('Nenhuma métrica registrada')}</p><span className="muted">{t('As métricas surgem dos eventos da sessão, após uma execução.')}</span></div>
  const cards = [
    { label: t('Tokens de entrada'), value: usage.length ? inputTokens.toLocaleString(localeTag()) : '—', Icon: Database },
    { label: t('Tokens de saída'), value: usage.length ? outputTokens.toLocaleString(localeTag()) : '—', Icon: Activity },
    { label: t('Duração concluída'), value: duration(events), Icon: Timer },
    { label: t('Ferramentas concluídas'), value: String(tools), Icon: Wrench },
    { label: t('Execuções concluídas'), value: String(completed), Icon: CheckCircle2 },
    { label: t('Execuções com falha'), value: String(failed), Icon: Activity },
  ]
  return <div className="metrics-pane">
    <div className="destination-heading"><div><h2>{t('Métricas da sessão')}</h2><p className="muted">{t('Valores calculados a partir do journal local.')}</p></div></div>
    <div className="metrics-grid">{cards.map(({ label, value, Icon }) => <div className="metric-tile" key={label}><div className="metric-tile-label"><Icon aria-hidden="true" /><span>{label}</span></div><strong>{value}</strong></div>)}</div>
    <div className="metrics-cost"><Coins aria-hidden="true" /><div><strong>{t('Custo não informado')}</strong><p className="muted">{t('Este backend não forneceu valores monetários verificáveis para a sessão.')}</p></div></div>
  </div>
}
