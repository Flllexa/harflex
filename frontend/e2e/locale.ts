import { test as base } from '@playwright/test'

// The specs were written against the Portuguese screens. Each page opens in pt-BR unless a spec says otherwise.
export const test = base.extend({
  page: async ({ page }, use) => {
    await page.addInitScript(() => { try { localStorage.setItem('harflex:locale', 'pt-BR') } catch { /* blocked storage */ } })
    await use(page)
  },
})
export { expect } from '@playwright/test'
export type { Page, Locator } from '@playwright/test'
