import type { Delegation } from './backend'

export function delegationBudgetText(link: Pick<Delegation, 'promptCount' | 'promptLimit' | 'timeoutSeconds'>): string {
  const duration = link.timeoutSeconds % 60 === 0 ? `${link.timeoutSeconds / 60} min` : `${link.timeoutSeconds} s`
  return `${link.promptCount} de ${link.promptLimit} chamadas · ${duration} por trecho ativo`
}
