import { expect, it } from 'vitest'
import { delegationBudgetText } from './delegationBudget'

it('says a subagent has no limit on calls or time when both are zero', () => {
  expect(delegationBudgetText({ promptCount: 4, promptLimit: 0, timeoutSeconds: 0 })).toBe('4 chamadas · sem limite de chamadas ou de tempo')
  expect(delegationBudgetText({ promptCount: 1, promptLimit: 3, timeoutSeconds: 300 })).toBe('1 de 3 chamadas · 5 min por trecho ativo')
})
