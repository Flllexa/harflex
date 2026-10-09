import { expect, test } from './locale'

for (const width of [320, 1440]) {
  test(`local log index filters and exports audit at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=logs')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Logs' }).click()
    await expect(page.getByRole('heading', { name: 'Logs e auditoria' })).toBeVisible()
    await expect(page.getByRole('table', { name: 'Índice de eventos persistidos' }).locator('tbody tr')).toHaveCount(2)
    await page.getByTestId('picker-logs-event-type').getByRole('combobox').fill('run.failed')
    await page.screenshot({ path: info.outputPath(`logs-picker-open-${width}.png`), fullPage: true })
    await page.getByRole('option', { name: 'run.failed', exact: true }).click()
    await expect(page.getByRole('table', { name: 'Índice de eventos persistidos' }).locator('tbody tr')).toHaveCount(1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    if (width === 320) await expect(page.getByRole('button', { name: 'Exportar auditoria da sessão session-1' })).toBeInViewport()
    await page.screenshot({ path: info.outputPath(`logs-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: 'Exportar auditoria da sessão session-1' }).click()
    await expect(page.getByRole('dialog', { name: 'Exportar auditoria' })).toBeVisible()
  })
}
