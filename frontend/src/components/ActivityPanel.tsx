import { useMemo } from 'react'
import { Square, X } from 'lucide-react'
import { currentLocale, useT } from '../i18n'
import type { AgentEvent, ChatWatcher, RunningSession } from '../lib/backend'
import { ActivityFlowView } from './activity/ActivityFlowView'
import { beforePlan, buildActivityFlow, type Action } from './activity/activityFlow'
import { RunningList } from './activity/RunningList'
import { WatcherList } from './activity/WatcherList'

function waitingForApproval(events: AgentEvent[]) {
  for (let index = events.length - 1; index >= 0; index--) {
    if (events[index].type === 'approval.requested') return true
    if (events[index].type === 'approval.approved' || events[index].type === 'approval.denied') return false
  }
  return false
}

const sources = { harflex: 'Plano do agente', codex: 'Plano do Codex', claude: 'Plano do Claude Code', checklist: 'Plano na resposta' } as const

function current(actions: Action[]) {
  for (let index = actions.length - 1; index >= 0; index--) if (actions[index].status === 'running' || actions[index].status === 'approval') return actions[index]
  return undefined
}

/** The latest request as a live flow: the agent's plan, what it is doing under each step and how it ended. */
export function ActivityPanel({ onClose, events = [], busy = false, running = [], focusedId, onOpenRunning, onStop, watchers = [], watcherNow = new Date(), onStopWatcher, onOpenWatcher }: { onClose: () => void; events?: AgentEvent[]; busy?: boolean; running?: RunningSession[]; focusedId?: string; onOpenRunning?: (item: RunningSession) => void; /** Stops the run the panel follows. */ onStop?: () => void; /** The project's chat watchers, above the activity. */ watchers?: ChatWatcher[]; watcherNow?: Date; onStopWatcher?: (watcherId: string) => void; onOpenWatcher?: (item: ChatWatcher) => void }) {
  const t = useT()
  const journal = useMemo(() => events.filter(item => item.sequence > 0), [events])
  // The flow's labels are written in the language on screen, so a change of language builds it again.
  const language = currentLocale()
  const built = useMemo(() => buildActivityFlow(journal), [journal, language])
  // A reply that left watchers is done only when they come back: until then the flow waits, with the next check.
  const waitingOn = watchers.filter(item => item.sessionId === focusedId && (item.status === 'watching' || !item.delivered))
  const nextCheck = waitingOn.filter(item => item.status === 'watching').reduce<number | undefined>((soonest, item) => {
    const left = Math.max(0, Math.round((new Date(item.nextCheckAt).getTime() - watcherNow.getTime()) / 1000))
    return soonest === undefined ? left : Math.min(soonest, left)
  }, undefined)
  const waitingLabel = waitingOn.length === 0 ? '' : (waitingOn.length === 1 ? t('Aguardando 1 vigia') : t('Aguardando {count} vigias', { count: waitingOn.length })) +
    (nextCheck === undefined ? ' · ' + t('voltando ao chat') : ' · ' + t('próxima em {time}', { time: nextCheck < 120 ? t('{count} s', { count: nextCheck }) : t('{count} min', { count: Math.round(nextCheck / 60) }) }))
  const flow = useMemo(() => waitingLabel && !busy && built.outcome?.status === 'completed' ? { ...built, outcome: { status: 'waiting' as const, label: waitingLabel } } : built, [built, waitingLabel, busy])
  const awaitingApproval = busy && waitingForApproval(journal)
  const live = busy && !awaitingApproval
  const done = flow.plan.filter(step => step.status === 'completed').length
  const step = flow.plan.find(item => item.status === 'in_progress')
  const now = current(flow.actions)
  const empty = !flow.request && !flow.actions.length && !flow.plan.length
  const announcement = busy ? now ? t('Agora: {label}', { label: now.label }) : step ? t('Etapa atual: {text}', { text: step.text }) : '' : flow.outcome ? flow.outcome.label : ''

  return <aside id="activity-panel" className="activity-panel has-flow" aria-label={t('Atividade do trabalho')}>
    <div className="panel-heading"><h2 id="activity-title">{t('Atividade')}</h2>
      <div className="activity-heading-actions">
        {busy && onStop && <button type="button" className="touch-target secondary-button activity-stop" onClick={onStop}><Square aria-hidden="true" />{t('Parar')}</button>}
        <button className="touch-target icon-button" aria-label={t('Fechar atividade')} onClick={onClose}><X aria-hidden="true" /></button>
      </div></div>
    <p className={`muted activity-description${live ? ' activity-description-live' : ''}`} aria-live="polite" aria-atomic="true">
      {live && <span className="activity-state-indicator" aria-hidden="true" />}
      {awaitingApproval ? t('Aguardando aprovação') : live ? t('Em execução · ações atualizadas ao vivo') : flow.outcome?.status === 'waiting' ? t('Aguardando as vigias deste chat') : empty ? t('Nenhuma atividade nesta sessão') : t('Último pedido desta sessão')}
    </p>
    <p className="visually-hidden" aria-live="polite" aria-atomic="true">{announcement}</p>
    {onStopWatcher && onOpenWatcher && <WatcherList items={watchers} now={watcherNow} onStop={onStopWatcher} onOpen={onOpenWatcher} />}
    {onOpenRunning && (running.length > 1 || running.length === 1 && running[0].session.id !== focusedId) && <RunningList items={running} focusedId={focusedId} onOpen={onOpenRunning} />}
    {flow.plan.length > 0 && <div className="activity-progress">
      <div className="activity-progress-row"><strong>{t('{done} de {total} etapas', { done, total: flow.plan.length })}</strong><span>{flow.planSource ? t(sources[flow.planSource]) : ''}</span></div>
      <div className="activity-progress-bar" role="progressbar" aria-label={t('Progresso do plano')} aria-valuemin={0} aria-valuemax={flow.plan.length} aria-valuenow={done}><i style={{ width: `${Math.round(done / flow.plan.length * 100)}%` }} /></div>
      {flow.explanation && <p className="activity-explanation">{flow.explanation}</p>}
    </div>}
    {empty ? <div className="activity-note"><p>{live ? t('Aguardando os primeiros eventos do agente.') : t('Inicie um trabalho para acompanhar a execução.')}</p></div>
      : <>
        <ActivityFlowView flow={flow} live={live} />
        <ol className="visually-hidden" aria-label={t('Plano de execução')}>
          {flow.actions.filter(item => item.step === beforePlan && item.label).map(item => <li key={item.id}>{item.label}{item.note ? ` · ${item.note}` : ''}</li>)}
          {flow.plan.map((item, index) => <li key={index}>
            {t('Etapa {number}: {text} ({status})', { number: index + 1, text: item.text, status: t(item.status === 'completed' ? 'feita' : item.status === 'in_progress' ? 'em andamento' : 'pendente') })}
            <ul>{flow.actions.filter(action => action.step === index && action.label).map(action => <li key={action.id}>{action.label}{action.note ? ` · ${action.note}` : ''}</li>)}</ul>
          </li>)}
          {flow.outcome && <li>{flow.outcome.label}</li>}
        </ol>
      </>}
  </aside>
}
