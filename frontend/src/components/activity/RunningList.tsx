import { useT } from '../../i18n'
import type { RunningSession } from '../../lib/backend'

const roles = { '': 'Chat', coder: 'Coder', evaluator: 'QA', publisher: 'PRs' } as const

/** Everything running in the project, to follow or open another one without stopping what is on screen. */
export function RunningList({ items, focusedId, onOpen }: { items: RunningSession[]; focusedId?: string; onOpen: (item: RunningSession) => void }) {
  const t = useT()
  return <ul className="activity-running" aria-label={t('Em execução · {count}', { count: items.length })}>
    {items.map(item => <li key={item.session.id}>
      <button type="button" className="touch-target activity-running-item" aria-current={item.session.id === focusedId ? 'true' : undefined} onClick={() => onOpen(item)}>
        <span className="activity-state-indicator" aria-hidden="true" />
        <span className="activity-running-title">{item.session.title || t(roles[item.role])}</span>
        <small className="muted">{t(roles[item.role])}</small>
      </button>
    </li>)}
  </ul>
}
