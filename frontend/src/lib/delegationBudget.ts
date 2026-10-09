import type { Delegation } from './backend'
import { t } from '../i18n'

export function delegationBudgetText(link: Pick<Delegation, 'promptCount' | 'promptLimit' | 'timeoutSeconds'>): string {
  const duration = link.timeoutSeconds % 60 === 0 ? t('{minutes} min', { minutes: link.timeoutSeconds / 60 }) : t('{seconds} s', { seconds: link.timeoutSeconds })
  return t('{count} de {limit} chamadas · {duration} por trecho ativo', { count: link.promptCount, limit: link.promptLimit, duration })
}
