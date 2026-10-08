import { expect, test } from '@playwright/test'
import { undersizedTargets } from './targets'

for (const width of [320, 768, 900, 1024, 1440]) {
  test(`CLI model and effort selector remains usable at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=cli-models')
    await page.getByLabel('Caminho da pasta').fill('/Users/dev/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    await page.getByLabel('Motivo para pular SDD nesta sessão').fill('Pesquisa local')
    await expect(page.getByRole('button', { name: 'Iniciar sessão livre' })).toBeDisabled()
    await page.getByRole('button', { name: 'Consultar modelos' }).click()
    const model = page.getByRole('combobox', { name: 'Modelo da sessão' })
    await expect(model).toBeVisible()
    await model.click()
    await model.fill('nome longo')
    await page.getByRole('option', { name: /Modelo dinâmico com nome longo/ }).click()
    const effort = page.getByTestId('picker-session-effort').getByRole('button')
    await effort.click()
    await page.getByRole('option', { name: 'high' }).click()
    await expect(page.getByRole('button', { name: 'Iniciar sessão livre' })).toBeEnabled()
    await page.screenshot({ path: testInfo.outputPath(`cli-model-${width}.png`), fullPage: true })
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    expect(await undersizedTargets(page)).toEqual([])
  })
}
