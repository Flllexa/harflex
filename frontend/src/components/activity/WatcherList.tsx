import { Eye, MessageSquare, Square } from 'lucide-react'
import { localeTag, useT } from '../../i18n'
import type { ChatWatcher } from '../../lib/backend'

const active = (item: ChatWatcher) => item.status === 'watching' || !item.delivered

function seconds(from: Date, to: string) { return Math.max(0, Math.round((new Date(to).getTime() - from.getTime()) / 1000)) }

function span(t: ReturnType<typeof useT>, total: number) {
  if (total < 120) return t('{count} s', { count: total })
  if (total < 3600) return t('{count} min', { count: Math.round(total / 60) })
  return t('{count} h', { count: Math.round(total / 360) / 10 })
}

function lastLine(output: string) {
  const lines = output.split('\n').map(line => line.trim()).filter(Boolean)
  return lines[lines.length - 1] ?? ''
}

/** The watchers chat agents left in this project: what each waits for, when it checks next, and how it ended. */
export function WatcherList({ items, now, onStop, onOpen }: { items: ChatWatcher[]; now: Date; onStop: (watcherId: string) => void; onOpen: (item: ChatWatcher) => void }) {
  const t = useT()
  if (items.length === 0) return null
  const running = items.filter(active).length
  const status = (item: ChatWatcher) => {
    if (item.status === 'watching') {
      if (!item.command) return t('Volta ao chat em {time}', { time: span(t, seconds(now, item.nextCheckAt)) })
      return t('Checando a cada {every} · próxima em {next} · {checks} checagens', { every: span(t, item.intervalSeconds), next: span(t, seconds(now, item.nextCheckAt)), checks: item.checks })
    }
    if (!item.delivered && item.status !== 'stopped') return t('Terminou; esperando o chat ficar livre')
    switch (item.status) {
      case 'fired': return item.endedAt ? t('Voltou ao chat · {time}', { time: new Date(item.endedAt).toLocaleTimeString(localeTag(), { hour: '2-digit', minute: '2-digit' }) }) : t('Voltou ao chat')
      case 'expired': return t('Prazo acabou sem a condição; o chat foi avisado')
      case 'failed': return t('O comando não pôde rodar; o chat foi avisado')
      default: return t('Parada por você')
    }
  }
  return <section className="activity-watchers">
    <ul aria-label={running === 1 ? t('Vigias · 1 ativa') : t('Vigias · {count} ativas', { count: running })}>
      {items.map(item => <li key={item.id} aria-label={item.title} className={`activity-watcher is-${active(item) ? 'active' : 'ended'}`}>
        <div className="activity-watcher-head">
          <span className="activity-watcher-icon" aria-hidden="true">{item.status === 'watching' ? <span className="activity-state-indicator" /> : <Eye />}</span>
          <strong>{item.title}</strong>
          {active(item) && <button type="button" className="icon-button activity-watcher-stop" aria-label={t('Parar vigia {title}', { title: item.title })} title={t('Parar vigia')} onClick={() => onStop(item.id)}><Square aria-hidden="true" /></button>}
        </div>
        <p className="activity-watcher-status muted">{status(item)}</p>
        {item.lastOutput && <code className="activity-watcher-output">{lastLine(item.lastOutput)}</code>}
        <button type="button" className="text-button activity-watcher-chat" onClick={() => onOpen(item)} aria-label={t('Abrir chat')}><MessageSquare aria-hidden="true" />{item.chatTitle || t('Abrir chat')}</button>
      </li>)}
    </ul>
  </section>
}
