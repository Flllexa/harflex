import { expect, test } from './locale'

for (const width of [320, 1440]) {
  test(`execution profile shows journal evidence at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=execution')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Custos e execução' }).click()
    await expect(page.getByRole('heading', { name: 'Custos e execução' })).toBeVisible()
    await expect(page.getByText('120', { exact: true })).toBeVisible()
    await expect(page.getByText('Custo não informado')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`execution-${width}.png`), fullPage: true })
  })
}
