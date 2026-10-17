import type { Delegation } from './backend'
import { t } from '../i18n'

export function delegationBudgetText(link: Pick<Delegation, 'promptCount' | 'promptLimit' | 'timeoutSeconds'>): string {
  // Zero means no limit: the subagent works as long as the task needs.
  if (link.promptLimit <= 0 && link.timeoutSeconds <= 0) return t(link.promptCount === 1 ? '{count} chamada · sem limite de chamadas ou de tempo' : '{count} chamadas · sem limite de chamadas ou de tempo', { count: link.promptCount })
  const duration = link.timeoutSeconds % 60 === 0 ? t('{minutes} min', { minutes: link.timeoutSeconds / 60 }) : t('{seconds} s', { seconds: link.timeoutSeconds })
  return t('{count} de {limit} chamadas · {duration} por trecho ativo', { count: link.promptCount, limit: link.promptLimit, duration })
}
